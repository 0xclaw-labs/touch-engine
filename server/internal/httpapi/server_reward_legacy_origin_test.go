package httpapi

import (
	"context"
	"fmt"
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"net/http/httptest"
	"testing"
	"time"
)

func legacyRewardBeforePromise(t *testing.T) (*campaignLiveFixture, string) {
	t.Helper()
	f, _ := newCfgFixture(t, true)
	f.s.Cfg.FeatureContentPacks = true
	f.s.Cfg.FeatureParticipation = true
	f.s.Cfg.FeatureRewardOps = true
	f.ts.Close()
	f.ts = httptest.NewServer(f.s.Handler())
	t.Cleanup(f.ts.Close)
	spec := f.seedEnableableWorld(t, 1)
	spec.Reward.StoreIDs = []string{f.store1ID}
	now := time.Now().UTC()
	spec.Reward.ClaimFrom = now.Add(-time.Hour).Format(time.RFC3339)
	spec.Reward.ClaimUntil = now.Add(time.Hour).Format(time.RFC3339)
	spec.Reward.RedeemFrom = spec.Reward.ClaimFrom
	spec.Reward.RedeemUntil = now.Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := f.s.St.TransitionCampaign(f.camp1ID, f.tenA, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	l, err := f.s.St.CreateLink(f.tenA, f.camp1ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	p := &handoffFixture{s: f.s, ts: f.ts, tenA: f.tenA, code: l.Code}
	status, out := p.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": l.Code, "touchpoint_source": "qr", "idempotency_key": "legacy-before"}))
	if status != 201 {
		t.Fatal(status, out)
	}
	id := out["id"].(string)
	if out["reservation_required"] != false {
		t.Fatal(out)
	}

	return &campaignLiveFixture{f: f, public: p, spec: spec, route: "/api/v1/public/links/" + l.Code}, id
}

func enableLegacyFixturePromise(t *testing.T, x *campaignLiveFixture) {
	t.Helper()
	path := "/api/v1/campaigns/" + x.f.camp1ID + "/publish-config"
	status, out := x.f.owner(t, "PUT", path, mustJSON(t, x.spec))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = x.f.owner(t, "POST", path+"/enable", "")
	if status != 200 {
		t.Fatal(status, out)
	}
}
func TestRewardLegacyParticipationBeforeFirstPromise(t *testing.T) {
	x, id := legacyRewardBeforePromise(t)
	status, attempt := x.public.public(t, "POST", "/api/v1/participations/"+id+"/attempts", visitorCookie, `{"platform":"douyin","handoff_method":"h5"}`)
	if status != 201 {
		t.Fatal(status, attempt)
	}
	enableLegacyFixturePromise(t, x)
	status, out := x.public.public(t, "GET", "/api/v1/participations/"+id, visitorCookie, "")
	if status != 200 || out["reservation_required"] != false || out["benefit_reservation_state"] != "not_requested" {
		t.Fatalf("legacy original contract changed: %d %v", status, out)
	}
	for i := 0; i < 2; i++ {
		status, out = x.retryHold(t, id, visitorCookie)
		if status != 200 || out["reservation_required"] != false || out["benefit_reservation_state"] != "not_requested" || out["id"] != id {
			t.Fatalf("legacy recovery inherited later reward: %d %v", status, out)
		}
	}
	status, out = x.public.public(t, "POST", "/api/v1/participations/"+id+"/handoff", visitorCookie, mustJSON(t, map[string]any{"purpose": "customer_resume", "attempt_id": attempt["id"]}))
	if status != 201 || out["participation"].(map[string]any)["benefit_reservation_state"] != "not_requested" {
		t.Fatalf("later promise blocked original attempt: %d %v", status, out)
	}
	status, out = x.postStart(t, "legacy-before", visitorCookie)
	if status != 200 || out["id"] != id || out["reservation_required"] != false {
		t.Fatalf("legacy start replay changed contract: %d %v", status, out)
	}
	// rule_version=1 is a numeric collision, not acceptance of formal v1.
	for _, version := range []int{1, 2} {
		status, out = x.f.owner(t, "POST", "/internal/v1/reward-reservations", mustJSON(t, map[string]any{"tenant_id": x.f.tenA, "campaign_id": x.f.camp1ID, "participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": version, "event_id": fmt.Sprintf("legacy-borrow-%d", version), "ttl_seconds": 600, "actor": "trusted-test"}))
		if status != 422 {
			t.Fatalf("legacy p borrowed later inventory: %d %v", status, out)
		}
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "legacy-borrow-confirm", "evidence": x.spec.VerificationMode, "store_id": x.f.store1ID}))
	if status != 422 {
		t.Fatalf("legacy p issued later promise: %d %v", status, out)
	}
	var benefits int
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits WHERE participation_id=?`, id).Scan(&benefits); err != nil || benefits != 0 {
		t.Fatalf("old attempt/recovery silently allocated: %d %v", benefits, err)
	}
	fresh := x.start(t, "new-under-formal-v1")
	if fresh["benefit_reservation_state"] != "confirmed" || fresh["reservation_required"] != true || fresh["rule_version"] != float64(1) {
		t.Fatalf("new p lost real reservation: %v", fresh)
	}
	ref := participation.ParticipationRef{ParticipationID: id, TenantID: x.f.tenB, CampaignID: x.f.camp1ID, StoreID: x.f.store1ID, BenefitSlot: id, RuleVersion: 1}
	result, err := (&campaignBenefitReserver{srv: x.f.s}).Reserve(context.Background(), ref)
	if err == nil && (result.Reserved || result.NotRequested) {
		t.Fatal("cross tenant identity was downgraded to legacy")
	}
	ref.TenantID = x.f.tenA
	ref.RuleVersion = 2
	result, err = (&campaignBenefitReserver{srv: x.f.s}).Reserve(context.Background(), ref)
	if err == nil && (result.Reserved || result.NotRequested) {
		t.Fatal("forged version was downgraded to legacy")
	}
}
func TestRewardLegacyMissingOrCorruptPublicationCannotDowngrade(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			x, id := legacyRewardBeforePromise(t)
			enableLegacyFixturePromise(t, x)
			var err error
			if kind == "missing" {
				_, err = x.f.s.St.DB.Exec(`DELETE FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND action='enable'`, x.f.tenA, x.f.camp1ID)
			} else {
				_, err = x.f.s.St.DB.Exec(`UPDATE campaign_publish_config_revisions SET payload='{}' WHERE tenant_id=? AND campaign_id=? AND action='enable'`, x.f.tenA, x.f.camp1ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			status, out := x.public.public(t, "GET", "/api/v1/participations/"+id, visitorCookie, "")
			if status != 200 || out["reservation_required"] != true || out["benefit_reservation_state"] == "not_requested" || out["benefit_reservation_state"] == "confirmed" {
				t.Fatalf("unknown publication treated as legacy: %d %v", status, out)
			}
			status, out = x.retryHold(t, id, visitorCookie)
			if status != 200 || out["benefit_reservation_state"] == "confirmed" || out["benefit_reservation_state"] == "not_requested" {
				t.Fatalf("unknown publication allocated: %d %v", status, out)
			}
			status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "unknown-history-confirm", "evidence": x.spec.VerificationMode, "store_id": x.f.store1ID}))
			if status != 422 {
				t.Fatalf("unknown history issued: %d %v", status, out)
			}
		})
	}
}
func TestRewardLegacyEstablishedRewardFactsSurviveFirstPromise(t *testing.T) {
	x, issuedID := legacyRewardBeforePromise(t)
	held := x.start(t, "legacy-already-held")
	heldID := held["id"].(string)
	confirm := func(id, event string) (int, map[string]any) {
		return x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": event, "evidence": x.spec.VerificationMode, "store_id": x.f.store1ID}))
	}
	status, issued := confirm(issuedID, "old-issued-before-formal")
	if status != 200 {
		t.Fatal(status, issued)
	}
	bid := issued["benefit"].(map[string]any)["id"].(string)
	status, heldBenefit := x.f.owner(t, "POST", "/internal/v1/reward-reservations", mustJSON(t, map[string]any{"tenant_id": x.f.tenA, "campaign_id": x.f.camp1ID, "participation_id": heldID, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "old-accepted-hold", "ttl_seconds": 600, "actor": "trusted-test"}))
	if status != 200 {
		t.Fatal(status, heldBenefit)
	}
	enableLegacyFixturePromise(t, x)
	status, out := confirm(issuedID, "read-old-issued-after-formal")
	if status != 200 || out["benefit"].(map[string]any)["id"] != bid {
		t.Fatalf("formal first enable rewrote old issued fact: %d %v", status, out)
	}
	status, out = confirm(heldID, "complete-old-established-hold")
	if status != 200 || out["benefit"].(map[string]any)["id"] != heldBenefit["benefit"].(map[string]any)["id"] {
		t.Fatalf("formal first enable canceled old established reward: %d %v", status, out)
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-benefits/"+bid+"/redeem", mustJSON(t, map[string]any{"event_id": "redeem-old-legacy-issued", "store_id": x.f.store1ID}))
	if status != 200 || out["benefit"].(map[string]any)["status"] != "redeemed" {
		t.Fatalf("old issued redemption blocked: %d %v", status, out)
	}
}

func TestRewardMissingEarlierPublicationNeverReclassifiesFormalParticipant(t *testing.T) {
	x := newCampaignLiveFixture(t)
	p := x.start(t, "formal-v1-before-v2")
	draft := x.nextDraft(t)
	x.publishStock(t, draft, 2, 3)
	if _, err := x.f.s.St.DB.Exec(`DELETE FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND action='enable' AND version=1`, x.f.tenA, x.f.camp1ID); err != nil {
		t.Fatal(err)
	}
	status, out := x.public.public(t, "GET", "/api/v1/participations/"+p["id"].(string), visitorCookie, "")
	if status != 200 || out["reservation_required"] != true || out["benefit_reservation_state"] == "not_requested" || out["benefit_reservation_state"] == "confirmed" {
		t.Fatalf("missing v1 proof was guessed to be pre-v2 legacy: %d %v", status, out)
	}
}

func TestRewardLegacyParticipationSurvivesUnpublishedDraft(t *testing.T) {
	x, id := legacyRewardBeforePromise(t)
	status, attempt := x.public.public(t, "POST", "/api/v1/participations/"+id+"/attempts", visitorCookie, `{"platform":"douyin","handoff_method":"h5"}`)
	if status != 201 {
		t.Fatal(status, attempt)
	}
	path := "/api/v1/campaigns/" + x.f.camp1ID + "/publish-config"
	for i := 0; i < 2; i++ {
		x.spec.CustomerCopy = fmt.Sprintf("尚未启用的编辑稿 %d", i+1)
		status, out := x.f.owner(t, "PUT", path, mustJSON(t, x.spec))
		if status != 200 {
			t.Fatal(status, out)
		}
		status, out = x.public.public(t, "GET", "/api/v1/participations/"+id, visitorCookie, "")
		if status != 200 || out["reservation_required"] != false || out["benefit_reservation_state"] != "not_requested" {
			t.Fatalf("unpublished draft blocked original participation: %d %v", status, out)
		}
		status, out = x.retryHold(t, id, visitorCookie)
		if status != 200 || out["id"] != id || out["reservation_required"] != false || out["benefit_reservation_state"] != "not_requested" {
			t.Fatalf("unpublished draft changed original recovery: %d %v", status, out)
		}
		status, out = x.postStart(t, "legacy-before", visitorCookie)
		if status != 200 || out["id"] != id || out["reservation_required"] != false {
			t.Fatalf("unpublished draft changed original replay: %d %v", status, out)
		}
	}
	status, out := x.public.public(t, "POST", "/api/v1/participations/"+id+"/handoff", visitorCookie, mustJSON(t, map[string]any{"purpose": "customer_resume", "attempt_id": attempt["id"]}))
	if status != 201 {
		t.Fatalf("unpublished draft blocked original handoff: %d %v", status, out)
	}
	status, out = x.postStart(t, "must-not-enter-draft", "touch_participation_visitor=abababababababababababababababab")
	if status != 409 {
		t.Fatalf("new customer entered an unpublished promise: %d %v", status, out)
	}
	status, out = x.f.owner(t, "POST", "/internal/v1/reward-reservations", mustJSON(t, map[string]any{"tenant_id": x.f.tenA, "campaign_id": x.f.camp1ID, "participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "draft-cannot-allocate", "ttl_seconds": 600, "actor": "trusted-test"}))
	if status != 422 {
		t.Fatalf("old participation borrowed unpublished inventory: %d %v", status, out)
	}
	var benefits int
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits WHERE participation_id=?`, id).Scan(&benefits); err != nil || benefits != 0 {
		t.Fatalf("draft silently allocated a benefit: %d %v", benefits, err)
	}
}

