package httpapi

// server_leads_test.go: HUI-1747 T1 acceptance matrix over the real HTTP
// surface with a local platform-notify stub (E4 directed-event profile).
//
// The stub plays notify + CRM target faithfully in two phases:
//   - POST /internal/v1/notify/events = notify acceptance (contract headers,
//     profile parse, target-side fact-key dedup + source_version monotonic
//     guard = the CRM inbox); the delivery row starts "pending";
//   - DeliverAll() simulates the CRM coming back / dispatcher success;
//   - GET /internal/v1/notify/events/{id}/deliveries reports that status.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/notifytask"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// ---- notify stub -----------------------------------------------------------------

type recordedEvent struct {
	Profile leads.EventProfile
	Raw     string
}

type notifyStub struct {
	mu          sync.Mutex
	srv         *httptest.Server
	events      []recordedEvent
	deliveries  map[string]string // notify event_id -> pending|delivered
	lastVersion map[string]int    // source_ref -> highest applied source_version
	marketing   map[string]bool   // source_ref -> marketing allowed at the target
	staleSkip   int
	publish500  bool // notify refuses (transport-level)
	badHeaders  int
	itemsAlias  bool // report status under "items" instead of "deliveries"
}

func newNotifyStub(t *testing.T) *notifyStub {
	t.Helper()
	s := &notifyStub{deliveries: map[string]string{}, lastVersion: map[string]int{}, marketing: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/notify/events", s.handlePublish)
	mux.HandleFunc("/internal/v1/notify/events/", s.handleDeliveries)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *notifyStub) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("X-PilotSeaView-Internal-Token") == "" || r.Header.Get("X-Notify-App-ID") != "touch-engine" {
		s.badHeaders++
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if s.publish500 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var wrapped struct {
		Type          string          `json:"type"`
		SchemaVersion int             `json:"schema_version"`
		AppID         string          `json:"app_id"`
		TenantID      string          `json:"tenant_id"`
		Data          json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil || wrapped.Type == "" || wrapped.SchemaVersion < 1 || wrapped.AppID != "touch-engine" || len(wrapped.Data) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var env struct {
		EventProfile leads.EventProfile    `json:"event_profile"`
		Payload      leads.PayloadMetadata `json:"payload"`
	}
	if err := json.Unmarshal(wrapped.Data, &env); err != nil || env.EventProfile.EventID == "" || env.EventProfile.EventType != wrapped.Type || env.EventProfile.TenantScope != wrapped.TenantID {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	notifyID := "evt_" + env.EventProfile.EventID
	s.events = append(s.events, recordedEvent{Profile: env.EventProfile, Raw: string(body)})
	s.deliveries[notifyID] = "pending" // accepted by notify, CRM not yet

	// target-side inbox: source_version monotonic guard per fact (submission).
	// The guard is tracked per source_ref across event types: a replayed older
	// fact can never overwrite (or resurrect) newer state — this is exactly the
	// property "重放旧事件不得恢复营销权限" depends on.
	ref := env.EventProfile.SourceRef
	have := s.lastVersion[ref]
	switch v := env.EventProfile.SourceVersion; {
	case v > have:
		s.lastVersion[ref] = v
		if env.EventProfile.EventType == leads.EventLeadConsentRevoked {
			s.marketing[ref] = false
		} else {
			s.marketing[ref] = env.Payload.MarketingOptin
		}
	case v == have:
		// idempotent replay: same_result, no second effect
	default:
		s.staleSkip++ // late old version: skipped, never overwrites newer state
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"event_id":"` + notifyID + `","duplicate":false}`))
}

func (s *notifyStub) handleDeliveries(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	eventID := parts[4]
	if r.URL.Query().Get("app_id") == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.deliveries[eventID]
	w.Header().Set("Content-Type", "application/json")
	if status == "" || s.itemsAlias {
		if s.itemsAlias && status != "" {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"items":[{"delivery_id":"d-%s","status":%q,"attempt_count":1}]}`, eventID, status)))
			return
		}
		_, _ = w.Write([]byte(`{"deliveries":[]}`))
		return
	}
	_, _ = w.Write([]byte(fmt.Sprintf(`{"deliveries":[{"delivery_id":"d-%s","status":%q,"attempt_count":1}]}`, eventID, status)))
}

// DeliverAll simulates CRM recovery: every pending delivery succeeds.
func (s *notifyStub) DeliverAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.deliveries {
		if v == "pending" {
			s.deliveries[k] = "delivered"
		}
	}
}

