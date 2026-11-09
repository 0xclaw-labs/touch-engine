package httpapi

// server_storefront_version_test.go: HUI-1676 门店首页版本域 HTTP 验收(T04/T01 核心)。
//
// 覆盖:
//   - off 基线:路由不注册统一 404;
//   - 授权:owner 全店可写;跨租户 404 掩码;
//   - draft CRUD:空起步、保存 rev1、幂等重放、stale_version 409;
//   - preflight:逐动作 verdict(一个动作缺配置不传染邻居)、页级 fatal、
//     变更摘要+指纹;
//   - publish:冻结切换公共投影、确认指纹(409 confirm_required)、受限动作
//     否决(422)、幂等重放、expected 漂移 409;
//   - 第二版草稿期间旧版继续服务(指针不动),发布才切换;
//   - rollback:指针回拨+事件、实时撤权重查(422)、重放;
//   - 公共面:4584 无版本回退保留、发布后渲染版本载荷、实时撤权剔除、
//     缓存按 revision 失效(发布即新 revision 可见,0 请求过期);
//   - 点评禁奖:review 渠道进奖励配置 400 review_reward_prohibited;
//   - 旧参与保护:发布/回滚零触碰 participations/reward 表(表行数不变)。

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

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type sfFixture struct {
	s        *Server
	ts       *httptest.Server
	tenA     string
	tenB     string
	store1ID string
}

func sfTestConfig(frontOn, cacheOn bool) config.Config {
	front, cache := "off", "off"
	if frontOn {
		front = "on"
	}
	if cacheOn {
		cache = "on"
	}
	return config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test"
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-app"
		case "LEADS_PHONE_PEPPER":
			return "test-pepper"
		case "FEATURE_STOREFRONT":
			return front
		case "FEATURE_PUBLIC_CACHE":
			return cache
		case "FEATURE_CAMPAIGN_RULES":
			return "on"
		}
		return ""
	})
}

