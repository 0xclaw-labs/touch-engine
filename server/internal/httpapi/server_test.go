package httpapi

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
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// ---- test harness -------------------------------------------------------------

type fixture struct {
	s                  *Server
	ts                 *httptest.Server
	tenA, tenB         string
	ownA, ownB, staffA string // principal refs
}

// fakeIdentity mimics platform-identity: fixed sessions per token.
func fakeIdentity(t *testing.T, sessions map[string]identitySession) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/identity/session/resolve":
			var in struct {
				SessionToken string `json:"session_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			sess, ok := sessions[in.SessionToken]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"authenticated":false}`))
				return
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"authenticated":true,"app_id":"touch-engine","session":{"principal_id":%q,"email":%q}}`, sess.Principal, sess.Email)))
		case "/internal/v1/identity/login":
			var in struct {
				Email    string `json:"email"`
				Password string `json:"password"`
				AppID    string `json:"app_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.AppID != "touch-engine" || in.Password == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at-login","refresh_token":"rt-login","token_type":"bearer","expires_in":3600}`))
		case "/internal/v1/identity/refresh":
			_, _ = w.Write([]byte(`{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`))
		case "/internal/v1/identity/revocations":
			_, _ = w.Write([]byte(`{}`))
		case "/internal/v1/identity/shell/launch":
			var in struct {
				PrincipalID string `json:"principal_id"`
				TenantID    string `json:"tenant_id"`
				AppID       string `json:"app_id"`
				TargetID    string `json:"target_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.PrincipalID == "" || in.TenantID == "" || in.AppID != "goboost" || in.TargetID != "ti-goboost-web" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"shell_launch_denied"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"app_id":"goboost","target_id":"ti-goboost-web","launch_url":"https://goboost.modelxing.com/projects?code=one-time"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type identitySession struct {
	Principal string
	Email     string
}

// fakeUpload mimics platform-upload GET /internal/v1/upload/assets/{id}.
func fakeUpload(t *testing.T, assets map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/internal/v1/upload/assets/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/internal/v1/upload/assets/")
		sha, ok := assets[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"upload_not_found"}}`))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"asset_id":%q,"app_id":"touch-engine","status":"ready","filename":"m.mp4","content_type":"video/mp4","size_bytes":1,"sha256":%q}`, id, sha)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newFixture(t *testing.T, featureUpload bool) *fixture {
	t.Helper()
	return newFixtureEnv(t, featureUpload, nil)
}

// newFixtureEnv is newFixture with extra config env (HUI-2222: feature flags
// such as FEATURE_SERVICE_DESK). Nil extras keep the original behavior.
func newFixtureEnv(t *testing.T, featureUpload bool, extraEnv map[string]string) *fixture {
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
	for _, m := range []struct {
		tenant, principal, role string
	}{
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
		case "PLATFORM_UPLOAD_BASE_URL":
			return "http://upload.test"
		case "PLATFORM_UPLOAD_TOKEN":
			return "upload-token"
		case "FEATURE_UPLOAD":
			if featureUpload {
				return "on"
			}
			return "off"
		}
		if v, ok := extraEnv[k]; ok {
			return v
		}
		return ""
	})

	// clients pointed at the fakes
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	upsrv := fakeUpload(t, map[string]string{
		"ast_good": "sha-aaa",
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upsrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upsrv.Client()}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID,
		ownA: "usr_owner_a", ownB: "usr_owner_b", staffA: "usr_staff_a"}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- request helpers -----------------------------------------------------------

func (f *fixture) do(t *testing.T, method, path, session, tenant, body string) (int, http.Header, map[string]any) {
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
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, res.Header, out
}

// ---- identity discipline --------------------------------------------------------

