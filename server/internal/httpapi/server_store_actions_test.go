package httpapi

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// 会让这个测试失败的生产改动：门店动作配置越权写入、旧 revision 覆盖成功、
// 负载内重复 ID 最后一项覆盖前项、validate 变更任何状态、公共门店面泄漏
// tenant 密钥/target_ref/内部 scope，或把停用/待映射行端给顾客。

type actionsFixture struct {
	f                    *fixture
	tenA, tenB           string
	stoA, stoB           string
	sessOwner, sessStaff string // tenA
	sessMgrA, sessMgrB   string // stoA / stoB
	sessOwnerB           string
}

func newActionsHTTPFixture(t *testing.T, storefrontFlag bool) *actionsFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	mustNoErr(t, err)
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("连锁甲")
	mustNoErr(t, err)
	tenB, err := st.CreateTenant("商家乙")
	mustNoErr(t, err)
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	stoA, err := st.CreateStore(tenA.ID, "甲一店", "甲址", "usr_owner_a")
	mustNoErr(t, err)
	stoB, err := st.CreateStore(tenA.ID, "甲二店", "乙址", "usr_owner_a")
	mustNoErr(t, err)
	for _, m := range []struct{ principal, scope string }{
		{"usr_mgr_a", stoA.ID},
		{"usr_mgr_b", stoB.ID},
	} {
		if _, err := st.CreateMemberScoped(tenA.ID, m.principal, "store_manager", m.principal, "test", true, m.scope); err != nil {
			t.Fatalf("seed manager: %v", err)
		}
	}

	extra := map[string]string{}
	if storefrontFlag {
		extra["FEATURE_STOREFRONT"] = "on"
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_UPLOAD_BASE_URL":
			return "http://upload.test"
		case "PLATFORM_UPLOAD_TOKEN":
			return "upload-token"
		case "PUBLIC_BASE_URL":
			return "https://h5.test"
		}
		if v, ok := extra[k]; ok {
			return v
		}
		return ""
	})
	sessions := map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-mgr-a":   {"usr_mgr_a", "mgr.a@example.com"},
		"sess-mgr-b":   {"usr_mgr_b", "mgr.b@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	}
	idsrv := fakeIdentity(t, sessions)
	upsrv := fakeUpload(t, map[string]string{})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upsrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upsrv.Client()}
	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &actionsFixture{
		f:    &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, ownA: "usr_owner_a", ownB: "usr_owner_b", staffA: "usr_staff_a"},
		tenA: tenA.ID, tenB: tenB.ID,
		stoA: stoA.ID, stoB: stoB.ID,
		sessOwner: "sess-owner-a", sessStaff: "sess-staff-a",
		sessMgrA: "sess-mgr-a", sessMgrB: "sess-mgr-b",
		sessOwnerB: "sess-owner-b",
	}
}

const twoItemsBody = `{"expected_revision":0,"items":[
	{"catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/9","order":1,"enabled":true},
	{"catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true}
]}`

// seedAdoptedStorePack 走 store 层链条种一个「已采用」的门店内容包（不走
// HTTP,不动 upload 假服）：建包 → 登记真实媒体元数据 → 候选版本 → 显式采用。
func seedAdoptedStorePack(t *testing.T, st *store.Store, tenantID, storeID string) (packID, versionID string) {
	t.Helper()
	pack, err := st.CreateStoreContentPack(tenantID, storeID, "", "集成绑定包", "usr_owner_a")
	mustNoErr(t, err)
	video, _, err := st.RegisterContentMedia(tenantID, storeID, upload.MediaMeta{
		SHA256: strings.Repeat("a", 64), MIME: "video/mp4", Kind: upload.KindVideo,
		ByteSize: 1024, DurationMS: 1234, Width: 64, Height: 64, MediaRef: strings.Repeat("a", 64),
	}, "bind.mp4", "usr_owner_a")
	mustNoErr(t, err)
	cover, _, err := st.RegisterContentMedia(tenantID, storeID, upload.MediaMeta{
		SHA256: strings.Repeat("b", 64), MIME: "image/jpeg", Kind: upload.KindImage,
		ByteSize: 1024, Width: 64, Height: 64, MediaRef: strings.Repeat("b", 64),
	}, "bind.jpg", "usr_owner_a")
	mustNoErr(t, err)
	v, err := st.CreateStoreContentPackVersion(tenantID, pack.ID, store.StoreVersionSpec{
		ContentKind: contentpack.ContentKindVideo, VideoMediaID: video.ID, CoverMediaID: cover.ID,
		Title: "集成绑定包", Body: "开业五折。", Topics: []string{"开业"},
		StoreName: "甲一店", PriceNote: "五折", ValidNote: "长期",
		PlatformVersions: []contentpack.PlatformVersion{{Platform: "douyin", Note: "竖版", SourceVersion: "src-1"}},
		GrantRefs:        []string{"grant-int-1"},
		Marking:          contentpack.Marking{AdRequired: true},
		Source:           contentpack.SourceMerchantUpload,
	}, contentpack.StatusCandidate, "usr_owner_a")
	mustNoErr(t, err)
	_, err = st.AdoptStoreContentPackVersion(tenantID, pack.ID, v.ID, "douyin", "usr_owner_a", time.Now())
	mustNoErr(t, err)
	return pack.ID, v.ID
}

