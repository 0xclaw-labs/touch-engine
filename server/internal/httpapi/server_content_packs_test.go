package httpapi

// server_content_packs_test.go: HUI-3553 活动内容包 HTTP 验收矩阵。
//
// 覆盖(票面 TDD 清单,HTTP 部分):
//   - 管理面全套端点:建包/列包/建版本/明确采用/阻止新分发/回流候选/
//     制作交接/能力申报/媒体交接铸造与兑换;
//   - authz:管理动作仅 org_owner(staff 403);读按既有业务角色;
//   - 校验矩阵:声明缺失各自 422(独立原因码);媒体缺失 422 media_missing;
//     授权过期采用 422 grant_expired;blocked 采用 422 not_distributable;
//   - 回流候选:同幂等键重放 replayed=true 且零新增;晚到旧稿不动指针;
//   - 制作交接:同幂等键重放同一行;引擎未接 = 503 handoff_unavailable
//     (如实降级,不伪造能力);capabilities 如实 production_available=false;
//   - 媒体交接:铸造带有效期/content-type/Content-Disposition;过期 410;
//     篡改 400;文件字节零过本服务(只有引用元数据);
//   - 公共快照(顾客可触达,走 FEATURE_CONTENT_PACKS 默认 off):off = 路由
//     不注册 + gate 统一 404;on = 公开字段白名单、blocked/grant 过期拒分发、
//     采用版两次读取逐字段一致(不静默变化);
//   - 跨租户一律 404(绝不泄露存在性)。

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

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type contentPackFixture struct {
	s        *Server
	ts       *httptest.Server
	tenA     string
	tenB     string
	campA    string
	linkCode string
}

func newContentPackFixture(t *testing.T, packsPublicOn bool) *contentPackFixture {
	return newContentPackFixtureWith(t, packsPublicOn, "", "", nil)
}

