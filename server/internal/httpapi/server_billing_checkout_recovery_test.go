package httpapi

import (
	"fmt"
	"testing"
)

func TestCashierRequiresCompleteTarget(t *testing.T) {
	f := billingFixture(t, false)
	f.s.Cfg.CashierAppID = "cashier"
	f.s.Cfg.CashierTargetID = ""
	if got := f.s.cashierCheckout(); got["available"] != false {
		t.Fatalf("incomplete checkout was offered: %v", got)
	}
	f.s.Cfg.CashierTargetID = "not-a-target"
	if got := f.s.cashierCheckout(); got["available"] != false {
		t.Fatalf("invalid target was offered: %v", got)
	}
}

func TestOriginalUnpaidOrderOffersAuthorizedCheckoutWithoutReordering(t *testing.T) {
	f := billingFixture(t, true)
	seedCatalog(t, f)
	f.s.Cfg.CashierAppID = "cashier"
	f.s.Cfg.CashierTargetID = "ti-cashier-web"
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"checkout-recovery"}`)
	if status != 201 {
		t.Fatal(status, out)
	}
	id := out["order"].(map[string]any)["id"].(string)
	path := "/api/v1/billing/orders/" + id
	for i := 0; i < 2; i++ {
		status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
		checkout, _ := out["checkout"].(map[string]any)
		if status != 200 || checkout["available"] != true || out["order"].(map[string]any)["id"] != id {
			t.Fatalf("original unpaid order has no continuation: %d %v", status, out)
		}
	}
	rows, err := f.s.St.ListBillingOrders(f.tenA, "subscription")
	mustNoErr(t, err)
	if len(rows) != 1 || rows[0].PaymentRef != "" {
		t.Fatalf("checkout lookup changed the order or payment: %+v", rows)
	}
	status, _ = f.do2(t, "GET", path, "sess-owner-b", f.tenB, "")
	if status != 404 {
		t.Fatalf("foreign order lookup: %d", status)
	}
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"checkout-recovery-paid","type":"payment_paid","payload":{"payment_ref":"checkout-pay"}}`, f.tenA, id))
	if status != 200 && status != 201 {
		t.Fatal(status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != false {
		t.Fatalf("already paid order offered a new checkout: %d %v", status, out)
	}
}
