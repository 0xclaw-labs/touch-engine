package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// 会让这个测试失败的生产改动：旧 revision 写入成功、重复/未知 ID 最后一项覆盖、
// 内容一致重放推高 revision、非法 Wi-Fi 密码/非官方企微域名入库、公共投影把
// 停用行或 config 非法行展示出来、事件表被写入成功语义枚举。

func newServicesFixture(t *testing.T) (*Store, string, string) {
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
		t.Fatalf("store: %v", err)
	}
	stores, err := st.ListStores(ten.ID)
	if err != nil || len(stores) != 1 {
		t.Fatalf("stores = %d err %v", len(stores), err)
	}
	return st, ten.ID, stores[0].ID
}

func wifiInput(id string, order int, enabled bool) StoreServiceInput {
	return StoreServiceInput{
		ID: id, Kind: ServiceKindWifi, CatalogCode: "A17",
		Label: "门店 Wi-Fi", Order: order, Enabled: enabled,
		Config: map[string]any{"ssid": "Painuo-Guest", "password": "passw0rd123", "encryption": "wpa2"},
	}
}

func TestReplaceStoreServicesOptimisticLockAndReplay(t *testing.T) {
	st, ten, sto := newServicesFixture(t)

	rev1, items, err := st.ReplaceStoreServices(ten, sto, 0, []StoreServiceInput{wifiInput("", 1, true)})
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if rev1 != 1 || len(items) != 1 {
		t.Fatalf("rev1=%d items=%d", rev1, len(items))
	}
	if items[0].ID == "" || !strings.HasPrefix(items[0].ID, "sv_") {
		t.Fatalf("opaque service id missing: %q", items[0].ID)
	}

	// 旧 revision 写入必须零写入失败。
	if _, _, err := st.ReplaceStoreServices(ten, sto, 0, []StoreServiceInput{wifiInput("", 1, true)}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale revision must fail, got %v", err)
	}

	// 内容一致重放：幂等，不推修订。
	same := wifiInput(items[0].ID, 1, true)
	rev2, items2, err := st.ReplaceStoreServices(ten, sto, rev1, []StoreServiceInput{same})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rev2 != rev1 || len(items2) != 1 || items2[0].Revision != 1 {
		t.Fatalf("idempotent replay bumped state: rev=%d itemRev=%d", rev2, items2[0].Revision)
	}

	// 负载内重复 id 拒绝，绝不最后一项覆盖。
	dup := wifiInput(items[0].ID, 2, true)
	if _, _, err := st.ReplaceStoreServices(ten, sto, rev1, []StoreServiceInput{dup, dup}); !errors.Is(err, ErrDuplicateServiceID) {
		t.Fatalf("duplicate id must fail, got %v", err)
	}
	// 未知 id 拒绝。
	ghost := wifiInput("sv_nope0000000000000000ff", 2, true)
	if _, _, err := st.ReplaceStoreServices(ten, sto, rev1, []StoreServiceInput{ghost}); !errors.Is(err, ErrUnknownServiceID) {
		t.Fatalf("unknown id must fail, got %v", err)
	}
}

func TestStoreServiceConfigValidation(t *testing.T) {
	st, ten, sto := newServicesFixture(t)

	cases := []struct {
		name string
		in   StoreServiceInput
	}{
		{"wifi 非开放加密缺密码", StoreServiceInput{Kind: ServiceKindWifi, Enabled: true,
			Config: map[string]any{"ssid": "S", "encryption": "wpa2"}}},
		{"wifi 缺 ssid", StoreServiceInput{Kind: ServiceKindWifi, Enabled: true,
			Config: map[string]any{"password": "passw0rd123", "encryption": "wpa2"}}},
		{"wifi 密码太短", StoreServiceInput{Kind: ServiceKindWifi, Enabled: true,
			Config: map[string]any{"ssid": "S", "password": "1234567", "encryption": "wpa2"}}},
		{"wifi 加密枚举外", StoreServiceInput{Kind: ServiceKindWifi, Enabled: true,
			Config: map[string]any{"ssid": "S", "password": "passw0rd123", "encryption": "wep"}}},
		{"contact wecom 非官方域名", StoreServiceInput{Kind: ServiceKindContact, Variant: "wecom", Enabled: true,
			Config: map[string]any{"url": "https://evil.example.com/ca/abc"}}},
		{"contact wecom 缺 url", StoreServiceInput{Kind: ServiceKindContact, Variant: "wecom", Enabled: true,
			Config: map[string]any{}}},
		{"contact personal 两者皆缺", StoreServiceInput{Kind: ServiceKindContact, Variant: "personal", Enabled: true,
			Config: map[string]any{}}},
		{"contact personal 非法微信号", StoreServiceInput{Kind: ServiceKindContact, Variant: "personal", Enabled: true,
			Config: map[string]any{"wechat_id": "1abc"}}},
		{"contact personal qr 非公网", StoreServiceInput{Kind: ServiceKindContact, Variant: "personal", Enabled: true,
			Config: map[string]any{"qr_ref": "http://10.0.0.1/qr.png"}}},
		{"community 全缺", StoreServiceInput{Kind: ServiceKindCommunity, Variant: "qq_group", Enabled: true,
			Config: map[string]any{}}},
		{"community 邀请链接非公网", StoreServiceInput{Kind: ServiceKindCommunity, Variant: "qq_group", Enabled: true,
			Config: map[string]any{"invite_url": "http://localhost/g", "group_no": "123456"}}},
		{"kind 枚举外", StoreServiceInput{Kind: "followed", Enabled: true, Config: map[string]any{}}},
	}
	for _, tc := range cases {
		_, _, err := st.ReplaceStoreServices(ten, sto, 0, []StoreServiceInput{tc.in})
		if !errors.Is(err, ErrBadServiceConfig) {
			t.Fatalf("%s: want ErrBadServiceConfig, got %v", tc.name, err)
		}
	}

	// open 加密可以无密码；密码一律按展示语义存（非凭据类）。
	ok := StoreServiceInput{Kind: ServiceKindWifi, Enabled: true, Config: map[string]any{"ssid": "OpenNet", "encryption": "open"}}
	_, items, err := st.ReplaceStoreServices(ten, sto, 0, []StoreServiceInput{ok})
	if err != nil {
		t.Fatalf("open wifi: %v", err)
	}
	if items[0].ConfigJSON["password"] != "" {
		t.Fatalf("open wifi password must normalize empty, got %v", items[0].ConfigJSON["password"])
	}

	// 企微官方「联系我」链接入库；个人微信 wechat_id 合法形状入库。
	wecom := StoreServiceInput{Kind: ServiceKindContact, Variant: "wecom", CatalogCode: "A18", Enabled: true,
		Config: map[string]any{"url": "https://work.weixin.qq.com/ca/abc?source=nfc", "source_note": "台卡"}}
	personal := StoreServiceInput{Kind: ServiceKindContact, Variant: "personal", Enabled: true,
		Config: map[string]any{"wechat_id": "painuo_store", "qr_ref": "https://cdn.example.com/qr.png"}}
	if _, _, err := st.ReplaceStoreServices(ten, sto, 1, []StoreServiceInput{wecom, personal}); err != nil {
		t.Fatalf("contact ok: %v", err)
	}
}

