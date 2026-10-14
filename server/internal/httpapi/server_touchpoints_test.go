package httpapi

// HUI-1665 增量(2026-10-14)触点管理与稳定入口 —— 真实非生产 E2E(真 sqlite + stub identity):
//   - 管理面 /api/v1/touchpoints:启用入口(标签派生)/列表筛选/详情检查/换绑预览与确认/
//     独立停用/部署五态/QR/CSV;权限矩阵(游客 401/staff 403/跨租户 404/门店经理本店);
//   - 停用语义:别名停用只关自己,共享 link 的其他卡零影响(旧 SetTagStatus 传播语义不动);
//   - 换绑:预览含已开始参与数(3546 只读统计),确认后 URL 不变,并发 409;
//   - 部署五态:只经显式 stage 推进,CSV/QR/检查零推进(写入未知状态不冒充已部署);
//   - 公共解析 /api/v1/public/touchpoints/{code}:FEATURE_TOUCHPOINT_ENTRY 默认 off
//     (off=路由不注册+handler gate 404 双保险);on=available 带码/别名停用/未知同形 404;
//     FEATURE_PUBLIC_CACHE on 时停用/换绑后缓存立即失效(epoch);
//   - PUBLIC_BASE_URL 缺失 → QR/CSV 503 fail-closed;
//   - 公共载荷白名单:uid_hint/后台角色零泄漏。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// tpFixture:fixtureWithQR + 门店经理会话 + 触点/参与开关。
type tpFixture struct {
	*fixture
	stoA, stoB store.StoreRecord
}

func newTPFixture(t *testing.T, extra map[string]string) *tpFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("商家A")
	mustNoErr(t, err)
	tenB, err := st.CreateTenant("商家B")
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
	stoA, err := st.CreateStore(tenA.ID, "A 店", "A 址", "usr_owner_a")
	mustNoErr(t, err)
	stoB, err := st.CreateStore(tenA.ID, "B 店", "B 址", "usr_owner_a")
	mustNoErr(t, err)
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_mgr_a", "store_manager", "usr_mgr_a", "test", true, stoA.ID); err != nil {
		t.Fatalf("seed manager: %v", err)
	}

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PUBLIC_BASE_URL":
			return "https://h5.test"
		}
		if v, ok := extra[k]; ok {
			return v
		}
		return ""
	})

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
		"sess-mgr-a":   {"usr_mgr_a", "mgr@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &tpFixture{fixture: &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID,
		ownA: "usr_owner_a", ownB: "usr_owner_b", staffA: "usr_staff_a"},
		stoA: stoA, stoB: stoB}
}

// tpSeedScene:A 店两个活动(active,带窗口)+ 3 张标签(tag1/tag2 同绑 camp1 首条短码共享
// link,tag3 绑 camp2)+ 两个别名(tp1/tag1、tp2/tag2;tag3 留作无别名样本)。
func tpSeedScene(t *testing.T, f *tpFixture) (camp1, camp2, code1, tag1, tag2, tag3, tp1, tp2 string) {
	t.Helper()
	now := time.Now().UTC()
	newCamp := func(title, storeID string) (string, []string) {
		payload := fmt.Sprintf(`{"title":%q,"public_content":"到店有礼","store_id":%q,"starts_at":%q,"ends_at":%q}`,
			title, storeID, now.Add(-1*time.Hour).Format(time.RFC3339), now.Add(48*time.Hour).Format(time.RFC3339))
		status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, payload)
		mustEqual(t, status, http.StatusCreated)
		cid := cmp["id"].(string)
		status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/links", "sess-owner-a", f.tenA, "")
		mustEqual(t, status, http.StatusCreated)
		status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
		mustEqual(t, status, http.StatusOK)
		return cid, linksOf(t, f, cid)
	}
	camp1, links1 := newCamp("周年庆", f.stoA.ID)
	camp2, links2 := newCamp("春季展", f.stoA.ID)
	code1 = tpLinkCode(t, f, camp1)

	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(camp1, links1[:1], 2, f.stoA.ID, "", "周年庆", "shared"))
	mustEqual(t, status, http.StatusCreated)
	items := batch["items"].([]any)
	tag1 = items[0].(map[string]any)["id"].(string)
	tag2 = items[1].(map[string]any)["id"].(string)
	status, _, batch3 := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(camp2, links2[:1], 1, f.stoA.ID, "", "春季", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tag3 = batch3["items"].([]any)[0].(map[string]any)["id"].(string)

	status, _, tpv1 := f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA,
		`{"tag_id":"`+tag1+`","label":"A 卡","position":"桌贴"}`)
	mustEqual(t, status, http.StatusCreated)
	tp1 = tpv1["id"].(string)
	status, _, tpv2 := f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA,
		`{"tag_id":"`+tag2+`","label":"B 卡","position":"收银台"}`)
	mustEqual(t, status, http.StatusCreated)
	tp2 = tpv2["id"].(string)
	return camp1, camp2, code1, tag1, tag2, tag3, tp1, tp2
}

