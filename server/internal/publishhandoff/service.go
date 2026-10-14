// Service:publish_handoffs / publish_handoff_events 两表的唯一写入方。
//
// 并发纪律:modernc/sqlite 单写连接;先落行再改状态,状态迁移一律先过
// CheckTransition(结构+证据双门禁)。本文件零网络调用、零生成调用、零
// 奖励调用——交接失败不触发任何付费生成是结构性保证,不是运行时承诺。
package publishhandoff

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
)

// Options configures the service. Now is injectable for tests.
type Options struct {
	Now     func() time.Time
	MediaTTL time.Duration // 受控媒体 URL 有效期(contentpack 上限 1h)
	// MediaSecret / MediaBaseURL feed contentpack.MintHandoff;未配置密钥时
	// 媒体铸造失败 → 交接 failed(诚实失败,不伪造可用)。
	MediaSecret  string
	MediaBaseURL string
	// ReceiptVerifier verifies official callbacks. nil/未配置 = 一律拒绝
	// (NOT_VERIFIED 残差)。
	ReceiptVerifier ReceiptVerifier
	// AppRef is THIS deployment's official platform app identity; receipts
	// naming another app are refused (错 app 拒绝).
	AppRef string
	// OpenVerifier proves editor_opened. nil = 没有可证实来源,一律拒绝
	// (窗口失焦/自报永远不够)。
	OpenVerifier OpenVerifier
	// H5EntryBase / MiniappEntryBase are the admitted-route entry bases.
	H5EntryBase    string
	MiniappEntryBase string
}

// OpenVerifier proves the editor actually opened on the platform. 实现必须
// 基于可验证证据(平台侧签名/受信回执),绝不接受客户端事件。
type OpenVerifier interface {
	VerifyEditorOpen(proof string, h Handoff) error
}

// Service owns the handoff tables.
type Service struct {
	db  *sql.DB
	opt Options
}

