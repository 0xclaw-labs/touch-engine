// Package deals holds the HUI-4590 团购套餐 domain policy: which platforms
// exist, what counts as a correct product link, when a price may be shown,
// and the shape of the external coupon adapter boundary.
//
// 纪律：
//   - 点击只是点击。本包没有任何"成交"概念——下单与成交在平台完成，本地
//     不可知就不展示，不推算。
//   - 价格只有核对过（72h 内）才可信；过期/未核实一律「到平台查看」。
//   - 外部券事实只来自授权适配器；未知就是未知，永不升级为已核销。
package deals

import (
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
)

// Platform values (三渠道固定；美团与点评分渠道保留，不同合)。
const (
	PlatformMeituan  = "meituan"
	PlatformDianping = "dianping"
	PlatformDouyin   = "douyin"
)

// Offer lifecycle statuses (mirror of the store_deal_offers CHECK).
const (
	StatusActive  = "active"
	StatusPaused  = "paused"
	StatusRetired = "retired"
)

// PriceDisplay: show = 核对过的可信价格；platform = 「到平台查看」。
type PriceDisplay = string

const (
	PriceShow     PriceDisplay = "show"
	PricePlatform PriceDisplay = "platform"
)

// Redeem result states returned by the adapter boundary.
const (
	RedeemRedeemed = "redeemed"
	RedeemFailed   = "failed"
	RedeemUnknown  = "unknown"
)

// PriceVerifiedTTL is how long a merchant-verified price stays trustworthy.
// 72h is a deliberate, documented constant: group-buy prices on these
// platforms move fast, so an older verification honestly answers「到平台查看」.
const PriceVerifiedTTL = 72 * time.Hour

// platformDomains maps each platform to its official product-page domain.
// Subdomains are allowed; suffix impostors are not (eviltmeituan.com 拒).
var platformDomains = map[string]string{
	PlatformMeituan:  "meituan.com",
	PlatformDianping: "dianping.com",
	PlatformDouyin:   "douyin.com",
}

// ValidPlatform reports whether p is one of the three supported channels.
func ValidPlatform(p string) bool {
	_, ok := platformDomains[p]
	return ok
}

// PlatformOfURL returns the platform whose official domain the URL host
// belongs to, or "" for foreign hosts.
func PlatformOfURL(raw string) string {
	host := hostOf(raw)
	for platform, domain := range platformDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return platform
		}
	}
	return ""
}

// ProductURLValid enforces the 正确商品跳转 gate: the URL must be a public
// https address (extrajump.OfficialAddress) AND belong to the offer's own
// platform domain. Anything else is a mis-channelled link and refused.
func ProductURLValid(platform, raw string) bool {
	domain, ok := platformDomains[platform]
	if !ok {
		return false
	}
	if !extrajump.OfficialAddress(raw) {
		return false
	}
	host := hostOf(raw)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func hostOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// PriceState decides whether the stored price may be shown to a customer.
// Only an active offer inside its validity window with a price verified
// within PriceVerifiedTTL earns "show"; everything else honestly answers
// 「到平台查看」(platform). Unparseable timestamps fail closed.
func PriceState(verifiedAt, validFrom, validUntil, status string, now time.Time) PriceDisplay {
	if status != StatusActive {
		return PricePlatform
	}
	if !WindowOpen(validFrom, validUntil, now) {
		return PricePlatform
	}
	raw := strings.TrimSpace(verifiedAt)
	if raw == "" {
		return PricePlatform
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return PricePlatform
		}
	}
	if t.After(now) {
		return PricePlatform
	}
	if now.Sub(t) >= PriceVerifiedTTL {
		return PricePlatform
	}
	return PriceShow
}

// WindowOpen reports whether the validity window covers now. Empty bounds are
// open-ended; unparseable bounds fail closed.
func WindowOpen(validFrom, validUntil string, now time.Time) bool {
	if raw := strings.TrimSpace(validFrom); raw != "" {
		if t, err := parseTime(raw); err != nil || t.After(now) {
			return false
		}
	}
	if raw := strings.TrimSpace(validUntil); raw != "" {
		if t, err := parseTime(raw); err != nil || !t.After(now) {
			return false
		}
	}
	return true
}

func parseTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, raw)
}

// CheckRequest asks the adapter whether an external coupon is currently valid.
// CouponRef is sensitive: it never enters logs or public surfaces.
type CheckRequest struct {
	Platform  string
	OfferRef  string
	CouponRef string
}

// CheckResult is what the adapter saw. Known=false (transport trouble) is
// reported through the error return instead; a nil-error answer is a real
// platform answer.
type CheckResult struct {
	Valid  bool
	Detail string
}

// RedeemRequest asks the adapter to redeem one external coupon on the platform.
// It is also the lookup key: a timed-out redeem MUST be requeried with the
// SAME request (同一请求先查，不重发核销).
type RedeemRequest struct {
	Platform  string
	OfferRef  string
	CouponRef string
}

// RedeemResult carries the platform's answer. State is one of the Redeem*
// constants; unknown stays unknown until a confirming lookup (永不升级为已核销).
type RedeemResult struct {
	State  string
	Detail string
}

// RefreshResult is one official refresh of an offer's platform facts
// (价格/可售性来自官方 API 时才可信；普通购买链接给不了这些事实).
type RefreshResult struct {
	PriceMinor int64
	Available  bool
	Detail     string
}

// Adapter is the authorization-backed boundary toward the external platform.
// Production wiring stays nil until an official authorization exists; a nil
// adapter means honestly not connected (不假核销). Implementations must be
// safe for sequential use; they own all network concerns. An error return is
// a transport-level failure: the caller answers unknown and never invents a
// platform fact from it.
type Adapter interface {
	Check(CheckRequest) (CheckResult, error)
	Redeem(RedeemRequest) (RedeemResult, error)
	// Lookup re-queries a previously submitted redeem with the SAME request
	// (超时/未知后的回查路径，绝不重发核销).
	Lookup(RedeemRequest) (RedeemResult, error)
	// RefreshOffer re-pulls an offer's official price/availability when the
	// deployment holds that official capability. Without it the refresh
	// surface answers an explicit capability status, never a fabricated fact.
	RefreshOffer(offerRef string) (RefreshResult, error)
}