func (s *notifyStub) recorded() []recordedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedEvent, len(s.events))
	copy(out, s.events)
	return out
}

func (s *notifyStub) lastRaw() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[len(s.events)-1].Raw
}

func (s *notifyStub) marketingAllowed(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.marketing[ref]
}

// forceReplay delivers an old raw envelope straight to the stub as if notify
// retried it — proving stale versions cannot restore marketing permission.
func (s *notifyStub) forceReplay(t *testing.T, raw string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.srv.URL+"/internal/v1/notify/events", strings.NewReader(raw))
	req.Header.Set("X-PilotSeaView-Internal-Token", "stub")
	req.Header.Set("X-Notify-App-ID", "touch-engine")
	res, err := s.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	res.Body.Close()
}

// ---- leads fixture -----------------------------------------------------------------

type leadsFixture struct {
	s            *Server
	ts           *httptest.Server
	tenA         string
	stub         *notifyStub
	code         string // active campaign short code
	campaignID   string
	forwarder    *leads.Forwarder
	adminSession string
}

func newLeadsFixture(t *testing.T) *leadsFixture {
	t.Helper()
	stub := newNotifyStub(t)
	cfg := leadsTestConfig(stub.srv.URL)

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
	tenB, err := st.CreateTenant("商家B")
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

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	f := &leadsFixture{
		s: s, ts: ts, tenA: tenA.ID, stub: stub, adminSession: "sess-owner-a",
		forwarder: leads.NewForwarder(s.St, newTestPoster(stub), log.New(io.Discard, "", 0)),
	}

	// seed: active campaign + link + enabled lead form (via the admin surface)
	status, _, body := f.admin(t, "POST", "/api/v1/campaigns",
		`{"title":"周末市集","public_content":"来玩","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`)
	if status != 201 {
		t.Fatalf("campaign create: %d %v", status, body)
	}
	f.campaignID = body["id"].(string)
	if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+f.campaignID+"/status", `{"status":"active"}`); status != 200 {
		t.Fatal("activate failed")
	}
	if status, _, link := f.admin(t, "POST", "/api/v1/campaigns/"+f.campaignID+"/links", ``); status != 201 {
		t.Fatal("link create failed")
	} else {
		f.code = link["code"].(string)
	}
	if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+f.campaignID+"/lead-form", `{"marketing_optin_enabled":true}`); status != 201 {
		t.Fatal("lead form upsert failed")
	}
	return f
}

func leadsTestConfig(notifyURL string) config.Config {
	return config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return notifyURL
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-app"
		case "LEADS_PHONE_PEPPER":
			return "test-pepper"
		case "FEATURE_LEADS_CAPTURE":
			return "on"
		}
		return ""
	})
}

func newTestPoster(stub *notifyStub) *notifytask.DirectedClient {
	return notifytask.NewDirectedClient(
		notifytask.New(notifytask.SideNotify, true, stub.srv.URL, "notify-token", "touch-engine", "X-Notify-App-ID"),
		stub.srv.Client())
}

// do performs a raw request against the fixture server (mirrors fixture.do).
func (f *leadsFixture) do(t *testing.T, method, path, session, tenant, body string) (int, http.Header, map[string]any) {
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
	return res.StatusCode, res.Header, out
}

// admin performs an authenticated admin call as tenant-A owner.
func (f *leadsFixture) admin(t *testing.T, method, path, body string) (int, http.Header, map[string]any) {
	t.Helper()
	return f.do(t, method, path, f.adminSession, f.tenA, body)
}

// guest performs an unauthenticated public call.
func (f *leadsFixture) guest(t *testing.T, method, path, body string) (int, http.Header, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "", "", body)
}

func (f *leadsFixture) submitLead(t *testing.T, code, body string) (int, map[string]any) {
	t.Helper()
	status, _, out := f.guest(t, "POST", "/api/v1/public/links/"+code+"/lead-submissions", body)
	return status, out
}

const validLeadBody = `{"name":"张三","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"wecom"}`

// ---- 1. feature off = invisible surface ---------------------------------------------