// New builds a Service over an opened database.
func New(database *sql.DB, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.MediaTTL <= 0 {
		opts.MediaTTL = contentpack.HandoffTTLDefault
	}
	return &Service{db: database, opt: opts}
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("publishhandoff: entropy unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

func (s *Service) now() time.Time { return s.opt.Now() }

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Sentinel errors (stable for handlers/tests).
var (
	ErrNotFound             = errors.New("publishhandoff: not found")
	ErrBadInput             = errors.New("publishhandoff: bad input")
	ErrAttemptNotFrozen     = errors.New("publishhandoff: attempt is not frozen at handoff")
	ErrConfirmationMismatch = errors.New("publishhandoff: confirmed content version does not match the frozen snapshot")
	ErrEvidenceRequired     = errors.New("publishhandoff: verifiable evidence required")
	ErrQueryRequired        = errors.New("publishhandoff: query required before any resend")
	ErrMediaPreparation     = errors.New("publishhandoff: media preparation failed")
	ErrInvalidState         = errors.New("publishhandoff: handoff state does not allow this action")
)

// HandoffRefusedError is the Gate refusal (not_admitted / closed / expired /
// unknown-without-fallback …). 拒绝原因与缺项如实透出给顾客页。
type HandoffRefusedError struct {
	Reason     string
	ReasonText string
	Notice     string
	Missing    []string
}

func (e *HandoffRefusedError) Error() string {
	return "publishhandoff: handoff refused: " + e.Reason
}

// ---- input / result types -------------------------------------------------

// StartInput is one confirmed customer handoff request. Text/Media are the
// resolved snapshot of the ADOPTED content version (httpapi resolves them
// after the contentpack distributable check); ConfirmedContentVersion must
// equal ContentFingerprint(Text, Media) — 换稿必须重新确认。
type StartInput struct {
	TenantID   string
	StoreID    string
	CampaignID string
	LinkCode   string

	ParticipationID string
	AttemptID       string // 3546 冻结语义(handed_off)的尝试
	VisitorID       string

	Platform  string
	EntryMode string
	Scenario  string

	IdempotencyKey          string
	ConfirmedContentVersion string
	ContentPackVersionID    string

	// H5Base / MiniappBase are the per-request entry bases (deployment config
	// may also carry them via Options; the input wins when set).
	H5Base      string
	MiniappBase string

	Text  TextInput
	Media []MediaItem
}

// MediaRef is one controlled download URL served to the customer page.
type MediaRef struct {
	Kind        string `json:"kind"`
	AssetID     string `json:"asset_id"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	ExpiresAt   string `json:"expires_at"`
}

// StartResult reports the created-or-replayed handoff with its current plan
// and fresh media refs.
type StartResult struct {
	Handoff Handoff
	Plan    Plan
	Media   []MediaRef
	Created bool
}

// Handoff is the safe-to-expose view of a handoff row.
type Handoff struct {
	ID              string
	TenantID        string
	StoreID         string
	ParticipationID string
	AttemptID       string
	Platform        string
	EntryMode       string
	Scenario        string
	Status          State
	EvidenceSource  EvidenceSource
	ContentVersion  string
	ContentPackVersionID string
	QueryCount      int
	TimedOut        bool
	CancelReason    string
	CreatedAt       string
	UpdatedAt       string
}

// receiptBinding is what official receipts must match for this handoff.
func (h Handoff) receiptBinding(appRef string) ReceiptBinding {
	return ReceiptBinding{
		AppRef: appRef, TenantID: h.TenantID,
		ParticipationID: h.ParticipationID, AttemptID: h.AttemptID,
		ContentVersion: h.ContentVersion,
	}
}

// ContentFingerprint binds the customer's confirmation to the exact snapshot
// (text + media bytes identity) the handoff will serve. 换稿=指纹变化=
// 必须重新确认。
func ContentFingerprint(text TextInput, media []MediaItem) string {
	sorted := append([]MediaItem(nil), media...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].AssetID < sorted[j].AssetID
	})
	var b strings.Builder
	b.WriteString("v1\x1f")
	b.WriteString(text.Title)
	b.WriteByte(0x1f)
	b.WriteString(text.Body)
	b.WriteByte(0x1f)
	b.WriteString(strings.Join(text.Topics, "\x1e"))
	b.WriteByte(0x1f)
	b.WriteString(text.StoreName)
	for _, m := range sorted {
		b.WriteByte(0x1f)
		b.WriteString(m.Kind)
		b.WriteByte(':')
		b.WriteString(m.AssetID)
		b.WriteByte(':')
		b.WriteString(m.SHA256)
		b.WriteByte(':')
		b.WriteString(m.ContentType)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ---- staff fact kinds -------------------------------------------------------

// The two HUMAN facts (票面: 人工见到提交、人工核对作品分别落库;官方确认是
// 第三种事实,走 ApplyReceipt,永不由人工写出).
const (
	FactStaffSawSubmit    = "staff_saw_submit"
	FactStaffVerifiedWork = "staff_verified_work"
)

// StaffActor identifies the observing staff member (already authorized by
// the HTTP layer's session + store scope; the domain repeats the tenant check).
type StaffActor struct {
	ID   string
	Name string
}

// ---- events -----------------------------------------------------------------

func (s *Service) appendEvent(tenantID, handoffID, kind, actor string, ev EvidenceSource, receiptID string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("publishhandoff: event payload: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, actor, evidence_source, receipt_id, payload, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		newID("phe_"), tenantID, handoffID, kind, actor, string(ev), receiptID, string(raw), rfc3339(s.now()))
	if err != nil {
		return fmt.Errorf("publishhandoff: append event: %w", err)
	}
	return nil
}

// setState moves the row through CheckTransition and appends BOTH the
// state_changed event and (optionally) a fact event.
func (s *Service) setState(h Handoff, to State, ev EvidenceSource) (Handoff, error) {
	if err := CheckTransition(h.Status, to, ev); err != nil {
		return h, err
	}
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET status=?, evidence_source=?, updated_at=?
		WHERE id=? AND tenant_id=?`,
		string(to), string(ev), rfc3339(s.now()), h.ID, h.TenantID); err != nil {
		return h, fmt.Errorf("publishhandoff: set state: %w", err)
	}
	from := h.Status
	h.Status, h.EvidenceSource, h.UpdatedAt = to, ev, rfc3339(s.now())
	if err := s.appendEvent(h.TenantID, h.ID, "state_changed", "", ev, "", map[string]any{
		"from": string(from), "to": string(to),
	}); err != nil {
		return h, err
	}
	return h, nil
}

// ---- start --------------------------------------------------------------------

// Start opens (or replays) one handoff: 闸→冻结核验→指纹确认→落行→媒体铸造→
// ready。Gate 拒绝路径零落行(不产生任何交接痕迹,只回诚实原因)。
func (s *Service) Start(_ context.Context, in StartInput, rec channeladmission.Record) (StartResult, error) {
	switch {
	case strings.TrimSpace(in.TenantID) == "", strings.TrimSpace(in.ParticipationID) == "",
		strings.TrimSpace(in.AttemptID) == "", strings.TrimSpace(in.VisitorID) == "",
		strings.TrimSpace(in.IdempotencyKey) == "", strings.TrimSpace(in.Platform) == "":
		return StartResult{}, ErrBadInput
	}
	// 1) Gate(服务端单点强制;拒绝=零写路径)。
	plan := PlanRoute(s.planFields(in), rec)
	if plan.Kind == PlanRefused {
		return StartResult{}, &HandoffRefusedError{
			Reason: plan.Reason, ReasonText: plan.ReasonText, Notice: plan.Notice, Missing: plan.Missing,
		}
	}
	// 2) 幂等:同键回原行(带新鲜媒体 URL)。
	if existing, ok, err := s.byKey(in.TenantID, in.IdempotencyKey); err != nil {
		return StartResult{}, err
	} else if ok {
		media, merr := s.mintMedia(existing)
		if merr != nil {
			return StartResult{}, merr
		}
		return StartResult{Handoff: existing, Plan: plan, Media: media, Created: false}, nil
	}
	// 3) attempt 必须已按 3546 语义冻结。
	if err := s.requireFrozenAttempt(in); err != nil {
		return StartResult{}, err
	}
	// 4) 内容指纹:顾客确认的版本必须就是本次要交出的快照。
	cv := ContentFingerprint(in.Text, in.Media)
	if cv != in.ConfirmedContentVersion {
		return StartResult{}, ErrConfirmationMismatch
	}
	// 5) 落行:handoff_preparing(证据=顾客动作)。
	textJSON, err := json.Marshal(in.Text)
	if err != nil {
		return StartResult{}, err
	}
	mediaJSON, err := json.Marshal(in.Media)
	if err != nil {
		return StartResult{}, err
	}
	h := Handoff{
		ID: newID("ph_"), TenantID: in.TenantID, ParticipationID: in.ParticipationID,
		AttemptID: in.AttemptID, Platform: in.Platform, EntryMode: in.EntryMode,
		Scenario: in.Scenario, Status: StatePreviewed, EvidenceSource: EvidenceCustomerAction,
		ContentVersion: cv, CreatedAt: rfc3339(s.now()), UpdatedAt: rfc3339(s.now()),
	}
	if _, err := s.db.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, campaign_id, link_code, participation_id, attempt_id,
		 platform, entry_mode, scenario, idempotency_key, status, evidence_source,
		 content_version, content_pack_version_id, media_json, text_json, visitor_id,
		 created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, in.TenantID, in.StoreID, in.CampaignID, in.LinkCode, in.ParticipationID, in.AttemptID,
		in.Platform, in.EntryMode, in.Scenario, in.IdempotencyKey, string(StatePreviewed),
		string(EvidenceCustomerAction), cv, in.ContentPackVersionID,
		string(mediaJSON), string(textJSON), in.VisitorID, h.CreatedAt, h.UpdatedAt); err != nil {
		return StartResult{}, fmt.Errorf("publishhandoff: insert: %w", err)
	}
	preparing, err := s.setState(h, StateHandoffPreparing, EvidenceCustomerAction)
	if err != nil {
		return StartResult{}, err
	}
	// 6) 媒体铸造(受控 URL;失败=诚实 failed,零付费生成)。
	media, err := s.mintMedia(preparing)
	if err != nil {
		failed, ferr := s.setState(preparing, StateFailed, EvidenceSystemAdapter)
		if ferr != nil {
			return StartResult{}, errors.Join(err, ferr)
		}
		_ = failed
		return StartResult{}, fmt.Errorf("%w: %v", ErrMediaPreparation, err)
	}
	// 7) ready(证据=系统适配器)。
	ready, err := s.setState(preparing, StateHandoffReady, EvidenceSystemAdapter)
	if err != nil {
		return StartResult{}, err
	}
	// 最终计划带媒体 URL(manual guide / h5 deep link 引用受控链接)。
	final := PlanRoute(s.planFieldsWithMedia(in, media), rec)
	return StartResult{Handoff: ready, Plan: final, Media: media, Created: true}, nil
}

