package httpapi

// HUI-1670 顾客发布交接 HTTP 面测试。
//
// 覆盖:
//   - off 双保险(路由不注册 + gate 统一 404);
//   - routes 只读裁决(not_admitted 拒绝/未登记 unknown 诚实提示);
//   - admitted fixture 全流程:内容快照确认→开始交接(过闸+attempt 冻结+
//     恢复凭据+受控媒体)→幂等重放→done/cancel→editor-opened 证据拒绝;
//   - 店员人工事实面(本店 200/邻店 403);
//   - 官方回执回调(验签通过确认/重放幂等/伪造拒绝);
//   - 跨租户/跨访客隔离(404)。

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

const testReceiptSecret = "receipt-secret-cfg"
const testVisitor = "visit_http_test"

type handoffFixture struct {
	s    *Server
	ts   *httptest.Server
	tenA string
	code string
	packID string
}

func newHandoffFixture(t *testing.T, on bool) *handoffFixture {
	t.Helper()
	flag := ""
	if on {
		flag = "on"
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_PUBLISH_HANDOFF":
			return flag
		case "PUBLIC_BASE_URL":
			return "https://h5.example.com"
		case "PUBLISH_HANDOFF_RECEIPT_SECRET":
			return testReceiptSecret
		case "PUBLISH_HANDOFF_APP_REF":
			return "app_touch_prod"
		case "PUBLISH_HANDOFF_MINIAPP_ENTRY_BASE":
			return "pages/publish/index"
		}
		return ""
	})

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("商家A")
	if err != nil {
		t.Fatal(err)
	}
	stoA, err := st.CreateStore(tenA.ID, "门店A", "南岸旗舰店", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_mgr_a", "store_manager"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed %s: %v", m.principal, err)
		}
	}
	// 店员作用域绑定本店。
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_mgr_scoped", "store_manager", "usr_mgr_scoped", "test", true, stoA.ID); err != nil {
		t.Fatal(err)
	}
	camp, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, Title: "碰一碰有礼", StoreID: stoA.ID, CreatedBy: "usr_owner_a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionCampaign(camp.ID, tenA.ID, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	link, err := st.CreateLink(tenA.ID, camp.ID, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner-a@example.com"},
		"sess-mgr-s":   {"usr_mgr_scoped", "scoped-mgr@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}

	s := New(cfg, d, idc, nil, log.New(io.Discard, "", 0))

	// 内容包:video+cover 资产 → 版本(adopted)→ 平台采用指针。
	video, err := st.CreateLibAsset(tenA.ID, assetlib.Registration{
		AssetRef: "up_video_1", SHA256: strings.Repeat("a", 64), MediaType: assetlib.MediaTypeVideo,
		Source: assetlib.SourceMerchantUpload, Purpose: "活动视频", GrantRef: "grant-1",
	}, stoA.ID, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	cover, err := st.CreateLibAsset(tenA.ID, assetlib.Registration{
		AssetRef: "up_cover_1", SHA256: strings.Repeat("b", 64), MediaType: assetlib.MediaTypeImage,
		Source: assetlib.SourceMerchantUpload, Purpose: "封面", GrantRef: "grant-1",
	}, stoA.ID, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := st.CreateContentPack(tenA.ID, camp.ID, stoA.ID, "周末烘焙节", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	v, err := st.CreateContentPackVersion(tenA.ID, pack.ID, contentpack.VersionSpec{
		ContentKind: contentpack.ContentKindVideo,
		Video:       contentpack.AssetSnapshot{AssetID: video.ID, AssetRef: video.AssetRef, SHA256: video.SHA256},
		Cover:       contentpack.AssetSnapshot{AssetID: cover.ID, AssetRef: cover.AssetRef, SHA256: cover.SHA256},
		Title:       "周末烘焙节", Body: "本周六全场八折", Topics: []string{"烘焙"},
		StoreName: "南岸旗舰店", PriceNote: "全场八折", ValidNote: "本周六",
		Source: contentpack.SourceMerchantUpload,
	}, contentpack.StatusCandidate, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdoptContentPackVersion(tenA.ID, pack.ID, v.ID, "xiaohongshu", "usr_owner_a", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// 准入记录:admitted xiaohongshu miniapp(fixture)+ not_admitted douyin h5。
	seedRecords := []channeladmission.Record{
		{
			Platform: "xiaohongshu", EntryMode: channeladmission.EntryMiniapp, Scenario: "store_promo",
			AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusAdmitted,
			Capabilities: channeladmission.Capabilities{
				Media: channeladmission.CapSupported, Title: channeladmission.CapSupported,
				Body: channeladmission.CapSupported, Topic: channeladmission.CapUnknown,
				POI: channeladmission.CapUnknown, Receipt: channeladmission.CapUnknown,
				Callback: channeladmission.CapUnknown,
			},
			OfficialSources: []string{"https://miniapp.xiaohongshu.com/doc/DC530301"},
			LastCheckedAt:   "2026-10-14", ValidUntil: "2027-01-12",
		},
		{
			Platform: "douyin", EntryMode: channeladmission.EntryH5, Scenario: "store_promo",
			AppBinding: "none", TenantScope: "*", Status: channeladmission.StatusNotAdmitted,
			ReasonCode: channeladmission.ReasonNotAdmitted,
			ReasonText: "抖音发布能力使用规范明确排除商铺宣传、商品介绍等营销内容场景",
			Capabilities: channeladmission.Capabilities{
				Media: channeladmission.CapUnknown, Title: channeladmission.CapUnknown,
				Body: channeladmission.CapUnknown, Topic: channeladmission.CapUnknown,
				POI: channeladmission.CapUnknown, Receipt: channeladmission.CapUnknown,
				Callback: channeladmission.CapUnknown,
			},
			OfficialSources: []string{"https://developer.open-douyin.com/docs/resource/zh-CN/dop/operation-standard/platform-capabilities/useclue"},
			LastCheckedAt:   "2026-10-14", ValidUntil: "2027-01-12",
		},
	}
	if on {
		if _, err := s.St.LoadAdmissionSeed(&channeladmission.Seed{Version: 1, GeneratedAt: "2026-10-14", Records: seedRecords}); err != nil {
			t.Fatal(err)
		}
	}

	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &handoffFixture{s: s, ts: ts, tenA: tenA.ID, code: link.Code, packID: pack.ID}
}

// startParticipation 直接经参与服务落一条 in_progress 参与(顾客面 visitor
// 由 cookie 承载;这里用固定 visitor 便于断言)。
func (f *handoffFixture) startParticipation(t *testing.T) string {
	t.Helper()
	res := f.s.St.ResolveLink(f.code, time.Now())
	if res.Outcome != store.OutcomeAvailable {
		t.Fatalf("link not available: %s", res.Outcome)
	}
	started, err := f.s.Participation.Start(t.Context(), participation.StartInput{
		TenantID: res.Campaign.TenantID, StoreID: res.Campaign.StoreID, CampaignID: res.Campaign.ID,
		LinkCode: res.Link.Code, Source: participation.SourceQR, RuleVersion: 1,
		ContentPackVersion: "v1", VisitorID: testVisitor, IdempotencyKey: "idem_http_1",
		Window: participation.Window{
			StartsAt: res.Campaign.StartsAt, ExpiresAt: res.Campaign.EndsAt,
		},
	})
	if err != nil {
		t.Fatalf("start participation: %v", err)
	}
	return started.Participation.ID
}

// fingerprintOf asks the routes surface (pack_id) for the server-computed
// content version — the same value the customer confirms.
func (f *handoffFixture) fingerprintOf(t *testing.T, platform string) string {
	t.Helper()
	status, body := f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform="+platform+"&pack_id="+f.packID, "", "")
	if status != http.StatusOK {
		t.Fatalf("routes+pack = %d: %v", status, body)
	}
	snap, _ := body["snapshot"].(map[string]any)
	if snap == nil || snap["content_version"] == "" {
		t.Fatalf("snapshot fingerprint missing: %v", body)
	}
	return snap["content_version"].(string)
}

func (f *handoffFixture) public(t *testing.T, method, path, cookie, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, cookie, body, "", "")
}

func (f *handoffFixture) do(t *testing.T, method, path, cookie, body, session, tenant string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

const visitorCookie = "touch_participation_visitor=" + testVisitor

// Off = routes not registered + gate uniform 404 (双保险)。
func TestPublishHandoffOffIsInvisible(t *testing.T) {
	f := newHandoffFixture(t, false)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/public/links/" + f.code + "/publish-handoff/routes?platform=xiaohongshu"},
		{"POST", "/api/v1/public/links/" + f.code + "/publish-handoff"},
		{"POST", "/api/v1/public/publish-receipts/ph_x"},
		{"POST", "/api/v1/publish-handoffs/ph_x/staff-events"},
	} {
		status, _ := f.do(t, tc.method, tc.path, visitorCookie, "{}", "sess-owner-a", f.tenA)
		if status != http.StatusNotFound {
			t.Fatalf("%s %s off = %d, want 404", tc.method, tc.path, status)
		}
	}
}

// Routes 只读裁决:admitted → automated;not_admitted → refused 且无手动后门;
// 未登记平台 → unknown 诚实提示。
func TestPublishHandoffRoutesSurface(t *testing.T) {
	f := newHandoffFixture(t, true)
	status, body := f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform=xiaohongshu", "", "")
	if status != http.StatusOK {
		t.Fatalf("routes xhs = %d: %v", status, body)
	}
	routes := body["routes"].([]any)
	r0 := routes[0].(map[string]any)
	plan := r0["plan"].(map[string]any)
	if plan["kind"] != "automated" || plan["allowed"] != true {
		t.Fatalf("admitted route must be automated: %v", plan)
	}

	status, body = f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform=douyin", "", "")
	if status != http.StatusOK {
		t.Fatalf("routes douyin = %d", status)
	}
	plan = body["routes"].([]any)[0].(map[string]any)["plan"].(map[string]any)
	if plan["kind"] != "refused" {
		t.Fatalf("douyin h5 must be refused, got %v", plan["kind"])
	}
	if plan["manual"] != nil {
		t.Fatal("not_admitted must not carry a manual backdoor")
	}
	if !strings.Contains(plan["notice"].(string), "明确排除") {
		t.Fatalf("notice = %v", plan["notice"])
	}

	status, body = f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform=bilibili", "", "")
	if status != http.StatusOK {
		t.Fatalf("routes bilibili = %d", status)
	}
	if !strings.Contains(body["notice"].(string), "未知规则") {
		t.Fatalf("unregistered platform notice = %v", body["notice"])
	}

	// with_snapshot=1:顾客确认面先拿到将要交出的快照与指纹(与交接同一
	// 指纹函数,换稿=指纹变化);未登记平台(unknown→manual_only)也给,
	// 让手动指引同样绑定快照。
	status, body = f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform=xiaohongshu&with_snapshot=1", "", "")
	if status != http.StatusOK {
		t.Fatalf("routes xhs snapshot = %d: %v", status, body)
	}
	snap, _ := body["snapshot"].(map[string]any)
	if snap == nil || snap["content_version"] == "" || snap["title"] != "周末烘焙节" || snap["pack_id"] != f.packID {
		t.Fatalf("with_snapshot snapshot = %v", body["snapshot"])
	}
	status, body = f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform=bilibili&with_snapshot=1", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("unregistered platform without adopted content = %d %v", status, body)
	}

	// 拒绝面零内容探测:not_admitted 平台不给快照(与 start 的 gate-first 一致)。
	status, body = f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/routes?platform=douyin&with_snapshot=1", "", "")
	if status != http.StatusOK {
		t.Fatalf("routes douyin snapshot = %d", status)
	}
	if body["snapshot"] != nil {
		t.Fatal("refused platform must not leak the content snapshot")
	}
}

// admitted fixture 全流程。
func TestPublishHandoffFullFlow(t *testing.T) {
	f := newHandoffFixture(t, true)
	ptID := f.startParticipation(t)
	fp := f.fingerprintOf(t, "xiaohongshu")

	startBody := `{"participation_id":"` + ptID + `","platform":"xiaohongshu","pack_id":"` + f.packID + `","confirmed_content_version":"` + fp + `"}`
	status, body := f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, startBody)
	if status != http.StatusCreated {
		t.Fatalf("start = %d: %v", status, body)
	}
	handoff := body["handoff"].(map[string]any)
	if handoff["status"] != "handoff_ready" {
		t.Fatalf("status = %v", handoff["status"])
	}
	plan := body["plan"].(map[string]any)
	if plan["kind"] != "automated" {
		t.Fatalf("plan = %v", plan["kind"])
	}
	entry := plan["entry"].(map[string]any)
	if entry["miniapp_path"] != "pages/publish/index" {
		t.Fatalf("entry = %v", entry)
	}
	media := body["media"].([]any)
	if len(media) != 2 {
		t.Fatalf("media refs = %d", len(media))
	}
	recovery := body["recovery"].(map[string]any)
	if recovery["token"] == "" {
		t.Fatal("recovery credential must be present once")
	}
	handoffID := handoff["id"].(string)

	// 幂等重放:同参与同平台 → 200 同一行,不再发凭据。
	status, body = f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, startBody)
	if status != http.StatusOK {
		t.Fatalf("replay = %d", status)
	}
	if body["handoff"].(map[string]any)["id"] != handoffID {
		t.Fatal("replay must return the same handoff")
	}
	if body["recovery"] != nil {
		t.Fatal("replay must not mint a second recovery credential")
	}

	// 确认不符(换稿后旧指纹)→ 422,不改状态。
	stale := strings.Replace(startBody, fp, "stale-fp", 1)
	status, body = f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, stale)
	if status != http.StatusUnprocessableEntity || body["error"] != "confirmation_mismatch" {
		t.Fatalf("stale confirm = %d %v", status, body)
	}

	// editor-opened 无证据 → 422 evidence_required(失焦/点击不是证据)。
	status, body = f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff/"+handoffID+"/editor-opened", visitorCookie, `{"proof":"window-blur-event"}`)
	if status != http.StatusUnprocessableEntity || body["error"] != "evidence_required" {
		t.Fatalf("editor-opened = %d %v", status, body)
	}

	// 顾客 done = 核验请求,状态不变。
	status, body = f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff/"+handoffID+"/done", visitorCookie, "")
	if status != http.StatusOK || !strings.Contains(body["notice"].(string), "不算发布成功") {
		t.Fatalf("done = %d %v", status, body)
	}

	// 店员人工事实:本店店员 200;邻店/他人 404(防枚举)。
	status, body = f.do(t, "POST", "/api/v1/publish-handoffs/"+handoffID+"/staff-events", "", `{"kind":"staff_saw_submit","note":"目测顾客已提交"}`, "sess-mgr-s", f.tenA)
	if status != http.StatusOK {
		t.Fatalf("staff event = %d: %v", status, body)
	}
	if body["handoff"].(map[string]any)["status"] != "awaiting_review" {
		t.Fatalf("staff fact must move to awaiting_review: %v", body["handoff"])
	}

	// 官方回执(测试密钥签的 fixture,模拟真实平台验签语义)。
	rcpt := publishhandoff.Receipt{
		ReceiptID: "rcp_http_1", Platform: "xiaohongshu", AppRef: "app_touch_prod",
		TenantID: f.tenA, ParticipationID: ptID, AttemptID: handoff["attempt_id"].(string),
		ContentVersion: handoff["content_version"].(string), PostID: "note_77", Outcome: publishhandoff.ReceiptOutcomePublished,
		SignedAt: time.Date(2026, 10, 14, 12, 30, 0, 0, time.UTC),
	}
	sig, err := publishhandoff.SignReceiptForTest(testReceiptSecret, rcpt)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"Receipt": rcpt, "Signature": sig,
	})
	status, body = f.public(t, "POST", "/api/v1/public/publish-receipts/"+handoffID, "", string(raw))
	if status != http.StatusOK || body["accepted"] != true || body["status"] != "platform_confirmed" {
		t.Fatalf("receipt = %d %v", status, body)
	}
	// 重放:幂等,状态不变。
	status, body = f.public(t, "POST", "/api/v1/public/publish-receipts/"+handoffID, "", string(raw))
	if status != http.StatusOK || body["accepted"] != true || body["status"] != "platform_confirmed" {
		t.Fatalf("receipt replay = %d %v", status, body)
	}
	// 伪造:错签名 → accepted=false。
	forged, _ := json.Marshal(map[string]any{"Receipt": rcpt, "Signature": "deadbeef"})
	status, body = f.public(t, "POST", "/api/v1/public/publish-receipts/"+handoffID, "", string(forged))
	if status != http.StatusOK || body["accepted"] != false {
		t.Fatalf("forged receipt must be refused: %d %v", status, body)
	}
}

