package deals

import (
	"testing"
	"time"
)

// 会让这些测试失败的生产改动：把非三平台渠道当成合法平台、把内网/http/.invalid
// 或跨平台商品页当成可跳转目标、把过期/未核实的价格当成可信价格展示、
// 或把一次点击/一次适配器错误当成平台成交。

func TestValidPlatforms(t *testing.T) {
	for _, p := range []string{"meituan", "dianping", "douyin"} {
		if !ValidPlatform(p) {
			t.Fatalf("%q must be a valid platform", p)
		}
	}
	for _, p := range []string{"", "eleme", "meituan ", "MEITUAN", "dianping.com"} {
		if ValidPlatform(p) {
			t.Fatalf("%q must NOT be a valid platform", p)
		}
	}
}

func TestProductURLValid(t *testing.T) {
	cases := []struct {
		platform string
		url      string
		want     bool
	}{
		{"meituan", "https://www.meituan.com/deal/123", true},
		{"meituan", "https://m.meituan.com/deal/9", true},
		{"dianping", "https://www.dianping.com/shop/abc", true},
		{"douyin", "https://www.douyin.com/video/x", true},
		// 跨平台商品页 = 错挂渠道，拒绝（正确商品跳转）。
		{"meituan", "https://www.douyin.com/deal/1", false},
		{"dianping", "https://www.meituan.com/deal/1", false},
		// 非官方地址：http、内网、.invalid、缺 host。
		{"meituan", "http://www.meituan.com/deal/1", false},
		{"meituan", "https://127.0.0.1/deal/1", false},
		{"meituan", "https://shop.example.invalid/deal/1", false},
		{"meituan", "https://10.0.0.3/deal/1", false},
		{"meituan", "", false},
		// 伪装域名字后缀不算（eviltmeituan.com）。
		{"meituan", "https://eviltmeituan.com/deal/1", false},
	}
	for _, c := range cases {
		if got := ProductURLValid(c.platform, c.url); got != c.want {
			t.Fatalf("ProductURLValid(%q,%q) = %v, want %v", c.platform, c.url, got, c.want)
		}
	}
}

func TestPriceState(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	rfc := func(t time.Time) string { return t.Format(time.RFC3339) }
	verified := rfc(now.Add(-1 * time.Hour))
	stale := rfc(now.Add(-72 * time.Hour)) // 恰好 72h 前 = 已过期（边界含）
	older := rfc(now.Add(-73 * time.Hour))
	freshButPaused := rfc(now.Add(-1 * time.Hour))

	cases := []struct {
		name                       string
		verifiedAt, validFrom, val string
		status                     string
		want                       PriceDisplay
	}{
		{"active+fresh", verified, "", "", StatusActive, PriceShow},
		{"no verification", "", "", "", StatusActive, PricePlatform},
		{"exactly 72h old is expired", stale, "", "", StatusActive, PricePlatform},
		{"73h old", older, "", "", StatusActive, PricePlatform},
		{"paused offer never shows price", freshButPaused, "", "", StatusPaused, PricePlatform},
		{"retired never shows price", freshButPaused, "", "", StatusRetired, PricePlatform},
		{"valid window not open yet", verified, rfc(now.Add(time.Hour)), "", StatusActive, PricePlatform},
		{"valid window closed", verified, "", rfc(now.Add(-time.Second)), StatusActive, PricePlatform},
		{"empty status treated as not showable", verified, "", "", "", PricePlatform},
	}
	for _, c := range cases {
		if got := PriceState(c.verifiedAt, c.validFrom, c.val, c.status, now); got != c.want {
			t.Fatalf("%s: PriceState = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAdapterInterfaceShape(t *testing.T) {
	// 接口形状守护：适配器实现不依赖网络；Result 的 State 只有三值；未知
	// 永远不会在领域层被升级为 redeemed。
	if RedeemRedeemed != "redeemed" || RedeemFailed != "failed" || RedeemUnknown != "unknown" {
		t.Fatalf("redeem state constants drifted: %q/%q/%q", RedeemRedeemed, RedeemFailed, RedeemUnknown)
	}
	var a Adapter = noopAdapter{}
	res, err := a.Check(CheckRequest{Platform: PlatformMeituan, CouponRef: "x"})
	if err != nil || res.Valid || res.Detail != "noop" {
		t.Fatalf("noop check = %+v/%v", res, err)
	}
	res2, err2 := a.Redeem(RedeemRequest{Platform: PlatformDianping, CouponRef: "y"})
	if err2 != nil || res2.State != RedeemUnknown || res2.Detail != "noop" {
		t.Fatalf("noop redeem = %+v/%v", res2, err2)
	}
}

type noopAdapter struct{}

func (noopAdapter) Check(CheckRequest) (CheckResult, error) {
	return CheckResult{Valid: false, Detail: "noop"}, nil
}

func (noopAdapter) Redeem(RedeemRequest) (RedeemResult, error) {
	return RedeemResult{State: RedeemUnknown, Detail: "noop"}, nil
}

func TestPlatformOfURLHostReportsOwnPlatformOnly(t *testing.T) {
	// 平台域名归属辅助：URL 归属的平台必须等于套餐渠道（正确商品跳转）。
	if got := PlatformOfURL("https://www.dianping.com/x"); got != PlatformDianping {
		t.Fatalf("PlatformOfURL = %q", got)
	}
	if got := PlatformOfURL("https://www.example.com/x"); got != "" {
		t.Fatalf("foreign url platform = %q, want empty", got)
	}
}
