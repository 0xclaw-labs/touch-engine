package httpapi

// server_media_access_session_test.go: 中央集成验收(2026-11-10)。
// HUI-3553 media-access 签发时刻接入 HUI-3546 会话权威:
//   - 有效会话签发成功(会话的动作绑定到版本所在包);
//   - 预览期换选到候选包后,凭所选版本签发成功(内容匹配分支);
//   - 无会话引用照常签发(平台适配器路径零变化);
//   - 已结束会话 409 session_ended;未知/跨租户会话统一 404(反枚举);
//   - 跨动作会话 409 action_mismatch;
//   - 顺带核对目录接线:ResolveAction 的 ContentPackVersion = 绑定包当前采用版。
//
// 兑换时刻(token GET)不校验会话——平台适配器凭已签 claim 取材,不持访客
// cookie;校验只在签发时刻。会话可取材范围 = 本动作绑定包的版本 ∪ 会话当前
// 选中的版本;两者皆非(别动作的包/别租户)一律拒绝。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func newMediaSessionFixture(t *testing.T) *publicContentFixture {
	t.Helper()
	return newPublicContentFixture(t, true,
		"FEATURE_PARTICIPATION", "on",
		"PUBLIC_BASE_URL", "https://h5.example.com",
	)
}

// seedActions seeds the store's action list (store level; no binding). The
// store mints opaque instance ids — they are returned in input order.
func (f *publicContentFixture) seedActions(t *testing.T, actionIDs ...string) []string {
	t.Helper()
	_, rev, err := f.s.St.ListStoreActions(f.tenA, f.storeA)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	inputs := make([]store.StoreActionInput, 0, len(actionIDs))
	for _, id := range actionIDs {
		code, label, target := "A18", "加微信", "https://work.weixin.qq.com/ca"
		if id == "act_wifi" {
			code, label, target = "A17", "门店 Wi-Fi", "https://shop.example.com/wifi"
		}
		inputs = append(inputs, store.StoreActionInput{
			CatalogCode: code, Label: label, TargetRef: target, Enabled: true,
		})
	}
	_, saved, err := f.s.St.ReplaceStoreActions(f.tenA, f.storeA, rev, inputs)
	if err != nil {
		t.Fatalf("seed actions: %v", err)
	}
	out := make([]string, 0, len(saved))
	for _, it := range saved {
		out = append(out, it.ID)
	}
	return out
}

func (f *publicContentFixture) bindAction(t *testing.T, actionID, packID string) {
	t.Helper()
	v, err := f.s.St.GetStoreAdoptedPackVersion(f.tenA, packID)
	if err != nil {
		t.Fatalf("adopted version: %v", err)
	}
	if _, err := f.s.St.BindActionContent(f.tenA, f.storeA, actionID, packID, v.ID, 1, "usr_owner_a", time.Now()); err != nil {
		t.Fatalf("bind: %v", err)
	}
}

// createSession creates a guest session for the action, returning the session
// id and the visitor cookie pair.
func (f *publicContentFixture) createSession(t *testing.T, actionID string) (string, string, int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/actions/"+actionID+"/sessions", strings.NewReader(`{"touchpoint_source":"nfc"}`))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	sid, _ := out["action_session_id"].(string)
	return sid, cookiePair(res.Header), res.StatusCode, out
}

