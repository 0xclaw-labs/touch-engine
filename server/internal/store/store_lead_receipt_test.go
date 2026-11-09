package store

// store_lead_receipt_test.go: HUI-4594 receiver receipts (持久化 ACK) + 撤回水位
// + 无活动门店留资 at the store layer. The ACK five-tuple
// (receipt_id/source_event_id/submission_id/source_revision/receiver_state) is
// the ONLY thing that ends sync (crm_received) — notify delivery success does
// not. Revocation sets a waterline that late replays cannot cross.

import (
	"errors"
	"testing"
)

func seedSubmittedLead(t *testing.T, s *Store) (tenantID, campaignID, storeID, ref string) {
	t.Helper()
	ten, cmp, sto, _, _ := seedActiveCampaign(t, s)
	_, dup, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, StoreID: sto,
		SubmissionRef: "sub_ack_1", DedupKey: "dk_ack_1",
		Name: "张三", Phone: "13800138000",
		NoticeVersion: "v1", ConsentAt: "2026-11-10T00:00:00Z",
		OutboxEventID: "ev-sub_ack_1", OutboxPayload: `{"event_profile":{"event_id":"sub_ack_1"}}`,
	})
	if err != nil || dup {
		t.Fatalf("submit: dup=%v err=%v", dup, err)
	}
	// the fact is at notify: the lead sits in pending_sync until the receiver ACKs
	if err := s.MarkLeadSyncPending("sub_ack_1"); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := s.MarkOutboxForwarded("ev-sub_ack_1", "ntf_1"); err != nil {
		t.Fatalf("forwarded: %v", err)
	}
	return ten, cmp, sto, "sub_ack_1"
}

func ackIn(submissionID string) LeadAck {
	return LeadAck{
		ReceiptID:      "rcp_leads_1",
		SourceEventID:  "sub_ack_1",
		SubmissionID:   submissionID,
		SourceRevision: 1,
		ReceiverState:  "stored",
	}
}

func TestAckLeadReceiptEndsSync(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)

	rec, dup, err := s.AckLeadReceipt(ten, ref, "leads-ingest", ackIn(ref))
	mustNoErr(t, err)
	if dup {
		t.Fatalf("first ack must not be duplicate")
	}
	if rec.ID != "rcp_leads_1" || rec.ReceiverState != "stored" {
		t.Fatalf("receipt=%+v", rec)
	}

	lead, err := s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	if lead.SyncState != "crm_received" {
		t.Fatalf("ack must end sync, got %s", lead.SyncState)
	}
	if lead.ReceiverState != "stored" || lead.ReceiptID != "rcp_leads_1" {
		t.Fatalf("projection missing: %+v", lead)
	}

	// audit trail records the receipt in the same transaction
	rows, err := s.ListLeadAudit(ten, ref)
	mustNoErr(t, err)
	found := false
	for _, r := range rows {
		if r["action"] == "receiver_ack" {
			found = true
		}
	}
	if !found {
		t.Fatalf("receiver_ack audit missing: %v", rows)
	}
}

func TestAckLeadReceiptReplayIdempotent(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)
	if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", ackIn(ref)); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	// same receipt id, same content -> duplicate, no error, no new row
	_, dup, err := s.AckLeadReceipt(ten, ref, "leads-ingest", ackIn(ref))
	mustNoErr(t, err)
	if !dup {
		t.Fatalf("replay must be duplicate")
	}
	n, err := s.countReceipts(ref)
	mustNoErr(t, err)
	if n != 1 {
		t.Fatalf("replay stored a second receipt: %d", n)
	}
}

func TestAckLeadReceiptConflictOnSameIDDifferentPayload(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)
	if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", ackIn(ref)); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	bad := ackIn(ref)
	bad.SourceRevision = 7 // same receipt id, different payload
	if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", bad); !errors.Is(err, ErrAckConflict) {
		t.Fatalf("want ErrAckConflict, got %v", err)
	}
}

func TestAckIncompleteIsRejected(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)
	for name, mutate := range map[string]func(*LeadAck){
		"receipt_id":      func(a *LeadAck) { a.ReceiptID = "" },
		"source_event_id": func(a *LeadAck) { a.SourceEventID = "" },
		"submission_id":   func(a *LeadAck) { a.SubmissionID = "" },
		"receiver_state":  func(a *LeadAck) { a.ReceiverState = "" },
	} {
		in := ackIn(ref)
		mutate(&in)
		if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", in); !errors.Is(err, ErrAckIncomplete) {
			t.Fatalf("%s: want ErrAckIncomplete, got %v", name, err)
		}
	}
	// nothing ended sync
	lead, err := s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	if lead.SyncState != "pending_sync" {
		t.Fatalf("incomplete ack must not end sync, got %s", lead.SyncState)
	}
}