// linksOf fetches the link ids of a campaign.
func linksOf(t *testing.T, f *tpFixture, campaignID string) []string {
	t.Helper()
	status, _, links := f.do(t, "GET", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	items := links["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["id"].(string))
	}
	return out
}

// tpLinkCode fetches the first link code of a campaign.
func tpLinkCode(t *testing.T, f *tpFixture, campaignID string) string {
	t.Helper()
	status, _, links := f.do(t, "GET", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	return links["items"].([]any)[0].(map[string]any)["code"].(string)
}

// tpAliasCode fetches an alias's code via the management detail.
func tpAliasCode(t *testing.T, f *tpFixture, id string) string {
	t.Helper()
	status, _, v := f.do(t, "GET", "/api/v1/touchpoints/"+id, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	return v["code"].(string)
}

// tpDetail fetches the management detail view of an alias.
func tpDetail(t *testing.T, f *tpFixture, id string) map[string]any {
	t.Helper()
	status, _, v := f.do(t, "GET", "/api/v1/touchpoints/"+id, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	return v
}

func TestTouchpointProvisionListAndDetailAPI(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, camp2, _, tag1, _, _, tp1, _ := tpSeedScene(t, f)

	// 重复启用 → 409 already_provisioned
	status, _, body := f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA, `{"tag_id":"`+tag1+`"}`)
	if status != http.StatusConflict || body["error"] != "already_provisioned" {
		t.Fatalf("duplicate provision = %d %v", status, body)
	}
	// 未知标签 → 404;空标签 → 400
	status, _, _ = f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA, `{"tag_id":"nfc_missing"}`)
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA, `{}`)
	mustEqual(t, status, http.StatusBadRequest)

	// 列表 + 筛选
	status, _, lst := f.do(t, "GET", "/api/v1/touchpoints", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 2 {
		t.Fatalf("list = %d, want 2", got)
	}
	first := lst["items"].([]any)[0].(map[string]any)
	if first["status"] != "active" || first["deployment_stage"] != "registered" {
		t.Fatalf("first = %v", first)
	}
	// 稳定 URL 可复制
	if url, _ := first["url"].(string); !strings.HasPrefix(url, "https://h5.test/t/") || len(url) != len("https://h5.test/t/")+12 {
		t.Fatalf("stable url = %v", first["url"])
	}
	status, _, lst = f.do(t, "GET", "/api/v1/touchpoints?campaign_id="+camp2, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 0 {
		t.Fatalf("filter camp2 = %d, want 0", got)
	}
	status, _, lst = f.do(t, "GET", "/api/v1/touchpoints?store_id="+f.stoA.ID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 2 {
		t.Fatalf("filter store = %d, want 2", got)
	}
	status, _, _ = f.do(t, "GET", "/api/v1/touchpoints?status=bogus", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusBadRequest)

	// 详情:检查态=访客所见(available)+ 视图 JOIN + 事件留痕
	v := tpDetail(t, f, tp1)
	if v["entry_state"] != "available" || v["entry_code"] == "" {
		t.Fatalf("detail entry = %v/%v", v["entry_state"], v["entry_code"])
	}
	if v["campaign_title"] != "周年庆" || v["store_name"] != "A 店" || v["tag_label"] != "周年庆-001" || v["position"] != "桌贴" {
		t.Fatalf("detail view = %v", v)
	}
	if events, ok := v["events"].([]any); !ok || len(events) != 1 {
		t.Fatalf("detail events = %v", v["events"])
	}
	// 跨租户详情 → 404
	status, _, _ = f.do(t, "GET", "/api/v1/touchpoints/"+tp1, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)
}

func TestTouchpointRebindPreviewConfirmAndConflict(t *testing.T) {
	f := newTPFixture(t, map[string]string{
		"FEATURE_TOUCHPOINT_ENTRY": "on",
		"FEATURE_PARTICIPATION":    "on",
	})
	camp1, camp2, _, _, _, _, tp1, _ := tpSeedScene(t, f)

	// 两位顾客从 camp1 的短码开始参与(真实公共路径,3546 语义)
	startBody := func(idem string) string {
		return `{"link_code":"` + tpLinkCode(t, f, camp1) + `","touchpoint_source":"nfc","idempotency_key":"` + idem + `","content_pack_version":"v1"}`
	}
	status, hdr, _ := f.doCookie(t, "POST", "/api/v1/participations", "", startBody("idem-1"))
	mustEqual(t, status, http.StatusCreated)
	cookie := cookiePair(hdr)
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations", cookie, startBody("idem-2"))
	mustEqual(t, status, http.StatusCreated)

	// 预览:当前→新 + 影响范围(已开始参与数=2,只读)
	status, _, pv := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind-preview", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+camp2+`"}`)
	mustEqual(t, status, http.StatusOK)
	if pv["started_participations"].(float64) != 2 {
		t.Fatalf("preview impact = %v, want 2", pv["started_participations"])
	}
	if cur, ok := pv["current"].(map[string]any); !ok || cur["id"] != camp1 {
		t.Fatalf("preview current = %v", pv["current"])
	}
	if nxt, ok := pv["next"].(map[string]any); !ok || nxt["id"] != camp2 {
		t.Fatalf("preview next = %v", pv["next"])
	}
	// 预览目标跨租户不可见 → 404
	status, _, _ = f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind-preview", "sess-owner-b", f.tenB, `{"campaign_id":"cmp_x"}`)
	mustEqual(t, status, http.StatusNotFound)

	// 确认换绑:URL(别名码)不变,目标变化
	before := tpDetail(t, f, tp1)
	status, _, after := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+camp2+`","expected_campaign_id":"`+camp1+`"}`)
	mustEqual(t, status, http.StatusOK)
	if after["code"] != before["code"] || after["url"] != before["url"] {
		t.Fatalf("rebind changed the printed URL: %v -> %v", before["url"], after["url"])
	}
	if after["campaign_id"] != camp2 {
		t.Fatalf("rebound campaign = %v", after["campaign_id"])
	}
	// 并发换绑:expected 过期 → 409
	status, _, body := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+camp1+`","expected_campaign_id":"`+camp1+`"}`)
	if status != http.StatusConflict || body["error"] != "rebind_conflict" {
		t.Fatalf("stale rebind = %d %v", status, body)
	}
	// 无短码目标 → 400
	status, _, c3 := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"无码活动"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, body = f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+c3["id"].(string)+`","expected_campaign_id":"`+camp2+`"}`)
	if status != http.StatusBadRequest || body["error"] != "campaign_without_entry" {
		t.Fatalf("no-entry rebind = %d %v", status, body)
	}
	// 审计:created + rebind
	events := tpDetail(t, f, tp1)["events"].([]any)
	if len(events) != 2 || events[1].(map[string]any)["kind"] != "rebind" {
		t.Fatalf("events after rebind = %v", events)
	}
}

func TestTouchpointDisableIndependentOfSharedLink(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, _, code1, tag1, _, _, tp1, tp2 := tpSeedScene(t, f)
	codeA := tpAliasCode(t, f, tp1)

	// 共享 link 的两张卡:停 A → A 的 /t 停用,B 的 /t 照常;底层短码 enabled 不变
	status, _, _ := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, view := f.do(t, "GET", "/api/v1/public/touchpoints/"+codeA, "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	if view["state"] != "touchpoint_disabled" {
		t.Fatalf("disabled alias public = %v", view)
	}
	status, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tp2), "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "available" || view["code"] != code1 {
		t.Fatalf("sibling alias public = %v", view)
	}
	// 底层短码照常(旧 /c 兼容零变化)
	status, _, view = f.do(t, "GET", "/api/v1/public/links/"+code1, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "available" {
		t.Fatalf("legacy link public = %v", view)
	}
	// 标签管理面状态未被动过(别名停用不写标签/链接)
	if tagV := tpDetail(t, f, tp1); tagV["tag_status"] != "active" {
		t.Fatalf("tag status mutated by alias disable: %v", tagV["tag_status"])
	}
	// 旧传播路径不受本票影响:旧端点停用标签 → 短码停用(既有语义钉住)
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tag1+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, view = f.do(t, "GET", "/api/v1/public/links/"+code1, "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	if view["state"] != "link_disabled" {
		t.Fatalf("legacy propagation broken: %v", view)
	}
	// 坏状态值 → 400
	status, _, _ = f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/status", "sess-owner-a", f.tenA, `{"status":"paused"}`)
	mustEqual(t, status, http.StatusBadRequest)
}