func newSfFixture(t *testing.T, frontOn, cacheOn bool) (*sfFixture, *sqlDB) {
	t.Helper()
	cfg := sfTestConfig(frontOn, cacheOn)
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("首页商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("首页商家B")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.CreateStore(tenA.ID, "旗舰店", "地址1", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	srv := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &sfFixture{s: srv, ts: ts, tenA: tenA.ID, tenB: tenB.ID, store1ID: s1.ID}, &sqlDB{d}
}

func (f *sfFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (f *sfFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

// seedActions registers three real action instances via the merchant API.
func (f *sfFixture) seedActions(t *testing.T) map[string]string {
	t.Helper()
	body := `{"expected_revision":0,"items":[
		{"catalog_code":"A01","label":"抖音发布","target_ref":"https://www.douyin.com/roi/poi","order":0,"enabled":true},
		{"catalog_code":"A19","label":"美团团购","target_ref":"https://www.meituan.com/deal","order":1,"enabled":true},
		{"catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/1","order":2,"enabled":true}
	]}`
	status, out := f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/actions", body)
	if status != 200 {
		t.Fatalf("seed actions: %d %v", status, out)
	}
	byCode := map[string]string{}
	items, _ := out["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		code, _ := item["catalog_code"].(string)
		id, _ := item["action_id"].(string)
		byCode[code] = id
	}
	if len(byCode) != 3 {
		t.Fatalf("seed actions incomplete: %v", byCode)
	}
	return byCode
}

func sfDraftBody(expected int, actionIDs ...string) string {
	ids := make([]string, 0, len(actionIDs))
	for _, id := range actionIDs {
		ids = append(ids, `"`+id+`"`)
	}
	return fmt.Sprintf(`{"expected_revision":%d,"sections":[
		{"id":"s1","type":"actions","title":"常用服务","action_ids":[%s]}
	],"brand_ref":""}`, expected, strings.Join(ids, ","))
}

func TestStorefrontRoutes404WhenFlagOff(t *testing.T) {
	f, _ := newSfFixture(t, false, false)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/stores/" + f.store1ID + "/storefront-draft"},
		{"PUT", "/api/v1/stores/" + f.store1ID + "/storefront-draft"},
		{"POST", "/api/v1/stores/" + f.store1ID + "/storefront/preflight"},
		{"POST", "/api/v1/stores/" + f.store1ID + "/storefront/publish"},
		{"POST", "/api/v1/stores/" + f.store1ID + "/storefront/rollback"},
		{"GET", "/api/v1/stores/" + f.store1ID + "/storefront/revisions"},
		{"GET", "/api/v1/stores/" + f.store1ID + "/storefront-preview"},
		{"GET", "/api/v1/public/storefronts/sf_anything"},
	} {
		status, _ := f.do(t, tc.m, tc.p, "sess-owner-a", f.tenA, "")
		if status != 404 {
			t.Fatalf("%s %s = %d, want 404 (off = invisible)", tc.m, tc.p, status)
		}
	}
}

func TestStorefrontDraftEmptyStartSaveReplayAndStale(t *testing.T) {
	f, _ := newSfFixture(t, true, false)
	ids := f.seedActions(t)

	status, out := f.owner(t, "GET", "/api/v1/stores/"+f.store1ID+"/storefront-draft", "")
	if status != 200 || out["revision"].(float64) != 0 || out["published_revision"].(float64) != 0 {
		t.Fatalf("empty draft = %d %v", status, out)
	}

	// 未知动作引用保存即拒(400),不留半条。
	status, out = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(0, "sa_made_up"))
	if status != 400 || out["error"] != "unknown_action_id" {
		t.Fatalf("unknown action ref = %d %v, want 400 unknown_action_id", status, out)
	}

	// 首存 rev1。
	status, out = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(0, ids["A01"]))
	if status != 200 || out["revision"].(float64) != 1 {
		t.Fatalf("first save = %d %v", status, out)
	}
	sections, _ := out["sections"].([]any)
	if len(sections) != 1 {
		t.Fatalf("sections = %v", out["sections"])
	}

	// 幂等重放(相同内容 + expected 1)。
	status, out = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(1, ids["A01"]))
	if status != 200 || out["revision"].(float64) != 1 {
		t.Fatalf("replay = %d %v", status, out)
	}

	// stale:expected 0 已过期 → 409 stale_version。
	status, out = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(0, ids["A19"]))
	if status != 409 || out["error"] != "stale_version" {
		t.Fatalf("stale save = %d %v, want 409 stale_version", status, out)
	}

	// 跨租户 404 掩码。
	status, _ = f.do(t, "GET", "/api/v1/stores/"+f.store1ID+"/storefront-draft", "sess-owner-b", f.tenB, "")
	if status != 404 {
		t.Fatalf("cross-tenant draft = %d, want 404", status)
	}
}

func TestStorefrontPreflightPerActionIsolationAndFatal(t *testing.T) {
	f, w := newSfFixture(t, true, false)
	ids := f.seedActions(t)
	// 用真实 action id 存草稿。
	status, out := f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft",
		sfDraftBody(0, ids["A01"], ids["A19"], ids["A08"]))
	if status != 200 {
		t.Fatalf("save draft: %d %v", status, out)
	}
	// 一个动作被停用(缺失配置形态):只影响它自己。
	w.exec(t, `UPDATE store_actions SET enabled=0 WHERE id=?`, ids["A08"])

	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	if status != 200 {
		t.Fatalf("preflight: %d %v", status, out)
	}
	if out["publishable"] != false {
		t.Fatalf("publishable must be false while an action lacks config: %v", out)
	}
	actions, _ := out["actions"].([]any)
	states := map[string]string{}
	for _, raw := range actions {
		v, _ := raw.(map[string]any)
		states[v["action_id"].(string)] = v["state"].(string)
	}
	if states[ids["A01"]] != "ready" || states[ids["A19"]] != "ready" {
		t.Fatalf("healthy actions contaminated: %v", states)
	}
	if states[ids["A08"]] != "missing_config" {
		t.Fatalf("disabled action = %v, want missing_config", states)
	}
	// 变更摘要 + 指纹(首版:全量新增)。
	summary, _ := out["change_summary"].([]any)
	if len(summary) == 0 || out["summary_fingerprint"] == "" {
		t.Fatalf("change summary missing: %v", out)
	}
	// 修复入口语义:动作恢复启用后 publishable 翻 true。
	w.exec(t, `UPDATE store_actions SET enabled=1 WHERE id=?`, ids["A08"])
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	if status != 200 || out["publishable"] != true {
		t.Fatalf("post-fix preflight = %d %v", status, out)
	}

	// 页级 fatal:段落引用未知动作 → 结构性保存时已被拒;此处验证空文档 fatal。
	status, out = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", `{"expected_revision":1,"sections":[]}`)
	if status != 200 {
		t.Fatalf("empty draft save: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	if status != 200 || out["publishable"] != false {
		t.Fatalf("empty doc must not be publishable: %d %v", status, out)
	}
}

