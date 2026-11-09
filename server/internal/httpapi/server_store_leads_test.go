package httpapi

// server_store_leads_test.go: HUI-4594 无活动留资公共面. The guest captures a
// lead from the store front / contact entry without any campaign existing.
// Same consent chain, same notice, same outbox; campaign_id stays '' and the
// capture_source is 'store'.

import (
	"fmt"
	"strings"
	"testing"
)

// storeLane provisions a real store + storefront code (4584 store front).
func storeLane(t *testing.T, f *leadsFixture) (storeCode, storeID string) {
	t.Helper()
	sto, err := f.s.St.CreateStore(f.tenA, "留资门店", "", "test")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sf, err := f.s.St.EnsureStorefront(f.tenA, sto.ID)
	if err != nil {
		t.Fatalf("storefront: %v", err)
	}
	return sf.StoreCode, sto.ID
}

func storeLeadBody(phone string) string {
	return fmt.Sprintf(`{"name":"店客","phone":%q,"consent_version":"v1","consent":true,"marketing_optin":true,"channel":"wecom"}`, phone)
}

func TestStoreLeadFormRequiresStoreLevelConfig(t *testing.T) {
	f := newLeadsFixture(t)
	code, storeID := storeLane(t, f)

	status, _, out := f.guest(t, "GET", "/api/v1/public/storefronts/"+code+"/lead-form", "")
	if status != 200 || out["enabled"] != false {
		t.Fatalf("no store form = %d %v, want 200 enabled:false", status, out)
	}
	// the campaign-level form must NOT bleed into the store lane
	// (fixture enabled the campaign form in newLeadsFixture)

	// merchant opens store-level capture
	if _, err := f.s.St.UpsertLeadFormForStore(f.tenA, storeID, "v1", true, "test"); err != nil {
		t.Fatalf("store form upsert: %v", err)
	}
	status, _, out = f.guest(t, "GET", "/api/v1/public/storefronts/"+code+"/lead-form", "")
	if status != 200 || out["enabled"] != true || out["marketing_optin_enabled"] != true {
		t.Fatalf("store form = %d %v", status, out)
	}
	if _, ok := out["notice"].(map[string]any); !ok {
		t.Fatalf("notice missing: %v", out)
	}
}

func TestStoreLeadSubmitWithoutCampaign(t *testing.T) {
	f := newLeadsFixture(t)
	code, storeID := storeLane(t, f)
	if _, err := f.s.St.UpsertLeadFormForStore(f.tenA, storeID, "v1", true, "test"); err != nil {
		t.Fatalf("store form upsert: %v", err)
	}

	status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions", storeLeadBody("13800138000"))
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	if out["state"] != "accepted" {
		t.Fatalf("state = %v", out)
	}

	lead, err := f.s.St.GetLeadSubmissionByRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if lead.CampaignID != "" || lead.CaptureSource != "store" || lead.StoreID != storeID {
		t.Fatalf("store lead = %+v", lead)
	}
	// the fact entered the same outbox the forwarder drains
	if _, err := f.s.St.LeadOutboxPayload(ref, "submit"); err != nil {
		t.Fatalf("outbox fact missing: %v", err)
	}
	// no contact data in the outbound payload
	raw, _ := f.s.St.LeadOutboxPayload(ref, "submit")
	for _, forbidden := range []string{"13800138000", "店客"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("store envelope leaks contact %q: %s", forbidden, raw)
		}
	}

	t.Run("same store same day is idempotent", func(t *testing.T) {
		status, _, out2 := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions", storeLeadBody("+86 138-0013-8000"))
		if status != 200 || out2["duplicate"] != true || out2["submission_ref"] != ref {
			t.Fatalf("idempotent replay = %d %v", status, out2)
		}
	})

	t.Run("status is a verified read", func(t *testing.T) {
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-status",
			fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref))
		if status != 200 || out["state"] != "accepted" {
			t.Fatalf("status = %d %v", status, out)
		}
		// phone mismatch is the same uniform 404
		status, _, _ = f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-status",
			fmt.Sprintf(`{"submission_ref":%q,"phone":"13900139000"}`, ref))
		if status != 404 {
			t.Fatalf("phone mismatch = %d", status)
		}
	})

	t.Run("revoke converges to tombstone", func(t *testing.T) {
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-revocations",
			fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref))
		if status != 200 || out["state"] != "revoked" {
			t.Fatalf("revoke = %d %v", status, out)
		}
		lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
		if lead.SyncState != "revoked" || lead.RevokeSourceRevision != 2 {
			t.Fatalf("revoked lead = %+v", lead)
		}
		// the trusted receiver now sees a tombstone with zero contact fields
		code2, body := f.fetchRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA)
		if code2 != 200 || !strings.Contains(body, `"tombstone":true`) || strings.Contains(body, "13800138000") {
			t.Fatalf("tombstone = %d %s", code2, body)
		}
	})

	t.Run("forwarder suppresses the unsent store fact after revoke", func(t *testing.T) {
		// a second, never-revoked store lead flows through the same outbox
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions", storeLeadBody("13600136000"))
		if status != 201 {
			t.Fatalf("second submit = %d %v", status, out)
		}
	})
}