func TestTouchpointStageExplicitOnly(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, _, _, _, _, tag3, tp1, _ := tpSeedScene(t, f)

	// 跳级拒绝
	status, _, body := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/stage", "sess-owner-a", f.tenA,
		`{"stage":"readback_done","note":"x"}`)
	if status != http.StatusConflict || body["error"] != "stage_transition" {
		t.Fatalf("skip stage = %d %v", status, body)
	}
	// 单步前进
	for _, want := range []string{"awaiting_write", "written_awaiting_readback", "readback_done", "device_verified"} {
		status, _, v := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/stage", "sess-owner-a", f.tenA,
			`{"stage":"`+want+`","note":"操作员登记"}`)
		mustEqual(t, status, http.StatusOK)
		if v["deployment_stage"] != want {
			t.Fatalf("stage = %v, want %s", v["deployment_stage"], want)
		}
	}
	// CSV 导出与检查零推进(写入未知状态不冒充已部署)
	status, _, _ = f.doRaw(t, "GET", "/api/v1/touchpoints/export.csv", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	tpDetail(t, f, tp1)
	if v := tpDetail(t, f, tp1); v["deployment_stage"] != "device_verified" {
		t.Fatalf("stage drifted without explicit advance: %v", v["deployment_stage"])
	}
	// 新别名一律从 registered 起步,导出/检查不给它推进
	status, _, fresh := f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA, `{"tag_id":"`+tag3+`"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, _ = f.doRaw(t, "GET", "/api/v1/touchpoints/export.csv", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	tpDetail(t, f, tp1)
	if v := tpDetail(t, f, fresh["id"].(string)); v["deployment_stage"] != "registered" {
		t.Fatalf("fresh alias stage = %v, want registered", v["deployment_stage"])
	}
}

func TestTouchpointQRAPI(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, _, _, _, _, _, tp1, _ := tpSeedScene(t, f)
	code := tpAliasCode(t, f, tp1)

	status, hdr, raw := f.doRaw(t, "GET", "/api/v1/touchpoints/"+tp1+"/qrcode", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if ct := hdr.Get("Content-Type"); ct != "image/png" || len(raw) == 0 {
		t.Fatalf("qr = %s %d bytes", ct, len(raw))
	}
	// json 载荷:可复制的稳定 URL
	status, _, body := f.do(t, "GET", "/api/v1/touchpoints/"+tp1+"/qrcode?format=json", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if body["url"] != "https://h5.test/t/"+code {
		t.Fatalf("qr json url = %v", body["url"])
	}
	// size 白名单
	status, _, _ = f.do(t, "GET", "/api/v1/touchpoints/"+tp1+"/qrcode?size=100", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusBadRequest)
	// PUBLIC_BASE_URL 缺失 → 503 fail-closed(不生成占位域名二维码)
	f.s.Cfg.PublicBaseURL = ""
	status, _, body = f.do(t, "GET", "/api/v1/touchpoints/"+tp1+"/qrcode", "sess-owner-a", f.tenA, "")
	if status != http.StatusServiceUnavailable || body["error"] != "qr_not_configured" {
		t.Fatalf("no base = %d %v", status, body)
	}
	status, _, body = f.do(t, "GET", "/api/v1/touchpoints/export.csv", "sess-owner-a", f.tenA, "")
	if status != http.StatusServiceUnavailable || body["error"] != "public_base_url_missing" {
		t.Fatalf("no base csv = %d %v", status, body)
	}
}

func TestTouchpointExportCSVAPI(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, _, _, _, _, tag3, _, _ := tpSeedScene(t, f)
	// 无 label 别名回退标签名:注入落在 tag3 标签名(fresh 别名无 label),
	// uid_hint 回填在管理面
	status, _, _ := f.do(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA, `{"tag_id":"`+tag3+`"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, _ = f.do(t, "PATCH", "/api/v1/nfc/tags/"+tag3, "sess-owner-a", f.tenA,
		`{"label":"=cmd|' /C calc'!A0","uid_hint":"04:A2:2F:9E"}`)
	mustEqual(t, status, http.StatusOK)

	status, hdr, raw := f.doRaw(t, "GET", "/api/v1/touchpoints/export.csv", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
	recs := parseCSV(t, raw)
	wantHeader := []string{"label", "short_code", "url", "store", "group", "uid_hint"}
	for i, h := range recs[0] {
		if h != wantHeader[i] {
			t.Fatalf("header = %v", recs[0])
		}
	}
	if len(recs) != 4 {
		t.Fatalf("csv rows = %d, want 4 (header+3)", len(recs))
	}
	// 每行 url 是 /t/ 稳定入口,short_code 列 = 别名码
	seen := map[string]bool{}
	for _, row := range recs[1:] {
		if !strings.HasPrefix(row[2], "https://h5.test/t/") || len(row[1]) != 12 {
			t.Fatalf("bad row %v", row)
		}
		seen[row[1]] = true
	}
	if len(seen) != 3 {
		t.Fatalf("distinct alias codes = %d, want 3", len(seen))
	}
	// 注入转义 + 无敏感字段
	escaped := 0
	for _, row := range recs[1:] {
		if row[0] == "'=cmd|' /C calc'!A0" {
			escaped++
		}
	}
	if escaped != 1 {
		t.Fatalf("escaped labels = %d, want 1", escaped)
	}
	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"tnt_", "usr_", "sess-", "tp_", "token", "secret"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("csv leaks %q:\n%s", banned, raw)
		}
	}
}

