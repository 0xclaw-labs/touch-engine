package appjump

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`INSERT INTO tenants (id, name, created_at) VALUES ('tnt_a', '组织A', '2026-11-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant a: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO tenants (id, name, created_at) VALUES ('tnt_b', '组织B', '2026-11-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant b: %v", err)
	}
	svc := New(d, Options{TTL: time.Minute})
	return svc, d
}

const (
	tenantA  = "tnt_a"
	tenantB  = "tnt_b"
	prinA    = "usr_owner"
	prinB    = "usr_other"
	appID    = "leads"
	targetID = "ti-leads-web"
	launch   = "https://leads.modelxing.com/in?code=one-time"
)

func TestTokenDisciplineMirrors3546(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if !strings.HasPrefix(tok, "jtg_") {
		t.Fatalf("token prefix = %q, want jtg_", tok[:8])
	}
	if !ValidTokenShape(tok) {
		t.Fatalf("fresh token must verify its checksum")
	}
	if ValidTokenShape("jtg_not-a-real-token") || ValidTokenShape("ptx_foreign_prefix.aa") || ValidTokenShape("") {
		t.Fatalf("foreign/malformed shapes must fail the checksum prefilter")
	}
	// 高熵:两次 mint 不重号(128-bit 随机的平凡性质,钉住实现别退化成确定性串)。
	other, err := NewToken()
	if err != nil {
		t.Fatalf("mint second token: %v", err)
	}
	if other == tok {
		t.Fatalf("two mints produced the same token")
	}
	if HashToken(tok) == HashToken(other) {
		t.Fatalf("distinct tokens must not collide in storage hash")
	}
}

func TestMintStoresHashOnlyAndRecordsEvent(t *testing.T) {
	svc, d := newTestService(t)
	minted, err := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if minted.Token == "" || !strings.Contains(minted.Token, "jtg_") {
		t.Fatalf("mint must return the plaintext token once")
	}
	var storedHash, storedURL string
	if err := d.QueryRow(`SELECT token_hash, launch_url FROM sso_jump_grants WHERE id=?`, minted.Grant.ID).
		Scan(&storedHash, &storedURL); err != nil {
		t.Fatalf("load grant: %v", err)
	}
	if storedHash == minted.Token {
		t.Fatalf("plaintext token must never touch the database")
	}
	if storedHash != HashToken(minted.Token) {
		t.Fatalf("storage hash mismatch")
	}
	if storedURL != launch {
		t.Fatalf("launch_url must be kept verbatim for the single redemption")
	}
	var kind string
	if err := d.QueryRow(`SELECT kind FROM sso_jump_events WHERE grant_id=?`, minted.Grant.ID).Scan(&kind); err != nil {
		t.Fatalf("mint event missing: %v", err)
	}
	if kind != "minted" {
		t.Fatalf("mint event kind = %q", kind)
	}
}

func TestRedeemIsSingleUseAndBindsPrincipalTenant(t *testing.T) {
	svc, d := newTestService(t)
	minted, err := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	got, err := svc.Redeem(minted.Token, prinA, tenantA)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got.LaunchURL != launch || got.AppID != appID || got.LaunchTargetID != targetID {
		t.Fatalf("redeem returned wrong grant payload: %+v", got)
	}
	var kind string
	if err := d.QueryRow(`SELECT kind FROM sso_jump_events WHERE grant_id=? AND kind='redeemed'`, minted.Grant.ID).Scan(&kind); err != nil {
		t.Fatalf("redeem event missing: %v", err)
	}

	// 单用途:同 token 第二次兑换拒绝,launch_url 不再交还。
	if _, err := svc.Redeem(minted.Token, prinA, tenantA); err != ErrUsed {
		t.Fatalf("second redeem err = %v, want ErrUsed", err)
	}

	// 绑定 mint 时的 principal+tenant:换人/换组织出示一律拒绝。
	m2, _ := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if _, err := svc.Redeem(m2.Token, prinB, tenantA); err != ErrNotFound {
		t.Fatalf("cross-principal redeem err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Redeem(m2.Token, prinA, tenantB); err != ErrNotFound {
		t.Fatalf("cross-tenant redeem err = %v, want ErrNotFound", err)
	}
	// 被跨租户出示拒绝的凭据仍可由本人兑换(拒绝不烧单用途,3546 同语义)。
	if _, err := svc.Redeem(m2.Token, prinA, tenantA); err != nil {
		t.Fatalf("rejected presentment must not burn the grant: %v", err)
	}
}

func TestRedeemExpiredAnswersGone(t *testing.T) {
	svc, _ := newTestService(t)
	svc.now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	minted, err := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// 快进 61s:过 TTL。
	svc.now = func() time.Time { return time.Date(2026, 11, 10, 12, 1, 1, 0, time.UTC) }
	if _, err := svc.Redeem(minted.Token, prinA, tenantA); err != ErrExpired {
		t.Fatalf("expired redeem err = %v, want ErrExpired", err)
	}
}

func TestRedeemMalformedShapeIsUniformNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.Redeem("jtg_garbage.aa", prinA, tenantA); err != ErrNotFound {
		t.Fatalf("malformed redeem err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Redeem("ptx_wrong_prefix.aa", prinA, tenantA); err != ErrNotFound {
		t.Fatalf("foreign prefix err = %v, want ErrNotFound", err)
	}
}

func TestRevokeCancelsUnusedGrant(t *testing.T) {
	svc, _ := newTestService(t)
	minted, err := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := svc.Revoke(minted.Grant.ID, prinA, tenantA); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.Redeem(minted.Token, prinA, tenantA); err != ErrRevoked {
		t.Fatalf("redeem after revoke err = %v, want ErrRevoked", err)
	}
	// 他人撤销:拒绝(NotFound 同形),凭据不烧。
	m2, _ := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if err := svc.Revoke(m2.Grant.ID, prinB, tenantA); err != ErrNotFound {
		t.Fatalf("foreign revoke err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Redeem(m2.Token, prinA, tenantA); err != nil {
		t.Fatalf("foreign revoke must not burn the grant: %v", err)
	}
}

func TestConcurrentRedeemHasExactlyOneWinner(t *testing.T) {
	svc, _ := newTestService(t)
	minted, err := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	const racers = 8
	results := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Redeem(minted.Token, prinA, tenantA)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent redeem winners = %d, want exactly 1", winners)
	}
}

func TestCleanupRemovesExpiredAndUsedPastGrace(t *testing.T) {
	svc, d := newTestService(t)
	svc.now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	expired, _ := svc.Mint(tenantA, prinA, appID, targetID, launch)
	used, _ := svc.Mint(tenantA, prinA, appID, targetID, launch)
	if _, err := svc.Redeem(used.Token, prinA, tenantA); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	// fresh mint 晚两小时,清理时刻仍未过期。
	svc.now = func() time.Time { return time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC) }
	fresh, _ := svc.Mint(tenantA, prinA, appID, targetID, launch)

	// grace 内:一律保留(争议期证据)。
	svc.now = func() time.Time { return time.Date(2026, 11, 10, 13, 30, 0, 0, time.UTC) }
	deleted, err := svc.Cleanup(svc.now(), credentialGrace)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("cleanup inside grace deleted %d rows, want 0", deleted)
	}

	// grace 外:过期+已用清掉,未过期 fresh 保留(过期 12:01 + 24h 争议期后)。
	svc.now = func() time.Time { return time.Date(2026, 11, 11, 13, 30, 0, 0, time.UTC) }
	deleted, err = svc.Cleanup(svc.now(), credentialGrace)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("cleanup deleted %d rows, want 2", deleted)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sso_jump_grants WHERE id IN (?,?,?)`, expired.Grant.ID, used.Grant.ID, fresh.Grant.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("only the fresh unexpired grant must survive cleanup, got %d", n)
	}
}
