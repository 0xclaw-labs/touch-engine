package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// 会让这个测试失败的生产改动：同店同目录多项互相覆盖、旧 revision 写入成功、
// 重复 ID 最后一项覆盖前项、相同内容重放把 config_revision 推高、公共投影
// 把停用/撤销/过期/非正式目标/待映射行展示出来。

func newActionsFixture(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := New(d)
	ten, err := st.CreateTenant("商家A")
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	if _, err := st.CreateStore(ten.ID, "甲店", "", "usr_x"); err != nil {
		t.Fatalf("store a: %v", err)
	}
	if _, err := st.CreateStore(ten.ID, "乙店", "", "usr_x"); err != nil {
		t.Fatalf("store b: %v", err)
	}
	stores, err := st.ListStores(ten.ID)
	if err != nil || len(stores) != 2 {
		t.Fatalf("stores = %d err %v", len(stores), err)
	}
	var storeA, storeB string
	for _, s := range stores {
		if s.Name == "甲店" {
			storeA = s.ID
		}
		if s.Name == "乙店" {
			storeB = s.ID
		}
	}
	if storeA == "" || storeB == "" {
		t.Fatalf("fixture stores missing: %+v", stores)
	}
	return st, ten.ID, storeA, storeB
}

func actionInput(id, catalog, label, target string, order int, enabled bool) StoreActionInput {
	return StoreActionInput{
		ID: id, CatalogCode: catalog, Platform: "junk-should-be-derived", Kind: "junk", Variant: "junk",
		Label: label, TargetRef: target, SortOrder: order, Enabled: enabled,
	}
}

func TestEnsureStorefrontStableCode(t *testing.T) {
	st, tenantID, storeID, _ := newActionsFixture(t)
	if _, err := st.EnsureStorefront("tnt_other", storeID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant ensure = %v, want ErrNotFound", err)
	}
	sf, err := st.EnsureStorefront(tenantID, storeID)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !strings.HasPrefix(sf.StoreCode, "sf_") || len(sf.StoreCode) < 10 {
		t.Fatalf("store code %q must be an opaque sf_ code", sf.StoreCode)
	}
	if sf.StorefrontRevision != 0 {
		t.Fatalf("storefront revision = %d, want 0 (发布版本归 1676)", sf.StorefrontRevision)
	}
	again, err := st.EnsureStorefront(tenantID, storeID)
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if again.StoreCode != sf.StoreCode {
		t.Fatalf("store code changed %s -> %s (必须是稳定码)", sf.StoreCode, again.StoreCode)
	}
	pub, err := st.GetStorefrontPublicByCode(sf.StoreCode)
	if err != nil {
		t.Fatalf("by code: %v", err)
	}
	if pub.StoreName != "甲店" || pub.StoreStatus != "active" || pub.TenantLifecycle != "active" {
		t.Fatalf("public storefront = %+v", pub)
	}
	if _, err := st.GetStorefrontPublicByCode("sf_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown code = %v, want ErrNotFound", err)
	}
}

