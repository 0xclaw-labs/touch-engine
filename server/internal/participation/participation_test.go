package participation

// HUI-3546 碰一碰门店交付 A2: 参与记录与跨端恢复。
//
// 纪律(票面红线,全文见 docs/superpowers/specs/2026-10-14-hui-3546-design.md):
//   - 浏览不建参与;同一幂等键重放回原记录;
//   - 交接时冻结 attempt 的内容包版本与规则版本;换稿/换平台 = 新 attempt,
//     不新增 benefit_slot;
//   - 交接凭据短时/单用途/可撤销,防枚举(128bit 随机 + 校验位),库内只存哈希;
//   - 未确认预留不得向顾客返回"已保留";奖励归 HUI-1671,本包只留受信端口;
//   - 凭据明文与参与档案不进公共日志。

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// openTestDB opens a real sqlite file database (not in-memory: the restart
// acceptance needs a path) with all embedded migrations applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "participation.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// seedRef inserts the minimal tenant/store/campaign rows every participation
// row references (foreign keys are enforced).
func seedRef(t *testing.T, d *sql.DB) (tenantID, storeID, campaignID string) {
	t.Helper()
	tenantID, storeID, campaignID = "tnt_seed", "sto_seed", "cmp_seed"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants (id, name, created_at) VALUES (?,?,?)`, []any{tenantID, "seed-tenant", "2026-10-14T00:00:00Z"}},
		{`INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES (?,?,?,?,?)`,
			[]any{storeID, tenantID, "seed-store", "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}},
		{`INSERT INTO campaigns (id, tenant_id, title, status, store_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			[]any{campaignID, tenantID, "seed-campaign", "active", storeID, "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return tenantID, storeID, campaignID
}

// T1: 迁移 0020/0021/0022 的 additive 语义。
func TestMigrationsCreateParticipationSchema(t *testing.T) {
	d := openTestDB(t)
	tenantID, storeID, campaignID := seedRef(t, d)

	// participations: 幂等键唯一。
	const insertParticipation = `INSERT INTO participations
		(id, tenant_id, store_id, campaign_id, link_code, touchpoint_source, rule_version,
		 content_pack_version, visitor_id, status, idempotency_key, benefit_slot,
		 benefit_reservation_state, starts_at, expires_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?, 'v1', ?, 'pending', ?, ?, 'not_requested', ?, ?, ?, ?)`
	participationArgs := func(id, visitor, idem string) []any {
		return []any{id, tenantID, storeID, campaignID, "link01", "qr", 1, visitor, idem, id,
			"2026-10-14T00:00:00Z", "2026-10-21T00:00:00Z", "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}
	}
	if _, err := d.Exec(insertParticipation, participationArgs("pt_a", "visit_a", "pt_a")...); err != nil {
		t.Fatalf("insert participation: %v", err)
	}
	if _, err := d.Exec(insertParticipation, participationArgs("pt_b", "visit_b", "pt_a")...); err == nil {
		t.Fatal("duplicate (tenant, campaign, idempotency_key) must be rejected")
	}

	// 状态 CHECK:未知状态拒绝。
	if _, err := d.Exec(`UPDATE participations SET status='success' WHERE id='pt_a'`); err == nil {
		t.Fatal("single success boolean must not be a legal status")
	}

	// attempts: 冻结版本行。
	if _, err := d.Exec(`INSERT INTO participation_attempts
		(id, tenant_id, participation_id, platform, handoff_method, content_pack_version, rule_version, status, created_at, updated_at)
		VALUES ('pta_a', ?, 'pt_a', 'douyin', 'h5', 'v1', 1, 'prepared', ?, ?)`,
		tenantID, "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	if _, err := d.Exec(`UPDATE participation_attempts SET status='published' WHERE id='pta_a'`); err == nil {
		t.Fatal("publish outcome states belong to HUI-1670/1671 domains, not attempts")
	}

	// credentials: token 哈希唯一;明文列不存在。
	if _, err := d.Exec(`INSERT INTO handoff_credentials
		(id, tenant_id, participation_id, attempt_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('phc_a', ?, 'pt_a', 'pta_a', 'customer_resume', 'h5', 'deadbeef', ?, ?)`,
		tenantID, "2026-10-14T00:10:00Z", "2026-10-14T00:00:00Z"); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO handoff_credentials
		(id, tenant_id, participation_id, attempt_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('phc_b', ?, 'pt_a', 'pta_a', 'customer_resume', 'h5', 'deadbeef', ?, ?)`,
		tenantID, "2026-10-14T00:10:00Z", "2026-10-14T00:00:00Z"); err == nil {
		t.Fatal("token_hash must be unique")
	}
	if _, err := d.Exec(`INSERT INTO handoff_credentials
		(id, tenant_id, participation_id, attempt_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('phc_c', ?, 'pt_a', 'pta_a', 'customer_resume', 'h5', 'cafebabe', ?, ?)`,
		tenantID, "2026-10-14T00:10:00Z", "2026-10-14T00:00:00Z"); err != nil {
		t.Fatalf("second distinct credential: %v", err)
	}
}
