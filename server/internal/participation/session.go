package participation

// 匿名动作会话(HUI-3546 增量轮 2026-11-09, B08): 动作维度的匿名权威。
//
// 与参与(participation)的分工:
//   - 会话是动作维度:campaign 可空,无活动/活动结束照建,绝不伪造活动 id;
//     浏览/选材/门店服务不需要参与,只有显式办理权益才经 AttachParticipation
//     升级建 participation(既有参与语义零改动);
//   - 会话创建即冻结快照(storefront/action revision + 规则快照 + 内容引用);
//     停用/撤权/下架是独立实时检查:一切"新动作"(换选/签发凭据/兑换/升级)
//     先经 ActionDirectory 实时复核,快照不掩盖当前状态;而已发生事实
//     (status 读、已发参与)不受停用影响——旧权益保护;
//   - 跨浏览器恢复只走短时/单用途/哈希入库的恢复凭据;全部凭据丢失不可认回
//     (无指纹、无手机号、无顺序 id 后门)。
//
// 并发纪律与参与一致:modernc/sqlite 单写连接;凭据单用途用条件 UPDATE
// 事务置位,所有校验通过才消费,拒绝不烧单用途。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SessionStatus is the session lifecycle. Sessions are lightweight: there is
// no fulfilled/canceled taxonomy here — ended is the only exit, and reading a
// ended session stays allowed (the customer can always see what happened).
type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionEnded  SessionStatus = "ended"
)

// Session sentinel errors (stable for tests/handlers).
var (
	// ErrDirectoryUnavailable: the published-action catalog port is not wired
	// (production wiring lands with HUI-4584). Fail-closed: no session can be
	// created or mutated against an unresolved action.
	ErrDirectoryUnavailable = errors.New("participation: action directory not wired")
	// ErrActionUnavailable: the action exists but is currently disabled/
	// unpublished (real-time), or it vanished after a credential was issued.
	ErrActionUnavailable = errors.New("participation: action is not currently available")
	// ErrStaleVersion: a client-supplied expected revision does not match the
	// server's live revision. Expectations are advisory, never authoritative.
	ErrStaleVersion = errors.New("participation: client expected a stale revision")
	// ErrSessionEnded: the session is closed; only reads remain.
	ErrSessionEnded = errors.New("participation: action session ended")
	// ErrCampaignRequired: benefit upgrade needs a real campaign and the
	// action has none. Never fabricate a campaign id.
	ErrCampaignRequired = errors.New("participation: action has no activity to join")
)

// ActionRef is the server-side projection of one PUBLISHED action, as the
// catalog resolves it at call time. It is the only trusted source for
// tenant/store binding, revisions and rule snapshots — client inputs are
// expectations, never authorities.
type ActionRef struct {
	ActionID           string
	CatalogCode        string
	Platform           string
	Kind               string
	TenantID           string
	StoreID            string
	StorefrontRevision int
	ActionRevision     int
	RuleVersion        int
	RuleSnapshot       string // frozen rule snapshot text (server-owned)
	ContentPackVersion string // currently adopted content version ("" = none)
	CampaignID         string // "" = the action carries no activity
	Window             Window // campaign-aligned window (zero when no campaign)
	Enabled            bool
}

// ActionDirectory is the port to the published-action catalog (HUI-4584's
// write surface). This package never stores a second copy of the directory:
// unknown, unpublished and disabled actions all fail closed here.
type ActionDirectory interface {
	// ResolveAction resolves a published action by its stable id. Unknown or
	// unpublished actions answer (zero, false, nil) — the caller maps that to
	// its own not-found semantics.
	ResolveAction(ctx context.Context, actionID string) (ActionRef, bool, error)
}

// ActionSession is the anonymous authority for one visitor acting on one
// action. It carries no identity fields — no phone, no platform open id, no
// account. The visitor binding (first-party random cookie key) is the only
// access secret.
type ActionSession struct {
	ID                 string
	TenantID           string
	StoreID            string
	ActionID           string
	CatalogCode        string
	Platform           string
	Kind               string
	StorefrontRevision int    // frozen at creation
	ActionRevision     int    // frozen at creation
	RuleVersion        int    // frozen at creation
	RuleSnapshot       string // frozen at creation; never leaves the server
	ContentPackVersion string // selected content reference (preview-time movable)
	VisitorID          string
	ParticipationID    string // "" until an explicit benefit upgrade
	Status             SessionStatus
	IdempotencyKey     string
	CreatedAt          string
	UpdatedAt          string
}

