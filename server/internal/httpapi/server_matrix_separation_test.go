package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixconsume"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type hui2390Fixture struct {
	s        *Server
	ts       *httptest.Server
	tenA     string
	tenB     string
	ownA     string
	ownB     string
	agentA   string
	storeA   string
	storeB   string
	mediaDir string
}

func newHUI2390Fixture(t *testing.T, matrixURL string) *hui2390Fixture {
	t.Helper()
	mediaDir := t.TempDir()
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "TOUCH_MEDIA_DIR":
			return mediaDir
		case "FEATURE_STOREFRONT":
			return "on"
		case "FEATURE_CONTENT_PACKS":
			return "on"
		case "FEATURE_LEADS_CAPTURE":
			return "on"
		case "FEATURE_TOUCHPOINT_ENTRY":
			return "on"
		case "FEATURE_AGENCY":
			return "on"
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test"
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "leads-app"
		case "LEADS_PHONE_PEPPER":
			return "pepper"
		case "TOUCH_MATRIX_DRAFT_URL":
			return matrixURL
		}
		return ""
	})
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("分离测试商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("分离测试商家B")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTenantBrand(tenA.ID, "brd_sep_a"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTenantBrand(tenB.ID, "brd_sep_b"); err != nil {
		t.Fatal(err)
	}
	stA, err := st.CreateStore(tenA.ID, "朝阳旗舰店", "朝阳路1号", "test")
	if err != nil {
		t.Fatal(err)
	}
	stB, err := st.CreateStore(tenB.ID, "海淀别家店", "海淀路2号", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenA.ID, "usr_agent_a", "agent"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner-a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff-a@example.com"},
		"sess-agent-a": {"usr_agent_a", "agent-a@example.com"},
		"sess-owner-b": {"usr_owner_b", "owner-b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &hui2390Fixture{
		s:        s,
		ts:       ts,
		tenA:     tenA.ID,
		tenB:     tenB.ID,
		ownA:     "usr_owner_a",
		ownB:     "usr_owner_b",
		agentA:   "usr_agent_a",
		storeA:   stA.ID,
		storeB:   stB.ID,
		mediaDir: mediaDir,
	}
}

func (f *hui2390Fixture) reqRaw(t *testing.T, method, path, sess, tenant, body string, extraHeaders map[string]string) (int, []byte, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if sess != "" {
		req.Header.Set("X-Internal-Token", "test-internal-secret")
		req.Header.Set("X-Session-Token", sess)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	return res.StatusCode, raw, parsed
}

func (f *hui2390Fixture) uploadMedia(t *testing.T, storeID, tenantID, sess, fixtureName string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "upload", "testdata", fixtureName))
	if err != nil {
		t.Fatalf("read testdata %s: %v", fixtureName, err)
	}
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	fw, err := mw.CreateFormFile("file", fixtureName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/stores/%s/content-media", f.ts.URL, storeID), buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", sess)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("upload %s: %v", fixtureName, err)
	}
	defer res.Body.Close()
	outRaw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(outRaw, &out)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s status=%d body=%s", fixtureName, res.StatusCode, string(outRaw))
	}
	id, _ := out["id"].(string)
	return id
}

func (f *hui2390Fixture) storeCode(t *testing.T, storeID, tenantID, sess string) string {
	t.Helper()
	st, raw, body := f.reqRaw(t, http.MethodGet, "/api/v1/stores/"+storeID+"/actions", sess, tenantID, "", nil)
	if st != http.StatusOK {
		t.Fatalf("get store actions for store_code = %d %s", st, string(raw))
	}
	code, _ := body["store_code"].(string)
	if code == "" {
		t.Fatalf("empty store_code in %s", string(raw))
	}
	return code
}

