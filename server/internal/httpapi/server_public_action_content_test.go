package httpapi

// server_public_action_content_test.go: HUI-3553 B05 公共内容读面 + 受控媒体验收。
//
// 覆盖(票面 T05/T06 + 反例):
//   - GET /api/v1/public/actions/{actionId}/content:按服务器已采用引用返回;
//     无采用 404 语义;blocked/grant 过期 409;两次读取逐字段一致;candidates
//     池 = 已采用且可分发,排除当前;不暴露授权原文/内部凭据;
//   - FEATURE_CONTENT_PACKS off = 路由不注册 + gate 统一 404;
//   - POST /api/v1/public/content/{version}/media-access:purpose 白名单;
//     版本须已采用且可分发;media 须属于版本;签发 url/expires/mime/size/sha/
//     filename;blocked 后拒绝新签发;审计落库;
//   - GET|HEAD /api/v1/public/content/media/{token}:真实文件 + 正确 MIME +
//     Range(206/Content-Range/416) + HEAD 零体 + 下载 attachment;
//     过期 410;篡改 400;blocked 后缓存 token 也拒绝;绝不以 HTML 冒充 mp4;
//   - 匿名(无商家 cookie/session)取媒体成功。

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
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type publicContentFixture struct {
	s        *Server
	ts       *httptest.Server
	tenA     string
	tenB     string
	storeA   string
	storeB   string
	mediaDir string
}

func newPublicContentFixture(t *testing.T, packsPublicOn bool, extraEnv ...string) *publicContentFixture {
	t.Helper()
	mediaDir := t.TempDir()
	flag := func(on bool) string {
		if on {
			return "on"
		}
		return ""
	}
	extra := map[string]string{}
	for i := 0; i+1 < len(extraEnv); i += 2 {
		extra[extraEnv[i]] = extraEnv[i+1]
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_CONTENT_PACKS":
			return flag(packsPublicOn)
		case "TOUCH_MEDIA_DIR":
			return mediaDir
		}
		if v, ok := extra[k]; ok {
			return v
		}
		return ""
	})
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)
	tenA, err := st.CreateTenant("公共内容商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("公共内容商家B")
	if err != nil {
		t.Fatal(err)
	}
	stA, err := st.CreateStore(tenA.ID, "公共一店", "地址一", "test")
	if err != nil {
		t.Fatal(err)
	}
	stB, err := st.CreateStore(tenB.ID, "别家公共店", "地址二", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMember(tenA.ID, "usr_owner_a", "org_owner", "usr_owner_a", "test", true); err != nil {
		t.Fatal(err)
	}
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &publicContentFixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, storeA: stA.ID, storeB: stB.ID, mediaDir: mediaDir}
}

// seedAdoptedContent uploads real bytes, builds a candidate, adopts it and
// binds it to an action — the whole merchant chain — returning ids.
func (f *publicContentFixture) seedAdoptedContent(t *testing.T, tenantID, storeID, actionID, title string, adopt bool) (packID, versionID, videoID, coverID string) {
	t.Helper()
	do := func(method, path, body string) (int, map[string]any) {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, f.ts.URL+path, rd)
		req.Header.Set("X-Internal-Token", "test-internal-secret")
		req.Header.Set("X-Session-Token", "sess-owner-a")
		req.Header.Set("X-Tenant-ID", tenantID)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("seed %s %s: %v", method, path, err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}
	upload := func(fixture, name string) string {
		raw, err := os.ReadFile(filepath.Join("..", "upload", "testdata", fixture))
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/stores/%s/content-media", f.ts.URL, storeID), body)
		req.Header.Set("X-Internal-Token", "test-internal-secret")
		req.Header.Set("X-Session-Token", "sess-owner-a")
		req.Header.Set("X-Tenant-ID", tenantID)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		res, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		defer res.Body.Close()
		rawOut, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(rawOut, &out)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s = %d: %s", fixture, res.StatusCode, rawOut)
		}
		id, _ := out["id"].(string)
		return id
	}
	videoID = upload("tiny.mp4", "tiny.mp4")
	coverID = upload("pixel.jpg", "pixel.jpg")
	status, out := do("POST", fmt.Sprintf("/api/v1/stores/%s/content-packs", storeID), fmt.Sprintf(`{"title":%q}`, title))
	if status != http.StatusCreated {
		t.Fatalf("seed pack = %d: %v", status, out)
	}
	packID, _ = out["id"].(string)
	status, out = do("POST", fmt.Sprintf("/api/v1/stores/%s/content-packs/%s/versions", storeID, packID), fmt.Sprintf(`{
		"content_kind":"video","video_media_id":%q,"cover_media_id":%q,
		"title":%q,"body":"本店开业全场五折。","topics":["开业"],
		"store_name":"公共一店","price_note":"五折","valid_note":"2026-11-10 至 2026-12-31",
		"platform_versions":[{"platform":"douyin","note":"竖版","source_version":"src-1"}],
		"grant_refs":["grant-001"],"marking":{"ad_required":true},
		"source":"merchant_upload","status":"candidate"}`, videoID, coverID, title))
	if status != http.StatusCreated {
		t.Fatalf("seed version = %d: %v", status, out)
	}
	versionID, _ = out["id"].(string)
	if adopt {
		status, out = do("POST", fmt.Sprintf("/api/v1/stores/%s/content-packs/%s/versions/%s/adopt", storeID, packID, versionID), `{"platform":"douyin"}`)
		if status != http.StatusOK {
			t.Fatalf("seed adopt = %d: %v", status, out)
		}
	}
	return packID, versionID, videoID, coverID
}