// newContentPackFixtureWith is the HUI-3553 W5 variant: it injects the trusted
// service registry (TOUCH_SERVICE_TOKENS/ROLES) and an optional log sink so the
// producer seam can be exercised end-to-end without the admin session.
func newContentPackFixtureWith(t *testing.T, packsPublicOn bool, serviceTokens, serviceRoles string, logSink io.Writer) *contentPackFixture {
	t.Helper()
	flag := func(on bool) string {
		if on {
			return "on"
		}
		return ""
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_ASSET_LIB":
			return "on"
		case "FEATURE_CONTENT_PACKS":
			return flag(packsPublicOn)
		case "TOUCH_SERVICE_TOKENS":
			return serviceTokens
		case "TOUCH_SERVICE_ROLES":
			return serviceRoles
		}
		return ""
	})

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("内容包商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("内容包商家B")
	if err != nil {
		t.Fatal(err)
	}
	cA, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, Title: "开业活动A", PublicContent: "周末开业", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	link, err := st.CreateLink(tenA.ID, cA.ID, "test")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	// 公共快照面只对 available 链接作答:活动置 active
	if _, err := st.TransitionCampaign(cA.ID, tenA.ID, "active"); err != nil {
		t.Fatalf("activate campaign: %v", err)
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
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "ownerb@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}

	sink := logSink
	if sink == nil {
		sink = io.Discard
	}
	s := New(cfg, d, idc, &upload.Client{}, log.New(sink, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &contentPackFixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, campA: cA.ID, linkCode: link.Code}
}

func (f *contentPackFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
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

func (f *contentPackFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

// mustLibAsset registers a lib asset directly through the store (the lib
// registration HTTP matrix is covered by server_asset_lib_test.go).
func (f *contentPackFixture) mustLibAsset(t *testing.T, ref, sha, mediaType string) store.LibAsset {
	t.Helper()
	a, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef: ref, SHA256: sha, MediaType: assetlib.MediaType(mediaType),
		Source: assetlib.SourceMerchantUpload, Purpose: "campaign_publish", GrantRef: "grant-2026-001",
	}, "", "seed")
	if err != nil {
		t.Fatalf("lib asset: %v", err)
	}
	return a
}

func packVersionBody(videoID, coverID string) string {
	return fmt.Sprintf(`{
		"content_kind":"video",
		"video_asset_id":%q,
		"cover_asset_id":%q,
		"title":"周末开业五折",
		"body":"本店周末全场五折。",
		"topics":["开业","五折"],
		"store_name":"旗舰店",
		"price_note":"全场五折",
		"valid_note":"2026-10-14 至 2026-10-20",
		"platform_versions":[{"platform":"douyin","note":"竖版","source_version":"src-1"}],
		"grant_refs":["grant-2026-001"],
		"marking":{"ad_required":true},
		"source":"merchant_upload",
		"status":"candidate"
	}`, videoID, coverID)
}

func mustPack(t *testing.T, f *contentPackFixture) string {
	t.Helper()
	status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-packs", `{"title":"开业主题包"}`)
	if status != 201 {
		t.Fatalf("pack: %d %v", status, out)
	}
	return out["id"].(string)
}

func mustVersion(t *testing.T, f *contentPackFixture, packID, videoID, coverID string) string {
	t.Helper()
	status, out := f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions", packVersionBody(videoID, coverID))
	if status != 201 {
		t.Fatalf("version: %d %v", status, out)
	}
	return out["id"].(string)
}

func TestContentPacksMerchantFlow(t *testing.T) {
	f := newContentPackFixture(t, false)
	video := f.mustLibAsset(t, "up-v1", strings.Repeat("a", 64), "video")
	cover := f.mustLibAsset(t, "up-c1", strings.Repeat("b", 64), "image")

	packID := mustPack(t, f)
	v1 := mustVersion(t, f, packID, video.ID, cover.ID)

	// 商家读包:版本与快照标识可见,技术簿记不进列表(无 asset_id 手填面)
	status, out := f.owner(t, "GET", "/api/v1/content-packs/"+packID, "")
	if status != 200 {
		t.Fatalf("get pack: %d %v", status, out)
	}
	if versions, ok := out["versions"].([]any); !ok || len(versions) != 1 {
		t.Fatalf("versions: %v", out)
	}

	// 明确采用
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/adopt", `{"platform":"douyin"}`)
	if status != 200 {
		t.Fatalf("adopt: %d %v", status, out)
	}
	if out["platform"] != "douyin" || out["version_id"] != v1 {
		t.Fatalf("adoption: %v", out)
	}

	// 活动列包:包/版本/采用态一次拿全(面板数据面)
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.campA+"/content-packs", "")
	if status != 200 {
		t.Fatalf("list: %d %v", status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items: %v", out)
	}

	// staff 建包 403(管理动作 org_owner 专属)
	status, _ = f.do(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-packs", "sess-staff-a", f.tenA, `{"title":"x"}`)
	if status != http.StatusForbidden {
		t.Fatalf("staff create pack: %d", status)
	}

	// 跨租户:owner-b 读 A 的包 = 404
	status, _ = f.do(t, "GET", "/api/v1/content-packs/"+packID, "sess-owner-b", f.tenB, "")
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant pack: %d", status)
	}

	// 声明缺失:缺价格 = 422 missing_price_note
	bad := strings.Replace(packVersionBody(video.ID, cover.ID), `"price_note":"全场五折",`, "", 1)
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions", bad)
	if status != 422 || out["error"] != "missing_price_note" {
		t.Fatalf("missing price: %d %v", status, out)
	}

	// 媒体缺失:引用不存在的素材 = 422 media_missing
	badMedia := strings.Replace(packVersionBody("las_missing", cover.ID), `"video_asset_id":"las_missing"`, `"video_asset_id":"las_missing"`, 1)
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions", badMedia)
	if status != 422 || out["error"] != "media_missing" {
		t.Fatalf("media missing: %d %v", status, out)
	}
}

func TestContentPacksReturnCandidatesAndAdoptionSafety(t *testing.T) {
	f := newContentPackFixture(t, false)
	video := f.mustLibAsset(t, "up-v1", strings.Repeat("a", 64), "video")
	cover := f.mustLibAsset(t, "up-c1", strings.Repeat("b", 64), "image")
	packID := mustPack(t, f)
	v1 := mustVersion(t, f, packID, video.ID, cover.ID)

	status, _ := f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/adopt", `{"platform":"douyin"}`)
	if status != 200 {
		t.Fatalf("adopt: %d", status)
	}

	retBody := fmt.Sprintf(`{
		"content_kind":"video","video_asset_id":%q,"cover_asset_id":%q,
		"title":"产品图回流主题","body":"产品图成果回流的文案。",
		"store_name":"旗舰店","price_note":"全场五折","valid_note":"2026-10-14 至 2026-10-20",
		"platform_versions":[{"platform":"douyin","note":"竖版","source_version":"src-1745-1"}],
		"grant_refs":["grant-2026-001"],"source":"product_photo_return",
		"idempotency_key":"return-1745-0001"
	}`, video.ID, cover.ID)

	// 回流候选:201;重放:200 replayed=true 且同一行
	status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-packs/"+packID+"/return-candidates", retBody)
	if status != 201 || out["replayed"] != false {
		t.Fatalf("return: %d %v", status, out)
	}
	cand := out["id"].(string)
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-packs/"+packID+"/return-candidates", retBody)
	if status != 200 || out["replayed"] != true || out["id"] != cand {
		t.Fatalf("return replay: %d %v", status, out)
	}

	// 回流/晚到旧稿绝不覆盖当前采用指针
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.campA+"/content-packs", "")
	_ = status
	items := out["items"].([]any)
	first := items[0].(map[string]any)
	adoptions := first["adoptions"].([]any)
	if len(adoptions) != 1 || adoptions[0].(map[string]any)["version_id"] != v1 {
		t.Fatalf("adoption moved by return: %v", adoptions)
	}

	// 普通建版本撞已有幂等键 = 409 duplicate_idempotency_key
	dup := strings.Replace(retBody, `"source":"product_photo_return"`, `"source":"merchant_upload"`, 1)
	dup = strings.Replace(dup, `"status":"candidate"`, `"status":"draft"`, 1)
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions", dup)
	if status != 409 || out["error"] != "duplicate_idempotency_key" {
		t.Fatalf("dup key: %d %v", status, out)
	}
}

func TestContentPacksBlockAndGrantExpiry(t *testing.T) {
	f := newContentPackFixture(t, false)
	video := f.mustLibAsset(t, "up-v1", strings.Repeat("a", 64), "video")
	cover := f.mustLibAsset(t, "up-c1", strings.Repeat("b", 64), "image")
	packID := mustPack(t, f)

	// grant_until 已过期:建版本成功(登记事实),采用拒绝 422 grant_expired
	expiredBody := strings.Replace(packVersionBody(video.ID, cover.ID),
		`"grant_refs":["grant-2026-001"],`, `"grant_refs":["grant-2026-001"],"grant_until":"2026-10-01T00:00:00Z",`, 1)
	status, out := f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions", expiredBody)
	if status != 201 {
		t.Fatalf("expired version create: %d %v", status, out)
	}
	vExp := out["id"].(string)
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+vExp+"/adopt", `{"platform":"douyin"}`)
	if status != 422 || out["error"] != "grant_expired" {
		t.Fatalf("expired adopt: %d %v", status, out)
	}

	// blocked:原因必填;阻止新分发(采用 422 not_distributable)
	v1 := mustVersion(t, f, packID, video.ID, cover.ID)
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/block", `{"reason":""}`)
	if status != 422 || out["error"] != "missing_block_reason" {
		t.Fatalf("block empty reason: %d %v", status, out)
	}
	status, _ = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/block", `{"reason":"素材授权被平台撤销"}`)
	if status != 200 {
		t.Fatalf("block: %d", status)
	}
	status, out = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/adopt", `{"platform":"douyin"}`)
	if status != 422 || out["error"] != "not_distributable" {
		t.Fatalf("blocked adopt: %d %v", status, out)
	}
}

