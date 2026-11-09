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

// sessionsReady reports whether the directory port is wired.
func (s *Service) sessionsReady() bool { return s.dir != nil }

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
	if !s.sessionsReady() {
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
	if !s.sessionsReady() {
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
func (s *Service) CancelActionSession(_ context.Context, id, visitorID string) (ActionSession, error) {
	sess, err := s.getSessionForVisitor(context.Background(), id, visitorID)
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

// ---- session lookups ------------------------------------------------------------

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
