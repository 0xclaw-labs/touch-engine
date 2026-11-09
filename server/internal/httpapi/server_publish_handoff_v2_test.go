package httpapi

// server_publish_handoff_v2_test.go: HUI-1670 touch-handoff/v2 HTTP 面正反例。
//
// 冻结契约(docs/contracts/publish-handoff/v2/handoff-contract.md):
//   - A27 纯链接:无内容包/无参与可完成交接,share_url 服务端生成;
//   - media:3553 已采用可分发版本 + 指纹确认 + 受控 media-access 引用;
//   - pending_external:资格未知 422 拒绝,零落行,不承诺;
//   - 幂等:同键同负载回放,同键不同负载 409;
//   - action_revision 陈旧 = 409 stale_version;
//   - 回调:未注册平台/验签失败 422,已验真被拒 200 accepted=false;
//   - visitor 反枚举:他人 cookie 读他人交接 = 404;
//   - off = 路由不注册统一 404。

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// stubV2Directory resolves published actions by id (测试夹具只进测试;
// 生产由 4584 目录一行接线,见 server_action_directory.go)。
type stubV2Directory struct {
	refs map[string]participation.ActionRef
}

func (d *stubV2Directory) ResolveAction(_ context.Context, actionID string) (participation.ActionRef, bool, error) {
	ref, ok := d.refs[actionID]
	return ref, ok, nil
}

var _ participation.ActionDirectory = (*stubV2Directory)(nil)

type handoffV2Fixture struct {
	s      *Server
	ts     *httptest.Server
	d      *sql.DB
	tenA   string
	stoA   string
	cookie string
	actA27 string // A27 link_share 动作实例 id(sa_*,4584 生成)
	actA07 string // A07 js_media 动作实例 id
	sidA   string // A27 会话
	sidB   string // A07 会话
}

func handoffV2EnvCaps(nav, media, receipt, reward string) channeladmission.EnvCapabilities {
	return channeladmission.EnvCapabilities{
		Navigation: nav, MediaHandoff: media, PlatformReceipt: receipt, RewardEligibility: reward,
	}
}

