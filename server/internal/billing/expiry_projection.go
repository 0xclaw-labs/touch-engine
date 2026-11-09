// expiry_projection.go: 订阅事实投影与六种到期对齐(HUI-4591 F18 / 接线 HUI-1671)。
//
// 规则(裁定书):
//   - 订阅到期只限新增收费能力;有效旧券继续查验/核销(拦截策略在 1671 的
//     rewardops.AdjudicateExpiry,本包只供给按租户的事实)。
//   - 到期判定换按租户事实:PaidUntil 是真源(进程级 Cfg 只是本包之外的回退,
//     由调用侧在无投影行时使用,旧行为逐字节保留)。
//   - 维护保留期明确:PaidUntil + GraceDays,只读保留,不删除任何事实。

package billing

import (
	"time"
)

// Subscription status values (four-state activation F16 + honest unknowns).
const (
	SubStatusUnconfirmed = "unconfirmed"
	SubStatusProcessing  = "processing"
	SubStatusOpening     = "opening"
	SubStatusActive      = "active"
	SubStatusFailed      = "failed"
	SubStatusExpired     = "expired"
)

// SubscriptionFact is the per-tenant subscription projection read model.
// It mirrors one billing_subscriptions row; the authoritative subscription
// lives in the 派诺生态统一账单 (subscription_ref is its reference).
type SubscriptionFact struct {
	TenantID              string `json:"tenant_id"`
	SubscriptionRef       string `json:"subscription_ref"`
	PlanCode              string `json:"plan_code"`
	PricingVersion        string `json:"pricing_version"`
	Status                string `json:"status"`
	PaidUntil             string `json:"paid_until"`
	GraceDays             int    `json:"grace_days"`
	StoreLimit            int    `json:"store_limit"`
	EntitlementProjection string `json:"entitlement_projection"`
	LastOrderID           string `json:"last_order_id"`
}

// EffectiveStatus derives the honest status at now: an active subscription
// whose paid_until has lapsed reads expired. An active subscription without a
// service period does NOT invent an expiry (no fact, no refusal). Non-active
// states pass through verbatim — processing never masquerades as expired.
func (f SubscriptionFact) EffectiveStatus(now string) string {
	if f.Status != SubStatusActive || f.PaidUntil == "" {
		return f.Status
	}
	until, err := time.Parse(time.RFC3339, f.PaidUntil)
	if err != nil {
		// 无法解析的服务期保守按未知(不发明到期,也不放行成有效)——返回
		// 原状态,由 1671 侧的保守到期语义兜底。
		return f.Status
	}
	nowT, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return f.Status
	}
	if nowT.After(until) {
		return SubStatusExpired
	}
	return f.Status
}

// RetainUntil returns the maintenance-retention end (paid_until + grace days).
// ” paid_until means no retention fact.
func (f SubscriptionFact) RetainUntil() (string, error) {
	if f.PaidUntil == "" {
		return "", nil
	}
	until, err := time.Parse(time.RFC3339, f.PaidUntil)
	if err != nil {
		return "", ErrRenewalBadInput
	}
	return FormatRFC3339(until.AddDate(0, 0, f.GraceDays)), nil
}

// GrantDeadlines builds the 1671 six-deadline context from tenant facts.
// SubscriptionUntil is the REAL paid_until (the adjudicator judges lapse
// naturally); channel stays unchecked — no live channel source exists here and
// an unknown state must not invent a refusal.
func (f SubscriptionFact) GrantDeadlines(campaignUntil, participationUntil string) (dl GrantDeadlinesView) {
	return GrantDeadlinesView{
		SubscriptionUntil:  f.PaidUntil,
		CampaignUntil:      campaignUntil,
		ParticipationUntil: participationUntil,
	}
}

// GrantDeadlinesView mirrors the fields rewardops.GrantDeadlines needs, kept
// as a plain struct so this package does not import the rewardops domain
// (the HTTP caller assigns it field-by-field onto rewardops.GrantDeadlines).
type GrantDeadlinesView struct {
	SubscriptionUntil  string
	CampaignUntil      string
	ParticipationUntil string
	// ChannelChecked 恒 false:本仓无实时渠道源,未知状态不得发明拒绝
	// (调用侧把 ChannelChecked/ChannelOpen 留给 1671 缺省)。
	ChannelChecked bool
}

// ParseRFC3339 parses a deadline; ” is a valid no-deadline.
func ParseRFC3339(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, v)
}

// FormatRFC3339 renders a moment as RFC3339 UTC.
func FormatRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func rfc3339OrZero(v string) time.Time {
	t, err := ParseRFC3339(v)
	if err != nil {
		return time.Time{}
	}
	return t
}
