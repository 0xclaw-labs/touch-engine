package participation

// HUI-3546 增量轮(2026-11-09): 匿名动作会话(action_sessions)与跨端恢复。
//
// 纪律(裁定书红线):
//   - action_sessions 是匿名权威:不建生态账号、不留手机号;无活动/活动结束
//     照建会话,绝不伪造 campaign id;
//   - 创建时冻结 storefront/action revision + 规则快照 + 内容引用;
//     停用/撤权是独立实时检查,快照不掩盖当前状态;
//   - 恢复凭据短时(≤15min)/单用途/服务端哈希存储,明文永不进 URL/日志;
//     店员码(staff_verify)结构性进不了会话兑换面;
//   - 只有显式办理权益才升级建 participation(既有语义不变);
//   - 全部凭据丢失不可认回:无指纹、无手机号、无顺序 ID 后门。

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// seedSessionRef: 会话行只需 tenant/store(动作维度,无需 campaign)。
func seedSessionRef(t *testing.T, d *sql.DB) (tenantID, storeID string) {
	t.Helper()
	tenantID, storeID = "tnt_act", "sto_act"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants (id, name, created_at) VALUES (?,?,?)`, []any{tenantID, "act-tenant", "2026-11-10T00:00:00Z"}},
		{`INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES (?,?,?,?,?)`,
			[]any{storeID, tenantID, "act-store", "2026-11-10T00:00:00Z", "2026-11-10T00:00:00Z"}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return tenantID, storeID
}

// T1: 迁移 0056/0057/0058 的 additive 语义与约束。
func TestMigrationsCreateActionSessionSchema(t *testing.T) {
	d := openTestDB(t)
	tenantID, storeID := seedSessionRef(t, d)
	nowT := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	now := nowT.Format(time.RFC3339)

	// 0056: 会话表存在且状态 CHECK 收紧。
	if _, err := d.Exec(`INSERT INTO action_sessions
		(id, tenant_id, store_id, action_id, storefront_revision, action_revision,
		 rule_version, rule_snapshot, content_pack_version, visitor_id, status, created_at, updated_at)
		VALUES ('as_ok',?,?,?,?,?,?,?,?,'visit_a','active',?,?)`,
		tenantID, storeID, "act_001", 3, 7, 1, "snap", "cp_v1", now, now); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO action_sessions
		(id, tenant_id, store_id, action_id, visitor_id, status, created_at, updated_at)
		VALUES ('as_bad',?,?,?,?,?,?,?)`,
		tenantID, storeID, "act_001", "visit_a", "bogus", now, now); err == nil {
		t.Fatal("status CHECK must reject unknown states")
	}
	// 跨租户 FK。
	if _, err := d.Exec(`INSERT INTO action_sessions
		(id, tenant_id, store_id, action_id, visitor_id, status, created_at, updated_at)
		VALUES ('as_x','tnt_missing',?,?,?,?,?,?)`,
		storeID, "act_001", "visit_a", "active", now, now); err == nil {
		t.Fatal("tenant FK must be enforced")
	}
	// 幂等键唯一(tenant, action, key):首行键为默认 ''(无键创建),同键再插被拒。
	if _, err := d.Exec(`INSERT INTO action_sessions
		(id, tenant_id, store_id, action_id, idempotency_key, visitor_id, status, created_at, updated_at)
		VALUES ('as_dup',?,'','','',?,?,?,?)`,
		tenantID, storeID, "act_001", "visit_b", "active", now, now); err == nil {
		t.Fatal("UNIQUE(tenant,action,idempotency_key) must reject duplicates")
	}

	// 0057: 凭据表 purpose 只允许 customer_resume(店员码结构性进不了)。
	tok := mustToken(t)
	if _, err := d.Exec(`INSERT INTO action_session_credentials
		(id, tenant_id, session_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('asc_ok',?,'as_ok','customer_resume','h5',?,?,?)`,
		tenantID, HashToken(tok), nowT.Add(10*time.Minute).Format(time.RFC3339), now); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO action_session_credentials
		(id, tenant_id, session_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('asc_staff',?,'as_ok','staff_verify','h5',?,?,?)`,
		tenantID, HashToken(tok+"x"), now, now); err == nil {
		t.Fatal("purpose CHECK must reject staff_verify on session credentials")
	}
	if _, err := d.Exec(`INSERT INTO action_session_credentials
		(id, tenant_id, session_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('asc_dup',?,'as_ok','customer_resume','h5',?,?,?)`,
		tenantID, HashToken(tok), now, now); err == nil {
		t.Fatal("token_hash UNIQUE must reject duplicates")
	}

	// 0058: 事件表 source+event_id 去重。
	ev := `INSERT INTO action_session_events
		(id, tenant_id, store_id, session_id, action_id, kind, source, event_id, occurred_at)
		VALUES (?,?,?,?,?,?,?,?,?)`
	if _, err := d.Exec(ev, "ase_1", tenantID, storeID, "as_ok", "act_001", "created", "touch-engine", "evt_1", now); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := d.Exec(ev, "ase_2", tenantID, storeID, "as_ok", "act_001", "resumed", "touch-engine", "evt_1", now); err == nil {
		t.Fatal("UNIQUE(source,event_id) must reject duplicate events")
	}
}

func mustToken(t *testing.T) string {
	t.Helper()
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if !strings.HasPrefix(tok, "ptx_") {
		t.Fatalf("token prefix = %q", tok)
	}
	return tok
}
