package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLeadSourceTraceSurvivesRevoke(t *testing.T) {
	f := newLeadsFixture(t)
	if _, err := f.s.St.DB.Exec(`UPDATE campaigns SET order_ref=? WHERE id=?`, "ord_keep", f.campaignID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, f.campaignID, "ast_trace", "7", "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(context.Background())
	f.stub.DeliverAll()
	f.forwarder.Tick(context.Background())
	submit := payloadOf(t, f.stub.recorded()[0].Raw)
	if submit["trace_id"] != "lead:"+ref || submit["return_target"] != "/c/"+f.code || submit["asset_ref"] != "ast_trace" || submit["campaign_version"] != "7" || submit["grant_ref"] != "ord_keep" {
		t.Fatalf("submit trace = %v", submit)
	}
	if _, ok := submit["phone"]; ok {
		t.Fatal("phone key on submit fact")
	}

	if _, err := f.s.St.AddCampaignAsset(f.tenA, f.campaignID, "ast_later", "9", "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.DB.Exec(`UPDATE campaigns SET order_ref=? WHERE id=?`, "ord_changed", f.campaignID); err != nil {
		t.Fatal(err)
	}
	status, _, link := f.admin(t, "POST", "/api/v1/campaigns/"+f.campaignID+"/links", ``)
	other, _ := link["code"].(string)
	if status != 201 || other == "" || other == f.code {
		t.Fatalf("second link = %d %v", status, link)
	}
	body := `{"submission_ref":"` + ref + `","phone":"13800138000"}`
	if status, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+other+"/lead-revocations", body); status != 200 || resp["state"] != "revoked" {
		t.Fatalf("revoke = %d %v", status, resp)
	}
	if tick := f.forwarder.Tick(context.Background()); tick.Published != 1 {
		t.Fatalf("revoke tick = %+v", tick)
	}
	events := f.stub.recorded()
	if len(events) != 2 || events[1].Profile.SourceVersion != 2 || events[1].Profile.SourceRef != ref {
		t.Fatalf("revoke profile = %+v len %d", events, len(events))
	}
	revoked := payloadOf(t, events[1].Raw)
	if revoked["trace_id"] != submit["trace_id"] || revoked["return_target"] != submit["return_target"] || revoked["asset_ref"] != "ast_trace" || revoked["campaign_version"] != "7" || revoked["grant_ref"] != "ord_keep" {
		t.Fatalf("revoke trace = %v", revoked)
	}
	if revoked["return_target"] == "/c/"+other || revoked["asset_ref"] == "ast_later" || revoked["grant_ref"] == "ord_changed" {
		t.Fatalf("revoke recomputed current campaign: %v", revoked)
	}
	raw := events[0].Raw + events[1].Raw
	for _, forbidden := range []string{"13800138000", "张三"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("contact %q leaked", forbidden)
		}
	}
}

func payloadOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatal(err)
	}
	data, _ := probe["data"].(map[string]any)
	payload, _ := data["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("no payload in %s", raw)
	}
	return payload
}