func (s *Service) planFields(in StartInput) PlanInput {
	h5, mini := in.H5Base, in.MiniappBase
	if h5 == "" {
		h5 = s.opt.H5EntryBase
	}
	if mini == "" {
		mini = s.opt.MiniappEntryBase
	}
	return PlanInput{
		TenantID: in.TenantID, Platform: in.Platform, EntryMode: in.EntryMode,
		Scenario: in.Scenario, Now: s.now(), Text: in.Text, Media: in.Media,
		DeepLinkBase: h5, MiniappBase: mini,
	}
}

func (s *Service) planFieldsWithMedia(in StartInput, media []MediaRef) PlanInput {
	p := s.planFields(in)
	urls := map[string]string{}
	for _, m := range media {
		urls[m.AssetID] = m.URL
	}
	p.MediaURLs = urls
	return p
}

func (s *Service) requireFrozenAttempt(in StartInput) error {
	var status string
	err := s.db.QueryRow(`SELECT status FROM participation_attempts
		WHERE id=? AND tenant_id=? AND participation_id=?`,
		in.AttemptID, in.TenantID, in.ParticipationID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptNotFrozen
	}
	if err != nil {
		return fmt.Errorf("publishhandoff: attempt lookup: %w", err)
	}
	// handed_off = 内容与规则已冻结(3546 BeginHandoff 语义)。
	if status != "handed_off" {
		return ErrAttemptNotFrozen
	}
	return nil
}

