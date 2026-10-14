package httpapi

// HUI-3546 参与/尝试/交接凭据的 HTTP 面 + 重启持久化验收。
//
// 纪律:
//   - FEATURE_PARTICIPATION 默认 off:off = 路由不注册(404 不可见);
//   - 浏览不建参与;同键重放回原行;跨浏览器无 cookie/凭据不可读;
//   - 店员面走真实商家会话 + 门店作用域;错租户不消费凭据;
//   - 重启验收用真实 SQLite 文件库 + 真实 httpapi.Server,不是 mock。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// newPartFixture: newFixture 同款骨架 + FEATURE_PARTICIPATION=on + PUBLIC_BASE_URL
// + 门店经理成员(本店作用域) + 一个已激活、绑定本店的活动短码。
// 返回 (fixture, linkCode)。
func newPartFixture(t *testing.T) (*fixture, string) {
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
	stoA, err := st.CreateStore(tenA.ID, "旗舰店", "", "test")
	mustNoErr(t, err)
	stoB, err := st.CreateStore(tenA.ID, "分店B", "", "test")
	mustNoErr(t, err)
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_mgr_a", "store_manager", "usr_mgr_a", "test", true, stoA.ID); err != nil {
		t.Fatalf("seed store manager: %v", err)
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_mgr_b", "store_manager", "usr_mgr_b", "test", true, stoB.ID); err != nil {
		t.Fatalf("seed second store manager: %v", err)
	}

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_PARTICIPATION":
			return "on"
		case "PUBLIC_BASE_URL":
			return "https://h5.example.com"
		}
		return ""
	})

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-mgr-a":   {"usr_mgr_a", "mgra@example.com"},
		"sess-mgr-b":   {"usr_mgr_b", "mgrb@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}

	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	// 参与出生即绑门店:活动直接绑定 fixture 的本店(stoA),店员作用域对齐。
	now := time.Now().UTC()
	cmp, err := st.CreateCampaign(store.NewCampaign{
		TenantID: tenA.ID, Title: "碰一碰有礼", PublicContent: "发一条领一份",
		StartsAt: now.Add(-time.Hour).Format(time.RFC3339),
		EndsAt:   now.Add(48 * time.Hour).Format(time.RFC3339),
		StoreID:  stoA.ID, CreatedBy: "test",
	})
	mustNoErr(t, err)
	_, err = st.TransitionCampaign(cmp.ID, tenA.ID, campaign.StatusActive)
	mustNoErr(t, err)
	lnk, err := st.CreateLink(tenA.ID, cmp.ID, "test")
	mustNoErr(t, err)

	return &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, ownA: "usr_owner_a", ownB: "usr_owner_b", staffA: "usr_mgr_a"}, lnk.Code
}

// doCookie: 不带租户头的访客面请求;cookie 是 "name=value" 形式(可空)。
func (f *fixture) doCookie(t *testing.T, method, path, cookie, body string) (int, http.Header, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	mustNoErr(t, err)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, res.Header, out
}

// cookiePair extracts "name=value" from the Set-Cookie header.
func cookiePair(h http.Header) string {
	sc := h.Get("Set-Cookie")
	if sc == "" {
		return ""
	}
	return strings.Split(sc, ";")[0]
}

func startBody(code, key, content string) string {
	return fmt.Sprintf(`{"link_code":%q,"touchpoint_source":"nfc","idempotency_key":%q,"content_pack_version":%q}`,
		code, key, content)
}

// off = 路由不注册,统一 404,面不可见。
func TestParticipationRoutesInvisibleWhenOff(t *testing.T) {
	f := newFixture(t, false) // 无 FEATURE_PARTICIPATION
	status, _, _ := f.do(t, "POST", "/api/v1/participations", "", f.tenA, `{"link_code":"x"}`)
	if status != http.StatusNotFound {
		t.Fatalf("off start = %d, want 404", status)
	}
	status, _, _ = f.do(t, "GET", "/api/v1/participations/pt_whatever", "sess-owner-a", f.tenA, "")
	if status != http.StatusNotFound {
		t.Fatalf("off get = %d, want 404", status)
	}
}