func TestTouchpointPermissionMatrix(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, camp2, _, _, _, _, tp1, _ := tpSeedScene(t, f)

	paths := []struct{ method, path, body string }{
		{"POST", "/api/v1/touchpoints", `{"tag_id":"x"}`},
		{"GET", "/api/v1/touchpoints/" + tp1, ""},
		{"POST", "/api/v1/touchpoints/" + tp1 + "/rebind-preview", `{"campaign_id":"x"}`},
		{"POST", "/api/v1/touchpoints/" + tp1 + "/rebind", `{"campaign_id":"x","expected_campaign_id":"y"}`},
		{"POST", "/api/v1/touchpoints/" + tp1 + "/status", `{"status":"disabled"}`},
		{"POST", "/api/v1/touchpoints/" + tp1 + "/stage", `{"stage":"awaiting_write"}`},
		{"GET", "/api/v1/touchpoints/" + tp1 + "/qrcode", ""},
	}
	for _, p := range paths {
		// 游客 → 401
		status, _, _ := f.doRaw(t, p.method, p.path, "", f.tenA, p.body)
		if status != http.StatusUnauthorized {
			t.Fatalf("guest %s %s = %d, want 401", p.method, p.path, status)
		}
		// staff → 403(伪造提权 query 也不行)
		status, _, body := f.do(t, p.method, p.path+"?role=owner&admin=1", "sess-staff-a", f.tenA, p.body)
		if status != http.StatusForbidden || body["error"] != "forbidden" {
			t.Fatalf("staff %s %s = %d %v", p.method, p.path, status, body)
		}
		// 跨租户 404 掩码(逐记录端点)
		status, _, _ = f.doRaw(t, p.method, p.path, "sess-owner-b", f.tenB, p.body)
		if status != http.StatusNotFound {
			t.Fatalf("owner-b %s %s = %d, want 404", p.method, p.path, status)
		}
	}
	// 列表形端点:跨租户互不可见(空列表),导出同理
	status, _, lst := f.do(t, "GET", "/api/v1/touchpoints", "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 0 {
		t.Fatalf("tenant B sees %d touchpoints, want 0", got)
	}
	status, hdr, raw := f.doRaw(t, "GET", "/api/v1/touchpoints/export.csv", "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusOK)
	recs := parseCSV(t, raw)
	if len(recs) != 1 {
		t.Fatalf("tenant B csv rows = %d, want header only", len(recs))
	}
	_ = hdr

	// 门店经理:只见本店别名
	status, _, mgrLst := f.do(t, "GET", "/api/v1/touchpoints", "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(mgrLst["items"].([]any)); got != 2 {
		t.Fatalf("mgr list = %d, want 2", got)
	}
	// 经理不能启用他店标签(404 掩码)
	status, _, btag := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(camp2, linksOf(t, f, camp2), 1, f.stoB.ID, "", "B店", "shared"))
	mustEqual(t, status, http.StatusCreated)
	btagID := btag["items"].([]any)[0].(map[string]any)["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/touchpoints", "sess-mgr-a", f.tenA, `{"tag_id":"`+btagID+`"}`)
	mustEqual(t, status, http.StatusNotFound)
	// 经理停用本店别名允许;查询他店别名 404
	status, _, _ = f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/status", "sess-mgr-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
}