// not_admitted 平台开交接 → 422 handoff_refused(不给后门;拒绝先于内容探测)。
func TestPublishHandoffStartDouyinRefused(t *testing.T) {
	f := newHandoffFixture(t, true)
	ptID := f.startParticipation(t)
	startBody := `{"participation_id":"` + ptID + `","platform":"douyin","confirmed_content_version":"whatever"}`
	status, body := f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, startBody)
	if status != http.StatusUnprocessableEntity || body["error"] != "handoff_refused" {
		t.Fatalf("douyin start = %d %v", status, body)
	}
	if body["reason"] != channeladmission.ReasonNotAdmitted {
		t.Fatalf("reason = %v", body["reason"])
	}
}

// 跨访客隔离:他人的 handoff 一律 404。
func TestPublishHandoffVisitorIsolation(t *testing.T) {
	f := newHandoffFixture(t, true)
	ptID := f.startParticipation(t)
	fp := f.fingerprintOf(t, "xiaohongshu")
	startBody := `{"participation_id":"` + ptID + `","platform":"xiaohongshu","confirmed_content_version":"` + fp + `"}`
	status, body := f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, startBody)
	if status != http.StatusCreated {
		t.Fatalf("start = %d", status)
	}
	handoffID := body["handoff"].(map[string]any)["id"].(string)
	status, _ = f.public(t, "GET", "/api/v1/public/links/"+f.code+"/publish-handoff/"+handoffID, "touch_participation_visitor=visit_other", "")
	if status != http.StatusNotFound {
		t.Fatalf("other visitor = %d, want 404", status)
	}
}