func TestNoSessionIs401(t *testing.T) {
	f := newFixture(t, true)
	// GET on session-required paths: guests get 401
	for _, p := range []string{
		"/api/v1/whoami", "/api/v1/stores", "/api/v1/campaigns",
		"/api/v1/admin/members",
	} {
		status, _, _ := f.do(t, "GET", p, "", f.tenA, "")
		if status != http.StatusUnauthorized {
			t.Fatalf("guest GET %s = %d, want 401", p, status)
		}
	}
	// POST on mutating paths: guests get 401 (whoami has no POST route; the
	// router's 405 equally refuses the guest)
	for _, p := range []string{"/api/v1/stores", "/api/v1/campaigns", "/api/v1/admin/members"} {
		status, _, _ := f.do(t, "POST", p, "", f.tenA, "{}")
		if status != http.StatusUnauthorized {
			t.Fatalf("guest POST %s = %d, want 401", p, status)
		}
	}
}

func TestWrongInternalTokenIs401(t *testing.T) {
	f := newFixture(t, true)
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/whoami", nil)
	req.Header.Set("X-Internal-Token", "forged")
	req.Header.Set("X-Session-Token", "sess-owner-a")
	req.Header.Set("X-Tenant-ID", f.tenA)
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged internal token = %d, want 401", res.StatusCode)
	}
}

func TestForgedTenantHeaderIsBare403(t *testing.T) {
	f := newFixture(t, true)
	// owner A claims tenant B: same bare refusal as no membership at all
	status, _, body := f.do(t, "GET", "/api/v1/campaigns", "sess-owner-a", f.tenB, "")
	if status != http.StatusForbidden || body["error"] != "not_member" {
		t.Fatalf("forged tenant = %d %v, want 403 not_member", status, body)
	}
	// unknown tenant id: identical shape (no existence leak)
	status, _, body2 := f.do(t, "GET", "/api/v1/campaigns", "sess-owner-a", "tnt_unknown", "")
	if status != http.StatusForbidden || body2["error"] != "not_member" {
		t.Fatalf("unknown tenant = %d %v, want 403 not_member", status, body2)
	}
}

func TestMemberIdentityDiscipline(t *testing.T) {
	f := newFixture(t, true)
	// staff cannot manage members
	status, _, body := f.do(t, "GET", "/api/v1/admin/members", "sess-staff-a", f.tenA, "")
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("staff manage members = %d %v", status, body)
	}
	// owner cannot create member from an email (identity discipline)
	status, _, body = f.do(t, "POST", "/api/v1/admin/members", "sess-owner-a", f.tenA,
		`{"principal_ref":"new@example.com","role":"staff"}`)
	if status != http.StatusBadRequest || body["error"] != "bad_principal_ref" {
		t.Fatalf("email member = %d %v, want 400 bad_principal_ref", status, body)
	}
	// owner CAN add an existing usr_* principal
	status, _, _ = f.do(t, "POST", "/api/v1/admin/members", "sess-owner-a", f.tenA,
		`{"principal_ref":"usr_new_staff","role":"staff","display_name":"新员工"}`)
	if status != http.StatusCreated {
		t.Fatalf("usr member create = %d", status)
	}
	// principal_ref is immutable on PATCH
	status, _, body = f.do(t, "PATCH", "/api/v1/admin/members/mem_x", "sess-owner-a", f.tenA,
		`{"principal_ref":"usr_other"}`)
	if status != http.StatusBadRequest || body["error"] != "principal_immutable" {
		t.Fatalf("patch principal_ref = %d %v", status, body)
	}
}

// ---- admin×staff×tenant matrix over campaigns ------------------------------------