// SessionStartInput is an explicit session open. The handler resolves the
// visitor cookie; browsing has no path into this method.
type SessionStartInput struct {
	ActionID                   string
	Source                     Source // touchpoint origin, recorded for counting only
	ContentPackVersion         string // customer's pick; default = adopted version
	IdempotencyKey             string // optional; replay/conflict per visitor
	VisitorID                  string
	ExpectedActionRevision     int // 0 = unchecked expectation
	ExpectedStorefrontRevision int // 0 = unchecked expectation
}

// SessionStarted reports whether a new session was created (true → 201) or an
// earlier one was restored for the same browser (false → 200).
type SessionStarted struct {
	Session ActionSession
	Created bool
}

const sessionCols = `id, tenant_id, store_id, action_id, catalog_code, platform, kind,
	storefront_revision, action_revision, rule_version, rule_snapshot,
	content_pack_version, visitor_id, participation_id, status,
	COALESCE(idempotency_key, ''), created_at, updated_at`

func scanSession(row rowScanner) (ActionSession, error) {
	var sess ActionSession
	var status string
	err := row.Scan(&sess.ID, &sess.TenantID, &sess.StoreID, &sess.ActionID, &sess.CatalogCode,
		&sess.Platform, &sess.Kind, &sess.StorefrontRevision, &sess.ActionRevision,
		&sess.RuleVersion, &sess.RuleSnapshot, &sess.ContentPackVersion, &sess.VisitorID,
		&sess.ParticipationID, &status, &sess.IdempotencyKey, &sess.CreatedAt, &sess.UpdatedAt)
	if err != nil {
		return ActionSession{}, err
	}
	sess.Status = SessionStatus(status)
	return sess, nil
}

// SetActionDirectory wires the published-action catalog port. Without it the
// whole session surface fails closed (ErrDirectoryUnavailable): production
// wiring lands with HUI-4584 via one assembly line, tests inject their stub.
func (s *Service) SetActionDirectory(d ActionDirectory) { s.dir = d }

// SessionsReady reports whether the directory port is wired (httpapi gate
// uses this to fail closed with 503 instead of pretending to serve).
func (s *Service) SessionsReady() bool { return s.dir != nil }

// liveAction resolves the action in real time. Unknown/unpublished collapses
// into ErrNotFound (anti-enumeration: indistinguishable from a wrong id); a
// known-but-disabled action answers ErrActionUnavailable honestly. This is
// the anti-snapshot check: freezing versions at creation never masks a live
// disablement or retraction.
func (s *Service) liveAction(ctx context.Context, actionID string) (ActionRef, error) {
	ref, ok, err := s.dir.ResolveAction(ctx, actionID)
	if err != nil {
		return ActionRef{}, fmt.Errorf("participation: resolve action: %w", err)
	}
	if !ok {
		return ActionRef{}, ErrNotFound
	}
	if !ref.Enabled {
		return ActionRef{}, ErrActionUnavailable
	}
	return ref, nil
}