func TestLeadsFeatureOffIsInvisible(t *testing.T) {
	f := newLeadsFixture(t)
	f.s.Cfg.FeatureLeadsCapture = false
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/public/links/" + f.code + "/lead-form"},
		{"POST", "/api/v1/public/links/" + f.code + "/lead-submissions"},
		{"POST", "/api/v1/public/links/" + f.code + "/lead-revocations"},
		{"POST", "/api/v1/public/links/" + f.code + "/view-events"},
	} {
		status, _, _ := f.guest(t, tc.method, tc.path, `{}`)
		if status != 404 {
			t.Fatalf("%s %s = %d, want uniform 404", tc.method, tc.path, status)
		}
	}
	// admin side is gated by the same flag: a form configured while the
	// collection pipeline (forwarder) is off would silently dead-end.
	if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+f.campaignID+"/lead-form", `{"notice_version":"v1"}`); status != 404 {
		t.Fatalf("admin lead-form with flag off = %d, want uniform 404", status)
	}
}

// ---- 2. happy path: consent -> event -> 待同步 -> confirmed ----------------------------

func TestLeadsE2EHappyPath(t *testing.T) {
	f := newLeadsFixture(t)

	status, _, form := f.guest(t, "GET", "/api/v1/public/links/"+f.code+"/lead-form", "")
	if status != 200 || form["enabled"] != true || form["marketing_optin_enabled"] != true {
		t.Fatalf("lead form = %d %v", status, form)
	}

	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	if out["state"] != "accepted" || !strings.HasPrefix(ref, "sub_") {
		t.Fatalf("submit body = %v", out)
	}

	// forward: notify accepts the directed fact; state = 待同步
	f.forwarder.Tick(context.Background())
	events := f.stub.recorded()
	if len(events) != 1 {
		t.Fatalf("stub events = %d, want 1", len(events))
	}
	p := events[0].Profile
	if p.ProfileVersion != "directed-event/v1" || p.EventType != "lead.authorized_submitted" ||
		p.SourceApp != "touch-engine" || p.TargetApp != "crm-app" || p.TenantScope != f.tenA ||
		p.SourceRef != ref || p.SourceVersion != 1 || p.EventID != ref {
		t.Fatalf("event profile = %+v", p)
	}
	if p.PayloadRef.Ref != "touch://leads/"+ref || len(p.PayloadRef.SHA256) != 64 {
		t.Fatalf("payload ref = %+v", p.PayloadRef)
	}
	lead, err := f.s.St.GetLeadSubmissionByRef(ref)
	if err != nil || lead.SyncState != "pending_sync" {
		t.Fatalf("state after publish = %q %v, want pending_sync", lead.SyncState, err)
	}

	// payload: consent/provenance references present, ZERO contact data
	raw := f.stub.lastRaw()
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatal(err)
	}
	if probe["type"] != "lead.authorized_submitted" || probe["app_id"] != "touch-engine" || probe["tenant_id"] != f.tenA {
		t.Fatalf("wrapper = %v", probe)
	}
	data, _ := probe["data"].(map[string]any)
	payload, _ := data["payload"].(map[string]any)
	if payload["consent_ref"] != "touch://leads/"+ref || payload["consent_version"] != "v1" ||
		payload["marketing_optin"] != true || payload["campaign_ref"] != f.campaignID {
		t.Fatalf("payload = %v", payload)
	}
	if payload["trace_id"] != "lead:"+ref || payload["return_target"] != "/c/"+f.code {
		t.Fatalf("source trace = %v", payload)
	}
	notifyID, nerr := f.s.St.NotifyEventID(ref, "submit")
	if nerr != nil || notifyID == "" || notifyID == ref || notifyID != "evt_"+ref {
		t.Fatalf("stored notify id = %q %v", notifyID, nerr)
	}
	for _, forbidden := range []string{"phone", "mobile", "wechat", "contact", "13800138000", "张三"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("envelope leaks contact data %q: %s", forbidden, raw)
		}
	}
	respRaw, _ := json.Marshal(out)
	if strings.Contains(string(respRaw), "13800138000") {
		t.Fatal("submit response leaks phone")
	}

	// CRM confirms -> crm_received
	f.stub.DeliverAll()
	if tick := f.forwarder.Tick(context.Background()); tick.Confirmed != 1 {
		t.Fatalf("confirm tick = %+v", tick)
	}
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "crm_received" {
		t.Fatalf("state after confirm = %q", lead.SyncState)
	}
	if !f.stub.marketingAllowed(ref) {
		t.Fatal("target should have marketing allowed for consented lead")
	}
	audits, _ := f.s.St.ListLeadAudit(f.tenA, ref)
	if len(audits) < 3 { // accept + sync_pending + sync_delivered
		t.Fatalf("audit rows = %d", len(audits))
	}
}

