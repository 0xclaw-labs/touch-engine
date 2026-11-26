package matrixhandoff

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreScopeWithoutCampaignAllowsDraftAndReturnURL(t *testing.T) {
	client := videoFixture()
	sink := &EffectSink{}
	svc := New(client, sink)

	req := Request{
		TenantID:            "ten_a",
		BrandID:             "brand_a",
		StoreID:             "store_solo",
		ScopeKind:           ScopeStore,
		ActivityID:          "", // campaign-less store
		ActivityVersion:     1,
		ContentPackID:       "pack_1",
		ContentVersionID:    "ver_1",
		Assets:              []Asset{{ID: "media_v1", Hash: "sha_v1", Kind: "video", GrantRef: "grant_store_1"}},
		OfferText:           "门店招牌套餐立减20元",
		OfferExpiry:         handoffNow.Add(72 * time.Hour),
		Disclosure:          "这是门店商家账号受限草稿，不是已经发出的内容。",
		ReturnLocationToken: "return:store:store_solo",
		ReturnURL:           "/work/stores/store_solo/content?pack_id=pack_1",
		AccountPool:         []string{"secret_pool"},
		SocialSecret:        "top_secret",
	}

	res, err := svc.CreateDraft(handoffNow, publisher(), req)
	if err != nil {
		t.Fatalf("campaign-less store draft failed: %v", err)
	}
	if res.Success {
		t.Fatal("draft must never report live publish success")
	}
	if res.Draft.ScopeKind != ScopeStore || res.Draft.Activity.ActivityID != "" || res.Draft.Activity.StoreID != "store_solo" {
		t.Fatalf("unexpected store scope draft: %+v", res.Draft)
	}
	if res.Draft.ReturnURL != "/work/stores/store_solo/content?pack_id=pack_1" {
		t.Fatalf("return_url = %q", res.Draft.ReturnURL)
	}
	if res.Draft.PlanState != PlanStateDraftSaved || res.Draft.Status != StatusDraft || res.Draft.Plan.ID == "" {
		t.Fatalf("draft state = %+v", res.Draft)
	}
	if res.Draft.CustomerPublish || res.Draft.CustomerEvidenceEligible || res.Draft.CustomerRewardEligible {
		t.Fatalf("merchant draft leaked customer eligibility: %+v", res.Draft)
	}
	assertClean(t, res.Draft)
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.UGCWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("store draft triggered customer effects: %+v", sink)
	}
	scoped := svc.ListByScope("ten_a", "store_solo", "")
	if len(scoped) != 1 || scoped[0].ID != res.Draft.ID {
		t.Fatalf("ListByScope = %+v", scoped)
	}
	if otherTenant := svc.ListByScope("ten_b", "store_solo", ""); len(otherTenant) != 0 {
		t.Fatalf("cross-tenant ListByScope leaked drafts: %+v", otherTenant)
	}
}

