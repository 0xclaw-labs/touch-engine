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

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// HUI-1671 B3 reward-ops HTTP surface. The /work admin surface has no feature
// flag (仓内惯例); the guest-reachable public summary is gated by
// FEATURE_REWARD_OPS (default off = invisible surface); the trusted
// reservation endpoint is internal-token only.

type rewardFixture struct {
	s         *Server
	ts        *httptest.Server
	tenA      string
	campaignA string
	storeA    string
	invID     string
	linkCode  string
}

func rewardHTTPFixture(t *testing.T, rewardOps bool) *rewardFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("奖励商家A")
	mustNoErr(t, err)
	storeA, err := st.CreateStore(tenA.ID, "旗舰店", "某地", "test")
	mustNoErr(t, err)
	storeB, err := st.CreateStore(tenA.ID, "二店", "别处", "test")
	mustNoErr(t, err)
	for _, m := range []struct {
		tenant, principal, role, scope string
	}{
		{tenA.ID, "usr_owner_a", "org_owner", ""},
		{tenA.ID, "usr_staff_a", "staff", ""},
		{tenA.ID, "usr_mgr_a", "store_manager", storeA.ID},
		{tenA.ID, "usr_mgr_b", "store_manager", storeB.ID},
		{tenA.ID, "usr_off_a", "staff", ""},
		{tenA.ID, "usr_agt_a", "agent", ""},
	} {
		if _, err := st.CreateMemberScoped(m.tenant, m.principal, m.role, m.principal, "test", true, m.scope); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	// A disabled staff member (撤权负例).
	if _, err := st.UpdateMember(mustMemberID(t, st, tenA.ID, "usr_off_a"), nil, boolPtr(false), nil); err != nil {
		t.Fatalf("disable member: %v", err)
	}
	// A disabled staff member (撤权负例).
	if _, err := st.UpdateMember(mustMemberID(t, st, tenA.ID, "usr_off_a"), nil, boolPtr(false), nil); err != nil {
		t.Fatalf("disable member: %v", err)
	}

	campA, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, Title: "碰一碰活动", StoreID: storeA.ID, CreatedBy: "test"})
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if _, err := st.TransitionCampaign(campA.ID, tenA.ID, campaign.StatusActive); err != nil {
		t.Fatalf("activate campaign: %v", err)
	}
	link, err := st.CreateLink(tenA.ID, campA.ID, "test")
	mustNoErr(t, err)
	inv, err := st.CreateRewardInventory(tenA.ID, store.NewRewardInventory{
		CampaignID: campA.ID, Kind: store.RewardKindStoreCoupon, Name: "开业券",
		BenefitSlot: "first_publish", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission", "staff_verified_post"},
		TotalQty:        10, FaceValueMinor: 500, CostMinor: 300, Currency: "FEN",
		StoreID: storeA.ID, CreatedBy: "usr_owner_a",
	})
	mustNoErr(t, err)

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_REWARD_OPS":
			if rewardOps {
				return "on"
			}
			return "off"
		}
		return ""
	})
	sessions := map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-mgr-a":   {"usr_mgr_a", "mgr@example.com"},
		"sess-mgr-b":   {"usr_mgr_b", "mgrb@example.com"},
		"sess-off-a":   {"usr_off_a", "off@example.com"},
		"sess-agt-a":   {"usr_agt_a", "agt@example.com"},
	}
	idsrv := fakeIdentity(t, sessions)
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &rewardFixture{s: s, ts: ts, tenA: tenA.ID, campaignA: campA.ID, storeA: storeA.ID, invID: inv.ID, linkCode: link.Code}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func mustMemberID(t *testing.T, st *store.Store, tenantID, principal string) string {
	t.Helper()
	m, err := st.GetMemberByPrincipal(tenantID, principal)
	if err != nil {
		t.Fatalf("member %s: %v", principal, err)
	}
	return m.ID
}

