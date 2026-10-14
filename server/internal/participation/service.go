package participation

// Service 是参与域的单一入口:参与/尝试/交接凭据的存储与状态迁移。
// 参与内部表只由本服务(G2)修改;httpapi 只经本服务消费。
//
// 并发纪律:modernc/sqlite 单写连接。任何网络调用(Reserve)绝不持有
// 写事务——先落行(state=pending),再外呼,再回写状态;崩溃在中间时
// 状态停在 pending,由下一次交接按同槽幂等重试,不重复占库存。

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultHandoffTTL is the default credential lifetime (短时).
const DefaultHandoffTTL = 10 * time.Minute

// reserveTimeout bounds the outbound Reserve call.
const reserveTimeout = 3 * time.Second

// credentialGrace is how long past expiry a used/unused credential row is
// kept before cleanup deletes it (dispute window).
const credentialGrace = 24 * time.Hour

// Options configures the service. Now is injectable for tests.
type Options struct {
	HandoffTTL time.Duration
	Now        func() time.Time
}

// Service owns the participation tables.
type Service struct {
	db       *sql.DB
	reserver BenefitReserver
	opts     Options
}

// New builds a Service over an opened database. A nil reserver is legal:
// participations still start and stay pending — production wiring lands with
// HUI-1671.
func New(database *sql.DB, res BenefitReserver, opts Options) *Service {
	if opts.HandoffTTL <= 0 {
		opts.HandoffTTL = DefaultHandoffTTL
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{db: database, reserver: res, opts: opts}
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("participation: entropy unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

func (s *Service) now() time.Time { return s.opts.Now() }

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// StaffView is the narrow member projection the service needs for staff
// reads. The httpapi authz matrix has already gated the route; this repeats
// the tenant/store scope check so the domain stays safe on its own.
type StaffView struct {
	TenantID   string
	Role       string // org_owner | store_manager (anything else reads nothing)
	StoreScope string
	Revoked    bool
}

// ---- start / lookup / read ---------------------------------------------------

// StartInput is an explicit customer start. The httpapi layer resolves the
// link first (browse stays read-only) and passes the campaign-aligned window.
type StartInput struct {
	TenantID           string
	StoreID            string
	CampaignID         string
	LinkCode           string
	Source             Source
	RuleVersion        int
	ContentPackVersion string
	VisitorID          string
	IdempotencyKey     string
	Window             Window
}

// Started reports whether a new row was created (true → 201) or an earlier
// start was replayed (false → 200). Replays never re-reserve.
type Started struct {
	Participation Participation
	Started       bool
}

// Start creates a pending participation and requests the benefit reservation.
// Browsing has no path into this method — only an explicit customer action.
func (s *Service) Start(ctx context.Context, in StartInput) (Started, error) {
	if strings.TrimSpace(in.VisitorID) == "" {
		return Started{}, ErrNotEntitled // 无会话/无访客键不建行
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return Started{}, ErrIdempotencyConflict
	}
	if !ValidSource(in.Source) {
		return Started{}, ErrInvalidSource
	}
	if in.Window.Expired(s.now()) {
		return Started{}, ErrFrozen
	}
	if existing, ok, err := s.getByIdempotencyKey(in.TenantID, in.CampaignID, in.IdempotencyKey); err != nil {
		return Started{}, err
	} else if ok {
		// 同键重放回原记录——但只回给同一个 visitor:换浏览器重放同键
		// 属冲突,不向陌生 visitor 泄漏原参与。
		if existing.VisitorID != in.VisitorID {
			return Started{}, ErrIdempotencyConflict
		}
		return Started{Participation: existing}, nil
	}

	now := rfc3339(s.now())
	p := Participation{
		ID: newID("pt_"), TenantID: in.TenantID, StoreID: in.StoreID, CampaignID: in.CampaignID,
		LinkCode: in.LinkCode, Source: in.Source, RuleVersion: in.RuleVersion,
		ContentPackVersion: in.ContentPackVersion, VisitorID: in.VisitorID,
		Status: StatusPending, IdempotencyKey: in.IdempotencyKey,
		BenefitSlot: newID("pt_") + "_slot", ReservationState: reservationStartState(s.reserver),
		Window: in.Window, CreatedAt: now, UpdatedAt: now,
	}
	// benefit_slot 的稳定幂等:槽键派生自参与本身(同一参与重试同槽)。
	p.BenefitSlot = p.ID
	_, err := s.db.Exec(`INSERT INTO participations
		(id, tenant_id, store_id, campaign_id, link_code, touchpoint_source, rule_version,
		 content_pack_version, visitor_id, status, idempotency_key, benefit_slot,
		 benefit_reservation_ref, benefit_reservation_state, fulfillment_ref,
		 starts_at, expires_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.TenantID, p.StoreID, p.CampaignID, p.LinkCode, string(p.Source), p.RuleVersion,
		p.ContentPackVersion, p.VisitorID, string(p.Status), p.IdempotencyKey, p.BenefitSlot,
		"", string(p.ReservationState), "",
		p.Window.StartsAt, p.Window.ExpiresAt, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		// 并发同键:唯一索引兜底,回查原行。
		if existing, ok, _ := s.getByIdempotencyKey(in.TenantID, in.CampaignID, in.IdempotencyKey); ok {
			return Started{Participation: existing}, nil
		}
		return Started{}, fmt.Errorf("participation: insert: %w", err)
	}

	// 建行后申请预留(不持事务)。transport err → 停 pending 可重试;
	// 明确失败(缺货/超时/冲突)→ failed。
	s.reserve(ctx, &p)
	if err := s.saveReservationState(p); err != nil {
		return Started{}, err
	}
	return Started{Participation: p, Started: true}, nil
}

func reservationStartState(res BenefitReserver) ReservationState {
	if res == nil {
		return ReservationNotRequested
	}
	return ReservationPending
}

// reserve calls the trusted port and mutates p's reservation fields. A
// transport error leaves state=pending (retryable); a definitive negative
// reason records failed. Neither is ever "confirmed".
func (s *Service) reserve(ctx context.Context, p *Participation) {
	if s.reserver == nil || p.ReservationState == ReservationConfirmed {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reserveTimeout)
	defer cancel()
	res, err := s.reserver.Reserve(cctx, ParticipationRef{
		ParticipationID: p.ID, TenantID: p.TenantID, StoreID: p.StoreID,
		CampaignID: p.CampaignID, BenefitSlot: p.BenefitSlot,
		RuleVersion: p.RuleVersion, ContentPackVersion: p.ContentPackVersion,
	})
	switch {
	case err != nil:
		p.ReservationState = ReservationPending
		p.ReservationRef = ""
	case res.Reserved && res.ReservationRef != "":
		p.ReservationState = ReservationConfirmed
		p.ReservationRef = res.ReservationRef
	default:
		p.ReservationState = ReservationFailed
		p.ReservationRef = ""
	}
}

func (s *Service) saveReservationState(p Participation) error {
	_, err := s.db.Exec(`UPDATE participations SET benefit_reservation_ref=?, benefit_reservation_state=?, updated_at=? WHERE id=? AND tenant_id=?`,
		p.ReservationRef, string(p.ReservationState), rfc3339(s.now()), p.ID, p.TenantID)
	if err != nil {
		return fmt.Errorf("participation: save reservation: %w", err)
	}
	return nil
}

// LookupByVisitor recovers the most recent unfinished participation this
// browser visitor has for the link (同设备重复碰触优先恢复未完参与).
func (s *Service) LookupByVisitor(ctx context.Context, tenantID, linkCode, visitorID string) (Participation, bool, error) {
	if visitorID == "" {
		return Participation{}, false, nil
	}
	row := s.db.QueryRow(`SELECT `+participationCols+` FROM participations
		WHERE tenant_id=? AND link_code=? AND visitor_id=?
		  AND status IN ('pending','in_progress','awaiting_review')
		ORDER BY created_at DESC, id DESC LIMIT 1`, tenantID, linkCode, visitorID)
	p, err := scanParticipation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Participation{}, false, nil
	}
	if err != nil {
		return Participation{}, false, fmt.Errorf("participation: lookup: %w", err)
	}
	return p, true, nil
}

// GetForVisitor reads a participation bound to the caller's own visitor key.
// Any mismatch — id, tenant, visitor — answers ErrNotFound so sequential or
// guessed ids have no probe surface.
func (s *Service) GetForVisitor(_ context.Context, id, tenantID, visitorID string) (Participation, error) {
	if visitorID == "" {
		return Participation{}, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+participationCols+` FROM participations WHERE id=? AND tenant_id=? AND visitor_id=?`, id, tenantID, visitorID)
	p, err := scanParticipation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Participation{}, ErrNotFound
	}
	return p, err
}

// GetForStaff reads a participation through a merchant member. org_owner sees
// the tenant; store_manager only the scoped store; everything else (including
// revoked members) fails closed.
func (s *Service) GetForStaff(_ context.Context, id string, m StaffView) (Participation, error) {
	if m.Revoked {
		return Participation{}, ErrNotEntitled
	}
	p, err := s.getScoped(id, m.TenantID)
	if err != nil {
		return Participation{}, err
	}
	switch m.Role {
	case "org_owner":
		return p, nil
	case "store_manager":
		if m.StoreScope != "" && m.StoreScope == p.StoreID {
			return p, nil
		}
	}
	return Participation{}, ErrNotEntitled
}

func (s *Service) getScoped(id, tenantID string) (Participation, error) {
	row := s.db.QueryRow(`SELECT `+participationCols+` FROM participations WHERE id=? AND tenant_id=?`, id, tenantID)
	p, err := scanParticipation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Participation{}, ErrNotFound
	}
	return p, err
}

func (s *Service) getByIdempotencyKey(tenantID, campaignID, key string) (Participation, bool, error) {
	row := s.db.QueryRow(`SELECT `+participationCols+` FROM participations WHERE tenant_id=? AND campaign_id=? AND idempotency_key=?`, tenantID, campaignID, key)
	p, err := scanParticipation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Participation{}, false, nil
	}
	if err != nil {
		return Participation{}, false, err
	}
	return p, true, nil
}

const participationCols = `id, tenant_id, store_id, campaign_id, link_code, touchpoint_source,
	rule_version, content_pack_version, visitor_id, status, idempotency_key, benefit_slot,
	benefit_reservation_ref, benefit_reservation_state, fulfillment_ref,
	starts_at, expires_at, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanParticipation(row rowScanner) (Participation, error) {
	var p Participation
	var source, status, resState string
	err := row.Scan(&p.ID, &p.TenantID, &p.StoreID, &p.CampaignID, &p.LinkCode, &source,
		&p.RuleVersion, &p.ContentPackVersion, &p.VisitorID, &status, &p.IdempotencyKey, &p.BenefitSlot,
		&p.ReservationRef, &resState, &p.FulfillmentRef,
		&p.Window.StartsAt, &p.Window.ExpiresAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Participation{}, err
	}
	p.Source, p.Status, p.ReservationState = Source(source), Status(status), ReservationState(resState)
	return p, nil
}

// ---- content / cancel / attempts ---------------------------------------------

// UpdateContent moves the current content-pack selection (预览期可换选).
// Frozen attempts are history rows; they never change.
func (s *Service) UpdateContent(_ context.Context, id, tenantID, visitorID, contentPackVersion string) (Participation, error) {
	p, err := s.GetForVisitor(context.Background(), id, tenantID, visitorID)
	if err != nil {
		return Participation{}, err
	}
	if p.Status != StatusPending && p.Status != StatusInProgress {
		return Participation{}, ErrFrozen
	}
	if _, err := s.db.Exec(`UPDATE participations SET content_pack_version=?, updated_at=? WHERE id=? AND tenant_id=? AND visitor_id=?`,
		contentPackVersion, rfc3339(s.now()), id, tenantID, visitorID); err != nil {
		return Participation{}, fmt.Errorf("participation: update content: %w", err)
	}
	return s.getScoped(id, tenantID)
}

// Cancel ends an unfinished participation by its owner (or the store via the
// staff surface in later wiring). Terminal states never resurrect.
func (s *Service) Cancel(_ context.Context, id, tenantID, visitorID string) (Participation, error) {
	p, err := s.GetForVisitor(context.Background(), id, tenantID, visitorID)
	if err != nil {
		return Participation{}, err
	}
	if !CanTransition(p.Status, StatusCanceled) {
		return Participation{}, ErrInvalidTransition
	}
	if _, err := s.db.Exec(`UPDATE participations SET status=?, updated_at=? WHERE id=? AND tenant_id=? AND visitor_id=?`,
		string(StatusCanceled), rfc3339(s.now()), id, tenantID, visitorID); err != nil {
		return Participation{}, fmt.Errorf("participation: cancel: %w", err)
	}
	_ = s.cancelAttempts(id, tenantID)
	return s.getScoped(id, tenantID)
}

func (s *Service) cancelAttempts(participationID, tenantID string) error {
	_, err := s.db.Exec(`UPDATE participation_attempts SET status=?, updated_at=? WHERE participation_id=? AND tenant_id=? AND status=?`,
		string(AttemptCanceled), rfc3339(s.now()), participationID, tenantID, string(AttemptPrepared))
	return err
}

// AttemptInput starts a new distinguishable try. Platform is the publish
// target (custpublish platforms); Method says how the attempt leaves the page.
type AttemptInput struct {
	ParticipationID string
	TenantID        string
	VisitorID       string
	Platform        string
	Method          HandoffMethod
}

// CreateAttempt records a prepared attempt. Versions freeze at BeginHandoff,
// not here — that is the exact difference between previewing and handing off.
func (s *Service) CreateAttempt(_ context.Context, in AttemptInput) (Attempt, error) {
	p, err := s.GetForVisitor(context.Background(), in.ParticipationID, in.TenantID, in.VisitorID)
	if err != nil {
		return Attempt{}, err
	}
	if !validMethod(in.Method) {
		return Attempt{}, ErrInvalidTransition
	}
	if p.Window.Expired(s.now()) {
		return Attempt{}, ErrFrozen
	}
	if p.Status != StatusPending && p.Status != StatusInProgress {
		return Attempt{}, ErrInvalidTransition
	}
	now := rfc3339(s.now())
	a := Attempt{
		ID: newID("pta_"), TenantID: in.TenantID, ParticipationID: p.ID,
		Platform: in.Platform, HandoffMethod: in.Method,
		ContentPackVersion: p.ContentPackVersion, RuleVersion: p.RuleVersion,
		Status: AttemptPrepared, CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.db.Exec(`INSERT INTO participation_attempts
		(id, tenant_id, participation_id, platform, handoff_method, content_pack_version, rule_version, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.TenantID, a.ParticipationID, a.Platform, string(a.HandoffMethod),
		a.ContentPackVersion, a.RuleVersion, string(a.Status), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return Attempt{}, fmt.Errorf("participation: insert attempt: %w", err)
	}
	return a, nil
}

// ListAttempts lists the caller's own attempts, oldest first.
func (s *Service) ListAttempts(_ context.Context, id, tenantID, visitorID string) ([]Attempt, error) {
	if _, err := s.GetForVisitor(context.Background(), id, tenantID, visitorID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, tenant_id, participation_id, platform, handoff_method,
		content_pack_version, rule_version, status, created_at, updated_at
		FROM participation_attempts WHERE participation_id=? AND tenant_id=? ORDER BY created_at, id`, id, tenantID)
	if err != nil {
		return nil, fmt.Errorf("participation: list attempts: %w", err)
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		var method, status string
		if err := rows.Scan(&a.ID, &a.TenantID, &a.ParticipationID, &a.Platform, &method,
			&a.ContentPackVersion, &a.RuleVersion, &status, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.HandoffMethod, a.Status = HandoffMethod(method), AttemptStatus(status)
		out = append(out, a)
	}
	return out, rows.Err()
}

func validMethod(m HandoffMethod) bool {
	switch m {
	case MethodH5, MethodPlatformMini, MethodStaffView:
		return true
	}
	return false
}

// ---- handoff credentials -------------------------------------------------------

// HandoffInput mints a short-lived, single-use, revocable credential that
// carries this SAME participation to another device or to staff.
// customer_resume requires AttemptID (the handoff freezes that attempt);
// staff_verify may omit it (目测核验没有发布尝试).
type HandoffInput struct {
	ParticipationID string
	TenantID        string
	VisitorID       string
	AttemptID       string
	Purpose         Purpose
	TargetScope     string // default: "h5" for resume, tenant for staff
	PublicBaseURL   string // canonical QR payload base; empty omits the payload
}

// HandoffToken is the ONLY moment the plaintext token exists. Store nothing
// but the hash; never log it.
type HandoffToken struct {
	Credential  Credential
	Token       string
	QRPayload   string
	Participation Participation
}

// BeginHandoff freezes the attempt (content + rule versions), marks it
// handed_off, moves the participation in_progress, and mints the credential.
// benefit_slot is never duplicated: retries reuse the participation's slot.
func (s *Service) BeginHandoff(ctx context.Context, in HandoffInput) (HandoffToken, error) {
	p, err := s.GetForVisitor(context.Background(), in.ParticipationID, in.TenantID, in.VisitorID)
	if err != nil {
		return HandoffToken{}, err
	}
	if in.Purpose == "" {
		in.Purpose = PurposeCustomerResume // attempt 交接的缺省语义
	}
	if in.Purpose != PurposeCustomerResume && in.Purpose != PurposeStaffVerify {
		return HandoffToken{}, ErrTargetMismatch
	}
	if p.Window.Expired(s.now()) {
		return HandoffToken{}, ErrFrozen
	}
	// customer_resume 只允许未交接/进行中;staff_verify 允许再出示
	// (核验中换一位店员再扫是合法场景)。
	switch {
	case in.Purpose == PurposeCustomerResume:
		if p.Status != StatusPending && p.Status != StatusInProgress {
			return HandoffToken{}, ErrInvalidTransition
		}
	default: // staff_verify
		if p.Status != StatusPending && p.Status != StatusInProgress && p.Status != StatusAwaitingReview {
			return HandoffToken{}, ErrInvalidTransition
		}
	}
	if in.Purpose == PurposeCustomerResume && strings.TrimSpace(in.AttemptID) == "" {
		return HandoffToken{}, ErrInvalidTransition // 交接必须冻结一个 attempt
	}

	freeze := strings.TrimSpace(in.AttemptID)
	if freeze != "" {
		res, err := s.db.Exec(`UPDATE participation_attempts
			SET content_pack_version=?, rule_version=?, status=?, updated_at=?
			WHERE id=? AND tenant_id=? AND participation_id=? AND status=?`,
			p.ContentPackVersion, p.RuleVersion, string(AttemptHandedOff), rfc3339(s.now()),
			freeze, in.TenantID, p.ID, string(AttemptPrepared))
		if err != nil {
			return HandoffToken{}, fmt.Errorf("participation: freeze attempt: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return HandoffToken{}, ErrNotFound
		}
	}
	if in.Purpose == PurposeCustomerResume && p.Status == StatusPending {
		if _, err := s.db.Exec(`UPDATE participations SET status=?, updated_at=? WHERE id=? AND tenant_id=?`,
			string(StatusInProgress), rfc3339(s.now()), p.ID, p.TenantID); err != nil {
			return HandoffToken{}, fmt.Errorf("participation: mark in_progress: %w", err)
		}
		p.Status = StatusInProgress
	}

	// 受信预留:未确认时按同槽重试(幂等,不重复占库存)。
	s.reserve(ctx, &p)
	if err := s.saveReservationState(p); err != nil {
		return HandoffToken{}, err
	}

	target := in.TargetScope
	if target == "" {
		if in.Purpose == PurposeCustomerResume {
			target = "h5"
		} else {
			target = p.TenantID
		}
	}
	token, err := NewToken()
	if err != nil {
		return HandoffToken{}, err
	}
	now := s.now()
	cred := Credential{
		ID: newID("phc_"), ParticipationID: p.ID, Purpose: in.Purpose, TargetScope: target,
		ExpiresAt: rfc3339(now.Add(s.opts.HandoffTTL)), CreatedAt: rfc3339(now),
	}
	if _, err := s.db.Exec(`INSERT INTO handoff_credentials
		(id, tenant_id, participation_id, attempt_id, purpose, target_scope, token_hash, expires_at, used_at, revoked_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		cred.ID, p.TenantID, p.ID, freeze, string(cred.Purpose), cred.TargetScope,
		HashToken(token), cred.ExpiresAt, "", "", cred.CreatedAt); err != nil {
		return HandoffToken{}, fmt.Errorf("participation: insert credential: %w", err)
	}
	cred.AttemptID = freeze
	if pl, err := handoffQRPayload(in.PublicBaseURL, token); err == nil {
		return HandoffToken{Credential: cred, Token: token, QRPayload: pl, Participation: p}, nil
	}
	return HandoffToken{Credential: cred, Token: token, Participation: p}, nil
}

// handoffQRPayload builds the canonical carrier URL <base>/p/<token>. The
// base must be a clean origin(-or-path prefix); tokens never ride query
// strings and long-lived secrets never enter URLs (this token is short-lived).
func handoffQRPayload(base, token string) (string, error) {
	b := strings.TrimSpace(base)
	if b == "" {
		return "", errors.New("participation: public base url unset")
	}
	b = strings.TrimRight(b, "/")
	if !strings.HasPrefix(b, "https://") && !strings.HasPrefix(b, "http://") {
		return "", errors.New("participation: public base url invalid")
	}
	if strings.ContainsAny(b, "?#") || strings.Contains(b, " ") {
		return "", errors.New("participation: public base url invalid")
	}
	return b + "/p/" + token, nil
}

// RedeemInput consumes a credential. customer_resume rebinds the
// participation to the presenting visitor (cross-device resume);
// staff_verify requires the handler to have already authorized the staff
// member (httpapi authz) and moves the participation to awaiting_review.
type RedeemInput struct {
	Token         string
	Purpose       Purpose
	TargetScope   string
	NewVisitorID  string // customer_resume: the receiving browser's visitor key
	StaffTenantID string // staff_verify: tenant the authorized member belongs to
}

// RedeemResult reports which participation the credential opened.
type RedeemResult struct {
	Participation Participation
	Attempt       Attempt
	HasAttempt    bool
}

// Redeem validates shape → hash → purpose/target → expiry → used → revoked,
// then claims used_at transactionally. Unknown/garbage tokens answer
// ErrNotFound uniformly (anti-enumeration).
func (s *Service) Redeem(_ context.Context, in RedeemInput) (RedeemResult, error) {
	if !ValidTokenShape(in.Token) {
		return RedeemResult{}, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT id, tenant_id, participation_id, attempt_id, purpose, target_scope,
		expires_at, used_at, revoked_at, created_at FROM handoff_credentials WHERE token_hash=?`, HashToken(in.Token))
	c, err := scanCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RedeemResult{}, ErrNotFound
	}
	if err != nil {
		return RedeemResult{}, fmt.Errorf("participation: credential lookup: %w", err)
	}
	if in.Purpose != c.Purpose || in.TargetScope != c.TargetScope {
		return RedeemResult{}, ErrTargetMismatch // purpose/target/app 不符
	}
	now := s.now()
	if expires, perr := time.Parse(time.RFC3339, c.ExpiresAt); perr != nil || !now.Before(expires) {
		return RedeemResult{}, ErrCredentialExpired
	}
	if c.UsedAt != "" {
		return RedeemResult{}, ErrCredentialUsed
	}
	if c.RevokedAt != "" {
		return RedeemResult{}, ErrCredentialRevoked
	}

	// 事务性单次消费:条件更新,0 行 = 并发已被用。
	res, err := s.db.Exec(`UPDATE handoff_credentials SET used_at=? WHERE id=? AND used_at='' AND revoked_at=''`,
		rfc3339(now), c.ID)
	if err != nil {
		return RedeemResult{}, fmt.Errorf("participation: claim credential: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return RedeemResult{}, ErrCredentialUsed
	}

	p, err := s.getScoped(c.ParticipationID, c.TenantID)
	if err != nil {
		return RedeemResult{}, err
	}
	out := RedeemResult{}
	if c.AttemptID != "" {
		if a, err := s.getAttempt(c.AttemptID, c.TenantID); err == nil {
			out.Attempt, out.HasAttempt = a, true
		}
	}
	switch in.Purpose {
	case PurposeCustomerResume:
		if strings.TrimSpace(in.NewVisitorID) == "" {
			return RedeemResult{}, ErrNotEntitled
		}
		if _, err := s.db.Exec(`UPDATE participations SET visitor_id=?, updated_at=? WHERE id=? AND tenant_id=?`,
			in.NewVisitorID, rfc3339(now), p.ID, p.TenantID); err != nil {
			return RedeemResult{}, fmt.Errorf("participation: rebind visitor: %w", err)
		}
		p.VisitorID = in.NewVisitorID
	case PurposeStaffVerify:
		// 目标租户不符在上方 TargetScope 已拒;这里把参与推进核验态。
		if in.StaffTenantID != p.TenantID {
			return RedeemResult{}, ErrTargetMismatch
		}
		// 已在核验中(如另一位店员已扫):幂等保持,不回退。
		if p.Status != StatusAwaitingReview {
			if !CanTransition(p.Status, StatusAwaitingReview) {
				return RedeemResult{}, ErrInvalidTransition
			}
			if _, err := s.db.Exec(`UPDATE participations SET status=?, updated_at=? WHERE id=? AND tenant_id=?`,
				string(StatusAwaitingReview), rfc3339(now), p.ID, p.TenantID); err != nil {
				return RedeemResult{}, fmt.Errorf("participation: mark awaiting_review: %w", err)
			}
			p.Status = StatusAwaitingReview
		}
	}
	out.Participation = p
	return out, nil
}

// PeekCredential resolves a token to its participation and binding WITHOUT
// consuming it. The staff surface authorizes the member against the
// participation's store BEFORE Redeem spends the single use. Garbage and
// unknown tokens answer ErrNotFound uniformly.
func (s *Service) PeekCredential(_ context.Context, token string) (Participation, Credential, error) {
	if !ValidTokenShape(token) {
		return Participation{}, Credential{}, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+credentialCols+` FROM handoff_credentials WHERE token_hash=?`, HashToken(token))
	c, err := scanCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Participation{}, Credential{}, ErrNotFound
	}
	if err != nil {
		return Participation{}, Credential{}, fmt.Errorf("participation: credential lookup: %w", err)
	}
	p, err := s.getScoped(c.ParticipationID, c.TenantID)
	if err != nil {
		return Participation{}, Credential{}, err
	}
	return p, c, nil
}

func (s *Service) getAttempt(id, tenantID string) (Attempt, error) {	row := s.db.QueryRow(`SELECT id, tenant_id, participation_id, platform, handoff_method,
		content_pack_version, rule_version, status, created_at, updated_at
		FROM participation_attempts WHERE id=? AND tenant_id=?`, id, tenantID)
	var a Attempt
	var method, status string
	err := row.Scan(&a.ID, &a.TenantID, &a.ParticipationID, &a.Platform, &method,
		&a.ContentPackVersion, &a.RuleVersion, &status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Attempt{}, err
	}
	a.HandoffMethod, a.Status = HandoffMethod(method), AttemptStatus(status)
	return a, nil
}

const credentialCols = `id, tenant_id, participation_id, attempt_id, purpose, target_scope,
	expires_at, used_at, revoked_at, created_at`

func scanCredential(row rowScanner) (Credential, error) {
	var c Credential
	var purpose string
	err := row.Scan(&c.ID, &c.TenantID, &c.ParticipationID, &c.AttemptID, &purpose, &c.TargetScope,
		&c.ExpiresAt, &c.UsedAt, &c.RevokedAt, &c.CreatedAt)
	if err != nil {
		return Credential{}, err
	}
	c.Purpose = Purpose(purpose)
	return c, nil
}

// Revoke invalidates an unused credential owned by the participation's
// current visitor. Used credentials cannot be un-used.
func (s *Service) Revoke(_ context.Context, id, tenantID, visitorID, credentialID string) error {
	if _, err := s.GetForVisitor(context.Background(), id, tenantID, visitorID); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE handoff_credentials SET revoked_at=?
		WHERE id=? AND tenant_id=? AND participation_id=? AND used_at='' AND revoked_at=''`,
		rfc3339(s.now()), credentialID, tenantID, id)
	if err != nil {
		return fmt.Errorf("participation: revoke: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// ---- trusted fulfillment (G3 wiring) -------------------------------------------

// MarkFulfilled is the ONLY path to StatusFulfilled, reserved for the reward
// domain (HUI-1671) after verified fulfillment. It is idempotent on ref and
// refuses to rewrite an already-fulfilled record with a different ref; late
// failures and out-of-order receipts never downgrade fulfillment. No HTTP
// route in this ticket reaches it.
func (s *Service) MarkFulfilled(_ context.Context, tenantID, id, fulfillmentRef string) (Participation, error) {
	p, err := s.getScoped(id, tenantID)
	if err != nil {
		return Participation{}, err
	}
	switch p.Status {
	case StatusFulfilled:
		if p.FulfillmentRef == fulfillmentRef {
			return p, nil // 幂等重放
		}
		return Participation{}, ErrInvalidTransition // 不改写已兑现
	case StatusAwaitingReview:
		if !CanTransition(p.Status, StatusFulfilled) {
			return Participation{}, ErrInvalidTransition
		}
	default:
		return Participation{}, ErrInvalidTransition // 预留/进行中都不是兑现
	}
	if _, err := s.db.Exec(`UPDATE participations SET status=?, fulfillment_ref=?, updated_at=? WHERE id=? AND tenant_id=? AND status=?`,
		string(StatusFulfilled), fulfillmentRef, rfc3339(s.now()), id, tenantID, string(StatusAwaitingReview)); err != nil {
		return Participation{}, fmt.Errorf("participation: fulfill: %w", err)
	}
	return s.getScoped(id, tenantID)
}

// ---- cleanup --------------------------------------------------------------------

// Cleanup runs one retention sweep: expired credentials older than the
// dispute grace are deleted; canceled participations past the retention
// window are anonymized (visitor key blanked; the business row stays for
// audit). Called by the operator's scheduler via StartCleanupLoop wiring —
// deliberately no embedded timer, no Redis.
func (s *Service) Cleanup(ctx context.Context, now time.Time, retention time.Duration) (anonymized, deleted int64, err error) {
	cutoff := rfc3339(now.Add(-credentialGrace))
	res, err := s.db.ExecContext(ctx, `DELETE FROM handoff_credentials WHERE expires_at < ?`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("participation: cleanup credentials: %w", err)
	}
	deleted, _ = res.RowsAffected()

	retCutoff := rfc3339(now.Add(-retention))
	res, err = s.db.ExecContext(ctx, `UPDATE participations SET visitor_id='', updated_at=?
		WHERE status=? AND updated_at < ? AND visitor_id != ''`, rfc3339(now), string(StatusCanceled), retCutoff)
	if err != nil {
		return 0, deleted, fmt.Errorf("participation: cleanup participations: %w", err)
	}
	anonymized, _ = res.RowsAffected()
	return anonymized, deleted, nil
}