// mediaAccess posts to the media-access endpoint with an optional session
// reference and cookie.
func (f *publicContentFixture) mediaAccess(t *testing.T, versionID, sessionID, cookie, mediaID string) (int, map[string]any) {
	t.Helper()
	body := `{"media_ids":["` + mediaID + `"],"purpose":"preview"`
	if sessionID != "" {
		body += `,"action_session_id":"` + sessionID + `"`
	}
	body += `}`
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/content/"+versionID+"/media-access", strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("media-access: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestMediaAccessWithValidSessionIssues(t *testing.T) {
	f := newMediaSessionFixture(t)
	p1, v1, video1, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "绑定包一", true)
	actA := f.seedActions(t, "act_wechat")[0]
	f.bindAction(t, actA, p1)

	// 目录接线核对:ResolveAction 的内容引用 = 绑定包当前采用版。
	ref, ok, err := f.s.ResolveAction(context.Background(), actA)
	if err != nil || !ok || ref.ContentPackVersion != v1 {
		t.Fatalf("directory content ref = (%v, %v, %q); want ok pack version %s", ok, err, ref.ContentPackVersion, v1)
	}

	sid, cookie, status, body := f.createSession(t, actA)
	if status != http.StatusCreated || sid == "" {
		t.Fatalf("create session = %d (%v)", status, body)
	}

	// 有效会话签发成功。
	status, out := f.mediaAccess(t, v1, sid, cookie, video1)
	if status != http.StatusOK {
		t.Fatalf("media-access with session = %d (%v); want 200", status, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", out)
	}

	// 无会话引用照常签发(平台适配器路径零变化)。
	status, out = f.mediaAccess(t, v1, "", "", video1)
	if status != http.StatusOK {
		t.Fatalf("media-access without session = %d (%v)", status, out)
	}
}

func TestMediaAccessAfterContentSwitchStillIssues(t *testing.T) {
	f := newMediaSessionFixture(t)
	p1, v1, video1, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "绑定包一", true)
	p2, v2, video2, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "候选包二", true)
	actA := f.seedActions(t, "act_wechat")[0]
	f.bindAction(t, actA, p1)

	sid, cookie, status, body := f.createSession(t, actA)
	if status != http.StatusCreated {
		t.Fatalf("create session = %d (%v)", status, body)
	}

	// 预览期换选到候选包二(会话内容引用 = v2)。
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/action-sessions/"+sid+"/content",
		strings.NewReader(`{"content_pack_version":"`+v2+`"}`))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("switch content: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("switch content = %d", res.StatusCode)
	}

	// 换选后凭所选版本签发成功(内容匹配分支)。
	status, out := f.mediaAccess(t, v2, sid, cookie, video2)
	if status != http.StatusOK {
		t.Fatalf("media-access after switch = %d (%v); want 200", status, out)
	}
	// 本动作绑定包的版本仍在可取材范围(动作内容,换选不收回既有事实)。
	status, out = f.mediaAccess(t, v1, sid, cookie, video1)
	if status != http.StatusOK {
		t.Fatalf("bound pack version after switch = %d (%v); want 200", status, out)
	}
	_ = p2
}

func TestMediaAccessEndedSessionRejected(t *testing.T) {
	f := newMediaSessionFixture(t)
	p1, v1, video1, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "绑定包一", true)
	actA := f.seedActions(t, "act_wechat")[0]
	f.bindAction(t, actA, p1)

	sid, cookie, status, _ := f.createSession(t, actA)
	if status != http.StatusCreated {
		t.Fatalf("create session = %d", status)
	}

	// 取消 = 会话终态(会话无 TTL,ended 是唯一过期)。
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/action-sessions/"+sid+"/cancel", strings.NewReader(`{}`))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	res.Body.Close()

	status, out := f.mediaAccess(t, v1, sid, cookie, video1)
	if status != http.StatusConflict || out["error"] != "session_ended" {
		t.Fatalf("ended session = %d %v; want 409 session_ended", status, out)
	}
}

func TestMediaAccessUnknownAndCrossTenantSessionsRejected(t *testing.T) {
	f := newMediaSessionFixture(t)
	p1, v1, video1, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "绑定包一", true)
	actA := f.seedActions(t, "act_wechat")[0]
	f.bindAction(t, actA, p1)
	// 别家店的已采用版本(跨租户素材):store 层链条种(夹具的 owner 不是
	// 租户 B 成员,HTTP 种包过不了成员门,而这里只需要行的存在性)。
	packB, verB, mediaB := seedAdoptedStorePackWithMedia(t, f.s.St, f.tenB, f.storeB)

	// 未知会话:404,与跨租户同形(反枚举)。
	status, out := f.mediaAccess(t, v1, "as_missing000000", "", video1)
	if status != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("unknown session = %d %v; want 404 not_found", status, out)
	}

	// 跨租户:租户 A 的会话引用去取租户 B 的版本 → 404。
	sid, cookie, status, _ := f.createSession(t, actA)
	if status != http.StatusCreated {
		t.Fatalf("create session = %d", status)
	}
	status, out = f.mediaAccess(t, verB, sid, cookie, mediaB)
	if status != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("cross-tenant session = %d %v; want 404 not_found", status, out)
	}
	// 同会话对本租户版本仍正常(负例不污染状态)。
	status, _ = f.mediaAccess(t, v1, sid, cookie, video1)
	if status != http.StatusOK {
		t.Fatalf("own version after negatives = %d", status)
	}
	_ = packB
}

