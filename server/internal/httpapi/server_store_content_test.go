package httpapi

// server_store_content_test.go: HUI-3553 门店根内容包 HTTP 管理面验收。
//
// 覆盖:
//   - 真实上传:multipart 真实字节 → 服务器实测元数据(MIME/哈希/大小/时长/
//     宽高);伪媒体字节拒绝;商家不填任何技术字段;
//   - 门店根建包(campaign 可空/同名 409/跨租户 404/查询适配含旧活动包);
//   - 建版本(media_id 语义;缺失 422 media_missing;快照冻结可见);
//   - 明确采用(记录 adopted_by;换版切换指针;旧版 retired);
//   - block 拒新分发;
//   - authz:staff 403。

import (
	"bytes"
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

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type storeContentFixture struct {
	s       *Server
	ts      *httptest.Server
	tenA    string
	tenB    string
	storeA  string
	storeB  string
	storeA2 string
	campA   string
	testDir string
}

func newStoreContentFixture(t *testing.T) *storeContentFixture {
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
		}
		return ""
	})
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("门店内容商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("门店内容商家B")
	if err != nil {
		t.Fatal(err)
	}
	stA, err := st.CreateStore(tenA.ID, "旗舰一店", "地址一", "test")
	if err != nil {
		t.Fatal(err)
	}
	stA2, err := st.CreateStore(tenA.ID, "同租户二店", "地址三", "test")
	if err != nil {
		t.Fatal(err)
	}
	stB, err := st.CreateStore(tenB.ID, "别家门店", "地址二", "test")
	if err != nil {
		t.Fatal(err)
	}
	cA, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, StoreID: stA.ID, Title: "门店活动A", PublicContent: "内容", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// HUI-4593 角色:门店经理(仅本店)与带 scope 店员。
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_manager_a", "store_manager", "经理A", "test", true, stA.ID); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a":  {"usr_owner_a", "owner@example.com"},
		"sess-staff-a":  {"usr_staff_a", "staff@example.com"},
		"sess-owner-b":  {"usr_owner_b", "ownerb@example.com"},
		"sess-manager-a": {"usr_manager_a", "manager@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}

	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &storeContentFixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, storeA: stA.ID, storeB: stB.ID, storeA2: stA2.ID, campA: cA.ID, testDir: mediaDir}
}

func (f *storeContentFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
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

func (f *storeContentFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

// uploadFile posts a real multipart upload with actual bytes; displayName is
// the client-declared filename (may lie — the server sniffs real bytes).
func (f *storeContentFixture) uploadFile(t *testing.T, session, tenant, storeID, fixtureName, displayName string) (int, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "upload", "testdata", fixtureName))
	if err != nil {
		t.Fatalf("fixture %s: %v", fixtureName, err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", displayName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/stores/%s/content-media", f.ts.URL, storeID), &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", session)
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer res.Body.Close()
	rawOut, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(rawOut, &out)
	return res.StatusCode, out
}

func storeVersionBody(videoID, coverID string) string {
	return fmt.Sprintf(`{
		"content_kind":"video",
		"video_media_id":%q,
		"cover_media_id":%q,
		"title":"门店开业五折",
		"body":"本店开业全场五折。",
		"topics":["开业","五折"],
		"store_name":"旗舰一店",
		"price_note":"全场五折",
		"valid_note":"2026-11-10 至 2026-11-30",
		"platform_versions":[{"platform":"douyin","note":"竖版","source_version":"src-1"}],
		"grant_refs":["grant-2026-001"],
		"marking":{"ad_required":true},
		"source":"merchant_upload",
		"status":"candidate"
	}`, videoID, coverID)
}

func TestStoreMediaUploadVerifiesRealBytes(t *testing.T) {
	f := newStoreContentFixture(t)

	// 真实 mp4:服务器实测元数据(商家全程没填任何技术字段)。
	status, out := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "tiny.mp4", "tiny.mp4")
	if status != http.StatusCreated {
		t.Fatalf("upload status = %d: %v", status, out)
	}
	if out["mime_type"] != "video/mp4" || out["kind"] != "video" {
		t.Fatalf("server-detected type = %v/%v, want video/mp4", out["mime_type"], out["kind"])
	}
	if v, ok := out["sha256"].(string); !ok || len(v) != 64 {
		t.Fatalf("sha256 = %v", out["sha256"])
	}
	if sz, ok := out["byte_size"].(float64); !ok || int64(sz) <= 0 {
		t.Fatalf("byte_size = %v", out["byte_size"])
	}
	if d, ok := out["duration_ms"].(float64); !ok || d < 900 {
		t.Fatalf("duration_ms = %v, want ~1000 from real probe", out["duration_ms"])
	}
	if w, _ := out["width"].(float64); int(w) != 64 {
		t.Fatalf("width = %v, want 64", out["width"])
	}

	// 伪 MIME 不采信:JPEG 字节伪装成 .mp4 文件名,服务器以真实字节判定。
	status, out = f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "pixel.jpg", "伪装视频.mp4")
	if status != http.StatusCreated {
		t.Fatalf("disguised jpeg upload = %d: %v", status, out)
	}
	if out["mime_type"] != "image/jpeg" || out["kind"] != "image" {
		t.Fatalf("server must sniff real bytes: %v/%v, want image/jpeg", out["mime_type"], out["kind"])
	}
}

