package rewardops

import "testing"

// HUI-1671: 点评类动作/准入侧 reward_eligibility=false 不触发奖励。
// 创建(ValidateActionBinding)与发放(Adjudicate)两侧双保险。

func eligibleSnap() Snapshot {
	return Snapshot{
		CampaignID:      "cmp_1",
		Version:         1,
		AllowedEvidence: []string{EvidenceStaffObserved, EvidenceOfficial},
		Slots:           []string{"first_publish"},
	}
}

func TestAdjudicateRefusesReviewActionKind(t *testing.T) {
	v := Adjudicate(eligibleSnap(), Input{
		TenantID: "ten", CampaignID: "cmp_1", ParticipationID: "p1",
		Slot: "first_publish", RuleVersion: 1, EventID: "ev1",
		Evidence: EvidenceStaffObserved, Actor: "staff", StoreID: "st1",
		ActionKind: ActionKindReview,
	})
	if v.Proceed || v.Reason != ReasonRewardNotEligible {
		t.Fatalf("review kind must refuse reward_not_eligible: %+v", v)
	}
}

func TestAdjudicateRefusesNotEligibleAction(t *testing.T) {
	v := Adjudicate(eligibleSnap(), Input{
		TenantID: "ten", CampaignID: "cmp_1", ParticipationID: "p1",
		Slot: "first_publish", RuleVersion: 1, EventID: "ev2",
		Evidence: EvidenceOfficial,
		Official: OfficialRef{FactID: "f1", PlatformPostID: "pp1", ReceiptSource: ReceiptOfficialQuery},
		Actor:    "staff", StoreID: "st1",
		ActionKind:     "publish_post",
		ActionEligible: ActionEligibleNo,
	})
	if v.Proceed || v.Reason != ReasonRewardNotEligible {
		t.Fatalf("not-eligible action must refuse: %+v", v)
	}
}

func TestAdjudicateAllowsEligibleAndLegacyBindings(t *testing.T) {
	base := Input{
		TenantID: "ten", CampaignID: "cmp_1", ParticipationID: "p1",
		Slot: "first_publish", RuleVersion: 1, EventID: "ev3",
		Evidence: EvidenceStaffObserved, Actor: "staff", StoreID: "st1",
	}
	if v := (func() Verdict {
		in := base
		in.ActionKind, in.ActionEligible = "publish_post", ActionEligibleYes
		return Adjudicate(eligibleSnap(), in)
	})(); !v.Proceed {
		t.Fatalf("eligible binding must proceed: %+v", v)
	}
	if v := Adjudicate(eligibleSnap(), base); !v.Proceed {
		t.Fatalf("legacy unbound input must proceed: %+v", v)
	}
}

func TestValidateActionBinding(t *testing.T) {
	if err := ValidateActionBinding(ActionKindReview, ActionEligibleYes); err != ErrRewardNotEligible {
		t.Fatalf("review binding must refuse: %v", err)
	}
	if err := ValidateActionBinding("publish_post", ActionEligibleNo); err != ErrRewardNotEligible {
		t.Fatalf("not-eligible binding must refuse: %v", err)
	}
	if err := ValidateActionBinding("publish_post", ActionEligibleYes); err != nil {
		t.Fatalf("eligible binding must pass: %v", err)
	}
	if err := ValidateActionBinding("", ActionEligibleUnknown); err != nil {
		t.Fatalf("legacy empty binding must pass: %v", err)
	}
	if err := ValidateActionBinding("publish_post", "maybe"); err != ErrInvalidBinding {
		t.Fatalf("unknown eligibility value must be invalid: %v", err)
	}
}