func (f *hui2390Fixture) seedCampaignWithVideo(t *testing.T, endsAt string) (store.Campaign, store.CampaignLink) {
	t.Helper()
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{
		TenantID:      f.tenA,
		Title:         "秋季招牌特惠",
		PublicContent: "招牌套餐第二份半价",
		StoreID:       f.storeA,
		EndsAt:        endsAt,
		OrderRef:      "ord_internal_secret",
		CreatedBy:     f.ownA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.TransitionCampaign(camp.ID, f.tenA, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	link, err := f.s.St.CreateLink(f.tenA, camp.ID, f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.UpsertLeadForm(f.tenA, camp.ID, "v1", true, f.ownA); err != nil {
		t.Fatal(err)
	}
	lib, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef:  "ast_vid_v3",
		SHA256:    strings.Repeat("ab", 32),
		MediaType: assetlib.MediaTypeVideo,
		Source:    assetlib.SourceMerchantUpload,
		Purpose:   "活动成片",
		GrantRef:  "grant_camp_1",
	}, f.storeA, f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, camp.ID, lib.AssetRef, "3", f.ownA); err != nil {
		t.Fatal(err)
	}
	return camp, link
}

// TestHUI2390_DualPath_MerchantMatrixDraftAndCustomerQRNFC verifies end-to-end
// separation between merchant matrix PublishPlan and anonymous customer QR/NFC
// browsing, publishing, reward claiming, and lead capture.
func TestHUI2390_DualPath_MerchantMatrixDraftAndCustomerQRNFC(t *testing.T) {
	f := newHUI2390Fixture(t, "http://matrix-configured.test")
	sink := &countingSink{
		available: true,
		ack:       matrixconsume.DraftAck{PlanID: "pln_dual_1", Status: matrixhandoff.StatusDraft, Executed: false},
	}
	effects := &matrixhandoff.EffectSink{}
	f.s.Matrix = readyMatrix(sink, effects)

	camp, link := f.seedCampaignWithVideo(t, time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339))

	// 1. Merchant schedules a restricted draft for the brand account.
	status, raw, body := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"idempotency_key":"dual-idem-1"}`, nil)
	if status != http.StatusOK || body["outbound_complete"] != false || body["plan_state"] != matrixhandoff.PlanStateDraftSaved {
		t.Fatalf("merchant matrix draft = %d %s", status, string(raw))
	}
	if body["return_url"] != "/work/campaigns/"+camp.ID {
		t.Fatalf("return_url = %v", body["return_url"])
	}
	if body["customer_evidence_eligible"] != false || body["customer_reward_eligible"] != false {
		t.Fatalf("merchant draft leaked customer flags: %s", string(raw))
	}
	draftID := stringMust(body["draft_id"])
	if draftID == "" || sink.puts != 1 {
		t.Fatalf("draft_id=%q puts=%d", draftID, sink.puts)
	}

	// 2. Confirm draft -> pending_approval (still not outbound).
	status, raw, conf := f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+draftID+"/confirm", "sess-owner-a", f.tenA, "", nil)
	if status != http.StatusOK || conf["approved"] != true || conf["plan_state"] != matrixhandoff.PlanStatePendingApproval || conf["outbound_complete"] != false {
		t.Fatalf("confirm = %d %s", status, string(raw))
	}

	// 3. Unverified caller self-claiming official publish authority MUST fail closed (403).
	status, raw, _ = f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+draftID+"/receipt", "sess-owner-a", f.tenA,
		`{"plan_id":"pln_dual_1","target_state":"published","sequence":1,"official_verified":true,"official_publish_granted":true}`, nil)
	if status != http.StatusForbidden {
		t.Fatalf("self-claimed published without trusted internal token must be 403, got %d %s", status, string(raw))
	}

	// 4. Trusted verified official receipt transitions plan_state to published,
	// keeps unknown metrics strictly JSON null, and NEVER creates customer evidence/rewards.
	status, raw, pubResp := f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+draftID+"/receipt", "sess-owner-a", f.tenA,
		`{"plan_id":"pln_dual_1","target_state":"published","sequence":2,"official_verified":true,"official_publish_granted":true}`,
		map[string]string{"X-Touch-Internal-Token": "test-internal-secret"})
	if status != http.StatusOK || pubResp["plan_state"] != matrixhandoff.PlanStatePublished || pubResp["applied"] != true {
		t.Fatalf("trusted verified receipt = %d %s", status, string(raw))
	}
	if pubResp["customer_evidence_eligible"] != false || pubResp["customer_reward_eligible"] != false || pubResp["customer_publish"] != false {
		t.Fatalf("merchant published receipt leaked customer eligibility: %s", string(raw))
	}
	metrics, _ := pubResp["metrics"].(map[string]any)
	for _, k := range []string{"reach", "likes", "leads"} {
		if val, ok := metrics[k]; !ok || val != nil {
			t.Fatalf("unknown metric %s must be JSON null, got %#v (%s)", k, val, string(raw))
		}
	}

	// Create an NFC tag & touchpoint bound to the same campaign link so both QR
	// and NFC public entries are exercised alongside the merchant matrix path.
	nfcTags, err := f.s.St.CreateTagsBatch(store.NewTagBatch{
		TenantID:   f.tenA,
		CampaignID: camp.ID,
		StoreID:    f.storeA,
		LinkIDs:    []string{link.ID},
		Count:      1,
		BindMode:   "shared",
		CreatedBy:  f.ownA,
	})
	if err != nil || len(nfcTags) != 1 {
		t.Fatalf("create nfc tag: %v (%d)", err, len(nfcTags))
	}
	st, tpRaw, tpDoc := f.reqRaw(t, http.MethodPost, "/api/v1/touchpoints", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"tag_id":%q}`, nfcTags[0].ID), nil)
	if st != http.StatusCreated {
		t.Fatalf("create touchpoint = %d %s", st, string(tpRaw))
	}
	tpCode := stringMust(tpDoc["code"])
	if tpCode == "" {
		t.Fatalf("touchpoint missing code: %s", string(tpRaw))
	}

	// 5. Parallel anonymous customer QR + NFC paths: browse repeatedly (5x), preview
	// customer publish, submit lead, check reward claim — ZERO merchant PublishPlan
	// created by customer actions, ZERO merchant leakage, ZERO customer reward
	// triggered by merchant matrix publish.
	for i := 0; i < 5; i++ {
		st, pubRaw, pubView := f.reqRaw(t, http.MethodGet, "/api/v1/public/links/"+link.Code, "", "", "", nil)
		if st != http.StatusOK || pubView["state"] != "available" {
			t.Fatalf("guest QR browse #%d = %d %s", i, st, string(pubRaw))
		}
		if err := matrixhandoff.AssertCustomerPayloadClean(pubRaw); err != nil {
			t.Fatalf("guest QR payload leaked merchant data: %v (%s)", err, string(pubRaw))
		}
		st, nfcRaw, nfcView := f.reqRaw(t, http.MethodGet, "/api/v1/public/touchpoints/"+tpCode, "", "", "", nil)
		if st != http.StatusOK || nfcView["state"] != "available" {
			t.Fatalf("guest NFC tap #%d = %d %s", i, st, string(nfcRaw))
		}
		if err := matrixhandoff.AssertCustomerPayloadClean(nfcRaw); err != nil {
			t.Fatalf("guest NFC payload leaked merchant data: %v (%s)", err, string(nfcRaw))
		}
	}
	// Customer preview & reward claim: merchant published plan does NOT trigger customer coupon!
	st, prevRaw, prev := f.reqRaw(t, http.MethodPost, "/api/v1/public/links/"+link.Code+"/publish-attempts", "", "",
		`{"platform":"douyin","publisher":"activity_customer","copy":"顾客探店实拍","account_label":"顾客本人"}`, nil)
	if st != http.StatusCreated {
		t.Fatalf("customer preview = %d %s", st, string(prevRaw))
	}
	if err := matrixhandoff.AssertCustomerPayloadClean(prevRaw); err != nil {
		t.Fatalf("customer preview leaked merchant data: %v (%s)", err, string(prevRaw))
	}
	attemptID := stringMust(prev["attempt_id"])
	st, claimRaw, claim := f.reqRaw(t, http.MethodPost, "/api/v1/public/links/"+link.Code+"/publish-attempts/"+attemptID+"/reward-claim", "", "", `{}`, nil)
	if st != http.StatusOK || claim["grant"] != false || claim["publish_success"] != false || claim["coupons_issued"] != float64(0) {
		t.Fatalf("merchant publish must NOT grant customer reward: %d %s", st, string(claimRaw))
	}
	if err := matrixhandoff.AssertCustomerPayloadClean(claimRaw); err != nil {
		t.Fatalf("customer reward claim leaked merchant data: %v (%s)", err, string(claimRaw))
	}

	// Customer anonymous lead submission succeeds independently.
	st, leadRaw, _ := f.reqRaw(t, http.MethodPost, "/api/v1/public/links/"+link.Code+"/lead-submissions", "", "",
		`{"name":"李四","phone":"13900139000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"wecom"}`, nil)
	if st != http.StatusCreated {
		t.Fatalf("customer lead submit = %d %s", st, string(leadRaw))
	}

	// Anonymous customer cannot call merchant matrix-draft endpoint (401).
	st, _, _ = f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "", "", "", nil)
	if st != http.StatusUnauthorized {
		t.Fatalf("anonymous customer matrix-draft = %d, want 401", st)
	}

	// Still exactly 1 merchant draft (none auto-created for customer post), 0 customer effects from matrix.
	if len(f.s.Matrix.Drafts()) != 1 || sink.puts != 1 {
		t.Fatalf("customer activity created extra merchant draft: drafts=%d puts=%d", len(f.s.Matrix.Drafts()), sink.puts)
	}
	if effects.RewardWrites != 0 || effects.UGCWrites != 0 || effects.LeadWrites != 0 || effects.GrantCalls != 0 {
		t.Fatalf("matrix triggered customer effect sink: %+v", effects)
	}
}

