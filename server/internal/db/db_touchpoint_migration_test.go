package db

// HUI-1665 Wave2: 触点 store 根目标迁移测试 —— 全部走真实 Migrate 路径重放(参照 0044 先例)。
//
// 会让这些测试失败的生产改动：
//   - target_type/binding_revision 缺列、或 CHECK 放开(坏目标类型/0 revision 可入库)；
//   - campaign_id 仍 NOT NULL(store 根触点不带活动注记不可入库)；
//   - 0103 表重建丢行/丢审计事件/丢换绑 revision(defer_foreign_keys 路径破坏)；
//   - 0104 反查越租户、覆盖已有 store_id、无店活动行被猜门店、或重放不幂等。

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func tpMigSeedTenant(t *testing.T, d *sql.DB) {
	t.Helper()
	mustExec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_tp','触点商家','now')`)
	mustExec(`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		VALUES('sto_a','tnt_tp','甲店','','u','now','now')`)
	mustExec(`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		VALUES('sto_b','tnt_tp','乙店','','u','now','now')`)
	mustExec(`INSERT INTO campaigns(id,tenant_id,title,status,store_id,created_by,created_at,updated_at)
		VALUES('cmp_a','tnt_tp','活动A','active','sto_a','u','now','now')`)
	mustExec(`INSERT INTO campaigns(id,tenant_id,title,status,store_id,created_by,created_at,updated_at)
		VALUES('cmp_b','tnt_tp','活动B','active','sto_b','u','now','now')`)
	mustExec(`INSERT INTO campaigns(id,tenant_id,title,status,store_id,created_by,created_at,updated_at)
		VALUES('cmp_none','tnt_tp','无店活动','active',NULL,'u','now','now')`)
}

func tpMigInsertAlias(t *testing.T, d *sql.DB, id, campaignID, storeID, targetType string, revision int) {
	t.Helper()
	var campSQL, storeSQL string
	if campaignID == "" {
		campSQL = "NULL"
	} else {
		campSQL = "'" + campaignID + "'"
	}
	if storeID == "" {
		storeSQL = "NULL"
	} else {
		storeSQL = "'" + storeID + "'"
	}
	q := `INSERT INTO touchpoint_aliases(id,tenant_id,code,campaign_id,store_id,target_type,binding_revision,
		label,created_by,created_at,updated_at)
		VALUES('` + id + `','tnt_tp','` + id + `code',` + campSQL + `,` + storeSQL + `,'` + targetType + `',` +
		itoa(revision) + `,'卡','','now','now')`
	if _, err := d.Exec(q); err != nil {
		t.Fatalf("%q: %v", q, err)
	}
}

// tpMigAlias reads one alias row back as (target_type, binding_revision, store_id, campaign_id).
func tpMigAlias(t *testing.T, d *sql.DB, id string) (string, int, string, string) {
	t.Helper()
	var targetType, storeID, campaignID string
	var revision int
	err := d.QueryRow(`SELECT target_type,binding_revision,COALESCE(store_id,''),COALESCE(campaign_id,'')
		FROM touchpoint_aliases WHERE id=?`, id).Scan(&targetType, &revision, &storeID, &campaignID)
	if err != nil {
		t.Fatalf("alias %s: %v", id, err)
	}
	return targetType, revision, storeID, campaignID
}

func TestTouchpointStoreTargetSchema(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	tpMigSeedTenant(t, d)

	// 旧行(不指定新列)落 default:target_type=legacy_campaign、binding_revision=1。
	tpMigInsertAlias(t, d, "al_legacy", "cmp_a", "", "legacy_campaign", 1)
	if targetType, revision, _, campaignID := tpMigAlias(t, d, "al_legacy"); targetType != "legacy_campaign" || revision != 1 || campaignID != "cmp_a" {
		t.Fatalf("legacy default = (%s,%d,%s), want (legacy_campaign,1,cmp_a)", targetType, revision, campaignID)
	}

	// store 根行:campaign_id 可空(不带活动注记的门店入口必须可入库)。
	tpMigInsertAlias(t, d, "al_store", "", "sto_a", "store", 3)
	if targetType, revision, storeID, campaignID := tpMigAlias(t, d, "al_store"); targetType != "store" || revision != 3 || storeID != "sto_a" || campaignID != "" {
		t.Fatalf("store row = (%s,%d,%s,%s), want (store,3,sto_a,'')", targetType, revision, storeID, campaignID)
	}

	// CHECK 闸:坏 target_type 与 0 revision 拒绝入库。
	if _, err := d.Exec(`INSERT INTO touchpoint_aliases(id,tenant_id,code,campaign_id,store_id,target_type,binding_revision,label,created_at,updated_at)
		VALUES('al_x','tnt_tp','al_xcode',NULL,'sto_a','storefront',1,'','now','now')`); err == nil {
		t.Fatal("target_type 'storefront' must be refused by CHECK (values are store|legacy_campaign)")
	}
	if _, err := d.Exec(`INSERT INTO touchpoint_aliases(id,tenant_id,code,campaign_id,store_id,target_type,binding_revision,label,created_at,updated_at)
		VALUES('al_y','tnt_tp','al_ycode',NULL,'sto_a','store',0,'','now','now')`); err == nil {
		t.Fatal("binding_revision 0 must be refused by CHECK")
	}

	// 门店检索路径:store_id 索引随迁移就位。
	var idx int
	if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='index' AND name='idx_touchpoint_aliases_store'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("idx_touchpoint_aliases_store missing (err=%v idx=%d)", err, idx)
	}
}