func TestLeadsItemsAliasDoesNotCountAsReceived(t *testing.T) {
	f := newLeadsFixture(t)
	f.stub.itemsAlias = true
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(context.Background())
	f.stub.DeliverAll()
	f.forwarder.Tick(context.Background())
	lead, err := f.s.St.GetLeadSubmissionByRef(ref)
	if err != nil || lead.SyncState != "pending_sync" {
		t.Fatalf("items alias state = %q %v, want pending_sync", lead.SyncState, err)
	}
}

// ---- 3. idempotency + concurrency ----------------------------------------------------

func TestLeadsIdempotentUnderConcurrency(t *testing.T) {
	f := newLeadsFixture(t)
	const n = 12
	var mu sync.Mutex
	refs := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, out := f.submitLead(t, f.code, validLeadBody)
			if status != 201 && status != 200 {
				t.Errorf("status %d inside concurrency", status)
				return
			}
			mu.Lock()
			refs[out["submission_ref"].(string)]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(refs) != 1 {
		t.Fatalf("distinct refs = %v, want exactly one logical submission", refs)
	}
	leadsRows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(leadsRows) != 1 {
		t.Fatalf("rows = %d, want 1", len(leadsRows))
	}
	outbox, _ := f.s.St.ListLeadsOutboxDue(10)
	if len(outbox) != 1 {
		t.Fatalf("outbox due = %d, want 1 (one fact, not twelve)", len(outbox))
	}
	// a different phone is a different lead
	other := strings.Replace(validLeadBody, "13800138000", "13900139000", 1)
	status, out := f.submitLead(t, f.code, other)
	if status != 201 || out["submission_ref"].(string) == refsKey(refs) {
		t.Fatalf("distinct phone must be a new submission: %d %v", status, out)
	}
	// 同键重投(响应丢失后重发)幂等返回原 ref
	againStatus, againOut := f.submitLead(t, f.code, validLeadBody)
	if againStatus != 200 || againOut["submission_ref"].(string) != refsKey(refs) {
		t.Fatalf("idempotent resubmit = %d %v", againStatus, againOut)
	}
}

func refsKey(m map[string]int) string {
	for k := range m {
		return k
	}
	return ""
}

// ---- 4. notify/CRM unavailable: 待同步, never fake receipt ------------------------------

func TestLeadsNotifyUnavailableStaysPending(t *testing.T) {
	f := newLeadsFixture(t)
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d", status)
	}
	ref := out["submission_ref"].(string)

	// notify refuses: state stays accepted with an error recorded, no fake sync
	f.stub.mu.Lock()
	f.stub.publish500 = true
	f.stub.mu.Unlock()
	f.forwarder.Tick(context.Background())
	lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "accepted" || lead.SyncError == "" {
		t.Fatalf("after refused publish: state=%q err=%q", lead.SyncState, lead.SyncError)
	}
	// recovery at notify: accepted -> pending_sync; CRM confirmation still missing
	f.stub.mu.Lock()
	f.stub.publish500 = false
	f.stub.mu.Unlock()
	f.forwarder.Tick(context.Background())
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "pending_sync" {
		t.Fatalf("state = %q, want pending_sync (待同步)", lead.SyncState)
	}
	// 内容预览不受停机影响
	if status, _, body := f.guest(t, "GET", "/api/v1/public/links/"+f.code, ""); status != 200 || body["state"] != "available" {
		t.Fatalf("public page during outage = %d %v", status, body)
	}
	// 不伪报入库:deliveries still pending -> not crm_received
	f.forwarder.Tick(context.Background())
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "pending_sync" {
		t.Fatal("pending delivery must not be reported as crm_received")
	}
	// CRM recovers: only then crm_received
	f.stub.DeliverAll()
	if tick := f.forwarder.Tick(context.Background()); tick.Confirmed != 1 {
		t.Fatalf("confirm tick = %+v", tick)
	}
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "crm_received" {
		t.Fatalf("state = %q, want crm_received", lead.SyncState)
	}
}

// ---- 5. negatives -----------------------------------------------------------------------

