package billing

// billing_test.go: HUI-4591 价格目录解析/续费扩店/到期投影领域测试(TDD 红先行)。
//
// 纪律(裁定书):价格目录配置不写死(代码零金额常量,全部来自 CatalogEntry);
// 订单引用价格快照;续期起算两分支;扩店差价按加店单价/阶梯;降档生效日=
// 当前服务期届满;到期投影按租户事实(paid_until),与 1671 六种到期对齐。

import (
	"strings"
	"testing"
	"time"
)

// ---- 价格目录解析 ----

func entry(plan, version string, scope string, from, to string) CatalogEntry {
	return CatalogEntry{
		ScopeTenant: scope, PlanCode: plan, PricingVersion: version,
		PriceRef: "price_" + plan + "_" + version, Currency: "CNY", PriceMinor: 990000,
		ServiceDays: 365, IncludedStoreCount: 3, ExtraStorePriceMinor: 60000,
		TierJSON: "[]", GraceDays: 30, EffectiveFrom: from, EffectiveTo: to,
	}
}

func TestResolveCatalogPrefersTenantScopedAndHonorsWindow(t *testing.T) {
	global := entry(PlanMonthly, "v2026", "", "2026-01-01T00:00:00Z", "")
	tenant := entry(PlanMonthly, "v2026t", "ten_a", "2026-01-01T00:00:00Z", "")
	got, err := ResolveCatalog([]CatalogEntry{global, tenant}, "ten_a", PlanMonthly, "2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ScopeTenant != "ten_a" {
		t.Fatalf("tenant-scoped entry must win: %+v", got)
	}
	// 租户无专属行 → 全局默认
	got, err = ResolveCatalog([]CatalogEntry{global, tenant}, "ten_b", PlanMonthly, "2026-06-01T00:00:00Z")
	if err != nil || got.ScopeTenant != "" {
		t.Fatalf("global fallback: %v %+v", err, got)
	}
}

func TestResolveCatalogRejectsOutsideWindowAndUnknownPlan(t *testing.T) {
	windowed := entry(PlanMonthly, "v1", "", "2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z")
	if _, err := ResolveCatalog([]CatalogEntry{windowed}, "ten_a", PlanMonthly, "2026-06-01T00:00:00Z"); err != ErrCatalogNotEffective {
		t.Fatalf("outside window = %v", err)
	}
	if _, err := ResolveCatalog([]CatalogEntry{windowed}, "ten_a", "store.plan.week", "2026-01-15T00:00:00Z"); err != ErrCatalogNotFound {
		t.Fatalf("unknown plan = %v", err)
	}
	// 多个生效版本:取 effective_from 最新(价格修订生效纪律)
	v1 := entry(PlanYearly, "v1", "", "2026-01-01T00:00:00Z", "")
	v2 := entry(PlanYearly, "v2", "", "2026-03-01T00:00:00Z", "")
	got, err := ResolveCatalog([]CatalogEntry{v1, v2}, "ten_a", PlanYearly, "2026-06-01T00:00:00Z")
	if err != nil || got.PricingVersion != "v2" {
		t.Fatalf("latest effective version: %v %+v", err, got)
	}
}

func TestCatalogZeroPriceConstantsInCode(t *testing.T) {
	// 目录纪律的机械证明:任何条目字段为 0/空时解析照常(价格是数据不是代码)。
	e := CatalogEntry{ScopeTenant: "", PlanCode: PlanMonthly, PricingVersion: "v0",
		PriceRef: "price_free", Currency: "CNY", PriceMinor: 0, ServiceDays: 30}
	got, err := ResolveCatalog([]CatalogEntry{e}, "ten_a", PlanMonthly, "2026-06-01T00:00:00Z")
	if err != nil || got.PriceMinor != 0 || got.PriceRef != "price_free" {
		t.Fatalf("zero-price entry must resolve as data: %v %+v", err, got)
	}
}

// ---- 阶梯加店价 ----