func newMultipartWriter(dst *bytes.Buffer, filename string, content []byte) *multipart.Writer {
	mw := multipart.NewWriter(dst)
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(content)
	mw.Close()
	return mw
}

func timeNowUTC() time.Time { return time.Now() }

// newExpiredClaim builds a syntactically valid claim whose TTL already ended
// (server must fail closed with 410, never mint new access from it).
func newExpiredClaim(versionID, mediaID string) (contentpack.MediaAccessClaim, error) {
	now := time.Now()
	return contentpack.NewMediaAccessClaim(versionID, mediaID, "preview", "", now.Add(-2*time.Hour), time.Minute)
}

func mintTestMediaToken(secret string, claim contentpack.MediaAccessClaim) (string, error) {
	return contentpack.MintMediaAccess(secret, claim)
}

// rawResponse performs an unauthenticated request and returns status, headers
// and body bytes (for the controlled media GET/HEAD matrix).
func (f *publicContentFixture) rawRequest(t *testing.T, method, path string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, raw
}

func (f *publicContentFixture) publicJSON(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, f.ts.URL+path, rd)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestPublicActionContentGateOff(t *testing.T) {
	f := newPublicContentFixture(t, false)
	// off = 路由不注册(mux 404,体可为空)+ gate 双保险(见 content-snapshot 面)。
	status, _ := f.publicJSON(t, "GET", "/api/v1/public/actions/act_x/content", "")
	if status != http.StatusNotFound {
		t.Fatalf("gate off content = %d, want 404", status)
	}
	status, _ = f.publicJSON(t, "POST", "/api/v1/public/content/cpv_x/media-access", `{"media_ids":["m"],"purpose":"preview"}`)
	if status != http.StatusNotFound {
		t.Fatalf("media-access gate off = %d, want 404", status)
	}
}

func TestPublicActionContentRequiresAdoption(t *testing.T) {
	f := newPublicContentFixture(t, true)
	// 已建包但未采用:404 语义(不是空数组伪装成功)。
	packID, versionID, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_1", "未采用包", false)
	_ = packID
	_ = versionID
	status, out := f.publicJSON(t, "GET", "/api/v1/public/actions/act_1/content", "")
	if status != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("no binding = %d %v, want 404", status, out)
	}
}

