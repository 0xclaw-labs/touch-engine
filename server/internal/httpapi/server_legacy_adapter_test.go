package httpapi

import (
	"context"
	"encoding/json"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"net/http"
	"testing"
	"time"
)

type legacyCapture struct {
	requests []publishhandoff.PrepareRequest
}

func (a *legacyCapture) Platform() string { return "xiaohongshu" }
func (a *legacyCapture) Prepare(_ context.Context, r publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	a.requests = append(a.requests, r)
	return publishhandoff.PrepareResult{ClientAction: publishhandoff.ClientAction{Type: publishhandoff.ClientActionJSMediaHandoff, Params: map[string]string{"sdk": "test-only", "account_ref": "trusted"}}}, nil
}
func (a *legacyCapture) Submit(context.Context, publishhandoff.Handoff, publishhandoff.PrepareResult) error {
	return publishhandoff.ErrUnsupported
}
func (a *legacyCapture) Query(context.Context, publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	return publishhandoff.QueryOutcome{}, publishhandoff.ErrUnsupported
}
func (a *legacyCapture) VerifyCallback(context.Context, publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, publishhandoff.ErrUnsupported
}
func TestCampaignNormalHandoffUsesOriginalAdapterWithoutSecondParticipation(t *testing.T) {
	f := newHandoffFixture(t, true)
	res := f.s.St.ResolveLink(f.code, time.Now())
	v, e := f.s.St.GetAdoptedVersion(f.tenA, res.Campaign.ID, "xiaohongshu")
	if e != nil {
		t.Fatal(e)
	}
	p, e := f.s.Participation.Start(context.Background(), participation.StartInput{TenantID: f.tenA, StoreID: res.Campaign.StoreID, CampaignID: res.Campaign.ID, LinkCode: f.code, Source: participation.SourceQR, RuleVersion: 1, ContentPackVersion: v.ID, VisitorID: testVisitor, IdempotencyKey: "legacy-adapter-test"})
	if e != nil {
		t.Fatal(e)
	}
	f.s.Cfg.PublishHandoffMerchantAppRef = "test-app"
	cap := channeladmission.ActionCapability{ID: "test-c4-js", SeedVersion: 1, CatalogCode: "A02", Platform: "xiaohongshu", Method: channeladmission.ActionMethodJSMedia, Scenario: "store_promo", ActionKind: "publish", Label: "test", AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusAdmitted, RequiredInputs: []string{"content_pack_version"}, FieldSupport: map[string]string{"body": "unsupported"}, EvidenceGrade: channeladmission.EvidenceOfficialSnapshot, CheckedAt: "2026-11-09", ValidUntil: "2027-02-07", Environments: map[string]channeladmission.EnvCapabilities{"desktop_browser": handoffV2EnvCaps("supported", "supported", "unknown", "unknown")}}
	for _, env := range []string{"ios_safari", "android_browser", "ios_wechat", "android_wechat", "target_app"} {
		cap.Environments[env] = cap.Environments["desktop_browser"]
	}
	if _, _, e = f.s.St.UpsertActionCapability(cap, store.ControlAudit{Actor: "test", Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	adapter := &legacyCapture{}
	f.s.PublishHandoff.SetAdapter(adapter)
	route := "/api/v1/public/links/" + f.code + "/publish-handoff"
	status, plan := f.public(t, "GET", route+"/routes?platform=xiaohongshu&with_snapshot=1&adapter_mode=1&environment=desktop_browser&participation_id="+p.Participation.ID, visitorCookie, "")
	if status != http.StatusOK || plan["confirmation_required"] == nil {
		t.Fatalf("plan %d %v", status, plan)
	}
	body, _ := json.Marshal(map[string]any{"participation_id": p.Participation.ID, "platform": "xiaohongshu", "adapter_mode": true, "environment": "desktop_browser", "confirmed_content_version": plan["confirmation_required"]})
	status, started := f.public(t, "POST", route, visitorCookie, string(body))
	if status != http.StatusCreated {
		t.Fatalf("start %d %v", status, started)
	}
	h := started["handoff"].(map[string]any)
	id := h["handoff_id"].(string)
	if len(adapter.requests) != 1 || len(adapter.requests[0].MediaRefs) != 2 || adapter.requests[0].FieldSupport["body"] != "unsupported" {
		t.Fatalf("adapter context not connected: %#v", adapter.requests)
	}
	status, restored := f.public(t, "GET", route+"/routes?platform=xiaohongshu&with_snapshot=1&adapter_mode=1&environment=desktop_browser&participation_id="+p.Participation.ID, visitorCookie, "")
	if status != 200 || restored["handoff"].(map[string]any)["handoff_id"] != id {
		t.Fatalf("restore %d %v", status, restored)
	}
	var count int
	f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM participations WHERE campaign_id=?`, res.Campaign.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("created second participation: %d", count)
	}
	status, _ = f.public(t, "GET", "/api/v1/public/handoffs/"+id, "touch_participation_visitor=wrong-visitor", "")
	if status != 404 {
		t.Fatalf("wrong visitor=%d", status)
	}

}

func TestCampaignRewardCannotBorrowPromotionPermissionFromLegacyAPI(t *testing.T) {
	f := newHandoffFixture(t, true)
	res := f.s.St.ResolveLink(f.code, time.Now())
	v, e := f.s.St.GetAdoptedVersion(f.tenA, res.Campaign.ID, "xiaohongshu")
	if e != nil {
		t.Fatal(e)
	}
	// A genuine legacy participation predates the new, unpublished reward draft.
	// Saving the draft cannot authorize a reward route via promotion permission.
	p := f.startParticipation(t)
	spec := validEnableSpec(v.ID)
	spec.Reward.StoreIDs = []string{res.Campaign.StoreID}
	if _, e = f.s.St.UpsertPublishConfigDraft(f.tenA, res.Campaign.ID, spec, "test"); e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(map[string]any{"participation_id": p, "platform": "xiaohongshu", "scenario": "store_promo", "confirmed_content_version": "any", "adapter_mode": false})
	status, _ := f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, string(payload))
	if status != 422 {
		t.Fatalf("borrowed non-reward permission: %d", status)
	}
	var count int
	f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM publish_handoffs`).Scan(&count)
	if count != 0 {
		t.Fatalf("refused route created %d handoffs", count)
	}
}