func (s *Service) byKey(tenantID, idem string) (Handoff, bool, error) {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs
		WHERE tenant_id=? AND idempotency_key=?`, tenantID, idem))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, false, nil
	}
	if err != nil {
		return Handoff{}, false, err
	}
	return h, true, nil
}

const handoffCols = `id, tenant_id, store_id, participation_id, attempt_id, platform, entry_mode,
	scenario, status, evidence_source, content_version, content_pack_version_id,
	query_count, timed_out, cancel_reason, created_at, updated_at`

func (s *Service) scan(row rowScanner) (Handoff, error) {
	var h Handoff
	var status, ev string
	var timedOut int
	err := row.Scan(&h.ID, &h.TenantID, &h.StoreID, &h.ParticipationID, &h.AttemptID, &h.Platform,
		&h.EntryMode, &h.Scenario, &status, &ev, &h.ContentVersion, &h.ContentPackVersionID,
		&h.QueryCount, &timedOut, &h.CancelReason, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return Handoff{}, err
	}
	h.Status, h.EvidenceSource, h.TimedOut = State(status), EvidenceSource(ev), timedOut == 1
	return h, nil
}

// GetScoped reads one handoff inside a tenant (staff/callback callers; the
// HTTP layer authorizes scope before this). Unknown ids answer ErrNotFound.
func (s *Service) GetScoped(_ context.Context, id, tenantID string) (Handoff, error) {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	return h, err
}

// LoadForCallback resolves a handoff by its opaque id alone (platform
// callbacks arrive without a tenant context). The id is 128-bit random, and
// every later check (验签+五绑定,其中 tenant 绑定对比行内真值)依然强制——
// 回执声称的租户与行不符即拒。Unknown ids answer ErrNotFound.
func (s *Service) LoadForCallback(_ context.Context, id string) (Handoff, error) {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	return h, err
}

// ApplyReceiptByID is the callback entry: resolve by id, then run the same
// verify→bind→dedupe→transition pipeline with the ROW's tenant as truth.
func (s *Service) ApplyReceiptByID(ctx context.Context, handoffID string, wire ReceiptWire) (Handoff, error) {
	h, err := s.LoadForCallback(ctx, handoffID)
	if err != nil {
		return Handoff{}, err
	}
	return s.ApplyReceipt(ctx, h.TenantID, handoffID, wire)
}

// SnapshotOf returns the frozen text + media items of a handoff (plan/media
// re-rendering; never re-reads the pack so 换稿不会静默漏进旧交接).
func (s *Service) SnapshotOf(_ context.Context, id, tenantID string) (TextInput, []MediaItem, error) {
	var textJSON, mediaJSON string
	err := s.db.QueryRow(`SELECT text_json, media_json FROM publish_handoffs WHERE id=? AND tenant_id=?`, id, tenantID).Scan(&textJSON, &mediaJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return TextInput{}, nil, ErrNotFound
	}
	if err != nil {
		return TextInput{}, nil, fmt.Errorf("publishhandoff: snapshot: %w", err)
	}
	var text TextInput
	var media []MediaItem
	if err := json.Unmarshal([]byte(textJSON), &text); err != nil {
		return TextInput{}, nil, fmt.Errorf("publishhandoff: text snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(mediaJSON), &media); err != nil {
		return TextInput{}, nil, fmt.Errorf("publishhandoff: media snapshot: %w", err)
	}
	return text, media, nil
}

type rowScanner interface{ Scan(dest ...any) error }

// ---- read / media ----------------------------------------------------------------

// GetForVisitor reads the caller's OWN handoff (visitor binding is the access
// secret). Any mismatch answers ErrNotFound (防枚举).
func (s *Service) GetForVisitor(_ context.Context, id, visitorID string) (Handoff, error) {
	if visitorID == "" {
		return Handoff{}, ErrNotFound
	}
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=? AND visitor_id=?`, id, visitorID))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	return h, err
}