func newHandoffV2Fixture(t *testing.T, on bool) *handoffV2Fixture {
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
		case "FEATURE_PARTICIPATION", "FEATURE_PUBLISH_HANDOFF":
			return flag
		case "FEATURE_CHANNEL_ADMISSION":
			return "on"
		case "PUBLIC_BASE_URL":
			return "https://h5.example.com"
		case "PUBLISH_HANDOFF_RECEIPT_SECRET":
			return "v2-http-receipt-secret"
		case "PUBLISH_HANDOFF_APP_REF":
			return "app_touch_prod"
		case "PUBLISH_HANDOFF_MERCHANT_APP_REF":
			return "merchant_app_ref_v2"
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
	stoA, err := st.CreateStore(tenA.ID, "旗舰店", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMember(tenA.ID, "usr_owner_a", "org_owner", "usr_owner_a", "test", true); err != nil {
		t.Fatal(err)
	}
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	// 已发布动作行(A27 纯链接目标 + A07 媒体动作;实例 id 由 store 生成)。
	_, acts, err := st.ReplaceStoreActions(tenA.ID, stoA.ID, 0, []store.StoreActionInput{
		{CatalogCode: "A27", Label: "分享门店链接", TargetRef: "https://dianping.example.com/store/123", Enabled: true},
		{CatalogCode: "A07", Label: "小红书图文", TargetRef: "https://xiaohongshu.example.com/user", Enabled: true},
	})
	if err != nil {
		t.Fatalf("seed actions: %v", err)
	}
	var actA27, actA07 string
	for _, a := range acts {
		switch a.CatalogCode {
		case "A27":
			actA27 = a.ID
		case "A07":
			actA07 = a.ID
		}
	}
	if actA27 == "" || actA07 == "" {
		t.Fatalf("seeded actions missing: %+v", acts)
	}

	// 3535 能力行:A27 link_share admitted(receipt=unsupported);A07 js_media
	// admitted(required_inputs=content_pack_version,media=supported)。
	linkCap := channeladmission.ActionCapability{
		ID: "cap_v2_link", CatalogCode: "A27", Platform: "qq", Method: channeladmission.ActionMethodLinkShare,
		Scenario: channeladmission.ActionScenarioStorePromo, ActionKind: "publish",
		Label: "分享门店链接", AppBinding: "none", TenantScope: "*",
		Status: channeladmission.StatusAdmitted,
		Environments: map[string]channeladmission.EnvCapabilities{
			channeladmission.EnvDesktopBrowser: handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvIOSSafari:      handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvAndroidBrowser: handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvIOSWechat:      handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvAndroidWechat:  handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvTargetApp:      handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
		},
		FieldSupport: map[string]string{}, RequiredInputs: []string{},
		SeedVersion: 1, EvidenceGrade: channeladmission.EvidenceOfficialDoc, CheckedAt: "2026-11-09", ValidUntil: "2027-02-07",
	}
	mediaCap := channeladmission.ActionCapability{
		ID: "cap_v2_media", CatalogCode: "A07", Platform: "xiaohongshu", Method: channeladmission.ActionMethodJSMedia,
		Scenario: channeladmission.ActionScenarioStorePromo, ActionKind: "publish",
		Label: "小红书图文", AppBinding: "platform_app", TenantScope: "*",
		Status: channeladmission.StatusAdmitted,
		Environments: map[string]channeladmission.EnvCapabilities{
			channeladmission.EnvDesktopBrowser: handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown),
			channeladmission.EnvIOSSafari:      handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown),
			channeladmission.EnvAndroidBrowser: handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown),
			channeladmission.EnvIOSWechat:      handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown),
			channeladmission.EnvAndroidWechat:  handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown),
			channeladmission.EnvTargetApp:      handoffV2EnvCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown),
		},
		FieldSupport:   map[string]string{"media": channeladmission.CapSupported, "title": channeladmission.CapUnsupported, "body": channeladmission.CapUnsupported, "topic": channeladmission.CapUnsupported},
		RequiredInputs: []string{"content_pack_version"},
		SeedVersion:    1, EvidenceGrade: channeladmission.EvidenceOfficialSnapshot, CheckedAt: "2026-11-09", ValidUntil: "2027-02-07",
	}
	for _, cap := range []channeladmission.ActionCapability{linkCap, mediaCap} {
		if _, _, err := st.UpsertActionCapability(cap, store.ControlAudit{Actor: "test", Reason: "seed"}); err != nil {
			t.Fatalf("seed capability: %v", err)
		}
	}

	// 真实门店内容包 + 版本 + 采用(media 会话内容;grant 空=不过期)。
	pack, err := st.CreateStoreContentPack(tenA.ID, stoA.ID, "", "v2 媒体包", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	video, _, err := st.RegisterContentMedia(tenA.ID, stoA.ID, upload.MediaMeta{
		SHA256: strings.Repeat("a", 64), MIME: "video/mp4", Kind: upload.KindVideo,
		ByteSize: 1024, DurationMS: 1234, Width: 64, Height: 64, MediaRef: strings.Repeat("a", 64),
	}, "v2.mp4", "usr_owner_a")
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	cover, _, err := st.RegisterContentMedia(tenA.ID, stoA.ID, upload.MediaMeta{
		SHA256: strings.Repeat("b", 64), MIME: "image/jpeg", Kind: upload.KindImage,
		ByteSize: 1024, Width: 64, Height: 64, MediaRef: strings.Repeat("b", 64),
	}, "v2.jpg", "usr_owner_a")
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	version, err := st.CreateStoreContentPackVersion(tenA.ID, pack.ID, store.StoreVersionSpec{
		ContentKind: contentpack.ContentKindVideo, VideoMediaID: video.ID, CoverMediaID: cover.ID,
		Title: "周末烘焙节", Body: "全场八折", Topics: []string{"烘焙"}, StoreName: "旗舰店", PriceNote: "全场八折", ValidNote: "2026-11-30",
		GrantRefs: []string{"grant-1"}, Source: contentpack.SourceMerchantUpload,
	}, contentpack.StatusCandidate, "usr_owner_a")
	if err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := st.AdoptStoreContentPackVersion(tenA.ID, pack.ID, version.ID, "xiaohongshu", "usr_owner_a", time.Now()); err != nil {
		t.Fatalf("seed adopt: %v", err)
	}

	// 会话目录接线(两个动作;平台/Kind 照目录真值)。
	s.Participation.SetActionDirectory(&stubV2Directory{refs: map[string]participation.ActionRef{
		actA27: {
			ActionID: actA27, CatalogCode: "A27", Platform: "qq", Kind: "share",
			TenantID: tenA.ID, StoreID: stoA.ID, StorefrontRevision: 1, ActionRevision: 7, Enabled: true,
		},
		actA07: {
			ActionID: actA07, CatalogCode: "A07", Platform: "xiaohongshu", Kind: "publish",
			TenantID: tenA.ID, StoreID: stoA.ID, StorefrontRevision: 1, ActionRevision: 9,
			ContentPackVersion: version.ID, Enabled: true,
		},
	}})

	f := &handoffV2Fixture{s: s, ts: ts, d: d, tenA: tenA.ID, stoA: stoA.ID, actA27: actA27, actA07: actA07}
	if !on {
		// off:面不可见,会话面同样未注册;路由级 404 用例不需要会话。
		return f
	}
	status, hdr, body := f.do(t, "POST", "/api/v1/public/actions/"+actA27+"/sessions", "", `{"touchpoint_source":"nfc"}`)
	if status != http.StatusCreated {
		t.Fatalf("session A = %d (%v)", status, body)
	}
	f.cookie = cookiePair(hdr)
	f.sidA, _ = body["action_session_id"].(string)
	status, _, body = f.do(t, "POST", "/api/v1/public/actions/"+actA07+"/sessions", f.cookie, `{"touchpoint_source":"nfc"}`)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("session B = %d (%v)", status, body)
	}
	f.sidB, _ = body["action_session_id"].(string)
	return f
}

