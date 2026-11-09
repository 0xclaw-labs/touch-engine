package store

import (
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func newLeadsFixture(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// seedActiveCampaign creates tenant+store+active campaign with a link, returns ids + code.
func seedActiveCampaign(t *testing.T, s *Store) (tenantID, campaignID, storeID, linkID, code string) {
	t.Helper()
	ten, err := s.CreateTenant("商家T")
	mustNoErr(t, err)
	sto, err := s.CreateStore(ten.ID, "门店", "", "test")
	mustNoErr(t, err)
	cmp, err := s.CreateCampaign(NewCampaign{
		TenantID: ten.ID, Title: "活动", PublicContent: "内容",
		StartsAt: "2026-01-01T00:00:00Z", EndsAt: "2030-01-01T00:00:00Z",
		StoreID: sto.ID, CreatedBy: "test",
	})
	mustNoErr(t, err)
	if _, err := s.TransitionCampaign(cmp.ID, ten.ID, "active"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	lnk, err := s.CreateLink(ten.ID, cmp.ID, "test")
	mustNoErr(t, err)
	return ten.ID, cmp.ID, sto.ID, lnk.ID, lnk.Code
}

func TestSubmitLeadAtomicOutbox(t *testing.T) {
	s := newLeadsFixture(t)
	ten, cmp, sto, lnk, _ := seedActiveCampaign(t, s)

	l, dup, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
		SubmissionRef: "sub_1", DedupKey: "dk_1",
		Name: "张三", Phone: "13800138000", MarketingOptin: true,
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_1", OutboxPayload: `{"event_profile":{}}`,
	})
	mustNoErr(t, err)
	if dup {
		t.Fatal("first submit must not be duplicate")
	}
	if l.SyncState != "accepted" {
		t.Fatalf("state = %q, want accepted", l.SyncState)
	}
	rows, err := s.ListLeadsOutboxDue(10)
	mustNoErr(t, err)
	if len(rows) != 1 || rows[0].EventID != "ev_1" || rows[0].Kind != "submit" {
		t.Fatalf("outbox rows = %+v, want exactly ev_1/submit", rows)
	}
}

func TestSubmitLeadDedupIdempotent(t *testing.T) {
	s := newLeadsFixture(t)
	ten, cmp, sto, lnk, _ := seedActiveCampaign(t, s)
	base := NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
		SubmissionRef: "sub_1", DedupKey: "dk_same",
		Name: "张三", Phone: "13800138000",
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_1", OutboxPayload: `{}`,
	}
	if _, dup, err := s.SubmitLead(base); err != nil || dup {
		t.Fatalf("first: dup=%v err=%v", dup, err)
	}
	// same key, different ref/payload: must return the ORIGINAL row, write nothing
	second := base
	second.SubmissionRef = "sub_2"
	second.OutboxEventID = "ev_2"
	got, dup, err := s.SubmitLead(second)
	mustNoErr(t, err)
	if !dup || got.SubmissionRef != "sub_1" {
		t.Fatalf("dup=%v ref=%q, want duplicate of sub_1", dup, got.SubmissionRef)
	}
	rows, _ := s.ListLeadsOutboxDue(10)
	if len(rows) != 1 || rows[0].EventID != "ev_1" {
		t.Fatalf("outbox after duplicate = %+v, want only ev_1", rows)
	}
}