// StartActionSession creates the visitor's session for an explicit action or
// restores the same browser's most recent active one. Order matters:
// directory readiness → visitor binding → real-time resolution → expectation
// checks → idempotent replay → same-browser restore → create.
func (s *Service) StartActionSession(ctx context.Context, in SessionStartInput) (SessionStarted, error) {
	if !s.SessionsReady() {
		return SessionStarted{}, ErrDirectoryUnavailable
	}
	if strings.TrimSpace(in.VisitorID) == "" {
		return SessionStarted{}, ErrNotEntitled // 无访客键不建行
	}
	if !ValidSource(in.Source) && in.Source != "" {
		return SessionStarted{}, ErrInvalidSource
	}
	ref, err := s.liveAction(ctx, in.ActionID)
	if err != nil {
		return SessionStarted{}, err
	}
	// 期望 revision 核对(零值 = 未声明,不核对)。
	if in.ExpectedActionRevision != 0 && in.ExpectedActionRevision != ref.ActionRevision {
		return SessionStarted{}, ErrStaleVersion
	}
	if in.ExpectedStorefrontRevision != 0 && in.ExpectedStorefrontRevision != ref.StorefrontRevision {
		return SessionStarted{}, ErrStaleVersion
	}
	if in.IdempotencyKey != "" {
		existing, ok, err := s.getSessionByIdempotencyKey(ref.TenantID, in.ActionID, in.IdempotencyKey)
		if err != nil {
			return SessionStarted{}, err
		}
		if ok {
			if existing.VisitorID != in.VisitorID {
				return SessionStarted{}, ErrIdempotencyConflict // 换浏览器重放同键
			}
			return SessionStarted{Session: existing}, nil
		}
	}
	if restored, ok, err := s.restoreSession(ref.TenantID, in.ActionID, in.VisitorID); err != nil {
		return SessionStarted{}, err
	} else if ok {
		if err := s.recordSessionEvent("resumed", restored, ""); err != nil {
			return SessionStarted{}, err
		}
		return SessionStarted{Session: restored}, nil
	}

	content := strings.TrimSpace(in.ContentPackVersion)
	if content == "" {
		content = ref.ContentPackVersion // 未选择时落目录已采用版本
	}
	now := rfc3339(s.now())
	sess := ActionSession{
		ID: newID("as_"), TenantID: ref.TenantID, StoreID: ref.StoreID,
		ActionID: ref.ActionID, CatalogCode: ref.CatalogCode, Platform: ref.Platform, Kind: ref.Kind,
		StorefrontRevision: ref.StorefrontRevision, ActionRevision: ref.ActionRevision,
		RuleVersion: ref.RuleVersion, RuleSnapshot: ref.RuleSnapshot,
		ContentPackVersion: content, VisitorID: in.VisitorID,
		Status: SessionActive, IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
		CreatedAt: now, UpdatedAt: now,
	}
	// 无幂等键落 NULL(SQLite UNIQUE 多 NULL 互不冲突);同键重复仍被拒。
	var keyArg any
	if sess.IdempotencyKey != "" {
		keyArg = sess.IdempotencyKey
	}
	if _, err := s.db.Exec(`INSERT INTO action_sessions
		(id, tenant_id, store_id, action_id, catalog_code, platform, kind,
		 storefront_revision, action_revision, rule_version, rule_snapshot,
		 content_pack_version, visitor_id, participation_id, status, idempotency_key,
		 created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.TenantID, sess.StoreID, sess.ActionID, sess.CatalogCode, sess.Platform, sess.Kind,
		sess.StorefrontRevision, sess.ActionRevision, sess.RuleVersion, sess.RuleSnapshot,
		sess.ContentPackVersion, sess.VisitorID, "", string(sess.Status), keyArg,
		sess.CreatedAt, sess.UpdatedAt); err != nil {
		// 并发同键:唯一索引兜底,回查原行。
		if sess.IdempotencyKey != "" {
			if existing, ok, _ := s.getSessionByIdempotencyKey(ref.TenantID, in.ActionID, sess.IdempotencyKey); ok && existing.VisitorID == in.VisitorID {
				return SessionStarted{Session: existing}, nil
			}
		}
		return SessionStarted{}, fmt.Errorf("participation: insert session: %w", err)
	}
	if err := s.recordSessionEvent("created", sess, ""); err != nil {
		return SessionStarted{}, err
	}
	return SessionStarted{Session: sess, Created: true}, nil
}

// UpdateActionSessionContent moves the session's content reference during
// preview. Authoritative content validation happens at the content-serving
// and handoff surfaces (HUI-3553/1670); this records the customer's pick and
// refuses when the action is live-disabled or the session has ended.
func (s *Service) UpdateActionSessionContent(ctx context.Context, id, visitorID, contentPackVersion string) (ActionSession, error) {
	if !s.SessionsReady() {
		return ActionSession{}, ErrDirectoryUnavailable
	}
	sess, err := s.getSessionForVisitor(ctx, id, visitorID)
	if err != nil {
		return ActionSession{}, err
	}
	if sess.Status != SessionActive {
		return ActionSession{}, ErrSessionEnded
	}
	if _, err := s.liveAction(ctx, sess.ActionID); err != nil {
		return ActionSession{}, err
	}
	content := strings.TrimSpace(contentPackVersion)
	if _, err := s.db.Exec(`UPDATE action_sessions SET content_pack_version=?, updated_at=?
		WHERE id=? AND tenant_id=? AND visitor_id=?`,
		content, rfc3339(s.now()), sess.ID, sess.TenantID, visitorID); err != nil {
		return ActionSession{}, fmt.Errorf("participation: update session content: %w", err)
	}
	sess.ContentPackVersion = content
	if err := s.recordSessionEvent("content_changed", sess, ""); err != nil {
		return ActionSession{}, err
	}
	return sess, nil
}

// CancelActionSession ends the visitor's own active session and revokes every
// outstanding unused resume credential (取消即全废:散落的凭据不能再兑换).
// Reads of the ended session stay allowed — the customer can always see what
// happened; nothing resurrects a ended session.
func (s *Service) CancelActionSession(ctx context.Context, id, visitorID string) (ActionSession, error) {
	sess, err := s.getSessionForVisitor(ctx, id, visitorID)
	if err != nil {
		return ActionSession{}, err
	}
	if sess.Status == SessionEnded {
		return sess, nil // 幂等:重复取消回同一终态
	}
	now := rfc3339(s.now())
	if _, err := s.db.Exec(`UPDATE action_sessions SET status=?, updated_at=?
		WHERE id=? AND tenant_id=? AND visitor_id=? AND status=?`,
		string(SessionEnded), now, sess.ID, sess.TenantID, visitorID, string(SessionActive)); err != nil {
		return ActionSession{}, fmt.Errorf("participation: cancel session: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE action_session_credentials SET revoked_at=?
		WHERE session_id=? AND used_at='' AND revoked_at=''`, now, sess.ID); err != nil {
		return ActionSession{}, fmt.Errorf("participation: revoke session credentials: %w", err)
	}
	sess.Status = SessionEnded
	if err := s.recordSessionEvent("canceled", sess, ""); err != nil {
		return ActionSession{}, err
	}
	return sess, nil
}

