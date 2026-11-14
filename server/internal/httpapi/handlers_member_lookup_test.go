package httpapi

import (
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMemberLookupControlledIdentity(t *testing.T) {
	f := newAgencyFixture(t, true)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/internal/v1/identity/users/exists" || r.URL.Query().Get("email") != "person@example.com" || r.Header.Get("X-PilotSeaView-Internal-Token") != "lookup-secret" {
			t.Errorf("unexpected Identity contract: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"exists":true,"user_id":"usr_identity_original","account_id":"acct_private","status":"active"}`))
	}))
	defer upstream.Close()
	original := f.s.ID.HTTP.Transport
	if original == nil {
		original = http.DefaultTransport
	}
	f.s.ID.HTTP = &http.Client{Transport: lookupTransport{original, upstream.URL}}
	f.s.ID.Token = "lookup-secret"
	status, out := f.do(t, "POST", "/api/v1/admin/members/lookup", "sess-staff-a", f.tenA, `{"email":"person@example.com"}`)
	if status != 403 || calls.Load() != 0 {
		t.Fatalf("unauthorized lookup: %d %+v calls=%d", status, out, calls.Load())
	}
	status, out = f.do(t, "POST", "/api/v1/admin/members/lookup", "sess-owner-a", f.tenA, `{"email":"person"}`)
	if status != 400 || calls.Load() != 0 {
		t.Fatalf("partial lookup: %d %+v", status, out)
	}
	status, out = f.do(t, "POST", "/api/v1/admin/members/lookup", "sess-owner-a", f.tenA, `{"email":" Person@Example.com "}`)
	if status != 200 || out["principal_ref"] != "usr_identity_original" || out["display_name"] != "person@example.com" {
		t.Fatalf("lookup: %d %+v", status, out)
	}
	if _, ok := out["account_id"]; ok {
		t.Fatal("account id disclosed")
	}
	for i := 0; i < 20; i++ {
		status, _ = f.do(t, "POST", "/api/v1/admin/members/lookup", "sess-owner-a", f.tenA, `{"email":"person@example.com"}`)
	}
	if status != 429 || calls.Load() > 10 {
		t.Fatalf("rate limit missing: %d calls=%d", status, calls.Load())
	}
}

type lookupTransport struct {
	fallback http.RoundTripper
	target   string
}

func (tr lookupTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/internal/v1/identity/users/exists" {
		copy := r.Clone(r.Context())
		u := *r.URL
		target, _ := http.NewRequest("GET", tr.target, nil)
		u.Scheme = target.URL.Scheme
		u.Host = target.URL.Host
		copy.URL = &u
		return tr.fallback.RoundTrip(copy)
	}
	return tr.fallback.RoundTrip(r)
}

func TestGrantedPortfolioExcludesExpiredAndDisabled(t *testing.T) {
	f := newAgencyFixture(t, true)
	id := f.mustEstablish(t, "usr_ag1", "portfolio")
	if _, err := f.s.St.CreateMember(f.tenB, "usr_ag1", "org_owner", "代理自己的组织", "test", true); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.St.SetAgencyRelationScope(id, f.tenA, store.SetAgencyScopeInput{ValidUntil: "2020-01-01T00:00:00Z", ExpectRevision: 0, ActorPrincipal: "usr_owner_a", Reason: "expired grant"})
	if err != nil {
		t.Fatal(err)
	}
	status, out := f.do(t, "GET", "/api/v1/agency/relations/granted", "sess-agent1", f.tenB, "")
	if status != 200 {
		t.Fatalf("portfolio: %d %+v", status, out)
	}
	if len(out["items"].([]any)) != 0 {
		t.Fatalf("expired grant still offered for entry: %+v", out)
	}

	if _, err := f.s.St.SetAgencyRelationScope(id, f.tenA, store.SetAgencyScopeInput{ExpectRevision: 1, ActorPrincipal: "usr_owner_a", Reason: "restore grant"}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.St.DisableAgentMember(f.tenA, "usr_ag1"); err != nil {
		t.Fatal(err)
	}
	status, out = f.do(t, "GET", "/api/v1/agency/relations/granted", "sess-agent1", f.tenB, "")
	if status != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("disabled target offered for entry: %d %+v", status, out)
	}
}