func TestReplaceStoreActionsOptimisticAndIdempotent(t *testing.T) {
	st, tenantID, storeID, _ := newActionsFixture(t)

	items, rev, err := st.ListStoreActions(tenantID, storeID)
	if err != nil || len(items) != 0 || rev != 0 {
		t.Fatalf("initial = %d items rev %d err %v, want 0/0", len(items), rev, err)
	}

	// 五点评并存 + 多关注并存：同店同目录多项互不覆盖（裁定书唯一下界）。
	in := []StoreActionInput{
		actionInput("", "A08", "大众点评一", "https://www.dianping.com/shop/1", 1, true),
		actionInput("", "A08", "大众点评二", "https://www.dianping.com/shop/2", 2, true),
		actionInput("", "A09", "抖音点评", "https://www.douyin.com/roi/1", 3, true),
		actionInput("", "A17", "门店 Wi-Fi", "https://shop.example.com/wifi", 4, true),
		actionInput("", "A13", "抖音关注", "https://www.douyin.com/user/9", 5, true),
		actionInput("", "A16", "公众号关注", "https://mp.weixin.qq.com/mp/9", 6, true),
	}
	savedRev, saved, err := st.ReplaceStoreActions(tenantID, storeID, 0, in)
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if savedRev != 1 || len(saved) != 6 {
		t.Fatalf("saved rev=%d items=%d, want 1/6", savedRev, len(saved))
	}
	ids := map[string]StoreAction{}
	for _, item := range saved {
		if !strings.HasPrefix(item.ID, "sa_") || len(item.ID) != len("sa_")+24 {
			t.Fatalf("action id %q must be sa_ + 24 hex (随机不透明，不用自增)", item.ID)
		}
		if _, dup := ids[item.ID]; dup {
			t.Fatalf("id collision %s", item.ID)
		}
		ids[item.ID] = item
		if item.Revision != 1 {
			t.Fatalf("new item revision = %d, want 1", item.Revision)
		}
		if item.Platform == "junk-should-be-derived" || item.Kind == "junk" || item.Variant == "junk" {
			t.Fatalf("%s: platform/kind/variant must derive from catalog, got (%s,%s,%s)",
				item.ID, item.Platform, item.Kind, item.Variant)
		}
	}

	// 幂等重放：相同内容 + 相同 expected_revision → revision 不动、快照不增。
	beforeSnap := snapshotCount(t, st, storeID)
	replay := make([]StoreActionInput, 0, len(saved))
	for _, item := range saved {
		replay = append(replay, StoreActionInput{
			ID: item.ID, CatalogCode: item.CatalogCode, Label: item.Label, TargetRef: item.TargetRef,
			SortOrder: item.SortOrder, Enabled: item.Enabled,
		})
	}
	replayRev, _, err := st.ReplaceStoreActions(tenantID, storeID, 1, replay)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replayRev != 1 {
		t.Fatalf("replay bumped revision to %d, want 1 (幂等重放不推高)", replayRev)
	}
	if snapshotCount(t, st, storeID) != beforeSnap {
		t.Fatal("replay wrote a snapshot")
	}

	// 改一项（改目标 + 排序）：其余五项逐字段不变（含 revision）。
	newIn := make([]StoreActionInput, 0, len(saved))
	for i, item := range saved {
		in := StoreActionInput{
			ID: item.ID, CatalogCode: item.CatalogCode, Label: item.Label, TargetRef: item.TargetRef,
			SortOrder: item.SortOrder, Enabled: item.Enabled,
		}
		if i == 0 {
			in.TargetRef = "https://www.dianping.com/shop/1-new"
			in.SortOrder = 9
		}
		newIn = append(newIn, in)
	}
	rev2, updated, err := st.ReplaceStoreActions(tenantID, storeID, 1, newIn)
	if err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if rev2 != 2 {
		t.Fatalf("revision = %d, want 2", rev2)
	}
	byID := map[string]StoreAction{}
	for _, item := range updated {
		byID[item.ID] = item
	}
	for i, orig := range saved {
		now := byID[orig.ID]
		if i == 0 {
			if now.TargetRef != "https://www.dianping.com/shop/1-new" || now.SortOrder != 9 || now.Revision != 2 {
				t.Fatalf("changed item = %+v", now)
			}
			continue
		}
		if now.Revision != orig.Revision || now.TargetRef != orig.TargetRef || now.SortOrder != orig.SortOrder ||
			now.Label != orig.Label || now.Enabled != orig.Enabled || now.CatalogCode != orig.CatalogCode {
			t.Fatalf("unchanged item drifted: orig %+v now %+v", orig, now)
		}
	}

	// 旧 revision 覆盖：ErrStaleRevision，零写入。
	if _, _, err := st.ReplaceStoreActions(tenantID, storeID, 99, newIn); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale = %v, want ErrStaleRevision", err)
	}
	if rev, ok := storeActionRevision(t, st, storeID); !ok || rev != 2 {
		t.Fatalf("stale write changed revision to %d (ok=%v)", rev, ok)
	}
	if snapshotCount(t, st, storeID) != 2 {
		t.Fatal("stale write wrote a snapshot")
	}

	// 负载内重复 action_id：明确拒绝，不最后一项覆盖。
	dup := []StoreActionInput{
		actionInput(saved[1].ID, "A08", "第一条", "https://www.dianping.com/a", 1, true),
		actionInput(saved[1].ID, "A08", "第二条", "https://www.dianping.com/b", 2, true),
	}
	if _, _, err := st.ReplaceStoreActions(tenantID, storeID, 2, dup); !errors.Is(err, ErrDuplicateActionID) {
		t.Fatalf("duplicate = %v, want ErrDuplicateActionID", err)
	}

	// 不属于本店的 action_id：拒绝。
	if _, _, err := st.ReplaceStoreActions(tenantID, storeID, 2, []StoreActionInput{
		actionInput("sa_does_not_exist", "A08", "x", "https://www.dianping.com/x", 1, true),
	}); !errors.Is(err, ErrUnknownActionID) {
		t.Fatalf("unknown id = %v, want ErrUnknownActionID", err)
	}

	// 缺席即删除：只留一项。
	rev3, remain, err := st.ReplaceStoreActions(tenantID, storeID, 2, []StoreActionInput{
		actionInput(saved[0].ID, "A08", "大众点评一", "https://www.dianping.com/shop/1-new", 9, true),
	})
	if err != nil || rev3 != 3 || len(remain) != 1 {
		t.Fatalf("delete-missing = rev %d items %d err %v, want 3/1", rev3, len(remain), err)
	}

	// 停用行允许空目标。
	if _, _, err := st.ReplaceStoreActions(tenantID, storeID, 3, []StoreActionInput{
		actionInput("", "A17", "Wi-Fi 备用", "", 1, false),
	}); err != nil {
		t.Fatalf("disabled empty target: %v", err)
	}
}

