package matrixconsume

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

func readyPermission(_ context.Context, _, _, _ string) (Permission, error) {
	return Permission{
		Allowed: true, Supply: SupplyReady, Reason: "brand_publish",
		Authority: Authority{PayerID: "pay_t", LicenseRef: "lic_t", LicenseExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
	}, nil
}

func missingPermission(_ context.Context, _, _, _ string) (Permission, error) {
	return Permission{Supply: SupplyMissing, Reason: "permission_unconfigured"}, nil
}

type scriptedSink struct {
	available bool
	ack       DraftAck
	err       error
	puts      int
	last      matrixhandoff.Draft
	lastAuth  Authority
}

func (s *scriptedSink) Available() bool { return s != nil && s.available }

func (s *scriptedSink) PutDraft(_ context.Context, doc matrixhandoff.Draft, auth Authority) (DraftAck, error) {
	s.puts++
	s.last = doc
	s.lastAuth = auth
	if s.err != nil {
		return DraftAck{}, s.err
	}
	return s.ack, nil
}

func sampleRequest() matrixhandoff.Request {
	return matrixhandoff.Request{
		TenantID:            "tnt_a",
		BrandID:             "br_a",
		StoreID:             "sto_a",
		ActivityID:          "cmp_a",
		ActivityVersion:     3,
		Assets:              []matrixhandoff.Asset{{ID: "asset_1", Hash: strings.Repeat("ab", 32), Kind: "video"}},
		OfferText:           "到店礼",
		OfferExpiry:         time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Disclosure:          "广告",
		ReturnLocationToken: "ret_1",
	}
}

func TestScheduleDraftIsNotOutbound(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft", Executed: false}}
	effects := &matrixhandoff.EffectSink{}
	c := New(PermissionFunc(readyPermission), sink, effects)
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.OutboundComplete || out.Draft.Executed || out.Draft.CustomerPublish || sink.puts != 1 {
		t.Fatalf("draft treated as send: %+v puts=%d", out, sink.puts)
	}
	if out.Draft.Status != "draft" || out.Draft.Plan.ID != "pln_1" {
		t.Fatalf("draft = %+v", out.Draft)
	}
	if strings.Contains(out.Copy, "发布成功") || strings.Contains(out.Copy, "外发完成") {
		t.Fatalf("copy claims a real send: %q", out.Copy)
	}
	if !out.MatrixButton.Enabled || out.LeadButton.Enabled != true || out.UGCButton.Enabled != true {
		t.Fatalf("buttons = matrix:%+v lead:%+v ugc:%+v", out.MatrixButton, out.LeadButton, out.UGCButton)
	}
	if effects.UGCWrites != 0 || effects.LeadWrites != 0 || effects.RewardWrites != 0 {
		t.Fatalf("customer effects moved: %+v", effects)
	}
	if sink.last.Activity.Version != 3 || len(sink.last.Activity.AssetHashes) != 1 || sink.last.Activity.AssetHashes[0] != strings.Repeat("ab", 32) || sink.last.Activity.Disclosure != "广告" || sink.last.ReturnLocationToken != "ret_1" || sink.last.Activity.OfferExpiry.IsZero() {
		t.Fatalf("preview = %+v", sink.last)
	}
	kept := c.Drafts()
	if len(kept) != 1 || kept[0].ID != out.Draft.ID || kept[0].Activity.Version != 3 || kept[0].Plan.ID != "pln_1" {
		t.Fatalf("kept = %+v", kept)
	}
	again, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil || again.Draft.ID != out.Draft.ID || len(c.Drafts()) != 1 {
		t.Fatalf("replay = %+v err %v drafts %d", again.Draft, err, len(c.Drafts()))
	}
}

func TestMissingSupplyDisablesOnlyMatrixButton(t *testing.T) {
	effects := &matrixhandoff.EffectSink{}
	c := New(PermissionFunc(missingPermission), &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_x", Status: "draft"}}, effects)
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != "permission_supply_missing" {
		t.Fatalf("matrix button = %+v", out.MatrixButton)
	}
	if out.LeadButton.Enabled != true || out.UGCButton.Enabled != false || out.OutboundComplete || out.Draft.ID != "" {
		t.Fatalf("other surfaces changed: %+v", out)
	}
}

// An allowed permission without the payer/license authority never reaches the
// matrix: no post, no record, a precise reason, other surfaces untouched.
func TestAllowedWithoutAuthorityBlocksPost(t *testing.T) {
	gate := PermissionFunc(func(_ context.Context, _, _, _ string) (Permission, error) {
		return Permission{Allowed: true, Supply: SupplyReady, Reason: "ok"}, nil
	})
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft"}}
	c := New(gate, sink, &matrixhandoff.EffectSink{})
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if sink.puts != 0 || out.MatrixButton.Enabled || out.MatrixButton.Reason != ReasonAuthorityMissing {
		t.Fatalf("gap posted: puts=%d button=%+v", sink.puts, out.MatrixButton)
	}
	if len(c.Drafts()) != 0 || out.OutboundComplete || !out.LeadButton.Enabled {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestMatrixSupplyMissingDoesNotRecordDraft(t *testing.T) {
	c := New(PermissionFunc(readyPermission), &scriptedSink{available: false}, &matrixhandoff.EffectSink{})
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != "matrix_supply_missing" || out.OutboundComplete || out.Draft.ID != "" {
		t.Fatalf("missing matrix = %+v", out)
	}
}

func TestPublishedAckIsNotCompletion(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_live", Status: "published", Executed: true}}
	c := New(PermissionFunc(readyPermission), sink, &matrixhandoff.EffectSink{})
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err == nil || !errors.Is(err, ErrNotDraft) {
		t.Fatalf("err = %v", err)
	}
	if out.OutboundComplete || out.Draft.Executed || out.MatrixButton.Reason != "matrix_ack_not_draft" {
		t.Fatalf("published ack accepted: %+v", out)
	}
}

func TestCrossTenantDoesNotSchedule(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft"}}
	c := New(PermissionFunc(readyPermission), sink, &matrixhandoff.EffectSink{})
	req := sampleRequest()
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_b", TenantID: "tnt_b", BrandID: "br_a", Role: "org_owner",
	}, req, Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err == nil || out.OutboundComplete || sink.puts != 0 || out.MatrixButton.Enabled {
		t.Fatalf("cross tenant scheduled: err=%v out=%+v puts=%d", err, out, sink.puts)
	}
}

// A repeated click, a timeout retry, or a re-login must resolve to the draft
// already recorded for the same activity version. It must not post a second
// plan to the matrix.
func TestRepeatScheduleDoesNotPostSecondMatrixPlan(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft", Executed: false}}
	c := New(PermissionFunc(readyPermission), sink, &matrixhandoff.EffectSink{})
	actor := Actor{PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner"}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	first, err := c.Schedule(context.Background(), now, actor, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	sink.ack = DraftAck{PlanID: "pln_2", Status: "draft", Executed: false}
	second, err := c.Schedule(context.Background(), now, actor, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if sink.puts != 1 {
		t.Fatalf("repeat schedule posted %d matrix drafts; want 1", sink.puts)
	}
	if second.Draft.ID != first.Draft.ID || second.Draft.Plan.ID != "pln_1" || !second.MatrixButton.Enabled || second.OutboundComplete {
		t.Fatalf("repeat schedule = %+v; want the first draft pln_1", second)
	}
	if kept := c.Drafts(); len(kept) != 1 || kept[0].Plan.ID != "pln_1" {
		t.Fatalf("kept drafts = %+v", kept)
	}
}

// Revocation is checked before the stored draft is reused, so a repeat
// click after the agent or member loses the grant is refused without a call.
func TestRepeatScheduleAfterRevokeIsRefusedWithoutPost(t *testing.T) {
	allowed := true
	gate := PermissionFunc(func(_ context.Context, _, _, _ string) (Permission, error) {
		if allowed {
			return Permission{Allowed: true, Supply: SupplyReady, Reason: "brand_publish",
				Authority: Authority{PayerID: "pay_t", LicenseRef: "lic_t", LicenseExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}, nil
		}
		return Permission{Allowed: false, Supply: SupplyReady, Reason: "grant_revoked"}, nil
	})
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft", Executed: false}}
	c := New(gate, sink, &matrixhandoff.EffectSink{})
	actor := Actor{PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner"}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.Schedule(context.Background(), now, actor, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true}); err != nil {
		t.Fatal(err)
	}
	allowed = false
	out, err := c.Schedule(context.Background(), now, actor, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != "grant_revoked" || sink.puts != 1 {
		t.Fatalf("revoked repeat = %+v puts=%d", out.MatrixButton, sink.puts)
	}
}
