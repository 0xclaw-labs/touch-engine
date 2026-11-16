package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
)

func TestPublishedConfigDraftPauseAndResumeUseOriginalRevision(t *testing.T) {
	s := openConfigStore(t)
	tenant, _, campaign, _ := configFixture(t, s)
	original := sampleSpec()
	_, err := s.UpsertPublishConfigDraft(tenant, campaign, original, "test")
	must(t, err)
	disclosure := campaignenable.BuildDisclosure(original, 1, timeFixed()).JSON()
	must(t, s.EnablePublishConfig(tenant, campaign, 1, disclosure, "test"))
	draft := sampleSpec()
	draft.CustomerCopy = "新草稿"
	draft.ContentVerID = "new-pending-content"
	_, err = s.UpsertPublishConfigDraft(tenant, campaign, draft, "test")
	must(t, err)
	live, err := s.GetPublishedConfig(tenant, campaign)
	must(t, err)
	if live.Version != 1 || live.Spec.CustomerCopy != original.CustomerCopy || live.Spec.ContentVerID != original.ContentVerID || live.Disclosure != disclosure || live.Status != "enabled" {
		t.Fatalf("draft drifted into live contract: %+v", live)
	}
	must(t, s.PausePublishConfig(tenant, campaign, "test"))
	row, err := s.GetPublishConfig(tenant, campaign)
	must(t, err)
	pausedAt := row.PausedAt
	draft.CustomerCopy = "暂停后编辑"
	_, err = s.UpsertPublishConfigDraft(tenant, campaign, draft, "test")
	must(t, err)
	row, err = s.GetPublishConfig(tenant, campaign)
	must(t, err)
	if row.Status != "paused" || row.PausedAt != pausedAt || row.Version != 3 {
		t.Fatalf("paused save silently resumed: %+v", row)
	}
	if err := s.ResumePublishConfig(tenant, campaign, 2, "test"); !errors.Is(err, ErrPublishConfigVersionConflict) {
		t.Fatalf("wrong live version resumed: %v", err)
	}
	must(t, s.ResumePublishConfig(tenant, campaign, 1, "test"))
	live, err = s.GetPublishedConfig(tenant, campaign)
	must(t, err)
	row, err = s.GetPublishConfig(tenant, campaign)
	must(t, err)
	if live.Version != 1 || live.Spec.ContentVerID != original.ContentVerID || row.Version != 3 || row.Spec.CustomerCopy != draft.CustomerCopy || row.PausedAt != "" {
		t.Fatalf("resume published pending draft: live=%+v edit=%+v", live, row)
	}
	// A delayed preflight for v1 cannot enable the current editable v3 by accident.
	if err := s.EnablePublishConfig(tenant, campaign, 1, disclosure, "test"); !errors.Is(err, ErrPublishConfigVersionConflict) {
		t.Fatalf("stale enable committed: %v", err)
	}
}

func TestPublishedConfigMissingOrInvalidRevisionFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "payload", "disclosure", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			s := openConfigStore(t)
			tenant, _, campaign, _ := configFixture(t, s)
			_, err := s.UpsertPublishConfigDraft(tenant, campaign, sampleSpec(), "test")
			must(t, err)
			disc := campaignenable.BuildDisclosure(sampleSpec(), 1, timeFixed()).JSON()
			must(t, s.EnablePublishConfig(tenant, campaign, 1, disc, "test"))
			draft := sampleSpec()
			draft.CustomerCopy = "不能回退到这个新草稿"
			_, err = s.UpsertPublishConfigDraft(tenant, campaign, draft, "test")
			must(t, err)
			// Controlled historical corruption must not create a fallback authority.
			switch mode {
			case "missing":
				_, err = s.DB.Exec(`DELETE FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND action='enable'`, tenant, campaign)
			case "payload":
				_, err = s.DB.Exec(`UPDATE campaign_publish_config_revisions SET payload='{}' WHERE tenant_id=? AND campaign_id=? AND action='enable'`, tenant, campaign)
			case "disclosure":
				_, err = s.DB.Exec(`UPDATE campaign_publish_config_revisions SET payload=json_set(payload,'$.disclosure','{}') WHERE tenant_id=? AND campaign_id=? AND action='enable'`, tenant, campaign)
			case "mismatch":
				_, err = s.DB.Exec(`UPDATE campaign_publish_config_revisions SET payload=json_set(payload,'$.spec.customer_copy','不同告知') WHERE tenant_id=? AND campaign_id=? AND action='enable'`, tenant, campaign)
			}
			must(t, err)
			if _, err := s.GetPublishedConfig(tenant, campaign); !errors.Is(err, ErrPublishConfigSnapshotInvalid) {
				t.Fatalf("invalid snapshot fell back to draft: %v", err)
			}
			if err := s.ResumePublishConfig(tenant, campaign, 1, "test"); !errors.Is(err, ErrPublishConfigSnapshotInvalid) {
				t.Fatalf("invalid snapshot resumed: %v", err)
			}
		})
	}
}

