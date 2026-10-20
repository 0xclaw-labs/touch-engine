package httpapi

// HUI-1664 FEAT-0165 二维码兜底入口的服务端面测试:
//   - owner 才能下载/读取 QR(PNG 或 json 载荷);staff 403;游客 401;跨租户 404;
//   - QR 载荷 = PUBLIC_BASE_URL + /c/{code} 的纯短码 canonical URL,
//     PNG 解码回读必须与 canonical URL 一致,载荷无凭证/租户/客户信息;
//   - size 白名单 128/256/512,其余显式 400;
//   - PUBLIC_BASE_URL 缺失 → 503 qr_not_configured(fail-closed);
//   - 公共面/管理面对伪造 query 参数(token/tenant/admin/role/next/session_token)
//     行为不变:响应字节一致、不派生任何后台能力、参数值不入日志。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// fixtureWithQR:与 newFixture 相同,但注入 PUBLIC_BASE_URL(并可选记录请求日志)。
func fixtureWithQR(t *testing.T, logBuf *bytes.Buffer) *fixture {
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
		return ""
	})

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	logger := log.New(io.Discard, "", 0)
	if logBuf != nil {
		logger = log.New(logBuf, "", 0)
	}
	s := New(cfg, d, idc, upc, logger)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID,
		ownA: "usr_owner_a", ownB: "usr_owner_b", staffA: "usr_staff_a"}
}

// seedActiveCampaign:创建 campaign + link 并把活动推到 active(含有效期窗口)。
func seedActiveCampaign(t *testing.T, f *fixture) (campaignID, linkID, code string) {
	t.Helper()
	now := time.Now().UTC()
	payload := fmt.Sprintf(`{"title":"周年庆","public_content":"到店有礼","starts_at":%q,"ends_at":%q}`,
		now.Add(-1*time.Hour).Format(time.RFC3339), now.Add(48*time.Hour).Format(time.RFC3339))
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, payload)
	mustEqual(t, status, http.StatusCreated)
	campaignID = cmp["id"].(string)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	linkID = link["id"].(string)
	code = link["code"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	return campaignID, linkID, code
}

// doRaw:像 do,但返回原始 body 与响应头(PNG 断言需要)。
func (f *fixture) doRaw(t *testing.T, method, path, session, tenant, body string) (int, http.Header, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	mustNoErr(t, err)
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
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	mustNoErr(t, err)
	return res.StatusCode, res.Header, raw
}

func decodeQRText(t *testing.T, pngBytes []byte) string {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatalf("binary bitmap: %v", err)
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	if err != nil {
		t.Fatalf("qr decode: %v", err)
	}
	return res.GetText()
}

func TestQRDownloadOwnerHappyPath(t *testing.T) {
	f := fixtureWithQR(t, nil)
	campaignID, linkID, code := seedActiveCampaign(t, f)

	status, hdr, body := f.doRaw(t, "GET",
		fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode", campaignID, linkID), "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if ct := hdr.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}
	if cd := hdr.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, code) {
		t.Fatalf("content-disposition = %q, want attachment with code %s", cd, code)
	}
	if !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("body is not PNG (len=%d)", len(body))
	}
	// 机器验收:解码回读内容 = canonical 短码 URL
	got := decodeQRText(t, body)
	if want := "https://h5.test/c/" + code; got != want {
		t.Fatalf("decoded %q, want %q", got, want)
	}
}