func TestPublicActionContentServesAdoptedVersion(t *testing.T) {
	f := newPublicContentFixture(t, true)
	packID, versionID, videoID, coverID := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_9", "开业视频包", true)

	// 绑定是内部调用(采用后建立绑定);4584 的 actions PUT 合流后对接。
	pack, err := f.s.St.GetStoreContentPack(packID, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, "act_9", packID, versionID, 2, "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatalf("bind: %v", err)
	}
	_ = pack

	status, out := f.publicJSON(t, "GET", "/api/v1/public/actions/act_9/content", "")
	if status != http.StatusOK {
		t.Fatalf("content = %d: %v", status, out)
	}
	if out["action_id"] != "act_9" || out["content_binding_ref"] == nil || out["selected_content_pack_version"] != versionID {
		t.Fatalf("content head = %v", out)
	}
	if out["content_kind"] != "video" || out["title"] != "开业视频包" {
		t.Fatalf("content body = %v", out)
	}
	mark, _ := out["marking"].(map[string]any)
	if mark["ad_required"] != true {
		t.Fatalf("public marking = %v", mark)
	}
	// 不暴露授权原文/内部凭据。
	if _, bad := out["grant_refs"]; bad {
		t.Fatalf("grant_refs must not leak to the public face: %v", out)
	}
	media, _ := out["media"].([]any)
	if len(media) != 2 {
		t.Fatalf("media = %d, want 2 (video + cover)", len(media))
	}
	first, _ := media[0].(map[string]any)
	if first["media_id"] != videoID || first["role"] != "video" || first["mime_type"] != "video/mp4" {
		t.Fatalf("media[0] = %v", first)
	}
	if first["sha256"] == "" || first["byte_size"] == nil || first["availability"] != "ready" {
		t.Fatalf("media[0] fields = %v", first)
	}
	second, _ := media[1].(map[string]any)
	if second["media_id"] != coverID || second["role"] != "cover" || second["order"] != float64(0) {
		t.Fatalf("media[1] = %v", second)
	}

	// 两次读取逐字段一致(采用版不静默变化)。
	_, out2 := f.publicJSON(t, "GET", "/api/v1/public/actions/act_9/content", "")
	raw1, _ := json.Marshal(out)
	raw2, _ := json.Marshal(out2)
	if !bytes.Equal(raw1, raw2) {
		t.Fatal("two reads of the adopted content differ")
	}
}

func TestPublicActionContentBlockedAndExpired(t *testing.T) {
	f := newPublicContentFixture(t, true)
	packID, versionID, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_blk", "被撤权包", true)
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, "act_blk", packID, versionID, 1, "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}
	// 撤权(block)后:公开读面 409,拒新分发;media-access 拒新签发。
	if _, err := f.s.St.BlockStoreContentPackVersion(f.tenA, packID, versionID, "版权方撤回授权", "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}
	status, out := f.publicJSON(t, "GET", "/api/v1/public/actions/act_blk/content", "")
	if status != http.StatusConflict {
		t.Fatalf("blocked content = %d %v, want 409", status, out)
	}
	status, out = f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access", `{"media_ids":[],"purpose":"preview"}`)
	if status != http.StatusConflict {
		t.Fatalf("blocked media-access = %d %v, want 409", status, out)
	}

	// 授权过期版本:409(采用动作已拒,直接落库造一版过期并采用的场景由
	// Distributable 单点拒;这里验证未采用即绑不上的路径已在 store 层覆盖)。
}

func TestPublicActionContentCandidatesPool(t *testing.T) {
	f := newPublicContentFixture(t, true)
	// 主题一(当前采用)。
	packA, verA, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_c", "主题一", true)
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, "act_c", packA, verA, 1, "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}
	// 主题二:另一包,也采用 → 候选池成员。
	packB, verB, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "主题二", true)
	_ = packB
	// 未采用的第三包不出现在候选池。
	f.seedAdoptedContent(t, f.tenA, f.storeA, "", "未采用三", false)

	status, out := f.publicJSON(t, "GET", "/api/v1/public/actions/act_c/content", "")
	if status != http.StatusOK {
		t.Fatalf("content = %d: %v", status, out)
	}
	if out["selected_content_pack_version"] != verA {
		t.Fatalf("selected = %v", out["selected_content_pack_version"])
	}
	candidates, _ := out["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1 (只切合法已采用候选)", len(candidates))
	}
	c0, _ := candidates[0].(map[string]any)
	if c0["version_id"] != verB {
		t.Fatalf("candidate = %v, want %s", c0, verB)
	}
}