func (f *handoffV2Fixture) do(t *testing.T, method, path, cookie, body string) (int, http.Header, map[string]any) {
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
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, res.Header, out
}

// off = 路由不注册,统一 404(v2 面与 v1 共用 FEATURE_PUBLISH_HANDOFF 双保险)。
func TestActionHandoffV2InvisibleWhenOff(t *testing.T) {
	f := newHandoffV2Fixture(t, false)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/public/actions/" + f.actA27 + "/handoffs/plan?method=link_share&environment=ios_safari"},
		{"POST", "/api/v1/public/actions/" + f.actA27 + "/handoffs"},
		{"GET", "/api/v1/public/handoffs/pha_x"},
		{"POST", "/api/v1/public/handoff-callbacks/wechat"},
	} {
		if status, _, _ := f.do(t, tc.method, tc.path, "", `{}`); status != http.StatusNotFound {
			t.Fatalf("%s %s off = %d, want 404", tc.method, tc.path, status)
		}
	}
}

// A27 纯链接正例全链:plan(confirmation_required)→ start(无参与)→ 幂等回放
// → 冲突 → 查态 → facts 去重 → cancel。
func TestActionHandoffV2LinkShareHappyPath(t *testing.T) {
	f := newHandoffV2Fixture(t, true)

	// 计划预览:official_handoff + confirmation_required=share_url。
	status, _, body := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA27+"/handoffs/plan?method=link_share&environment=ios_safari&payload_kind=link_share&action_session_id="+f.sidA,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan = %d (%v)", status, body)
	}
	plan := body["plan"].(map[string]any)
	if plan["kind"] != "official_handoff" || plan["allowed"] != true {
		t.Fatalf("plan = %v", plan)
	}
	if plan["evidence_level"] != "none" {
		t.Fatalf("receipt=unsupported must map evidence_level=none: %v", plan)
	}
	shareURL, _ := body["confirmation_required"].(string)
	if shareURL != "https://dianping.example.com/store/123" {
		t.Fatalf("confirmation_required = %v", body["confirmation_required"])
	}

	// 开交接:201 + 无参与 + open_url client_action。
	startBody := `{"action_session_id":"` + f.sidA + `","action_revision":7,"payload_kind":"link_share",
		"method":"link_share","environment":"ios_safari","confirmation":"https://dianping.example.com/store/123",
		"idempotency_key":"idem-v2-link"}`
	status, _, body = f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, strings.ReplaceAll(startBody, "\n", ""))
	if status != http.StatusCreated {
		t.Fatalf("start = %d (%v)", status, body)
	}
	handoff := body["handoff"].(map[string]any)
	hid, _ := handoff["handoff_id"].(string)
	if hid == "" || handoff["status"] != "handoff_ready" || handoff["plan_kind"] != "official_handoff" {
		t.Fatalf("handoff = %v", handoff)
	}
	if _, has := handoff["participation_id"]; has {
		t.Fatal("link_share must not create a participation")
	}
	if handoff["share_url"] != "https://dianping.example.com/store/123" {
		t.Fatalf("share_url = %v", handoff["share_url"])
	}
	ca := handoff["client_action"].(map[string]any)
	if ca["type"] != "open_url" || ca["url"] != "https://dianping.example.com/store/123" {
		t.Fatalf("client_action = %v", ca)
	}

	// 幂等回放:同键同负载 200 回原行。
	status, _, body = f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, strings.ReplaceAll(startBody, "\n", ""))
	if status != http.StatusOK {
		t.Fatalf("replay = %d", status)
	}
	if body["handoff"].(map[string]any)["handoff_id"] != hid {
		t.Fatal("replay must return the original row")
	}

	// 同键不同负载(顾客确认了另一个内容)= 409 IDEMPOTENCY_CONFLICT。
	conflict := strings.ReplaceAll(startBody, "https://dianping.example.com/store/123", "https://dianping.example.com/store/999")
	if status, _, _ := f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, conflict); status != http.StatusConflict {
		t.Fatalf("conflict = %d, want 409", status)
	}

	// 查态:本人可读;他人 visitor 404(反枚举)。
	if status, _, body := f.do(t, "GET", "/api/v1/public/handoffs/"+hid, f.cookie, ""); status != http.StatusOK {
		t.Fatalf("get = %d (%v)", status, body)
	}
	if status, _, _ := f.do(t, "GET", "/api/v1/public/handoffs/"+hid, "touch_participation_visitor=ffffffffffffffffffffffffffffffff", ""); status != http.StatusNotFound {
		t.Fatalf("foreign visitor = %d, want 404", status)
	}

	// facts:点击落事件;同 source+event_id 去重;同键不同 kind 冲突。
	factBody := `{"kind":"customer_click","source":"customer_page","event_id":"evt-1"}`
	if status, _, body := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/facts", f.cookie, factBody); status != http.StatusOK || body["deduped"] != false {
		t.Fatalf("fact = %d (%v)", status, body)
	}
	if status, _, body := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/facts", f.cookie, factBody); status != http.StatusOK || body["deduped"] != true {
		t.Fatalf("fact replay = %d (%v)", status, body)
	}
	conflictFact := `{"kind":"wake_attempt","source":"customer_page","event_id":"evt-1"}`
	if status, _, _ := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/facts", f.cookie, conflictFact); status != http.StatusConflict {
		t.Fatalf("fact conflict = %d, want 409", status)
	}

	// 取消:终态;取消后再 fact 不再可用?事实面仍可记录(审计事实),但交接终态。
	if status, _, body := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/cancel", f.cookie, `{"reason":"guest left"}`); status != http.StatusOK || body["handoff"].(map[string]any)["status"] != "canceled" {
		t.Fatalf("cancel = %d (%v)", status, body)
	}
}

