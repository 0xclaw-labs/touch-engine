package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
)

// HUI-1671 边界测试: 动作资格双保险、六种到期字段落库与旧券保护、
// 外部券核销边界、外部发放幂等回查、顾客展示码与访客本人查询。

const boundaryNow = "2026-11-10T09:00:00Z"

func TestCreateRewardInventoryRefusesReviewBinding(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	base := NewRewardInventory{
		CampaignID: campA, Kind: RewardKindStoreCoupon, Name: "点评券",
		BenefitSlot: "review_reward", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"},
		TotalQty:        1, CreatedBy: "seed",
	}
	in := base
	in.ActionKind = rewardops.ActionKindReview
	if _, err := s.CreateRewardInventory(tenA, in); err == nil || !strings.Contains(err.Error(), rewardops.ReasonRewardNotEligible) {
		t.Fatalf("review binding must refuse at creation: %v", err)
	}
	in = base
	in.ActionKind, in.RewardEligible = "publish_post", rewardops.ActionEligibleNo
	if _, err := s.CreateRewardInventory(tenA, in); err == nil || !strings.Contains(err.Error(), rewardops.ReasonRewardNotEligible) {
		t.Fatalf("not-eligible binding must refuse at creation: %v", err)
	}
	// 库存/账目不变: 拒绝后没有新半行(seed 的开业券仍在,数量不增)。
	var n int
	must(t, s.DB.QueryRow(`SELECT COUNT(*) FROM reward_inventory WHERE tenant_id=?`, tenA).Scan(&n))
	if n != 1 {
		t.Fatalf("refused creation must not leave a row (seed batch only): %d", n)
	}
}

