package httpapi

import (
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"testing"
	"time"
)

func TestSubscriptionQuoteUsesOriginalTierAndCreatesNoOrder(t *testing.T) {
	f := billingFixture(t, false)
	_, err := f.s.St.UpsertBillingCatalogEntry(billing.CatalogEntry{ScopeTenant: f.tenA, PlanCode: billing.PlanMonthly, PricingVersion: "tier-v1", PriceRef: "tier-original", Currency: "CNY", PriceMinor: 500, ServiceDays: 30, IncludedStoreCount: 1, ExtraStorePriceMinor: 100, TierJSON: `[{"upto":2,"price_minor":30}]`}, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-quote", "sess-staff-a", f.tenA, `{"action":"expand","plan_code":"store.plan.month","additional_stores":3}`)
	if status != 403 {
		t.Fatalf("staff quote = %d %+v", status, out)
	}
	status, out = f.do2(t, "POST", "/api/v1/billing/subscription-quote", "sess-owner-a", f.tenA, `{"action":"expand","plan_code":"store.plan.month","additional_stores":3}`)
	if status != 200 || out["amount_minor"] != float64(160) || out["price_ref"] != "tier-original" {
		t.Fatalf("quote = %d %+v", status, out)
	}
	orders, err := f.s.St.ListBillingOrders(f.tenA, "")
	if err != nil || len(orders) != 0 {
		t.Fatalf("quote wrote orders: %+v %v", orders, err)
	}
}
