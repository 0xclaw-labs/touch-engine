package db

// HUI-1670 touch-handoff/v2 (0073): publish_handoffs / publish_handoff_events
// 两表重建后的行为锚点。
//
// 纪律:
//   - v2 动作交接合法地无参与(campaign/participation/attempt 可空):
//     A27 纯链接交接不伪建参与行;
//   - 事件 kind CHECK 白名单放行 v2 六层事实
//     (customer_click/wake_attempt/media_handoff/customer_self_report/
//      staff_confirm/platform_confirm),保留 v1 八种;
//   - source+event_id 去重唯一索引:同 (tenant,handoff,source,event_id) 二写
//     必撞唯一约束(应用层转幂等去重/IDEMPOTENCY_CONFLICT);
//   - payload_kind CHECK 只收 ''|media|link_share;
//   - v1 形状行(campaign+participation+attempt 全带)照常可插(/c 兼容)。

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func open0073(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func seed0073Refs(t *testing.T, d *sql.DB) (tenantID, storeID, campaignID, participationID, attemptID string) {
	t.Helper()
	tenantID, storeID, campaignID, participationID, attemptID = "tnt_73", "sto_73", "cmp_73", "prt_73", "atm_73"
	for _, q := range []string{
		`INSERT INTO tenants (id, name, created_at) VALUES ('tnt_73','t','2026-11-10T00:00:00Z')`,
		`INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES ('sto_73','tnt_73','s','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`,
		`INSERT INTO campaigns (id, tenant_id, title, status, store_id, created_at, updated_at)
		 VALUES ('cmp_73','tnt_73','c','active','sto_73','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`,
		`INSERT INTO participations (id, tenant_id, store_id, campaign_id, link_code, touchpoint_source,
		 rule_version, visitor_id, status, idempotency_key, starts_at, expires_at, created_at, updated_at)
		 VALUES ('prt_73','tnt_73','sto_73','cmp_73','c73','nfc', 0, 'vis_73','pending','idem-73-p',
		 '2026-11-10T00:00:00Z','2027-11-10T00:00:00Z','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`,
		`INSERT INTO participation_attempts (id, tenant_id, participation_id, platform, handoff_method,
		 rule_version, status, created_at, updated_at)
		 VALUES ('atm_73','tnt_73','prt_73','douyin','h5', 0,'handed_off','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q[:40], err)
		}
	}
	return tenantID, storeID, campaignID, participationID, attemptID
}

func Test0073LinkShareHandoffWithoutParticipation(t *testing.T) {
	d := open0073(t)
	tenantID, storeID, _, _, _ := seed0073Refs(t, d)
	// v2 link_share 行:无 campaign/参与/尝试,动作会话权威,share_url 冻结。
	_, err := d.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, platform, scenario, idempotency_key, status, evidence_source,
		 visitor_id, action_id, catalog_code, action_session_id, action_revision,
		 payload_kind, method, environment, plan_kind, evidence_level,
		 client_action_json, unsupported_fields_json, share_url, request_hash, gate_json,
		 created_at, updated_at)
		VALUES ('ph_73_link', ?, ?, 'wechat', 'store_promo', 'idem-73', 'handoff_ready', 'system_adapter',
		 'vis_73', 'sa_73', 'A27', 'ases_73', 3,
		 'link_share', 'link_share', 'ios_safari', 'official_handoff', 'none',
		 '{}', '[]', 'https://example.com/store', 'rh_73', '{}',
		 '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`, tenantID, storeID)
	if err != nil {
		t.Fatalf("insert link_share handoff: %v", err)
	}
	var share, plan string
	if err := d.QueryRow(`SELECT share_url, plan_kind FROM publish_handoffs WHERE id='ph_73_link'`).Scan(&share, &plan); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if share != "https://example.com/store" || plan != "official_handoff" {
		t.Fatalf("share=%q plan=%q", share, plan)
	}
}

func Test0073LegacyShapedRowStillInsertable(t *testing.T) {
	d := open0073(t)
	tenantID, storeID, campaignID, participationID, attemptID := seed0073Refs(t, d)
	// v1 形状(/c 参与面):campaign+participation+attempt 全带,照常可插。
	_, err := d.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, campaign_id, link_code, participation_id, attempt_id,
		 platform, entry_mode, scenario, idempotency_key, status, evidence_source,
		 content_version, created_at, updated_at)
		VALUES ('ph_73_legacy', ?, ?, ?, 'c73', ?, ?,
		 'douyin', 'h5', 'store_promo', 'idem-legacy', 'handoff_ready', 'system_adapter',
		 'cv', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`,
		tenantID, storeID, campaignID, participationID, attemptID)
	if err != nil {
		t.Fatalf("insert legacy-shaped handoff: %v", err)
	}
}

func Test0073PayloadKindCheckRejectsGarbage(t *testing.T) {
	d := open0073(t)
	tenantID, storeID, _, _, _ := seed0073Refs(t, d)
	_, err := d.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, platform, scenario, idempotency_key, status, evidence_source,
		 payload_kind, created_at, updated_at)
		VALUES ('ph_73_bad', ?, ?, 'douyin', 'store_promo', 'idem-bad', 'previewed', 'customer_action',
		 'auto_video', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`, tenantID, storeID)
	if err == nil || !strings.Contains(err.Error(), "payload_kind") {
		t.Fatalf("payload_kind CHECK did not reject: %v", err)
	}
}