func TestAckUnknownSourceEventRefused(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)
	in := ackIn(ref)
	in.SourceEventID = "ev-never-sent"
	if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", in); !errors.Is(err, ErrUnknownSourceEvent) {
		t.Fatalf("want ErrUnknownSourceEvent, got %v", err)
	}
	// notify's own event id is also a known reference (it is on the forwarded row)
	in2 := ackIn(ref)
	in2.ReceiptID = "rcp_leads_2"
	in2.SourceEventID = "ntf_1"
	if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", in2); err != nil {
		t.Fatalf("notify event id must be acceptable: %v", err)
	}
}

func TestAckStoredOnRevokedLeadIsWaterlineBlocked(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)
	lead, err := s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	payload, err := s.LeadOutboxPayload(ref, "submit")
	mustNoErr(t, err)
	if _, err := s.RevokeLeadSubmission(ref, "ev-"+ref+"-revoke", payload+"-revoke"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, err := s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	if revoked.RevokeSourceRevision != lead.SourceVersion+1 {
		t.Fatalf("waterline must be source_version+1, got %d", revoked.RevokeSourceRevision)
	}
	// late replay of the SUBMIT fact comes back as a stored ack -> refused
	if _, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", ackIn(ref)); !errors.Is(err, ErrRevocationWaterline) {
		t.Fatalf("want ErrRevocationWaterline, got %v", err)
	}
	lead, err = s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	if lead.ReceiverState == "stored" || lead.ReceiptID != "" {
		t.Fatalf("waterline must not project a stored receipt: %+v", lead)
	}
	// the receiver acknowledging the revoke fact converges honestly; the ack can
	// only reference the revoke fact AFTER it left (forwarded), mirroring the
	// submit fact discipline
	if err := s.MarkOutboxForwarded("ev-"+ref+"-revoke", "ntf_revoke_1"); err != nil {
		t.Fatalf("forward revoke: %v", err)
	}
	in := ackIn(ref)
	in.ReceiverState = "revoked"
	in.SourceEventID = "ev-" + ref + "-revoke"
	in.SourceRevision = lead.RevokeSourceRevision
	in.ReceiptID = "rcp_revoke_1"
	rec, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", in)
	mustNoErr(t, err)
	if rec.ReceiverState != "revoked" {
		t.Fatalf("receipt=%+v", rec)
	}
	final, err := s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	if final.ReceiverState != "revoked" || final.SyncState != "revoked" {
		t.Fatalf("revoked ack must converge projection: %+v", final)
	}
}

func TestAckReceiverClaimedRevokeRecordsOnly(t *testing.T) {
	// the revoke fact originates on this side; a receiver claiming revoked while
	// the local row is still live is recorded as the receiver's claim — the local
	// sync state is NOT invented from it.
	s := newLeadsFixture(t)
	ten, _, _, ref := seedSubmittedLead(t, s)
	in := ackIn(ref)
	in.ReceiverState = "revoked"
	rec, _, err := s.AckLeadReceipt(ten, ref, "leads-ingest", in)
	mustNoErr(t, err)
	if rec.ReceiverState != "revoked" {
		t.Fatalf("receipt=%+v", rec)
	}
	lead, err := s.GetLeadSubmissionByRef(ref)
	mustNoErr(t, err)
	if lead.SyncState != "pending_sync" {
		t.Fatalf("receiver claim must not flip local state, got %s", lead.SyncState)
	}
	if lead.ReceiverState != "revoked" {
		t.Fatalf("receiver claim must be visible: %+v", lead)
	}
}

func TestAckWrongTenantIsNotFound(t *testing.T) {
	s := newLeadsFixture(t)
	_, _, _, ref := seedSubmittedLead(t, s)
	other, err := s.CreateTenant("别家")
	mustNoErr(t, err)
	if _, _, err := s.AckLeadReceipt(other.ID, ref, "leads-ingest", ackIn(ref)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func (s *Store) countReceipts(ref string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM lead_receipts WHERE submission_ref=?`, ref).Scan(&n)
	return n, err
}
