package matrixconsume

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

func TestStoreScopeWithoutCampaignWireContractAndIdempotentReplay(t *testing.T) {
	srv := newWireDraftServer("active")
	c := wireConsumer(t, srv, permJSON)
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	actor := Actor{PrincipalID: "usr", TenantID: "ten", BrandID: "brd", Role: "org_owner"}
	req := matrixhandoff.Request{
		TenantID:            "ten",
		BrandID:             "brd",
		StoreID:             "sto_nocamp",
		ScopeKind:           matrixhandoff.ScopeStore,
		ActivityID:          "",
		ActivityVersion:     2,
		ContentPackID:       "pack_solo",
		ContentVersionID:    "ver_solo",
		Assets:              []matrixhandoff.Asset{{ID: "ast_solo", Hash: strings.Repeat("cd", 32), Kind: "video", GrantRef: "grant_solo"}},
		OfferText:           "无活动门店招牌菜88元",
		OfferExpiry:         time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Disclosure:          "这是门店商家账号受限草稿，不是已经发出的内容。",
		ReturnLocationToken: "return:store:sto_nocamp",
		ReturnURL:           "/work/stores/sto_nocamp/content?pack_id=pack_solo",
		IdempotencyKey:      "idem-store-solo-1",
	}

	first, err := c.Schedule(context.Background(), now, actor, req, Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatalf("store schedule err: %v", err)
	}
	if !first.MatrixButton.Enabled || first.OutboundComplete || first.PlanState != matrixhandoff.PlanStateDraftSaved {
		t.Fatalf("unexpected outcome: %+v", first)
	}
	if srv.puts != 1 {
		t.Fatalf("puts = %d, want 1", srv.puts)
	}
	if srv.lastBody.SourceProjectID != "store:sto_nocamp" || srv.lastBody.CampaignID != "" {
		t.Fatalf("store wire body = %+v", srv.lastBody)
	}
	if srv.lastBody.ReturnTarget.Path != "/work/stores/sto_nocamp/content?pack_id=pack_solo" {
		t.Fatalf("return target = %+v", srv.lastBody.ReturnTarget)
	}

	// Confirm the draft, then replay Schedule with same idempotency key: must
	// recover the same draft and NEVER post a second draft to matrix.
	confirmed, err := c.ConfirmDraft(now, actor, first.Draft.ID)
	if err != nil || !confirmed.Draft.Approved || confirmed.Draft.PlanState != matrixhandoff.PlanStatePendingApproval {
		t.Fatalf("confirm = %+v err=%v", confirmed, err)
	}
	replay, err := c.Schedule(context.Background(), now, actor, req, Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatalf("replay err: %v", err)
	}
	if srv.puts != 1 || replay.Draft.ID != first.Draft.ID || replay.PlanID != first.PlanID || !replay.Draft.Approved {
		t.Fatalf("replay after confirm failed: puts=%d replay=%+v", srv.puts, replay)
	}
}

func TestConsumerAuthorityContentTypeAndPaymentBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

	t.Run("customer, operator_admin, revoked delegate, personal payer rejected", func(t *testing.T) {
		cases := []struct {
			name   string
			actor  Actor
			mutate func(*matrixhandoff.Request)
			reason string
		}{
			{
				name:   "customer_flag",
				actor:  Actor{PrincipalID: "u1", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner", IsCustomer: true},
				reason: matrixhandoff.ReasonCustomerPathForbidden,
			},
			{
				name:   "operator_admin",
				actor:  Actor{PrincipalID: "u2", TenantID: "tnt_a", BrandID: "br_a", Role: matrixhandoff.RoleOperatorAdmin},
				reason: matrixhandoff.ReasonOperatorNotPublisher,
			},
			{
				name:   "delegate_revoked",
				actor:  Actor{PrincipalID: "u3", TenantID: "tnt_a", BrandID: "br_a", Role: matrixhandoff.RoleDelegate, Revoked: true},
				reason: matrixhandoff.ReasonDelegateRevoked,
			},
			{
				name:   "delegate_personal_payment_flag",
				actor:  Actor{PrincipalID: "u4", TenantID: "tnt_a", BrandID: "br_a", Role: matrixhandoff.RoleDelegate},
				mutate: func(r *matrixhandoff.Request) { r.ActingAsPersonalPayer = true },
				reason: matrixhandoff.ReasonDelegatePaymentForbidden,
			},
			{
				name:   "delegate_personal_payment_scope",
				actor:  Actor{PrincipalID: "u5", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner", PaymentScope: matrixhandoff.PaymentScopeDelegatePersonal},
				reason: matrixhandoff.ReasonDelegatePaymentForbidden,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_x", Status: "draft"}}
				effects := &matrixhandoff.EffectSink{}
				c := New(PermissionFunc(readyPermission), sink, effects)
				req := sampleRequest()
				if tc.mutate != nil {
					tc.mutate(&req)
				}
				out, err := c.Schedule(context.Background(), now, tc.actor, req, Surfaces{LeadEnabled: true, UGCEnabled: true})
				if err == nil || out.MatrixButton.Enabled || out.MatrixButton.Reason != tc.reason || sink.puts != 0 {
					t.Fatalf("got err=%v out=%+v puts=%d", err, out, sink.puts)
				}
				if !out.LeadButton.Enabled || !out.UGCButton.Enabled || effects.RewardWrites != 0 || effects.UGCWrites != 0 || effects.LeadWrites != 0 {
					t.Fatalf("other surfaces or effects changed: out=%+v effects=%+v", out, effects)
				}
			})
		}
	})

	t.Run("image masquerading as video, expired asset, revoked asset fail closed", func(t *testing.T) {
		cases := []struct {
			name   string
			assets []matrixhandoff.Asset
			reason string
		}{
			{
				name:   "image_masquerading_as_video",
				assets: []matrixhandoff.Asset{{ID: "a1", Hash: strings.Repeat("aa", 32), Kind: "image", DeclaredContentType: "video"}},
				reason: matrixhandoff.ReasonWrongContentType,
			},
			{
				name:   "needs_video",
				assets: []matrixhandoff.Asset{{ID: "a2", Hash: strings.Repeat("bb", 32), Kind: "image"}},
				reason: matrixhandoff.ReasonNeedsVideo,
			},
			{
				name:   "asset_expired",
				assets: []matrixhandoff.Asset{{ID: "a3", Hash: strings.Repeat("cc", 32), Kind: "video", ExpiresAt: now.Add(-time.Hour)}},
				reason: matrixhandoff.ReasonAssetExpired,
			},
			{
				name:   "asset_revoked",
				assets: []matrixhandoff.Asset{{ID: "a4", Hash: strings.Repeat("dd", 32), Kind: "video", Revoked: true}},
				reason: matrixhandoff.ReasonAssetRevoked,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_x", Status: "draft"}}
				c := New(PermissionFunc(readyPermission), sink, &matrixhandoff.EffectSink{})
				req := sampleRequest()
				req.Assets = tc.assets
				out, err := c.Schedule(context.Background(), now, Actor{
					PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
				}, req, Surfaces{LeadEnabled: true, UGCEnabled: true})
				if err != nil || out.MatrixButton.Enabled || out.MatrixButton.Reason != tc.reason || sink.puts != 0 || out.OutboundComplete {
					t.Fatalf("got err=%v out=%+v puts=%d", err, out, sink.puts)
				}
			})
		}
	})
}

