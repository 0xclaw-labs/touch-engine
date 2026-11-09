package store

// store_lead_store_capture_test.go: HUI-4594 无活动留资(门店维度). A guest can
// leave contact data from the store front / contact entry without any campaign
// existing. campaign_id is '' on those rows; the campaign FK is gone (0090
// rebuild). Forms are store-scoped rows in the same lead_forms table.

import (
	"testing"
)

func TestStoreLeadFormRoundTrip(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, sto, _, _ := seedActiveCampaign(t, s)

	if _, err := s.GetLeadFormByStore(sto); err == nil {
		t.Fatalf("no form expected before upsert")
	}
	f, err := s.UpsertLeadFormForStore(ten, sto, "v1", true, "test")
	mustNoErr(t, err)
	if !f.Enabled || f.NoticeVersion != "v1" || !f.MarketingOptinEnabled {
		t.Fatalf("form=%+v", f)
	}
	got, err := s.GetLeadFormByStore(sto)
	mustNoErr(t, err)
	if got.ID != f.ID {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, f)
	}
	// upsert again: still exactly one row per store
	if _, err := s.UpsertLeadFormForStore(ten, sto, "v1", false, "test"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	n, err := s.countStoreForms(sto)
	mustNoErr(t, err)
	if n != 1 {
		t.Fatalf("store form must be one row per store, got %d", n)
	}
	// campaign-scoped lookups stay blind to store rows
	if _, err := s.GetLeadFormByCampaign(sto); err == nil {
		t.Fatalf("store row must not be reachable by campaign lookup")
	}
}

func TestSubmitLeadWithoutCampaign(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, sto, _, _ := seedActiveCampaign(t, s)

	l, dup, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: "", StoreID: sto,
		CaptureSource: "store", SessionID: "acts_1",
		SubmissionRef: "sub_store_1", DedupKey: "dk_store_1",
		Name: "李四", Phone: "13900139000",
		NoticeVersion: "v1", ConsentAt: "2026-11-10T00:00:00Z",
		OutboxEventID: "ev-sub_store_1", OutboxPayload: `{"event_profile":{"event_id":"sub_store_1"}}`,
	})
	mustNoErr(t, err)
	if dup || l.CampaignID != "" || l.CaptureSource != "store" || l.SessionID != "acts_1" {
		t.Fatalf("store lead=%+v dup=%v", l, dup)
	}
	// the outbox fact exists (the forwarder queue is the same one)
	if _, err := s.LeadOutboxPayload("sub_store_1", "submit"); err != nil {
		t.Fatalf("outbox fact missing: %v", err)
	}
	// campaign rows keep their provenance
	l2, _, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: "cmp_x", CaptureSource: "campaign",
		SubmissionRef: "sub_camp_1", DedupKey: "dk_camp_1",
		Name: "王五", Phone: "13700137000",
		NoticeVersion: "v1", ConsentAt: "2026-11-10T00:00:00Z",
		OutboxEventID: "ev-sub_camp_1", OutboxPayload: `{"event_profile":{"event_id":"sub_camp_1"}}`,
	})
	mustNoErr(t, err)
	if l2.CampaignID != "cmp_x" || l2.CaptureSource != "campaign" {
		t.Fatalf("campaign lead=%+v", l2)
	}
}

func TestListLeadsByTenantSpansBothSources(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, sto, _, _ := seedActiveCampaign(t, s)
	other, err := s.CreateTenant("别家T")
	mustNoErr(t, err)

	mk := func(ref, dk, campaign, source string) {
		t.Helper()
		_, _, err := s.SubmitLead(NewLeadSubmission{
			TenantID: ten, CampaignID: campaign, StoreID: sto, CaptureSource: source,
			SubmissionRef: ref, DedupKey: dk,
			Name: "联系人", Phone: "1380000000" + ref[len(ref)-1:],
			NoticeVersion: "v1", ConsentAt: "2026-11-10T00:00:00Z",
			OutboxEventID: "ev-" + ref, OutboxPayload: `{"event_profile":{"event_id":"` + ref + `"}}`,
		})
		if err != nil {
			t.Fatalf("submit %s: %v", ref, err)
		}
	}
	mk("sub_a1", "dk_a1", "cmp_x", "campaign")
	mk("sub_a2", "dk_a2", "", "store")
	mk("sub_a3", "dk_a3", "", "store")
	// another tenant's rows never come back
	_, _, err2 := s.SubmitLead(NewLeadSubmission{
		TenantID: other.ID, CampaignID: "cmp_y", CaptureSource: "campaign",
		SubmissionRef: "sub_b1", DedupKey: "dk_b1",
		Name: "外人", Phone: "13600136000",
		NoticeVersion: "v1", ConsentAt: "2026-11-10T00:00:00Z",
		OutboxEventID: "ev-sub_b1", OutboxPayload: `{}`,
	})
	mustNoErr(t, err2)

	items, err := s.ListLeadsByTenant(ten, 50)
	mustNoErr(t, err)
	if len(items) != 3 {
		t.Fatalf("want 3 tenant rows, got %d", len(items))
	}
	for _, it := range items {
		if it.TenantID != ten {
			t.Fatalf("cross-tenant leak: %+v", it)
		}
	}

	// ACK one row and count states: pending_sync must stay its own bucket
	if err := s.MarkLeadSyncPending("sub_a1"); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := s.MarkOutboxForwarded("ev-sub_a1", "ntf_a1"); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if _, _, err := s.AckLeadReceipt(ten, "sub_a1", "leads-ingest", LeadAck{
		ReceiptID: "rcp_t1", SourceEventID: "sub_a1", SubmissionID: "sub_a1",
		SourceRevision: 1, ReceiverState: ReceiverStateStored,
	}); err != nil {
		t.Fatalf("ack: %v", err)
	}

	counts, err := s.CountTenantLeadStates(ten)
	mustNoErr(t, err)
	got := map[[2]string]int{}
	for _, c := range counts {
		got[[2]string{c.SyncState, c.ReceiverState}] = c.Count
	}
	if got[[2]string{"crm_received", "stored"}] != 1 {
		t.Fatalf("crm_received/stored missing: %v", got)
	}
	if got[[2]string{"accepted", ""}] != 2 {
		t.Fatalf("accepted rows must stay accepted: %v", got)
	}
	// the revoked bucket surfaces the receiver projection, not resurrection
}

func (s *Store) countStoreForms(storeID string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM lead_forms WHERE store_id=? AND campaign_id=''`, storeID).Scan(&n)
	return n, err
}
