package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type followUpTransport func(*http.Request) (*http.Response, error)

func (f followUpTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestWorkLeadFollowUpGatesOriginalStoreAndOneTimeReturn(t *testing.T) {
	f := newLeadsFixture(t)
	ref, sid := storeLeadViaAPI(t, f, "13900000883")
	path := "/api/v1/work/leads/" + ref + "/follow-up"
	if status, _, out := f.admin(t, "POST", path, ""); status != 409 {
		t.Fatalf("pending=%d %v", status, out)
	}
	f.forwarder.Tick(t.Context())
	if _, _, err := f.s.St.AckLeadReceipt(f.tenA, ref, leadsReceiverID, storedAck(ref)); err != nil {
		t.Fatal(err)
	}
	// A manager in a different store cannot use a visible/guessed source ref.
	if status, _, out := f.do(t, "POST", path, "sess-sm-a", f.tenA, ""); status != 404 {
		t.Errorf("foreign store=%d %v", status, out)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Touch-Follow-Up-Signature") == "" {
			t.Error("missing service proof")
		}
		if strings.HasSuffix(r.URL.Path, "/resolve") {
			writeJSON(w, 200, map[string]string{"crm_tenant_id": "crm-mapped-tenant", "lead_id": "real-lead-not-source-ref"})
		} else {
			w.WriteHeader(201)
		}
	}))
	defer target.Close()
	f.s.Cfg.LeadsFollowUpBaseURL = target.URL
	f.s.Cfg.LeadsFollowUpToken = "follow-up-service-secret"
	denied := true
	count := 0
	original := f.s.ID.HTTP.Transport
	if original == nil {
		original = http.DefaultTransport
	}
	f.s.ID.HTTP = &http.Client{Transport: followUpTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/internal/v1/identity/shell/launch" {
			return original.RoundTrip(r)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tenant_id"] != "crm-mapped-tenant" || body["principal_id"] != "usr_owner_a" {
			t.Errorf("wrong mapped shell authority=%v", body)
		}
		status := 200
		out := ""
		if denied {
			status = 403
			out = `{"error":"target_disabled"}`
		} else {
			count++
			out = fmt.Sprintf(`{"launch_url":"https://leads.modelxing.com/?code=original-code&h=hnd_%032x"}`, count)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(out)), Header: make(http.Header)}, nil
	})}
	if status, _, out := f.admin(t, "POST", path, ""); status != 503 {
		t.Fatalf("disabled target=%d %v", status, out)
	}
	denied = false
	status, _, out := f.admin(t, "POST", path, "")
	if status != 200 {
		t.Fatalf("retry=%d %v", status, out)
	}
	token, _ := out["jump_token"].(string)
	if token == "" || out["launch_url"] != nil {
		t.Fatalf("must use original one-time jump=%v", out)
	}
	status, _, out = f.admin(t, "POST", "/api/v1/eco-nav/switch/redeem", fmt.Sprintf(`{"jump_token":%q}`, token))
	if status != 200 || out["launch_url"] != "https://leads.modelxing.com/?code=original-code&h=hnd_00000000000000000000000000000001" {
		t.Fatalf("unchanged shell URL=%d %v", status, out)
	}
	if status, _, out = f.admin(t, "POST", "/api/v1/eco-nav/switch/redeem", fmt.Sprintf(`{"jump_token":%q}`, token)); status != 404 {
		t.Fatalf("jump replay=%d %v", status, out)
	}
	origin := "hnd_00000000000000000000000000000001"
	back := "hnd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	returnPath := "/api/v1/internal/lead-follow-ups/" + origin + "/returns"
	req, _ := http.NewRequest("POST", f.ts.URL+returnPath, strings.NewReader(fmt.Sprintf(`{"handoff_id":%q,"principal_id":"usr_owner_a"}`, back)))
	req.Header.Set("X-Service-ID", leadsReceiverID)
	req.Header.Set("X-Service-Token", leadsReceiverToken)
	req.Header.Set("X-Leads-Tenant", f.tenA)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("register original return=%d", res.StatusCode)
	}
	resume := "/api/v1/lead-follow-up-returns/" + back + "/resume"
	if status, _, out := f.do(t, "POST", resume, "sess-owner-b", "", ""); status != 403 {
		t.Fatalf("return foreign identity=%d %v", status, out)
	}
	status, _, out = f.do(t, "POST", resume, "sess-owner-a", "arbitrary-client-tenant", "")
	if status != 200 || out["tenant_id"] != f.tenA || out["path"] != "/work/stores/"+sid {
		t.Fatalf("server-derived original store=%d %v", status, out)
	}
	if status, _, out := f.do(t, "POST", resume, "sess-owner-a", "", ""); status != 410 {
		t.Fatalf("return replay=%d %v", status, out)
	}
	// No active source consent can be inferred from ACK after withdrawal.
	if _, err := f.s.St.DB.Exec(`UPDATE lead_submissions SET revoked_at=?,revoke_source_revision=2 WHERE submission_ref=?`, time.Now().UTC().Format(time.RFC3339), ref); err != nil {
		t.Fatal(err)
	}
	if status, _, out := f.admin(t, "POST", path, ""); status != 409 {
		t.Fatalf("withdrawn ACK=%d %v", status, out)
	}
	n, err := f.s.St.GetFollowUpNavigation(origin)
	if err != nil || n.StoreID != sid || n.SourceRef != ref || n.CRMTenantID != "crm-mapped-tenant" {
		t.Fatalf("original tuple=%+v %v", n, err)
	}
}