// MediaForVisitor re-mints the controlled download URLs from the frozen
// snapshot (弱网重试:同一交接、状态不变、不新建行).
func (s *Service) MediaForVisitor(ctx context.Context, id, visitorID string) ([]MediaRef, error) {
	h, err := s.GetForVisitor(ctx, id, visitorID)
	if err != nil {
		return nil, err
	}
	return s.mintMedia(h)
}

func _context() context.Context { return context.Background() }

// mintMedia mints controlled URLs for a handoff's stored snapshot.
func (s *Service) mintMedia(h Handoff) ([]MediaRef, error) {
	mediaJSON := ""
	var versionID string
	if err := s.db.QueryRow(`SELECT media_json, content_pack_version_id FROM publish_handoffs WHERE id=?`, h.ID).Scan(&mediaJSON, &versionID); err != nil {
		return nil, fmt.Errorf("publishhandoff: media snapshot: %w", err)
	}
	hh := h
	hh.ContentPackVersionID = versionID
	var items []MediaItem
	if err := json.Unmarshal([]byte(mediaJSON), &items); err != nil {
		return nil, fmt.Errorf("publishhandoff: media snapshot: %w", err)
	}
	return s.mintFromItems(hh, items)
}

func (s *Service) mintFromItems(h Handoff, items []MediaItem) ([]MediaRef, error) {
	if strings.TrimSpace(s.opt.MediaSecret) == "" || strings.TrimSpace(s.opt.MediaBaseURL) == "" {
		return nil, errors.New("media handoff signing is not configured")
	}
	versionID := h.ContentPackVersionID
	if versionID == "" {
		versionID = h.ID
	}
	out := make([]MediaRef, 0, len(items))
	for _, m := range items {
		claim, err := contentpack.NewHandoffClaim(versionID, m.AssetID, m.SHA256, m.ContentType, "attachment", m.Filename, s.now(), s.opt.MediaTTL)
		if err != nil {
			return nil, err
		}
		token, err := contentpack.MintHandoff(s.opt.MediaSecret, claim)
		if err != nil {
			return nil, err
		}
		base := strings.TrimRight(s.opt.MediaBaseURL, "/")
		out = append(out, MediaRef{
			Kind: m.Kind, AssetID: m.AssetID, URL: base + "/" + token, SHA256: m.SHA256,
			ContentType: m.ContentType, Filename: m.Filename, ExpiresAt: rfc3339(claim.ExpiresAt),
		})
	}
	return out, nil
}

