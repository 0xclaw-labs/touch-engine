package httpapi

import (
	"fmt"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// HUI-4664 authority events 的 v1.1 消费动作契约测试：
// payment_paid 按 checkout_ref 路由映射行 + 金额裁决（不符大声失败）；
// activation_opening FYI 不拒绝；order_registered 停发即 409。

func touchRefSeed(t *testing.T, f *fixture, key, ck string) store.BillingOrderRow {
	t.Helper()
	order := checkoutOrderSeed(t, f, key)
	seeded, err := f.s.St.UpsertBillingCheckoutRef(store.BillingCheckoutRefRow{
		TenantID: f.tenA, OrderID: order.ID, CheckoutRef: ck,
		Status: "pending", AmountMinor: 100, Currency: "CNY",
	}, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed ref map: %v", err)
	}
	_ = seeded
	return order
}

func TestAuthorityPaymentPaidRoutesByCheckoutRefAndAdjudicates(t *testing.T) {
	f := billingFixture(t, true)
	order := touchRefSeed(t, f, "idem-v11-1", "ck_v11_1")
	// 金额一致（producer 承诺 = 冻结承认值 100）→ gating 推进映射 paid + 原单 opening。
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":"ck_v11_1","checkout_ref":"ck_v11_1","source":"pai-billing","event_id":"pai-pay-1",
		  "type":"payment_paid","occurred_at":"2026-06-01T10:00:00Z",
		  "payload":{"order_ref":"NT1","payment_ref":"NT1","amount_minor":100,"currency":"CNY"}}`, f.tenA))
	if status != 201 {
		t.Fatalf("paid gating = %d %+v", status, out)
	}
	refRow, err := f.s.St.GetBillingCheckoutRef(f.tenA, order.ID)
	if err != nil || refRow.Status != "paid" {
		t.Fatalf("映射推进 paid: %+v err=%v", refRow.Status, err)
	}
	// 金额不符 = 资金异常大声失败（409 event_mismatch，不冒充干净重放）。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":"ck_v11_1","checkout_ref":"ck_v11_1","source":"pai-billing","event_id":"pai-pay-bad",
		  "type":"payment_paid","occurred_at":"2026-06-01T10:05:00Z",
		  "payload":{"order_ref":"NT2","payment_ref":"NT2","amount_minor":999,"currency":"CNY"}}`, f.tenA))
	if status != 409 || out["error"] != "event_mismatch" {
		t.Fatalf("金额不符必须 409 event_mismatch: %d %+v", status, out)
	}
	// 原单状态机已 opening（payment_paid 已推进）——后续 paid 变体为迟到事件。
	row, err := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if err != nil || row.Status != "opening" {
		t.Fatalf("原单 opening: %+v err=%v", row.Status, err)
	}
}

func TestAuthorityActivationOpeningFYIWithCheckoutRef(t *testing.T) {
	f := billingFixture(t, true)
	order := touchRefSeed(t, f, "idem-v11-2", "ck_v11_2")
	// 带 checkout_ref 的 opening：FYI 如实 200，不推进原单状态机（仍 processing）。
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":"ck_v11_2","checkout_ref":"ck_v11_2","source":"pai-billing","event_id":"pai-open-1",
		  "type":"activation_opening","occurred_at":"2026-06-01T10:00:00Z","payload":{}}`, f.tenA))
	if status != 200 || out["fyi"] != true {
		t.Fatalf("opening FYI = %d %+v", status, out)
	}
	row, err := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if err != nil || row.Status != "processing" {
		t.Fatalf("FYI 不得推进原单: %+v err=%v", row.Status, err)
	}
	_ = order
}

func TestAuthorityOrderRegisteredRetiredIsLoud(t *testing.T) {
	f := billingFixture(t, true)
	touchRefSeed(t, f, "idem-v11-3", "ck_v11_3")
	// order_registered 自 v1.1 停发：到达即异常（老 producer/伪造），409 大声失败。
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":"ck_v11_3","checkout_ref":"ck_v11_3","source":"pai-billing","event_id":"pai-reg-1",
		  "type":"order_registered","occurred_at":"2026-06-01T10:00:00Z","payload":{}}`, f.tenA))
	if status != 409 || out["error"] != "event_type_retired" {
		t.Fatalf("停发型必须 409 event_type_retired: %d %+v", status, out)
	}
}