func Test0073V2EventKindsAcceptedAndDedupIndexFires(t *testing.T) {
	d := open0073(t)
	tenantID, storeID, _, _, _ := seed0073Refs(t, d)
	if _, err := d.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, platform, scenario, idempotency_key, status, evidence_source,
		 payload_kind, created_at, updated_at)
		VALUES ('ph_73_ev', ?, ?, 'douyin', 'store_promo', 'idem-ev', 'handoff_ready', 'system_adapter',
		 'media', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`, tenantID, storeID); err != nil {
		t.Fatalf("seed handoff: %v", err)
	}
	for _, kind := range []string{
		"customer_click", "wake_attempt", "media_handoff",
		"customer_self_report", "staff_confirm", "platform_confirm",
	} {
		if _, err := d.Exec(`INSERT INTO publish_handoff_events
			(id, tenant_id, handoff_id, kind, evidence_source, source, event_id, created_at)
			VALUES (?, ?, 'ph_73_ev', ?, 'customer_action', 'customer_page', 'ev-'||?, ?)`,
			"phe_"+kind, tenantID, kind, kind, "2026-11-10T00:00:00Z"); err != nil {
			t.Fatalf("insert v2 event kind %s: %v", kind, err)
		}
	}
	// 同 source+event_id 二写:唯一索引必须拒绝(应用层据此做去重/冲突)。
	_, err := d.Exec(`INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, evidence_source, source, event_id, created_at)
		VALUES ('phe_dup', ?, 'ph_73_ev', 'customer_click', 'customer_action', 'customer_page', 'ev-customer_click', '2026-11-10T00:01:00Z')`, tenantID)
	if err == nil {
		t.Fatalf("duplicate source+event_id was accepted; dedup index missing")
	}
	// v1 kind 仍被接受(兼容)。
	if _, err := d.Exec(`INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, evidence_source, created_at)
		VALUES ('phe_v1', ?, 'ph_73_ev', 'official_confirmed', 'official_receipt', '2026-11-10T00:02:00Z')`, tenantID); err != nil {
		t.Fatalf("v1 kind rejected: %v", err)
	}
	// platform_confirm 的 receipt_id 部分唯一:同回执 ID 二写拒绝。
	sig := "2026-11-10T00:03:00Z"
	if _, err := d.Exec(`INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, evidence_source, receipt_id, created_at)
		VALUES ('phe_pc1', ?, 'ph_73_ev', 'platform_confirm', 'official_receipt', 'rcp_x', ?)`, tenantID, sig); err != nil {
		t.Fatalf("platform_confirm insert: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, evidence_source, receipt_id, created_at)
		VALUES ('phe_pc2', ?, 'ph_73_ev', 'platform_confirm', 'official_receipt', 'rcp_x', ?)`, tenantID, sig); err == nil {
		t.Fatalf("duplicate platform_confirm receipt_id accepted; partial unique index missing")
	}
}