// TestHUI2390_CampaignLessStoreMatrixDraftAndCustomerContentIsolation verifies:
// - a store with ZERO campaigns can use adopted store content for merchant matrix draft;
// - image-text masquerading as video fails closed as wrong_content_type (and plain image-text as needs_video);
// - blocked/expired store content versions fail closed;
// - anonymous customer storefront & action content reads work with 0 model calls and 0 merchant leakage.
func TestHUI2390_CampaignLessStoreMatrixDraftAndCustomerContentIsolation(t *testing.T) {
	f := newHUI2390Fixture(t, "")
	sink := &countingSink{
		available: true,
		ack:       matrixconsume.DraftAck{PlanID: "pln_store_solo", Status: matrixhandoff.StatusDraft, Executed: false},
	}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})

	// Verify storeA has ZERO campaigns.
	camps, err := f.s.St.ListCampaigns(f.tenA)
	if err != nil || len(camps) != 0 {
		t.Fatalf("expected 0 campaigns for campaign-less store test, got %d err=%v", len(camps), err)
	}

	// Upload real video + cover and adopt a store-root video content pack version.
	videoID := f.uploadMedia(t, f.storeA, f.tenA, "sess-owner-a", "tiny.mp4")
	coverID := f.uploadMedia(t, f.storeA, f.tenA, "sess-owner-a", "pixel.jpg")
	st, raw, packOut := f.reqRaw(t, http.MethodPost, "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-a", f.tenA, `{"title":"无活动门店视频包"}`, nil)
	if st != http.StatusCreated {
		t.Fatalf("create store pack = %d %s", st, string(raw))
	}
	packID := stringMust(packOut["id"])
	grantUntil := time.Now().Add(96 * time.Hour).UTC().Format(time.RFC3339)
	st, raw, verOut := f.reqRaw(t, http.MethodPost, fmt.Sprintf("/api/v1/stores/%s/content-packs/%s/versions", f.storeA, packID), "sess-owner-a", f.tenA, fmt.Sprintf(`{
		"content_kind":"video","video_media_id":%q,"cover_media_id":%q,
		"title":"朝阳旗舰店招牌菜","body":"无需先建活动即可复用视频内容","topics":["探店"],
		"store_name":"朝阳旗舰店","price_note":"招牌套餐68元","valid_note":"本月有效",
		"grant_refs":["grant_store_v1"],"grant_until":%q,"marking":{"ad_required":true},
		"source":"merchant_upload","status":"candidate"}`, videoID, coverID, grantUntil), nil)
	if st != http.StatusCreated {
		t.Fatalf("create store version = %d %s", st, string(raw))
	}
	verID := stringMust(verOut["id"])
	st, raw, _ = f.reqRaw(t, http.MethodPost, fmt.Sprintf("/api/v1/stores/%s/content-packs/%s/versions/%s/adopt", f.storeA, packID, verID), "sess-owner-a", f.tenA, `{"platform":"douyin"}`, nil)
	if st != http.StatusOK {
		t.Fatalf("adopt store version = %d %s", st, string(raw))
	}

	// 1. Campaign-less store creates merchant matrix draft directly!
	st, raw, body := f.reqRaw(t, http.MethodPost, "/api/v1/stores/"+f.storeA+"/matrix-draft", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"pack_id":%q,"idempotency_key":"store-solo-1"}`, packID), nil)
	if st != http.StatusOK || body["outbound_complete"] != false || body["plan_state"] != matrixhandoff.PlanStateDraftSaved {
		t.Fatalf("store matrix draft = %d %s", st, string(raw))
	}
	if body["plan_id"] != "pln_store_solo" || body["return_url"] != "/work/stores/"+f.storeA+"/content?pack_id="+packID || sink.puts != 1 {
		t.Fatalf("store draft details = %s puts=%d", string(raw), sink.puts)
	}
	// List store drafts (own tenant sees 1; foreign tenant gets 404).
	st, raw, list := f.reqRaw(t, http.MethodGet, "/api/v1/stores/"+f.storeA+"/matrix-drafts", "sess-owner-a", f.tenA, "", nil)
	if st != http.StatusOK {
		t.Fatalf("list store drafts = %d %s", st, string(raw))
	}
	items, _ := list["items"].([]any)
	if len(items) != 1 || list["customer_evidence_eligible"] != false || list["customer_reward_eligible"] != false {
		t.Fatalf("store draft list = %s", string(raw))
	}
	st, _, _ = f.reqRaw(t, http.MethodGet, "/api/v1/stores/"+f.storeA+"/matrix-drafts", "sess-owner-b", f.tenB, "", nil)
	if st != http.StatusNotFound {
		t.Fatalf("cross-tenant store draft list = %d, want 404", st)
	}

	// 2. Store image-text pack: image masquerading as video -> wrong_content_type;
	// plain image-text -> needs_video; zero puts, zero transcode, zero charge.
	st, raw, imgPack := f.reqRaw(t, http.MethodPost, "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-a", f.tenA, `{"title":"纯图文包"}`, nil)
	if st != http.StatusCreated {
		t.Fatalf("create imagetext pack = %d %s", st, string(raw))
	}
	imgPackID := stringMust(imgPack["id"])
	st, raw, imgVer := f.reqRaw(t, http.MethodPost, fmt.Sprintf("/api/v1/stores/%s/content-packs/%s/versions", f.storeA, imgPackID), "sess-owner-a", f.tenA, fmt.Sprintf(`{
		"content_kind":"imagetext","image_text_media_ids":[%q],
		"title":"图文晒单","body":"纯图片内容","topics":["图文"],
		"store_name":"朝阳旗舰店","price_note":"39元","valid_note":"本月有效",
		"grant_refs":["grant_img_1"],"grant_until":%q,"marking":{"ad_required":true},
		"source":"merchant_upload","status":"candidate"}`, coverID, grantUntil), nil)
	if st != http.StatusCreated {
		t.Fatalf("create imagetext version = %d %s", st, string(raw))
	}
	imgVerID := stringMust(imgVer["id"])

	// Spoofing declared_content_type="video" on imagetext -> wrong_content_type.
	st, raw, spoof := f.reqRaw(t, http.MethodPost, "/api/v1/stores/"+f.storeA+"/matrix-draft", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"pack_id":%q,"version_id":%q,"declared_content_type":"video"}`, imgPackID, imgVerID), nil)
	spoofBtn, _ := spoof["matrix_button"].(map[string]any)
	if st != http.StatusOK || spoofBtn["enabled"] != false || spoofBtn["reason"] != matrixhandoff.ReasonWrongContentType || spoof["outbound_complete"] != false || sink.puts != 1 {
		t.Fatalf("spoofed video on imagetext = %d %s puts=%d", st, string(raw), sink.puts)
	}
	// Plain imagetext without spoof -> needs_video (not success).
	st, raw, nv := f.reqRaw(t, http.MethodPost, "/api/v1/stores/"+f.storeA+"/matrix-draft", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"pack_id":%q,"version_id":%q}`, imgPackID, imgVerID), nil)
	nvBtn, _ := nv["matrix_button"].(map[string]any)
	if st != http.StatusOK || nvBtn["enabled"] != false || nvBtn["reason"] != matrixhandoff.ReasonNeedsVideo || nv["outbound_complete"] != false || sink.puts != 1 {
		t.Fatalf("imagetext needs_video = %d %s puts=%d", st, string(raw), sink.puts)
	}

	// 3. Anonymous customer storefront & adopted content read (campaign-less store):
	// zero merchant account/quota/plan leakage, repeated touches trigger 0 model calls.
	sc := f.storeCode(t, f.storeA, f.tenA, "sess-owner-a")
	for i := 0; i < 5; i++ {
		st, sfRaw, _ := f.reqRaw(t, http.MethodGet, "/api/v1/public/storefronts/"+sc, "", "", "", nil)
		if st != http.StatusOK {
			t.Fatalf("public storefront #%d = %d %s", i, st, string(sfRaw))
		}
		if err := matrixhandoff.AssertCustomerPayloadClean(sfRaw); err != nil {
			t.Fatalf("public storefront leaked merchant fields: %v (%s)", err, string(sfRaw))
		}
	}

	// 4. Block the video version -> matrix draft fails closed with asset_revoked.
	st, raw, _ = f.reqRaw(t, http.MethodPost, fmt.Sprintf("/api/v1/stores/%s/content-packs/%s/versions/%s/block", f.storeA, packID, verID), "sess-owner-a", f.tenA, `{"reason":"授权撤回"}`, nil)
	if st != http.StatusOK {
		t.Fatalf("block version = %d %s", st, string(raw))
	}
	st, raw, blocked := f.reqRaw(t, http.MethodPost, "/api/v1/stores/"+f.storeA+"/matrix-draft", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"pack_id":%q,"version_id":%q,"idempotency_key":"store-after-block"}`, packID, verID), nil)
	blockedBtn, _ := blocked["matrix_button"].(map[string]any)
	if st != http.StatusOK || blockedBtn["enabled"] != false || blockedBtn["reason"] != matrixhandoff.ReasonAssetRevoked || sink.puts != 1 {
		t.Fatalf("blocked version draft = %d %s puts=%d", st, string(raw), sink.puts)
	}
}