// ---- resume credentials (cross-browser) -------------------------------------------
//
// 恢复凭据:ptx_ 令牌纪律复用(128-bit 随机 + 校验位;库内只存 SHA-256 全
// token 哈希)。purpose 结构性只有 customer_resume(迁移 CHECK);店员码
// (staff_verify)与参与交接凭据物理上在 handoff_credentials,进不了会话
// 兑换面。明文只在 IssueResumeCredential 的响应出现一次。

// SessionCredential is the safe-to-expose view of a resume credential.
type SessionCredential struct {
	ID        string
	SessionID string
	ExpiresAt string
	UsedAt    string
	RevokedAt string
	CreatedAt string
}

// ResumeCredentialInput mints a cross-browser resume credential for the
// caller's OWN session. purpose is fixed customer_resume by construction.
type ResumeCredentialInput struct {
	SessionID string
	VisitorID string
}

// SessionResumeToken is the ONLY moment the plaintext token exists.
type SessionResumeToken struct {
	Credential SessionCredential
	Token      string
}

// IssueResumeCredential: 只有当前合法访客能给自己 active 的会话签发;动作
// 实时停用时拒绝(停用不再发放新的跨端凭据;同浏览器恢复与状态读不受影响)。
func (s *Service) IssueResumeCredential(ctx context.Context, in ResumeCredentialInput) (SessionResumeToken, error) {
	if !s.SessionsReady() {
		return SessionResumeToken{}, ErrDirectoryUnavailable
	}
	sess, err := s.getSessionForVisitor(ctx, in.SessionID, in.VisitorID)
	if err != nil {
		return SessionResumeToken{}, err
	}
	if sess.Status != SessionActive {
		return SessionResumeToken{}, ErrSessionEnded
	}
	if _, err := s.liveAction(ctx, sess.ActionID); err != nil {
		return SessionResumeToken{}, err
	}
	token, err := NewToken()
	if err != nil {
		return SessionResumeToken{}, err
	}
	now := s.now()
	cred := SessionCredential{
		ID: newID("rsc_"), SessionID: sess.ID,
		ExpiresAt: rfc3339(now.Add(s.opts.ResumeTTL)), CreatedAt: rfc3339(now),
	}
	if _, err := s.db.Exec(`INSERT INTO action_session_credentials
		(id, tenant_id, session_id, purpose, target_scope, token_hash, expires_at, used_at, revoked_at, created_at)
		VALUES (?,?,?,'customer_resume','h5',?,?, '','' ,?)`,
		cred.ID, sess.TenantID, sess.ID, HashToken(token), cred.ExpiresAt, cred.CreatedAt); err != nil {
		return SessionResumeToken{}, fmt.Errorf("participation: insert resume credential: %w", err)
	}
	if err := s.recordSessionEvent("resume_credential_issued", sess, cred.ID); err != nil {
		return SessionResumeToken{}, err
	}
	return SessionResumeToken{Credential: cred, Token: token}, nil
}