func TestLeadsSubmitNegativeMatrix(t *testing.T) {
	f := newLeadsFixture(t)
	cases := []struct {
		name, body, wantErr string
		wantStatus          int
	}{
		{"no consent", `{"name":"张三","phone":"13800138000","consent_version":"v1","consent":false}`, "consent_required", 400},
		{"notice version mismatch", `{"name":"张三","phone":"13800138000","consent_version":"v9","consent":true}`, "notice_version_mismatch", 400},
		{"bad phone", `{"name":"张三","phone":"12345","consent_version":"v1","consent":true}`, "bad_phone", 400},
		{"missing name", `{"phone":"13800138000","consent_version":"v1","consent":true}`, "bad_request", 400},
		{"forged target_tenant", `{"name":"张三","phone":"13800138000","consent_version":"v1","consent":true,"target_tenant":"tnt_evil"}`, "bad_request", 400},
		{"forged tenant_id", `{"name":"张三","phone":"13800138000","consent_version":"v1","consent":true,"tenant_id":"tnt_evil"}`, "bad_request", 400},
		{"forged source_app", `{"name":"张三","phone":"13800138000","consent_version":"v1","consent":true,"source_app":"other-app"}`, "bad_request", 400},
	}
	for _, tc := range cases {
		status, out := f.submitLead(t, f.code, tc.body)
		if status != tc.wantStatus || out["error"] != tc.wantErr {
			t.Fatalf("%s: %d %v, want %d %s", tc.name, status, out, tc.wantStatus, tc.wantErr)
		}
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 0 {
		t.Fatalf("negatives wrote rows: %d", len(rows))
	}

	// expired campaign -> stateful 404
	f.admin(t, "POST", "/api/v1/campaigns", `{"title":"过期","starts_at":"2020-01-01T00:00:00Z","ends_at":"2020-12-01T00:00:00Z"}`)
	items, _ := f.s.St.ListCampaigns(f.tenA)
	var expiredID string
	for _, c := range items {
		if c.Title == "过期" {
			expiredID = c.ID
		}
	}
	f.admin(t, "POST", "/api/v1/campaigns/"+expiredID+"/status", `{"status":"active"}`)
	_, _, link := f.admin(t, "POST", "/api/v1/campaigns/"+expiredID+"/links", ``)
	if status, out := f.submitLead(t, link["code"].(string), validLeadBody); status != 404 || out["state"] != "expired" {
		t.Fatalf("expired = %d %v", status, out)
	}

	// campaign without a lead form -> 409 (capture not open)
	f.admin(t, "POST", "/api/v1/campaigns", `{"title":"无表单"}`)
	items, _ = f.s.St.ListCampaigns(f.tenA)
	var plainID string
	for _, c := range items {
		if c.Title == "无表单" {
			plainID = c.ID
		}
	}
	f.admin(t, "POST", "/api/v1/campaigns/"+plainID+"/status", `{"status":"active"}`)
	_, _, flink := f.admin(t, "POST", "/api/v1/campaigns/"+plainID+"/links", ``)
	if status, out := f.submitLead(t, flink["code"].(string), validLeadBody); status != 409 || out["error"] != "lead_capture_not_open" {
		t.Fatalf("no form = %d %v", status, out)
	}

	// marketing checkbox when the form disables it: accepted but forced off
	f.admin(t, "POST", "/api/v1/campaigns/"+f.campaignID+"/lead-form", `{"marketing_optin_enabled":false}`)
	status, out := f.submitLead(t, f.code, strings.Replace(validLeadBody, "13800138000", "13700137000", 1))
	if status != 201 {
		t.Fatalf("marketing-disabled submit = %d %v", status, out)
	}
	f.forwarder.Tick(context.Background())
	if raw := f.stub.lastRaw(); strings.Contains(raw, `"marketing_optin":true`) {
		t.Fatal("marketing optin must be forced off when the form disables it")
	}
}

// ---- 5b. D-X1: 国码前缀/分隔符容忍归一(对齐 leads 公共表单语义) --------------------------

func TestLeadsPhonePrefixNormalization(t *testing.T) {
	f := newLeadsFixture(t)

	// ① +86 前缀 + 分隔符形态 → 201,且落库为裸 11 位(归一先于校验与存储)
	prefixed := `{"name":"张三","phone":"+86 138-0013-8000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"wecom"}`
	status, out := f.submitLead(t, f.code, prefixed)
	if status != 201 {
		t.Fatalf("prefixed submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	lead, err := f.s.St.GetLeadSubmissionByRef(ref)
	if err != nil || lead.Phone != "13800138000" {
		t.Fatalf("stored phone = %q, %v; want bare 13800138000", lead.Phone, err)
	}

	// ② 同号码的 86 前缀形态同日重投 → 幂等命中原 ref(指纹/去重键由归一值派生,一致)
	altForm := strings.Replace(prefixed, `"+86 138-0013-8000"`, `"8613800138000"`, 1)
	status2, out2 := f.submitLead(t, f.code, altForm)
	if status2 != 200 || out2["duplicate"] != true || out2["submission_ref"].(string) != ref {
		t.Fatalf("86-prefixed resubmit = %d %v; want idempotent hit on %s", status2, out2, ref)
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (both forms are one logical submission)", len(rows))
	}

	// ③ 无法归一出合法 11 位 CN 手机号的仍 400 bad_phone,且零落库
	for _, p := range []string{"+86138001380", "86138001380", "8623800138000", "86138001380001", "12345", "abcdefghijk"} {
		body := fmt.Sprintf(`{"name":"张三","phone":%q,"consent_version":"v1","consent":true}`, p)
		st, o := f.submitLead(t, f.code, body)
		if st != 400 || o["error"] != "bad_phone" {
			t.Fatalf("phone %q: %d %v, want 400 bad_phone", p, st, o)
		}
	}
	rows, _ = f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 {
		t.Fatalf("bad phones wrote rows: %d", len(rows))
	}

	// ④ PII 门回归(零改变):channel 字段夹带联系方式形态仍在落库前拒绝
	pii := `{"name":"张三","phone":"13900139000","consent_version":"v1","consent":true,"channel":"call 13800138000 now"}`
	stPii, oPii := f.submitLead(t, f.code, pii)
	if stPii != 500 || oPii["error"] != "internal" {
		t.Fatalf("PII gate = %d %v, want pre-storage refusal", stPii, oPii)
	}
	rows, _ = f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 {
		t.Fatalf("PII-smuggling body wrote rows: %d", len(rows))
	}

	// ⑤ 撤销面同一归一器:+86 形态撤销裸值落库的提交 → 命中
	f.forwarder.Tick(context.Background())
	revoke := fmt.Sprintf(`{"submission_ref":%q,"phone":"+8613800138000"}`, ref)
	if st, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", revoke); st != 200 || resp["state"] != "revoked" {
		t.Fatalf("prefixed revoke = %d %v", st, resp)
	}
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "revoked" {
		t.Fatalf("state = %q, want revoked", lead.SyncState)
	}
}

// ---- 6. revocation matrix -----------------------------------------------------------------

func TestLeadsRevokeBeforeDeliveryBlocked(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	// revoke before the forwarder ever runs
	body := fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref)
	if status, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", body); status != 200 || resp["state"] != "revoked" {
		t.Fatalf("revoke = %d %v", status, resp)
	}
	f.forwarder.Tick(context.Background())
	if got := len(f.stub.recorded()); got != 0 {
		t.Fatalf("stub received %d events after pre-delivery revoke, want 0 (未投递阻止)", got)
	}
	lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "revoked" {
		t.Fatalf("state = %q, want revoked", lead.SyncState)
	}
	// revoke is final: later passes must not resurrect or forward it
	f.forwarder.Tick(context.Background())
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "revoked" || len(f.stub.recorded()) != 0 {
		t.Fatal("revoked lead must never be forwarded later")
	}
}