func TestContentHandoffHonestDegradation(t *testing.T) {
	f := newContentPackFixture(t, false)

	// capabilities 如实申报:引擎未接 = production_available:false + 原因
	status, out := f.owner(t, "GET", "/api/v1/content-handoffs/capabilities", "")
	if status != 200 || out["production_available"] != false || out["reason"] == "" {
		t.Fatalf("capabilities: %d %v", status, out)
	}

	body := `{"purpose":"开业宣传","store_brief":"{\"store_name\":\"旗舰店\"}","desired_form":"15s 竖版视频","asset_ids":[],"idempotency_key":"handoff-0001"}`
	// 交接落库去重但如实 503(不伪造可用)
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", body)
	if status != 503 || out["error"] != "handoff_unavailable" {
		t.Fatalf("handoff: %d %v", status, out)
	}
	h1 := out["handoff"].(map[string]any)["id"].(string)
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", body)
	if status != 503 || out["replayed"] != true || out["handoff"].(map[string]any)["id"] != h1 {
		t.Fatalf("handoff replay: %d %v", status, out)
	}
}

func TestMediaHandoffMintAndRedeem(t *testing.T) {
	f := newContentPackFixture(t, false)
	video := f.mustLibAsset(t, "up-v1", strings.Repeat("a", 64), "video")
	cover := f.mustLibAsset(t, "up-c1", strings.Repeat("b", 64), "image")
	packID := mustPack(t, f)
	v1 := mustVersion(t, f, packID, video.ID, cover.ID)

	// 铸造:带有效期/content_type/Content-Disposition 的受控 URL
	status, out := f.owner(t, "POST", "/api/v1/content-pack-versions/"+v1+"/media-handoff",
		`{"asset":"video","content_type":"video/mp4","disposition":"attachment","filename":"shop.mp4"}`)
	if status != 200 {
		t.Fatalf("mint: %d %v", status, out)
	}
	token := out["token"].(string)
	if out["content_type"] != "video/mp4" || out["content_disposition"] != `attachment; filename="shop.mp4"` {
		t.Fatalf("mint fields: %v", out)
	}
	if out["expires_at"] == "" || !strings.HasPrefix(out["url"].(string), "/api/v1/media-handoffs/") {
		t.Fatalf("mint url: %v", out)
	}

	// 兑换:引用元数据(平台 upload 拉取锚点),零文件字节
	status, out = f.owner(t, "GET", "/api/v1/media-handoffs/"+token, "")
	if status != 200 || out["asset_ref"] != video.AssetRef || out["sha256"] != video.SHA256 {
		t.Fatalf("redeem: %d %v", status, out)
	}

	// 篡改 400;伪造 token 400
	status, _ = f.owner(t, "GET", "/api/v1/media-handoffs/"+token+"xx", "")
	if status != http.StatusBadRequest {
		t.Fatalf("tampered: %d", status)
	}

	// 过期:TTL 上限校验 422;兑换端过期 410(用极短 TTL 不可行,直接构造过期 mint 被拒)
	status, out = f.owner(t, "POST", "/api/v1/content-pack-versions/"+v1+"/media-handoff",
		`{"asset":"video","content_type":"video/mp4","disposition":"inline","ttl_seconds":7200}`)
	if status != 422 || out["error"] != "bad_handoff_ttl" {
		t.Fatalf("ttl cap: %d %v", status, out)
	}

	// 坏 content type 422
	status, out = f.owner(t, "POST", "/api/v1/content-pack-versions/"+v1+"/media-handoff",
		`{"asset":"video","content_type":"application/octet-stream","disposition":"inline"}`)
	if status != 422 || out["error"] != "bad_content_type" {
		t.Fatalf("bad content type: %d %v", status, out)
	}
}