// doR posts an authenticated admin request; session "" means no session.
func (f *rewardFixture) doR(t *testing.T, method, path, session, body string) (int, map[string]any) {
	t.Helper()
	return f.doRWithTenant(t, method, path, session, f.tenA, body)
}

func (f *rewardFixture) doRWithTenant(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
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
	return res.StatusCode, out
}

func (f *rewardFixture) confirmBody(participation, evidence string, fulfill bool) string {
	return fmt.Sprintf(`{"participation_id":%q,"benefit_slot":"first_publish","rule_version":1,`+
		`"event_id":"cfm-%s","evidence":%q,"store_id":%q,"fulfill_now":%t,"redeem_event_id":"rdm-%s",`+
		`"note":"店员现场确认"}`, participation, participation, evidence, f.storeA, fulfill, participation)
}

func TestRewardInventoryAdminSurface(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	// Owner lists the seeded batch.
	code, out := f.doR(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-owner-a", "")
	if code != 200 {
		t.Fatalf("owner list inventory: %d %v", code, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["available_qty"].(float64) != 10 {
		t.Fatalf("seeded batch: %v", out)
	}
	// Staff can list but not create (budget stays org_owner).
	code, _ = f.doR(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-staff-a", "")
	if code != 200 {
		t.Fatalf("staff list inventory: %d", code)
	}
	body := fmt.Sprintf(`{"kind":"store_coupon","name":"加赠券","benefit_slot":"second_publish",`+
		`"rule_version":1,"allowed_evidence":["staff_observed_submission"],"total_qty":5,`+
		`"face_value_minor":100,"cost_minor":50,"currency":"FEN"}`)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-staff-a", body)
	if code != 403 {
		t.Fatalf("staff create inventory must 403: %d %v", code, out)
	}
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-owner-a", body)
	if code != 200 {
		t.Fatalf("owner create inventory: %d %v", code, out)
	}
	// Invalid kind refused; unknown evidence refused.
	bad := strings.Replace(body, `"store_coupon"`, `"cash"`, 1)
	if code, _ = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-owner-a", bad); code != 422 {
		t.Fatalf("invalid kind must 422: %d", code)
	}
	// A principal with no membership in the target tenant never reaches the
	// surface (middleware fails closed before any campaign lookup).
	code, _ = f.doRWithTenant(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-owner-a", "ten-foreign", "")
	if code != 403 {
		t.Fatalf("foreign tenant membership must 403, got %d", code)
	}
	// Pause needs org_owner; unknown status refused.
	code, _ = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory/"+f.invID+"/status", "sess-staff-a", `{"status":"paused"}`)
	if code != 403 {
		t.Fatalf("staff pause must 403: %d", code)
	}
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory/"+f.invID+"/status", "sess-owner-a", `{"status":"paused"}`)
	if code != 200 || out["status"] != "paused" {
		t.Fatalf("pause: %d %v", code, out)
	}
	// A paused batch stops new promises.
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", f.confirmBody("p-paused", "staff_observed_submission", false))
	if code != 422 {
		t.Fatalf("confirm on paused batch must 422: %d %v", code, out)
	}
}

func TestRewardConfirmAuthMatrix(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	path := "/api/v1/campaigns/" + f.campaignA + "/reward-confirm"
	cases := []struct {
		name    string
		session string
		want    int
	}{
		{"staff ok", "sess-staff-a", 200},
		{"owner ok", "sess-owner-a", 200},
		{"no session", "", 401},
		{"disabled member", "sess-off-a", 403},
		{"agent", "sess-agt-a", 403},
	}
	for i, tc := range cases {
		part := fmt.Sprintf("p-auth-%d", i)
		code, out := f.doR(t, "POST", path, tc.session, f.confirmBody(part, "staff_observed_submission", false))
		if code != tc.want {
			t.Fatalf("%s: got %d want %d (%v)", tc.name, code, tc.want, out)
		}
	}
	// Store manager of the right store passes...
	mgrBody := f.confirmBody("p-mgr", "staff_verified_post", false)
	code, out := f.doR(t, "POST", path, "sess-mgr-a", mgrBody)
	if code != 200 {
		t.Fatalf("own-store manager confirm: %d %v", code, out)
	}
	// ...but a manager asked to act on another store is out of scope (403
	// from the authz matrix)...
	wrongStore := strings.Replace(mgrBody, f.storeA, "sto-elsewhere", 1)
	code, out = f.doR(t, "POST", path, "sess-mgr-a", wrongStore)
	if code != 403 {
		t.Fatalf("wrong-store manager must 403: %d %v", code, out)
	}
	// ...and even a tenant-level role cannot confirm a store-scoped batch at
	// a store it does not cover (wrong store is refused by the ledger; the
	// fresh event id keeps this a real adjudication, not a replay).
	ownerWrong := strings.Replace(wrongStore, `"cfm-p-mgr"`, `"cfm-owner-wrongstore"`, 1)
	code, out = f.doR(t, "POST", path, "sess-owner-a", ownerWrong)
	if code != 422 || !strings.Contains(fmt.Sprint(out), "store_mismatch") {
		t.Fatalf("wrong-store confirm must 422: %d %v", code, out)
	}
}

func TestRewardConfirmIssAndRedeemFacts(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	path := "/api/v1/campaigns/" + f.campaignA + "/reward-confirm"
	// Plain confirm issues one fact.
	code, out := f.doR(t, "POST", path, "sess-staff-a", f.confirmBody("p-1", "staff_observed_submission", false))
	if code != 200 {
		t.Fatalf("confirm: %d %v", code, out)
	}
	ben := out["benefit"].(map[string]any)
	if ben["status"] != "issued" || ben["code"] == "" || ben["face_value_minor"].(float64) != 500 {
		t.Fatalf("issued benefit shape: %v", ben)
	}
	// A second staff picking the other allowed evidence reads back the same
	// benefit; no second reward.
	other := f.confirmBody("p-1", "staff_verified_post", false)
	other = strings.Replace(other, `"cfm-p-1"`, `"cfm-p-1-staff2"`, 1)
	code, out = f.doR(t, "POST", path, "sess-owner-a", other)
	if code != 200 {
		t.Fatalf("second evidence confirm: %d %v", code, out)
	}
	if out["benefit"].(map[string]any)["id"] != ben["id"] || out["outcome"] != "already_issued" {
		t.Fatalf("double evidence must read back: %v", out)
	}
	// Fulfil-now posts issue+redeem in one operation.
	code, out = f.doR(t, "POST", path, "sess-staff-a", f.confirmBody("p-2", "staff_verified_post", true))
	if code != 200 {
		t.Fatalf("fulfil now: %d %v", code, out)
	}
	if out["benefit"].(map[string]any)["status"] != "redeemed" || out["outcome"] != "issued_and_redeemed" {
		t.Fatalf("fulfil-now shape: %v", out)
	}
	// Summary reflects the two facts and the evidence mix.
	code, out = f.doR(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-summary", "sess-staff-a", "")
	if code != 200 {
		t.Fatalf("summary: %d %v", code, out)
	}
	counts := out["counts"].(map[string]any)
	if counts["issued"].(float64) != 1 || counts["redeemed"].(float64) != 1 {
		t.Fatalf("summary counts: %v", counts)
	}
	ev := out["evidence_granted"].(map[string]any)
	if ev["staff_observed_submission"].(float64) != 1 || ev["staff_verified_post"].(float64) != 1 {
		t.Fatalf("evidence stats: %v", ev)
	}
}

func TestRewardRedeemEndpoint(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	confirmPath := "/api/v1/campaigns/" + f.campaignA + "/reward-confirm"
	code, out := f.doR(t, "POST", confirmPath, "sess-staff-a", f.confirmBody("p-1", "staff_observed_submission", false))
	mustCode(t, code, 200, out)
	benID := out["benefit"].(map[string]any)["id"].(string)
	redeemPath := fmt.Sprintf("/api/v1/campaigns/%s/reward-benefits/%s/redeem", f.campaignA, benID)
	body := fmt.Sprintf(`{"event_id":"rdm-staff-b","store_id":%q}`, f.storeA)
	// Staff B redeems: exactly one wins.
	code, out = f.doR(t, "POST", redeemPath, "sess-staff-a", body)
	mustCode(t, code, 200, out)
	if out["outcome"] != "redeemed" {
		t.Fatalf("first redeem: %v", out)
	}
	// A second staff member reads back the processed result (回读, not 409).
	second := fmt.Sprintf(`{"event_id":"rdm-staff-c","store_id":%q}`, f.storeA)
	code, out = f.doR(t, "POST", redeemPath, "sess-owner-a", second)
	mustCode(t, code, 200, out)
	if out["outcome"] != "already_redeemed" {
		t.Fatalf("second redeem read-back: %v", out)
	}
	// Reserved benefits cannot be redeemed directly.
	code, out = f.doR(t, "POST", "/internal/v1/reward-reservations", "", fmt.Sprintf(
		`{"tenant_id":%q,"campaign_id":%q,"participation_id":"p-res","benefit_slot":"first_publish",`+
			`"rule_version":1,"event_id":"res-1","ttl_seconds":600,"actor":"participation-service"}`, f.tenA, f.campaignA))
	mustCode(t, code, 200, out)
	bens, out := f.doR(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits", "sess-staff-a", "")
	mustCode(t, bens, 200, out)
	var reservedID string
	for _, b := range out["items"].([]any) {
		bm := b.(map[string]any)
		if bm["participation_id"] == "p-res" {
			reservedID = bm["id"].(string)
		}
	}
	code, out = f.doR(t, "POST", fmt.Sprintf("/api/v1/campaigns/%s/reward-benefits/%s/redeem", f.campaignA, reservedID),
		"sess-staff-a", fmt.Sprintf(`{"event_id":"rdm-res","store_id":%q}`, f.storeA))
	if code != 409 {
		t.Fatalf("reserved redeem must 409: %d %v", code, out)
	}
	// A manager of another store finds the whole campaign out of scope
	// (campaignScoped masks out-of-scope as 404, 仓内既有惯例).
	code, _ = f.doR(t, "POST", fmt.Sprintf("/api/v1/campaigns/%s/reward-benefits/%s/redeem", f.campaignA, benID),
		"sess-mgr-b", fmt.Sprintf(`{"event_id":"rdm-mgr-b","store_id":%q}`, f.storeA))
	if code != 404 {
		t.Fatalf("other-store manager redeem must be masked 404: %d", code)
	}
}

func TestRewardReverseMatrix(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	code, out := f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a",
		f.confirmBody("p-1", "staff_observed_submission", false))
	mustCode(t, code, 200, out)
	benID := out["benefit"].(map[string]any)["id"].(string)
	revPath := fmt.Sprintf("/api/v1/campaigns/%s/reward-benefits/%s/reverse", f.campaignA, benID)
	// Staff cannot reverse (预算/更正是 owner 面).
	code, _ = f.doR(t, "POST", revPath, "sess-staff-a", `{"reason":"误审","event_id":"rvs-1"}`)
	if code != 403 {
		t.Fatalf("staff reverse must 403: %d", code)
	}
	// Reason is mandatory.
	code, _ = f.doR(t, "POST", revPath, "sess-owner-a", `{"reason":"","event_id":"rvs-2"}`)
	if code != 400 {
		t.Fatalf("missing reason must 400: %d", code)
	}
	code, out = f.doR(t, "POST", revPath, "sess-owner-a", `{"reason":"误审撤销","event_id":"rvs-3"}`)
	mustCode(t, code, 200, out)
	if out["benefit"].(map[string]any)["status"] != "reversed" {
		t.Fatalf("reverse: %v", out)
	}
	// Confirming again (a fresh event) never re-issues a reversed slot.
	again := f.confirmBody("p-1", "staff_verified_post", false)
	again = strings.Replace(again, `"cfm-p-1"`, `"cfm-p-1-again"`, 1)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", again)
	if code != 409 || !strings.Contains(fmt.Sprint(out), "slot_reversed") {
		t.Fatalf("reversed slot re-confirm: %d %v", code, out)
	}
}

func TestRewardOfficialOnlyOverHTTP(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	// An official-only gift batch.
	body := fmt.Sprintf(`{"kind":"gift_discount","name":"官方礼","benefit_slot":"official_gift",`+
		`"rule_version":1,"allowed_evidence":["official_publish_confirmed"],"total_qty":5,`+
		`"face_value_minor":0,"cost_minor":200,"currency":"FEN"}`)
	code, out := f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-inventory", "sess-owner-a", body)
	mustCode(t, code, 200, out)
	// Staff evidence cannot satisfy it.
	staff := fmt.Sprintf(`{"participation_id":"p-o1","benefit_slot":"official_gift","rule_version":1,`+
		`"event_id":"cfm-o1","evidence":"staff_observed_submission","store_id":%q}`, f.storeA)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", staff)
	if code != 422 || !strings.Contains(fmt.Sprint(out), "official_evidence_required") {
		t.Fatalf("official-only staff evidence: %d %v", code, out)
	}
	// A typed receipt is fabricated: no server-side fact.
	forged := fmt.Sprintf(`{"participation_id":"p-o2","benefit_slot":"official_gift","rule_version":1,`+
		`"event_id":"cfm-o2","evidence":"official_publish_confirmed","store_id":%q,`+
		`"official":{"fact_id":"att-nope","platform_post_id":"999","receipt_source":"official_query"}}`, f.storeA)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", forged)
	if code != 422 || !strings.Contains(fmt.Sprint(out), "fabricated_official_receipt") {
		t.Fatalf("forged receipt: %d %v", code, out)
	}
	// Unknown evidence class refused.
	weird := fmt.Sprintf(`{"participation_id":"p-o3","benefit_slot":"official_gift","rule_version":1,`+
		`"event_id":"cfm-o3","evidence":"self_report","store_id":%q}`, f.storeA)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", weird)
	if code != 422 || !strings.Contains(fmt.Sprint(out), "unknown_evidence") {
		t.Fatalf("unknown evidence: %d %v", code, out)
	}
	// Failed confirmations leak no coupon code.
	if strings.Contains(fmt.Sprint(out), "SC") {
		t.Fatalf("failure response must not carry codes: %v", out)
	}
}

func TestRewardReviewRequestsEndpoint(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	// Seed one self-reported attempt for the campaign.
	mustNoErr(t, f.s.St.NotePublishAdapter(f.tenA, "douyin", "adapter-x"))
	att, err := f.s.St.CreateCustomerPublishPreview(f.tenA, f.campaignA, f.linkCode,
		custpublish.Attempt{
			Platform: "douyin", Publisher: custpublish.PublisherActivityCustomer,
			Copy: "顾客自报内容", AccountLabel: "acc", ContentVersion: "v1",
			Status: custpublish.StatusPreviewed,
		})
	mustNoErr(t, err)
	if _, err := f.s.St.MarkCustomerPublishSelfReport(att.ID, f.tenA, ""); err != nil {
		t.Fatalf("self report: %v", err)
	}
	code, out := f.doR(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-review-requests", "sess-staff-a", "")
	mustCode(t, code, 200, out)
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["attempt_id"] != att.ID {
		t.Fatalf("review queue: %v", out)
	}
	// Confirming drains the queue.
	body := fmt.Sprintf(`{"participation_id":%q,"benefit_slot":"first_publish","rule_version":1,`+
		`"event_id":"cfm-sr","evidence":"staff_verified_post","store_id":%q}`, att.ID, f.storeA)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", body)
	mustCode(t, code, 200, out)
	code, out = f.doR(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-review-requests", "sess-staff-a", "")
	mustCode(t, code, 200, out)
	if len(out["items"].([]any)) != 0 {
		t.Fatalf("queue after confirm: %v", out)
	}
}

func TestRewardReservationsTrustedEndpoint(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	body := fmt.Sprintf(`{"tenant_id":%q,"campaign_id":%q,"participation_id":"p-1",`+
		`"benefit_slot":"first_publish","rule_version":1,"event_id":"res-1","ttl_seconds":600,`+
		`"actor":"participation-service"}`, f.tenA, f.campaignA)
	// Internal token is mandatory (fixture doR always sends it; use raw
	// request without the header for the negative case).
	req, err := http.NewRequest("POST", f.ts.URL+"/internal/v1/reward-reservations", strings.NewReader(body))
	mustNoErr(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("reservation without internal token must 401, got %d %s", res.StatusCode, raw)
	}
	// Trusted call reserves atomically and shows the TTL.
	code, out := f.doR(t, "POST", "/internal/v1/reward-reservations", "", body)
	mustCode(t, code, 200, out)
	ben := out["benefit"].(map[string]any)
	if ben["status"] != "reserved" || ben["reserved_until"] == "" {
		t.Fatalf("reserved shape: %v", out)
	}
	// Repeating the same event answers without a second allocation.
	code, out = f.doR(t, "POST", "/internal/v1/reward-reservations", "", body)
	mustCode(t, code, 200, out)
	if out["replay"] != true {
		t.Fatalf("reservation replay: %v", out)
	}
	// A second participation reserves independently (stock is generous here;
	// the last-unit race is covered at the store layer and in
	// TestRewardInventoryAdminSurface via the paused batch).
	other := strings.Replace(body, `"p-1"`, `"p-2"`, 1)
	other = strings.Replace(other, `"res-1"`, `"res-2"`, 1)
	code, out = f.doR(t, "POST", "/internal/v1/reward-reservations", "", other)
	mustCode(t, code, 200, out)
	// TTL is mandatory (明示).
	noTTL := strings.Replace(body, `"ttl_seconds":600`, `"ttl_seconds":0`, 1)
	code, _ = f.doR(t, "POST", "/internal/v1/reward-reservations", "", noTTL)
	if code != 422 {
		t.Fatalf("missing TTL must be refused: %d", code)
	}
}

func TestRewardPublicSummaryGate(t *testing.T) {
	// Off: the route does not exist.
	f := rewardHTTPFixture(t, false)
	req, err := http.NewRequest("GET", f.ts.URL+"/api/v1/public/links/"+f.linkCode+"/reward-summary", nil)
	mustNoErr(t, err)
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("off must be invisible, got %d %s", res.StatusCode, raw)
	}
	// On: whitelisted counts only — no codes, no face, no cost.
	f2 := rewardHTTPFixture(t, true)
	_, out := f2.doR(t, "POST", "/api/v1/campaigns/"+f2.campaignA+"/reward-confirm", "sess-staff-a",
		f2.confirmBody("p-1", "staff_observed_submission", false))
	code, out := f2.doR(t, "GET", "/api/v1/public/links/"+f2.linkCode+"/reward-summary", "", "")
	if code != 200 {
		t.Fatalf("public summary on: %d %v", code, out)
	}
	counts := out["counts"].(map[string]any)
	if counts["issued"].(float64) != 1 {
		t.Fatalf("public counts: %v", counts)
	}
	dump := fmt.Sprint(out)
	for _, banned := range []string{"code", "face_value", "cost_minor", "token"} {
		if strings.Contains(dump, banned) {
			t.Fatalf("public summary must not carry %q: %v", banned, out)
		}
	}
	// An unknown short code is a plain 404 state, never an error shell.
	req, err = http.NewRequest("GET", f2.ts.URL+"/api/v1/public/links/nosuchcode/reward-summary", nil)
	mustNoErr(t, err)
	res, err = f2.ts.Client().Do(req)
	mustNoErr(t, err)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("unknown code must 404, got %d", res.StatusCode)
	}
}

func mustCode(t *testing.T, got, want int, out map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %d want %d: %v", got, want, out)
	}
}