func TestLeadsRevokeAfterDeliverySendsRevokeFact(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(context.Background()) // publish -> pending_sync
	f.stub.DeliverAll()
	f.forwarder.Tick(context.Background()) // confirm -> crm_received
	lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "crm_received" {
		t.Fatalf("precondition: %q", lead.SyncState)
	}
	submitRaw := f.stub.recorded()[0].Raw

	// revoke after delivery: explicit revocation/stop-marketing fact must be sent
	body := fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref)
	if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", body); status != 200 {
		t.Fatal("revoke failed")
	}
	if tick := f.forwarder.Tick(context.Background()); tick.Published != 1 {
		t.Fatalf("revoke publish tick = %+v", tick)
	}
	events := f.stub.recorded()
	if len(events) != 2 || events[1].Profile.EventType != "lead.consent_revoked" {
		t.Fatalf("events = %+v", events)
	}
	if events[1].Profile.SourceVersion != 2 || events[1].Profile.SourceRef != ref {
		t.Fatalf("revoke profile = %+v", events[1].Profile)
	}
	if f.stub.marketingAllowed(ref) {
		t.Fatal("marketing must be revoked at the target")
	}
	// 重放旧事件不得恢复营销权限:the original submit fact replays (at-least-once)
	before := f.stub.staleSkip
	f.stub.forceReplay(t, submitRaw)
	if f.stub.staleSkip != before+1 {
		t.Fatal("replayed submit fact must be skipped as stale")
	}
	if f.stub.marketingAllowed(ref) {
		t.Fatal("stale replay must not restore marketing permission")
	}
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "revoked" {
		t.Fatalf("local state = %q, want revoked", lead.SyncState)
	}
}