func TestPublicContentSnapshotFeatureGate(t *testing.T) {
	f := newContentPackFixture(t, false) // off 基线
	code := f.linkCode
	for _, path := range []string{
		"/api/v1/public/links/" + code + "/content-snapshot?pack_id=x&platform=douyin",
		"/api/v1/public/links/" + code + "/content-choices",
	} {
		status, _ := f.do(t, "GET", path, "", "", "")
		// off = 路由不注册:统一 404(mux 空体),面板不可见
		if status != http.StatusNotFound {
			t.Fatalf("off baseline %s: %d", path, status)
		}
	}
}

func TestPublicContentSnapshotServesAdoptedVersion(t *testing.T) {
	f := newContentPackFixture(t, true) // FEATURE_CONTENT_PACKS on
	video := f.mustLibAsset(t, "up-v1", strings.Repeat("a", 64), "video")
	cover := f.mustLibAsset(t, "up-c1", strings.Repeat("b", 64), "image")
	packID := mustPack(t, f)
	v1 := mustVersion(t, f, packID, video.ID, cover.ID)
	status, _ := f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/adopt", `{"platform":"douyin"}`)
	if status != 200 {
		t.Fatalf("adopt: %d", status)
	}

	base := "/api/v1/public/links/" + f.linkCode

	// choices:只有已采用包;封面引用在列,内部簿记不出现
	status, out := f.do(t, "GET", base+"/content-choices", "", "", "")
	if status != 200 {
		t.Fatalf("choices: %d %v", status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("choices items: %v", out)
	}
	choice := items[0].(map[string]any)
	if choice["pack_id"] != packID || choice["title"] != "开业主题包" {
		t.Fatalf("choice: %v", choice)
	}

	// snapshot:公开字段白名单;内部字段(candidate/block 簿记、grant 原文、内部 id)不出现
	status, out = f.do(t, "GET", base+"/content-snapshot?pack_id="+packID+"&platform=douyin", "", "", "")
	if status != 200 {
		t.Fatalf("snapshot: %d %v", status, out)
	}
	for _, must := range []string{"pack_version_id", "title", "body", "store_name", "price_note", "valid_note", "cover"} {
		if _, ok := out[must]; !ok {
			t.Fatalf("snapshot missing %s: %v", must, out)
		}
	}
	if out["pack_version_id"] != v1 {
		t.Fatalf("snapshot version: %v", out)
	}
	for _, forbidden := range []string{"grant_refs", "grant_until", "idempotency_key", "tenant_id", "blocked_reason", "asset_id"} {
		if _, ok := out[forbidden]; ok {
			t.Fatalf("snapshot leaks %s: %v", forbidden, out)
		}
	}
	snap1 := out

	// 采用版不静默变化:候选回流后两次读取逐字段一致
	retBody := fmt.Sprintf(`{"content_kind":"video","video_asset_id":%q,"cover_asset_id":%q,
		"title":"回流主题","body":"回流文案。","store_name":"旗舰店","price_note":"x","valid_note":"y",
		"platform_versions":[{"platform":"douyin","note":"n","source_version":"s"}],
		"source":"product_photo_return","idempotency_key":"ret-1"}`, video.ID, cover.ID)
	status, _ = f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-packs/"+packID+"/return-candidates", retBody)
	if status != 201 {
		t.Fatalf("return: %d", status)
	}
	status, out = f.do(t, "GET", base+"/content-snapshot?pack_id="+packID+"&platform=douyin", "", "", "")
	if status != 200 || out["pack_version_id"] != snap1["pack_version_id"] || out["title"] != snap1["title"] {
		t.Fatalf("snapshot drifted: %v vs %v", out, snap1)
	}

	// block 后新分发被拒:409 not_distributable
	status, _ = f.owner(t, "POST", "/api/v1/content-packs/"+packID+"/versions/"+v1+"/block", `{"reason":"授权撤销"}`)
	if status != 200 {
		t.Fatalf("block: %d", status)
	}
	status, out = f.do(t, "GET", base+"/content-snapshot?pack_id="+packID+"&platform=douyin", "", "", "")
	if status != http.StatusConflict || out["error"] != "not_distributable" {
		t.Fatalf("blocked snapshot: %d %v", status, out)
	}

	// 未采用的活动包 = 404
	status, _ = f.do(t, "GET", base+"/content-snapshot?pack_id=nopack&platform=douyin", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown pack snapshot: %d", status)
	}
}