func TestStoreMediaUploadRefusesGarbageAndStaff(t *testing.T) {
	f := newStoreContentFixture(t)
	// 文本字节不是媒体:422。
	dir := t.TempDir()
	bad := filepath.Join(dir, "not-media.bin")
	if err := os.WriteFile(bad, []byte("just some text, definitely not media"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(bad)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "not-media.bin")
	fw.Write(raw)
	mw.Close()
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/stores/"+f.storeA+"/content-media", &body)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", "sess-owner-a")
	req.Header.Set("X-Tenant-ID", f.tenA)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("garbage upload = %d, want 422", res.StatusCode)
	}

	// staff 无管理权:403。
	status, _ := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-staff-a", f.tenA, `{"title":"x"}`)
	if status != http.StatusForbidden {
		t.Fatalf("staff create pack = %d, want 403", status)
	}
}

func TestStoreContentPackLifecycle(t *testing.T) {
	f := newStoreContentFixture(t)

	// 门店根建包:campaign 可空。
	status, out := f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", `{"title":"门店主题包"}`)
	if status != http.StatusCreated {
		t.Fatalf("create pack = %d: %v", status, out)
	}
	packID, _ := out["id"].(string)
	campaignID, _ := out["campaign_id"].(string)
	if packID == "" || campaignID != "" || out["legacy"] != false {
		t.Fatalf("pack = %v", out)
	}
	// 同店同名 = 409。
	status, _ = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", `{"title":"门店主题包"}`)
	if status != http.StatusConflict {
		t.Fatalf("duplicate pack = %d, want 409", status)
	}
	// 挂活动:可空字段显式传入也接受。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", fmt.Sprintf(`{"title":"活动主题包","campaign_id":%q}`, f.campA))
	if status != http.StatusCreated {
		t.Fatalf("campaign pack = %d: %v", status, out)
	}

	// 真实上传 → 建版本。
	_, video := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "tiny.mp4", "tiny.mp4")
	videoID, _ := video["id"].(string)
	_, cover := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "pixel.jpg", "pixel.jpg")
	coverID, _ := cover["id"].(string)

	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions", storeVersionBody(videoID, coverID))
	if status != http.StatusCreated {
		t.Fatalf("version = %d: %v", status, out)
	}
	v1, _ := out["id"].(string)
	videoSnap, _ := out["video"].(map[string]any)
	if videoSnap["media_id"] != videoID || videoSnap["mime_type"] != "video/mp4" {
		t.Fatalf("video snapshot = %v", videoSnap)
	}
	if d, _ := videoSnap["duration_ms"].(float64); d < 900 {
		t.Fatalf("frozen duration = %v", videoSnap["duration_ms"])
	}

	// 缺失媒体 → 422 media_missing。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions", storeVersionBody("cmd_missing", coverID))
	if status != http.StatusUnprocessableEntity || out["error"] != "media_missing" {
		t.Fatalf("missing media = %d %v, want 422 media_missing", status, out)
	}

	// 明确采用:记录 adopted_by。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions/"+v1+"/adopt", `{"platform":"douyin"}`)
	if status != http.StatusOK {
		t.Fatalf("adopt = %d: %v", status, out)
	}
	if out["version_id"] != v1 || out["adopted_by"] == "" {
		t.Fatalf("adoption = %v", out)
	}

	// 换版:第二个候选 → 采用 → 指针切换。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions", storeVersionBody(videoID, coverID))
	if status != http.StatusCreated {
		t.Fatalf("v2 = %d: %v", status, out)
	}
	v2, _ := out["id"].(string)
	if v2 == v1 {
		t.Fatal("version id reused")
	}
	if _, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions/"+v2+"/adopt", `{"platform":"douyin"}`); out["version_id"] != v2 {
		t.Fatalf("switch = %v", out)
	}

	// 列表:包+版本+指针一体;旧 campaign 包经查询适配出现。
	status, out = f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-a", f.tenA, "")
	if status != http.StatusOK {
		t.Fatalf("list = %d: %v", status, out)
	}
	items, _ := out["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("items = %d, want >=2 (store packs)", len(items))
	}
	found := false
	for _, it := range items {
		p, _ := it.(map[string]any)
		if p["id"] == packID {
			found = true
			versions, _ := p["versions"].([]any)
			if len(versions) != 2 {
				t.Fatalf("versions = %d, want 2", len(versions))
			}
			adoptions, _ := p["adoptions"].([]any)
			if len(adoptions) != 1 {
				t.Fatalf("adoptions = %d, want 1", len(adoptions))
			}
		}
	}
	if !found {
		t.Fatal("store pack missing from list")
	}

	// block:v1 → 新分发拒绝由公共面把关,管理面返回 blocked 状态。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions/"+v1+"/block", `{"reason":"授权到期撤回"}`)
	if status != http.StatusOK || out["status"] != "blocked" {
		t.Fatalf("block = %d %v", status, out)
	}
	// 原因必填。
	status, _ = f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions/"+v2+"/block", `{"reason":"  "}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("empty block reason = %d, want 422", status)
	}
}