func TestLeadSyncStateMachine(t *testing.T) {
	s := newLeadsFixture(t)
	ten, cmp, sto, lnk, _ := seedActiveCampaign(t, s)
	_, _, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
		SubmissionRef: "sub_1", DedupKey: "dk_1",
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_1", OutboxPayload: `{}`,
	})
	mustNoErr(t, err)

	// HUI-4594: crm_received is ACK-only. Without the receiver's persisted ACK
	// the row must stay in pending_sync no matter what the transport says.
	mustNoErr(t, s.MarkLeadSyncPending("sub_1"))
	mustNoErr(t, s.MarkLeadSyncPending("sub_1")) // idempotent
	mustNoErr(t, s.MarkOutboxForwarded("ev_1", "ntf_ev_1"))
	got, err := s.GetLeadSubmissionByRef("sub_1")
	mustNoErr(t, err)
	if got.SyncState != "pending_sync" || got.ReceiverState != "" {
		t.Fatalf("transport alone must not end sync: %+v", got)
	}
	// the persisted ACK is the only path to crm_received (idempotent on replay)
	if _, _, err := s.AckLeadReceipt(ten, "sub_1", "leads-ingest", LeadAck{
		ReceiptID: "rcp_sm_1", SourceEventID: "ev_1", SubmissionID: "sub_1",
		SourceRevision: 1, ReceiverState: ReceiverStateStored,
	}); err != nil {
		t.Fatalf("ack: %v", err)
	}
	// second ack under a new receipt id stays idempotent at the state level
	if _, _, err := s.AckLeadReceipt(ten, "sub_1", "leads-ingest", LeadAck{
		ReceiptID: "rcp_sm_2", SourceEventID: "ev_1", SubmissionID: "sub_1",
		SourceRevision: 1, ReceiverState: ReceiverStateStored,
	}); err != nil {
		t.Fatalf("second ack: %v", err)
	}
	got, _ = s.GetLeadSubmissionByRef("sub_1")
	if got.SyncState != "crm_received" || got.ReceiverState != "stored" {
		t.Fatalf("state = %q receiver %q, want crm_received/stored", got.SyncState, got.ReceiverState)
	}

	// sync errors never move a crm_received row
	mustNoErr(t, s.RecordLeadSyncError("sub_1", "boom"))
	got, _ = s.GetLeadSubmissionByRef("sub_1")
	if got.SyncState != "crm_received" || got.Attempts != 0 {
		t.Fatalf("crm_received row mutated by sync error: %+v", got)
	}

	// rejected from pending
	if _, _, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, SubmissionRef: "sub_2", DedupKey: "dk_2",
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_2", OutboxPayload: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	mustNoErr(t, s.MarkLeadSyncPending("sub_2"))
	mustNoErr(t, s.MarkLeadRejected("sub_2", "dead_letter"))
	got, _ = s.GetLeadSubmissionByRef("sub_2")
	if got.SyncState != "rejected" {
		t.Fatalf("state = %q, want rejected", got.SyncState)
	}

	// HUI-4594 repair: a later delivered verdict proves the dead-letter call
	// wrong; the row returns to 待同步, never straight to crm_received
	mustNoErr(t, s.RepairLeadToPending("sub_2"))
	got, _ = s.GetLeadSubmissionByRef("sub_2")
	if got.SyncState != "pending_sync" || got.ReceiverState != "" {
		t.Fatalf("repair = %q/%q, want pending_sync with empty receiver", got.SyncState, got.ReceiverState)
	}
	// repair is idempotent at the target state and conflicts from live states:
	// accepted means the fact never left, so a repair claim must not pass
	if err := s.RepairLeadToPending("sub_2"); err != nil {
		t.Fatalf("repair at target must be idempotent: %v", err)
	}
	if _, _, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, SubmissionRef: "sub_3", DedupKey: "dk_3",
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_3", OutboxPayload: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RepairLeadToPending("sub_3"); err == nil {
		t.Fatal("accepted -> repair must conflict (compare-and-set)")
	}
}

func TestRevokeLeadMatrix(t *testing.T) {
	t.Run("before forward: submit row suppressed, no revoke event", func(t *testing.T) {
		s := newLeadsFixture(t)
		ten, cmp, sto, lnk, _ := seedActiveCampaign(t, s)
		_, _, err := s.SubmitLead(NewLeadSubmission{
			TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
			SubmissionRef: "sub_a", DedupKey: "dk_a",
			NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
			OutboxEventID: "ev_a", OutboxPayload: `{}`,
		})
		mustNoErr(t, err)
		_, err = s.RevokeLeadSubmission("sub_a", "ev_a_r", `{}`)
		mustNoErr(t, err)
		rows, _ := s.ListLeadsOutboxDue(10)
		if len(rows) != 0 {
			t.Fatalf("suppressed row still due: %+v", rows)
		}
		got, _ := s.GetLeadSubmissionByRef("sub_a")
		if got.SyncState != "revoked" || got.RevokedAt == "" {
			t.Fatalf("want revoked with timestamp, got %+v", got)
		}
		// idempotent revoke
		if _, err := s.RevokeLeadSubmission("sub_a", "ev_a_r2", `{}`); err != nil {
			t.Fatalf("re-revoke: %v", err)
		}
	})
	t.Run("after forward: revoke event enqueued atomically", func(t *testing.T) {
		s := newLeadsFixture(t)
		ten, cmp, sto, lnk, _ := seedActiveCampaign(t, s)
		_, _, err := s.SubmitLead(NewLeadSubmission{
			TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
			SubmissionRef: "sub_b", DedupKey: "dk_b",
			NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
			OutboxEventID: "ev_b", OutboxPayload: `{}`,
		})
		mustNoErr(t, err)
		mustNoErr(t, s.MarkOutboxForwarded("ev_b", "evt_notify_b"))
		if got, err := s.NotifyEventID("sub_b", "submit"); err != nil || got != "evt_notify_b" {
			t.Fatalf("notify event id = %q %v", got, err)
		}
		mustNoErr(t, s.MarkLeadSyncPending("sub_b"))
		if _, err := s.RevokeLeadSubmission("sub_b", "ev_b_r", `{"kind":"revoke"}`); err != nil {
			t.Fatal(err)
		}
		due, _ := s.ListLeadsOutboxDue(10)
		if len(due) != 1 || due[0].Kind != "revoke" || due[0].EventID != "ev_b_r" {
			t.Fatalf("due = %+v, want exactly the revoke event", due)
		}
	})
	t.Run("unknown ref", func(t *testing.T) {
		s := newLeadsFixture(t)
		if _, err := s.RevokeLeadSubmission("sub_none", "ev_x", `{}`); err == nil {
			t.Fatal("unknown ref must fail")
		}
	})
	t.Run("rejected after forward: revoke still returns to the original owner", func(t *testing.T) {
		s := newLeadsFixture(t)
		ten, cmp, sto, lnk, _ := seedActiveCampaign(t, s)
		_, _, err := s.SubmitLead(NewLeadSubmission{
			TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
			SubmissionRef: "sub_r", DedupKey: "dk_r",
			NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
			OutboxEventID: "ev_r", OutboxPayload: `{}`,
		})
		mustNoErr(t, err)
		mustNoErr(t, s.MarkOutboxForwarded("ev_r", "evt_notify_r"))
		mustNoErr(t, s.MarkLeadSyncPending("sub_r"))
		mustNoErr(t, s.MarkLeadRejected("sub_r", "dead: http status 404"))
		got, err := s.RevokeLeadSubmission("sub_r", "ev_r_revoke", `{"kind":"revoke"}`)
		mustNoErr(t, err)
		if got.SyncState != "revoked" || got.TenantID != ten {
			t.Fatalf("revoke = %+v, want revoked on the original tenant", got)
		}
		due, _ := s.ListLeadsOutboxDue(10)
		if len(due) != 1 || due[0].Kind != "revoke" || due[0].EventID != "ev_r_revoke" {
			t.Fatalf("due = %+v, want the revoke event", due)
		}
	})
}