func TestPublishedConfigConcurrentSaveAndExpectedEnableAreAtomic(t *testing.T) {
	for i := 0; i < 8; i++ {
		s := openConfigStore(t)
		tenant, _, campaign, _ := configFixture(t, s)
		first := sampleSpec()
		_, err := s.UpsertPublishConfigDraft(tenant, campaign, first, "test")
		must(t, err)
		must(t, s.EnablePublishConfig(tenant, campaign, 1, campaignenable.BuildDisclosure(first, 1, timeFixed()).JSON(), "test"))
		second := sampleSpec()
		second.CustomerCopy = "明确检查的第2版"
		_, err = s.UpsertPublishConfigDraft(tenant, campaign, second, "test")
		must(t, err)
		third := sampleSpec()
		third.CustomerCopy = "并发保存的第3版"
		start := make(chan struct{})
		var wg sync.WaitGroup
		var saveErr, enableErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, saveErr = s.UpsertPublishConfigDraft(tenant, campaign, third, "other-editor")
		}()
		go func() {
			defer wg.Done()
			<-start
			enableErr = s.EnablePublishConfig(tenant, campaign, 2, campaignenable.BuildDisclosure(second, 2, timeFixed()).JSON(), "publisher")
		}()
		close(start)
		wg.Wait()
		must(t, saveErr)
		if enableErr != nil && !errors.Is(enableErr, ErrPublishConfigVersionConflict) {
			t.Fatal(enableErr)
		}
		live, err := s.GetPublishedConfig(tenant, campaign)
		must(t, err)
		row, err := s.GetPublishConfig(tenant, campaign)
		must(t, err)
		if row.Version != 3 || row.Spec.CustomerCopy != third.CustomerCopy {
			t.Fatalf("draft save lost: %+v", row)
		}
		if live.Version == 2 {
			if enableErr != nil || live.Spec.CustomerCopy != second.CustomerCopy {
				t.Fatalf("partial enable: %+v %v", live, enableErr)
			}
		} else if live.Version == 1 {
			if !errors.Is(enableErr, ErrPublishConfigVersionConflict) {
				t.Fatalf("enable acknowledged without committing: %v", enableErr)
			}
		} else {
			t.Fatalf("unconfirmed concurrent draft went live: %+v", live)
		}
	}
}