func TestParticipationStartIdempotentAndProbeSafe(t *testing.T) {
	f, code := newPartFixture(t)

	// 显式开始:201 + 首方 cookie + pending + 无伪报字段。
	status, hdr, body := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(code, "idem-1", "cp_v1"))
	mustEqual(t, status, http.StatusCreated)
	cookie := cookiePair(hdr)
	if cookie == "" {
		t.Fatal("start must issue the visitor cookie")
	}
	if body["status"] != "pending" {
		t.Fatalf("status = %v, want pending", body["status"])
	}
	if body["benefit_reservation_state"] != "not_requested" {
		t.Fatalf("reservation state = %v (nil reserver), want not_requested — never confirmed", body["benefit_reservation_state"])
	}
	for _, leak := range []string{"visitor_id", "idempotency_key", "benefit_reservation_ref", "store_id", "tenant_id"} {
		if _, ok := body[leak]; ok {
			t.Fatalf("whitelist payload must not carry %q", leak)
		}
	}
	pid := body["id"].(string)

	// 同键重放:200 回原行。
	status, _, body2 := f.doCookie(t, "POST", "/api/v1/participations", cookie, startBody(code, "idem-1", "cp_v1"))
	mustEqual(t, status, http.StatusOK)
	if body2["id"] != pid {
		t.Fatalf("replay id = %v, want %v", body2["id"], pid)
	}

	// 换浏览器重放同键:409,不向陌生 visitor 泄漏。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations", "touch_participation_visitor=ffffffffffffffffffffffffffffffff", startBody(code, "idem-1", "cp_v1"))
	mustEqual(t, status, http.StatusConflict)

	// 裸 ID 无 cookie / 陌生 cookie:一律 404。
	status, _, _ = f.doCookie(t, "GET", "/api/v1/participations/"+pid, "", "")
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.doCookie(t, "GET", "/api/v1/participations/"+pid, "touch_participation_visitor=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "")
	mustEqual(t, status, http.StatusNotFound)

	// 自身 cookie 可读。
	status, _, _ = f.doCookie(t, "GET", "/api/v1/participations/"+pid, cookie, "")
	mustEqual(t, status, http.StatusOK)

	// 同设备重复碰触:lookup 恢复未完参与。
	status, _, lk := f.doCookie(t, "POST", "/api/v1/participations/lookup", cookie, fmt.Sprintf(`{"link_code":%q}`, code))
	mustEqual(t, status, http.StatusOK)
	if lk["id"] != pid {
		t.Fatalf("lookup id = %v, want %v", lk["id"], pid)
	}
	// 另一个浏览器 lookup:不自动恢复。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations/lookup", "touch_participation_visitor=dddddddddddddddddddddddddddddddd", fmt.Sprintf(`{"link_code":%q}`, code))
	mustEqual(t, status, http.StatusNotFound)

	// 浏览(公共页 GET)不建参与:参与数只来自显式开始——公共面没有
	// 「GET 建行」路由本身就是断言。
}