func TestTierStepsParseAndFailClosed(t *testing.T) {
	e := entry(PlanYearly, "v1", "", "", "")
	e.TierJSON = `[{"upto":10,"price_minor":50000},{"upto":20,"price_minor":40000}]`
	steps, err := TierSteps(e)
	if err != nil || len(steps) != 2 || steps[1].PriceMinor != 40000 {
		t.Fatalf("tier steps: %v %+v", err, steps)
	}
	e.TierJSON = `not-json`
	if _, err := TierSteps(e); err == nil {
		t.Fatal("malformed tier json must fail closed")
	}
	// 空数组 = 无阶梯(统一加店单价)
	e.TierJSON = `[]`
	if steps, err := TierSteps(e); err != nil || len(steps) != 0 {
		t.Fatalf("empty tiers: %v %+v", err, steps)
	}
}

// ---- 扩店差价 ----

func TestExpansionQuoteUsesTierThenFlatPrice(t *testing.T) {
	e := entry(PlanYearly, "v1", "", "", "") // included=3, flat extra=60000
	e.TierJSON = `[{"upto":5,"price_minor":50000},{"upto":8,"price_minor":40000}]`
	// included 3 + 2 → 落在 upto=5 档 → 2×50000
	amount, err := ExpansionQuote(e, 2)
	if err != nil || amount != 100000 {
		t.Fatalf("tier quote: %v %d", err, amount)
	}
	// included 3 + 4 → 加店 1..4 全部落在累计上限 upto=5 档 → 4×50000
	amount, err = ExpansionQuote(e, 4)
	if err != nil || amount != 200000 {
		t.Fatalf("mixed tier quote: %v %d", err, amount)
	}
	// 超出阶梯上限 → 前两档走阶梯,余量回落统一加店单价:
	// 5×50000 + 3×40000 + 2×60000 = 490000
	amount, err = ExpansionQuote(e, 10)
	if err != nil || amount != 490000 {
		t.Fatalf("overflow flat quote: %v %d", err, amount)
	}
	// 无阶梯 → 全按 extra_store_price_minor
	e.TierJSON = "[]"
	amount, err = ExpansionQuote(e, 2)
	if err != nil || amount != 120000 {
		t.Fatalf("flat quote: %v %d", err, amount)
	}
	if _, err := ExpansionQuote(e, 0); err == nil {
		t.Fatal("zero additional stores must refuse")
	}
}

// ---- 续期起算(F17) ----

func TestRenewalStartTwoBranches(t *testing.T) {
	// 未到期续费:从 paid_until 起算(不丢剩余天数)
	got, err := RenewalStart(RenewalContext{
		Now: "2026-06-01T00:00:00Z", PaidUntil: "2026-12-31T00:00:00Z",
		PaymentConfirmedAt: "2026-06-01T00:00:00Z", ServiceDays: 365,
	})
	if err != nil || got != "2026-12-31T00:00:00Z" {
		t.Fatalf("renew before expiry: %v %q", err, got)
	}
	// 已到期续费:从付款确认时刻起算
	got, err = RenewalStart(RenewalContext{
		Now: "2026-06-01T00:00:00Z", PaidUntil: "2026-01-01T00:00:00Z",
		PaymentConfirmedAt: "2026-06-01T00:00:00Z", ServiceDays: 365,
	})
	if err != nil || got != "2026-06-01T00:00:00Z" {
		t.Fatalf("renew after expiry: %v %q", err, got)
	}
	// 无服务期(首购):从付款确认时刻起算
	got, err = RenewalStart(RenewalContext{
		Now: "2026-06-01T00:00:00Z", PaidUntil: "",
		PaymentConfirmedAt: "2026-05-31T12:00:00Z", ServiceDays: 30,
	})
	if err != nil || got != "2026-05-31T12:00:00Z" {
		t.Fatalf("first purchase: %v %q", err, got)
	}
}