func TestConsumerLayeredStatesNullMetricsAndStaleReceipts(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_layered", Status: "draft"}}
	effects := &matrixhandoff.EffectSink{}
	c := New(PermissionFunc(readyPermission), sink, effects)
	actor := Actor{PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner"}

	out, err := c.Schedule(context.Background(), now, actor, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmDraft(now, actor, out.Draft.ID); err != nil {
		t.Fatal(err)
	}
	// Without official authority, claiming published fails closed.
	if _, err := c.ApplyReceipt(now, actor, out.Draft.ID, matrixhandoff.Receipt{
		PlanID:      "pln_layered",
		TargetState: matrixhandoff.PlanStatePublished,
		Sequence:    1,
	}); matrixhandoff.ReasonOf(err) != matrixhandoff.ReasonNoOfficialAuthority {
		t.Fatalf("expected no_official_publish_authority, got %v", err)
	}
	// With official authority, published is recorded with null unknown metrics and 0 customer effects.
	authActor := actor
	authActor.OfficialPublishGranted = true
	res, err := c.ApplyReceipt(now, authActor, out.Draft.ID, matrixhandoff.Receipt{
		PlanID:           "pln_layered",
		TargetState:      matrixhandoff.PlanStatePublished,
		Sequence:         2,
		OfficialVerified: true,
	})
	if err != nil || !res.Applied || res.Draft.PlanState != matrixhandoff.PlanStatePublished {
		t.Fatalf("verified receipt = %+v err=%v", res, err)
	}
	if res.CustomerEvidenceEligible || res.CustomerRewardEligible || effects.RewardWrites != 0 || effects.UGCWrites != 0 || effects.LeadWrites != 0 {
		t.Fatalf("customer separation violated: %+v effects=%+v", res, effects)
	}
	raw, _ := json.Marshal(res.Draft.Metrics)
	if string(raw) != `{"reach":null,"likes":null,"leads":null}` {
		t.Fatalf("unknown metrics JSON = %s, want all null", string(raw))
	}
}

func TestConsumerMatrixTimeoutAndOutageLeavesSurfacesOpen(t *testing.T) {
	slowOrDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	}))
	defer slowOrDown.Close()

	c := New(PermissionFunc(readyPermission), NewHTTPDraftSink(slowOrDown.URL, slowOrDown.Client()), &matrixhandoff.EffectSink{})
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		Actor{PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner"},
		sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatalf("matrix outage should degrade gracefully, got err=%v", err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != ReasonMatrixSupply {
		t.Fatalf("matrix button = %+v", out.MatrixButton)
	}
	if !out.LeadButton.Enabled || !out.UGCButton.Enabled || out.OutboundComplete {
		t.Fatalf("matrix outage broke lead/ugc surfaces: %+v", out)
	}

	// Explicit transport error also degrades cleanly without erroring out core surfaces.
	errSink := &scriptedSink{available: true, err: errors.New("context deadline exceeded")}
	c2 := New(PermissionFunc(readyPermission), errSink, &matrixhandoff.EffectSink{})
	out2, err := c2.Schedule(context.Background(), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		Actor{PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner"},
		sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil || out2.MatrixButton.Enabled || !out2.LeadButton.Enabled || !out2.UGCButton.Enabled {
		t.Fatalf("timeout degrade failed: out=%+v err=%v", out2, err)
	}
}