func TestCustomerAndAuthorityBoundariesFailClosed(t *testing.T) {
	t.Run("customer cannot create merchant publish plan", func(t *testing.T) {
		for _, actor := range []Actor{
			{TenantID: "ten_a", BrandID: "brand_a", Role: RoleBrandPublisher, IsCustomer: true},
			{TenantID: "ten_a", BrandID: "brand_a", Role: "customer"},
			{TenantID: "ten_a", BrandID: "brand_a", Role: "visitor"},
			{TenantID: "ten_a", BrandID: "brand_a", Role: "guest"},
		} {
			svc := New(videoFixture(), &EffectSink{})
			res, err := svc.CreateDraft(handoffNow, actor, baseReq())
			if ReasonOf(err) != ReasonCustomerPathForbidden || res.Success || len(svc.List()) != 0 {
				t.Fatalf("actor %+v got err=%v res=%+v", actor, err, res)
			}
		}
	})

	t.Run("merchant quota cannot act as delegate personal payment", func(t *testing.T) {
		svc := New(videoFixture(), &EffectSink{})
		req := baseReq()
		req.ActingAsPersonalPayer = true
		if _, err := svc.CreateDraft(handoffNow, Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleDelegate}, req); ReasonOf(err) != ReasonDelegatePaymentForbidden {
			t.Fatalf("delegate acting as personal payer err = %v", err)
		}
		actorScope := Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleDelegate, PaymentScope: PaymentScopeDelegatePersonal}
		if _, err := svc.CreateDraft(handoffNow, actorScope, baseReq()); ReasonOf(err) != ReasonDelegatePaymentForbidden {
			t.Fatalf("delegate personal payment scope err = %v", err)
		}
		ownerScope := Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleBrandPublisher, PaymentScope: PaymentScopeDelegatePersonal}
		if _, err := svc.CreateDraft(handoffNow, ownerScope, baseReq()); ReasonOf(err) != ReasonDelegatePaymentForbidden {
			t.Fatalf("publisher personal payment scope err = %v", err)
		}
		if len(svc.List()) != 0 {
			t.Fatal("refused payment escalation created a draft")
		}
	})

	t.Run("expired and revoked assets fail closed", func(t *testing.T) {
		svc := New(videoFixture(), &EffectSink{})
		expiredAsset := baseReq()
		expiredAsset.Assets = []Asset{{ID: "a1", Hash: "h1", Kind: "video", ExpiresAt: handoffNow.Add(-time.Minute)}}
		if _, err := svc.CreateDraft(handoffNow, publisher(), expiredAsset); ReasonOf(err) != ReasonAssetExpired {
			t.Fatalf("expired asset err = %v", err)
		}
		revokedAsset := baseReq()
		revokedAsset.Assets = []Asset{{ID: "a2", Hash: "h2", Kind: "video", Revoked: true}}
		if _, err := svc.CreateDraft(handoffNow, publisher(), revokedAsset); ReasonOf(err) != ReasonAssetRevoked {
			t.Fatalf("revoked asset err = %v", err)
		}
		if len(svc.List()) != 0 {
			t.Fatal("invalid asset stored a draft")
		}
	})
}

func TestWrongContentTypeAndNeedsVideoNeverTranscodeOrCharge(t *testing.T) {
	client := videoFixture()
	svc := New(client, &EffectSink{})

	// Image masquerading as video -> fail closed with ReasonWrongContentType.
	spoofed := baseReq()
	spoofed.Assets = []Asset{{ID: "img_1", Hash: "sha_img", Kind: "image", DeclaredContentType: "video"}}
	res, err := svc.CreateDraft(handoffNow, publisher(), spoofed)
	if ReasonOf(err) != ReasonWrongContentType || res.Success || len(svc.List()) != 0 {
		t.Fatalf("spoofed video got err=%v res=%+v", err, res)
	}
	if client.Puts != 0 || svc.ChargeCount() != 0 || svc.TranscodeCount() != 0 || svc.AICutCount() != 0 {
		t.Fatalf("spoofed content triggered side effects puts=%d charge=%d transcode=%d aicut=%d",
			client.Puts, svc.ChargeCount(), svc.TranscodeCount(), svc.AICutCount())
	}

	// Honest image on video-only client -> StatusNeedsVideo, not success, cannot confirm.
	imgOnly := baseReq()
	imgOnly.Assets = []Asset{{ID: "img_2", Hash: "sha_img2", Kind: "image"}}
	nv, err := svc.CreateDraft(handoffNow, publisher(), imgOnly)
	if err != nil || nv.Success || nv.Draft.Status != StatusNeedsVideo || nv.Draft.Plan.ID != "" {
		t.Fatalf("needs_video = %+v err=%v", nv, err)
	}
	if _, err := svc.Confirm(handoffNow, publisher(), nv.Draft.ID); ReasonOf(err) != ReasonNeedsVideo {
		t.Fatalf("confirm needs_video err = %v", err)
	}
	if client.Puts != 0 || svc.ChargeCount() != 0 || svc.TranscodeCount() != 0 || svc.AICutCount() != 0 {
		t.Fatal("needs_video transcoded or charged")
	}
}