func TestQRDownloadPermissionMatrix(t *testing.T) {
	f := fixtureWithQR(t, nil)
	campaignID, linkID, _ := seedActiveCampaign(t, f)
	qrPath := fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode", campaignID, linkID)

	// guest -> 401
	status, _, _ := f.doRaw(t, "GET", qrPath, "", f.tenA, "")
	mustEqual(t, status, http.StatusUnauthorized)

	// staff -> 403 forbidden(owner-only export)
	status, _, body := f.do(t, "GET", qrPath, "sess-staff-a", f.tenA, "")
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("staff QR = %d %v, want 403 forbidden", status, body)
	}
	// staff 用伪造 query 提权也不行(参数绝不参与鉴权)
	status, _, body = f.do(t, "GET", qrPath+"?role=owner&admin=1", "sess-staff-a", f.tenA, "")
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("staff QR with forged query = %d %v, want 403 forbidden", status, body)
	}

	// owner B(另一租户)→ 404(A 的记录在 B 的范围不可见)
	status, _, _ = f.doRaw(t, "GET", qrPath, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)

	// 本租户内不存在的 link id → 404
	status, _, _ = f.doRaw(t, "GET",
		fmt.Sprintf("/api/v1/campaigns/%s/links/lnk_missing/qrcode", campaignID), "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusNotFound)

	// link 不属于该 campaign(跨活动错配)→ 404
	_, otherLinkID, _ := seedActiveCampaign(t, f)
	status, _, _ = f.doRaw(t, "GET",
		fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode", campaignID, otherLinkID), "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusNotFound)
}

func TestQRDownloadSizeWhitelist(t *testing.T) {
	f := fixtureWithQR(t, nil)
	campaignID, linkID, _ := seedActiveCampaign(t, f)
	qrPath := fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode", campaignID, linkID)

	for _, size := range []string{"128", "256", "512", ""} {
		status, hdr, body := f.doRaw(t, "GET", qrPath+"?size="+size, "sess-owner-a", f.tenA, "")
		mustEqual(t, status, http.StatusOK)
		if ct := hdr.Get("Content-Type"); ct != "image/png" {
			t.Fatalf("size %q content-type = %q", size, ct)
		}
		if !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("size %q body not PNG", size)
		}
	}
	for _, bad := range []string{"100", "64", "1024", "0", "-1", "abc"} {
		status, _, body := f.do(t, "GET", qrPath+"?size="+bad, "sess-owner-a", f.tenA, "")
		if status != http.StatusBadRequest || body["error"] != "bad_size" {
			t.Fatalf("size %q = %d %v, want 400 bad_size", bad, status, body)
		}
	}
}

func TestQRDownloadRequiresPublicBaseURL(t *testing.T) {
	f := fixtureWithQR(t, nil)
	campaignID, linkID, _ := seedActiveCampaign(t, f)
	qrPath := fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode", campaignID, linkID)

	f.s.Cfg.PublicBaseURL = ""
	status, _, body := f.do(t, "GET", qrPath, "sess-owner-a", f.tenA, "")
	if status != http.StatusServiceUnavailable || body["error"] != "qr_not_configured" {
		t.Fatalf("no PUBLIC_BASE_URL = %d %v, want 503 qr_not_configured", status, body)
	}
}

func TestQRMetaPayload(t *testing.T) {
	f := fixtureWithQR(t, nil)
	campaignID, linkID, code := seedActiveCampaign(t, f)
	qrPath := fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode", campaignID, linkID)

	status, hdr, raw := f.doRaw(t, "GET", qrPath+"?format=json&size=512", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	// 载荷字段闭合:{url, code, size} —— 没有任何凭证/租户/主体/客户信息字段
	if len(payload) != 3 {
		t.Fatalf("payload keys = %v, want exactly url/code/size", payload)
	}
	wantURL := "https://h5.test/c/" + code
	if payload["url"] != wantURL || payload["code"] != code || payload["size"] != float64(512) {
		t.Fatalf("payload = %v, want url=%s code=%s size=512", payload, wantURL, code)
	}
	lower := strings.ToLower(wantURL)
	for _, banned := range []string{"token", "tenant", "usr_", "tnt_", "secret", "session", "phone"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("payload %q contains banned substring %q", wantURL, banned)
		}
	}
	if strings.ContainsAny(wantURL, "?&#@") {
		t.Fatalf("payload %q carries query/fragment/userinfo", wantURL)
	}
}

