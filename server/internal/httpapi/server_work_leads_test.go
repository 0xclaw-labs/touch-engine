package httpapi

// server_work_leads_test.go: HUI-4594 M11 backend — GET /api/v1/work/leads.
// Tenant-wide authorized-lead view across both capture sources with honest
// stats: 待同步(pending_sync) and 已获客(receiver-confirmed crm_received) are
// separate buckets; a pending lead is never counted as acquired.

import (
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func storeLeadViaAPI(t *testing.T, f *leadsFixture, phone string) (storeRef, storeID string) {
	t.Helper()
	code, sid := storeLane(t, f)
	if _, err := f.s.St.UpsertLeadFormForStore(f.tenA, sid, "v1", true, "test"); err != nil {
		t.Fatal(err)
	}
	status, _, out := f.guest(t, "POST", "/api/v1/public/storefronts/"+code+"/lead-submissions", storeLeadBody(phone))
	if status != 201 {
		t.Fatalf("store submit = %d %v", status, out)
	}
	return out["submission_ref"].(string), sid
}

// storedAck builds the store-level five-tuple for one lead.
func storedAck(ref string) store.LeadAck {
	return store.LeadAck{
		ReceiptID: "rcp_wl_" + ref[len(ref)-6:], SourceEventID: ref, SubmissionID: ref,
		SourceRevision: 1, ReceiverState: store.ReceiverStateStored,
	}
}

func TestWorkLeadsTenantWideHonestStats(t *testing.T) {
	f := newLeadsFixture(t)

	// campaign lead: pushed to pending_sync then confirmed by the receiver ACK
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("campaign submit = %d %v", status, out)
	}
	campRef := out["submission_ref"].(string)
	f.forwarder.Tick(t.Context())
	if _, _, err := f.s.St.AckLeadReceipt(f.tenA, campRef, leadsReceiverID, storedAck(campRef)); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// store lead: captured without any campaign, left in 待同步
	storeRef, _ := storeLeadViaAPI(t, f, "13800138001")

	// the second campaign lead stays pending (transport delivered is NOT 落库)
	status, out = f.submitLead(t, f.code, validLeadBody)
	if status != 200 { // idempotent same-day replay of the same contact
		t.Fatalf("second submit = %d %v", status, out)
	}
	status, out = f.submitLead(t, f.code, `{"name":"乙","phone":"13700137000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"web"}`)
	if status != 201 {
		t.Fatalf("pending lead submit = %d %v", status, out)
	}
	pendingRef := out["submission_ref"].(string)
	f.forwarder.Tick(t.Context())
	f.stub.DeliverAll()
	f.forwarder.Tick(t.Context())
	pending, err := f.s.St.GetLeadSubmissionByRef(pendingRef)
	if err != nil || pending.SyncState != "pending_sync" {
		t.Fatalf("pending precondition: %+v %v", pending, err)
	}

	status, _, body := f.admin(t, "GET", "/api/v1/work/leads", "")
	if status != 200 {
		t.Fatalf("work leads = %d %v", status, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (ack + store + pending)", len(items))
	}
	stats, _ := body["stats"].(map[string]any)
	if stats == nil {
		t.Fatalf("stats missing: %v", body)
	}
	if stats["pending_sync"].(float64) != 2 {
		t.Fatalf("pending_sync bucket = %v (delivered-but-unACKed must stay 待同步)", stats)
	}
	if stats["receiver_received"].(float64) != 1 {
		t.Fatalf("receiver_received must count only ACKed leads: %v", stats)
	}
	if stats["authorized_total"].(float64) != 3 || stats["revoked"].(float64) != 0 {
		t.Fatalf("totals wrong: %v", stats)
	}

	// the store lead item carries the store provenance and empty receiver state
	found := false
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["submission_ref"] == storeRef {
			found = true
			if row["capture_source"] != "store" || row["campaign_id"] != "" {
				t.Fatalf("store item = %v", row)
			}
			if row["receiver_state"] != "" || row["sync_state"] != "pending_sync" {
				t.Fatalf("store item sync = %v", row)
			}
		}
	}
	if !found {
		t.Fatalf("store lead missing from items: %v", body)
	}
}

func TestWorkLeadsRevokedBucket(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations",
		`{"submission_ref":"`+ref+`","phone":"13800138000"}`); status != 200 {
		t.Fatal("revoke failed")
	}
	status, _, body := f.admin(t, "GET", "/api/v1/work/leads", "")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	stats, _ := body["stats"].(map[string]any)
	if stats["revoked"].(float64) != 1 || stats["authorized_total"].(float64) != 1 {
		t.Fatalf("revoked bucket wrong: %v", stats)
	}
	items, _ := body["items"].([]any)
	row, _ := items[0].(map[string]any)
	if row["revoked"] != true {
		t.Fatalf("item must flag revocation: %v", row)
	}
}

func TestWorkLeadsCrossTenantIsolation(t *testing.T) {
	f := newLeadsFixture(t)
	if status, _ := f.submitLead(t, f.code, validLeadBody); status != 201 {
		t.Fatal("submit failed")
	}
	status, _, body := f.do(t, "GET", "/api/v1/work/leads", "sess-owner-b", f.tenB, "")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("cross-tenant leak: %v", body)
	}
}

func TestWorkLeadsStoreManagerSeesOwnStoreOnly(t *testing.T) {
	f := newLeadsFixture(t)
	// storeLane provisions its own (other) store; the manager is scoped to
	// baseStore, so that store-lane lead must stay invisible to them
	_, _ = storeLeadViaAPI(t, f, "13800138005")
	status, _, body := f.do(t, "GET", "/api/v1/work/leads", "sess-sm-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("manager status = %d %v", status, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("manager must not see other stores' rows: %v", body)
	}

	// a lead on the manager's own store IS visible: submit via the campaign
	// lane bound to the base store
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{
		TenantID: f.tenA, Title: "本店活动", PublicContent: "内容",
		StartsAt: "2026-01-01T00:00:00Z", EndsAt: "2030-01-01T00:00:00Z",
		StoreID: f.baseStoreID, CreatedBy: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.TransitionCampaign(camp.ID, f.tenA, "active"); err != nil {
		t.Fatal(err)
	}
	lnk, err := f.s.St.CreateLink(f.tenA, camp.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.UpsertLeadForm(f.tenA, camp.ID, "v1", true, "test"); err != nil {
		t.Fatal(err)
	}
	status, _, out := f.guest(t, "POST", "/api/v1/public/links/"+lnk.Code+"/lead-submissions", validLeadBody)
	if status != 201 {
		t.Fatalf("own-store submit = %d %v", status, out)
	}
	status, _, body = f.do(t, "GET", "/api/v1/work/leads", "sess-sm-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("manager status = %d", status)
	}
	items, _ = body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("manager must see own-store rows: %v", body)
	}
}

func TestWorkLeadsFeatureOffIsInvisible(t *testing.T) {
	cfg := leadsTestConfig("http://notify.test")
	cfg.FeatureLeadsCapture = false
	f := newLeadsFixtureWithConfig(t, cfg)
	status, _, body := f.admin(t, "GET", "/api/v1/work/leads", "")
	if status != 404 {
		t.Fatalf("feature off = %d %v, want uniform 404", status, body)
	}
}