func TestAdminMatrixOverCampaigns(t *testing.T) {
	f := newFixture(t, true)
	// owner A creates a store + campaign
	status, _, sto := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA,
		`{"name":"A门店","address":"A地址"}`)
	if status != http.StatusCreated {
		t.Fatalf("store create = %d %v", status, sto)
	}
	storeID := sto["id"].(string)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-staff-a", f.tenA,
		fmt.Sprintf(`{"title":"周年庆","public_content":"到店礼","store_id":%q}`, storeID))
	if status != http.StatusCreated {
		t.Fatalf("staff campaign create = %d %v", status, cmp)
	}
	campaignID := cmp["id"].(string)
	if cmp["status"] != "draft" {
		t.Fatalf("new campaign status = %v", cmp["status"])
	}

	// staff enables it (draft -> active), pauses, resumes
	for _, step := range []struct {
		want   int
		status string
	}{
		{200, "active"}, {200, "paused"}, {200, "active"},
	} {
		st, _, body := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-staff-a", f.tenA,
			fmt.Sprintf(`{"status":%q}`, step.status))
		if st != step.want || body["status"] != step.status {
			t.Fatalf("staff transition %s = %d %v", step.status, st, body)
		}
	}
	// illegal transition draft-ish: paused campaign -> draft is impossible; use ended->active later
	// owner B: every path to A's records under B's own scope resolves 404; the
	// forged-header shortcut is a bare 403 (see TestForgedTenantHeaderIsBare403)
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"read A campaign under B", "GET", "/api/v1/campaigns/" + campaignID, ""},
		{"pause A campaign under B", "POST", "/api/v1/campaigns/" + campaignID + "/status", `{"status":"paused"}`},
		{"patch A campaign under B", "PATCH", "/api/v1/campaigns/" + campaignID, `{"title":"夺舍"}`},
		{"list A links under B", "GET", "/api/v1/campaigns/" + campaignID + "/links", ""},
		{"add asset to A campaign under B", "POST", "/api/v1/campaigns/" + campaignID + "/assets", `{"asset_id":"ast_good"}`},
	} {
		st, _, _ := f.do(t, tc.method, tc.path, "sess-owner-b", f.tenB, tc.body)
		if st != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404 (A's record invisible in B's scope)", tc.name, st)
		}
	}
	// B's own campaign list is 200 and contains ONLY B's campaigns
	st, _, list := f.do(t, "GET", "/api/v1/campaigns", "sess-owner-b", f.tenB, "")
	mustEqual(t, st, http.StatusOK)
	items, _ := list["items"].([]any)
	for _, it := range items {
		if it.(map[string]any)["id"] == campaignID {
			t.Fatal("B's campaign list leaked A's campaign")
		}
	}
	// B cannot stop A's link
	st, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/links/lnk_whatever/enabled", "sess-owner-b", f.tenB, `{"enabled":false}`)
	mustEqual(t, st, http.StatusNotFound)
}

// ---- assets: upload validation matrix ---------------------------------------------

func TestAssetAddRequiresUploadFeature(t *testing.T) {
	f := newFixture(t, false) // FEATURE_UPLOAD off (default)
	status, _, body := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"t"}`)
	mustEqual(t, status, http.StatusCreated)
	cid := body["id"].(string)

	status, _, body = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/assets", "sess-owner-a", f.tenA,
		`{"asset_id":"ast_good"}`)
	if status != http.StatusServiceUnavailable || body["error"] != "upload_disabled" {
		t.Fatalf("asset add with upload off = %d %v, want 503 upload_disabled", status, body)
	}
}

func TestAssetAddValidationMatrix(t *testing.T) {
	f := newFixture(t, true)
	_, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"t"}`)
	cid := cmp["id"].(string)

	// success: version anchored to platform sha256
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+cid+"/assets", "sess-owner-a", f.tenA,
		`{"asset_id":"ast_good"}`)
	mustEqual(t, status, http.StatusCreated)
	if body["version"] != "sha-aaa" || body["asset_id"] != "ast_good" {
		t.Fatalf("asset ref = %v, want version sha-aaa anchored", body)
	}
	// duplicate version refused
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/assets", "sess-owner-a", f.tenA,
		`{"asset_id":"ast_good"}`)
	mustEqual(t, status, http.StatusConflict)
	// caller-supplied mismatched version refused
	status, _, body = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/assets", "sess-owner-a", f.tenA,
		`{"asset_id":"ast_good","version":"sha-other"}`)
	mustEqual(t, status, http.StatusConflict)
	if body["error"] != "version_mismatch" {
		t.Fatalf("mismatch = %v", body)
	}
	// unknown asset -> 404 upload_not_found
	status, _, body = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/assets", "sess-owner-a", f.tenA,
		`{"asset_id":"ast_missing"}`)
	mustEqual(t, status, http.StatusNotFound)
	if body["error"] != "upload_not_found" {
		t.Fatalf("missing asset = %v", body)
	}
	// guest asset add -> 401 (never reaches upload)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+cid+"/assets", "", f.tenA, `{"asset_id":"ast_good"}`)
	mustEqual(t, status, http.StatusUnauthorized)
}