// 资格未知(method 无记录)→ pending_external 422 零落行,不承诺。
func TestActionHandoffV2PendingExternalRefuses(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	startBody := `{"action_session_id":"` + f.sidA + `","action_revision":7,"payload_kind":"link_share",
		"method":"h5_share","environment":"ios_safari","confirmation":"https://dianping.example.com/store/123",
		"idempotency_key":"idem-v2-unknown"}`
	status, _, body := f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, strings.ReplaceAll(startBody, "\n", ""))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown method = %d (%v)", status, body)
	}
	if body["error"] != "handoff_refused" || body["pending_external"] != true {
		t.Fatalf("refusal = %v", body)
	}
	if strings.Contains(body["notice"].(string), "手动发布指引") {
		t.Fatalf("pending_external must not promise manual completion: %v", body["notice"])
	}
}

// media 正例:confirmation_required 指纹 → start → 受控 media 引用(platform_fetch)。
func TestActionHandoffV2MediaHappyPath(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	status, _, body := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=js_media_handoff&environment=ios_safari&payload_kind=media&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan = %d (%v)", status, body)
	}
	fingerprint, _ := body["confirmation_required"].(string)
	if fingerprint == "" {
		t.Fatalf("media plan must carry the confirmation fingerprint: %v", body)
	}
	startBody := `{"action_session_id":"` + f.sidB + `","action_revision":9,"payload_kind":"media",
		"method":"js_media_handoff","environment":"ios_safari","confirmation":"` + fingerprint + `",
		"idempotency_key":"idem-v2-media"}`
	status, _, body = f.do(t, "POST", "/api/v1/public/actions/"+f.actA07+"/handoffs", f.cookie, strings.ReplaceAll(startBody, "\n", ""))
	if status != http.StatusCreated {
		t.Fatalf("start = %d (%v)", status, body)
	}
	handoff := body["handoff"].(map[string]any)
	if handoff["payload_kind"] != "media" || handoff["content_version"] != fingerprint {
		t.Fatalf("handoff = %v", handoff)
	}
	mediaList := body["media"].([]any)
	if len(mediaList) != 2 {
		t.Fatalf("media refs = %v", body["media"])
	}
	m0 := mediaList[0].(map[string]any)
	if !strings.HasPrefix(m0["url"].(string), "https://h5.example.com/api/v1/public/content/media/") {
		t.Fatalf("media url = %v", m0)
	}
	if m0["purpose"] != "platform_fetch" {
		t.Fatalf("official handoff media purpose = %v", m0["purpose"])
	}
	// 换内容未重新确认 = 422 confirmation_mismatch。
	stale := strings.ReplaceAll(strings.ReplaceAll(startBody, `"confirmation":"`+fingerprint+`"`, `"confirmation":"deadbeef"`), "idem-v2-media", "idem-v2-media-2")
	if status, _, _ := f.do(t, "POST", "/api/v1/public/actions/"+f.actA07+"/handoffs", f.cookie, stale); status != http.StatusUnprocessableEntity {
		t.Fatalf("stale confirmation = %d, want 422", status)
	}
}

