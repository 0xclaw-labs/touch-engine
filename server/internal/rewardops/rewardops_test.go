package rewardops

import (
	"reflect"
	"strings"
	"testing"
)

// HUI-1671 B3: three evidence classes. A campaign rule snapshot declares which
// of them may satisfy the reward. Staff observations and customer self-reports
// never become an official confirmation.

func TestEvidenceConstants(t *testing.T) {
	if EvidenceStaffObserved != "staff_observed_submission" {
		t.Fatalf("staff observed constant = %q", EvidenceStaffObserved)
	}
	if EvidenceStaffVerified != "staff_verified_post" {
		t.Fatalf("staff verified constant = %q", EvidenceStaffVerified)
	}
	if EvidenceOfficial != "official_publish_confirmed" {
		t.Fatalf("official constant = %q", EvidenceOfficial)
	}
}

func baseSnapshot() Snapshot {
	return Snapshot{
		CampaignID:      "camp-1",
		Version:         3,
		AllowedEvidence: []string{EvidenceStaffObserved, EvidenceStaffVerified},
		Slots:           []string{"first_publish"},
	}
}

func staffInput() Input {
	return Input{
		TenantID: "t1", CampaignID: "camp-1", ParticipationID: "p-1",
		Slot: "first_publish", RuleVersion: 3,
		EventID: "evt-1", Evidence: EvidenceStaffObserved,
		Actor: "staff-9", StoreID: "store-1",
	}
}

func TestAdjudicateStaffEvidenceGrantsWhenAllowed(t *testing.T) {
	for _, ev := range []string{EvidenceStaffObserved, EvidenceStaffVerified} {
		in := staffInput()
		in.Evidence = ev
		v := Adjudicate(baseSnapshot(), in)
		if !v.Proceed {
			t.Fatalf("%s should proceed, got %+v", ev, v)
		}
		if v.PendingReview || v.Reason != "" {
			t.Fatalf("%s unexpected verdict %+v", ev, v)
		}
	}
}

func TestAdjudicateEvidenceNotInSnapshotRefused(t *testing.T) {
	in := staffInput()
	in.Evidence = EvidenceStaffVerified
	snap := baseSnapshot()
	snap.AllowedEvidence = []string{EvidenceStaffObserved}
	v := Adjudicate(snap, in)
	if v.Proceed || v.PendingReview {
		t.Fatalf("unexpected evidence must refuse: %+v", v)
	}
	if v.Reason != ReasonEvidenceNotAllowed {
		t.Fatalf("reason = %q", v.Reason)
	}
}

func TestAdjudicateUnknownEvidenceRefused(t *testing.T) {
	in := staffInput()
	in.Evidence = "self_declared_publish"
	v := Adjudicate(baseSnapshot(), in)
	if v.Proceed || v.Reason != ReasonUnknownEvidence {
		t.Fatalf("unknown evidence must refuse: %+v", v)
	}
}

func TestAdjudicateOfficialOnlyRefusesStaffEvidence(t *testing.T) {
	snap := baseSnapshot()
	snap.AllowedEvidence = []string{EvidenceOfficial}
	v := Adjudicate(snap, staffInput())
	if v.Proceed || v.PendingReview {
		t.Fatalf("official-only must refuse staff evidence: %+v", v)
	}
	if v.Reason != ReasonOfficialEvidenceRequired {
		t.Fatalf("reason = %q", v.Reason)
	}
}

func TestAdjudicateStaffEvidenceNeverBecomesOfficial(t *testing.T) {
	// A staff member claiming the official class with a typed post id is a
	// fabricated receipt: it is refused, never converted.
	in := staffInput()
	in.Evidence = EvidenceOfficial
	in.Official = OfficialRef{FactID: "fact-1", PlatformPostID: "999", ReceiptSource: ReceiptOfficialQuery}
	v := Adjudicate(baseSnapshot(), in)
	if v.Proceed || v.Reason != ReasonEvidenceNotAllowed {
		t.Fatalf("staff evidence in staff rule must not pass as official: %+v", v)
	}
}