// ---- public route: five states + whitelist payload ---------------------------------

func TestPublicRouteFiveStates(t *testing.T) {
	f := newFixture(t, true)

	// seed campaigns in different states via admin API
	mk := func(title, content, starts, ends string) (string, string) {
		payload := fmt.Sprintf(`{"title":%q,"public_content":%q`, title, content)
		if starts != "" {
			payload += fmt.Sprintf(`,"starts_at":%q`, starts)
		}
		if ends != "" {
			payload += fmt.Sprintf(`,"ends_at":%q`, ends)
		}
		payload += `}`
		status, _, body := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, payload)
		mustEqual(t, status, http.StatusCreated)
		id := body["id"].(string)
		status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
		mustEqual(t, status, http.StatusCreated)
		return id, link["code"].(string)
	}
	toStatus := func(id, s string) {
		st, _, _ := f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA,
			fmt.Sprintf(`{"status":%q}`, s))
		mustEqual(t, st, http.StatusOK)
	}

	// valid: active, window containing now（相对 now 计算，夹具不再随日历过期）
	now := time.Now().UTC()
	validID, validCode := mk("周年庆", "到店有礼", now.Add(-48*time.Hour).Format(time.RFC3339), now.Add(48*time.Hour).Format(time.RFC3339))
	toStatus(validID, "active")

	// paused
	pausedID, pausedCode := mk("暂停活动", "", "", "")
	toStatus(pausedID, "active")
	toStatus(pausedID, "paused")

	// expired
	expiredID, expiredCode := mk("过期活动", "", now.Add(-96*time.Hour).Format(time.RFC3339), now.Add(-1*time.Hour).Format(time.RFC3339))
	toStatus(expiredID, "active")

	// disabled link on an otherwise-valid campaign
	dID, disabledCode := mk("停用链接", "", "", "")
	toStatus(dID, "active")
	_, _, links := f.do(t, "GET", "/api/v1/campaigns/"+dID+"/links", "sess-owner-a", f.tenA, "")
	linkID := links["items"].([]any)[0].(map[string]any)["id"].(string)
	st, _, _ := f.do(t, "POST", fmt.Sprintf("/api/v1/campaigns/%s/links/%s/enabled", dID, linkID), "sess-owner-a", f.tenA, `{"enabled":false}`)
	mustEqual(t, st, http.StatusOK)

	// not_found
	unknownCode := "ZZZZZZZZZZZZ"

	type tc struct {
		name     string
		code     string
		wantHTTP int
		wantSt   string
	}
	cases := []tc{
		{"有效", validCode, 200, "available"},
		{"停用", disabledCode, 404, "link_disabled"},
		{"过期", expiredCode, 404, "expired"},
		{"暂停", pausedCode, 404, "paused"},
		{"不存在", unknownCode, 404, "not_found"},
	}
	for _, c := range cases {
		status, _, body := f.do(t, "GET", "/api/v1/public/links/"+c.code, "", "", "")
		if status != c.wantHTTP {
			t.Fatalf("%s: HTTP %d, want %d (%v)", c.name, status, c.wantHTTP, body)
		}
		if body["state"] != c.wantSt {
			t.Fatalf("%s: state %v, want %q", c.name, body["state"], c.wantSt)
		}
	}

	// the valid payload exposes ONLY whitelist fields
	status, _, valid := f.do(t, "GET", "/api/v1/public/links/"+validCode, "", "", "")
	mustEqual(t, status, 200)
	for key := range valid {
		switch key {
		case "state", "title", "public_content", "starts_at", "ends_at", "merchant_name", "store_name", "store_notice", "capabilities":
		default:
			t.Fatalf("public payload has non-whitelist key %q: %v", key, valid)
		}
	}
	for _, forbidden := range []string{"tenant_id", "id", "order_ref", "store_id", "status", "created_by", "code", "assets"} {
		if _, ok := valid[forbidden]; ok {
			t.Fatalf("public payload leaks %q: %v", forbidden, valid)
		}
	}
	// non-valid payloads expose state only
	for _, c := range cases[1:] {
		_, _, body := f.do(t, "GET", "/api/v1/public/links/"+c.code, "", "", "")
		if len(body) != 1 {
			t.Fatalf("%s: non-valid payload must carry state only, got %v", c.name, body)
		}
	}
	// guest cannot write the public surface
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/links/"+validCode, strings.NewReader("{}"))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("guest POST public = %d, want 405", res.StatusCode)
	}
}