func TestLeadFormAndStats(t *testing.T) {
	s := newLeadsFixture(t)
	ten, cmp, sto, lnk, code := seedActiveCampaign(t, s)

	if _, err := s.GetLeadFormByCampaign(cmp); err != ErrNotFound {
		t.Fatalf("absent form lookup = %v, want ErrNotFound", err)
	}
	f, err := s.UpsertLeadForm(ten, cmp, "v1", true, "test")
	mustNoErr(t, err)
	if !f.Enabled || !f.MarketingOptinEnabled {
		t.Fatalf("form = %+v", f)
	}
	// upsert updates, cross-tenant refused
	f2, err := s.UpsertLeadForm(ten, cmp, "v2", false, "test")
	mustNoErr(t, err)
	if f2.NoticeVersion != "v2" || f2.MarketingOptinEnabled {
		t.Fatalf("upsert = %+v", f2)
	}
	if _, err := s.UpsertLeadForm("tnt_other", cmp, "v1", true, "x"); err != ErrNotFound {
		t.Fatalf("cross-tenant upsert = %v, want ErrNotFound", err)
	}

	mustNoErr(t, s.IncrementViewStat(code, "2026-09-19", "wecom"))
	mustNoErr(t, s.IncrementViewStat(code, "2026-09-19", "wecom"))
	n, err := s.GetViewStat(code, "2026-09-19", "wecom")
	mustNoErr(t, err)
	if n != 2 {
		t.Fatalf("views = %d, want 2", n)
	}
	subs, views, err := s.LeadStats(ten, cmp)
	mustNoErr(t, err)
	if subs != 0 || views != 2 {
		t.Fatalf("stats = (%d,%d), want (0,2)", subs, views)
	}

	if _, _, err := s.SubmitLead(NewLeadSubmission{
		TenantID: ten, CampaignID: cmp, StoreID: sto, LinkID: lnk,
		SubmissionRef: "sub_1", DedupKey: "dk_1",
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_1", OutboxPayload: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	subs, views, _ = s.LeadStats(ten, cmp)
	if subs != 1 || views != 2 {
		t.Fatalf("stats = (%d,%d), want (1,2)", subs, views)
	}
}

func TestLeadTenantScopedAdminList(t *testing.T) {
	s := newLeadsFixture(t)
	tenA, cmpA, stoA, lnkA, _ := seedActiveCampaign(t, s)
	tenB, err := s.CreateTenant("商家B")
	mustNoErr(t, err)

	if _, _, err := s.SubmitLead(NewLeadSubmission{
		TenantID: tenA, CampaignID: cmpA, StoreID: stoA, LinkID: lnkA,
		SubmissionRef: "sub_a", DedupKey: "dk_a",
		NoticeVersion: "v1", ConsentAt: "2026-09-19T00:00:00Z",
		OutboxEventID: "ev_a", OutboxPayload: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListLeadSubmissions(tenA, cmpA)
	mustNoErr(t, err)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	items, err = s.ListLeadSubmissions(tenB.ID, cmpA)
	mustNoErr(t, err)
	if len(items) != 0 {
		t.Fatalf("cross-tenant list leaked %d rows", len(items))
	}
}

func TestLeadAuditTrail(t *testing.T) {
	s := newLeadsFixture(t)
	ten, _, _, _, _ := seedActiveCampaign(t, s)
	mustNoErr(t, s.AppendLeadAudit(ten, "sub_1", "accept", `{"notice":"v1"}`, "consumer"))
	mustNoErr(t, s.AppendLeadAudit(ten, "sub_1", "revoke", "", "consumer"))
	rows, err := s.ListLeadAudit(ten, "sub_1")
	mustNoErr(t, err)
	if len(rows) != 2 || rows[0]["action"] != "accept" || rows[1]["action"] != "revoke" {
		t.Fatalf("audit = %+v", rows)
	}
}