// ---- 公共解析 ----------------------------------------------------------------

func TestTouchpointPublicResolutionAPI(t *testing.T) {
	// off 保险一:路由不注册 → ServeMux 统一 404
	off := newTPFixture(t, nil)
	_, _, _, _, _, _, tpOff, _ := tpSeedScene(t, off)
	offCode := tpAliasCode(t, off, tpOff)
	status, _, _ := off.do(t, "GET", "/api/v1/public/touchpoints/"+offCode, "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	// off 时非 GET 一样 404(公共面不可探测)
	status, _, _ = off.do(t, "POST", "/api/v1/public/touchpoints/"+offCode, "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	// off 保险二:handler gate(即便路由被注册,off 也统一 404 JSON)
	req, _ := http.NewRequest("GET", "/api/v1/public/touchpoints/"+offCode, nil)
	req.SetPathValue("code", offCode)
	rec := httptest.NewRecorder()
	off.s.handlePublicTouchpoint(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("gate off = %d, want 404", rec.Code)
	}
	var gateView map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &gateView); err != nil || gateView["state"] != "not_found" {
		t.Fatalf("gate off view = %v (%v)", gateView, err)
	}

	// on:available → {state, code}
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	camp1, _, code1, tag2, _, _, tpA, tpB := tpSeedScene(t, f)
	status, _, view := f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tpA), "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "available" || view["code"] != code1 {
		t.Fatalf("public = %v, want available+%s", view, code1)
	}
	// 载荷白名单形状:只有 state/code,无 uid_hint/角色/租户
	if len(view) != 2 {
		t.Fatalf("public keys = %v, want state+code only", view)
	}
	// 未知码/形状非法/别名停用 → 同形 404 单字段
	status, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/AAAAAAAAAAAAAAAAAAAA", "", "", "")
	if status != http.StatusNotFound || view["state"] != "not_found" || len(view) != 1 {
		t.Fatalf("unknown = %d %v", status, view)
	}
	status, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/short", "", "", "")
	if status != http.StatusNotFound || view["state"] != "not_found" {
		t.Fatalf("malformed = %d %v", status, view)
	}
	f.do(t, "POST", "/api/v1/touchpoints/"+tpA+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	status, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tpA), "", "", "")
	if status != http.StatusNotFound || view["state"] != "touchpoint_disabled" || len(view) != 1 {
		t.Fatalf("disabled = %d %v", status, view)
	}
	// 活动暂停:仍 200 带码(交既有 /c 页渲染五态文案)
	f.do(t, "POST", "/api/v1/touchpoints/"+tpB+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	f.do(t, "POST", "/api/v1/campaigns/"+camp1+"/status", "sess-owner-a", f.tenA, `{"status":"paused"}`)
	status, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tpB), "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "paused" {
		t.Fatalf("paused public = %v", view)
	}
	// uid_hint 零泄漏(管理面回填后)
	f.do(t, "PATCH", "/api/v1/nfc/tags/"+tag2, "sess-owner-a", f.tenA, `{"uid_hint":"04:BB"}`)
	status, _, raw := f.doRaw(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tpB), "", "", "")
	if strings.Contains(strings.ToLower(string(raw)), "uid_hint") || strings.Contains(string(raw), "04:BB") {
		t.Fatalf("public leaks uid_hint: %s", raw)
	}
}