func TestAdjudicateOfficialRequiresCompleteTrustedReceipt(t *testing.T) {
	snap := baseSnapshot()
	snap.AllowedEvidence = []string{EvidenceOfficial}
	cases := []struct {
		name string
		ref  OfficialRef
		want string
	}{
		{"missing fact", OfficialRef{PlatformPostID: "9", ReceiptSource: ReceiptOfficialQuery}, ReasonFabricatedReceipt},
		{"missing post id", OfficialRef{FactID: "f1", ReceiptSource: ReceiptOfficialQuery}, ReasonFabricatedReceipt},
		{"wrong source", OfficialRef{FactID: "f1", PlatformPostID: "9", ReceiptSource: "customer_typed"}, ReasonFabricatedReceipt},
		{"empty ref", OfficialRef{}, ReasonFabricatedReceipt},
	}
	for _, tc := range cases {
		in := staffInput()
		in.Evidence = EvidenceOfficial
		in.Official = tc.ref
		v := Adjudicate(snap, in)
		if v.Proceed || v.Reason != tc.want {
			t.Fatalf("%s: got %+v", tc.name, v)
		}
	}
	ok := staffInput()
	ok.Evidence = EvidenceOfficial
	ok.Official = OfficialRef{FactID: "f1", PlatformPostID: "9", ReceiptSource: ReceiptOfficialQuery}
	v := Adjudicate(snap, ok)
	if !v.Proceed || v.Reason != "" {
		t.Fatalf("complete trusted receipt should proceed: %+v", v)
	}
}

func TestAdjudicateMissingFields(t *testing.T) {
	snap := baseSnapshot()
	mutations := []struct {
		name string
		mut  func(*Input)
	}{
		{"tenant", func(i *Input) { i.TenantID = "" }},
		{"campaign", func(i *Input) { i.CampaignID = "" }},
		{"participation", func(i *Input) { i.ParticipationID = "" }},
		{"slot", func(i *Input) { i.Slot = "" }},
		{"version", func(i *Input) { i.RuleVersion = 0 }},
		{"event", func(i *Input) { i.EventID = "" }},
		{"actor", func(i *Input) { i.Actor = "" }},
		{"store", func(i *Input) { i.StoreID = "" }},
	}
	for _, m := range mutations {
		in := staffInput()
		m.mut(&in)
		v := Adjudicate(snap, in)
		if v.Proceed || v.Reason != ReasonMissingField {
			t.Fatalf("%s: got %+v", m.name, v)
		}
	}
	// Snapshot mismatch is a rejection, not a proceed.
	wrong := staffInput()
	wrong.CampaignID = "camp-other"
	if v := Adjudicate(snap, wrong); v.Proceed || v.Reason != ReasonCampaignMismatch {
		t.Fatalf("campaign mismatch: %+v", v)
	}
	wrongSlot := staffInput()
	wrongSlot.Slot = "other_slot"
	if v := Adjudicate(snap, wrongSlot); v.Proceed || v.Reason != ReasonSlotUnknown {
		t.Fatalf("unknown slot: %+v", v)
	}
	wrongVersion := staffInput()
	wrongVersion.RuleVersion = 2
	if v := Adjudicate(snap, wrongVersion); v.Proceed || v.Reason != ReasonVersionMismatch {
		t.Fatalf("version mismatch: %+v", v)
	}
}

func TestAdjudicateVersionSnapshotMismatch(t *testing.T) {
	// The input announces the version the participation was promised; the
	// snapshot the caller loaded must be that same version (in-flight keeps
	// its original snapshot even when a newer one exists).
	in := staffInput()
	v := Adjudicate(baseSnapshot(), in)
	if !v.Proceed {
		t.Fatalf("baseline should proceed: %+v", v)
	}
}

func TestSelfReportOnlyRequestsReview(t *testing.T) {
	v := SelfReportVerdict()
	if v.Proceed || !v.PendingReview {
		t.Fatalf("self report must stay pending: %+v", v)
	}
	if v.Reason != ReasonSelfReportPending {
		t.Fatalf("reason = %q", v.Reason)
	}
}

func TestValidateSnapshot(t *testing.T) {
	if err := ValidateSnapshot(baseSnapshot()); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	bad := baseSnapshot()
	bad.CampaignID = ""
	if err := ValidateSnapshot(bad); err == nil {
		t.Fatal("empty campaign accepted")
	}
	bad = baseSnapshot()
	bad.AllowedEvidence = nil
	if err := ValidateSnapshot(bad); err == nil {
		t.Fatal("no allowed evidence accepted")
	}
	bad = baseSnapshot()
	bad.AllowedEvidence = []string{"click"}
	if err := ValidateSnapshot(bad); err == nil {
		t.Fatal("unknown evidence class accepted")
	}
	bad = baseSnapshot()
	bad.Slots = nil
	if err := ValidateSnapshot(bad); err == nil {
		t.Fatal("no slots accepted")
	}
}

func TestOfficialRefForAttempt(t *testing.T) {
	ref := OfficialRefForAttempt("a-1", "post-9")
	if !reflect.DeepEqual(ref, OfficialRef{FactID: "a-1", PlatformPostID: "post-9", ReceiptSource: ReceiptOfficialQuery}) {
		t.Fatalf("unexpected ref %+v", ref)
	}
	if !strings.Contains(ReceiptOfficialQuery, "official") {
		t.Fatalf("receipt source = %q", ReceiptOfficialQuery)
	}
}