func TestLeadsRevokeVerificationFailures(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	cases := []struct{ name, body string }{
		{"wrong phone", fmt.Sprintf(`{"submission_ref":%q,"phone":"13900139000"}`, ref)},
		{"unknown ref", `{"submission_ref":"sub_deadbeefdeadbeefdeadbeefdeadbeef","phone":"13800138000"}`},
		{"garbage ref", `{"submission_ref":"","phone":"13800138000"}`},
	}
	for _, tc := range cases {
		status, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", tc.body)
		if status != 404 {
			t.Fatalf("%s: %d %v, want uniform 404", tc.name, status, resp)
		}
	}
	lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "accepted" {
		t.Fatal("failed verification must not revoke")
	}
	// idempotent revoke: second identical call is still 200 revoked
	body := fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref)
	f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", body)
	if status, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", body); status != 200 || resp["state"] != "revoked" {
		t.Fatalf("idempotent revoke = %d %v", status, resp)
	}
}

func TestRejectedDeliveryRepairsWhenSameEventIsDelivered(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(context.Background())
	notifyID := "evt_" + ref
	f.stub.mu.Lock()
	f.stub.deliveries[notifyID] = "dead"
	f.stub.mu.Unlock()
	if tick := f.forwarder.Tick(context.Background()); tick.Rejected != 1 {
		t.Fatalf("dead tick = %+v", tick)
	}
	lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "rejected" {
		t.Fatalf("state = %q, want rejected", lead.SyncState)
	}
	f.stub.mu.Lock()
	f.stub.deliveries[notifyID] = "delivered"
	f.stub.mu.Unlock()
	if tick := f.forwarder.Tick(context.Background()); tick.Confirmed != 1 {
		t.Fatalf("repair tick = %+v", tick)
	}
	lead, _ = f.s.St.GetLeadSubmissionByRef(ref)
	if lead.SyncState != "crm_received" || lead.SyncError != "" {
		t.Fatalf("repaired = state %q err %q", lead.SyncState, lead.SyncError)
	}
}

func TestRevokeAfterRejectedEnqueuesStop(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(context.Background())
	notifyID := "evt_" + ref
	f.stub.mu.Lock()
	f.stub.deliveries[notifyID] = "dead"
	f.stub.mu.Unlock()
	f.forwarder.Tick(context.Background())
	body := fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref)
	if status, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", body); status != 200 || resp["state"] != "revoked" {
		t.Fatalf("revoke after rejected = %d %v", status, resp)
	}
	if tick := f.forwarder.Tick(context.Background()); tick.Published != 1 {
		t.Fatalf("revoke publish = %+v", tick)
	}
	events := f.stub.recorded()
	if len(events) != 2 || events[1].Profile.EventType != leads.EventLeadConsentRevoked || events[1].Profile.SourceRef != ref {
		t.Fatalf("events = %+v", events)
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 || rows[0].SyncState != "revoked" || rows[0].TenantID != f.tenA {
		t.Fatalf("rows = %+v", rows)
	}
}

// ---- 7. restart recovery -------------------------------------------------------------------

func TestLeadsRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "touch.db")
	stub := newNotifyStub(t)

	open := func() (*Server, *store.Store) {
		d, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { d.Close() })
		idsrv := fakeIdentity(t, map[string]identitySession{"sess-owner-a": {"usr_owner_a", "a@example.com"}})
		idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
		s := New(leadsTestConfig(stub.srv.URL), d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
		return s, s.St
	}

	s1, st1 := open()
	ten, err := st1.CreateTenant("商家A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st1.CreateMember(ten.ID, "usr_owner_a", "org_owner", "o", "test", true); err != nil {
		t.Fatal(err)
	}
	cmp, err := st1.CreateCampaign(store.NewCampaign{
		TenantID: ten.ID, Title: "重启活动", PublicContent: "",
		StartsAt: "2026-01-01T00:00:00Z", EndsAt: "2030-01-01T00:00:00Z", CreatedBy: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st1.TransitionCampaign(cmp.ID, ten.ID, "active"); err != nil {
		t.Fatal(err)
	}
	lnk, err := st1.CreateLink(ten.ID, cmp.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st1.UpsertLeadForm(ten.ID, cmp.ID, "v1", true, "test"); err != nil {
		t.Fatal(err)
	}

	f := &leadsFixture{s: s1, ts: httptest.NewServer(s1.Handler()), tenA: ten.ID, stub: stub, adminSession: "sess-owner-a"}
	defer f.ts.Close()
	status, out := f.submitLead(t, lnk.Code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)

	// "restart": the first server never forwarded; a new server over the same DB does
	s2, st2 := open()
	fw := leads.NewForwarder(st2, newTestPoster(stub), log.New(io.Discard, "", 0))
	if got := len(stub.recorded()); got != 0 {
		t.Fatalf("pre-restart events = %d", got)
	}
	fw.Tick(context.Background())
	events := stub.recorded()
	if len(events) != 1 || events[0].Profile.EventID != ref {
		t.Fatalf("post-restart events = %+v, want the one logical fact", events)
	}
	lead, err := st2.GetLeadSubmissionByRef(ref)
	if err != nil || lead.SyncState != "pending_sync" {
		t.Fatalf("post-restart state = %q %v", lead.SyncState, err)
	}
	// no duplicate facts on subsequent passes
	fw.Tick(context.Background())
	if got := len(stub.recorded()); got != 1 {
		t.Fatalf("duplicate facts after second tick: %d", got)
	}
	_ = s2
}

// ---- 8. anonymous view isolation -------------------------------------------------------------

func TestLeadsAnonymousViewIsolation(t *testing.T) {
	f := newLeadsFixture(t)
	for i := 0; i < 3; i++ {
		if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", `{"channel":"wecom"}`); status != 204 {
			t.Fatalf("view = %d", status)
		}
	}
	// submissions untouched: anonymous browsing never enters the contact pool
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 0 {
		t.Fatalf("contact pool polluted by views: %d", len(rows))
	}
	// admin stats keep the two counters separate
	status, _, stats := f.admin(t, "GET", "/api/v1/campaigns/"+f.campaignID+"/lead-stats", ``)
	if status != 200 || stats["submissions"] != float64(0) || stats["anonymous_views"] != float64(3) {
		t.Fatalf("stats = %d %v", status, stats)
	}
	// the aggregate table structurally has no identity columns
	cols, err := f.s.St.DB.Query(`PRAGMA table_info(public_view_stats)`)
	if err != nil {
		t.Fatal(err)
	}
	defer cols.Close()
	for cols.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt any
		if err := cols.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "code", "day", "channel", "views":
		default:
			t.Fatalf("unexpected column %q in aggregate view stats", name)
		}
	}
}

// ---- 9. rate limit ---------------------------------------------------------------------------

func TestLeadsRateLimitPerIP(t *testing.T) {
	f := newLeadsFixture(t)
	for i := 0; i < leadsRatePerMinute; i++ {
		body := strings.Replace(validLeadBody, "13800138000", fmt.Sprintf("138%08d", i), 1)
		if status, _ := f.submitLead(t, f.code, body); status != 201 {
			t.Fatalf("submission %d refused early: %d", i, status)
		}
	}
	status, out := f.submitLead(t, f.code, strings.Replace(validLeadBody, "13800138000", "15000000000", 1))
	if status != 429 || out["error"] != "rate_limited" {
		t.Fatalf("over-budget = %d %v", status, out)
	}
}

// ---- 10. admin authz --------------------------------------------------------------------------

func TestLeadsAdminAuthz(t *testing.T) {
	f := newLeadsFixture(t)
	f.submitLead(t, f.code, validLeadBody)

	// staff of the tenant can list
	status, _, list := f.do(t, "GET", "/api/v1/campaigns/"+f.campaignID+"/leads", "sess-staff-a", f.tenA, ``)
	if status != 200 || len(list["items"].([]any)) != 1 {
		t.Fatalf("staff list = %d %v", status, list)
	}
	// foreign principal is not a member of this tenant -> 403 (never auto-member)
	status, _, _ = f.do(t, "GET", "/api/v1/campaigns/"+f.campaignID+"/leads", "sess-owner-b", f.tenA, ``)
	if status != 403 {
		t.Fatalf("cross-tenant list = %d, want 403", status)
	}
	// no session -> 401
	status, _, _ = f.guest(t, "GET", "/api/v1/campaigns/"+f.campaignID+"/leads", ``)
	if status != 401 {
		t.Fatalf("guest admin list = %d, want 401", status)
	}
	// audit trail reachable for the owner
	ref := ""
	if rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID); len(rows) == 1 {
		ref = rows[0].SubmissionRef
	}
	status, _, audit := f.admin(t, "GET", "/api/v1/campaigns/"+f.campaignID+"/leads/"+ref+"/audit", ``)
	if status != 200 || len(audit["items"].([]any)) < 1 {
		t.Fatalf("audit = %d %v", status, audit)
	}
}