// action_revision 陈旧 = 409 stale_version(3546 同码)。
func TestActionHandoffV2StaleRevision(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	startBody := `{"action_session_id":"` + f.sidA + `","action_revision":6,"payload_kind":"link_share",
		"method":"link_share","environment":"ios_safari","confirmation":"https://dianping.example.com/store/123",
		"idempotency_key":"idem-v2-stale"}`
	if status, _, body := f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, strings.ReplaceAll(startBody, "\n", "")); status != http.StatusConflict {
		t.Fatalf("stale revision = %d (%v)", status, body)
	}
}

// 缺必填字段 = 400 param_required。
func TestActionHandoffV2MissingFields(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	if status, _, _ := f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, `{"payload_kind":"link_share"}`); status != http.StatusBadRequest {
		t.Fatal("missing fields must 400")
	}
	if status, _, _ := f.do(t, "GET", "/api/v1/public/actions/"+f.actA27+"/handoffs/plan?method=link_share&action_session_id="+f.sidA, f.cookie, ""); status != http.StatusBadRequest {
		t.Fatal("plan without environment must 400")
	}
}

// 回调:未注册平台 422(验签缺位,NOT_VERIFIED 残差);未知交接引用 404;
// 错绑定已验真 200 accepted=false(照录拒绝,停止重试)。
func TestActionHandoffV2CallbackSurface(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	// 未注册平台:无 adapter → 422 receipt_unverified。
	if status, _, body := f.do(t, "POST", "/api/v1/public/handoff-callbacks/bilibili", "", `{"x":1}`); status != http.StatusUnprocessableEntity || body["error"] != "receipt_unverified" {
		t.Fatalf("unregistered callback = %d (%v)", status, body)
	}
	// 已验真但查无此行 → 404(反枚举同形;平台侧停止重试)。
	f.s.PublishHandoff.SetAdapter(&v2CallbackStub{platform: "qq", receipt: publishhandoff.Receipt{
		ReceiptID: "rcp_cb_2", Platform: "qq", AppRef: "app_touch_prod",
		TenantID: f.tenA, HandoffID: "pha_does_not_exist", Outcome: publishhandoff.ReceiptOutcomePublished,
	}})
	if status, _, _ := f.do(t, "POST", "/api/v1/public/handoff-callbacks/qq", "", `{"x":1}`); status != http.StatusNotFound {
		t.Fatal("unknown handoff reference must 404")
	}
	// 真实交接 + 错租户绑定:已验真被拒 → 200 accepted=false。
	startBody := `{"action_session_id":"` + f.sidA + `","action_revision":7,"payload_kind":"link_share",
		"method":"link_share","environment":"ios_safari","confirmation":"https://dianping.example.com/store/123",
		"idempotency_key":"idem-v2-cb"}`
	status, _, body := f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, strings.ReplaceAll(startBody, "\n", ""))
	if status != http.StatusCreated {
		t.Fatalf("start = %d (%v)", status, body)
	}
	hid := body["handoff"].(map[string]any)["handoff_id"].(string)
	f.s.PublishHandoff.SetAdapter(&v2CallbackStub{platform: "qq", receipt: publishhandoff.Receipt{
		ReceiptID: "rcp_cb_3", Platform: "qq", AppRef: "app_touch_prod",
		TenantID: "tnt_wrong", HandoffID: hid, Outcome: publishhandoff.ReceiptOutcomePublished,
	}})
	if status, _, body := f.do(t, "POST", "/api/v1/public/handoff-callbacks/qq", "", `{"x":1}`); status != http.StatusOK || body["accepted"] != false {
		t.Fatalf("binding-mismatch callback = %d (%v)", status, body)
	}
}

type v2CallbackStub struct {
	platform string
	receipt  publishhandoff.Receipt
}

func (a *v2CallbackStub) Platform() string { return a.platform }
func (a *v2CallbackStub) Prepare(_ context.Context, _ publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	return publishhandoff.PrepareResult{ClientAction: publishhandoff.ClientAction{Type: publishhandoff.ClientActionOpenURL}}, nil
}
func (a *v2CallbackStub) Submit(_ context.Context, _ publishhandoff.Handoff, _ publishhandoff.PrepareResult) error {
	return publishhandoff.ErrUnsupported
}
func (a *v2CallbackStub) Query(_ context.Context, _ publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	return publishhandoff.QueryOutcome{}, publishhandoff.ErrUnsupported
}
func (a *v2CallbackStub) VerifyCallback(_ context.Context, _ publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return a.receipt, nil
}
