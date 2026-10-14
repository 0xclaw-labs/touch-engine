package publishhandoff

// HUI-1670 碰一碰门店交付 B1: 顾客发布交接(真实渠道交接与恢复)。
//
// 纪律(票面红线 + design spec):
//   - 交接前强制过 channeladmission.Gate;未知/未准入路线不得显示可执行;
//   - 状态机九态,每次迁移带证据来源;窗口失焦/自报/点击不是 editor_opened
//     或 platform_confirmed 的证据;
//   - 三种事实(staff_saw_submit/staff_verified_work/official_confirmed)
//     分别落库,互不转换;人工永不转官方;
//   - 重放/乱序/超时先回查同一次交接,不盲目重投;
//   - 本包零平台外呼、零付费生成调用(结构性保证)。

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// openTestDB opens a real sqlite file database (not in-memory: the restart
// recovery acceptance needs a path) with all embedded migrations applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "publishhandoff.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// seedRef inserts the minimal tenant/store/campaign rows every handoff row
// references (foreign keys are enforced).
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

// seedParticipation inserts one participation + one handed-off attempt row so
// handoff rows can reference them. Returns (participationID, attemptID).
func seedParticipation(t *testing.T, d *sql.DB, tenantID, storeID, campaignID, suffix string) (string, string) {
	t.Helper()
	ptID, atID := "pt_"+suffix, "pta_"+suffix
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO participations
			(id, tenant_id, store_id, campaign_id, link_code, touchpoint_source, rule_version,
			 content_pack_version, visitor_id, status, idempotency_key, benefit_slot,
			 benefit_reservation_state, starts_at, expires_at, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,'in_progress',?,?,'not_requested',?,?,?,?)`,
			[]any{ptID, tenantID, storeID, campaignID, "link01", "qr", 1, "v1", "visit_" + suffix,
				"idem_" + suffix, ptID,
				"2026-10-14T00:00:00Z", "2026-10-21T00:00:00Z", "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}},
		{`INSERT INTO participation_attempts
			(id, tenant_id, participation_id, platform, handoff_method, content_pack_version, rule_version, status, created_at, updated_at)
			VALUES (?,?,?,?,'h5','v1',1,'handed_off',?,?)`,
			[]any{atID, tenantID, ptID, "xiaohongshu", "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed participation: %v", err)
		}
	}
	return ptID, atID
}

// T1: 迁移 0034/0035 的 additive 语义。
func TestMigrationsCreatePublishHandoffSchema(t *testing.T) {
	d := openTestDB(t)
	tenantID, storeID, campaignID := seedRef(t, d)
	ptID, atID := seedParticipation(t, d, tenantID, storeID, campaignID, "a")

	// publish_handoffs: 幂等键租户内唯一;状态 CHECK 九态;证据来源必填。
	const insertHandoff = `INSERT INTO publish_handoffs
		(id, tenant_id, store_id, campaign_id, link_code, participation_id, attempt_id,
		 platform, entry_mode, scenario, idempotency_key, status, evidence_source,
		 content_version, content_pack_version_id, visitor_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	handoffArgs := func(id, idem, status string) []any {
		return []any{id, tenantID, storeID, campaignID, "link01", ptID, atID,
			"xiaohongshu", "miniapp", "store_promo", idem, status, "customer_action",
			"cv1", "cpv1", "visit_a", "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}
	}
	if _, err := d.Exec(insertHandoff, handoffArgs("ph_a", "idem_a", "handoff_ready")...); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}
	if _, err := d.Exec(insertHandoff, handoffArgs("ph_b", "idem_a", "previewed")...); err == nil {
		t.Fatal("duplicate (tenant, idempotency_key) must be rejected")
	}
	if _, err := d.Exec(`UPDATE publish_handoffs SET status='success' WHERE id='ph_a'`); err == nil {
		t.Fatal("single success boolean must not be a legal handoff status")
	}
	if _, err := d.Exec(`UPDATE publish_handoffs SET status='published' WHERE id='ph_a'`); err == nil {
		t.Fatal("published must not be a handoff status; platform_confirmed is the only confirmed state")
	}
	if _, err := d.Exec(`UPDATE publish_handoffs SET evidence_source='' WHERE id='ph_a'`); err == nil {
		t.Fatal("evidence_source is mandatory on every handoff row")
	}

	// publish_handoff_events: kind CHECK 白名单;receipt_id 防重放唯一。
	const insertEvent = `INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, actor, evidence_source, receipt_id, payload, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`
	eventArgs := func(id, kind, receipt string) []any {
		return []any{id, tenantID, "ph_a", kind, "customer", "customer_action", receipt, "{}", "2026-10-14T00:01:00Z"}
	}
	if _, err := d.Exec(insertEvent, eventArgs("phe_a", "customer_done_requested", "")...); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := d.Exec(insertEvent, eventArgs("phe_b", "platform_confirmed", "rcp_1")...); err != nil {
		t.Fatalf("insert official event: %v", err)
	}
	if _, err := d.Exec(insertEvent, eventArgs("phe_c", "platform_confirmed", "rcp_1")...); err == nil {
		t.Fatal("receipt_id must be unique (replay protection at the storage layer)")
	}
	if _, err := d.Exec(insertEvent, eventArgs("phe_d", "we_just_made_this_up", "")...); err == nil {
		t.Fatal("event kinds outside the whitelist must be rejected")
	}
	// 人工事实不能改写成官方事实(事件表只追加,连 UPDATE 语义都不存在——
	// 这里验证 kind 约束阻止"直接写一条 official 顶替人工"的白名单漏洞)。
	if _, err := d.Exec(`UPDATE publish_handoff_events SET kind='official_confirmed' WHERE id='phe_a'`); err == nil {
		t.Fatal("human facts must never be rewritten into official facts")
	}
}