func TestStoreServicesScopeAndPublicProjection(t *testing.T) {
	st, ten, sto := newServicesFixture(t)

	_, items, err := st.ReplaceStoreServices(ten, sto, 0, []StoreServiceInput{
		wifiInput("", 1, true),
		{Kind: ServiceKindContact, Variant: "wecom", CatalogCode: "A18", Label: "加企微", Order: 2, Enabled: false,
			Config: map[string]any{"url": "https://work.weixin.qq.com/ca/xyz"}},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	svcID := items[0].ID

	// Scope：id 不是授权——按 id 反查受信 tenant/store；未知统一 ErrNotFound。
	gten, gsto, err := st.StoreServiceScope(svcID)
	if err != nil || gten != ten || gsto != sto {
		t.Fatalf("scope: %v %s %s", err, gten, gsto)
	}
	if _, _, err := st.StoreServiceScope("sv_missing0000000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing scope want ErrNotFound, got %v", err)
	}

	// 公共投影：停用行不出现（fail-closed）。
	pub, err := st.ListPublicStoreServices(sto)
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if len(pub) != 1 || pub[0].ID != svcID {
		t.Fatalf("public rows = %d, want 1 (enabled only)", len(pub))
	}

	// 启用但 config 非法的行（绕过模型层直接写库）不得出现在公共投影。
	if _, err := st.DB.Exec(`INSERT INTO store_services(id,tenant_id,store_id,catalog_code,kind,variant,label,config,
		sort_order,enabled,revision,created_at,updated_at)
		VALUES('sv_bad','` + ten + `','` + sto + `','','wifi','','x','{"encryption":"wep"}',3,1,1,'now','now')`); err != nil {
		t.Fatalf("raw insert: %v", err)
	}
	pub2, err := st.ListPublicStoreServices(sto)
	if err != nil {
		t.Fatalf("public2: %v", err)
	}
	if len(pub2) != 1 {
		t.Fatalf("invalid-config row must stay invisible, got %d rows", len(pub2))
	}
}

func TestRecordServiceEventEvidenceOnly(t *testing.T) {
	st, ten, sto := newServicesFixture(t)
	_, items, err := st.ReplaceStoreServices(ten, sto, 0, []StoreServiceInput{wifiInput("", 1, true)})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := items[0].ID

	// 三种行为证据可落库；成功语义枚举被模型层拒绝（与 CHECK 双保险）。
	for _, kind := range []string{ServiceEventOpen, ServiceEventJumpAttempt, ServiceEventCopy} {
		if err := st.RecordServiceEvent(ten, sto, id, kind, "ios_safari"); err != nil {
			t.Fatalf("event %s: %v", kind, err)
		}
	}
	if err := st.RecordServiceEvent(ten, sto, id, "followed", "ios_safari"); err == nil {
		t.Fatal("event 'followed' must be refused (no success semantics)")
	}
	if err := st.RecordServiceEvent(ten, sto, "sv_unknown00000000000ff", ServiceEventOpen, ""); err == nil {
		t.Fatal("event for unknown service must be refused")
	}

	rows, err := st.CountServiceEvents(ten, sto, id)
	if err != nil || rows != 3 {
		t.Fatalf("count = %d err %v", rows, err)
	}
}

func TestRecordWecomCallbackDigestOnly(t *testing.T) {
	st, _, _ := newServicesFixture(t)
	states := []string{"pending_qualification", "signature_mismatch", "decrypt_failed", "verified"}
	for _, s := range states {
		if err := st.RecordWecomCallback(s, "sig123", true, "abc", "note"); err != nil {
			t.Fatalf("state %s: %v", s, err)
		}
	}
	if err := st.RecordWecomCallback("customer_added", "", false, "", ""); err == nil {
		t.Fatal("callback state 'customer_added' must be refused")
	}
	n, err := st.CountWecomCallbacks()
	if err != nil || n != len(states) {
		t.Fatalf("callbacks = %d err %v", n, err)
	}
}