func TestStoreContentPackTenantIsolation(t *testing.T) {
	f := newStoreContentFixture(t)
	status, out := f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", `{"title":"隔离包"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d", status)
	}
	packID, _ := out["id"].(string)
	// B 租户列 A 的门店包 = 404(跨租户不可见);B 列自己的门店正常。
	status, _ = f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-b", f.tenB, "")
	if status != http.StatusNotFound {
		t.Fatalf("B list A store = %d, want 404", status)
	}
	status, _ = f.do(t, "GET", "/api/v1/stores/"+f.storeB+"/content-packs", "sess-owner-b", f.tenB, "")
	if status != http.StatusOK {
		t.Fatalf("B list own store = %d", status)
	}
	status, _ = f.do(t, "POST", "/api/v1/stores/"+f.storeB+"/content-packs", "sess-owner-b", f.tenB, `{"title":"B包"}`)
	if status != http.StatusCreated {
		t.Fatalf("B create = %d", status)
	}
	// B 用 A 的包建版本 = 404(不泄露存在性)。
	status, out = f.do(t, "POST", "/api/v1/stores/"+f.storeB+"/content-packs/"+packID+"/versions", "sess-owner-b", f.tenB, storeVersionBody("cmd_x", "cmd_y"))
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant version = %d %v, want 404", status, out)
	}
}

// ---- Walk A F 修复轮:门店经理可完整经营本店内容包;越店一律拒绝 ----------