// ---- customer actions --------------------------------------------------------------

// CustomerDone records "我已完成" as a verification REQUEST only: 状态不变、
// 不触发奖励、不是任何一种事实。
func (s *Service) CustomerDone(_ context.Context, id, visitorID string) (Handoff, error) {
	h, err := s.GetForVisitor(_context(), id, visitorID)
	if err != nil {
		return Handoff{}, err
	}
	if !IsActive(h.Status) {
		return h, fmt.Errorf("%w: %s is terminal", ErrInvalidState, h.Status)
	}
	if err := s.appendEvent(h.TenantID, h.ID, "customer_done_requested", "customer", EvidenceCustomerAction, "", nil); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

// Cancel ends the handoff by its owner. Terminal states never resurrect.
func (s *Service) Cancel(_ context.Context, id, visitorID, reason string) (Handoff, error) {
	h, err := s.GetForVisitor(_context(), id, visitorID)
	if err != nil {
		return Handoff{}, err
	}
	next, err := s.setState(h, StateCanceled, EvidenceCustomerAction)
	if err != nil {
		return Handoff{}, err
	}
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET cancel_reason=?, updated_at=? WHERE id=? AND tenant_id=?`,
		strings.TrimSpace(reason), rfc3339(s.now()), h.ID, h.TenantID); err != nil {
		return Handoff{}, fmt.Errorf("publishhandoff: cancel reason: %w", err)
	}
	next.CancelReason = strings.TrimSpace(reason)
	if err := s.appendEvent(h.TenantID, h.ID, "canceled", "customer", EvidenceCustomerAction, "", map[string]any{
		"reason": strings.TrimSpace(reason),
	}); err != nil {
		return Handoff{}, err
	}
	return next, nil
}

// MarkEditorOpened moves handoff_ready → editor_opened ONLY on verifiable
// proof. Without a configured OpenVerifier (or with a bad proof) the request
// is refused: 失焦/点击/自报不是证据。
func (s *Service) MarkEditorOpened(_ context.Context, id, visitorID, proof string) (Handoff, error) {
	h, err := s.GetForVisitor(_context(), id, visitorID)
	if err != nil {
		return Handoff{}, err
	}
	if s.opt.OpenVerifier == nil {
		return h, ErrEvidenceRequired
	}
	if err := s.opt.OpenVerifier.VerifyEditorOpen(proof, h); err != nil {
		return h, fmt.Errorf("%w: %v", ErrEvidenceRequired, err)
	}
	return s.setState(h, StateEditorOpened, EvidenceVerifierProof)
}

// MarkSubmitting records that the adapter handed the publish request to the
// platform surface (automated paths only). No outbound call happens here.
func (s *Service) MarkSubmitting(_ context.Context, id, visitorID string) (Handoff, error) {
	h, err := s.GetForVisitor(_context(), id, visitorID)
	if err != nil {
		return Handoff{}, err
	}
	return s.setState(h, StateSubmitting, EvidenceSystemAdapter)
}

// ---- timeout / query (先回查,不盲目重投) ---------------------------------------------

// MarkTimedOut moves an in-flight submitting handoff to unknown without
// sending anything again.
func (s *Service) MarkTimedOut(_ context.Context, id string) (Handoff, error) {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	if err != nil {
		return Handoff{}, err
	}
	next, err := s.setState(h, StateUnknown, EvidenceSystemTimeout)
	if err != nil {
		return h, err
	}
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET timed_out=1, updated_at=? WHERE id=? AND tenant_id=?`,
		rfc3339(s.now()), h.ID, h.TenantID); err != nil {
		return Handoff{}, fmt.Errorf("publishhandoff: mark timeout: %w", err)
	}
	next.TimedOut = true
	return next, nil
}

