package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
)

func TestUnchangedPromiseKeepsInventoryVersion(t *testing.T) {
	s := openConfigStore(t)
	tenant, _, campaign, _ := configFixture(t, s)
	first, err := s.UpsertPublishConfigDraft(tenant, campaign, sampleSpec(), "owner")
	must(t, err)
	same, err := s.UpsertPublishConfigDraft(tenant, campaign, sampleSpec(), "owner")
	must(t, err)
	if same.Version != first.Version {
		t.Fatalf("unchanged promise changed version %d -> %d and strands prepared inventory", first.Version, same.Version)
	}
	revisions, err := s.ListPublishConfigRevisions(tenant, campaign)
	must(t, err)
	if len(revisions) != 1 {
		t.Fatalf("no-op save generated %d revisions", len(revisions))
	}
}

func TestPrepareInventoryFromPromiseAndReplay(t *testing.T) {
	s := openConfigStore(t)
	tenant, _, campaign, _ := configFixture(t, s)
	st, err := s.CreateStore(tenant, "承诺门店", "地址", "owner")
	must(t, err)
	spec := sampleSpec()
	spec.Reward.StoreIDs = []string{st.ID}
	cfg, err := s.UpsertPublishConfigDraft(tenant, campaign, spec, "owner")
	must(t, err)
	in := PrepareRewardInventory{Name: "周末优惠", Quantity: 4, RequestKey: "prepare-1", CostMinor: nil}
	first, err := s.PrepareRewardInventory(tenant, campaign, in, "owner")
	must(t, err)
	replay, err := s.PrepareRewardInventory(tenant, campaign, in, "owner")
	must(t, err)
	if replay.AvailableQty != first.AvailableQty || first.ID != replay.ID || first.RuleVersion != cfg.Version || first.BenefitSlot != spec.Reward.Slot || first.StoreID != st.ID || first.ValidUntil != spec.Reward.RedeemUntil || first.CostRecorded {
		t.Fatalf("bad derived/replayed inventory: %+v / %+v", first, replay)
	}
	if len(first.AllowedEvidence) != 1 || first.AllowedEvidence[0] != spec.VerificationMode {
		t.Fatalf("wrong evidence %+v", first)
	}
	in.Quantity = 5
	if _, err = s.PrepareRewardInventory(tenant, campaign, in, "owner"); !errors.Is(err, ErrRewardConflict) {
		t.Fatalf("same request changed payload accepted: %v", err)
	}
	zero := int64(0)
	in.Quantity = 2
	in.RequestKey = "prepare-2"
	in.CostMinor = &zero
	explicit, err := s.PrepareRewardInventory(tenant, campaign, in, "owner")
	must(t, err)
	if !explicit.CostRecorded {
		t.Fatal("explicit zero cost must remain recorded")
	}
	spec.Reward.TotalQuantity = intPtr(6)
	_, err = s.UpsertPublishConfigDraft(tenant, campaign, spec, "owner")
	must(t, err)
	in.RequestKey = "prepare-3"
	in.Quantity = 7
	if _, err = s.PrepareRewardInventory(tenant, campaign, in, "owner"); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("over promise accepted: %v", err)
	}
}

func TestPreparedCostStatusFreezesInBenefitAndSummary(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "explicit_zero"}[recorded], func(t *testing.T) {
			s := openConfigStore(t)
			tenant, _, campaign, _ := configFixture(t, s)
			st, err := s.CreateStore(tenant, "门店", "地址", "owner")
			must(t, err)
			spec := sampleSpec()
			spec.Reward.StoreIDs = []string{st.ID}
			spec.Reward.RedeemFrom = "2020-01-01T00:00:00Z"
			spec.Reward.RedeemUntil = "2030-01-01T00:00:00Z"
			cfg, err := s.UpsertPublishConfigDraft(tenant, campaign, spec, "owner")
			must(t, err)
			in := PrepareRewardInventory{Name: "承诺优惠", Quantity: 2, RequestKey: "cost-freeze"}
			zero := int64(0)
			if recorded {
				in.CostMinor = &zero
			}
			_, err = s.PrepareRewardInventory(tenant, campaign, in, "owner")
			must(t, err)
			must(t, s.EnablePublishConfig(tenant, campaign, cfg.Version, campaignenable.BuildDisclosure(spec, cfg.Version, timeFixed()).JSON(), "owner"))
			_, err = s.TransitionCampaign(campaign, tenant, "active")
			must(t, err)
			// Cost is frozen on the reward promised to a real participation.
			// A fabricated ID cannot establish which published version it accepted.
			parts := participation.New(s.DB, nil, participation.Options{})
			started, err := parts.Start(context.Background(), participation.StartInput{TenantID: tenant, StoreID: st.ID, CampaignID: campaign, LinkCode: "cost-contract", Source: participation.SourceManual, RuleVersion: cfg.Version, ContentPackVersion: spec.ContentVerID, VisitorID: "cost-visitor", IdempotencyKey: "cost-part", Window: participation.Window{StartsAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}})
			must(t, err)
			partID := started.Participation.ID
			ben, _, err := s.ReserveRewardBenefit(RewardReserveInput{TenantID: tenant, CampaignID: campaign, ParticipationID: partID, Slot: spec.Reward.Slot, RuleVersion: cfg.Version, EventID: "reserve-cost", Actor: "customer", TTLSeconds: 300}, now())
			must(t, err)
			if ben.CostRecorded != recorded {
				t.Fatalf("cost fact lost at reservation: %+v", ben)
			}
			issued, _, err := s.ConfirmRewardBenefit(RewardConfirmInput{TenantID: tenant, CampaignID: campaign, ParticipationID: partID, Slot: spec.Reward.Slot, RuleVersion: cfg.Version, EventID: "confirm-cost", Evidence: spec.VerificationMode, Actor: "owner", StoreID: st.ID, Now: now()})
			must(t, err)
			if issued.CostRecorded != recorded {
				t.Fatalf("cost fact lost at issuance: %+v", issued)
			}
			summary, err := s.RewardSummary(tenant, campaign)
			must(t, err)
			if recorded && summary.CommittedCostUnrecorded != 0 || !recorded && summary.CommittedCostUnrecorded != 1 {
				t.Fatalf("unknown cost counted as zero: %+v", summary)
			}
		})
	}
}
