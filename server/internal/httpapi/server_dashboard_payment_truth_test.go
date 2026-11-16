package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// 原订单写路径 → 真实统计 HTTP → 独立逐行复算 → 页面共用期望。
// 身份和权威付款/退款输入均为本地工程替身，不代表正式支付或正常浏览器通过。
func TestDashboardPaymentFactsMatchIndependentLedgerAndPageFixture(t *testing.T) {
	f := newOpsE2EFixture(t)
	const occurredAt = "2026-10-14T09:00:00Z"
	const receivedAt = "2026-10-15T10:00:00Z"
	orders := map[string]string{}
	for _, row := range []struct {
		kind   string
		amount int64
	}{{billing.KindSubscription, 1200}, {billing.KindHardware, 2000}} {
		order, _, err := f.st.CreateBillingOrder(store.NewBillingOrder{
			TenantID: f.tenA, Kind: row.kind, Action: billing.ActionNew,
			PlanCode: billing.PlanMonthly, PriceRef: "engineering-price", PriceSnapshot: `{}`,
			AmountMinor: row.amount, Currency: "CNY", IdempotencyKey: "analytics-" + row.kind,
			ActorPrincipal: "usr_owner_a", ActorRole: "org_owner",
		}, "2026-10-14T08:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		orders[row.kind] = order.ID
		payment := billing.AuthorityEventInput{
			Source: "engineering-authority", EventID: "payment-" + row.kind, Type: billing.EventPaymentPaid,
			OccurredAt: occurredAt, Payload: billing.EventPayload{PaymentRef: "engineering-paid-" + row.kind},
		}
		if _, _, err := f.st.ApplyBillingOrderEvent(f.tenA, order.ID, payment, receivedAt); err != nil {
			t.Fatal(err)
		}
		// 同一支付事件重放，以及同单新回调标识，都不增加订单数。
		if _, replay, err := f.st.ApplyBillingOrderEvent(f.tenA, order.ID, payment, receivedAt); err != nil || !replay {
			t.Fatalf("payment replay=%v err=%v", replay, err)
		}
		if row.kind == billing.KindSubscription {
			payment.EventID += "-again"
			if _, _, err := f.st.ApplyBillingOrderEvent(f.tenA, order.ID, payment, receivedAt); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.st.ApplyBillingOrderEvent(f.tenA, order.ID, billing.AuthorityEventInput{
				Source: "engineering-authority", EventID: "activation-failed", Type: billing.EventActivationFailed,
				OccurredAt: receivedAt, Payload: billing.EventPayload{FailReason: "工程开通失败"},
			}, receivedAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, row := range []struct {
		id, kind string
		amount   int64
	}{{"subscription", billing.KindSubscription, 300}, {"hardware-one", billing.KindHardware, 200}, {"hardware-two", billing.KindHardware, 300}} {
		refund := billing.RefundInput{
			RefundRef: "engineering-refund-" + row.id, AmountMinor: row.amount, Currency: "CNY",
			Reason: "工程部分退款", InvoiceStatus: billing.InvoiceNone, Auditor: "engineering-auditor",
			Source: "engineering-authority", EventID: "refund-" + row.id, OccurredAt: "2026-10-14T11:00:00Z",
		}
		if _, _, err := f.st.ApplyBillingRefund(f.tenA, orders[row.kind], refund, receivedAt); err != nil {
			t.Fatal(err)
		}
		if _, replay, err := f.st.ApplyBillingRefund(f.tenA, orders[row.kind], refund, receivedAt); err != nil || !replay {
			t.Fatalf("refund replay=%v err=%v", replay, err)
		}
	}
	status, body := f.owner(t, "GET", "/api/v1/dashboard/operations?window_start=2026-10-14T00:00:00Z&window_end=2026-10-14T23:59:59Z", "")
	if status != http.StatusOK {
		t.Fatalf("operations=%d %v", status, body)
	}
	recon := body["cost_reconciliation"].(map[string]any)
	raw, err := os.ReadFile(filepath.Join("testdata", "analytics-payment-facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for key, want := range expected {
		if !reflect.DeepEqual(recon[key], want) {
			t.Errorf("API/page fixture %s=%v, want %v", key, recon[key], want)
		}
	}

	// 独立逐事件读取后手工去重，不调用统计查询或组装器。
	rows, err := f.st.DB.Query(`SELECT e.order_id,o.kind,o.amount_minor FROM billing_order_events e
		JOIN billing_orders o ON o.id=e.order_id AND o.tenant_id=e.tenant_id
		WHERE e.tenant_id=? AND e.event_type='payment_paid' AND date(e.occurred_at)='2026-10-14'`, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	seen, sums, counts := map[string]bool{}, map[string]int64{}, map[string]int64{}
	for rows.Next() {
		var id, kind string
		var amount int64
		if err := rows.Scan(&id, &kind, &amount); err != nil {
			t.Fatal(err)
		}
		if !seen[id] {
			seen[id] = true
			sums[kind] += amount
			counts[kind]++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, kind := range []string{billing.KindSubscription, billing.KindHardware} {
		if recon[kind+"_paid_minor"] != float64(sums[kind]) || recon[kind+"_paid_orders"] != float64(counts[kind]) {
			t.Errorf("independent payment ledger %s=%d/%d, API=%v", kind, sums[kind], counts[kind], recon)
		}
	}
	refundRows, err := f.st.DB.Query(`SELECT o.kind,r.amount_minor FROM billing_refunds r
		JOIN billing_orders o ON o.id=r.order_id AND o.tenant_id=r.tenant_id
		WHERE r.tenant_id=? AND date(r.occurred_at)='2026-10-14'`, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	sums, counts = map[string]int64{}, map[string]int64{}
	for refundRows.Next() {
		var kind string
		var amount int64
		if err := refundRows.Scan(&kind, &amount); err != nil {
			t.Fatal(err)
		}
		sums[kind] += amount
		counts[kind]++
	}
	if err := refundRows.Err(); err != nil {
		t.Fatal(err)
	}
	refundRows.Close()
	for _, kind := range []string{billing.KindSubscription, billing.KindHardware} {
		if recon[kind+"_refund_minor"] != float64(sums[kind]) || recon[kind+"_refunds"] != float64(counts[kind]) {
			t.Errorf("independent refund ledger %s=%d/%d, API=%v", kind, sums[kind], counts[kind], recon)
		}
	}

	// 再收到一条无法归窗的原账单事实时，HTTP 不得把先前可确认的部分和
	// 继续作为完整金额输出；页面仍能读到已确认落窗数和无法归窗数。
	for _, kind := range []string{billing.KindSubscription, billing.KindHardware} {
		order, _, err := f.st.CreateBillingOrder(store.NewBillingOrder{
			TenantID: f.tenA, Kind: kind, Action: billing.ActionNew, PlanCode: billing.PlanMonthly,
			PriceRef: "engineering-price", PriceSnapshot: `{}`, AmountMinor: 800, Currency: "CNY",
			IdempotencyKey: "undated-" + kind, ActorPrincipal: "usr_owner_a", ActorRole: "org_owner",
		}, receivedAt)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.st.ApplyBillingOrderEvent(f.tenA, order.ID, billing.AuthorityEventInput{
			Source: "engineering-authority", EventID: "undated-payment-" + kind, Type: billing.EventPaymentPaid,
			OccurredAt: "bad-time", Payload: billing.EventPayload{PaymentRef: "undated-" + kind},
		}, receivedAt); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.st.ApplyBillingRefund(f.tenA, order.ID, billing.RefundInput{
			RefundRef: "undated-" + kind, AmountMinor: 100, Currency: "CNY", Reason: "历史工程事实",
			InvoiceStatus: billing.InvoiceNone, Auditor: "engineering-auditor", Source: "engineering-authority",
			EventID: "undated-refund-" + kind, OccurredAt: "bad-time",
		}, receivedAt); err != nil {
			t.Fatal(err)
		}
	}
	status, body = f.owner(t, "GET", "/api/v1/dashboard/operations?window_start=2026-10-14T00:00:00Z&window_end=2026-10-14T23:59:59Z", "")
	if status != http.StatusOK {
		t.Fatalf("operations after undated facts=%d %v", status, body)
	}
	recon = body["cost_reconciliation"].(map[string]any)
	for _, kind := range []string{billing.KindSubscription, billing.KindHardware} {
		if recon[kind+"_paid_minor"] != nil || recon[kind+"_refund_minor"] != nil || recon[kind+"_available"] != false {
			t.Errorf("unknown dates must suppress complete amount totals: %v", recon)
		}
		if recon[kind+"_paid_time_unknown"] != float64(1) || recon[kind+"_refund_time_unknown"] != float64(1) ||
			recon[kind+"_paid_orders"] != expected[kind+"_paid_orders"] || recon[kind+"_refunds"] != expected[kind+"_refunds"] {
			t.Errorf("known in-window and unplaceable facts must remain distinct: %v", recon)
		}
	}
}
