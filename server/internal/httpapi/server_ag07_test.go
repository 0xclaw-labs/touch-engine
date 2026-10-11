package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixconsume"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func TestMatrixFromConfigDoesNotInventSupply(t *testing.T) {
	if matrixFromConfig(config.Config{}) != nil {
		t.Fatal("empty urls must leave the consumer unset")
	}
	if matrixFromConfig(config.Config{PublicPermissionURL: "http://127.0.0.1:9"}) == nil {
		t.Fatal("permission url should install a consumer")
	}
	f := newFixture(t, false)
	if f.s.Matrix != nil {
		t.Fatal("fixture env has no supply url")
	}
}

func TestSessionMembershipsIgnoreForgedTenant(t *testing.T) {
	f := newFixture(t, false)
	status, _, body := f.do(t, "GET", "/api/v1/session/memberships?tenant_id="+f.tenB, "sess-owner-a", f.tenB, "")
	if status != 200 {
		t.Fatalf("memberships = %d %v", status, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	row, _ := items[0].(map[string]any)
	if row["tenant_id"] != f.tenA || row["display_name"] != "商家A" || row["source"] != "membership" || row["role"] != "org_owner" {
		t.Fatalf("row = %v", row)
	}
	if status, _, who := f.do(t, "GET", "/api/v1/whoami", "sess-owner-a", "", ""); status != 400 {
		t.Fatalf("whoami without tenant = %d %v", status, who)
	}
	if status, _, who := f.do(t, "GET", "/api/v1/whoami", "sess-owner-a", f.tenB, ""); status != 403 {
		t.Fatalf("whoami forged tenant = %d %v", status, who)
	}
	if status, _, _ := f.do(t, "GET", "/api/v1/session/memberships", "", "", ""); status != 401 {
		t.Fatalf("guest memberships = %d", status)
	}

	extra, err := f.s.St.CreateMember(f.tenB, f.ownA, "staff", "跨户", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	status, _, body = f.do(t, "GET", "/api/v1/session/memberships", "sess-owner-a", f.tenB, "")
	items, _ = body["items"].([]any)
	if status != 200 || len(items) != 2 {
		t.Fatalf("two memberships = %d %v", status, items)
	}
	off := false
	if _, err := f.s.St.UpdateMember(extra.ID, nil, &off, nil); err != nil {
		t.Fatal(err)
	}
	status, _, body = f.do(t, "GET", "/api/v1/session/memberships", "sess-owner-a", "", "")
	items, _ = body["items"].([]any)
	if status != 200 || len(items) != 1 {
		t.Fatalf("disabled dropped = %d %v", status, items)
	}
	row, _ = items[0].(map[string]any)
	if row["tenant_id"] != f.tenA {
		t.Fatalf("remaining = %v", row)
	}
}

type countingSink struct {
	puts      int
	available bool
	ack       matrixconsume.DraftAck
	last      matrixhandoff.Draft
	lastAuth  matrixconsume.Authority
}

func (c *countingSink) Available() bool { return c.available }

func (c *countingSink) PutDraft(_ context.Context, doc matrixhandoff.Draft, auth matrixconsume.Authority) (matrixconsume.DraftAck, error) {
	c.puts++
	c.last = doc
	c.lastAuth = auth
	return c.ack, nil
}

func readyMatrix(sink *countingSink, effects *matrixhandoff.EffectSink) *matrixconsume.Consumer {
	gate := matrixconsume.PermissionFunc(func(context.Context, string, string, string) (matrixconsume.Permission, error) {
		return matrixconsume.Permission{
			Allowed: true, Supply: matrixconsume.SupplyReady, Reason: "ok",
			Authority: matrixconsume.Authority{
				PayerID: "pay_http", LicenseRef: "lic_http", LicenseExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		}, nil
	})
	return matrixconsume.New(gate, sink, effects)
}

func TestMatrixDraftMissingSupplyDisablesOnlyThatButton(t *testing.T) {
	f := newFixture(t, false)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "无矩阵", CreatedBy: f.ownA})
	if err != nil {
		t.Fatal(err)
	}
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	if status != 200 || body["outbound_complete"] != false {
		t.Fatalf("nil matrix = %d %v", status, body)
	}
	matrix, _ := body["matrix_button"].(map[string]any)
	lead, _ := body["lead_button"].(map[string]any)
	ugc, _ := body["ugc_button"].(map[string]any)
	if matrix["enabled"] != false || matrix["reason"] != matrixconsume.ReasonPermissionSupply || lead["enabled"] != false || ugc["enabled"] != true {
		t.Fatalf("buttons = %v", body)
	}
	enableLeadSurface(f)
	if _, err := f.s.St.UpsertLeadForm(f.tenA, camp.ID, "v1", false, f.ownA); err != nil {
		t.Fatal(err)
	}
	status, _, body = f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	lead, _ = body["lead_button"].(map[string]any)
	ugc, _ = body["ugc_button"].(map[string]any)
	matrix, _ = body["matrix_button"].(map[string]any)
	if status != 200 || matrix["enabled"] != false || lead["enabled"] != true || ugc["enabled"] != true {
		t.Fatalf("open lead form = %d %v", status, body)
	}
	if strings.Contains(stringMust(body["copy"]), "发布成功") || strings.Contains(stringMust(body["copy"]), "外发完成") {
		t.Fatalf("copy = %v", body["copy"])
	}
}

func TestMatrixDraftRecordsDraftNotOutbound(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft, Executed: false}}
	effects := &matrixhandoff.EffectSink{}
	f.s.Matrix = readyMatrix(sink, effects)
	enableLeadSurface(f)
	camp := seedMatrixCampaign(t, f, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), true)
	if _, err := f.s.St.UpsertLeadForm(f.tenA, camp.ID, "v1", false, f.ownA); err != nil {
		t.Fatal(err)
	}

	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"brand_id":"forged","sha256":"00"}`)
	if status != 200 || body["outbound_complete"] != false {
		t.Fatalf("draft = %d %v", status, body)
	}
	matrix, _ := body["matrix_button"].(map[string]any)
	lead, _ := body["lead_button"].(map[string]any)
	ugc, _ := body["ugc_button"].(map[string]any)
	copy := stringMust(body["copy"])
	if matrix["enabled"] != true || matrix["reason"] != matrixconsume.ReasonDraftRecorded || lead["enabled"] != true || ugc["enabled"] != true {
		t.Fatalf("buttons = %v", body)
	}
	if body["draft_status"] != matrixhandoff.StatusDraft || body["draft_id"] == "" || sink.puts != 1 {
		t.Fatalf("draft record = %v puts %d", body, sink.puts)
	}
	if strings.Contains(copy, "发布成功") || strings.Contains(copy, "外发完成") || effects.LeadWrites != 0 || effects.UGCWrites != 0 || effects.RewardWrites != 0 {
		t.Fatalf("copy %q effects %+v", copy, effects)
	}
	if sink.last.Activity.Version != 3 || len(sink.last.Activity.AssetHashes) != 1 || sink.last.Activity.AssetHashes[0] != strings.Repeat("ab", 32) || sink.last.Activity.Disclosure == "" || sink.last.ReturnLocationToken == "" || sink.last.Activity.OfferExpiry.IsZero() {
		t.Fatalf("posted draft = %+v", sink.last)
	}
	kept := f.s.Matrix.Drafts()
	if len(kept) != 1 || kept[0].ID != body["draft_id"] || kept[0].Activity.Version != 3 || kept[0].Activity.AssetHashes[0] != strings.Repeat("ab", 32) {
		t.Fatalf("stored drafts = %+v", kept)
	}
	// Replay (repeat click / retry / re-login) must reuse the recorded draft and
	// must not post a second matrix plan: puts stays at 1.
	if status, _, again := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, ""); status != 200 || again["draft_id"] != body["draft_id"] || len(f.s.Matrix.Drafts()) != 1 || sink.puts != 1 {
		t.Fatalf("replay = %d %v drafts %d", status, again, len(f.s.Matrix.Drafts()))
	}

	if status, _, denied := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-b", f.tenB, ""); status != 404 || sink.puts != 1 {
		t.Fatalf("cross tenant = %d %v puts %d", status, denied, sink.puts)
	}
	if status, _, staff := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-staff-a", f.tenA, ""); status != 422 || staff["outbound_complete"] != false || sink.puts != 1 {
		t.Fatalf("staff = %d %v puts %d", status, staff, sink.puts)
	}
}

func TestMatrixDraftRejectsDisabledMemberBeforeSupply(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft}}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})
	camp := seedMatrixCampaign(t, f, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), true)
	members, err := f.s.St.ListMembers(f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, m := range members {
		if m.PrincipalRef == f.ownA && m.Role == "org_owner" {
			id = m.ID
		}
	}
	off := false
	if _, err := f.s.St.UpdateMember(id, nil, &off, nil); err != nil {
		t.Fatal(err)
	}
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	if status != 403 || body["error"] != "member_disabled" || sink.puts != 0 || len(f.s.Matrix.Drafts()) != 0 {
		t.Fatalf("disabled = %d %v puts %d", status, body, sink.puts)
	}
}

func TestMatrixDraftEmptyEndsDoesNotInventExpiry(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft}}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})
	camp := seedMatrixCampaign(t, f, "", true)
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	matrix, _ := body["matrix_button"].(map[string]any)
	if status != 200 || matrix["enabled"] != false || matrix["reason"] != matrixhandoff.ReasonOfferExpired || sink.puts != 0 || len(f.s.Matrix.Drafts()) != 0 {
		t.Fatalf("empty ends = %d %v", status, body)
	}
	if status, _, again := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, ""); status != 200 || again["draft_id"] != "" || sink.puts != 0 {
		t.Fatalf("second empty ends = %d %v puts %d", status, again, sink.puts)
	}
}

func TestMatrixDraftLibraryErrorIsNotMissingVideo(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft}}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})
	camp := seedMatrixCampaign(t, f, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), true)
	if _, err := f.s.St.DB.Exec(`ALTER TABLE lib_assets RENAME TO lib_assets_down`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.s.St.DB.Exec(`ALTER TABLE lib_assets_down RENAME TO lib_assets`)
	})
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	if status != 500 || body["error"] != "internal" || sink.puts != 0 {
		t.Fatalf("library error = %d %v", status, body)
	}
	if matrix, _ := body["matrix_button"].(map[string]any); matrix["reason"] == matrixhandoff.ReasonNeedsVideo {
		t.Fatalf("library error disguised as needs_video: %v", body)
	}
}

func TestMatrixDraftBlocksExpiredAndMissingVideo(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft}}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})
	enableLeadSurface(f)

	expired := seedMatrixCampaign(t, f, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), true)
	if _, err := f.s.St.UpsertLeadForm(f.tenA, expired.ID, "v1", false, f.ownA); err != nil {
		t.Fatal(err)
	}
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+expired.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	matrix, _ := body["matrix_button"].(map[string]any)
	if status != 200 || matrix["reason"] != matrixhandoff.ReasonOfferExpired || sink.puts != 0 || body["outbound_complete"] != false {
		t.Fatalf("expired = %d %v", status, body)
	}

	plain := seedMatrixCampaign(t, f, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), false)
	if _, err := f.s.St.UpsertLeadForm(f.tenA, plain.ID, "v1", false, f.ownA); err != nil {
		t.Fatal(err)
	}
	status, _, body = f.do(t, "POST", "/api/v1/campaigns/"+plain.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	matrix, _ = body["matrix_button"].(map[string]any)
	lead, _ := body["lead_button"].(map[string]any)
	ugc, _ := body["ugc_button"].(map[string]any)
	if status != 200 || matrix["reason"] != matrixhandoff.ReasonNeedsVideo || lead["enabled"] != true || ugc["enabled"] != true || sink.puts != 0 {
		t.Fatalf("needs video = %d %v", status, body)
	}
}

func seedMatrixCampaign(t *testing.T, f *fixture, endsAt string, withVideo bool) store.Campaign {
	t.Helper()
	if err := f.s.St.BindTenantBrand(f.tenA, "brd_a"); err != nil {
		t.Fatal(err)
	}
	sto, err := f.s.St.CreateStore(f.tenA, "南山店", "南山大道", f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{
		TenantID: f.tenA, Title: "国庆档", PublicContent: "到店有礼", StoreID: sto.ID,
		EndsAt: endsAt, OrderRef: "ord_opaque", CreatedBy: f.ownA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !withVideo {
		return camp
	}
	sha := strings.Repeat("ab", 32)
	lib, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef: "ast_vid", SHA256: sha, MediaType: assetlib.MediaTypeVideo,
		Source: assetlib.SourceMerchantUpload, Purpose: "活动成片", GrantRef: "grant_1",
	}, sto.ID, f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, camp.ID, lib.AssetRef, "3", f.ownA); err != nil {
		t.Fatal(err)
	}
	return camp
}

func enableLeadSurface(f *fixture) {
	f.s.Cfg.FeatureLeadsCapture = true
	f.s.Cfg.NotifyBaseURL = "http://notify.test"
	f.s.Cfg.NotifyToken = "notify-token"
	f.s.Cfg.LeadsTargetApp = "leads-app"
	f.s.Cfg.LeadsPhonePepper = "pepper"
}

func stringMust(v any) string {
	s, _ := v.(string)
	return s
}
