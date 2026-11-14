package store

import (
	"testing"
	"time"
)

func TestRewardAvailabilityExpiryUsesSameInstantAsLedger(t *testing.T) {
	s, tenant, campaign, _, _, _, _ := seedRewardWorld(t)
	inv, err := s.CreateRewardInventory(tenant, NewRewardInventory{CampaignID: campaign, Kind: RewardKindStoreCoupon, Name: "时钟边界库存", BenefitSlot: "clock-stock", RuleVersion: 3, AllowedEvidence: []string{"staff_observed_submission"}, TotalQty: 4, CreatedBy: "test"})
	must(t, err)
	base := time.Now().UTC().Truncate(time.Second)
	reserve := func(id string, ttl int, at time.Time) RewardBenefit {
		t.Helper()
		b, _, err := s.ReserveRewardBenefit(RewardReserveInput{TenantID: tenant, CampaignID: campaign, ParticipationID: id, Slot: inv.BenefitSlot, RuleVersion: 3, EventID: "reserve-" + id, Actor: "test", TTLSeconds: ttl}, at.Format(time.RFC3339Nano))
		must(t, err)
		return b
	}
	reserve("still-live", 600, base)
	due := reserve("due-now", 60, base)
	confirm := func(id string) RewardBenefit {
		t.Helper()
		in := confirmInput(tenant, campaign, id, "staff_observed_submission")
		in.Slot = inv.BenefitSlot
		in.Now = base.Format(time.RFC3339Nano)
		b, _, err := s.ConfirmRewardBenefit(in)
		must(t, err)
		return b
	}
	confirm("already-issued")
	redeemed := confirm("already-redeemed")
	_, _, err = s.RedeemRewardBenefit(tenant, campaign, redeemed.ID, "test", "store-1", "redeem-original", base.Format(time.RFC3339Nano))
	must(t, err)
	boundary := base.Add(time.Minute)
	before, err := s.GetRewardInventoryAt(inv.ID, tenant, boundary.Add(-time.Nanosecond))
	must(t, err)
	if before.AvailableQty != 0 {
		t.Fatalf("unexpired hold was reclaimed early: %+v", before)
	}
	at, err := s.GetRewardInventoryAt(inv.ID, tenant, boundary)
	must(t, err)
	if at.AvailableQty != 1 {
		t.Fatalf("issued/redeemed/live hold occupancy changed or due hold stuck: %+v", at)
	}
	rows, err := s.ListRewardInventoryAt(tenant, campaign, boundary)
	must(t, err)
	for _, r := range rows {
		if r.ID == inv.ID && r.AvailableQty != 1 {
			t.Fatalf("list/get disagree at same instant: %+v", r)
		}
	}
	sum, err := s.RewardSummaryAt(tenant, campaign, boundary)
	must(t, err)
	if sum.Counts[RewardStatusReserved] != 1 || sum.Counts[RewardStatusExpired] != 1 || sum.Counts[RewardStatusIssued] != 1 || sum.Counts[RewardStatusRedeemed] != 1 {
		t.Fatalf("read projection did not preserve fact classes: %+v", sum)
	}
	raw, err := s.GetRewardBenefit(due.ID, tenant, campaign)
	must(t, err)
	if raw.Status != RewardStatusReserved {
		t.Fatalf("read wrote expiration: %+v", raw)
	}
	reserve("new-at-exact-deadline", 600, boundary)
	raw, err = s.GetRewardBenefit(due.ID, tenant, campaign)
	must(t, err)
	if raw.Status != RewardStatusExpired {
		t.Fatalf("ledger did not sweep at same exact instant: %+v", raw)
	}
	at, err = s.GetRewardInventoryAt(inv.ID, tenant, boundary)
	must(t, err)
	if at.AvailableQty != 0 {
		t.Fatalf("allocation violated conservation: %+v", at)
	}
}
func TestRewardAvailabilityUnknownDeadlineStaysOccupied(t *testing.T) {
	for _, until := range []string{"", "bad-time", "2026-02-30T12:00:00Z"} {
		if rewardHoldExpiredAt(until, time.Now().UTC()) {
			t.Fatalf("unprovable deadline released stock: %q", until)
		}
	}
}
