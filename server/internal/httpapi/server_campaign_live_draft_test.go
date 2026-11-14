package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
)

type campaignLiveFixture struct {
	f      *configFixture
	public *handoffFixture
	spec   campaignenable.Spec
	route  string
}

func newCampaignLiveFixture(t *testing.T) *campaignLiveFixture {
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
	spec.Reward.ClaimFrom = now.Add(-24 * time.Hour).Format(time.RFC3339)
	spec.Reward.ClaimUntil = now.Add(24 * time.Hour).Format(time.RFC3339)
	spec.Reward.RedeemFrom = spec.Reward.ClaimFrom
	spec.Reward.RedeemUntil = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	path := "/api/v1/campaigns/" + f.camp1ID + "/publish-config"
	status, out := f.owner(t, "PUT", path, mustJSON(t, spec))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = f.owner(t, "POST", path+"/enable", "")
	if status != 200 {
		t.Fatalf("enable v1: %d %v", status, out)
	}
	if _, err := f.s.St.TransitionCampaign(f.camp1ID, f.tenA, "active"); err != nil {
		t.Fatal(err)
	}
	link, err := f.s.St.CreateLink(f.tenA, f.camp1ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &campaignLiveFixture{f: f, public: &handoffFixture{s: f.s, ts: f.ts, tenA: f.tenA, code: link.Code}, spec: spec, route: "/api/v1/public/links/" + link.Code}
}
func (x *campaignLiveFixture) start(t *testing.T, key string) map[string]any {
	t.Helper()
	status, out := x.public.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": x.public.code, "touchpoint_source": "qr", "idempotency_key": key}))
	if status != 201 {
		t.Fatalf("start %s: %d %v", key, status, out)
	}
	return out
}
func (x *campaignLiveFixture) nextDraft(t *testing.T) campaignenable.Spec {
	t.Helper()
	old, err := x.f.s.St.GetContentPackVersion(x.spec.ContentVerID, x.f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	v, err := x.f.s.St.CreateContentPackVersion(x.f.tenA, old.PackID, contentpack.VersionSpec{ContentKind: old.ContentKind, Video: old.Video, Cover: old.Cover, Title: "待发布新内容", Body: old.Body, Topics: old.Topics, StoreName: old.StoreName, PriceNote: old.PriceNote, ValidNote: old.ValidNote, PlatformVersions: old.PlatformVersions, GrantRefs: old.GrantRefs, Marking: old.Marking, Source: old.Source}, contentpack.StatusCandidate, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.f.s.St.AdoptContentPackVersion(x.f.tenA, old.PackID, v.ID, "douyin", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	spec := x.spec
	spec.ContentVerID = v.ID
	spec.CustomerCopy = "第2版待发布承诺"
	n := 80
	spec.Reward.TotalQuantity = &n
	return spec
}

func TestCampaignLiveDraftSaveKeepsPublishedHTTPContract(t *testing.T) {
	x := newCampaignLiveFixture(t)
	f := x.f
	path := "/api/v1/campaigns/" + f.camp1ID + "/publish-config"
	p1 := x.start(t, "v1-before-save")
	draft := x.nextDraft(t)
	status, out := f.owner(t, "PUT", path, mustJSON(t, draft))
	if status != 200 || out["version"] != float64(2) {
		t.Fatal(status, out)
	}
	// Saving a draft is not an unpublish operation. All guest authorities agree.
	status, out = x.public.public(t, "GET", x.route+"/content-choices", "", "")
	if status != 200 {
		t.Fatalf("saving v2 draft withdrew live v1 choices: %d %v", status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["version_id"] != x.spec.ContentVerID {
		t.Fatalf("draft replaced published choices: %v", out)
	}
	status, out = f.owner(t, "GET", path+"/snapshot", "")
	if status != 200 || out["version"] != float64(1) || out["disclosure"].(map[string]any)["customer_copy"] != x.spec.CustomerCopy {
		t.Fatalf("G3 live snapshot mixed draft: %d %v", status, out)
	}
	status, out = x.public.public(t, "GET", x.route+"/reward-summary", "", "")
	if status != 200 || out["promise"].(map[string]any)["description"] != x.spec.CustomerCopy || out["promise"].(map[string]any)["enabled"] != true || out["availability"].(map[string]any)["can_start"] != true {
		t.Fatalf("live reward promise changed on save/adopt: %d %v", status, out)
	}
	pStillV1 := x.start(t, "v1-after-draft")
	if pStillV1["content_pack_version"] != x.spec.ContentVerID || pStillV1["rule_version"] != float64(1) {
		t.Fatalf("new participant got draft: %v", pStillV1)
	}
	// Explicit publish must still reject an unprepared v2 instead of reusing v1 stock.
	status, out = f.owner(t, "POST", path+"/enable", `{"expected_version":2}`)
	if status != 422 {
		t.Fatalf("unprepared draft enabled using old stock: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-inventory/prepare", `{"request_key":"stock-v2","name":"第2版门店券","quantity":10}`)
	if status != 200 && status != 201 {
		t.Fatalf("prepare v2: %d %v", status, out)
	}
	if out["rule_version"] != float64(2) {
		t.Fatalf("prepared stock used live version instead of explicit draft: %v", out)
	}
	status, out = x.public.public(t, "GET", x.route+"/reward-summary", "", "")
	if status != 200 || out["promise"].(map[string]any)["description"] != x.spec.CustomerCopy {
		t.Fatalf("prepared unpublished stock replaced live promise: %d %v", status, out)
	}
	for _, slot := range out["slots"].([]any) {
		if slot.(map[string]any)["rule_version"] != float64(1) {
			t.Fatalf("draft stock advertised before enable: %v", out)
		}
	}
	status, out = f.owner(t, "POST", path+"/enable", `{"expected_version":2}`)
	if status != 200 || out["enabled_version"] != float64(2) {
		t.Fatalf("explicit publish v2: %d %v", status, out)
	}
	status, out = x.public.public(t, "GET", x.route+"/reward-summary", "", "")
	if status != 200 || out["promise"].(map[string]any)["description"] != draft.CustomerCopy || out["slots"].([]any)[0].(map[string]any)["rule_version"] != float64(2) {
		t.Fatalf("published reward reader did not switch to v2: %d %v", status, out)
	}
	p2 := x.start(t, "v2-after-enable")
	if p2["content_pack_version"] != draft.ContentVerID || p2["rule_version"] != float64(2) {
		t.Fatalf("new participant did not get v2: %v", p2)
	}
	status, out = x.public.public(t, "GET", x.route+"/content-snapshot?participation_id="+p1["id"].(string)+"&version_id="+x.spec.ContentVerID+"&platform=douyin", visitorCookie, "")
	if status != 200 || out["pack_version_id"] != x.spec.ContentVerID {
		t.Fatalf("old participant changed after explicit publish: %d %v", status, out)
	}
	cfg, err := f.s.St.GetPublishConfig(f.tenA, f.camp1ID)
	if err != nil || cfg.Spec.ContentVerID != draft.ContentVerID {
		t.Fatalf("editor did not keep draft: %+v %v", cfg, err)
	}
}

func TestCampaignLiveDraftPauseResumeKeepsDraftAndIssuedBenefits(t *testing.T) {
	x := newCampaignLiveFixture(t)
	f := x.f
	path := "/api/v1/campaigns/" + f.camp1ID + "/publish-config"
	issuedP := x.start(t, "already-issued")
	waitingP := x.start(t, "waiting-issue")
	confirm := func(p map[string]any, key string) (int, map[string]any) {
		return f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": p["id"], "benefit_slot": x.spec.Reward.Slot, "rule_version": 1, "event_id": key, "evidence": x.spec.VerificationMode, "store_id": f.store1ID}))
	}
	status, benefit := confirm(issuedP, "issue-before-draft")
	if status != 200 {
		t.Fatalf("initial issue: %d %v", status, benefit)
	}
	draft := x.nextDraft(t)
	status, out := f.owner(t, "PUT", path, mustJSON(t, draft))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = f.owner(t, "POST", path+"/pause", "")
	if status != 200 {
		t.Fatalf("pause with newer draft: %d %v", status, out)
	}
	pausedAt := out["paused_at"]
	status, out = confirm(waitingP, "issue-while-paused")
	if status != 422 {
		t.Fatalf("config pause allowed new issuance: %d %v", status, out)
	}
	status, out = confirm(issuedP, "issue-before-draft")
	if status != 200 || out["replay"] != true {
		t.Fatalf("config pause blocked committed issue replay: %d %v", status, out)
	}
	draft.CustomerCopy = "第3版仍待准备库存"
	status, out = f.owner(t, "PUT", path, mustJSON(t, draft))
	if status != 200 || out["paused_at"] != pausedAt || out["status"] != "paused" {
		t.Fatalf("save while paused changed lifecycle: %d %v", status, out)
	}
	status, out = x.public.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": x.public.code, "touchpoint_source": "qr", "idempotency_key": "paused-new"}))
	if status != 409 {
		t.Fatalf("paused draft accepted new participant: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", path+"/resume", `{"expected_version":1}`)
	if status != 200 || out["enabled_version"] != float64(1) || out["version"] != float64(3) || out["paused_at"] != "" {
		t.Fatalf("resume published v1 should retain draft v3: %d %v", status, out)
	}
	p := x.start(t, "resumed-v1")
	if p["rule_version"] != float64(1) || p["content_pack_version"] != x.spec.ContentVerID {
		t.Fatalf("resume published an unprepared draft: %v", p)
	}
	status, out = confirm(waitingP, "issue-after-resume")
	if status != 200 || out["benefit"].(map[string]any)["rule_version"] != float64(1) {
		t.Fatalf("old participation no longer issued by frozen terms: %d %v", status, out)
	}
	pendingEnd := x.start(t, "pending-before-end")
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/status", `{"status":"ended"}`)
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = confirm(pendingEnd, "issue-after-ended")
	if status != 422 {
		t.Fatalf("ended campaign allowed issuance: %d %v", status, out)
	}
	id := benefit["benefit"].(map[string]any)["id"].(string)
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-benefits/"+id+"/redeem", mustJSON(t, map[string]any{"event_id": "redeem-old-after-ended", "store_id": f.store1ID}))
	if status != 200 || out["benefit"].(map[string]any)["status"] != "redeemed" {
		t.Fatalf("draft/pause/end changed issued coupon: %d %v", status, out)
	}
}

func TestCampaignLiveDraftExpectedVersionAndOldInventoryRecovery(t *testing.T) {
	x := newCampaignLiveFixture(t)
	f := x.f
	path := "/api/v1/campaigns/" + f.camp1ID + "/publish-config"
	draft := x.nextDraft(t)
	status, out := f.owner(t, "PUT", path, mustJSON(t, draft))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = f.owner(t, "POST", path+"/enable", `{"expected_version":1}`)
	if status != 409 || out["error"] != "version_mismatch" {
		t.Fatalf("stale reviewed version not rejected before preflight: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-inventory/prepare", `{"request_key":"stale-stock-request","expected_version":1,"name":"旧页面未确认新承诺","quantity":1}`)
	if status != 409 {
		t.Fatalf("stock silently bound to concurrent new draft: %d %v", status, out)
	}
	var newBatches int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_inventory WHERE campaign_id=? AND rule_version=2`, f.camp1ID).Scan(&newBatches); err != nil || newBatches != 0 {
		t.Fatalf("stale preparation left partial stock: %d %v", newBatches, err)
	}
	status, accepted := f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-inventory/prepare", `{"request_key":"stock-accepted-v2","expected_version":2,"name":"第2版库存","quantity":3}`)
	if status != 200 || accepted["rule_version"] != float64(2) {
		t.Fatalf("prepare v2: %d %v", status, accepted)
	}
	draft.CustomerCopy = "又保存的第3版草稿"
	status, out = f.owner(t, "PUT", path, mustJSON(t, draft))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/reward-inventory/prepare?request_key=stock-accepted-v2", "")
	if status != 200 || out["id"] != accepted["id"] || out["rule_version"] != float64(2) {
		t.Fatalf("old request recovery drifted to new draft: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", path+"/enable", `{"expected_version":3}`)
	if status != 422 {
		t.Fatalf("v3 reused recovered v2 stock: %d %v", status, out)
	}
	p := x.start(t, "still-v1-after-preflight-refusals")
	if p["rule_version"] != float64(1) {
		t.Fatalf("failed prepare/publish withdrew or changed live promise: %v", p)
	}
}

func TestCampaignLiveDraftInvalidPublicationNeverFallsBackToEditableSpec(t *testing.T) {
	x := newCampaignLiveFixture(t)
	f := x.f
	path := "/api/v1/campaigns/" + f.camp1ID + "/publish-config"
	draft := x.nextDraft(t)
	status, out := f.owner(t, "PUT", path, mustJSON(t, draft))
	if status != 200 {
		t.Fatal(status, out)
	}
	if _, err := f.s.St.DB.Exec(`DELETE FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND action='enable'`, f.tenA, f.camp1ID); err != nil {
		t.Fatal(err)
	}
	status, out = f.owner(t, "GET", path, "")
	if status != 200 || out["published_status"] != "unknown" || out["version"] != float64(2) {
		t.Fatalf("editor falsely claimed live publication: %d %v", status, out)
	}
	status, out = x.public.public(t, "GET", x.route+"/content-choices", "", "")
	if status != 404 {
		t.Fatalf("missing revision fell back to candidate: %d %v", status, out)
	}
	status, out = x.public.public(t, "GET", x.route+"/reward-summary", "", "")
	if status != 200 || out["availability"].(map[string]any)["can_start"] != false || len(out["slots"].([]any)) != 0 {
		t.Fatalf("missing revision advertised reward: %d %v", status, out)
	}
	status, out = x.public.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": x.public.code, "touchpoint_source": "qr", "idempotency_key": "missing-revision"}))
	if status != 409 {
		t.Fatalf("missing revision accepted participant: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", path+"/resume", `{"expected_version":1}`)
	if status != 503 {
		t.Fatalf("missing publication resumed draft: %d %v", status, out)
	}
}

func TestCampaignLiveDraftResumeFullyReservedBatchKeepsNewStockGate(t *testing.T) {
	x := newCampaignLiveFixture(t)
	f := x.f
	path := "/api/v1/campaigns/" + f.camp1ID + "/publish-config"
	spec := x.spec
	spec.CustomerCopy = "仅一份的正式承诺"
	one := 1
	spec.Reward.TotalQuantity = &one
	status, out := f.owner(t, "PUT", path, mustJSON(t, spec))
	if status != 200 {
		t.Fatal(status, out)
	}
	status, batch := f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-inventory/prepare", `{"request_key":"single-original-stock","expected_version":2,"name":"唯一原券","quantity":1}`)
	if status != 200 {
		t.Fatal(status, batch)
	}
	status, out = f.owner(t, "POST", path+"/enable", `{"expected_version":2}`)
	if status != 200 {
		t.Fatal(status, out)
	}
	p := x.start(t, "original-reserved-participant")
	status, reserved := f.owner(t, "POST", "/internal/v1/reward-reservations", mustJSON(t, map[string]any{"tenant_id": f.tenA, "campaign_id": f.camp1ID, "participation_id": p["id"], "benefit_slot": spec.Reward.Slot, "rule_version": 2, "event_id": "reserve-single-original", "ttl_seconds": 600, "actor": "controlled-participation-service"}))
	if status != 200 || reserved["benefit"].(map[string]any)["status"] != "reserved" {
		t.Fatalf("original real reservation: %d %v", status, reserved)
	}
	status, out = f.owner(t, "POST", path+"/pause", "")
	if status != 200 {
		t.Fatal(status, out)
	}
	// Zero availability alone may be restored; an administratively paused batch
	// is a separate, real refusal and must not be swallowed by that exception.
	inventoryPath := "/api/v1/campaigns/" + f.camp1ID + "/reward-inventory/" + batch["id"].(string) + "/status"
	status, out = f.owner(t, "POST", inventoryPath, `{"status":"paused"}`)
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = f.owner(t, "POST", path+"/resume", `{"expected_version":2}`)
	if status != 422 {
		t.Fatalf("resume swallowed unavailable batch: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", inventoryPath, `{"status":"active"}`)
	if status != 200 {
		t.Fatal(status, out)
	}
	status, out = f.owner(t, "POST", path+"/resume", `{"expected_version":2}`)
	if status != 200 || out["enabled_version"] != float64(2) {
		t.Fatalf("fully reserved valid original could not resume: %d %v", status, out)
	}
	status, out = x.public.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": x.public.code, "touchpoint_source": "qr", "idempotency_key": "new-user-with-no-free-stock"}))
	if status != 422 || out["error"] != "benefit_stock_missing" {
		t.Fatalf("resume allowed unfunded new participation: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/reward-confirm", mustJSON(t, map[string]any{"participation_id": p["id"], "benefit_slot": spec.Reward.Slot, "rule_version": 2, "event_id": "confirm-original-reservation", "evidence": spec.VerificationMode, "store_id": f.store1ID}))
	if status != 200 || out["benefit"].(map[string]any)["id"] != reserved["benefit"].(map[string]any)["id"] || out["benefit"].(map[string]any)["status"] != "issued" {
		t.Fatalf("original reservation could not complete after resume: %d %v", status, out)
	}
	rows, err := f.s.St.ListRewardInventory(f.tenA, f.camp1ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range rows {
		if inv.ID == batch["id"] && (inv.TotalQty != 1 || inv.AvailableQty != 0) {
			t.Fatalf("reservation/issuance changed stock conservation: %+v", inv)
		}
	}
	var parts, issued int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM participations WHERE campaign_id=?`, f.camp1ID).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits WHERE campaign_id=? AND rule_version=2 AND status='issued'`, f.camp1ID).Scan(&issued); err != nil {
		t.Fatal(err)
	}
	if parts != 1 || issued != 1 {
		t.Fatalf("blocked new user or old confirmation created extra facts: participants=%d issued=%d", parts, issued)
	}
}