func TestPublishedRewardPauseCommitPrecedesConfirmationTransaction(t *testing.T) {
	s, tenant, campaign, _, _, _, _ := seedRewardWorld(t)
	_, err := s.TransitionCampaign(campaign, tenant, "active")
	must(t, err)
	shop, err := s.CreateStore(tenant, "原预留门店", "", "test")
	must(t, err)
	spec := sampleSpec()
	spec.Reward.Slot = "first_publish"
	spec.Reward.StoreIDs = []string{shop.ID}
	for _, copy := range []string{"第1版", "第2版", "第3版"} {
		spec.CustomerCopy = copy
		_, err = s.UpsertPublishConfigDraft(tenant, campaign, spec, "test")
		must(t, err)
	}
	must(t, s.EnablePublishConfig(tenant, campaign, 3, campaignenable.BuildDisclosure(spec, 3, timeFixed()).JSON(), "test"))
	// Formal reward allocation now requires an actual participation which
	// accepted that published version. Keep the ordering test on real domain
	// starts instead of inventing participation IDs at the ledger boundary.
	parts := participation.New(s.DB, nil, participation.Options{})
	start := func(key string) string {
		t.Helper()
		p, err := parts.Start(context.Background(), participation.StartInput{TenantID: tenant, StoreID: shop.ID, CampaignID: campaign, LinkCode: "pause-contract", Source: participation.SourceManual, RuleVersion: 3, ContentPackVersion: spec.ContentVerID, VisitorID: key, IdempotencyKey: key, Window: participation.Window{StartsAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}})
		must(t, err)
		return p.Participation.ID
	}
	issuedPart := start("already-issued-before-pause")
	waitingPart := start("waiting-confirm")
	confirm := func(part, key string) (RewardBenefit, RewardOutcome, error) {
		return s.ConfirmRewardBenefit(RewardConfirmInput{TenantID: tenant, CampaignID: campaign, ParticipationID: part, Slot: "first_publish", RuleVersion: 3, EventID: key, Evidence: "staff_observed_submission", Actor: "staff", StoreID: shop.ID, Now: now()})
	}
	// The reverse ordering has already committed an immutable issuance fact.
	issued, _, err := confirm(issuedPart, "issue-before-pause")
	must(t, err)
	reserved, _, err := s.ReserveRewardBenefit(RewardReserveInput{TenantID: tenant, CampaignID: campaign, ParticipationID: waitingPart, Slot: "first_publish", RuleVersion: 3, EventID: "reserve-before-pause", Actor: "trusted-participation", TTLSeconds: 600}, now())
	must(t, err)
	afterPause := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		<-afterPause
		_, _, err := confirm(waitingPart, "confirm-enters-after-pause")
		result <- err
	}()
	must(t, s.PausePublishConfig(tenant, campaign, "owner"))
	_, renewal, err := s.ReserveRewardBenefit(RewardReserveInput{TenantID: tenant, CampaignID: campaign, ParticipationID: waitingPart, Slot: "first_publish", RuleVersion: 3, EventID: "renew-original-reservation-while-paused", Actor: "trusted-participation", TTLSeconds: 600}, now())
	must(t, err)
	if renewal.Outcome != "re_reserved" {
		t.Fatalf("pause discarded in-flight reservation recovery: %+v", renewal)
	}
	close(afterPause) // pause has committed before confirmation can enter its tx
	if err := <-result; !errors.Is(err, ErrPromiseBlocked) {
		t.Fatalf("confirmation after pause still issued: %v", err)
	}
	unchanged, err := s.GetRewardBenefit(reserved.ID, tenant, campaign)
	must(t, err)
	if unchanged.Status != RewardStatusReserved {
		t.Fatalf("rejected confirm changed original reservation: %+v", unchanged)
	}
	replay, outcome, err := confirm(issuedPart, "issue-before-pause")
	must(t, err)
	if !outcome.Replay || replay.ID != issued.ID {
		t.Fatalf("pause discarded committed issue replay: %+v %+v", replay, outcome)
	}
	replay, outcome, err = confirm(issuedPart, "read-issued-after-pause")
	must(t, err)
	if outcome.Outcome != "already_issued" || replay.ID != issued.ID {
		t.Fatalf("existing fact was treated as a new issue: %+v %+v", replay, outcome)
	}
	redeemed, _, err := s.RedeemRewardBenefit(tenant, campaign, issued.ID, "staff", shop.ID, "redeem-while-paused", now())
	must(t, err)
	if redeemed.Status != RewardStatusRedeemed {
		t.Fatalf("pause stopped original valid coupon: %+v", redeemed)
	}
}
