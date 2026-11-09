package rewardops

import (
	"reflect"
	"testing"
)

// HUI-1671 六种到期独立判定: 订阅到期/活动结束/渠道关闭/参与截止/预留 TTL/
// 券本身有效期,各判各的,查询时分别给出原因;已发未过期权益按原承诺可查询、
// 可重签展示码、店员可核销(T16)。

const expiryNow = "2026-11-10T08:00:00Z"

func d(sub, camp, part, res, ben string, chanChecked bool, chanOpen bool) GrantDeadlines {
	return GrantDeadlines{
		SubscriptionUntil:  sub,
		CampaignUntil:      camp,
		ParticipationUntil: part,
		ReservationUntil:   res,
		BenefitUntil:       ben,
		ChannelChecked:     chanChecked,
		ChannelOpen:        chanOpen,
	}
}

func TestAdjudicateExpiryPromiseBlockedPerCause(t *testing.T) {
	// 每种到期独立拦截新承诺(promise),原因各归各。
	cases := []struct {
		name    string
		in      GrantDeadlines
		wantAny []string
	}{
		{
			name:    "subscription passed",
			in:      d("2026-11-01T00:00:00Z", "2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-12-31T00:00:00Z", false, true),
			wantAny: []string{"subscription_expired"},
		},
		{
			name:    "campaign ended",
			in:      d("2026-12-31T00:00:00Z", "2026-11-01T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-12-31T00:00:00Z", false, true),
			wantAny: []string{"campaign_ended"},
		},
		{
			name:    "channel closed",
			in:      d("2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-12-31T00:00:00Z", true, false),
			wantAny: []string{"channel_closed"},
		},
		{
			name:    "participation window closed",
			in:      d("2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "2026-11-01T00:00:00Z", "", "2026-12-31T00:00:00Z", false, true),
			wantAny: []string{"participation_closed"},
		},
		{
			name:    "benefit validity passed",
			in:      d("2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-11-01T00:00:00Z", false, true),
			wantAny: []string{"benefit_expired"},
		},
		{
			name:    "channel unknown does not block",
			in:      d("2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-12-31T00:00:00Z", false, false),
			wantAny: nil,
		},
		{
			name:    "reservation TTL elapsed alone does not block promise",
			in:      d("", "", "", "2026-11-01T00:00:00Z", "", false, false),
			wantAny: nil,
		},
		{
			name:    "all open allows promise",
			in:      d("2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-12-31T00:00:00Z", true, true),
			wantAny: nil,
		},
	}
	for _, tc := range cases {
		v := AdjudicateExpiry(ExpiryOpPromise, tc.in, expiryNow)
		if v.Allowed != (len(tc.wantAny) == 0) {
			t.Fatalf("%s: allowed=%v reasons=%v", tc.name, v.Allowed, v.Reasons)
		}
		for _, want := range tc.wantAny {
			if !verdictHas(v, want) {
				t.Fatalf("%s: missing reason %s in %v", tc.name, want, v.Reasons)
			}
		}
		if len(tc.wantAny) == 0 && len(v.Reasons) != 0 {
			t.Fatalf("%s: unexpected reasons %v", tc.name, v.Reasons)
		}
	}
}

func TestAdjudicateExpiryMultipleCausesAllReported(t *testing.T) {
	// 订阅+活动同时过期: 两个原因都给出,不得复用单一 expired 字段抹平。
	in := d("2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "2026-12-31T00:00:00Z", "", "2026-12-31T00:00:00Z", true, false)
	v := AdjudicateExpiry(ExpiryOpPromise, in, expiryNow)
	if v.Allowed {
		t.Fatalf("must refuse: %v", v.Reasons)
	}
	for _, want := range []string{"subscription_expired", "campaign_ended", "channel_closed"} {
		if !verdictHas(v, want) {
			t.Fatalf("missing %s in %v", want, v.Reasons)
		}
	}
}

func TestAdjudicateExpiryIssuedBenefitSurvivesEverythingButOwnExpiry(t *testing.T) {
	// T16: 订阅到期+活动结束+参与截止+预留TTL过,券仍有效 → 核销/重签/查询全通过。
	base := d("2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "2026-11-03T00:00:00Z", "2026-11-04T00:00:00Z", "2026-12-31T00:00:00Z", true, false)
	for _, op := range []ExpiryOp{ExpiryOpRedeem, ExpiryOpResign, ExpiryOpQuery} {
		v := AdjudicateExpiry(op, base, expiryNow)
		if !v.Allowed {
			t.Fatalf("op %s blocked by %v", op, v.Reasons)
		}
	}
	// 券本身过期: redeem/resign 拒,query 永不拦(已发事实仍可查)。
	base.BenefitUntil = "2026-11-01T00:00:00Z"
	for _, op := range []ExpiryOp{ExpiryOpRedeem, ExpiryOpResign} {
		v := AdjudicateExpiry(op, base, expiryNow)
		if v.Allowed || !verdictHas(v, "benefit_expired") {
			t.Fatalf("op %s must refuse benefit_expired: %v %v", op, v.Allowed, v.Reasons)
		}
	}
	if v := AdjudicateExpiry(ExpiryOpQuery, base, expiryNow); !v.Allowed {
		t.Fatalf("query must never be blocked: %v", v.Reasons)
	}
}

func TestAdjudicateExpiryQueryReportsCausesForDisplay(t *testing.T) {
	// 查询路径永不拦,但把命中的到期原因逐项带回(展示用)。
	in := d("2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "2026-11-03T00:00:00Z", "", "2026-11-04T00:00:00Z", false, false)
	v := AdjudicateExpiry(ExpiryOpQuery, in, expiryNow)
	if !v.Allowed {
		t.Fatalf("query must always be allowed")
	}
	want := []string{"subscription_expired", "campaign_ended", "participation_closed", "benefit_expired"}
	if !reflect.DeepEqual(v.Reasons, want) {
		t.Fatalf("reasons = %v want %v", v.Reasons, want)
	}
}

func TestAdjudicateExpiryEmptyMeansNoDeadline(t *testing.T) {
	// '' = 无期限(不判);全空 + 渠道未勾选 = 一切通过。
	in := d("", "", "", "", "", false, false)
	for _, op := range []ExpiryOp{ExpiryOpPromise, ExpiryOpRedeem, ExpiryOpResign, ExpiryOpQuery} {
		if v := AdjudicateExpiry(op, in, expiryNow); !v.Allowed || len(v.Reasons) != 0 {
			t.Fatalf("op %s: empty deadlines must allow: %v", op, v.Reasons)
		}
	}
}

func TestAdjudicateExpiryBadTimestampRefuses(t *testing.T) {
	// 无法解析的期限按"到期"保守处理,不静默放行。
	in := d("not-a-time", "", "", "", "", false, false)
	v := AdjudicateExpiry(ExpiryOpPromise, in, expiryNow)
	if v.Allowed || !verdictHas(v, "subscription_expired") {
		t.Fatalf("unparseable deadline must refuse: %v %v", v.Allowed, v.Reasons)
	}
	if v := AdjudicateExpiry(ExpiryOpPromise, d("", "", "", "", "", false, false), "also-bad"); v.Allowed {
		t.Fatalf("unparseable now must refuse")
	}
}

func verdictHas(v ExpiryVerdict, reason string) bool {
	for _, r := range v.Reasons {
		if r == reason {
			return true
		}
	}
	return false
}