func TestStorefrontPublishFreezeAndPublicProjection(t *testing.T) {
	f, w := newSfFixture(t, true, false)
	ids := f.seedActions(t)
	status, draft := f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft",
		sfDraftBody(0, ids["A01"], ids["A19"]))
	if status != 200 {
		t.Fatalf("save draft: %d %v", status, draft)
	}
	// 未确认摘要 → 400。
	status, out := f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish",
		`{"candidate_revision":1,"expected_published_revision":0,"summary_fingerprint":"bad","confirm_changes":true}`)
	if status != 409 || out["error"] != "confirm_required" {
		t.Fatalf("publish bad fingerprint = %d %v, want 409 confirm_required", status, out)
	}

	// preflight 拿指纹 → 发布。
	status, pre := f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	fp, _ := pre["summary_fingerprint"].(string)
	body := fmt.Sprintf(`{"candidate_revision":1,"expected_published_revision":0,"summary_fingerprint":%q,"confirm_changes":true}`, fp)
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish", body)
	if status != 200 {
		t.Fatalf("publish = %d %v", status, out)
	}
	if out["published_revision"].(float64) != 1 {
		t.Fatalf("published pointer = %v", out["published_revision"])
	}

	// 公共面渲染版本载荷(带 store_code 需要从 draft 拿)。
	code, _ := draft["store_code"].(string)
	if code == "" {
		t.Fatalf("draft missing store_code: %v", draft)
	}
	status, pub := f.do(t, "GET", "/api/v1/public/storefronts/"+code, "", "", "")
	if status != 200 {
		t.Fatalf("public storefront: %d %v", status, pub)
	}
	if pub["storefront_revision"].(float64) != 1 {
		t.Fatalf("public revision = %v, want 1", pub["storefront_revision"])
	}
	sections, _ := pub["sections"].([]any)
	if len(sections) != 1 {
		t.Fatalf("public sections = %v", pub["sections"])
	}
	actions, _ := pub["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("public actions = %v", pub["actions"])
	}
	first, _ := actions[0].(map[string]any)
	for _, forbidden := range []string{"tenant_id", "target_ref"} {
		if _, bad := first[forbidden]; bad {
			t.Fatalf("public payload leaks %s: %v", forbidden, first)
		}
	}

	// 第二版草稿保存期间,旧版继续服务(指针不动)。
	status, _ = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(1, ids["A01"], ids["A08"]))
	if status != 200 {
		t.Fatalf("save draft2: %d", status)
	}
	status, pub = f.do(t, "GET", "/api/v1/public/storefronts/"+code, "", "", "")
	if status != 200 || pub["storefront_revision"].(float64) != 1 {
		t.Fatalf("draft save must not move the public page: %d %v", status, pub)
	}
	actions, _ = pub["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("public page changed during draft: %v", pub["actions"])
	}

	// 实时撤权剔除:撤销 A19 → 公共面立即不见它(revision 仍 1)。
	w.exec(t, `UPDATE store_actions SET revoked=1 WHERE id=?`, ids["A19"])
	status, pub = f.do(t, "GET", "/api/v1/public/storefronts/"+code, "", "", "")
	if status != 200 || pub["storefront_revision"].(float64) != 1 {
		t.Fatalf("public after revoke = %d %v", status, pub)
	}
	actions, _ = pub["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("revoked action must vanish from public face: %v", pub["actions"])
	}

	// 旧参与保护:发布/回滚零触碰参与与奖励表。
	if n := w.count(t, "SELECT COUNT(1) FROM participations"); n != 0 {
		t.Fatalf("participations touched: %d", n)
	}
	if n := w.count(t, "SELECT COUNT(1) FROM reward_benefits"); n != 0 {
		t.Fatalf("reward tables touched: %d", n)
	}
}