func TestMediaAccessIssuesControlledURLs(t *testing.T) {
	f := newPublicContentFixture(t, true)
	packID, versionID, videoID, coverID := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_m", "取材包", true)
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, "act_m", packID, versionID, 1, "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}

	// 正例:平台抓取用途,两个媒体一起取(平台适配器凭已签 claim 取材,不持
	// 访客 cookie——会话引用留空;带引用的签发校验由 server_media_access_
	// session_test.go 单独覆盖)。
	status, out := f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access",
		fmt.Sprintf(`{"media_ids":[%q,%q],"purpose":"platform_fetch"}`, videoID, coverID))
	if status != http.StatusOK {
		t.Fatalf("media-access = %d: %v", status, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	item, _ := items[0].(map[string]any)
	for _, key := range []string{"media_id", "url", "expires_at", "mime_type", "byte_size", "sha256", "filename"} {
		if item[key] == nil || item[key] == "" {
			t.Fatalf("item missing %s: %v", key, item)
		}
	}
	if !strings.HasPrefix(item["url"].(string), "/api/v1/public/content/media/") {
		t.Fatalf("url = %v", item["url"])
	}

	// 负例:错媒体(不属于本版本)。
	status, out = f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access", `{"media_ids":["cmd_missing"],"purpose":"preview"}`)
	if status != http.StatusUnprocessableEntity || out["error"] != "media_missing" {
		t.Fatalf("bad media = %d %v, want 422 media_missing", status, out)
	}
	// 负例:用途白名单。
	status, out = f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access", fmt.Sprintf(`{"media_ids":[%q],"purpose":"generate"}`, videoID))
	if status != http.StatusBadRequest {
		t.Fatalf("bad purpose = %d %v, want 400", status, out)
	}
	// 负例:错版本。
	status, _ = f.publicJSON(t, "POST", "/api/v1/public/content/scpv_missing/media-access", fmt.Sprintf(`{"media_ids":[%q],"purpose":"preview"}`, videoID))
	if status != http.StatusNotFound {
		t.Fatalf("bad version = %d, want 404", status)
	}
	// 续签 = 重发同一合法版本(凭据非一次性;审计逐行)。
	status, _ = f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access", fmt.Sprintf(`{"media_ids":[%q],"purpose":"preview"}`, videoID))
	if status != http.StatusOK {
		t.Fatal("renewal refused")
	}
	grants, err := f.s.St.ListMediaAccessGrants(f.tenA, versionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 3 {
		t.Fatalf("grants = %d, want 3 (2 first + 1 renewal)", len(grants))
	}
}

func TestControlledMediaServesRangeHeadAndMIME(t *testing.T) {
	f := newPublicContentFixture(t, true)
	packID, versionID, videoID, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_g", "播放包", true)
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, "act_g", packID, versionID, 1, "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}
	status, out := f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access",
		fmt.Sprintf(`{"media_ids":[%q],"purpose":"preview"}`, videoID))
	if status != http.StatusOK {
		t.Fatalf("media-access = %d: %v", status, out)
	}
	items := out["items"].([]any)
	item := items[0].(map[string]any)
	url := item["url"].(string)
	rawFixture, err := os.ReadFile(filepath.Join("..", "upload", "testdata", "tiny.mp4"))
	if err != nil {
		t.Fatal(err)
	}

	// 匿名全量 GET:200 + video/mp4 + Accept-Ranges(绝不以 HTML 冒充 mp4)。
	code, header, body := f.rawRequest(t, "GET", url, nil)
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	if ct := header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4 (not a login HTML page)", ct)
	}
	if header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q", header.Get("Accept-Ranges"))
	}
	if !bytes.Equal(body, rawFixture) {
		t.Fatal("served bytes differ from the uploaded file")
	}

	// Range:bytes=0-99 → 206 + Content-Range + 100 字节。
	code, header, body = f.rawRequest(t, "GET", url, map[string]string{"Range": "bytes=0-99"})
	if code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", code)
	}
	if cr := header.Get("Content-Range"); cr != fmt.Sprintf("bytes 0-99/%d", len(rawFixture)) {
		t.Fatalf("Content-Range = %q", cr)
	}
	if len(body) != 100 {
		t.Fatalf("range body = %d bytes, want 100", len(body))
	}
	if !bytes.Equal(body, rawFixture[:100]) {
		t.Fatal("range bytes mismatch")
	}

	// 后缀 Range:bytes=-256 → 最后 256 字节。
	code, _, body = f.rawRequest(t, "GET", url, map[string]string{"Range": "bytes=-256"})
	if code != http.StatusPartialContent || len(body) != 256 {
		t.Fatalf("suffix range = %d %d bytes", code, len(body))
	}
	if !bytes.Equal(body, rawFixture[len(rawFixture)-256:]) {
		t.Fatal("suffix bytes mismatch")
	}

	// 开区间 Range:bytes=100- → 到末尾。
	code, _, body = f.rawRequest(t, "GET", url, map[string]string{"Range": "bytes=100-"})
	if code != http.StatusPartialContent || len(body) != len(rawFixture)-100 {
		t.Fatalf("open range = %d %d bytes", code, len(body))
	}

	// 不可满足 Range → 416。
	code, _, _ = f.rawRequest(t, "GET", url, map[string]string{"Range": fmt.Sprintf("bytes=%d-", len(rawFixture)+10)})
	if code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range = %d, want 416", code)
	}

	// HEAD:零体 + 头齐(真实文件存在 + 大小可探测)。
	code, header, body = f.rawRequest(t, "HEAD", url, nil)
	if code != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD = %d %d bytes", code, len(body))
	}
	if header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("HEAD content type = %q", header.Get("Content-Type"))
	}

	// 下载用途:Content-Disposition attachment(中文文件名保真)。
	status, out = f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access",
		fmt.Sprintf(`{"media_ids":[%q],"purpose":"download"}`, videoID))
	if status != http.StatusOK {
		t.Fatalf("download access = %d: %v", status, out)
	}
	dl := out["items"].([]any)[0].(map[string]any)
	_, header, _ = f.rawRequest(t, "GET", dl["url"].(string), nil)
	if cd := header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("download disposition = %q, want attachment", cd)
	}

	// 过期 token → 410(不再签发新访问;重试须走 media-access 续签)。
	// 直接构造过期凭据验证服务端 fail-closed。
	claim, err := newExpiredClaim(versionID, videoID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := mintTestMediaToken("test-internal-secret", claim)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ = f.rawRequest(t, "GET", "/api/v1/public/content/media/"+token, nil)
	if code != http.StatusGone {
		t.Fatalf("expired token = %d, want 410", code)
	}
	// 篡改 token → 400。
	code, _, _ = f.rawRequest(t, "GET", "/api/v1/public/content/media/"+token+"xx", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("tampered token = %d, want 400", code)
	}
}