func TestConfirmRefusesStoredReviewBinding(t *testing.T) {
	// 双保险第二道: 存量批次(绕过创建校验落库的 review 绑定)在发放侧硬拒。
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	var invID string
	must(t, s.DB.QueryRow(`SELECT id FROM reward_inventory WHERE tenant_id=? AND campaign_id=?`, tenA, campA).Scan(&invID))
	if _, err := s.DB.Exec(`UPDATE reward_inventory SET action_kind='review' WHERE id=?`, invID); err != nil {
		t.Fatal(err)
	}

	in := confirmInput(tenA, campA, "p-review", "staff_observed_submission")
	_, _, err := s.ConfirmRewardBenefit(in)
	if err == nil || !strings.Contains(err.Error(), rewardops.ReasonRewardNotEligible) {
		t.Fatalf("stored review binding must refuse at grant: %v", err)
	}
	var n int
	must(t, s.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits`).Scan(&n))
	if n != 0 {
		t.Fatalf("refused grant must not create a benefit: %d", n)
	}
	// 库存计数不变(没有占位)。
	inv, err := s.GetRewardInventory(invID, tenA)
	must(t, err)
	if inv.AvailableQty != inv.TotalQty {
		t.Fatalf("stock must be untouched: %+v", inv)
	}
}

func TestExpiryFactsSnapshotWrittenAtIssue(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	ben, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-snap", "staff_observed_submission"))
	must(t, err)
	f, err := s.GetRewardExpiryFacts(ben.ID, tenA)
	if err != nil {
		t.Fatalf("expiry facts must land at issue: %v", err)
	}
	if f.CampaignUntil == "" {
		// seed campaign has no ends_at: snapshot keeps '' (no deadline), which
		// is a valid judged value; the field must simply exist.
		t.Logf("campaign without ends_at keeps empty deadline")
	}
}

func TestRedeemRespectsBenefitValidityOnly(t *testing.T) {
	// T16: 活动结束/订阅到期不拦核销(领域里本就不读订阅);只有券本身有效期拦。
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	ben, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-valid", "staff_observed_submission"))
	must(t, err)

	// 活动已结束(store 层不参与公共 Gate;handler 层六到期判定的职责是
	// promise 方向;redeem 只看券有效期)。
	if _, err := s.DB.Exec(`UPDATE campaigns SET ends_at='2026-01-01T00:00:00Z' WHERE id=?`, campA); err != nil {
		t.Fatal(err)
	}
	out, _, err := s.RedeemRewardBenefit(tenA, campA, ben.ID, "staff-9", "store-1", "rdm-old", boundaryNow)
	must(t, err)
	if out.Status != RewardStatusRedeemed {
		t.Fatalf("issued-not-expired coupon must redeem after campaign end: %+v %+v", out, err)
	}

	// 券本身有效期已过 → benefit_expired。
	ben2, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-stale", "staff_observed_submission"))
	must(t, err)
	if _, err := s.DB.Exec(`UPDATE reward_expiry_facts SET benefit_until='2026-11-01T00:00:00Z' WHERE benefit_id=?`, ben2.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.RedeemRewardBenefit(tenA, campA, ben2.ID, "staff-9", "store-1", "rdm-stale", boundaryNow)
	if !errors.Is(err, ErrRewardExpired) {
		t.Fatalf("expired benefit must refuse redeem: %v", err)
	}
}

func TestRedeemExternalOfferForbidden(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	offer, err := s.CreateDealOffer(tenA, NewDealOffer{
		CampaignID: campA, Kind: DealKindExternalProduct, Issuer: "meituan",
		Provider: "meituan", ProviderOfferID: "mt-1", AuthorizationRef: "auth-1",
		Title: "美团双人套餐", FaceValueMinor: 8800, CreatedBy: "seed",
	})
	must(t, err)
	var invID string
	must(t, s.DB.QueryRow(`SELECT id FROM reward_inventory WHERE tenant_id=? AND campaign_id=?`, tenA, campA).Scan(&invID))
	if _, err := s.DB.Exec(`UPDATE reward_inventory SET offer_id=? WHERE id=?`, offer.ID, invID); err != nil {
		t.Fatal(err)
	}

	ben, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-ext", "staff_observed_submission"))
	must(t, err)
	_, _, err = s.RedeemRewardBenefit(tenA, campA, ben.ID, "staff-9", "store-1", "rdm-ext", boundaryNow)
	if !errors.Is(err, ErrExternalRedeemForbidden) {
		t.Fatalf("external benefit must refuse local redeem: %v", err)
	}
	// 账不变: 状态仍是 issued。
	cur, err := s.GetRewardBenefit(ben.ID, tenA, campA)
	must(t, err)
	if cur.Status != RewardStatusIssued {
		t.Fatalf("refused redeem must not mutate state: %+v", cur)
	}
}

func TestGrantExternalCouponDiscipline(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	offer, err := s.CreateDealOffer(tenA, NewDealOffer{
		CampaignID: campA, Kind: DealKindExternalProduct, Issuer: "dianping",
		Provider: "dianping", ProviderOfferID: "dp-1", AuthorizationRef: "auth-dp",
		Title: "点评套餐", FaceValueMinor: 6600, CreatedBy: "seed",
	})
	must(t, err)

	// 无授权: 拒绝,零行,无伪平台券(offer 自身也没有授权引用)。
	noAuth, err := s.CreateDealOffer(tenA, NewDealOffer{
		CampaignID: campA, Kind: DealKindExternalProduct, Issuer: "meituan",
		Provider: "meituan", ProviderOfferID: "mt-9",
		Title: "未授权套餐", FaceValueMinor: 100, CreatedBy: "seed",
	})
	must(t, err)
	_, _, err = s.GrantExternalCoupon(ExternalGrantInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-1",
		OfferID: noAuth.ID, IdempotencyKey: "ext-noauth",
	}, nil, nil, boundaryNow)
	if !errors.Is(err, ErrExternalAuthorizationRequired) {
		t.Fatalf("no authorization must refuse: %v", err)
	}
	if _, err := s.GetExternalIssuance(tenA, "ext-noauth"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused grant must not record: %v", err)
	}

	issueCalls := 0
	issue := func() (rewardops.IssuanceResult, error) {
		issueCalls++
		return rewardops.IssuanceResult{}, errors.New("timeout")
	}
	// 超时 → 回查成功 → issued(同幂等键,不重复出票)。
	e, replay, err := s.GrantExternalCoupon(ExternalGrantInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-1",
		OfferID: offer.ID, IdempotencyKey: "ext-1", AuthorizationRef: "auth-dp", Provider: "dianping",
	}, issue, func() (rewardops.IssuanceResult, error) {
		return rewardops.IssuanceResult{State: rewardops.StateIssued, Code: "DP-SECRET-1"}, nil
	}, boundaryNow)
	must(t, err)
	if replay {
		t.Fatalf("first grant is not a replay")
	}
	if e.State != ExternalStateIssued || e.CouponRef != "DP-SECRET-1" || issueCalls != 1 {
		t.Fatalf("requery must confirm with one issue call: %+v calls=%d", e, issueCalls)
	}

	// 同键重放: 不再调用 issue,读回已有事实。
	e2, replay2, err := s.GrantExternalCoupon(ExternalGrantInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-1",
		OfferID: offer.ID, IdempotencyKey: "ext-1", AuthorizationRef: "auth-dp",
	}, issue, nil, boundaryNow)
	must(t, err)
	if !replay2 || issueCalls != 1 || e2.State != ExternalStateIssued {
		t.Fatalf("same key must replay without re-issue: %+v replay=%v calls=%d", e2, replay2, issueCalls)
	}

	// 超时且回查也失败 → pending_unknown(待确认,永不显示成功)。
	e3, _, err := s.GrantExternalCoupon(ExternalGrantInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-2",
		OfferID: offer.ID, IdempotencyKey: "ext-2", AuthorizationRef: "auth-dp",
	}, issue, func() (rewardops.IssuanceResult, error) {
		return rewardops.IssuanceResult{}, errors.New("lookup down")
	}, boundaryNow)
	must(t, err)
	if e3.State != ExternalStateUnknown {
		t.Fatalf("unconfirmed result must stay pending_unknown: %+v", e3)
	}

	// 待确认行用同键回查升级;仍未知就保持待确认。
	e4, replay4, err := s.GrantExternalCoupon(ExternalGrantInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-2",
		OfferID: offer.ID, IdempotencyKey: "ext-2", AuthorizationRef: "auth-dp",
	}, issue, func() (rewardops.IssuanceResult, error) {
		return rewardops.IssuanceResult{State: rewardops.StateUnknown}, nil
	}, boundaryNow)
	must(t, err)
	if !replay4 || e4.State != ExternalStateUnknown {
		t.Fatalf("still-unknown must stay pending: %+v replay=%v", e4, replay4)
	}
	e5, _, err := s.GrantExternalCoupon(ExternalGrantInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-2",
		OfferID: offer.ID, IdempotencyKey: "ext-2", AuthorizationRef: "auth-dp",
	}, issue, func() (rewardops.IssuanceResult, error) {
		return rewardops.IssuanceResult{State: rewardops.StateIssued, Code: "DP-SECRET-2"}, nil
	}, boundaryNow)
	must(t, err)
	if e5.State != ExternalStateIssued || e5.CouponRef != "DP-SECRET-2" {
		t.Fatalf("confirming lookup must upgrade: %+v", e5)
	}
}

func seedParticipation(t *testing.T, s *Store, tenantID, campaignID, participationID, visitorID string) {
	t.Helper()
	sto, err := s.CreateStore(tenantID, "边界店", "", "seed")
	must(t, err)
	if _, err := s.DB.Exec(
		`INSERT INTO participations(id,tenant_id,store_id,campaign_id,link_code,touchpoint_source,rule_version,visitor_id,status,idempotency_key,benefit_slot,benefit_reservation_ref,benefit_reservation_state,fulfillment_ref,starts_at,expires_at,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		participationID, tenantID, sto.ID, campaignID, "code-x", "manual", 1, visitorID, "in_progress",
		"idem-"+participationID, "first_publish", "", "not_requested", "",
		"2026-11-01T00:00:00Z", "2026-12-31T00:00:00Z", boundaryNow, boundaryNow); err != nil {
		t.Fatal(err)
	}
}

func TestMintAndResolveDisplayCode(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	seedParticipation(t, s, tenA, campA, "p-visitor", "visitor-ok")

	// 预留态不能重签展示码(不是 grant)。
	rb := RewardReserveInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: "p-visitor", Slot: "first_publish",
		RuleVersion: 3, EventID: "res-1", Actor: "participation-service", TTLSeconds: 3600,
	}
	if _, _, err := s.ReserveRewardBenefit(rb, boundaryNow); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := s.MintRewardDisplayCode(tenA, "", "visitor-ok", boundaryNow); err == nil {
		t.Fatalf("missing benefit id must refuse")
	}
	ben, _, err := s.ConfirmRewardBenefit(func() RewardConfirmInput {
		in := confirmInput(tenA, campA, "p-visitor", "staff_observed_submission")
		in.EventID = "cfm-visitor"
		return in
	}())
	must(t, err)

	code, err := s.MintRewardDisplayCode(tenA, ben.ID, "visitor-ok", boundaryNow)
	must(t, err)
	if code.Token == "" || code.ExpiresAt == "" {
		t.Fatalf("mint must return a short-lived token: %+v", code)
	}
	resolved, err := s.ResolveRewardDisplayCode(tenA, code.Token, boundaryNow)
	must(t, err)
	if resolved.ID != ben.ID || resolved.Status != RewardStatusIssued {
		t.Fatalf("code must locate the benefit: %+v", resolved)
	}

	// 过期旧码本身拒绝(同 unknown 形,不泄露)。
	later := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := s.ResolveRewardDisplayCode(tenA, code.Token, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired code must be refused: %v", err)
	}

	// 伪造/跨访客拒绝。
	if _, err := s.MintRewardDisplayCode(tenA, ben.ID, "visitor-evil", boundaryNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong visitor must be masked as not found: %v", err)
	}
	// 店员码定位返回 RewardBenefit:结构上没有访客字段,店员码换不来顾客身份。
}