func TestListPublicStoreActionsFiltering(t *testing.T) {
	st, tenantID, storeID, otherStore := newActionsFixture(t)

	if _, _, err := st.ReplaceStoreActions(tenantID, storeID, 0, []StoreActionInput{
		actionInput("", "A08", "大众点评", "https://www.dianping.com/shop/ok", 1, true),
		actionInput("", "A12", "美团点评", "https://www.meituan.com/poi/ok", 2, true),
		actionInput("", "A17", "Wi-Fi", "https://shop.example.com/wifi", 3, false), // 停用
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// 手工制造 legacy 态行：撤销/过期/非正式目标/待映射。
	mustExec(t, st, `UPDATE store_actions SET revoked=1 WHERE catalog_code='A12'`)
	mustExec(t, st, `INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		presentation,sort_order,enabled,revision,revoked,expires_at,created_at,updated_at)
		VALUES('sa_expired','`+tenantID+`','`+storeID+`','A10','amap','review','','高德','https://www.amap.com/x',
		'link',4,1,1,0,'2020-01-01T00:00:00Z','now','now')`)
	mustExec(t, st, `INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		presentation,sort_order,enabled,revision,revoked,expires_at,created_at,updated_at)
		VALUES('sa_evil','`+tenantID+`','`+storeID+`','A11','baidu','review','','百度','http://127.0.0.1/x',
		'link',5,1,1,0,'','now','now')`)
	mustExec(t, st, `INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		presentation,sort_order,enabled,revision,revoked,expires_at,created_at,updated_at)
		VALUES('sa_pending','`+tenantID+`','`+storeID+`','','','review','','待映射','https://shop.example.com/x',
		'link',6,1,1,0,'','now','now')`)

	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	pub, err := st.ListPublicStoreActions(storeID, now)
	if err != nil {
		t.Fatalf("public list: %v", err)
	}
	if len(pub) != 1 || pub[0].CatalogCode != "A08" {
		t.Fatalf("public = %+v, want only the healthy A08 row", pub)
	}

	// 第二个门店同平台完全隔离。
	if _, _, err := st.ReplaceStoreActions(tenantID, otherStore, 0, []StoreActionInput{
		actionInput("", "A08", "乙店点评", "https://www.dianping.com/shop/b", 1, true),
	}); err != nil {
		t.Fatalf("store b setup: %v", err)
	}
	aList, _, _ := st.ListStoreActions(tenantID, storeID)
	bList, _, _ := st.ListStoreActions(tenantID, otherStore)
	if len(aList) == 0 || len(bList) != 1 {
		t.Fatalf("isolation broken: a=%d b=%d", len(aList), len(bList))
	}
	if bList[0].Label != "乙店点评" || aList[0].Label == bList[0].Label {
		t.Fatalf("cross-store rows not isolated: %+v vs %+v", aList[0], bList[0])
	}
}

func snapshotCount(t *testing.T, st *Store, storeID string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(1) FROM store_action_snapshots WHERE store_id=?`, storeID).Scan(&n); err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	return n
}

func storeActionRevision(t *testing.T, st *Store, storeID string) (int, bool) {
	t.Helper()
	var rev int
	err := st.DB.QueryRow(`SELECT config_revision FROM store_action_configs WHERE store_id=?`, storeID).Scan(&rev)
	if err != nil {
		return 0, false
	}
	return rev, true
}

func mustExec(t *testing.T, st *Store, q string) {
	t.Helper()
	if _, err := st.DB.Exec(q); err != nil {
		t.Fatalf("%q: %v", q, err)
	}
}
