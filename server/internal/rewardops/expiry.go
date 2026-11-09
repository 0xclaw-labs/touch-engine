package rewardops

import "time"

// HUI-1671 六种到期独立判定(可测试的领域函数)。
//
// 订阅到期、活动结束、渠道关闭、参与截止、预留 TTL、券本身有效期各判各的,
// 永不复用单一 expired 字段裁决所有场景。判定结果是逐项原因列表: 查询路径
// 永不被拦,但把命中的原因全部带回供展示;新承诺(promise)被任一相关期限拦;
// 已发未过期权益按原承诺存活——订阅到期、活动结束、渠道关闭、参与截止都
// 不拦核销与展示码重签(T16),只有券本身有效期到期才拦。
//
// 期限值为 RFC3339 字符串;''=无期限(不判)。无法解析的期限按已到期保守
// 处理,不静默放行。

// ExpiryReason is one machine-readable cause among the six fixed deadlines.
const (
	ReasonSubscriptionExpired   = "subscription_expired"
	ReasonCampaignEnded         = "campaign_ended"
	ReasonChannelClosed         = "channel_closed"
	ReasonParticipationClosed   = "participation_closed"
	ReasonReservationTTLElapsed = "reservation_ttl_elapsed"
	ReasonBenefitExpired        = "benefit_expired"
)

// ExpiryOp is the operation the deadlines are judged for.
type ExpiryOp string

const (
	// ExpiryOpPromise covers NEW commitments: reserve and first-time confirm.
	ExpiryOpPromise ExpiryOp = "promise"
	// ExpiryOpQuery is reading an existing fact; never blocked.
	ExpiryOpQuery ExpiryOp = "query"
	// ExpiryOpResign re-mints a short-lived staff_verify display code for an
	// issued benefit the visitor owns.
	ExpiryOpResign ExpiryOp = "resign"
	// ExpiryOpRedeem is staff redemption of an issued benefit.
	ExpiryOpRedeem ExpiryOp = "redeem"
)

// GrantDeadlines carries the six independent deadlines. Until fields are
// RFC3339 (or ” for no deadline). ChannelOpen is only consulted when
// ChannelChecked is true: a domain without a live channel source does not
// invent a refusal.
type GrantDeadlines struct {
	SubscriptionUntil  string
	CampaignUntil      string
	ParticipationUntil string
	ReservationUntil   string
	BenefitUntil       string
	ChannelChecked     bool
	ChannelOpen        bool
}

// ExpiryVerdict answers whether op may proceed and, independently, lists ALL
// causes that have lapsed — so a query can display per-cause truth while a
// promise refusal names every reason it was refused.
type ExpiryVerdict struct {
	Allowed bool
	Reasons []string
}

// AdjudicateExpiry judges each of the six deadlines independently at now and
// applies the operation's policy:
//
//	promise: blocked by subscription_expired / campaign_ended /
//	         channel_closed / participation_closed / benefit_expired
//	         (a lapsed reservation TTL frees stock; it never blocks).
//	query:   never blocked; reasons ride along for display.
//	resign / redeem: blocked only by benefit_expired — the original promise
//	         survives subscription, campaign, channel, participation and
//	         reservation deadlines.
func AdjudicateExpiry(op ExpiryOp, dl GrantDeadlines, now string) ExpiryVerdict {
	nowT, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return ExpiryVerdict{Reasons: []string{ReasonMissingField}}
	}
	lapsed := func(until string, reason string) bool {
		if until == "" {
			return false
		}
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return true // unparseable = conservatively lapsed
		}
		return nowT.After(t)
	}
	var reasons []string
	add := func(cond bool, reason string) {
		if cond {
			reasons = append(reasons, reason)
		}
	}
	add(lapsed(dl.SubscriptionUntil, ReasonSubscriptionExpired), ReasonSubscriptionExpired)
	add(lapsed(dl.CampaignUntil, ReasonCampaignEnded), ReasonCampaignEnded)
	add(dl.ChannelChecked && !dl.ChannelOpen, ReasonChannelClosed)
	add(lapsed(dl.ParticipationUntil, ReasonParticipationClosed), ReasonParticipationClosed)
	add(lapsed(dl.ReservationUntil, ReasonReservationTTLElapsed), ReasonReservationTTLElapsed)
	add(lapsed(dl.BenefitUntil, ReasonBenefitExpired), ReasonBenefitExpired)

	switch op {
	case ExpiryOpQuery:
		return ExpiryVerdict{Allowed: true, Reasons: reasons}
	case ExpiryOpRedeem, ExpiryOpResign:
		for _, r := range reasons {
			if r == ReasonBenefitExpired {
				return ExpiryVerdict{Allowed: false, Reasons: reasons}
			}
		}
		return ExpiryVerdict{Allowed: true, Reasons: reasons}
	default: // ExpiryOpPromise and anything unknown: strictest policy
		if len(reasons) > 0 {
			// reservation TTL is stock bookkeeping, not a promise blocker
			filtered := reasons[:0:0]
			for _, r := range reasons {
				if r != ReasonReservationTTLElapsed {
					filtered = append(filtered, r)
				}
			}
			if len(filtered) > 0 {
				return ExpiryVerdict{Allowed: false, Reasons: filtered}
			}
		}
		return ExpiryVerdict{Allowed: true, Reasons: nil}
	}
}