// TestStoreActionsMerchantSurface 覆盖 GET/PUT 的契约、鉴权矩阵、乐观锁、
// 幂等重放与逐字段拒绝码。
func TestStoreActionsMerchantSurface(t *testing.T) {
	af := newActionsHTTPFixture(t, false)
	f := af.f

	// 未登录：401（GET/PUT 一致）。
	if status, _, _ := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", "", af.tenA, ""); status != http.StatusUnauthorized {
		t.Fatalf("guest get = %d", status)
	}
	if status, _, _ := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", "", af.tenA, twoItemsBody); status != http.StatusUnauthorized {
		t.Fatalf("guest put = %d", status)
	}

	// 空态：config_revision 0、items 空、catalog 30 项、store_code 已生成。
	status, _, got := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got["config_revision"] != float64(0) {
		t.Fatalf("empty config_revision = %v", got["config_revision"])
	}
	if items, _ := got["items"].([]any); len(items) != 0 {
		t.Fatalf("empty items = %v", got["items"])
	}
	catalog, _ := got["catalog"].([]any)
	if len(catalog) != 30 {
		t.Fatalf("catalog = %d entries, want 30", len(catalog))
	}
	storeCode, _ := got["store_code"].(string)
	if !strings.HasPrefix(storeCode, "sf_") {
		t.Fatalf("store_code = %q", storeCode)
	}

	// 首次保存：owner。
	status, _, saved := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, twoItemsBody)
	mustEqual(t, status, http.StatusOK)
	if saved["config_revision"] != float64(1) {
		t.Fatalf("saved config_revision = %v", saved["config_revision"])
	}
	items := itemsOf(t, saved)
	if len(items) != 2 {
		t.Fatalf("saved items = %d", len(items))
	}
	frozen := []string{"action_id", "catalog_code", "platform", "kind", "variant", "label",
		"target_ref", "content_binding_ref", "order", "enabled", "revision"}
	first := items[0]
	for _, key := range frozen {
		if _, ok := first[key]; !ok {
			t.Fatalf("item missing contract field %q: %v", key, first)
		}
	}
	if first["platform"] != "dianping" || first["kind"] != "review" {
		t.Fatalf("platform/kind not derived from catalog: %v", first)
	}
	if first["action_revision"] != first["revision"] {
		t.Fatalf("action_revision alias missing: %v", first)
	}
	if first["catalog_label"] != "大众点评" {
		t.Fatalf("catalog_label = %v", first["catalog_label"])
	}
	ids := []string{first["action_id"].(string), items[1]["action_id"].(string)}
	if ids[0] == ids[1] || !strings.HasPrefix(ids[0], "sa_") {
		t.Fatalf("ids = %v", ids)
	}

	// 再读：内容一致，码稳定。
	status, _, again := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessMgrA, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if again["config_revision"] != float64(1) {
		t.Fatalf("manager cannot even read own store: %v", again)
	}
	if itemsOf(t, again)[0]["action_id"] != ids[0] {
		t.Fatalf("action id not stable across reads")
	}

	// 幂等重放：同内容 + expected 1 → revision 仍为 1。
	replay := `{"expected_revision":1,"items":[
		{"action_id":"` + ids[0] + `","catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/9","order":1,"enabled":true},
		{"action_id":"` + ids[1] + `","catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true}
	]}`
	status, _, replayed := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, replay)
	mustEqual(t, status, http.StatusOK)
	if replayed["config_revision"] != float64(1) {
		t.Fatalf("replay bumped revision to %v", replayed["config_revision"])
	}

	// 改一项：其余项逐字段不变。
	changeOne := `{"expected_revision":1,"items":[
		{"action_id":"` + ids[0] + `","catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/changed","order":5,"enabled":true},
		{"action_id":"` + ids[1] + `","catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true}
	]}`
	status, _, changed := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, changeOne)
	mustEqual(t, status, http.StatusOK)
	if changed["config_revision"] != float64(2) {
		t.Fatalf("changed revision = %v", changed["config_revision"])
	}
	citems := itemsOf(t, changed)
	if citems[0]["revision"] != float64(2) || citems[0]["target_ref"] != "https://www.dianping.com/shop/changed" {
		t.Fatalf("changed item = %v", citems[0])
	}
	if citems[1]["revision"] != float64(1) || citems[1]["target_ref"] != "https://shop.example.com/wifi" || citems[1]["order"] != float64(2) {
		t.Fatalf("unchanged item drifted: %v", citems[1])
	}

	// 旧 revision 覆盖：409 stale_version，零写入。
	status, _, stale := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, twoItemsBody)
	if status != http.StatusConflict || stale["error"] != "stale_version" {
		t.Fatalf("stale = %d %v", status, stale)
	}
	status, _, after := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	if status != http.StatusOK || after["config_revision"] != float64(2) || len(itemsOf(t, after)) != 2 {
		t.Fatalf("stale write mutated state: %d %v", status, after)
	}

	// 负载内重复 ID：400 duplicate_action_id。
	dup := `{"expected_revision":2,"items":[
		{"action_id":"` + ids[0] + `","catalog_code":"A08","label":"一","target_ref":"https://www.dianping.com/a","order":1,"enabled":true},
		{"action_id":"` + ids[0] + `","catalog_code":"A08","label":"二","target_ref":"https://www.dianping.com/b","order":2,"enabled":true}
	]}`
	status, _, dupRes := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, dup)
	if status != http.StatusBadRequest || dupRes["error"] != "duplicate_action_id" {
		t.Fatalf("duplicate = %d %v", status, dupRes)
	}

	// 未知 catalog_code / 非正式目标 / 启用但无目标 / 内容引用形态。
	for _, tc := range []struct{ body, code string }{
		{`{"expected_revision":2,"items":[{"catalog_code":"A99","label":"x","target_ref":"https://www.dianping.com/x","order":1,"enabled":true}]}`, "unknown_catalog_code"},
		{`{"expected_revision":2,"items":[{"catalog_code":"A08","label":"x","target_ref":"http://127.0.0.1/x","order":1,"enabled":true}]}`, "bad_target_ref"},
		{`{"expected_revision":2,"items":[{"catalog_code":"A08","label":"x","target_ref":"https://shop.example.com/x","order":1,"enabled":true},{"catalog_code":"A17","label":"y","target_ref":"","order":2,"enabled":true}]}`, "target_required"},
		{`{"expected_revision":2,"items":[{"catalog_code":"A08","label":"x","target_ref":"https://www.dianping.com/x","content_binding_ref":"cpk_123","order":1,"enabled":true}]}`, "bad_content_binding_ref"},
		{`{"expected_revision":2,"items":[{"catalog_code":"A08","label":"x","target_ref":"https://www.dianping.com/x","content_binding_ref":"pack:","order":1,"enabled":true}]}`, "bad_content_binding_ref"},
	} {
		status, _, res := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, tc.body)
		if status != http.StatusBadRequest || res["error"] != tc.code {
			t.Fatalf("reject %s = %d %v, want 400 %s", tc.code, status, res, tc.code)
		}
	}
	status, _, intact := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	if intact["config_revision"] != float64(2) || len(itemsOf(t, intact)) != 2 {
		t.Fatalf("rejected writes mutated state")
	}

	// HUI-3553 契约（中央集成 2026-11-10 写入侧已切 BindActionContent）：
	// pack:<id> 保存 = 权威绑定落地；不存在的 pack 引用 = 400（不再是纯形状校验）。
	goodPack, goodVersion := seedAdoptedStorePack(t, af.f.s.St, af.tenA, af.stoA)
	status, _, bound := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, `{"expected_revision":2,"items":[
		{"action_id":"`+ids[0]+`","catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/changed","content_binding_ref":"pack:`+goodPack+`","order":5,"enabled":true},
		{"action_id":"`+ids[1]+`","catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true}
	]}`)
	mustEqual(t, status, http.StatusOK)
	if itemsOf(t, bound)[0]["content_binding_ref"] != "pack:"+goodPack {
		t.Fatalf("content_binding_ref not stored/echoed: %v", itemsOf(t, bound)[0])
	}
	if b, err := af.f.s.St.GetActionContentBinding(af.tenA, ids[0]); err != nil ||
		b.PackID != goodPack || b.LastVersionID != goodVersion {
		t.Fatalf("authoritative binding = (%v, %+v); want pack %s version %s", err, b, goodPack, goodVersion)
	}
	status, _, ghost := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, `{"expected_revision":3,"items":[
		{"action_id":"`+ids[0]+`","catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/changed","content_binding_ref":"pack:no_such_pack","order":5,"enabled":true},
		{"action_id":"`+ids[1]+`","catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true}
	]}`)
	if status != http.StatusBadRequest || ghost["error"] != "bad_content_binding_ref" {
		t.Fatalf("ghost pack = %d %v; want 400 bad_content_binding_ref", status, ghost)
	}
	if rows, _, err := af.f.s.St.ListStoreActions(af.tenA, af.stoA); err != nil {
		t.Fatalf("list after ghost round: %v", err)
	} else {
		var bound0 string
		for _, r := range rows {
			if r.ID == ids[0] {
				bound0 = r.ContentBindingRef
			}
		}
		if bound0 != "pack:"+goodPack {
			t.Fatalf("row value not restored after failed round: %q (%+v)", bound0, rows)
		}
	}

	// 鉴权矩阵：staff 可写；本店经理可写；他店经理 403；跨租户 404。
	// （上面幽灵包那轮 Replace 先落一次修订后绑定失败,行值已还原但修订照实
	// 占位留痕——当前修订=4,这里从 4 起步。）
	status, _, staffPut := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessStaff, af.tenA, `{"expected_revision":4,"items":[
		{"action_id":"`+ids[0]+`","catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/changed","order":5,"enabled":true},
		{"action_id":"`+ids[1]+`","catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true},
		{"catalog_code":"A13","label":"抖音关注","target_ref":"https://www.douyin.com/user/7","order":3,"enabled":true}
	]}`)
	mustEqual(t, status, http.StatusOK)
	if staffPut["config_revision"] != float64(5) {
		t.Fatalf("staff put revision = %v", staffPut["config_revision"])
	}

	status, _, mgrOwn := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessMgrA, af.tenA, `{"expected_revision":5,"items":[
		{"action_id":"`+ids[0]+`","catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/changed","order":5,"enabled":true},
		{"action_id":"`+ids[1]+`","catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true},
		{"action_id":"`+itemsOf(t, staffPut)[2]["action_id"].(string)+`","catalog_code":"A13","label":"抖音关注","target_ref":"https://www.douyin.com/user/7","order":3,"enabled":false}
	]}`)
	mustEqual(t, status, http.StatusOK)
	if mgrOwn["config_revision"] != float64(6) {
		t.Fatalf("own-store manager put = %v", mgrOwn["config_revision"])
	}

	status, _, mgrOther := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessMgrB, af.tenA, twoItemsBody)
	if status != http.StatusForbidden {
		t.Fatalf("other-store manager put = %d %v", status, mgrOther)
	}
	status, _, crossTenant := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwnerB, af.tenB, twoItemsBody)
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant put = %d %v", status, crossTenant)
	}
	status, _, _ = f.do(t, "PUT", "/api/v1/stores/sto_missing/actions", af.sessOwner, af.tenA, twoItemsBody)
	if status != http.StatusNotFound {
		t.Fatalf("unknown store put = %d", status)
	}

	// 五点评 + 多关注并存：互不覆盖（乙店首次保存）。
	five := `{"expected_revision":0,"items":[
		{"catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/1","order":1,"enabled":true},
		{"catalog_code":"A09","label":"抖音点评","target_ref":"https://www.douyin.com/roi/2","order":2,"enabled":true},
		{"catalog_code":"A10","label":"高德点评","target_ref":"https://www.amap.com/place/3","order":3,"enabled":true},
		{"catalog_code":"A11","label":"百度点评","target_ref":"https://map.baidu.com/place/4","order":4,"enabled":true},
		{"catalog_code":"A12","label":"美团点评","target_ref":"https://www.meituan.com/shop/5","order":5,"enabled":true},
		{"catalog_code":"A13","label":"抖音关注","target_ref":"https://www.douyin.com/user/6","order":6,"enabled":true},
		{"catalog_code":"A15","label":"小红书关注","target_ref":"https://www.xiaohongshu.com/user/7","order":7,"enabled":true}
	]}`
	status, _, fiveSaved := f.do(t, "PUT", "/api/v1/stores/"+af.stoB+"/actions", af.sessMgrB, af.tenA, five)
	mustEqual(t, status, http.StatusOK)
	if items := itemsOf(t, fiveSaved); len(items) != 7 {
		t.Fatalf("five reviews + two follows = %d items", len(items))
	}
	// 甲一店的配置不受乙店写入影响（店间隔离）。
	status, _, storeA := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	if status != http.StatusOK || len(itemsOf(t, storeA)) != 3 || storeA["config_revision"] != float64(6) {
		t.Fatalf("store isolation broken: %v", storeA["config_revision"])
	}
}

func itemsOf(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	raw, _ := doc["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, _ := item.(map[string]any)
		out = append(out, row)
	}
	return out
}
