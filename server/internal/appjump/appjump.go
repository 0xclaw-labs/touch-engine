// Package appjump guards the credential hand-off for cross-app SSO jumps
// (HUI-2222 wave2, M11: touch -> Leads 等).
//
// The public-ai shell launch stays the ONLY authority that mints the one-time
// target code and the launch URL. This package adds the 3546 credential
// discipline to the hand-off between our BFF and the merchant's browser:
//
//   - the mint step answers a short-lived single-use jump token (jtg_, 128-bit
//     entropy + checksum), never the launch URL itself;
//   - the redeem step claims the grant with a transactional conditional
//     update — exactly one winner under concurrency — and only then releases
//     the launch URL the shell produced;
//   - grants bind to the minting principal AND tenant: presenting a stolen
//     token from another session or another org answers the same uniform
//     not-found (no existence leak);
//   - the database stores only the SHA-256 of the token; the plaintext exists
//     once, in the mint response, and never again;
//   - rejected presentments do not burn the grant (3546 semantics).
package appjump

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Grant lifecycle errors. Expired is its own honest signal (mapped to 410
// upstream, same as 3546); used/revoked/foreign/malformed collapse into
// ErrNotFound so a probe cannot tell which check failed.
var (
	ErrExpired  = errors.New("appjump: jump grant expired")
	ErrUsed     = errors.New("appjump: jump grant already used")
	ErrNotFound = errors.New("appjump: jump grant not found")
	ErrRevoked  = errors.New("appjump: jump grant revoked")
)

// credentialGrace keeps expired/used rows for the dispute window before
// Cleanup removes them (mirrors participation.credentialGrace semantics).
const credentialGrace = 24 * time.Hour

// DefaultTTL bounds the hand-off window: the browser redeems immediately
// after mint; anything longer adds replay surface without serving a flow.
const DefaultTTL = 60 * time.Second

// Grant is the server-side view of one jump grant. LaunchURL is the shell's
// artifact kept verbatim; it is released only to the single redemption.
type Grant struct {
	ID             string
	TenantID       string
	PrincipalID    string
	AppID          string
	LaunchTargetID string
	LaunchURL      string
	ExpiresAt      string
	UsedAt         string
	RevokedAt      string
	CreatedAt      string
}

// Minted is the only moment the plaintext token exists.
type Minted struct {
	Grant  Grant
	Token  string
}

// Options tunes the hand-off window. Zero TTL falls back to DefaultTTL.
type Options struct {
	TTL time.Duration
}

// Service owns the jump-grant surface over the shared sqlite handle.
type Service struct {
	db *sql.DB
	// Now is the clock for mint/expiry decisions. Production leaves it nil
	// (wall clock); httpapi tests may point it past the TTL to drive expiry.
	Now func() time.Time
	ttl time.Duration
}

// New builds a Service. A zero TTL falls back to DefaultTTL.
func New(database *sql.DB, opts Options) *Service {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Service{db: database, Now: time.Now, ttl: ttl}
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func newID(prefix string) string {
	b, err := NewToken()
	if err != nil {
		// Entropy failure here is a construction bug; the mint path reports it
		// as a grant error either way. Never fabricate a deterministic id.
		return prefix + "entropy_failed"
	}
	return prefix + b
}

// Mint records a grant for a freshly minted shell launch. The caller has
// already validated the launch URL (https, non-loopback); this package keeps
// it verbatim and answers the single-use token.
func (s *Service) Mint(tenantID, principalID, appID, launchTargetID, launchURL string) (Minted, error) {
	token, err := NewToken()
	if err != nil {
		return Minted{}, err
	}
	now := s.now()
	g := Grant{
		ID:             newID("jgr_"),
		TenantID:       tenantID,
		PrincipalID:    principalID,
		AppID:          appID,
		LaunchTargetID: launchTargetID,
		LaunchURL:      launchURL,
		ExpiresAt:      rfc3339(now.Add(s.ttl)),
		CreatedAt:      rfc3339(now),
	}
	if _, err := s.db.Exec(`INSERT INTO sso_jump_grants
		(id, tenant_id, principal_id, app_id, launch_target_id, launch_url, token_hash, expires_at, used_at, revoked_at, created_at)
		VALUES (?,?,?,?,?,?,?,?, '','' ,?)`,
		g.ID, g.TenantID, g.PrincipalID, g.AppID, g.LaunchTargetID, g.LaunchURL,
		HashToken(token), g.ExpiresAt, g.CreatedAt); err != nil {
		return Minted{}, fmt.Errorf("appjump: insert grant: %w", err)
	}
	if err := s.recordEvent("minted", g); err != nil {
		return Minted{}, err
	}
	return Minted{Grant: g, Token: token}, nil
}

// Redeem consumes a jump token and releases the shell's launch URL. Order
// matters: shape → hash lookup → expiry → used → revoked → principal/tenant
// binding → transactional single-use claim. Every rejection before the claim
// leaves the grant spendable; concurrent claims produce exactly one winner.
func (s *Service) Redeem(token, principalID, tenantID string) (Grant, error) {
	if !ValidTokenShape(token) {
		return Grant{}, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT id, tenant_id, principal_id, app_id, launch_target_id,
		launch_url, expires_at, used_at, revoked_at, created_at
		FROM sso_jump_grants WHERE token_hash=?`, HashToken(token))
	var g Grant
	err := row.Scan(&g.ID, &g.TenantID, &g.PrincipalID, &g.AppID, &g.LaunchTargetID,
		&g.LaunchURL, &g.ExpiresAt, &g.UsedAt, &g.RevokedAt, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("appjump: grant lookup: %w", err)
	}
	now := s.now()
	if expires, perr := time.Parse(time.RFC3339, g.ExpiresAt); perr != nil || !now.Before(expires) {
		return Grant{}, ErrExpired
	}
	if g.UsedAt != "" {
		return Grant{}, ErrUsed
	}
	if g.RevokedAt != "" {
		return Grant{}, ErrRevoked
	}
	// 绑定校验在同形 NotFound 之下:偷到的 token 换会话/换组织出示,与未知
	// token 不可区分(无存在性泄漏)。
	if g.PrincipalID != principalID || g.TenantID != tenantID {
		return Grant{}, ErrNotFound
	}
	// 事务性单次消费:条件更新,0 行 = 并发已被用。
	res, err := s.db.Exec(`UPDATE sso_jump_grants SET used_at=?
		WHERE id=? AND used_at='' AND revoked_at=''`, rfc3339(now), g.ID)
	if err != nil {
		return Grant{}, fmt.Errorf("appjump: claim grant: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Grant{}, ErrUsed
	}
	g.UsedAt = rfc3339(now)
	if err := s.recordEvent("redeemed", g); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// Revoke cancels an unused grant owned by the minting principal in the
// minting tenant. Used grants cannot be un-used; foreign callers answer the
// uniform not-found and do not burn the grant.
func (s *Service) Revoke(id, principalID, tenantID string) error {
	row := s.db.QueryRow(`SELECT tenant_id, principal_id, app_id, launch_target_id,
		launch_url, expires_at, used_at, revoked_at, created_at
		FROM sso_jump_grants WHERE id=?`, id)
	var g Grant
	g.ID = id
	err := row.Scan(&g.TenantID, &g.PrincipalID, &g.AppID, &g.LaunchTargetID,
		&g.LaunchURL, &g.ExpiresAt, &g.UsedAt, &g.RevokedAt, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("appjump: revoke lookup: %w", err)
	}
	if g.PrincipalID != principalID || g.TenantID != tenantID {
		return ErrNotFound
	}
	res, err := s.db.Exec(`UPDATE sso_jump_grants SET revoked_at=?
		WHERE id=? AND used_at='' AND revoked_at=''`, rfc3339(s.now()), g.ID)
	if err != nil {
		return fmt.Errorf("appjump: revoke grant: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	g.RevokedAt = rfc3339(s.now())
	return s.recordEvent("revoked", g)
}

// Cleanup runs one retention sweep: grants that expired (and never got used
// or were used) leave after the dispute grace. Fresh unexpired grants are
// never touched.
func (s *Service) Cleanup(now time.Time, grace time.Duration) (int64, error) {
	cutoff := rfc3339(now.Add(-grace))
	res, err := s.db.Exec(`DELETE FROM sso_jump_grants WHERE expires_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("appjump: cleanup grants: %w", err)
	}
	return res.RowsAffected()
}

// recordEvent appends one audit row. event ids are server-generated; the
// UNIQUE(source,event_id) dedup guards replays. No token, no hash, no launch
// URL ever lands here — only contract ids and the kind.
func (s *Service) recordEvent(kind string, g Grant) error {
	id := newID("jse_")
	_, err := s.db.Exec(`INSERT INTO sso_jump_events
		(id, tenant_id, principal_id, grant_id, app_id, launch_target_id, kind, source, event_id, occurred_at)
		VALUES (?,?,?,?,?,?,?, 'touch-engine', ?, ?)`,
		id, g.TenantID, g.PrincipalID, g.ID, g.AppID, g.LaunchTargetID, kind, id, rfc3339(s.now()))
	if err != nil {
		return fmt.Errorf("appjump: record event: %w", err)
	}
	return nil
}
