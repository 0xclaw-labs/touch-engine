package httpapi

import (
	"context"
	"fmt"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"strings"
	"testing"
	"time"
)

// The normal public start must cross the original reward authority, rather
// than requiring tests or customers to invoke the trusted reservation API.
func TestParticipationRewardReserverNormalHTTPStart(t *testing.T) {
	x := newCampaignLiveFixture(t)
	p := x.start(t, "normal-reserver-start")
	if p["benefit_reservation_state"] != "confirmed" {
		t.Fatalf("normal public start did not reserve real stock: %v", p)
	}
	var id, status, slot string
	if err := x.f.s.St.DB.QueryRow(`SELECT id,status,benefit_slot FROM reward_benefits WHERE tenant_id=? AND campaign_id=? AND participation_id=?`, x.f.tenA, x.f.camp1ID, p["id"]).Scan(&id, &status, &slot); err != nil {
		t.Fatalf("normal participation has no authoritative reward reservation: %v", err)
	}
	if id == "" || status != "reserved" || slot != x.spec.Reward.Slot {
		t.Fatalf("wrong real reservation identity/status/slot: %s %s %s", id, status, slot)
	}
	var ref string
	if err := x.f.s.St.DB.QueryRow(`SELECT benefit_reservation_ref FROM participations WHERE id=?`, p["id"]).Scan(&ref); err != nil || ref != id {
		t.Fatalf("participation was not linked to original reward authority: %s %v", ref, err)
	}
}

// Only the admission directory is controlled here; participation, revision
// resolution, reward allocation and every HTTP route use the production stack.
type delayedRealReserver struct {
	inner                  *campaignBenefitReserver
	blocked                bool
	commitThenLoseResponse bool
}