// TestForgedParamsStayGuests:HUI-1664 验收 —— 从二维码/URL 参数不派生任何后台权限。
func TestForgedParamsStayGuests(t *testing.T) {
	f := fixtureWithQR(t, nil)
	campaignID, _, code := seedActiveCampaign(t, f)
	publicPath := "/api/v1/public/links/" + code

	// 干净公共请求作为基线
	baseStatus, _, baseRaw := f.doRaw(t, "GET", publicPath, "", "", "")

	// 伪造一切"可能被解释为权限"的 query 参数:响应必须与干净请求逐字节一致
	forged := publicPath + "?token=secretvalue&session_token=sess-owner-a&session=sess-owner-a" +
		"&tenant=" + f.tenB + "&tenant_id=" + f.tenB + "&admin=1&role=owner&is_admin=true&principal=usr_owner_a&next=//evil"
	status, _, raw := f.doRaw(t, "GET", forged, "", "", "")
	if status != baseStatus || !bytes.Equal(raw, baseRaw) {
		t.Fatalf("forged params changed public response: %d %s, want %d %s", status, raw, baseStatus, baseRaw)
	}
	// 且公共载荷仍是游客白名单形状(state + available 五字段)
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil || view["state"] != "available" {
		t.Fatalf("forged public view = %v (%v)", view, err)
	}
	for key := range view {
		switch key {
		case "state", "title", "public_content", "starts_at", "ends_at", "merchant_name", "store_name", "store_notice", "capabilities":
		default:
			t.Fatalf("forged params derived extra field %q: %v", key, view)
		}
	}

	// 管理面:staff 带"提权 query"仍是 staff
	status, _, body := f.do(t, "GET", "/api/v1/admin/members?role=owner&admin=1", "sess-staff-a", f.tenA, "")
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("staff members with forged query = %d %v", status, body)
	}
	// campaign 写操作照旧按会话+租户头裁定,query 不参与(不受 ?status=ended 影响)
	status, _, body = f.do(t, "POST",
		fmt.Sprintf("/api/v1/campaigns/%s/status?status=ended&role=owner", campaignID),
		"sess-staff-a", f.tenA, `{"status":"paused"}`)
	if status != http.StatusOK {
		t.Fatalf("staff status change with forged query = %d %v", status, body)
	}
}

// TestForgedParamsNotLogged:请求日志只含 path,伪造参数值绝不入日志。
func TestForgedParamsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	f := fixtureWithQR(t, &buf)
	_, _, code := seedActiveCampaign(t, f)

	f.doRaw(t, "GET", "/api/v1/public/links/"+code+"?token=secretvalue&tenant=tnt_evil&next=//evil", "", "", "")
	f.do(t, "GET", "/api/v1/campaigns", "sess-owner-a", f.tenA, "")

	logged := buf.String()
	for _, banned := range []string{"secretvalue", "tnt_evil", "//evil"} {
		if strings.Contains(logged, banned) {
			t.Fatalf("log leaks forged param value %q:\n%s", banned, logged)
		}
	}
	if !strings.Contains(logged, "/api/v1/public/links/"+code) {
		t.Fatalf("log should carry the request path:\n%s", logged)
	}
}

// TestRedirectTargetNeverServerIssued:服务端不发任何 3xx 跳转;next/redirect
// 参数即使"合法"也不被服务端执行(跳转只可能是前端行为,且仅限站内相对路径)。
func TestRedirectTargetNeverServerIssued(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, _, code := seedActiveCampaign(t, f)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/public/links/"+code+"?next=/admin&redirect=//evil", nil)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	res, err := client.Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		t.Fatalf("server issued a redirect: %d %v", res.StatusCode, res.Header)
	}
	if loc := res.Header.Get("Location"); loc != "" {
		t.Fatalf("server set Location: %q", loc)
	}
}