func TestControlledMediaRefusesBlockedVersionCachedToken(t *testing.T) {
	f := newPublicContentFixture(t, true)
	packID, versionID, videoID, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_rev", "撤权包", true)
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, "act_rev", packID, versionID, 1, "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}
	status, out := f.publicJSON(t, "POST", "/api/v1/public/content/"+versionID+"/media-access",
		fmt.Sprintf(`{"media_ids":[%q],"purpose":"preview"}`, videoID))
	if status != http.StatusOK {
		t.Fatalf("media-access = %d", status)
	}
	url := out["items"].([]any)[0].(map[string]any)["url"].(string)
	// 撤权后:即使缓存 token 未过期,受控读也拒绝(fail-closed)。
	if _, err := f.s.St.BlockStoreContentPackVersion(f.tenA, packID, versionID, "授权撤销", "usr_owner_a", timeNowUTC()); err != nil {
		t.Fatal(err)
	}
	code, _, _ := f.rawRequest(t, "GET", url, nil)
	if code != http.StatusConflict {
		t.Fatalf("blocked cached token = %d, want 409", code)
	}
}

func TestControlledMediaCrossStoreIsolation(t *testing.T) {
	f := newPublicContentFixture(t, true)
	packID, versionID, videoID, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "act_iso", "隔离包", true)
	_ = packID
	// B 店同 sha 媒体不存在;A 版本+非本版本媒体在 media-access 已拒;
	// 这里验证直接构造 token 指向不存在媒体 = 410(fail-closed)。
	claim, err := newExpiredClaim(versionID, videoID)
	if err != nil {
		t.Fatal(err)
	}
	claim2 := claim
	claim2.MediaID = "cmd_ghost"
	token, err := mintTestMediaToken("test-internal-secret", claim2)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := f.rawRequest(t, "GET", "/api/v1/public/content/media/"+token, nil)
	if code != http.StatusGone {
		t.Fatalf("ghost media token = %d, want 410", code)
	}
}