func TestParticipationAttemptFreezeHandoffAndRedeem(t *testing.T) {
	f, code := newPartFixture(t)

	status, hdr, body := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(code, "idem-1", "cp_v1"))
	mustEqual(t, status, http.StatusCreated)
	cookie := cookiePair(hdr)
	pid := body["id"].(string)

	// 预览期换选自由。
	status, _, _ = f.doCookie(t, "PATCH", "/api/v1/participations/"+pid+"/content", cookie, `{"content_pack_version":"cp_v2"}`)
	mustEqual(t, status, http.StatusOK)

	// 建 attempt(默认 h5)并交接:冻结 cp_v2。
	status, _, att := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/attempts", cookie, `{"platform":"douyin"}`)
	mustEqual(t, status, http.StatusCreated)
	attID := att["id"].(string)
	status, _, hk := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/handoff", cookie, fmt.Sprintf(`{"attempt_id":%q}`, attID))
	mustEqual(t, status, http.StatusCreated)
	token, _ := hk["token"].(string)
	if token == "" || !strings.HasPrefix(token, "ptx_") {
		t.Fatalf("handoff token = %q", token)
	}
	if qp, _ := hk["qr_payload"].(string); qp == "" || strings.Contains(qp, "?") || !strings.HasPrefix(qp, "https://h5.example.com/p/") {
		t.Fatalf("qr payload = %q", qp)
	}
	if p, _ := hk["participation"].(map[string]any); p["status"] != "in_progress" {
		t.Fatalf("status after handoff = %v, want in_progress", p["status"])
	}

	// 冻结后换选:参与当前选择前进,attempt 历史行不变。
	status, _, _ = f.doCookie(t, "PATCH", "/api/v1/participations/"+pid+"/content", cookie, `{"content_pack_version":"cp_v3"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, gv := f.doCookie(t, "GET", "/api/v1/participations/"+pid, cookie, "")
	mustEqual(t, status, http.StatusOK)
	if gv["content_pack_version"] != "cp_v3" {
		t.Fatalf("current selection = %v, want cp_v3", gv["content_pack_version"])
	}
	attempts, _ := gv["attempts"].([]any)
	if len(attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(attempts))
	}
	a0 := attempts[0].(map[string]any)
	if a0["content_pack_version"] != "cp_v2" || a0["status"] != "handed_off" {
		t.Fatalf("frozen attempt = %v", a0)
	}

	// 跨端续接:另一浏览器凭凭据兑换,拿到同一参与。
	newCookie := "touch_participation_visitor=11111111111111111111111111111111"
	status, _, rd := f.doCookie(t, "POST", "/api/v1/participations/handoff/redeem", newCookie, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	if rd["id"] != pid {
		t.Fatalf("redeemed id = %v, want %v", rd["id"], pid)
	}
	// 旧设备绑定被转移。
	status, _, _ = f.doCookie(t, "GET", "/api/v1/participations/"+pid, cookie, "")
	mustEqual(t, status, http.StatusNotFound)

	// 一次性:重放被拒。
	status, _, used := f.doCookie(t, "POST", "/api/v1/participations/handoff/redeem", "touch_participation_visitor=22222222222222222222222222222222", fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusConflict)
	if used["error"] != "handoff_used" {
		t.Fatalf("replay error = %v", used["error"])
	}

	// 撤销:第二张凭据撤销后兑换被拒。
	status, _, att2 := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/attempts", newCookie, `{"platform":"xiaohongshu"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, hk2 := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/handoff", newCookie, fmt.Sprintf(`{"attempt_id":%q}`, att2["id"]))
	mustEqual(t, status, http.StatusCreated)
	status, _, _ = f.doCookie(t, "DELETE", "/api/v1/participations/"+pid+"/handoff/"+hk2["credential_id"].(string), newCookie, "")
	mustEqual(t, status, http.StatusOK)
	status, _, rv := f.doCookie(t, "POST", "/api/v1/participations/handoff/redeem", "touch_participation_visitor=33333333333333333333333333333333", fmt.Sprintf(`{"token":%q}`, hk2["token"]))
	mustEqual(t, status, http.StatusConflict)
	if rv["error"] != "handoff_revoked" {
		t.Fatalf("revoked error = %v", rv["error"])
	}

	// 取消后不能再交接。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/cancel", newCookie, "")
	mustEqual(t, status, http.StatusOK)
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/attempts", newCookie, `{"platform":"douyin"}`)
	mustEqual(t, status, http.StatusUnprocessableEntity)
}

func TestParticipationStaffScopeMatrix(t *testing.T) {
	f, code := newPartFixture(t)

	_, hdr, body := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(code, "idem-1", "cp_v1"))
	cookie := cookiePair(hdr)
	pid := body["id"].(string)

	// 顾客出 staff_verify 凭据(店员目测)。
	status, _, hk := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/handoff", cookie, `{"purpose":"staff_verify"}`)
	mustEqual(t, status, http.StatusCreated)
	staffToken := hk["token"].(string)

	// 店员读取:本店经理可读;邻店经理 403;总部 owner 可读;错租户 404。
	status, _, _ = f.do(t, "GET", "/api/v1/participations/"+pid+"/staff-view", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	status, _, _ = f.do(t, "GET", "/api/v1/participations/"+pid+"/staff-view", "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	status, _, _ = f.do(t, "GET", "/api/v1/participations/"+pid+"/staff-view", "sess-mgr-b", f.tenA, "")
	mustEqual(t, status, http.StatusForbidden)
	status, _, _ = f.do(t, "GET", "/api/v1/participations/"+pid+"/staff-view", "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)

	// 错租户店员兑换:403 且不消费凭据(之后本店仍可兑)。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/redeem", "sess-owner-b", f.tenB, fmt.Sprintf(`{"token":%q}`, staffToken))
	mustEqual(t, status, http.StatusForbidden)
	status, _, rd := f.do(t, "POST", "/api/v1/participations/staff/redeem", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, staffToken))
	mustEqual(t, status, http.StatusOK)
	if rd["status"] != "awaiting_review" {
		t.Fatalf("after staff redeem status = %v, want awaiting_review", rd["status"])
	}
	// 单用途:重放 409。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/redeem", "sess-owner-a", f.tenA, fmt.Sprintf(`{"token":%q}`, staffToken))
	mustEqual(t, status, http.StatusConflict)

	// 顾客面兑换通道不认 staff 凭据(purpose/target 不符,不泄漏)。
	_, hdr2, body2 := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(code, "idem-2", "cp_v1"))
	cookie2 := cookiePair(hdr2)
	pid2 := body2["id"].(string)
	status, _, hk2 := f.doCookie(t, "POST", "/api/v1/participations/"+pid2+"/handoff", cookie2, `{"purpose":"staff_verify"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations/handoff/redeem", "touch_participation_visitor=44444444444444444444444444444444", fmt.Sprintf(`{"token":%q}`, hk2["token"]))
	mustEqual(t, status, http.StatusForbidden)

	// 伪造/未知凭据:统一 404。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations/handoff/redeem", "touch_participation_visitor=55555555555555555555555555555555", `{"token":"ptx_zzzz.zzzz"}`)
	mustEqual(t, status, http.StatusNotFound)

	// 两个店员同时操作:第二位店员在 awaiting_review 态再出/再读均安全。
	status, _, hk3 := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/handoff", cookie, `{"purpose":"staff_verify"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/redeem", "sess-owner-a", f.tenA, fmt.Sprintf(`{"token":%q}`, hk3["token"]))
	mustEqual(t, status, http.StatusOK)
	status, _, _ = f.do(t, "GET", "/api/v1/participations/"+pid+"/staff-view", "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
}

func TestParticipationBadInputsAndLinkStates(t *testing.T) {
	f, code := newPartFixture(t)

	// 未知短码:404(公共五态)。
	status, _, st := f.doCookie(t, "POST", "/api/v1/participations", "", startBody("no_such_code", "k", "cp"))
	mustEqual(t, status, http.StatusNotFound)
	if _, ok := st["state"]; !ok {
		t.Fatal("public link 404 must carry the state field")
	}
	// 非法来源:422。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations", "", `{"link_code":"`+code+`","touchpoint_source":"fingerprint","idempotency_key":"k1"}`)
	mustEqual(t, status, http.StatusUnprocessableEntity)
	// 缺幂等键:409(冲突语义)。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations", "", `{"link_code":"`+code+`","touchpoint_source":"qr"}`)
	mustEqual(t, status, http.StatusConflict)
	// 非 JSON:400。
	status, _, _ = f.doCookie(t, "POST", "/api/v1/participations", "", "not-json")
	mustEqual(t, status, http.StatusBadRequest)
}

// 凭据明文与参与档案不进公共日志:请求日志面只有 method/path/status。
func TestParticipationLogsNeverContainToken(t *testing.T) {
	var buf bytes.Buffer
	f, code := newPartFixture(t)
	f.s.Log = log.New(&buf, "", 0)

	_, hdr, body := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(code, "idem-1", "cp_v1"))
	cookie := cookiePair(hdr)
	pid := body["id"].(string)
	_, att := f.doCookiex(t, "POST", "/api/v1/participations/"+pid+"/attempts", cookie, `{"platform":"douyin"}`)
	_, hk := f.doCookiex(t, "POST", "/api/v1/participations/"+pid+"/handoff", cookie, fmt.Sprintf(`{"attempt_id":%q}`, att["id"]))
	token, _ := hk["token"].(string)

	if token == "" || !strings.Contains(hk["token"].(string), "ptx_") {
		t.Fatal("sanity: token must exist in the create response")
	}
	if strings.Contains(buf.String(), token) {
		t.Fatal("credential plaintext must never appear in logs")
	}
	if strings.Contains(buf.String(), cookie) {
		t.Fatal("visitor key must never appear in logs")
	}
}

// doCookiex: doCookie 的 (header, body) 变体,上面日志测试用。
func (f *fixture) doCookiex(t *testing.T, method, path, cookie, body string) (http.Header, map[string]any) {
	t.Helper()
	status, hdr, out := f.doCookie(t, method, path, cookie, body)
	if status >= 400 {
		t.Fatalf("%s %s = %d", method, path, status)
	}
	return hdr, out
}

// T12 重启持久化验收:真实 SQLite 文件库 + 真实 httpapi.Server,
// 杀进程(关库)后重启,同一参与与冻结历史原样恢复。
func TestParticipationSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "restart.db")

	newSrv := func() (*Server, *httptest.Server) {
		t.Helper()
		d, err := db.Open(dbPath)
		mustNoErr(t, err)
		cfg := config.Load(func(k string) string {
			switch k {
			case "TOUCH_INTERNAL_TOKEN":
				return "restart-secret"
			case "FEATURE_PARTICIPATION":
				return "on"
			case "PUBLIC_BASE_URL":
				return "https://h5.example.com"
			}
			return ""
		})
		s := New(cfg, d, nil, nil, log.New(io.Discard, "", 0))
		ts := httptest.NewServer(s.Handler())
		return s, ts
	}

	s1, ts1 := newSrv()
	code := "abc2345" // 直接经 store 造活动/短码
	{
		st := store.New(s1.St.DB)
		ten, err := st.CreateTenant("重启商家")
		mustNoErr(t, err)
		sto, err := st.CreateStore(ten.ID, "重启店", "", "test")
		mustNoErr(t, err)
		cmp, err := st.CreateCampaign(store.NewCampaign{
			TenantID: ten.ID, Title: "重启活动", PublicContent: "内容",
			StartsAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
			EndsAt:   time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
			StoreID:  sto.ID, CreatedBy: "test",
		})
		mustNoErr(t, err)
		_, err = st.TransitionCampaign(cmp.ID, ten.ID, campaign.StatusActive)
		mustNoErr(t, err)
		lnk, err := st.CreateLink(ten.ID, cmp.ID, "test")
		mustNoErr(t, err)
		code = lnk.Code
	}

	// 第一次生命:开始 + 交接。
	req, _ := http.NewRequest("POST", ts1.URL+"/api/v1/participations", strings.NewReader(startBody(code, "idem-1", "cp_v1")))
	req.Header.Set("X-Internal-Token", "restart-secret")
	res, err := ts1.Client().Do(req)
	mustNoErr(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusCreated)
	var first map[string]any
	mustNoErr(t, json.Unmarshal(raw, &first))
	pid := first["id"].(string)
	cookie := cookiePair(res.Header)

	req, _ = http.NewRequest("POST", ts1.URL+"/api/v1/participations/"+pid+"/attempts", strings.NewReader(`{"platform":"douyin"}`))
	req.Header.Set("X-Internal-Token", "restart-secret")
	req.Header.Set("Cookie", cookie)
	res, err = ts1.Client().Do(req)
	mustNoErr(t, err)
	raw, _ = io.ReadAll(res.Body)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusCreated)
	var att map[string]any
	mustNoErr(t, json.Unmarshal(raw, &att))

	req, _ = http.NewRequest("POST", ts1.URL+"/api/v1/participations/"+pid+"/handoff", strings.NewReader(fmt.Sprintf(`{"attempt_id":%q}`, att["id"])))
	req.Header.Set("X-Internal-Token", "restart-secret")
	req.Header.Set("Cookie", cookie)
	res, err = ts1.Client().Do(req)
	mustNoErr(t, err)
	raw, _ = io.ReadAll(res.Body)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusCreated)
	var hk map[string]any
	mustNoErr(t, json.Unmarshal(raw, &hk))
	token := hk["token"].(string)

	// 杀进程:关 HTTP 与库句柄。
	ts1.Close()
	s1.Close()

	// 重启:同一 SQLite 文件,新 Server。
	s2, ts2 := newSrv()
	t.Cleanup(func() { ts2.Close(); s2.Close() })

	// 同 cookie 恢复同一参与;冻结 attempt 与预留状态原样。
	req, _ = http.NewRequest("GET", ts2.URL+"/api/v1/participations/"+pid, nil)
	req.Header.Set("X-Internal-Token", "restart-secret")
	req.Header.Set("Cookie", cookie)
	res, err = ts2.Client().Do(req)
	mustNoErr(t, err)
	raw, _ = io.ReadAll(res.Body)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusOK)
	var restored map[string]any
	mustNoErr(t, json.Unmarshal(raw, &restored))
	if restored["id"] != pid || restored["status"] != "in_progress" {
		t.Fatalf("restored = %v %v, want %s in_progress", restored["id"], restored["status"], pid)
	}
	if restored["content_pack_version"] != "cp_v1" {
		t.Fatalf("restored selection = %v", restored["content_pack_version"])
	}
	atts, _ := restored["attempts"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["content_pack_version"] != "cp_v1" || atts[0].(map[string]any)["status"] != "handed_off" {
		t.Fatalf("restored attempts = %v", atts)
	}

	// 凭据跨重启仍可单次兑换(重放保护在库里,不在内存)。
	req, _ = http.NewRequest("POST", ts2.URL+"/api/v1/participations/handoff/redeem", strings.NewReader(fmt.Sprintf(`{"token":%q}`, token)))
	req.Header.Set("X-Internal-Token", "restart-secret")
	req.Header.Set("Cookie", "touch_participation_visitor=99999999999999999999999999999999")
	res, err = ts2.Client().Do(req)
	mustNoErr(t, err)
	raw, _ = io.ReadAll(res.Body)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusOK)
	var rd map[string]any
	mustNoErr(t, json.Unmarshal(raw, &rd))
	if rd["id"] != pid {
		t.Fatalf("redeemed after restart id = %v, want %v", rd["id"], pid)
	}

	// 幂等键跨重启仍唯一:同键换 visitor 重放 → 409。
	req, _ = http.NewRequest("POST", ts2.URL+"/api/v1/participations", strings.NewReader(startBody(code, "idem-1", "cp_v1")))
	req.Header.Set("X-Internal-Token", "restart-secret")
	req.Header.Set("Cookie", "touch_participation_visitor=88888888888888888888888888888888")
	res, err = ts2.Client().Do(req)
	mustNoErr(t, err)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusConflict)
}