// ---- auth flows through HTTP --------------------------------------------------------

func TestAuthFlow(t *testing.T) {
	f := newFixture(t, true)
	// login sets both cookies
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/auth/login",
		strings.NewReader(`{"email":"a@example.com","password":"pw"}`))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusOK)
	var cookies []string
	for _, ck := range res.Cookies() {
		if ck.Name == "touch_session" || ck.Name == "touch_session_refresh" {
			cookies = append(cookies, ck.Name)
		}
	}
	if len(cookies) != 2 {
		t.Fatalf("login cookies = %v (%s)", cookies, b)
	}
	// refresh rotates
	req, _ = http.NewRequest("POST", f.ts.URL+"/api/v1/auth/refresh", nil)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.AddCookie(&http.Cookie{Name: "touch_session_refresh", Value: "rt-login"})
	res, err = f.ts.Client().Do(req)
	mustNoErr(t, err)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusOK)
	// logout clears
	req, _ = http.NewRequest("POST", f.ts.URL+"/api/v1/auth/logout", nil)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	res, err = f.ts.Client().Do(req)
	mustNoErr(t, err)
	res.Body.Close()
	mustEqual(t, res.StatusCode, http.StatusOK)
	if len(res.Cookies()) == 0 {
		t.Fatal("logout must clear cookies")
	}
	// login rejection stays opaque
	status, _, _ := f.do(t, "POST", "/api/v1/auth/login", "", "", `{"email":"a@example.com","password":""}`)
	mustEqual(t, status, http.StatusBadRequest)
}

// ---- config gate fail-closed ----------------------------------------------------------

func TestConfigGateFailsClosed(t *testing.T) {
	f := newFixture(t, true)
	// strip identity config: every authenticated action must 503
	f.s.Cfg.IdentityBaseURL = ""
	status, _, body := f.do(t, "GET", "/api/v1/campaigns", "sess-owner-a", f.tenA, "")
	if status != http.StatusServiceUnavailable || body["error"] != "config_gate" {
		t.Fatalf("config gate = %d %v", status, body)
	}
	// healthz still observable
	status, _, health := f.do(t, "GET", "/healthz", "", "", "")
	mustEqual(t, status, 200)
	if health["status"] != "degraded" {
		t.Fatalf("healthz = %v", health)
	}
	// public surface keeps working without platform config (no identity involved)
	// (seeded earlier campaigns are gone in this fixture; just check not_found shape)
	status, _, body = f.do(t, "GET", "/api/v1/public/links/ZZZZZZZZZZZZ", "", "", "")
	mustEqual(t, status, 404)
	if body["state"] != "not_found" {
		t.Fatalf("public unknown = %v", body)
	}
}

func mustEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
