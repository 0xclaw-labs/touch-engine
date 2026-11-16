package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestRewardExpiredLastHoldReadOnlyAvailabilityAndNewStart(t *testing.T) {
	x := newCampaignLiveFixture(t)
	spec := x.spec
	spec.Reward.TotalQuantity = intPtr(1)
	x.publishStock(t, spec, 2, 1)
	current := time.Now().UTC()
	x.f.s.rewardClock = func() time.Time { return current }
	old := x.start(t, "last-unit-original")
	oldID := old["id"].(string)
	readSummary := func() map[string]any {
		t.Helper()
		status, out := x.public.public(t, "GET", x.route+"/reward-summary", visitorCookie, "")
		if status != 200 {
			t.Fatal(status, out)
		}
		return out
	}
	before := readSummary()
	if before["promise"].(map[string]any)["available_qty"] != float64(0) {
		t.Fatal(before)
	}
	// A hold stays occupied right up to its own original TTL.
	current = current.Add(9 * time.Minute)
	notDue := readSummary()
	if notDue["promise"].(map[string]any)["available_qty"] != float64(0) {
		t.Fatalf("unexpired hold reclaimed early: %v", notDue)
	}
	// Advance the shared business clock, without changing any stored deadline.
	current = current.Add(2 * time.Minute)
	after := readSummary()
	if after["promise"].(map[string]any)["available_qty"] != float64(1) || after["availability"].(map[string]any)["can_start"] != true {
		t.Fatalf("naturally expired last hold left public preflight sold out: %v", after)
	}
	var status string
	var ops int
	if err := x.f.s.St.DB.QueryRow(`SELECT status FROM reward_benefits WHERE participation_id=?`, oldID).Scan(&status); err != nil || status != "reserved" {
		t.Fatalf("browsing mutated ledger: %s %v", status, err)
	}
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_operations o JOIN reward_benefits b ON b.id=o.benefit_id WHERE b.participation_id=? AND o.op='expire'`, oldID).Scan(&ops); err != nil || ops != 0 {
		t.Fatalf("browsing wrote expiry operation: %d %v", ops, err)
	}
	type result struct {
		status int
		out    map[string]any
	}
	ch := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			status, out := x.postStart(t, fmt.Sprintf("new-after-expiry-%d", i), fmt.Sprintf("touch_participation_visitor=%032x", i+400))
			ch <- result{status, out}
		}(i)
	}
	close(start)
	winners := 0
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.status == 201 && r.out["benefit_reservation_state"] == "confirmed" {
			winners++
		} else if r.status != 422 && !(r.status == 201 && r.out["benefit_reservation_state"] != "confirmed") {
			t.Fatalf("unexpected last unit contender: %+v", r)
		}
	}
	if winners != 1 {
		t.Fatalf("expired final allocation had %d winners", winners)
	}
	got, out := x.retryHold(t, oldID, visitorCookie)
	if got != 200 || out["id"] != oldID || out["benefit_reservation_state"] != "failed" {
		t.Fatalf("old participant falsely kept newly occupied stock: %d %v", got, out)
	}
	got, out = x.public.public(t, "GET", "/api/v1/participations/"+oldID, visitorCookie, "")
	if got != 200 || out["benefit_reservation_state"] != "failed" {
		t.Fatalf("read forgot original failed recovery: %d %v", got, out)
	}
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits WHERE campaign_id=? AND rule_version=2 AND status='reserved'`, x.f.camp1ID).Scan(&ops); err != nil || ops != 1 {
		t.Fatalf("last unit overallocated after expiry: %d %v", ops, err)
	}
}