func TestStorefrontPublishBlockedByRestrictedActionAndRollback(t *testing.T) {
	f, w := newSfFixture(t, true, false)
	ids := f.seedActions(t)
	status, _ := f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft",
		sfDraftBody(0, ids["A01"], ids["A08"]))
	if status != 200 {
		t.Fatalf("save draft: %d", status)
	}
	// A08 撤权 → publish 422 actions_not_ready 只列它。
	w.exec(t, `UPDATE store_actions SET revoked=1 WHERE id=?`, ids["A08"])
	status, pre := f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	fp, _ := pre["summary_fingerprint"].(string)
	body := fmt.Sprintf(`{"candidate_revision":1,"expected_published_revision":0,"summary_fingerprint":%q,"confirm_changes":true}`, fp)
	status, out := f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish", body)
	if status != 422 || out["error"] != "actions_not_ready" {
		t.Fatalf("publish veto = %d %v, want 422 actions_not_ready", status, out)
	}
	list, _ := out["actions"].([]any)
	if len(list) != 1 {
		t.Fatalf("veto list = %v, want only A08", out["actions"])
	}
	// 商家明确排除 → 新修订 → 发布成功。
	status, _ = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(1, ids["A01"]))
	if status != 200 {
		t.Fatalf("save draft2: %d", status)
	}
	status, pre = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	fp, _ = pre["summary_fingerprint"].(string)
	body = fmt.Sprintf(`{"candidate_revision":2,"expected_published_revision":0,"summary_fingerprint":%q,"confirm_changes":true}`, fp)
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish", body)
	if status != 200 || out["published_revision"].(float64) != 2 {
		t.Fatalf("publish healthy subset = %d %v", status, out)
	}

	// 幂等重放:同请求再发 → replayed。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish", body)
	if status != 200 || out["replayed"] != true {
		t.Fatalf("replay publish = %d %v", status, out)
	}

	// 再发布 rev3(含 A19)制造两个已发布版本,回滚才有目标。
	status, _ = f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(2, ids["A01"], ids["A19"]))
	if status != 200 {
		t.Fatalf("save draft3: %d", status)
	}
	status, pre = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	fp, _ = pre["summary_fingerprint"].(string)
	body = fmt.Sprintf(`{"candidate_revision":3,"expected_published_revision":2,"summary_fingerprint":%q,"confirm_changes":true}`, fp)
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish", body)
	if status != 200 || out["published_revision"].(float64) != 3 {
		t.Fatalf("publish rev3 = %d %v", status, out)
	}

	// expected 漂移 → 409(指针已到 3,expected 0 过期)。
	status, _ = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/rollback",
		`{"target_revision":2,"expected_published_revision":0}`)
	if status != 409 {
		t.Fatalf("rollback stale = %d, want 409", status)
	}
	// 不存在的目标 → 404。
	status, _ = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/rollback",
		`{"target_revision":1,"expected_published_revision":3}`)
	if status != 404 {
		t.Fatalf("rollback unknown target = %d, want 404", status)
	}

	// 回滚到 rev2:A01 已撤权 → 422 rollback_blocked(不复活撤权内容)。
	w.exec(t, `UPDATE store_actions SET revoked=1 WHERE id=?`, ids["A01"])
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/rollback",
		`{"target_revision":2,"expected_published_revision":3}`)
	if status != 422 || out["error"] != "actions_not_ready" {
		t.Fatalf("rollback veto = %d %v, want 422", status, out)
	}
	// 撤权恢复后回滚放行,指针回拨。
	w.exec(t, `UPDATE store_actions SET revoked=0 WHERE id=?`, ids["A01"])
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/rollback",
		`{"target_revision":2,"expected_published_revision":3}`)
	if status != 200 || out["published_revision"].(float64) != 2 {
		t.Fatalf("rollback = %d %v", status, out)
	}
	// 重放。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/rollback",
		`{"target_revision":2,"expected_published_revision":2}`)
	if status != 200 || out["replayed"] != true {
		t.Fatalf("rollback replay = %d %v", status, out)
	}
	// 版本与事件审计:rev2/rev3 两行,publish×2 + rollback×1。
	status, out = f.owner(t, "GET", "/api/v1/stores/"+f.store1ID+"/storefront/revisions", "")
	if status != 200 {
		t.Fatalf("revisions: %d %v", status, out)
	}
	items, _ := out["items"].([]any)
	events, _ := out["events"].([]any)
	if len(items) != 2 || len(events) != 3 {
		t.Fatalf("revisions/events = %d/%d, want 2/3", len(items), len(events))
	}
}