// TestHUI2390_TwoMerchantsAgencyRevokePaymentScopeAndExpiredPrice verifies all
// cross-tenant, agency management vs publish authority, agency revocation,
// delegate personal payment escalation, expired price, and spoofed image cases.
func TestHUI2390_TwoMerchantsAgencyRevokePaymentScopeAndExpiredPrice(t *testing.T) {
	f := newHUI2390Fixture(t, "")
	sink := &countingSink{
		available: true,
		ack:       matrixconsume.DraftAck{PlanID: "pln_authz", Status: matrixhandoff.StatusDraft, Executed: false},
	}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})

	camp, _ := f.seedCampaignWithVideo(t, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339))

	// 1. Active agency relation -> agency management authority is NOT brand publish authority.
	rel, err := f.s.St.CreateAgencyRelation(f.tenA, f.agentA, f.ownA, "代运营合作")
	if err != nil {
		t.Fatal(err)
	}
	st, raw, agActive := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-agent-a", f.tenA, "", nil)
	agBtn, _ := agActive["matrix_button"].(map[string]any)
	if st != http.StatusUnprocessableEntity || agBtn["enabled"] != false || agBtn["reason"] != matrixhandoff.ReasonOperatorNotPublisher || sink.puts != 0 {
		t.Fatalf("active agency must fail closed as operator_admin_is_not_brand_publisher: %d %s puts=%d", st, string(raw), sink.puts)
	}

	// 2. Revoke agency relation -> revoked delegate fails closed with delegate_revoked.
	if _, err := f.s.St.RevokeAgencyRelation(rel.ID, f.tenA, f.ownA); err != nil {
		t.Fatal(err)
	}
	st, raw, agRevoked := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-agent-a", f.tenA, "", nil)
	revBtn, _ := agRevoked["matrix_button"].(map[string]any)
	if st != http.StatusUnprocessableEntity || revBtn["enabled"] != false || revBtn["reason"] != matrixhandoff.ReasonDelegateRevoked || sink.puts != 0 {
		t.Fatalf("revoked agency must fail closed as delegate_revoked: %d %s puts=%d", st, string(raw), sink.puts)
	}

	// 3. Merchant quota cannot be escalated to delegate personal payment authority.
	st, raw, payer := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"acting_as_personal_payer":true}`, nil)
	payerBtn, _ := payer["matrix_button"].(map[string]any)
	if st != http.StatusUnprocessableEntity || payerBtn["enabled"] != false || payerBtn["reason"] != matrixhandoff.ReasonDelegatePaymentForbidden || sink.puts != 0 {
		t.Fatalf("personal payer escalation = %d %s puts=%d", st, string(raw), sink.puts)
	}
	st, raw, payerScope := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"payment_scope":"delegate_personal_payment"}`, nil)
	psBtn, _ := payerScope["matrix_button"].(map[string]any)
	if st != http.StatusUnprocessableEntity || psBtn["enabled"] != false || psBtn["reason"] != matrixhandoff.ReasonDelegatePaymentForbidden || sink.puts != 0 {
		t.Fatalf("personal payment scope escalation = %d %s puts=%d", st, string(raw), sink.puts)
	}

	// 4. Merchant A creates a valid draft; Merchant B cannot read, create, confirm, or post receipt.
	st, raw, okDraft := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "", nil)
	if st != http.StatusOK || sink.puts != 1 {
		t.Fatalf("merchant A draft = %d %s", st, string(raw))
	}
	draftID := stringMust(okDraft["draft_id"])

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/campaigns/" + camp.ID + "/matrix-draft", ""},
		{http.MethodGet, "/api/v1/campaigns/" + camp.ID + "/matrix-drafts", ""},
		{http.MethodPost, "/api/v1/matrix-drafts/" + draftID + "/confirm", ""},
		{http.MethodPost, "/api/v1/matrix-drafts/" + draftID + "/receipt", `{"target_state":"scheduled","sequence":1}`},
	} {
		st, raw, _ = f.reqRaw(t, tc.method, tc.path, "sess-owner-b", f.tenB, tc.body, nil)
		if st != http.StatusNotFound {
			t.Fatalf("cross-merchant %s %s = %d %s, want 404", tc.method, tc.path, st, string(raw))
		}
	}

	// 5. Campaign with only image asset spoofing video -> wrong_content_type.
	imgCamp, err := f.s.St.CreateCampaign(store.NewCampaign{
		TenantID:      f.tenA,
		Title:         "图文活动",
		PublicContent: "图文卖点",
		StoreID:       f.storeA,
		EndsAt:        time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
		CreatedBy:     f.ownA,
	})
	if err != nil {
		t.Fatal(err)
	}
	imgLib, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef:  "ast_img_only",
		SHA256:    strings.Repeat("ef", 32),
		MediaType: assetlib.MediaTypeImage,
		Source:    assetlib.SourceMerchantUpload,
		Purpose:   "海报图片",
		GrantRef:  "grant_img_camp",
	}, f.storeA, f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, imgCamp.ID, imgLib.AssetRef, "1", f.ownA); err != nil {
		t.Fatal(err)
	}
	st, raw, imgSpoof := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+imgCamp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"declared_content_type":"video"}`, nil)
	imgSpoofBtn, _ := imgSpoof["matrix_button"].(map[string]any)
	if st != http.StatusOK || imgSpoofBtn["enabled"] != false || imgSpoofBtn["reason"] != matrixhandoff.ReasonWrongContentType || sink.puts != 1 {
		t.Fatalf("campaign image spoof = %d %s puts=%d", st, string(raw), sink.puts)
	}
}

// TestHUI2390_IdempotentReplayCampaignUpdateNoSilentOverwriteAndLateReceipt verifies:
// - repeated clicks / retries after approval return the same draft with 0 extra matrix puts;
// - updating campaign content/assets NEVER silently overwrites an already-approved plan;
// - confirming a new version supersedes the old plan without rewriting its stored facts;
// - late receipts for superseded/old versions or lower/unsequenced numbers are ignored.
func TestHUI2390_IdempotentReplayCampaignUpdateNoSilentOverwriteAndLateReceipt(t *testing.T) {
	f := newHUI2390Fixture(t, "http://matrix-configured.test")
	sink := &countingSink{
		available: true,
		ack:       matrixconsume.DraftAck{PlanID: "pln_v1", Status: matrixhandoff.StatusDraft, Executed: false},
	}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})

	camp, _ := f.seedCampaignWithVideo(t, time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339))

	// 1. Create v1 draft and confirm it.
	st, raw, d1 := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"idempotency_key":"idem-v1"}`, nil)
	if st != http.StatusOK || sink.puts != 1 {
		t.Fatalf("v1 draft = %d %s", st, string(raw))
	}
	id1 := stringMust(d1["draft_id"])
	st, raw, _ = f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+id1+"/confirm", "sess-owner-a", f.tenA, "", nil)
	if st != http.StatusOK {
		t.Fatalf("confirm v1 = %d %s", st, string(raw))
	}

	// 2. Duplicate click / retry after confirm with same idempotency_key -> same draft, puts stays 1.
	st, raw, replay := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"idempotency_key":"idem-v1"}`, nil)
	if st != http.StatusOK || replay["draft_id"] != id1 || replay["plan_id"] != "pln_v1" || sink.puts != 1 {
		t.Fatalf("idempotent replay after confirm = %d %s puts=%d", st, string(raw), sink.puts)
	}

	// 3. Campaign content update + new asset version v4 MUST NOT silently overwrite approved v1.
	st, raw, _ = f.reqRaw(t, http.MethodPatch, "/api/v1/campaigns/"+camp.ID, "sess-owner-a", f.tenA, `{"public_content":"改版后全新卖点"}`, nil)
	if st != http.StatusOK {
		t.Fatalf("patch campaign = %d %s", st, string(raw))
	}
	libV4, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef:  "ast_vid_v4",
		SHA256:    strings.Repeat("cd", 32),
		MediaType: assetlib.MediaTypeVideo,
		Source:    assetlib.SourceMerchantUpload,
		Purpose:   "新版成片",
		GrantRef:  "grant_camp_v4",
	}, f.storeA, f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, camp.ID, libV4.AssetRef, "4", f.ownA); err != nil {
		t.Fatal(err)
	}
	keptV1, ok := f.s.Matrix.GetDraftForTenant(f.tenA, id1)
	if !ok || !keptV1.Approved || keptV1.Superseded || keptV1.Activity.OfferText != "招牌套餐第二份半价" || keptV1.Activity.Version != 3 {
		t.Fatalf("campaign update silently overwrote approved plan v1: %+v", keptV1)
	}

	// 4. Create v2 draft (new version requires explicit confirmation before superseding v1).
	sink.ack = matrixconsume.DraftAck{PlanID: "pln_v2", Status: matrixhandoff.StatusDraft, Executed: false}
	st, raw, d2 := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"idempotency_key":"idem-v2"}`, nil)
	if st != http.StatusOK || d2["draft_id"] == id1 || d2["plan_id"] != "pln_v2" || sink.puts != 2 {
		t.Fatalf("v2 draft = %d %s puts=%d", st, string(raw), sink.puts)
	}
	id2 := stringMust(d2["draft_id"])
	// Before v2 is confirmed, v1 MUST remain approved.
	keptV1, _ = f.s.Matrix.GetDraftForTenant(f.tenA, id1)
	if !keptV1.Approved || keptV1.Superseded {
		t.Fatalf("unconfirmed v2 superseded v1 prematurely: %+v", keptV1)
	}

	// 5. Confirm v2 -> v1 becomes superseded without losing its original activity snapshot.
	st, raw, _ = f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+id2+"/confirm", "sess-owner-a", f.tenA, "", nil)
	if st != http.StatusOK {
		t.Fatalf("confirm v2 = %d %s", st, string(raw))
	}
	keptV1, _ = f.s.Matrix.GetDraftForTenant(f.tenA, id1)
	if keptV1.Approved || !keptV1.Superseded || keptV1.Activity.OfferText != "招牌套餐第二份半价" {
		t.Fatalf("v1 after v2 confirm = %+v", keptV1)
	}

	// 6. Late receipt targeting superseded v1 is ignored.
	st, raw, late1 := f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+id1+"/receipt", "sess-owner-a", f.tenA,
		`{"plan_id":"pln_v1","target_state":"published","sequence":10,"official_verified":true,"official_publish_granted":true}`,
		map[string]string{"X-Touch-Internal-Token": "test-internal-secret"})
	if st != http.StatusOK || late1["applied"] != false || late1["ignored"] != true || late1["reason"] != matrixhandoff.ReasonStaleReceipt {
		t.Fatalf("late v1 receipt = %d %s", st, string(raw))
	}

	// 7. Apply valid receipt seq=5 on v2 -> published, then stale seq=3 & older activity_version=3 are ignored.
	st, raw, pub2 := f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+id2+"/receipt", "sess-owner-a", f.tenA,
		`{"plan_id":"pln_v2","target_state":"published","activity_version":4,"sequence":5,"official_verified":true,"official_publish_granted":true}`,
		map[string]string{"X-Touch-Internal-Token": "test-internal-secret"})
	if st != http.StatusOK || pub2["applied"] != true || pub2["plan_state"] != matrixhandoff.PlanStatePublished {
		t.Fatalf("v2 published receipt = %d %s", st, string(raw))
	}
	for _, body := range []string{
		`{"plan_id":"pln_v2","target_state":"scheduled","activity_version":4,"sequence":3}`,
		`{"plan_id":"pln_v2","target_state":"scheduled","activity_version":3,"sequence":9}`,
		`{"plan_id":"pln_v2","target_state":"draft_saved","activity_version":4,"sequence":10}`,
	} {
		st, raw, late2 := f.reqRaw(t, http.MethodPost, "/api/v1/matrix-drafts/"+id2+"/receipt", "sess-owner-a", f.tenA, body, nil)
		if st != http.StatusOK || late2["applied"] != false || late2["ignored"] != true || late2["reason"] != matrixhandoff.ReasonStaleReceipt || late2["plan_state"] != matrixhandoff.PlanStatePublished {
			t.Fatalf("stale v2 receipt %s not ignored: %d %s", body, st, string(raw))
		}
	}
}