func (d *delayedRealReserver) ResolveStart(ctx context.Context, in participation.StartInput) (participation.StartInput, error) {
	return d.inner.ResolveStart(ctx, in)
}
func (d *delayedRealReserver) ReadReservation(ctx context.Context, ref participation.ParticipationRef) (participation.ReserveResult, error) {
	return d.inner.ReadReservation(ctx, ref)
}
func (d *delayedRealReserver) Reserve(ctx context.Context, ref participation.ParticipationRef) (participation.ReserveResult, error) {
	if d.blocked {
		return participation.ReserveResult{}, context.DeadlineExceeded
	}
	result, err := d.inner.Reserve(ctx, ref)
	if d.commitThenLoseResponse {
		return participation.ReserveResult{}, context.DeadlineExceeded
	}
	return result, err
}
func (x *campaignLiveFixture) publishStock(t *testing.T, spec campaignenable.Spec, version, qty int) {
	t.Helper()
	path := "/api/v1/campaigns/" + x.f.camp1ID + "/publish-config"
	status, out := x.f.owner(t, "PUT", path, mustJSON(t, spec))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-inventory/prepare", mustJSON(t, map[string]any{"request_key": fmt.Sprintf("reserver-stock-%d", version), "expected_version": version, "name": "原承诺真实库存", "quantity": qty}))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = x.f.owner(t, "POST", path+"/enable", mustJSON(t, map[string]any{"expected_version": version}))
	if status != 200 {
		t.Fatal(status, out)
	}
}
func (x *campaignLiveFixture) postStart(t *testing.T, key, cookie string) (int, map[string]any) {
	t.Helper()
	return x.public.public(t, "POST", "/api/v1/participations", cookie, mustJSON(t, map[string]any{"link_code": x.public.code, "touchpoint_source": "qr", "idempotency_key": key}))
}
func (x *campaignLiveFixture) retryHold(t *testing.T, id, cookie string) (int, map[string]any) {
	t.Helper()
	return x.public.public(t, "POST", "/api/v1/participations/"+id+"/reservation", cookie, `{}`)
}
func TestParticipationRewardReserverLastUnitConcurrentReplayAndScope(t *testing.T) {
	x := newCampaignLiveFixture(t)
	spec := x.spec
	spec.Reward.TotalQuantity = intPtr(1)
	x.publishStock(t, spec, 2, 1)
	type response struct {
		status int
		body   map[string]any
		cookie string
	}
	results := make(chan response, 2)
	ready := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-ready
			cookie := fmt.Sprintf("touch_participation_visitor=%032x", i+42)
			status, out := x.postStart(t, fmt.Sprintf("last-unit-%d", i), cookie)
			results <- response{status, out, cookie}
		}(i)
	}
	close(ready)
	var winner response
	confirmed := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.status == 201 && r.body["benefit_reservation_state"] == "confirmed" {
			winner = r
			confirmed++
		} else if r.status != 422 && !(r.status == 201 && r.body["benefit_reservation_state"] != "confirmed") {
			t.Fatalf("unexpected race response: %+v", r)
		}
	}
	if confirmed != 1 {
		t.Fatalf("last unit had %d winners", confirmed)
	}
	id := winner.body["id"].(string)
	var key string
	if err := x.f.s.St.DB.QueryRow(`SELECT idempotency_key FROM participations WHERE id=?`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	status, out := x.postStart(t, key, winner.cookie)
	if status != 200 || out["id"] != id || out["benefit_reservation_state"] != "confirmed" {
		t.Fatalf("last original hold blocked by zero free stock: %d %v", status, out)
	}
	status, out = x.postStart(t, key, "touch_participation_visitor=ffffffffffffffffffffffffffffffff")
	if status != 409 {
		t.Fatalf("foreign idempotency key exposed original: %d %v", status, out)
	}
	if out["id"] == id {
		t.Fatal("foreign actor leaked participation")
	}
	status, out = x.retryHold(t, id, "touch_participation_visitor=ffffffffffffffffffffffffffffffff")
	if status != 404 {
		t.Fatalf("foreign actor resumed original hold: %d %v", status, out)
	}
	p, err := x.f.s.Participation.GetForVisitor(context.Background(), id, strings.TrimPrefix(winner.cookie, "touch_participation_visitor="))
	if err != nil {
		t.Fatal(err)
	}
	ref := participation.ParticipationRef{ParticipationID: p.ID, TenantID: x.f.tenB, StoreID: p.StoreID, CampaignID: p.CampaignID, BenefitSlot: p.BenefitSlot, RuleVersion: p.RuleVersion, ContentPackVersion: p.ContentPackVersion}
	result, err := (&campaignBenefitReserver{srv: x.f.s}).Reserve(context.Background(), ref)
	if err == nil && result.Reserved {
		t.Fatal("cross tenant reserve succeeded")
	}
	ref.TenantID = p.TenantID
	ref.StoreID = "foreign-store"
	result, err = (&campaignBenefitReserver{srv: x.f.s}).Reserve(context.Background(), ref)
	if err == nil && result.Reserved {
		t.Fatal("cross store reserve succeeded")
	}
	var n, ops int
	if err = x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits WHERE campaign_id=? AND rule_version=2 AND status='reserved'`, x.f.camp1ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("stock overallocated: %d %v", n, err)
	}
	if err = x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_operations o JOIN reward_benefits b ON b.id=o.benefit_id WHERE b.campaign_id=? AND b.participation_id=? AND o.op='reserve'`, x.f.camp1ID, id).Scan(&ops); err != nil || ops != 1 {
		t.Fatalf("replay allocated twice: %d %v", ops, err)
	}
}
func TestParticipationRewardReserverUnknownRecoveryPinsOriginalPublishedVersion(t *testing.T) {
	x := newCampaignLiveFixture(t)
	d := &delayedRealReserver{inner: &campaignBenefitReserver{srv: x.f.s}, blocked: true}
	x.f.s.Participation.SetBenefitReserver(d)
	p := x.start(t, "unknown-original-v1")
	if p["benefit_reservation_state"] != "pending" {
		t.Fatal(p)
	}
	id := p["id"].(string)
	status, attempt := x.public.public(t, "POST", "/api/v1/participations/"+id+"/attempts", visitorCookie, `{"platform":"douyin","handoff_method":"h5"}`)
	if status != 201 {
		t.Fatal(status, attempt)
	}
	status, out := x.public.public(t, "POST", "/api/v1/participations/"+id+"/handoff", visitorCookie, mustJSON(t, map[string]any{"purpose": "customer_resume", "attempt_id": attempt["id"]}))
	if status != 409 || out["error"] != "reservation_unconfirmed" {
		t.Fatalf("unconfirmed hold entered publication: %d %v", status, out)
	}
	var frozen string
	if err := x.f.s.St.DB.QueryRow(`SELECT status FROM participation_attempts WHERE participation_id=?`, id).Scan(&frozen); err != nil || frozen != "prepared" {
		t.Fatalf("failed hold froze attempt: %s %v", frozen, err)
	}
	draft := x.nextDraft(t)
	draft.Reward.Slot = "new-version-slot"
	x.publishStock(t, draft, 2, 3)
	d.blocked = false
	status, out = x.retryHold(t, id, visitorCookie)
	if status != 200 || out["id"] != id || out["rule_version"] != float64(1) || out["content_pack_version"] != x.spec.ContentVerID || out["benefit_reservation_state"] != "confirmed" {
		t.Fatalf("retry drifted from original v1: %d %v", status, out)
	}
	var slot string
	var version int
	if err := x.f.s.St.DB.QueryRow(`SELECT benefit_slot,rule_version FROM reward_benefits WHERE participation_id=?`, id).Scan(&slot, &version); err != nil || slot != x.spec.Reward.Slot || version != 1 {
		t.Fatalf("recovery used current stock: %s %d %v", slot, version, err)
	}
	// A real committed reservation whose HTTP result is lost remains recoverable.
	d.commitThenLoseResponse = true
	unknown := x.start(t, "unknown-after-real-commit")
	if unknown["benefit_reservation_state"] != "pending" {
		t.Fatal(unknown)
	}
	status, out = x.public.public(t, "GET", "/api/v1/participations/"+unknown["id"].(string), visitorCookie, "")
	if status != 200 || out["benefit_reservation_state"] != "confirmed" {
		t.Fatalf("read did not recover committed fact: %d %v", status, out)
	}
	d.commitThenLoseResponse = false
	status, out = x.retryHold(t, unknown["id"].(string), visitorCookie)
	if status != 200 || out["id"] != unknown["id"] || out["benefit_reservation_state"] != "confirmed" {
		t.Fatal(status, out)
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/publish-config/pause", "")
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = x.retryHold(t, id, visitorCookie)
	if status != 200 || out["benefit_reservation_state"] == "confirmed" {
		t.Fatalf("pause did not revoke new task continuation: %d %v", status, out)
	}
	var count int
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM participations WHERE idempotency_key='unknown-original-v1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("recovery replaced original p: %d %v", count, err)
	}
}
func TestParticipationRewardReserverActionUpgradeKeepsRulesAndChosenWork(t *testing.T) {
	x := newCampaignLiveFixture(t)
	dir := &stubASDirectory{ok: true, ref: participation.ActionRef{ActionID: "reward-action", CatalogCode: "A04", Platform: "douyin", Kind: "share", TenantID: x.f.tenA, StoreID: x.f.store1ID, CampaignID: x.f.camp1ID, StorefrontRevision: 1, ActionRevision: 4, RuleVersion: 77, RuleSnapshot: `{"anti_abuse_version":77}`, ContentPackVersion: x.spec.ContentVerID, Enabled: true}}
	x.f.s.Participation.SetActionDirectory(dir)
	status, sess := x.public.public(t, "POST", "/api/v1/public/actions/reward-action/sessions", visitorCookie, sessionCreateBody("upgrade-real-hold"))
	if status != 201 {
		t.Fatal(status, sess)
	}
	sid := sess["action_session_id"].(string)
	path := "/api/v1/public/action-sessions/" + sid + "/participations"
	status, out := x.public.public(t, "POST", path, visitorCookie, `{"touchpoint_source":"nfc"}`)
	if status != 201 {
		t.Fatal(status, out)
	}
	p := out["participation"].(map[string]any)
	if p["benefit_reservation_state"] != "confirmed" || p["reservation_required"] != true || p["rule_version"] != float64(1) || out["rule_version"] != float64(77) {
		t.Fatalf("anti abuse and reward versions mixed: %v", out)
	}
	status, out = x.public.public(t, "POST", path, visitorCookie, `{"touchpoint_source":"nfc"}`)
	if status != 200 || out["participation"].(map[string]any)["id"] != p["id"] {
		t.Fatalf("upgrade replay allocated a replacement: %d %v", status, out)
	}
	var frozen int
	var snapshot string
	if err := x.f.s.St.DB.QueryRow(`SELECT rule_version,rule_snapshot FROM action_sessions WHERE id=?`, sid).Scan(&frozen, &snapshot); err != nil || frozen != 77 || snapshot != dir.ref.RuleSnapshot {
		t.Fatalf("session anti abuse snapshot changed: %d %s %v", frozen, snapshot, err)
	}
	dir.ref.ContentPackVersion = "different-confirmed-work"
	status, sess = x.public.public(t, "POST", "/api/v1/public/actions/reward-action/sessions", "touch_participation_visitor=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", sessionCreateBody("mismatch-chosen-work"))
	if status != 201 {
		t.Fatal(status, sess)
	}
	status, out = x.public.public(t, "POST", "/api/v1/public/action-sessions/"+sess["action_session_id"].(string)+"/participations", "touch_participation_visitor=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", `{"touchpoint_source":"nfc"}`)
	if status != 409 || out["error"] != "version_mismatch" {
		t.Fatalf("upgrading silently switched chosen work: %d %v", status, out)
	}
}

