package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// 会让这个测试失败的生产改动：store_actions 重新引入 (campaign,kind) 或
// catalog 唯一键、旧数据迁移猜平台、navigate/return 被迁走或删除、
// 迁移不幂等（重复执行翻倍）。

// TestStoreActionsSchemaInstanceModel 锁死实例模型的存储下界：
// action_id 唯一，同店同 catalog+platform+variant 可并存多条，无 kind 唯一键。
func TestStoreActionsSchemaInstanceModel(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	mustExec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','商家','now')`)
	mustExec(`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		VALUES('sto_1','tnt_1','一店','','u','now','now')`)
	mustExec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		content_binding_ref,capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,
		legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_a','tnt_1','sto_1','A08','dianping','review','','点评一','https://www.dianping.com/a','','','link',1,1,1,0,'','','','now','now')`)

	// 同店同 catalog+platform+variant 第二条：必须可并存（五个点评互不覆盖）。
	mustExec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		content_binding_ref,capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,
		legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_b','tnt_1','sto_1','A08','dianping','review','','点评二','https://www.dianping.com/b','','','link',2,1,1,0,'','','','now','now')`)

	// catalog_code CHECK：只接受 '' 或 A[0-9][0-9] 形状。
	if _, err := d.Exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		content_binding_ref,capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,
		legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_x','tnt_1','sto_1','X99','','review','','x','','','','link',3,1,1,0,'','','','now','now')`); err == nil {
		t.Fatal("catalog_code 'X99' must be refused by CHECK")
	}
	if _, err := d.Exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		content_binding_ref,capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,
		legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_y','tnt_1','sto_1','B08','','review','','x','','','','link',3,1,1,0,'','','','now','now')`); err == nil {
		t.Fatal("catalog_code 'B08' must be refused by CHECK")
	}
	// '' = 旧数据待商家映射，可入库。
	mustExec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		content_binding_ref,capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,
		legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_c','tnt_1','sto_1','','','review','','待映射','','','','link',4,1,1,0,'','cmp_1','review','now','now')`)

	// store_code 是 store_storefronts 的 UNIQUE 列，stores 表未被本票改列。
	if _, err := d.Query(`SELECT store_code FROM stores LIMIT 1`); err == nil {
		t.Fatal("stores table must not gain a store_code column (store_code lives in store_storefronts)")
	}
}