// TestTouchpointAliasesRebuildPreservesRows 走真实 Migrate 重放 0103(表重建):
// 旧行、target_type/binding_revision、审计事件全数幸存 —— Migrate 在事务内逐文件执行,
// 外键强制不关,重建依赖 defer_foreign_keys 把 DROP 的引用检查推迟到 COMMIT。
func TestTouchpointAliasesRebuildPreservesRows(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	tpMigSeedTenant(t, d)
	tpMigInsertAlias(t, d, "al_legacy", "cmp_a", "", "legacy_campaign", 1)
	tpMigInsertAlias(t, d, "al_store", "", "sto_a", "store", 4)
	mustExec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO touchpoint_events(id,tenant_id,alias_id,kind,detail,created_by,created_at)
		VALUES('tpe_1','tnt_tp','al_legacy','created','code=al_legacycode','u','now')`)
	mustExec(`INSERT INTO touchpoint_events(id,tenant_id,alias_id,kind,detail,created_by,created_at)
		VALUES('tpe_2','tnt_tp','al_store','rebind','target -> store','u','now')`)

	// 只重放 0103(重建)。
	if _, err := d.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0103_%'`); err != nil {
		t.Fatalf("unapply 0103: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("re-migrate 0103: %v", err)
	}
	assertRows := func(stage string) {
		t.Helper()
		if targetType, revision, storeID, campaignID := tpMigAlias(t, d, "al_legacy"); targetType != "legacy_campaign" || revision != 1 || campaignID != "cmp_a" || storeID != "" {
			t.Fatalf("%s: legacy row = (%s,%d,%s,%s)", stage, targetType, revision, storeID, campaignID)
		}
		if targetType, revision, storeID, campaignID := tpMigAlias(t, d, "al_store"); targetType != "store" || revision != 4 || storeID != "sto_a" || campaignID != "" {
			t.Fatalf("%s: store row = (%s,%d,%s,%s)", stage, targetType, revision, storeID, campaignID)
		}
		var events int
		if err := d.QueryRow(`SELECT COUNT(1) FROM touchpoint_events WHERE alias_id IN ('al_legacy','al_store')`).Scan(&events); err != nil || events != 2 {
			t.Fatalf("%s: events survived = %d (err=%v), want 2", stage, events, err)
		}
	}
	assertRows("after rebuild")

	// 再次重放:重建必须幂等,行/审计/revision 零变化。
	if _, err := d.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0103_%'`); err != nil {
		t.Fatalf("unapply 0103 again: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("re-migrate 0103 again: %v", err)
	}
	assertRows("after replay")
}

// TestTouchpointStoreBackfill 走真实 Migrate 重放 0104:现有触点行由 Campaign 反查门店补
// store_id;只补空、不覆盖、不越租户、无店活动保持空、重放零改动。
func TestTouchpointStoreBackfill(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	tpMigSeedTenant(t, d)
	tpMigInsertAlias(t, d, "al_fill", "cmp_a", "", "legacy_campaign", 1)      // 可反查 → 补 sto_a
	tpMigInsertAlias(t, d, "al_keep", "cmp_a", "sto_b", "legacy_campaign", 1) // 已有归属 → 不覆盖
	tpMigInsertAlias(t, d, "al_none", "cmp_none", "", "legacy_campaign", 1)   // 无店活动 → 保持空

	unapply := func(stage string) {
		t.Helper()
		if _, err := d.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0104_%'`); err != nil {
			t.Fatalf("unapply 0104 (%s): %v", stage, err)
		}
		if err := Migrate(d); err != nil {
			t.Fatalf("re-migrate 0104 (%s): %v", stage, err)
		}
	}
	check := func(stage string) {
		t.Helper()
		if _, _, storeID, _ := tpMigAlias(t, d, "al_fill"); storeID != "sto_a" {
			t.Fatalf("%s: al_fill.store_id = %q, want sto_a (Campaign 反查)", stage, storeID)
		}
		if _, _, storeID, _ := tpMigAlias(t, d, "al_keep"); storeID != "sto_b" {
			t.Fatalf("%s: al_keep.store_id = %q, want sto_b (已有归属不覆盖)", stage, storeID)
		}
		if _, _, storeID, _ := tpMigAlias(t, d, "al_none"); storeID != "" {
			t.Fatalf("%s: al_none.store_id = %q, want '' (无店活动不许猜门店)", stage, storeID)
		}
	}
	unapply("first")
	check("first")
	unapply("replay")
	check("replay")
}