// RevokeResumeCredential invalidates an unused credential owned by the
// session's current visitor. Used credentials cannot be un-used.
func (s *Service) RevokeResumeCredential(ctx context.Context, sessionID, visitorID, credentialID string) error {
	sess, err := s.getSessionForVisitor(ctx, sessionID, visitorID)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE action_session_credentials SET revoked_at=?
		WHERE id=? AND tenant_id=? AND session_id=? AND used_at='' AND revoked_at=''`,
		rfc3339(s.now()), credentialID, sess.TenantID, sess.ID)
	if err != nil {
		return fmt.Errorf("participation: revoke resume credential: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if err := s.recordSessionEvent("resume_credential_revoked", sess, credentialID); err != nil {
		return err
	}
	return nil
}

// SessionExchangeInput consumes a resume credential: the presenting browser
// (NewVisitorID) takes over the SAME session. All checks run before the
// transactional claim — every rejection leaves the credential spendable, and
// concurrent exchanges produce exactly one winner.
type SessionExchangeInput struct {
	Token        string
	NewVisitorID string
}

// SessionExchangeResult reports the session the credential opened.
type SessionExchangeResult struct {
	Session ActionSession
}

// ExchangeResumeCredential: 形状 → 哈希查找(只在本表,店员码/参与凭据
// 结构性查不到) → 过期 → 已用 → 撤销 → 会话 active → 动作实时可用 →
// 事务性单次消费 → 换绑 visitor。全部凭据丢失不可认回:除凭据本身外
// 没有任何认回路径。
func (s *Service) ExchangeResumeCredential(ctx context.Context, in SessionExchangeInput) (SessionExchangeResult, error) {
	if !ValidTokenShape(in.Token) {
		return SessionExchangeResult{}, ErrNotFound
	}
	if strings.TrimSpace(in.NewVisitorID) == "" {
		return SessionExchangeResult{}, ErrNotEntitled
	}
	row := s.db.QueryRow(`SELECT id, tenant_id, session_id, purpose, target_scope,
		expires_at, used_at, revoked_at, created_at FROM action_session_credentials
		WHERE token_hash=?`, HashToken(in.Token))
	var c SessionCredential
	var tenantID string
	var purpose, target string
	err := row.Scan(&c.ID, &tenantID, &c.SessionID, &purpose, &target,
		&c.ExpiresAt, &c.UsedAt, &c.RevokedAt, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionExchangeResult{}, ErrNotFound
	}
	if err != nil {
		return SessionExchangeResult{}, fmt.Errorf("participation: resume credential lookup: %w", err)
	}
	if purpose != string(PurposeCustomerResume) || target != "h5" {
		return SessionExchangeResult{}, ErrTargetMismatch
	}
	now := s.now()
	if expires, perr := time.Parse(time.RFC3339, c.ExpiresAt); perr != nil || !now.Before(expires) {
		return SessionExchangeResult{}, ErrCredentialExpired
	}
	if c.UsedAt != "" {
		return SessionExchangeResult{}, ErrCredentialUsed
	}
	if c.RevokedAt != "" {
		return SessionExchangeResult{}, ErrCredentialRevoked
	}
	sess, err := s.getSession(c.SessionID, tenantID)
	if err != nil {
		return SessionExchangeResult{}, err
	}
	if sess.Status != SessionActive {
		return SessionExchangeResult{}, ErrSessionEnded
	}
	// 实时停用检查:签发后动作被停用/下架,兑换一律拒绝(快照不掩盖现状)。
	if s.SessionsReady() {
		if _, err := s.liveAction(ctx, sess.ActionID); err != nil {
			return SessionExchangeResult{}, err
		}
	} else {
		return SessionExchangeResult{}, ErrDirectoryUnavailable
	}
	// 事务性单次消费:条件更新,0 行 = 并发已被用。
	res, err := s.db.Exec(`UPDATE action_session_credentials SET used_at=?
		WHERE id=? AND used_at='' AND revoked_at=''`, rfc3339(now), c.ID)
	if err != nil {
		return SessionExchangeResult{}, fmt.Errorf("participation: claim resume credential: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SessionExchangeResult{}, ErrCredentialUsed
	}
	res, err = s.db.Exec(`UPDATE action_sessions SET visitor_id=?, updated_at=?
		WHERE id=? AND tenant_id=? AND status=?`,
		in.NewVisitorID, rfc3339(now), sess.ID, sess.TenantID, string(SessionActive))
	if err != nil {
		return SessionExchangeResult{}, fmt.Errorf("participation: rebind session visitor: %w", err)
	}
	// 并发取消竞态:消费与会话换绑之间会话被取消时,这里 0 行——诚实回答
	// 终态拒绝(凭据已烧,但取消是显式终态,兑换不得伪报成功)。
	if n, _ := res.RowsAffected(); n != 1 {
		return SessionExchangeResult{}, ErrSessionEnded
	}
	sess.VisitorID = in.NewVisitorID
	if err := s.recordSessionEvent("resume_credential_exchanged", sess, c.ID); err != nil {
		return SessionExchangeResult{}, err
	}
	return SessionExchangeResult{Session: sess}, nil
}

// ---- status view (read-only) ------------------------------------------------------

// SessionParticipationRef is the benefit reference carried on the status
// view: progress + reservation state only. No visitor key, no idempotency
// key, no reservation ref ever leaves the server.
type SessionParticipationRef struct {
	ID                      string
	Status                  string
	BenefitReservationState string
}

// SessionStatusView is the read-only status payload: the frozen snapshot plus
// two real-time flags. A disabled/unpublished action never blocks reading —
// 已发生的事实不受停用影响(旧权益保护) — but resume_available answers false
// so the current state is never masked by the snapshot.
type SessionStatusView struct {
	Session         ActionSession
	ActionAvailable bool
	ResumeAvailable bool
	Participation   *SessionParticipationRef
}

// ActionSessionStatus reads the caller's own session with live availability
// flags. Read-only: no state transition, no credential issuance.
func (s *Service) ActionSessionStatus(ctx context.Context, id, visitorID string) (SessionStatusView, error) {
	if !s.SessionsReady() {
		return SessionStatusView{}, ErrDirectoryUnavailable
	}
	sess, err := s.getSessionForVisitor(ctx, id, visitorID)
	if err != nil {
		return SessionStatusView{}, err
	}
	view := SessionStatusView{Session: sess}
	if ref, ok, rerr := s.dir.ResolveAction(ctx, sess.ActionID); rerr == nil && ok {
		view.ActionAvailable = ref.Enabled
	}
	view.ResumeAvailable = view.ActionAvailable && sess.Status == SessionActive
	if sess.ParticipationID != "" {
		if p, perr := s.getScoped(sess.ParticipationID, sess.TenantID); perr == nil {
			view.Participation = &SessionParticipationRef{
				ID:                      p.ID,
				Status:                  string(p.Status),
				BenefitReservationState: string(p.ReservationState),
			}
		}
	}
	return view, nil
}

// ---- benefit upgrade (explicit participation) --------------------------------------

// AttachInput is the explicit benefit upgrade. The participation idempotency
// key is DERIVED from the session id — one session, one participation, no
// matter how many times the customer taps 办理 or which client key arrives.
type AttachInput struct {
	SessionID string
	VisitorID string
	Source    Source
}

// AttachResult reports the session and its (new or existing) participation.
// Created distinguishes a fresh upgrade (201) from an idempotent re-attach (200).
type AttachResult struct {
	Session       ActionSession
	Participation Participation
	Created       bool
}

// AttachParticipation upgrades an active session into the existing
// participation semantics: server re-resolves the action live; a session
// without a real campaign refuses (ErrCampaignRequired — never fabricate
// campaign ids); an expired campaign window flows into the existing
// Start() rules (ErrFrozen); rule_version comes from the server (the
// client's expectation never becomes the authority); the benefit reservation
// goes through the existing trusted BenefitReserver port (nil → not_requested,
// never 已保留). Idempotent: an already-attached session returns its
// participation unchanged.
func (s *Service) AttachParticipation(ctx context.Context, in AttachInput) (AttachResult, error) {
	if !s.SessionsReady() {
		return AttachResult{}, ErrDirectoryUnavailable
	}
	sess, err := s.getSessionForVisitor(ctx, in.SessionID, in.VisitorID)
	if err != nil {
		return AttachResult{}, err
	}
	if sess.Status != SessionActive {
		return AttachResult{}, ErrSessionEnded
	}
	ref, err := s.liveAction(ctx, sess.ActionID)
	if err != nil {
		return AttachResult{}, err
	}
	if ref.CampaignID == "" {
		return AttachResult{}, ErrCampaignRequired // 无活动动作办不了权益,不伪造活动
	}
	if sess.ParticipationID != "" {
		p, perr := s.getScoped(sess.ParticipationID, sess.TenantID)
		if perr != nil {
			return AttachResult{}, perr
		}
		return AttachResult{Session: sess, Participation: p, Created: false}, nil
	}
	content := sess.ContentPackVersion
	if content == "" {
		content = ref.ContentPackVersion
	}
	// 既有 Start() 全语义:窗口过期→ErrFrozen;幂等键派生自会话,并发同回原行;
	// reserver nil → not_requested(不伪报已保留)。
	started, err := s.Start(ctx, StartInput{
		TenantID: ref.TenantID, StoreID: ref.StoreID, CampaignID: ref.CampaignID,
		LinkCode: "", Source: in.Source, RuleVersion: ref.RuleVersion,
		ContentPackVersion: content, VisitorID: sess.VisitorID,
		IdempotencyKey: "sessattach_" + sess.ID, Window: ref.Window,
	})
	if err != nil {
		return AttachResult{}, err
	}
	p := started.Participation
	if _, err := s.db.Exec(`UPDATE action_sessions SET participation_id=?, updated_at=?
		WHERE id=? AND tenant_id=?`, p.ID, rfc3339(s.now()), sess.ID, sess.TenantID); err != nil {
		return AttachResult{}, fmt.Errorf("participation: link session participation: %w", err)
	}
	sess.ParticipationID = p.ID
	if err := s.recordSessionEvent("participation_attached", sess, ""); err != nil {
		return AttachResult{}, err
	}
	return AttachResult{Session: sess, Participation: p, Created: true}, nil
}

// ---- cleanup --------------------------------------------------------------------

// CleanupSessions runs one retention sweep for the session surface: expired
// resume credentials older than the dispute grace are deleted; ended sessions
// past the retention window are anonymized (visitor key blanked; the row
// stays for audit). Active sessions are never touched.
func (s *Service) CleanupSessions(ctx context.Context, now time.Time, retention time.Duration) (anonymized, deleted int64, err error) {
	cutoff := rfc3339(now.Add(-credentialGrace))
	res, err := s.db.ExecContext(ctx, `DELETE FROM action_session_credentials WHERE expires_at < ?`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("participation: cleanup session credentials: %w", err)
	}
	deleted, _ = res.RowsAffected()

	retCutoff := rfc3339(now.Add(-retention))
	res, err = s.db.ExecContext(ctx, `UPDATE action_sessions SET visitor_id='', updated_at=?
		WHERE status=? AND updated_at < ? AND visitor_id != ''`,
		rfc3339(now), string(SessionEnded), retCutoff)
	if err != nil {
		return deleted, 0, fmt.Errorf("participation: cleanup sessions: %w", err)
	}
	anonymized, _ = res.RowsAffected()
	return anonymized, deleted, nil
}

// ---- session lookups ------------------------------------------------------------

// SessionRef is the server-internal projection of one session reference for
// trusted cross-package issuance checks (中央集成 2026-11-10: HUI-3553
// media-access 签发时刻校验). Scope ids and the content reference only — no
// visitor key (the visitor binding is the session's only access secret and
// never leaves this package), no snapshot text.
type SessionRef struct {
	TenantID           string
	StoreID            string
	ActionID           string
	ContentPackVersion string
}

// LookupSessionRef resolves a session reference at a trusted server-internal
// issuance point. The session must exist and still be active — sessions carry
// no TTL of their own; ended is the only expiry. Unknown ids answer
// ErrNotFound (uniform, no existence leak); ended sessions answer
// ErrSessionEnded. This is NOT an authorization: callers must still check the
// returned scope against the resource being issued for.
func (s *Service) LookupSessionRef(id string) (SessionRef, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM action_sessions WHERE id=?`, strings.TrimSpace(id))
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRef{}, ErrNotFound
	}
	if err != nil {
		return SessionRef{}, fmt.Errorf("participation: session ref lookup: %w", err)
	}
	if sess.Status != SessionActive {
		return SessionRef{}, ErrSessionEnded
	}
	return SessionRef{
		TenantID:           sess.TenantID,
		StoreID:            sess.StoreID,
		ActionID:           sess.ActionID,
		ContentPackVersion: sess.ContentPackVersion,
	}, nil
}