// TestStoreActionsLegacyBackfill 走真实 Migrate 路径重放 0044：旧行每行生成
// action_id、catalog 按 kind+域名映射、映射不出留空待采用、campaign 落
// legacy_campaign_id、navigate 与无店活动行不迁、重复执行零新增。
func TestStoreActionsLegacyBackfill(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	mustExec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','老商家','now')`)
	mustExec(`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		VALUES('sto_a','tnt_1','甲店','','u','now','now')`)
	mustExec(`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		VALUES('sto_b','tnt_1','乙店','','u','now','now')`)
	insertCampaign := func(id, storeID string) {
		t.Helper()
		if storeID == "" {
			mustExec(`INSERT INTO campaigns(id,tenant_id,title,status,store_id,created_by,created_at,updated_at)
				VALUES('` + id + `','tnt_1','活动','active',NULL,'u','now','now')`)
			return
		}
		mustExec(`INSERT INTO campaigns(id,tenant_id,title,status,store_id,created_by,created_at,updated_at)
			VALUES('` + id + `','tnt_1','活动','active','` + storeID + `','u','now','now')`)
	}
	insertCampaign("cmp_1", "sto_a")
	insertCampaign("cmp_2", "sto_a")
	insertCampaign("cmp_3", "")      // 无店活动：旧行留旧表，不迁
	insertCampaign("cmp_4", "sto_b") // 第二家店：同平台完全隔离
	jump := func(campaign, kind, href string, enabled int) {
		t.Helper()
		mustExec(`INSERT INTO extra_jump_actions(id,tenant_id,campaign_id,kind,href,enabled,updated_at)
			VALUES('ej_` + campaign + `_` + kind + `','tnt_1','` + campaign + `','` + kind + `','` + href + `',` +
			itoa(enabled) + `,'now')`)
	}
	jump("cmp_1", "wifi", "https://shop.example.com/wifi", 1)
	jump("cmp_1", "review", "https://www.dianping.com/shop/x", 1)
	jump("cmp_1", "wecom", "https://work.weixin.qq.com/ca/y", 1)
	jump("cmp_1", "follow", "https://www.douyin.com/user/z", 0)  // 停用态也要保留
	jump("cmp_2", "review", "https://shop.example.com/other", 1) // 平台推不出：待映射
	jump("cmp_4", "follow", "https://shop.example.com/fb", 1)    // 平台推不出：待映射
	// UNIQUE(campaign_id,kind)：同活动同 kind 只能一行；navigate 留旧路径。
	jump("cmp_1", "navigate", "https://uri.amap.com/marker", 1)

	// 只重放 0044（0043 已随首次 Open 应用）。
	if _, err := d.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0044_store_actions_legacy_backfill.sql'`); err != nil {
		t.Fatalf("unapply 0044: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("re-migrate 0044: %v", err)
	}

	type row struct {
		id, storeID, catalog, platform, kind, variant, label, target, legacyCampaign, legacyKind string
		enabled                                                                                  int
	}
	rows := func() []row {
		t.Helper()
		rs, err := d.Query(`SELECT id,store_id,catalog_code,platform,kind,variant,label,target_ref,
			legacy_campaign_id,legacy_kind,enabled FROM store_actions ORDER BY legacy_campaign_id,legacy_kind`)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rs.Close()
		var out []row
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.id, &r.storeID, &r.catalog, &r.platform, &r.kind, &r.variant, &r.label,
				&r.target, &r.legacyCampaign, &r.legacyKind, &r.enabled); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, r)
		}
		if err := rs.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return out
	}

	got := rows()
	if len(got) != 6 {
		t.Fatalf("backfilled rows = %d, want 6 (navigate 与无店行不迁): %+v", len(got), got)
	}
	want := map[string]struct{ storeID, catalog, platform, variant string }{
		"cmp_1|wifi":   {"sto_a", "A17", "", ""},
		"cmp_1|review": {"sto_a", "A08", "dianping", ""},
		"cmp_1|wecom":  {"sto_a", "A18", "wechat", "wecom"},
		"cmp_1|follow": {"sto_a", "A13", "douyin", ""},
		"cmp_2|review": {"sto_a", "", "", ""},
		"cmp_4|follow": {"sto_b", "", "", ""},
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.legacyCampaign + "|" + r.legacyKind
		if seen[key] {
			t.Fatalf("duplicate legacy mapping %s", key)
		}
		seen[key] = true
		w, ok := want[key]
		if !ok {
			t.Fatalf("unexpected migrated row %s (navigate/return 不得迁移)", key)
		}
		if r.storeID != w.storeID || r.catalog != w.catalog || r.platform != w.platform || r.variant != w.variant {
			t.Fatalf("%s = (%s,%s,%s,%s), want (%s,%s,%s,%s)", key, r.storeID, r.catalog, r.platform, r.variant,
				w.storeID, w.catalog, w.platform, w.variant)
		}
		if !strings.HasPrefix(r.id, "sa_") || len(r.id) < 10 {
			t.Fatalf("action id %q must be a stable opaque sa_ id, not an int", r.id)
		}
		if r.label == "" {
			t.Fatalf("%s label empty", key)
		}
		if r.legacyCampaign == "" {
			t.Fatalf("%s lost legacy_campaign_id", key)
		}
	}
	ids := map[string]bool{}
	for _, r := range got {
		if ids[r.id] {
			t.Fatalf("action id collision %s", r.id)
		}
		ids[r.id] = true
	}
	var enabled int
	if err := d.QueryRow(`SELECT enabled FROM store_actions WHERE legacy_campaign_id='cmp_1' AND legacy_kind='follow'`).Scan(&enabled); err != nil {
		t.Fatalf("follow row missing: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("disabled legacy row migrated as enabled=%d, want 0", enabled)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM extra_jump_actions`).Scan(&n); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if n != 7 {
		t.Fatalf("legacy table rows = %d, want 7 (旧行不删除——旧路径保持兼容)", n)
	}

	// 幂等：再次执行 0044 零新增。
	if _, err := d.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0044_store_actions_legacy_backfill.sql'`); err != nil {
		t.Fatalf("unapply 0044 again: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("re-migrate 0044 again: %v", err)
	}
	if again := rows(); len(again) != 6 {
		t.Fatalf("second backfill rows = %d, want 6 (迁移必须幂等)", len(again))
	}
}

func itoa(n int) string {
	digits := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