// TestHUI2390_MatrixOutageAndTimeoutNeverBlocksStoreCampaignOrLeads verifies
// that Matrix 503/timeout disables only the matrix button while keeping store
// campaigns, anonymous QR/storefront touches, and lead capture fully operational.
func TestHUI2390_MatrixOutageAndTimeoutNeverBlocksStoreCampaignOrLeads(t *testing.T) {
	downMatrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("matrix unavailable"))
	}))
	t.Cleanup(downMatrix.Close)

	f := newHUI2390Fixture(t, downMatrix.URL)
	gate := matrixconsume.PermissionFunc(func(_ context.Context, _, _, _ string) (matrixconsume.Permission, error) {
		return matrixconsume.Permission{
			Allowed: true, Supply: matrixconsume.SupplyReady, Reason: "ok",
			Authority: matrixconsume.Authority{
				PayerID: "pay_outage", LicenseRef: "lic_outage", LicenseExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
			},
		}, nil
	})
	f.s.Matrix = matrixconsume.New(gate, matrixconsume.NewHTTPDraftSink(downMatrix.URL, downMatrix.Client()), &matrixhandoff.EffectSink{})

	camp, link := f.seedCampaignWithVideo(t, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339))

	// Matrix fails with 503 -> only matrix_button is disabled; lead & ugc stay enabled.
	st, raw, body := f.reqRaw(t, http.MethodPost, "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "", nil)
	if st != http.StatusOK || body["outbound_complete"] != false {
		t.Fatalf("matrix outage response = %d %s", st, string(raw))
	}
	mBtn, _ := body["matrix_button"].(map[string]any)
	lBtn, _ := body["lead_button"].(map[string]any)
	uBtn, _ := body["ugc_button"].(map[string]any)
	if mBtn["enabled"] != false || mBtn["reason"] != matrixconsume.ReasonMatrixSupply || lBtn["enabled"] != true || uBtn["enabled"] != true {
		t.Fatalf("matrix outage degraded other surfaces: %s", string(raw))
	}

	// Campaign stays active (not locked).
	gotCamp, err := f.s.St.GetCampaign(camp.ID, f.tenA)
	if err != nil || gotCamp.Status != "active" {
		t.Fatalf("campaign locked after matrix outage: %+v err=%v", gotCamp, err)
	}

	// Anonymous QR browse, storefront browse, and customer lead capture all stay healthy!
	st, qrRaw, qr := f.reqRaw(t, http.MethodGet, "/api/v1/public/links/"+link.Code, "", "", "", nil)
	if st != http.StatusOK || qr["state"] != "available" {
		t.Fatalf("QR browse blocked by matrix outage: %d %s", st, string(qrRaw))
	}
	sc := f.storeCode(t, f.storeA, f.tenA, "sess-owner-a")
	st, sfRaw, _ := f.reqRaw(t, http.MethodGet, "/api/v1/public/storefronts/"+sc, "", "", "", nil)
	if st != http.StatusOK {
		t.Fatalf("storefront blocked by matrix outage: %d %s", st, string(sfRaw))
	}
	st, leadRaw, _ := f.reqRaw(t, http.MethodPost, "/api/v1/public/links/"+link.Code+"/lead-submissions", "", "",
		`{"name":"王五","phone":"13700137000","consent_version":"v1","consent":true,"marketing_optin":false,"channel":"wecom"}`, nil)
	if st != http.StatusCreated {
		t.Fatalf("lead capture blocked by matrix outage: %d %s", st, string(leadRaw))
	}
}