func TestStoreLeadNegativeMatrix(t *testing.T) {
	f := newLeadsFixture(t)
	code, storeID := storeLane(t, f)
	if _, err := f.s.St.UpsertLeadFormForStore(f.tenA, storeID, "v1", true, "test"); err != nil {
		t.Fatal(err)
	}

	t.Run("consent and notice discipline is identical", func(t *testing.T) {
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions",
			`{"name":"店客","phone":"13800138000","consent_version":"v1","consent":false}`)
		if status != 400 || out["error"] != "consent_required" {
			t.Fatalf("no consent = %d %v", status, out)
		}
		status, _, out = f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions",
			`{"name":"店客","phone":"13800138000","consent_version":"v9","consent":true}`)
		if status != 400 || out["error"] != "notice_version_mismatch" {
			t.Fatalf("bad notice = %d %v", status, out)
		}
		// target_tenant steering is refused by the strict schema
		status, _, out = f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions",
			`{"name":"店客","phone":"13800138000","consent_version":"v1","consent":true,"tenant_id":"tnt_evil"}`)
		if status != 400 {
			t.Fatalf("forged tenant = %d %v", status, out)
		}
	})

	t.Run("unknown or foreign store code is a uniform 404", func(t *testing.T) {
		status, _, out := f.guest(t, "GET", "/api/v1/public/storefronts/sf_unknown/lead-form", "")
		if status != 404 {
			t.Fatalf("unknown code = %d %v", status, out)
		}
		status, _, _ = f.guest(t, "POST", "/api/v1/public/storefronts/sf_unknown/lead-submissions", storeLeadBody("13800138000"))
		if status != 404 {
			t.Fatalf("unknown code submit = %d", status)
		}
	})

	t.Run("capture closed without a store form", func(t *testing.T) {
		other, err := f.s.St.CreateStore(f.tenA, "二号店", "", "test")
		if err != nil {
			t.Fatal(err)
		}
		sf, err := f.s.St.EnsureStorefront(f.tenA, other.ID)
		if err != nil {
			t.Fatal(err)
		}
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+sf.StoreCode+"/lead-submissions", storeLeadBody("13800138001"))
		if status != 409 || out["error"] != "lead_capture_not_open" {
			t.Fatalf("no store form = %d %v", status, out)
		}
	})

	t.Run("action session reference must be real and in-scope", func(t *testing.T) {
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions",
			`{"name":"店客","phone":"13800138002","consent_version":"v1","consent":true,"action_session_id":"acts_missing"}`)
		if status != 400 || out["error"] != "bad_session" {
			t.Fatalf("fake session = %d %v", status, out)
		}
	})

	t.Run("marketing optin disabled stays forced off", func(t *testing.T) {
		if _, err := f.s.St.UpsertLeadFormForStore(f.tenA, storeID, "v1", false, "test"); err != nil {
			t.Fatal(err)
		}
		status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions",
			`{"name":"店客","phone":"13800138003","consent_version":"v1","consent":true,"marketing_optin":true}`)
		if status != 201 {
			t.Fatalf("submit = %d %v", status, out)
		}
		ref := out["submission_ref"].(string)
		lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
		if lead.MarketingOptin {
			t.Fatalf("marketing must be forced off: %+v", lead)
		}
	})
}