// Query re-checks the SAME handoff: counts the attempt, sends nothing,
// changes nothing without an official receipt.
func (s *Service) Query(_ context.Context, id, visitorID string) (Handoff, error) {
	h, err := s.GetForVisitor(_context(), id, visitorID)
	if err != nil {
		return Handoff{}, err
	}
	if h.Status != StateSubmitting && h.Status != StateUnknown {
		return h, fmt.Errorf("%w: %s has nothing in flight", ErrInvalidState, h.Status)
	}
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET query_count=query_count+1, updated_at=? WHERE id=? AND tenant_id=?`,
		rfc3339(s.now()), h.ID, h.TenantID); err != nil {
		return Handoff{}, fmt.Errorf("publishhandoff: query: %w", err)
	}
	h.QueryCount++
	return h, nil
}

// Resend refuses to place a second outbound request after a timeout: the
// caller must Query first (先回查). 本部署亦无任何自动外发通道。
func (s *Service) Resend(_ context.Context, id string) error {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if h.Status == StateUnknown || h.TimedOut {
		return ErrQueryRequired
	}
	return fmt.Errorf("%w: no automated outbound exists for %s", ErrInvalidState, h.Status)
}

// ---- staff facts -----------------------------------------------------------------

// RecordStaffEvent writes one HUMAN fact (见到提交 / 核对作品) and moves an
// active handoff to awaiting_review. 人工永不转换成官方:本方法结构上到不了
// platform_confirmed(状态机的 requiredEvidence 不含 staff_observation)。
func (s *Service) RecordStaffEvent(_ context.Context, tenantID, handoffID string, who StaffActor, fact, note string) (Handoff, error) {
	if fact != FactStaffSawSubmit && fact != FactStaffVerifiedWork {
		return Handoff{}, ErrBadInput
	}
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=? AND tenant_id=?`, handoffID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	if err != nil {
		return Handoff{}, err
	}
	if !IsActive(h.Status) {
		return h, fmt.Errorf("%w: %s is terminal", ErrInvalidState, h.Status)
	}
	// 未交接完成的 handoff 没有可核验对象(previewed/preparing 拒绝)。
	if h.Status == StatePreviewed || h.Status == StateHandoffPreparing {
		return h, fmt.Errorf("%w: %s has nothing to verify yet", ErrInvalidState, h.Status)
	}
	if h.Status != StateAwaitingReview {
		if _, err := s.setState(h, StateAwaitingReview, EvidenceStaffObservation); err != nil {
			return h, err
		}
		h.Status = StateAwaitingReview
	}
	if err := s.appendEvent(tenantID, handoffID, fact, who.Name, EvidenceStaffObservation, "", map[string]any{
		"staff_id": who.ID, "note": strings.TrimSpace(note),
	}); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

// ---- official receipts -------------------------------------------------------------

// ApplyReceipt verifies + binds + dedupes one official callback:
//  1. 验签(未配置/错签名=拒绝且不留 receipt_id——防伪造者抢占真回执 ID);
//  2. 五绑定(app/tenant/participation/attempt/content_version);
//  3. 重放:同 receipt_id 已官方确认 → 幂等回原状态,不新增事件;
//  4. 结果语义:非 published 照录不确认;
//  5. 乱序:状态机不允许的迁移 → receipt_rejected + 先回查,状态不盲进。
func (s *Service) ApplyReceipt(_ context.Context, tenantID, handoffID string, wire ReceiptWire) (Handoff, error) {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=? AND tenant_id=?`, handoffID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	if err != nil {
		return Handoff{}, err
	}

	reject := func(reason string, receiptID string, err error) (Handoff, error) {
		_ = s.appendEvent(tenantID, handoffID, "receipt_rejected", "", EvidenceOfficialReceipt, receiptID, map[string]any{
			"reason": reason,
		})
		return h, err
	}

	// 1) 验签。失败不留 receipt_id(伪造者不得抢占真 ID)。
	if s.opt.ReceiptVerifier == nil {
		return reject("verifier_not_configured", "", ErrReceiptUnverified)
	}
	r, verr := s.opt.ReceiptVerifier.Verify(wire)
	if verr != nil {
		return reject("unverified", "", fmt.Errorf("%w: %v", ErrReceiptUnverified, verr))
	}
	// 2) 五绑定。
	if err := BindReceipt(r, h.receiptBinding(s.opt.AppRef)); err != nil {
		return reject("binding_mismatch", r.ReceiptID, err)
	}
	// 3) 重放(仅认已确认的官方回执;被拒回执允许在状态推进后重新评估=回查)。
	var seen int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM publish_handoff_events
		WHERE tenant_id=? AND handoff_id=? AND receipt_id=? AND kind='official_confirmed'`,
		tenantID, handoffID, r.ReceiptID).Scan(&seen); err != nil {
		return Handoff{}, fmt.Errorf("publishhandoff: replay check: %w", err)
	}
	if seen > 0 {
		return h, nil // 幂等重放:回原状态,不新增事件
	}
	if terminal(h.Status) {
		return reject("terminal_state", r.ReceiptID, fmt.Errorf("%w: %s is terminal", ErrInvalidState, h.Status))
	}
	// 4) 结果语义:照录不确认。
	if !ReceiptConfirms(r.Outcome) {
		return reject("outcome_not_confirming", r.ReceiptID, fmt.Errorf("publishhandoff: receipt outcome %q does not confirm", r.Outcome))
	}
	// 5) 状态机(乱序拒绝:先回查,不盲进)。
	next, err := s.setState(h, StatePlatformConfirmed, EvidenceOfficialReceipt)
	if err != nil {
		return reject("out_of_order", r.ReceiptID, err)
	}
	if err := s.appendEvent(tenantID, handoffID, "official_confirmed", "", EvidenceOfficialReceipt, r.ReceiptID, map[string]any{
		"post_id": r.PostID, "platform": r.Platform, "app_ref": r.AppRef,
	}); err != nil {
		return next, err
	}
	return next, nil
}

// ---- restart recovery ----------------------------------------------------------------

// Resume continues a handoff after a process restart: a preparing row stuck
// mid-media-mint is completed from its FROZEN snapshot (system_recovery,
// once); anything else is a no-op. 从不重投:submitting/unknown 走 Query。
func (s *Service) Resume(_ context.Context, tenantID, handoffID string) (Handoff, error) {
	h, err := s.scan(s.db.QueryRow(`SELECT `+handoffCols+` FROM publish_handoffs WHERE id=? AND tenant_id=?`, handoffID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	if err != nil {
		return Handoff{}, err
	}
	if h.Status != StateHandoffPreparing {
		return h, nil // 已 ready 及之后:恢复是 no-op
	}
	if _, err := s.mintMedia(h); err != nil {
		return s.setState(h, StateFailed, EvidenceSystemAdapter)
	}
	next, err := s.setState(h, StateHandoffReady, EvidenceSystemRecovery)
	if err != nil {
		return h, err
	}
	return next, nil
}