func TestFollowUpCanonicalSSOCookieUsesOriginalSessionResolution(t *testing.T) {
	f := newLeadsFixture(t)
	const cookieName = "touch-engine_session"
	const token = "issued-only-by-engineering-identity-exchange"
	var exchanged bool
	id := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/sso/exchange" {
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if r.Host != "touch-followup.modelxing.com" || r.Header.Get("Origin") != "https://touch-followup.modelxing.com" || in["code"] != "one-time-code" || exchanged {
				w.WriteHeader(403)
				return
			}
			exchanged = true
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Secure: true, HttpOnly: true})
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/internal/v1/identity/session/resolve" {
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if r.Header.Get("X-App-ID") != "touch-engine" || r.Header.Get("X-PilotSeaView-Internal-Token") != "identity-token" || !exchanged || in["session_token"] != token {
				writeJSON(w, 200, map[string]bool{"authenticated": false})
				return
			}
			writeJSON(w, 200, map[string]any{"authenticated": true, "app_id": "touch-engine", "session": map[string]string{"principal_id": "usr_owner_a", "email": "fixture-owner@example.test"}})
			return
		}
		w.WriteHeader(404)
	}))
	defer id.Close()
	f.s.ID.BaseURL = id.URL
	f.s.ID.HTTP = id.Client()
	f.s.Cfg.SessionCookie = cookieName
	exchange, _ := http.NewRequest("POST", id.URL+"/api/auth/sso/exchange", strings.NewReader(`{"code":"one-time-code"}`))
	exchange.Host = "touch-followup.modelxing.com"
	exchange.Header.Set("Origin", "https://touch-followup.modelxing.com")
	issued, err := id.Client().Do(exchange)
	if err != nil {
		t.Fatal(err)
	}
	issued.Body.Close()
	if issued.StatusCode != 200 || len(issued.Cookies()) != 1 || issued.Cookies()[0].Name != cookieName {
		t.Fatalf("SSO did not issue canonical app cookie=%v", issued)
	}
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/whoami", nil)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Tenant-ID", f.tenA)
	req.AddCookie(issued.Cookies()[0])
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var who map[string]any
	_ = json.NewDecoder(res.Body).Decode(&who)
	if res.StatusCode != 200 || who["tenant_id"] != f.tenA || who["principal_ref"] != "usr_owner_a" {
		t.Fatalf("canonical cookie rejected by original resolve path=%d %v", res.StatusCode, who)
	}
	req2, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/whoami", nil)
	req2.Header.Set("X-Internal-Token", "test-internal-secret")
	req2.Header.Set("X-Tenant-ID", f.tenB)
	req2.AddCookie(issued.Cookies()[0])
	foreign, err := f.ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	foreign.Body.Close()
	if foreign.StatusCode != 403 {
		t.Fatalf("same SSO cookie selected foreign tenant=%d", foreign.StatusCode)
	}
}