func TestRewardLegacyHeldBeforeUnpublishedDraftKeepsOriginalBenefit(t *testing.T) {
	x, id := legacyRewardBeforePromise(t)
	status, held := x.f.owner(t, "POST", "/internal/v1/reward-reservations", mustJSON(t, map[string]any{"tenant_id": x.f.tenA, "campaign_id": x.f.camp1ID, "participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "old-hold-before-first-draft", "ttl_seconds": 600, "actor": "trusted-test"}))
	if status != 200 {
		t.Fatal(status, held)
	}
	benefitID := held["benefit"].(map[string]any)["id"]
	status, out := x.f.owner(t, "PUT", "/api/v1/campaigns/"+x.f.camp1ID+"/publish-config", mustJSON(t, x.spec))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "confirm-original-before-any-publication", "evidence": x.spec.VerificationMode, "store_id": x.f.store1ID}))
	if status != 200 || out["benefit"].(map[string]any)["id"] != benefitID || out["benefit"].(map[string]any)["status"] != "issued" {
		t.Fatalf("unpublished draft blocked established original hold: %d %v", status, out)
	}
}

func TestRewardUnpublishedDraftRequiresIntactOriginEvidence(t *testing.T) {
	for _, kind := range []string{"missing_create", "corrupt_create", "publication_trace"} {
		t.Run(kind, func(t *testing.T) {
			x, id := legacyRewardBeforePromise(t)
			status, out := x.f.owner(t, "PUT", "/api/v1/campaigns/"+x.f.camp1ID+"/publish-config", mustJSON(t, x.spec))
			if status != 200 {
				t.Fatal(status, out)
			}
			query := `DELETE FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND action='create'`
			if kind == "corrupt_create" {
				query = `UPDATE campaign_publish_config_revisions SET payload='{}' WHERE tenant_id=? AND campaign_id=? AND action='create'`
			} else if kind == "publication_trace" {
				query = `UPDATE campaign_publish_config_revisions SET action='enable' WHERE tenant_id=? AND campaign_id=? AND action='create'`
			}
			if _, err := x.f.s.St.DB.Exec(query, x.f.tenA, x.f.camp1ID); err != nil {
				t.Fatal(err)
			}
			status, out = x.public.public(t, "GET", "/api/v1/participations/"+id, visitorCookie, "")
			if status != 200 || out["reservation_required"] != true || out["benefit_reservation_state"] == "not_requested" || out["benefit_reservation_state"] == "confirmed" {
				t.Fatalf("unproved draft was downgraded to legacy: %d %v", status, out)
			}
		})
	}
}