func TestTouchpointDisableInvalidatesCacheImmediately(t *testing.T) {
	// 票面「停用后缓存」:双开关 on,命中缓存后停用 → 下一次请求立即停用态。
	f := newTPFixture(t, map[string]string{
		"FEATURE_TOUCHPOINT_ENTRY": "on",
		"FEATURE_PUBLIC_CACHE":     "on",
	})
	camp1, camp2, code1, _, _, _, tp1, _ := tpSeedScene(t, f)
	codeA := tpAliasCode(t, f, tp1)

	for i := 0; i < 3; i++ { // 预热进缓存
		status, _, view := f.do(t, "GET", "/api/v1/public/touchpoints/"+codeA, "", "", "")
		mustEqual(t, status, http.StatusOK)
		if view["state"] != "available" {
			t.Fatalf("warmup = %v", view)
		}
	}
	f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	status, _, view := f.do(t, "GET", "/api/v1/public/touchpoints/"+codeA, "", "", "")
	if status != http.StatusNotFound || view["state"] != "touchpoint_disabled" {
		t.Fatalf("after disable with cache on = %d %v, want immediate disabled", status, view)
	}
	// 换绑后缓存同样立即失效:恢复别名,换绑到 camp2 → 解析码变化
	f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	f.do(t, "GET", "/api/v1/public/touchpoints/"+codeA, "", "", "")
	f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+camp2+`","expected_campaign_id":"`+camp1+`"}`)
	status, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/"+codeA, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["code"] == code1 {
		t.Fatalf("after rebind the resolved code did not move: %v", view)
	}
}