func TestIdempotencyKeyRecoversDraftEvenAfterApproval(t *testing.T) {
	client := videoFixture()
	svc := New(client, &EffectSink{})

	req := baseReq()
	req.IdempotencyKey = "idem-merchant-click-001"
	first, err := svc.CreateDraft(handoffNow, publisher(), req)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := svc.Confirm(handoffNow, publisher(), first.Draft.ID)
	if err != nil || !approved.Draft.Approved || approved.Draft.PlanState != PlanStatePendingApproval {
		t.Fatalf("confirm = %+v err=%v", approved, err)
	}

	// Retry / re-login with the same idempotency key must recover the same draft
	// via Existing() without dialing PutDraft again or overwriting approval.
	existing, ok := svc.Existing(req)
	if !ok || existing.ID != first.Draft.ID || existing.Plan.ID != first.Draft.Plan.ID || !existing.Approved {
		t.Fatalf("Existing after approval = %+v ok=%v", existing, ok)
	}
	replayReq := req
	replayReq.OfferText = "重试改写文案不应覆盖"
	second, err := svc.CreateDraft(handoffNow, publisher(), replayReq)
	if err != nil || second.Draft.ID != first.Draft.ID || second.Draft.Activity.OfferText != "第二杯半价" || !second.Draft.Approved {
		t.Fatalf("idempotent replay mutated draft: %+v err=%v", second, err)
	}
	if client.Puts != 1 || len(svc.List()) != 1 {
		t.Fatalf("puts=%d drafts=%d", client.Puts, len(svc.List()))
	}
}

func TestLayeredPlanStatesMetricsNullAndNoOfficialAuthority(t *testing.T) {
	client := videoFixture()
	sink := &EffectSink{}
	svc := New(client, sink)

	created, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	if created.Draft.PlanState != PlanStateDraftSaved {
		t.Fatalf("initial plan_state = %q, want %q", created.Draft.PlanState, PlanStateDraftSaved)
	}
	// Verify unknown metrics marshal as JSON null, never 0.
	assertMetricsNullJSON(t, created.Draft)

	// Confirm moves to pending_approval.
	confirmed, err := svc.Confirm(handoffNow, publisher(), created.Draft.ID)
	if err != nil || confirmed.Draft.PlanState != PlanStatePendingApproval {
		t.Fatalf("confirm plan_state = %q err=%v", confirmed.Draft.PlanState, err)
	}

	// Receipt: scheduled (with unknown metrics -> still JSON null).
	sched, err := svc.ApplyReceipt(handoffNow, publisher(), created.Draft.ID, Receipt{
		PlanID:      created.Draft.Plan.ID,
		TargetState: PlanStateScheduled,
		Sequence:    1,
	})
	if err != nil || !sched.Applied || sched.Draft.PlanState != PlanStateScheduled {
		t.Fatalf("scheduled receipt = %+v err=%v", sched, err)
	}
	assertMetricsNullJSON(t, sched.Draft)

	// Receipt: attempt to claim published WITHOUT official authority -> fail closed,
	// stays honestly on scheduled, never fabricates published.
	denied, err := svc.ApplyReceipt(handoffNow, publisher(), created.Draft.ID, Receipt{
		PlanID:           created.Draft.Plan.ID,
		TargetState:      PlanStatePublished,
		Sequence:         2,
		OfficialVerified: false,
	})
	if ReasonOf(err) != ReasonNoOfficialAuthority || denied.Applied || !denied.Ignored || denied.Draft.PlanState != PlanStateScheduled {
		t.Fatalf("unverified publish must fail closed: outcome=%+v err=%v", denied, err)
	}

	// Receipt: unknown state is honestly recorded.
	unk, err := svc.ApplyReceipt(handoffNow, publisher(), created.Draft.ID, Receipt{
		PlanID:      created.Draft.Plan.ID,
		TargetState: PlanStateUnknown,
		Sequence:    3,
	})
	if err != nil || !unk.Applied || unk.Draft.PlanState != PlanStateUnknown {
		t.Fatalf("unknown state = %+v err=%v", unk, err)
	}

	// Receipt: verified official authority -> published, still null metrics when
	// unreported, and ZERO customer publish/reward eligibility.
	authActor := publisher()
	authActor.OfficialPublishGranted = true
	pub, err := svc.ApplyReceipt(handoffNow, authActor, created.Draft.ID, Receipt{
		PlanID:           created.Draft.Plan.ID,
		TargetState:      PlanStatePublished,
		Sequence:         4,
		OfficialVerified: true,
	})
	if err != nil || !pub.Applied || pub.Draft.PlanState != PlanStatePublished || !pub.Draft.OfficialAuthorityGranted {
		t.Fatalf("verified publish = %+v err=%v", pub, err)
	}
	if pub.CustomerEvidenceEligible || pub.CustomerRewardEligible || pub.Draft.CustomerPublish || pub.Draft.CustomerEvidenceEligible || pub.Draft.CustomerRewardEligible {
		t.Fatalf("merchant publish became customer evidence/reward: %+v", pub)
	}
	assertMetricsNullJSON(t, pub.Draft)
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.UGCWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("merchant receipts triggered customer effects: %+v", sink)
	}
}