func TestStorefrontPreviewFaceConstrainedAndWhitelist(t *testing.T) {
	f, _ := newSfFixture(t, true, false)
	ids := f.seedActions(t)
	status, _ := f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft",
		sfDraftBody(0, ids["A01"], ids["A19"]))
	if status != 200 {
		t.Fatalf("save draft: %d", status)
	}
	// 草稿预览(无 revision 参数 = 当前稿)。
	status, out := f.owner(t, "GET", "/api/v1/stores/"+f.store1ID+"/storefront-preview", "")
	if status != 200 || out["preview"] != true {
		t.Fatalf("draft preview = %d %v", status, out)
	}
	if out["storefront_revision"].(float64) != 1 {
		t.Fatalf("preview revision = %v", out["storefront_revision"])
	}
	actions, _ := out["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("preview actions = %v", out["actions"])
	}
	// 无会话不可达(预览受商家会话约束)。
	status, _ = f.do(t, "GET", "/api/v1/stores/"+f.store1ID+"/storefront-preview", "", "", "")
	if status != 401 {
		t.Fatalf("preview without session = %d, want 401", status)
	}
}

func TestStorefrontCacheInvalidatesOnPublish(t *testing.T) {
	f, _ := newSfFixture(t, true, true) // FEATURE_PUBLIC_CACHE on
	ids := f.seedActions(t)
	status, draft := f.owner(t, "PUT", "/api/v1/stores/"+f.store1ID+"/storefront-draft", sfDraftBody(0, ids["A01"]))
	if status != 200 {
		t.Fatalf("save: %d", status)
	}
	code, _ := draft["store_code"].(string)
	status, pub := f.do(t, "GET", "/api/v1/public/storefronts/"+code, "", "", "")
	if status != 200 || pub["storefront_revision"].(float64) != 0 {
		t.Fatalf("pre-publish public = %d %v", status, pub)
	}
	status, pre := f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/preflight", "")
	fp, _ := pre["summary_fingerprint"].(string)
	body := fmt.Sprintf(`{"candidate_revision":1,"expected_published_revision":0,"summary_fingerprint":%q,"confirm_changes":true}`, fp)
	if status, _ := f.owner(t, "POST", "/api/v1/stores/"+f.store1ID+"/storefront/publish", body); status != 200 {
		t.Fatalf("publish failed")
	}
	// 缓存命中过的 code 在发布后必须立即可见新 revision(epoch 失效边界)。
	status, pub = f.do(t, "GET", "/api/v1/public/storefronts/"+code, "", "", "")
	if status != 200 || pub["storefront_revision"].(float64) != 1 {
		t.Fatalf("post-publish public = %d %v, want revision 1 (0 stale requests)", status, pub)
	}
}

func TestPublishConfigRejectsReviewRewardChannel(t *testing.T) {
	f, _ := newSfFixture(t, true, false)
	status, camp := f.owner(t, "POST", "/api/v1/campaigns",
		`{"title":"点评禁奖活动","public_content":"c","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`)
	if status != 201 {
		t.Fatalf("campaign create: %d %v", status, camp)
	}
	campID, _ := camp["id"].(string)
	body := `{"verification_mode":"staff_observed_submission","customer_copy":"发布后给店员看","reward":{
		"kind":"store_coupon","slot":"slot-1","budget_mode":"total_quantity","total_quantity":10,
		"currency":"CNY","store_ids":[],"claim_from":"","claim_until":"","redeem_from":"","redeem_until":"",
		"onsite_redemption":false,"staff_scope":"store_staff","out_of_stock_rule":"stop_new"},
		"dedup_basis":"platform_authorized_subject","per_order_limit":false,
		"channels":[{"platform":"dianping","entry_mode":"manual","scenario":"review"}],
		"content_version_id":""}`
	status, out := f.owner(t, "PUT", "/api/v1/campaigns/"+campID+"/publish-config", body)
	if status != 400 {
		t.Fatalf("review channel PUT = %d %v, want 400", status, out)
	}
	if out["error"] != "review_reward_prohibited" {
		t.Fatalf("error = %v, want review_reward_prohibited", out["error"])
	}
}