// seedAdoptedStorePackWithMedia: store 层种一个「已采用」包(租户 B 用——夹具
// 的 owner 不是租户 B 成员,HTTP 种包过不了成员门;此处只需行的存在性),
// 顺带给出版本引用的视频媒体 id(供 media-access 请求体引用)。
func seedAdoptedStorePackWithMedia(t *testing.T, st *store.Store, tenantID, storeID string) (packID, versionID, videoMediaID string) {
	t.Helper()
	pack, err := st.CreateStoreContentPack(tenantID, storeID, "", "别家包", "test")
	if err != nil {
		t.Fatalf("seed pack b: %v", err)
	}
	video, _, err := st.RegisterContentMedia(tenantID, storeID, upload.MediaMeta{
		SHA256: strings.Repeat("c", 64), MIME: "video/mp4", Kind: upload.KindVideo,
		ByteSize: 1024, DurationMS: 1234, Width: 64, Height: 64, MediaRef: strings.Repeat("c", 64),
	}, "b.mp4", "test")
	if err != nil {
		t.Fatalf("seed media b: %v", err)
	}
	cover, _, err := st.RegisterContentMedia(tenantID, storeID, upload.MediaMeta{
		SHA256: strings.Repeat("d", 64), MIME: "image/jpeg", Kind: upload.KindImage,
		ByteSize: 1024, Width: 64, Height: 64, MediaRef: strings.Repeat("d", 64),
	}, "b.jpg", "test")
	if err != nil {
		t.Fatalf("seed cover b: %v", err)
	}
	v, err := st.CreateStoreContentPackVersion(tenantID, pack.ID, store.StoreVersionSpec{
		ContentKind: contentpack.ContentKindVideo, VideoMediaID: video.ID, CoverMediaID: cover.ID,
		Title: "别家包", Body: "别家内容。", Topics: []string{"x"},
		StoreName: "别家店", PriceNote: "-", ValidNote: "长期",
		PlatformVersions: []contentpack.PlatformVersion{{Platform: "douyin", Note: "竖版", SourceVersion: "src-b"}},
		GrantRefs:        []string{"grant-b"},
		Source:           contentpack.SourceMerchantUpload,
	}, contentpack.StatusCandidate, "test")
	if err != nil {
		t.Fatalf("seed version b: %v", err)
	}
	if _, err := st.AdoptStoreContentPackVersion(tenantID, pack.ID, v.ID, "douyin", "test", time.Now()); err != nil {
		t.Fatalf("seed adopt b: %v", err)
	}
	return pack.ID, v.ID, video.ID
}

func TestMediaAccessCrossActionSessionRejected(t *testing.T) {
	f := newMediaSessionFixture(t)
	p1, v1, video1, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "绑定包一", true)
	ids := f.seedActions(t, "act_wechat", "act_wifi")
	f.bindAction(t, ids[0], p1)

	// 跨动作会话:动作 Wi-Fi 的会话不能为「加微信」动作绑定的内容签发
	// (无绑定动作的会话内容引用为空,两个匹配分支都不成立)。
	sidB, cookieB, status, _ := f.createSession(t, ids[1])
	if status != http.StatusCreated {
		t.Fatalf("create session wifi = %d", status)
	}
	status, out := f.mediaAccess(t, v1, sidB, cookieB, video1)
	if status != http.StatusConflict || out["error"] != "action_mismatch" {
		t.Fatalf("cross-action session = %d %v; want 409 action_mismatch", status, out)
	}
}