func TestStoreManagerOperatesOwnStoreContent(t *testing.T) {
	f := newStoreContentFixture(t)
	mgr := func(method, path, body string) (int, map[string]any) {
		return f.do(t, method, path, "sess-manager-a", f.tenA, body)
	}

	// 同表单连续上传视频+封面(走查场景:第一件成功,第二件不再 403)。
	status, out := f.uploadFile(t, "sess-manager-a", f.tenA, f.storeA, "tiny.mp4", "tiny.mp4")
	if status != http.StatusCreated {
		t.Fatalf("manager upload mp4 = %d: %v", status, out)
	}
	videoID, _ := out["id"].(string)
	status, out = f.uploadFile(t, "sess-manager-a", f.tenA, f.storeA, "pixel.jpg", "pixel.jpg")
	if status != http.StatusCreated {
		t.Fatalf("manager upload cover = %d: %v (同表单第二件必须同样成功)", status, out)
	}
	coverID, _ := out["id"].(string)

	// 建包(活动可空)→ 建候选 → 明确采用 → 列表可读:4584 R2 依赖链。
	status, out = mgr("POST", "/api/v1/stores/"+f.storeA+"/content-packs", `{"title":"经理的主题包"}`)
	if status != http.StatusCreated {
		t.Fatalf("manager create pack = %d: %v", status, out)
	}
	packID, _ := out["id"].(string)
	status, out = mgr("POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions", storeVersionBody(videoID, coverID))
	if status != http.StatusCreated {
		t.Fatalf("manager create version = %d: %v", status, out)
	}
	versionID, _ := out["id"].(string)
	status, out = mgr("POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions/"+versionID+"/adopt", `{"platform":"douyin"}`)
	if status != http.StatusOK {
		t.Fatalf("manager adopt = %d: %v", status, out)
	}
	status, out = mgr("GET", "/api/v1/stores/"+f.storeA+"/content-packs", "")
	if status != http.StatusOK {
		t.Fatalf("manager list = %d: %v", status, out)
	}
	mediaStatus, _ := mgr("GET", "/api/v1/stores/"+f.storeA+"/content-media", "")
	if mediaStatus != http.StatusOK {
		t.Fatalf("manager media list = %d", mediaStatus)
	}

	// 越店拒绝:经理只对本店有管理权。
	status, _ = f.uploadFile(t, "sess-manager-a", f.tenA, f.storeA2, "tiny.mp4", "tiny.mp4")
	if status != http.StatusForbidden {
		t.Fatalf("manager cross-store upload = %d, want 403", status)
	}
	status, _ = mgr("POST", "/api/v1/stores/"+f.storeA2+"/content-packs", `{"title":"越店包"}`)
	if status != http.StatusForbidden {
		t.Fatalf("manager cross-store pack = %d, want 403", status)
	}
	// 本店 URL + 本店包:继续可建版本。
	status, _ = mgr("POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions", storeVersionBody(videoID, coverID))
	if status != http.StatusCreated {
		t.Fatalf("own-store pack version = %d", status)
	}
	// 别店包造一个:owner 在 storeA2 下建包。
	status, out = f.owner(t, "POST", "/api/v1/stores/"+f.storeA2+"/content-packs", `{"title":"二店的包"}`)
	if status != http.StatusCreated {
		t.Fatalf("owner create storeA2 pack = %d: %v", status, out)
	}
	foreignPack, _ := out["id"].(string)
	status, _ = mgr("POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+foreignPack+"/versions", storeVersionBody(videoID, coverID))
	if status != http.StatusNotFound {
		t.Fatalf("manager foreign-pack via own-store URL = %d, want 404", status)
	}
	status, _ = mgr("POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+foreignPack+"/versions/"+versionID+"/adopt", `{"platform":"douyin"}`)
	if status != http.StatusNotFound {
		t.Fatalf("manager foreign-pack adopt = %d, want 404", status)
	}

	// 无 scope 店员:管理面维持 403(fail-closed 不变),读面保持租户级。
	status, _ = f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-staff-a", f.tenA, `{"title":"店员的包"}`)
	if status != http.StatusForbidden {
		t.Fatalf("staff manage = %d, want 403", status)
	}
	status, _ = f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-staff-a", f.tenA, "")
	if status != http.StatusOK {
		t.Fatalf("legacy staff read = %d, want 200 (T0 语义保留)", status)
	}
}