func TestLateAndStaleReceiptsNeverOverwriteNewerAdoptedVersion(t *testing.T) {
	client := videoFixture()
	svc := New(client, &EffectSink{})

	v1, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(handoffNow, publisher(), v1.Draft.ID); err != nil {
		t.Fatal(err)
	}

	reqV2 := baseReq()
	reqV2.ActivityVersion = 4
	reqV2.Assets = []Asset{{ID: "asset_v2", Hash: "hash_v2", Kind: "video"}}
	v2, err := svc.CreateDraft(handoffNow, publisher(), reqV2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(handoffNow, publisher(), v2.Draft.ID); err != nil {
		t.Fatal(err)
	}

	authActor := publisher()
	authActor.OfficialPublishGranted = true

	// 1. Late receipt targeting superseded v1 is ignored.
	lateV1, err := svc.ApplyReceipt(handoffNow, authActor, v1.Draft.ID, Receipt{
		PlanID:           v1.Draft.Plan.ID,
		TargetState:      PlanStatePublished,
		Sequence:         10,
		OfficialVerified: true,
	})
	if err != nil || lateV1.Applied || !lateV1.Ignored || lateV1.Reason != ReasonStaleReceipt {
		t.Fatalf("late v1 receipt not ignored: %+v err=%v", lateV1, err)
	}

	// 2. Stale activity version receipt targeting v2 (version 3 < 4) is ignored.
	staleVer, err := svc.ApplyReceipt(handoffNow, authActor, v2.Draft.ID, Receipt{
		PlanID:          v2.Draft.Plan.ID,
		TargetState:     PlanStateScheduled,
		ActivityVersion: 3,
		Sequence:        1,
	})
	if err != nil || staleVer.Applied || !staleVer.Ignored || staleVer.Reason != ReasonStaleReceipt {
		t.Fatalf("stale version receipt not ignored: %+v err=%v", staleVer, err)
	}

	// 3. Apply valid receipt seq=5 on v2 -> published.
	okPub, err := svc.ApplyReceipt(handoffNow, authActor, v2.Draft.ID, Receipt{
		PlanID:           v2.Draft.Plan.ID,
		TargetState:      PlanStatePublished,
		ActivityVersion:  4,
		Sequence:         5,
		OfficialVerified: true,
	})
	if err != nil || !okPub.Applied || okPub.Draft.PlanState != PlanStatePublished {
		t.Fatalf("v2 publish = %+v err=%v", okPub, err)
	}

	// 4. Lower sequence (seq=4) and unsequenced (seq=0) late receipts must be ignored.
	for _, r := range []Receipt{
		{PlanID: v2.Draft.Plan.ID, TargetState: PlanStateScheduled, ActivityVersion: 4, Sequence: 4},
		{PlanID: v2.Draft.Plan.ID, TargetState: PlanStateDraftSaved, ActivityVersion: 4, Sequence: 0},
		{PlanID: v2.Draft.Plan.ID, TargetState: PlanStateScheduled, ActivityVersion: 4, Sequence: 6},
	} {
		out, err := svc.ApplyReceipt(handoffNow, authActor, v2.Draft.ID, r)
		if err != nil || out.Applied || !out.Ignored || out.Reason != ReasonStaleReceipt {
			t.Fatalf("late/regressing receipt %+v not ignored: %+v err=%v", r, out, err)
		}
		if out.Draft.PlanState != PlanStatePublished {
			t.Fatalf("v2 plan_state regressed to %q", out.Draft.PlanState)
		}
	}
}

func TestStripMerchantFieldsAndAssertCustomerPayloadClean(t *testing.T) {
	dirty := map[string]any{
		"action_id":          "act_1",
		"title":              "招牌双人餐",
		"price_note":         "88元起",
		"account_pool":       []string{"dy_brand_1"},
		"merchant_account":   "brand_acc_secret",
		"schedule_token":     "sched_tok_123",
		"merchant_quota":     500,
		"matrix_plan_id":     "pln_secret",
		"payer_id":           "pay_1",
		"license_ref":        "lic_1",
		"grant_refs":         []string{"grant_1"},
		"adopted_by":         "usr_owner",
		"tenant_id":          "ten_a",
		"eco_top_nav":        true,
		"nested_candidate": map[string]any{
			"version_id":         "ver_1",
			"schedule_credential": "cred_xyz",
			"brand_quota":        99,
		},
		"media": []any{
			map[string]any{
				"media_id":     "m_1",
				"access_token": "tok_leak",
			},
		},
	}

	clean, stripped := StripMerchantFields(dirty)
	if len(stripped) < 10 {
		t.Fatalf("expected merchant keys stripped, got %v", stripped)
	}
	if clean["title"] != "招牌双人餐" || clean["price_note"] != "88元起" {
		t.Fatalf("customer-safe fields damaged: %+v", clean)
	}
	// Original map must not be mutated.
	if dirty["merchant_account"] != "brand_acc_secret" {
		t.Fatal("StripMerchantFields mutated input map")
	}

	cleanRaw, err := json.Marshal(clean)
	if err != nil {
		t.Fatal(err)
	}
	if err := AssertCustomerPayloadClean(cleanRaw); err != nil {
		t.Fatalf("clean payload failed assertion: %v", err)
	}

	dirtyRaw, _ := json.Marshal(dirty)
	err = AssertCustomerPayloadClean(dirtyRaw)
	if err == nil || !strings.Contains(err.Error(), "merchant_account") {
		t.Fatalf("dirty payload must be flagged, got err=%v", err)
	}
}

func TestConcurrentIdempotentCreateAndReceiptRace(t *testing.T) {
	svc := New(videoFixture(), &EffectSink{})
	req := baseReq()
	req.IdempotencyKey = "race-idem-1"

	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.CreateDraft(handoffNow, publisher(), req)
			if err == nil {
				ids <- res.Draft.ID
			}
			_ = svc.ListByScope("ten_a", "store_a", "act_a")
		}()
	}
	wg.Wait()
	close(ids)
	var firstID string
	for id := range ids {
		if firstID == "" {
			firstID = id
		} else if id != firstID {
			t.Fatalf("concurrent idempotent creates diverged: %q vs %q", firstID, id)
		}
	}
	if firstID == "" {
		t.Fatal("no draft created")
	}
	if _, err := svc.Confirm(handoffNow, publisher(), firstID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		seq := int64(i)
		go func() {
			defer wg.Done()
			_, _ = svc.ApplyReceipt(handoffNow, publisher(), firstID, Receipt{
				TargetState: PlanStateScheduled,
				Sequence:    seq,
			})
		}()
	}
	wg.Wait()
}

func assertMetricsNullJSON(t *testing.T, doc Draft) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	metrics, ok := parsed["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics missing in JSON: %s", string(raw))
	}
	for _, k := range []string{"reach", "likes", "leads"} {
		val, exists := metrics[k]
		if !exists {
			t.Fatalf("metrics.%s missing: %s", k, string(raw))
		}
		if val != nil {
			t.Fatalf("unknown metrics.%s must be JSON null, got %#v (%s)", k, val, string(raw))
		}
	}
}