// getSessionForVisitor reads a session bound to the caller's own visitor key.
// Any mismatch — id or visitor — answers ErrNotFound so sequential or guessed
// ids have no probe surface.
func (s *Service) getSessionForVisitor(_ context.Context, id, visitorID string) (ActionSession, error) {
	if visitorID == "" {
		return ActionSession{}, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM action_sessions WHERE id=? AND visitor_id=?`, id, visitorID)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionSession{}, ErrNotFound
	}
	return sess, err
}

func (s *Service) getSession(id, tenantID string) (ActionSession, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM action_sessions WHERE id=? AND tenant_id=?`, id, tenantID)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionSession{}, ErrNotFound
	}
	return sess, err
}

// restoreSession recovers the visitor's most recent active session for the
// action (同设备重复碰触优先恢复,不开新行).
func (s *Service) restoreSession(tenantID, actionID, visitorID string) (ActionSession, bool, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM action_sessions
		WHERE tenant_id=? AND action_id=? AND visitor_id=? AND status=?
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		tenantID, actionID, visitorID, string(SessionActive))
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionSession{}, false, nil
	}
	if err != nil {
		return ActionSession{}, false, fmt.Errorf("participation: restore session: %w", err)
	}
	return sess, true, nil
}

func (s *Service) getSessionByIdempotencyKey(tenantID, actionID, key string) (ActionSession, bool, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM action_sessions
		WHERE tenant_id=? AND action_id=? AND idempotency_key=?`, tenantID, actionID, key)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionSession{}, false, nil
	}
	if err != nil {
		return ActionSession{}, false, err
	}
	return sess, true, nil
}

// ---- session events (audit) ------------------------------------------------------

// recordSessionEvent appends one audit row. event ids are server-generated
// (the UNIQUE(source,event_id) dedup guards replays); no token, no visitor
// key, no phone ever lands here.
func (s *Service) recordSessionEvent(kind string, sess ActionSession, credentialID string) error {
	id := newID("ase_")
	_, err := s.db.Exec(`INSERT INTO action_session_events
		(id, tenant_id, store_id, session_id, action_id, action_revision, kind,
		 source, event_id, participation_id, credential_id, content_pack_version, occurred_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, sess.TenantID, sess.StoreID, sess.ID, sess.ActionID, sess.ActionRevision, kind,
		"touch-engine", id, sess.ParticipationID, credentialID, sess.ContentPackVersion,
		rfc3339(s.now()))
	if err != nil {
		return fmt.Errorf("participation: session event: %w", err)
	}
	return nil
}

func (s *Service) countSessionEvents(sessionID string) (int, error) {
	row := s.db.QueryRow(`SELECT COUNT(*) FROM action_session_events WHERE session_id=?`, sessionID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
