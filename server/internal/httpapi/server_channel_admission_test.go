package httpapi

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

type admissionFixture struct {
	s    *Server
	ts   *httptest.Server
	tenA string
	code string // short code of an enabled link in tenA
}

func newAdmissionFixture(t *testing.T, on bool) *admissionFixture {
	t.Helper()
	flag := ""
	if on {
		flag = "on"
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_CHANNEL_ADMISSION":
			return flag
		}
		return ""
	})

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("商家A")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed %s: %v", m.principal, err)
		}
	}
	camp, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, Title: "门店活动", CreatedBy: "usr_owner_a"})
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	link, err := st.CreateLink(tenA.ID, camp.ID, "usr_owner_a")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	// 公共短码只在活动 active 时可用(五态),先把活动推到 active。
	if _, err := st.TransitionCampaign(camp.ID, tenA.ID, campaign.StatusActive); err != nil {
		t.Fatalf("activate campaign: %v", err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner-a@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}

	s := New(cfg, d, idc, nil, log.New(io.Discard, "", 0))
	// mirror Open()'s seed loading for the on-branch (Open() is the production
	// path; tests construct the server directly against the fixture DB).
	if on {
		seed, err := channeladmission.EmbeddedSeed()
		if err != nil {
			t.Fatalf("embedded seed: %v", err)
		}
		if _, err := s.St.LoadAdmissionSeed(seed); err != nil {
			t.Fatalf("load seed: %v", err)
		}
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &admissionFixture{s: s, ts: ts, tenA: tenA.ID, code: link.Code}
}

func (f *admissionFixture) get(t *testing.T, path, session, tenant string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// TestChannelAdmissionOffIsInvisible: off = routes not registered AND the
// handler gate answers uniform 404 (dual insurance, same as FEATURE_AGENCY).
func TestChannelAdmissionOffIsInvisible(t *testing.T) {
	f := newAdmissionFixture(t, false)
	status, _ := f.get(t, "/api/v1/channel-admissions", "sess-owner-a", f.tenA)
	if status != http.StatusNotFound {
		t.Fatalf("admin surface off = %d, want 404 (route not registered: mux plain 404)", status)
	}
	status, _ = f.get(t, "/api/v1/public/links/"+f.code+"/channel-admission?platform=douyin&entry_mode=h5&scenario=store_promo", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("public surface off = %d, want 404", status)
	}
}

// TestChannelAdmissionMerchantSurface: the merchant face lists every seeded
// route with honest refusals — 1x not_admitted + 4x admission_unknown, each
// with 缺项原因/所需材料 (missing + official sources visible to the merchant).
func TestChannelAdmissionMerchantSurface(t *testing.T) {
	f := newAdmissionFixture(t, true)
	status, body := f.get(t, "/api/v1/channel-admissions", "sess-owner-a", f.tenA)
	if status != http.StatusOK {
		t.Fatalf("admin surface = %d, want 200: %v", status, body)
	}
	recs, ok := body["records"].([]any)
	if !ok || len(recs) != 5 {
		t.Fatalf("records = %T len %v, want 5", body["records"], len(recs))
	}
	reasons := map[string]int{}
	for _, raw := range recs {
		rec := raw.(map[string]any)
		if rec["allowed"] != false {
			t.Errorf("%v must not be allowed as of the seed truth (no platform approval)", rec["platform"])
		}
		reason, _ := rec["reason"].(string)
		reasons[reason]++
		missing, _ := rec["missing"].([]any)
		if len(missing) == 0 {
			t.Errorf("%v refusal must carry missing items (商家能看见缺项原因及所需材料)", rec["platform"])
		}
		sources, _ := rec["official_sources"].([]any)
		if len(sources) == 0 {
			t.Errorf("%v must carry official sources", rec["platform"])
		}
		if rec["last_checked_at"] != "2026-10-14" {
			t.Errorf("%v last_checked_at = %v, want 2026-10-14", rec["platform"], rec["last_checked_at"])
		}
	}
	if reasons["not_admitted"] != 1 || reasons["admission_unknown"] != 4 {
		t.Fatalf("reason mix = %v, want 1 not_admitted + 4 admission_unknown", reasons)
	}
}

// TestChannelAdmissionPublicSurface: the guest face answers the gate verdict
// for the link's own tenant; unregistered routes answer honestly as unknown.
func TestChannelAdmissionPublicSurface(t *testing.T) {
	f := newAdmissionFixture(t, true)

	status, body := f.get(t, "/api/v1/public/links/"+f.code+"/channel-admission?platform=douyin&entry_mode=h5&scenario=store_promo", "", "")
	if status != http.StatusOK {
		t.Fatalf("public douyin h5 = %d, want 200: %v", status, body)
	}
	if body["allowed"] != false || body["reason"] != "not_admitted" {
		t.Fatalf("douyin h5 verdict = allowed=%v reason=%v, want false/not_admitted", body["allowed"], body["reason"])
	}
	if body["last_checked_at"] != "2026-10-14" {
		t.Fatalf("public payload leaks audit fields? last_checked_at = %v", body["last_checked_at"])
	}

	status, body = f.get(t, "/api/v1/public/links/"+f.code+"/channel-admission?platform=channels&entry_mode=h5&scenario=store_promo", "", "")
	if status != http.StatusOK {
		t.Fatalf("public unregistered route = %d, want 200", status)
	}
	if body["allowed"] != false || body["reason"] != "admission_unknown" {
		t.Fatalf("unregistered route = allowed=%v reason=%v, want false/admission_unknown (honest unknown, never allowed)", body["allowed"], body["reason"])
	}

	status, _ = f.get(t, "/api/v1/public/links/"+f.code+"/channel-admission?platform=douyin", "", "")
	if status != http.StatusBadRequest {
		t.Fatalf("missing params = %d, want 400 param_required", status)
	}

	// guest surface has no session concept: session/tenant headers are not
	// consulted (the route is registered without requireSession), and a bad
	// short code answers the standard public 404 state, not a leak.
	status, body = f.get(t, "/api/v1/public/links/nosuchcode/channel-admission?platform=douyin&entry_mode=h5&scenario=store_promo", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("bad code = %d, want 404", status)
	}
	if body["state"] == "" {
		t.Fatalf("bad code body = %v, want public state envelope", body)
	}
}

// TestChannelAdmissionOpenLoadsSeed covers the production Open() path: with
// the flag on, a server opened against a real DB path serves seed truth.
func TestChannelAdmissionOpenLoadsSeed(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_DB_PATH":
			return filepath.Join(dir, "touch.db")
		case "FEATURE_CHANNEL_ADMISSION":
			return "on"
		}
		return ""
	})
	s, err := Open(cfg, log.New(os.Stderr, "", 0))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	rows, err := s.St.ListChannelAdmissions("t_any")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("seed rows loaded = %d, want 5", len(rows))
	}
	if got := rows[0].Platform + "|" + rows[0].EntryMode; !strings.HasPrefix(got, "bilibili|h5") {
		t.Fatalf("first row = %s, want bilibili ordering", got)
	}
}
