package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePublicationContentBoundary(t *testing.T) {
	f := newPublicContentFixture(t, true, "FEATURE_STOREFRONT", "on", "FEATURE_PARTICIPATION", "on", "PUBLIC_BASE_URL", "https://h5.example.com")
	req := func(method, path, body, cookie string, merchant bool) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
		r.Header.Set("X-Internal-Token", "test-internal-secret")
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		if merchant {
			r.Header.Set("X-Session-Token", "sess-owner-a")
			r.Header.Set("X-Tenant-ID", f.tenA)
		}
		res, err := f.ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}
	base := "/api/v1/stores/" + f.storeA
	pack, v1, video, cover := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "第一版", true)
	actionBody := func(rev int, id, target string) string {
		return fmt.Sprintf(`{"publication_mode":"draft","expected_revision":%d,"items":[{"action_id":%q,"catalog_code":"A01","label":"发布到抖音","target_ref":%q,"content_binding_ref":%q,"order":1,"enabled":true}]}`, rev, id, target, "pack:"+pack)
	}
	status, out := req("PUT", base+"/actions", actionBody(0, "", "https://www.douyin.com/poi/1"), "", true)
	if status != 200 {
		t.Fatalf("actions %d %v", status, out)
	}
	action := out["items"].([]any)[0].(map[string]any)["action_id"].(string)
	contentPath := "/api/v1/public/actions/" + action + "/content"
	if status, out = req("GET", contentPath, "", "", false); status != 404 {
		t.Fatalf("unpublished content %d %v", status, out)
	}
	if _, _, status, out = f.createSession(t, action); status != 404 {
		t.Fatalf("unpublished session %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v1, "", "", video); status != 404 {
		t.Fatalf("unpublished anonymous media %d %v", status, out)
	}
	publish := func(expected, published int) {
		t.Helper()
		status, draft := req("PUT", base+"/storefront-draft", sfDraftBody(expected, action), "", true)
		if status != 200 {
			t.Fatalf("save %d %v", status, draft)
		}
		_, pre := req("POST", base+"/storefront/preflight", `{}`, "", true)
		status, out = req("POST", base+"/storefront/publish", fmt.Sprintf(`{"candidate_revision":%v,"expected_published_revision":%d,"confirm_changes":true,"summary_fingerprint":%q}`, draft["revision"], published, pre["summary_fingerprint"]), "", true)
		if status != 200 {
			t.Fatalf("publish %d %v; pre=%v", status, out, pre)
		}
	}
	merchantMedia := base + "/content-packs/" + pack + "/versions/" + v1 + "/media-access"
	mediaBody := fmt.Sprintf(`{"media_ids":[%q],"purpose":"preview"}`, video)
	if status, out = req("POST", merchantMedia, mediaBody, "", true); status != 200 {
		t.Fatalf("scoped merchant preview %d %v", status, out)
	}
	if status, out = req("POST", merchantMedia, mediaBody, "", false); status != 401 {
		t.Fatalf("anonymous merchant preview %d %v", status, out)
	}
	publish(0, 0)
	sid, cookie, status, out := f.createSession(t, action)
	if status != 201 || out["selected_content_pack_version"] != v1 {
		t.Fatalf("v1 session %d %v", status, out)
	}
	status, issued := f.mediaAccess(t, v1, sid, cookie, video)
	if status != 200 {
		t.Fatal(status, issued)
	}
	oldMediaURL := issued["items"].([]any)[0].(map[string]any)["url"].(string)
	// Start editing before the replacement exists. Further saves preserve the
	// draft row's original created_at, while publication happens after adoption.
	status, out = req("PUT", base+"/storefront-draft", sfDraftBody(1, action), "", true)
	if status != 200 {
		t.Fatal(status, out)
	}
	versionBody := fmt.Sprintf(`{"content_kind":"video","video_media_id":%q,"cover_media_id":%q,"title":"第二版","body":"第二版顾客文案","topics":["门店"],"store_name":"公共一店","price_note":"五折","valid_note":"长期有效","platform_versions":[{"platform":"douyin","note":"竖版","source_version":"src-2"}],"grant_refs":["grant-002"],"marking":{"ad_required":true},"source":"merchant_upload","status":"candidate"}`, video, cover)
	status, out = req("POST", base+"/content-packs/"+pack+"/versions", versionBody, "", true)
	if status != 201 {
		t.Fatalf("v2 create %d %v", status, out)
	}
	v2 := out["id"].(string)
	if status, out = req("POST", base+"/content-packs/"+pack+"/versions/"+v2+"/media-access", mediaBody, "", true); status != 200 {
		t.Fatalf("unadopted merchant preview %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v2, "", "", video); status != 404 {
		t.Fatalf("unadopted anonymous candidate %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v2, sid, cookie, video); status != 409 {
		t.Fatalf("unadopted same-pack session media %d %v", status, out)
	}
	status, out = req("POST", base+"/content-packs/"+pack+"/versions/"+v2+"/adopt", `{"platform":"douyin"}`, "", true)
	if status != 200 {
		t.Fatal(status, out)
	}
	// A later adoption for another platform must not become this A01 default
	// when an unrelated storefront change is explicitly published.
	wechatBody := fmt.Sprintf(`{"content_kind":"imagetext","image_text_media_ids":[%q],"title":"朋友圈图文","body":"仅用于朋友圈的图文","topics":[],"store_name":"公共一店","platform_versions":[{"platform":"wechat","note":"图文","source_version":"src-3"}],"grant_refs":["grant-003"],"marking":{"ad_required":true},"source":"merchant_upload","status":"candidate"}`, cover)
	status, out = req("POST", base+"/content-packs/"+pack+"/versions", wechatBody, "", true)
	if status != 201 {
		t.Fatalf("wechat version create %d %v", status, out)
	}
	wechatVersion := out["id"].(string)
	status, out = req("POST", base+"/content-packs/"+pack+"/versions/"+wechatVersion+"/adopt", `{"platform":"wechat"}`, "", true)
	if status != 200 {
		t.Fatalf("wechat adoption %d %v", status, out)
	}
	status, out = req("PUT", base+"/actions", actionBody(1, action, "https://www.douyin.com/poi/2"), "", true)
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = req("GET", contentPath, "", "", false)
	if status != 200 || out["selected_content_pack_version"] != v1 {
		t.Fatalf("draft leaked into public %d %v", status, out)
	}
	raw, _ := json.Marshal(out["candidates"])
	if strings.Contains(string(raw), v2) {
		t.Fatalf("unpublished candidate leaked %s", raw)
	}
	_, _, status, out = f.createSession(t, action)
	if status != 201 || out["selected_content_pack_version"] != v1 {
		t.Fatalf("unpublished new session drift %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v2, "", "", video); status != 404 {
		t.Fatalf("unpublished anonymous media %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v2, sid, cookie, video); status != 409 {
		t.Fatalf("same-pack unpublished media %d %v", status, out)
	}
	if status, out = req("POST", "/api/v1/public/action-sessions/"+sid+"/content", fmt.Sprintf(`{"content_pack_version":%q}`, v2), cookie, false); status != 409 {
		t.Fatalf("unpublished selection %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v1, sid, cookie, video); status != 200 {
		t.Fatalf("retired legitimate v1 stopped %d %v", status, out)
	}
	if status, _ := req("GET", oldMediaURL, "", "", false); status != 200 {
		t.Fatalf("previously signed platform media invalidated by adoption %d", status)
	}
	publish(2, 1)
	status, out = req("GET", contentPath, "", "", false)
	if status != 200 || out["selected_content_pack_version"] != v2 {
		t.Fatalf("A01 switched to another platform at publication %d %v", status, out)
	}
	raw, _ = json.Marshal(out["candidates"])
	if strings.Contains(string(raw), wechatVersion) {
		t.Fatalf("A01 offered a wechat candidate %s", raw)
	}
	_, _, status, out = f.createSession(t, action)
	if status != 201 || out["selected_content_pack_version"] != v2 {
		t.Fatalf("new v2 session %d %v", status, out)
	}
	status, out = req("GET", contentPath+"?action_session_id="+sid, "", cookie, false)
	if status != 200 || out["selected_content_pack_version"] != v1 {
		t.Fatalf("old session content changed %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, v1, sid, cookie, video); status != 200 {
		t.Fatalf("old session media stopped %d %v", status, out)
	}
	// Re-adopting an earlier eligible version is allowed, but replacing it
	// after a block must not turn that block into ordinary retirement.
	status, out = req("POST", base+"/content-packs/"+pack+"/versions/"+v1+"/adopt", `{"platform":"douyin"}`, "", true)
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = req("POST", base+"/content-packs/"+pack+"/versions/"+v1+"/block", `{"reason":"停止分发旧文案"}`, "", true)
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = req("POST", base+"/content-packs/"+pack+"/versions/"+v2+"/adopt", `{"platform":"douyin"}`, "", true)
	if status != 200 {
		t.Fatal(status, out)
	}
	if status, _ := req("GET", oldMediaURL, "", "", false); status != 409 {
		t.Fatalf("blocked signed media remained accessible %d", status)
	}
	if status, out = f.mediaAccess(t, v1, sid, cookie, video); status != 409 {
		t.Fatalf("blocked frozen media issued %d %v", status, out)
	}
}

// Migration fixture: old sessions have no persisted candidate pool. Only their
// explicitly recorded, provably adopted choice survives; nil never means any pack version.
func TestLegacySessionWithoutPoolAuthorizesOnlyRecordedAdoptedVersion(t *testing.T) {
	f := newMediaSessionFixture(t)
	pack, v1, video, cover := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "旧会话内容", true)
	action := f.seedActions(t, "act_wechat")[0]
	f.bindAction(t, action, pack)
	sid, cookie, status, out := f.createSession(t, action)
	if status != 201 {
		t.Fatal(status, out)
	}
	if _, err := f.s.St.DB.Exec(`UPDATE action_sessions SET content_candidates=NULL WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	old, err := f.s.St.GetStoreContentPackVersion(v1, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := f.s.St.CreateStoreContentPackVersion(f.tenA, pack, store.StoreVersionSpec{ContentKind: old.ContentKind, VideoMediaID: video, CoverMediaID: cover, Title: "新候选", Body: old.Body, Topics: old.Topics, StoreName: old.StoreName, PriceNote: old.PriceNote, ValidNote: old.ValidNote, PlatformVersions: old.PlatformVersions, GrantRefs: old.GrantRefs, Marking: old.Marking, Source: contentpack.SourceMerchantUpload}, contentpack.StatusCandidate, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AdoptStoreContentPackVersion(f.tenA, pack, candidate.ID, "douyin", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if status, out = f.mediaAccess(t, v1, sid, cookie, video); status != 200 {
		t.Fatalf("legitimate historical choice %d %v", status, out)
	}
	if status, out = f.mediaAccess(t, candidate.ID, sid, cookie, video); status != 409 {
		t.Fatalf("nil pool allowed later same-pack version %d %v", status, out)
	}
}