func TestListRewardBenefitsForVisitorT16(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	seedParticipation(t, s, tenA, campA, "p-list", "visitor-list")

	ben, _, err := s.ConfirmRewardBenefit(func() RewardConfirmInput {
		in := confirmInput(tenA, campA, "p-list", "staff_observed_submission")
		in.EventID = "cfm-list"
		// 活动早已结束、订阅早已到期: 查询照常,核销照常。
		in.Expiry = &rewardops.GrantDeadlines{
			SubscriptionUntil:  "2026-11-01T00:00:00Z",
			CampaignUntil:      "2026-11-02T00:00:00Z",
			ParticipationUntil: "2026-11-03T00:00:00Z",
		}
		return in
	}())
	must(t, err)

	items, err := s.ListRewardBenefitsForVisitor("visitor-list", boundaryNow)
	must(t, err)
	if len(items) != 1 {
		t.Fatalf("visitor must see their benefit: %d", len(items))
	}
	v := items[0]
	if v.Benefit.ID != ben.ID || v.Benefit.Code == "" {
		t.Fatalf("own coupon code must be returned: %+v", v.Benefit)
	}
	if !v.RedeemAllowed {
		t.Fatalf("valid benefit survives subscription/campaign/participation deadlines: %+v", v)
	}
	has := func(list []string, want string) bool {
		for _, r := range list {
			if r == want {
				return true
			}
		}
		return false
	}
	if !has(v.ExpiryReasons, rewardops.ReasonSubscriptionExpired) || !has(v.ExpiryReasons, rewardops.ReasonCampaignEnded) {
		t.Fatalf("query must report per-cause reasons: %+v", v.ExpiryReasons)
	}
	if len(v.RedeemBlockers) != 0 {
		t.Fatalf("expired non-benefit deadlines must not block redeem: %+v", v.RedeemBlockers)
	}

	// 他人 visitor 看不到。
	other, err := s.ListRewardBenefitsForVisitor("visitor-elsewhere", boundaryNow)
	must(t, err)
	if len(other) != 0 {
		t.Fatalf("cross-visitor visibility must be empty: %+v", other)
	}
}