// ServicePeriodEnd 从起算时刻加服务期天数(目录 service_days,不写死周期)。
func TestServicePeriodEnd(t *testing.T) {
	got, err := ServicePeriodEnd("2026-01-01T00:00:00Z", 365)
	if err != nil {
		t.Fatalf("period end: %v", err)
	}
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if got != want {
		t.Fatalf("period end = %q want %q", got, want)
	}
	if _, err := ServicePeriodEnd("bogus", 365); err == nil {
		t.Fatal("bad start must fail")
	}
	if _, err := ServicePeriodEnd("2026-01-01T00:00:00Z", 0); err == nil {
		t.Fatal("zero service days must fail (catalog discipline)")
	}
}

// ---- 降档生效日(F17:当前服务期届满) ----

func TestDowngradeEffectiveAt(t *testing.T) {
	got, err := DowngradeEffectiveAt("2026-12-31T00:00:00Z", "2026-06-01T00:00:00Z")
	if err != nil || got != "2026-12-31T00:00:00Z" {
		t.Fatalf("downgrade waits for period end: %v %q", err, got)
	}
	// 无服务期(无承诺可削减)→ 立即生效
	got, err = DowngradeEffectiveAt("", "2026-06-01T00:00:00Z")
	if err != nil || got != "2026-06-01T00:00:00Z" {
		t.Fatalf("downgrade without period: %v %q", err, got)
	}
}

// ---- 到期投影(F18,对齐 1671) ----

func TestSubscriptionFactEffectiveStatus(t *testing.T) {
	f := SubscriptionFact{Status: SubStatusActive, PaidUntil: "2026-01-01T00:00:00Z"}
	if got := f.EffectiveStatus("2026-06-01T00:00:00Z"); got != SubStatusExpired {
		t.Fatalf("lapsed active must read expired, got %s", got)
	}
	if got := f.EffectiveStatus("2025-06-01T00:00:00Z"); got != SubStatusActive {
		t.Fatalf("active within period: %s", got)
	}
	// active 且无服务期:不发明到期(诚实未知)
	f.PaidUntil = ""
	if got := f.EffectiveStatus("2026-06-01T00:00:00Z"); got != SubStatusActive {
		t.Fatalf("active without period stays active: %s", got)
	}
	// 非 active 状态原样返回(processing 不偷跑成 expired)
	f.Status = SubStatusProcessing
	if got := f.EffectiveStatus("2026-06-01T00:00:00Z"); got != SubStatusProcessing {
		t.Fatalf("processing stays processing: %s", got)
	}
}

func TestSubscriptionFactRetainUntil(t *testing.T) {
	f := SubscriptionFact{PaidUntil: "2026-01-01T00:00:00Z", GraceDays: 30}
	got, err := f.RetainUntil()
	if err != nil || got != "2026-01-31T00:00:00Z" {
		t.Fatalf("retain until: %v %q", err, got)
	}
	f.PaidUntil = ""
	if got, err := f.RetainUntil(); err != nil || got != "" {
		t.Fatalf("no period no retain: %v %q", err, got)
	}
}

func TestSubscriptionFactGrantDeadlinesFor1671(t *testing.T) {
	f := SubscriptionFact{Status: SubStatusActive, PaidUntil: "2026-12-31T00:00:00Z"}
	dl := f.GrantDeadlines("2026-11-30T00:00:00Z", "2026-11-15T00:00:00Z")
	if dl.SubscriptionUntil != "2026-12-31T00:00:00Z" || dl.CampaignUntil != "2026-11-30T00:00:00Z" ||
		dl.ParticipationUntil != "2026-11-15T00:00:00Z" {
		t.Fatalf("deadlines: %+v", dl)
	}
	if dl.ChannelChecked {
		t.Fatal("no live channel source: must not invent a channel check")
	}
}

func TestRfc3339Helpers(t *testing.T) {
	if _, err := ParseRFC3339("bogus"); err == nil {
		t.Fatal("bogus time must fail")
	}
	if _, err := ParseRFC3339(""); err != nil {
		t.Fatalf("empty is valid no-deadline: %v", err)
	}
	if !strings.HasPrefix(FormatRFC3339(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), "2026-01-02T03:04:05") {
		t.Fatal("format")
	}
}