func TestParticipationRewardReserverIssuedFactSurvivesClaimExpiryAndPause(t *testing.T) {
	x := newCampaignLiveFixture(t)
	p := x.start(t, "issued-before-claim-expiry")
	id := p["id"].(string)
	status, out := x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": id, "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": "issue-original-before-expiry", "evidence": x.spec.VerificationMode, "store_id": x.f.store1ID}))
	if status != 200 {
		t.Fatal(status, out)
	}
	bid := out["benefit"].(map[string]any)["id"].(string)
	// Advance only the trusted port's qualification clock. The original issued
	// coupon still has its independent, later redemption deadline.
	end, err := time.Parse(time.RFC3339, x.spec.Reward.ClaimUntil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &campaignBenefitReserver{srv: x.f.s, clock: func() time.Time { return end.Add(time.Hour) }}
	x.f.s.Participation.SetBenefitReserver(adapter)
	status, out = x.postStart(t, "new-after-original-claim-expiry", visitorCookie)
	if status != 409 && status != 422 {
		t.Fatalf("claim expiry admitted a new reward participation: %d %v", status, out)
	}
	status, out = x.public.public(t, "GET", "/api/v1/participations/"+id, visitorCookie, "")
	if status != 200 || out["benefit_reservation_state"] != "confirmed" || out["reservation_required"] != true {
		t.Fatalf("claim deadline hid original issued fact: %d %v", status, out)
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/publish-config/pause", "")
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = x.retryHold(t, id, visitorCookie)
	if status != 200 || out["benefit_reservation_state"] != "confirmed" || out["id"] != id {
		t.Fatalf("old issued fact became a pending new reservation: %d %v", status, out)
	}
	var ref string
	if err := x.f.s.St.DB.QueryRow(`SELECT benefit_reservation_ref FROM participations WHERE id=?`, id).Scan(&ref); err != nil || ref != bid {
		t.Fatalf("issued identity changed: %s %v", ref, err)
	}
	status, out = x.f.owner(t, "POST", "/api/v1/campaigns/"+x.f.camp1ID+"/reward-benefits/"+bid+"/redeem", mustJSON(t, map[string]any{"event_id": "redeem-issued-after-claim-expiry", "store_id": x.f.store1ID}))
	if status != 200 || out["benefit"].(map[string]any)["id"] != bid || out["benefit"].(map[string]any)["status"] != "redeemed" {
		t.Fatalf("pause or claim expiry blocked old coupon: %d %v", status, out)
	}
	var holds int
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_operations WHERE benefit_id=? AND op='reserve'`, bid).Scan(&holds); err != nil || holds != 1 {
		t.Fatalf("old issued read/retry allocated again: %d %v", holds, err)
	}
}
