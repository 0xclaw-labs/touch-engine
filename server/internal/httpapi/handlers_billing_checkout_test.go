package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// HUI-4664 原单收银接续：合同面测试。producer 由测试注入 Server；
// 生产默认 nil = checkout_unwired（fail-closed，走查已验）。

// scriptedCheckoutProducer 是合同面测试替身：按脚本回放五态之一。
type scriptedCheckoutProducer struct {
	ready  bool
	result billing.OriginalOrderCheckoutResult
	err    error
	calls  int
}

func (p *scriptedCheckoutProducer) Ready() bool { return p.ready }

func (p *scriptedCheckoutProducer) CreateOrResumeCheckout(ctx context.Context, in billing.OriginalOrderCheckoutRequest) (billing.OriginalOrderCheckoutResult, error) {
	p.calls++
	if in.TenantID == "" || in.OrderID == "" || in.IdempotencyKey == "" {
		panic("producer 合同：服务端必须从原单装配完整身份，不得空转")
	}
	return p.result, p.err
}

func checkoutOrderSeed(t *testing.T, f *fixture, key string) store.BillingOrderRow {
	t.Helper()
	order, replayed, err := f.s.St.CreateBillingOrder(store.NewBillingOrder{
		TenantID: f.tenA, Kind: billing.KindSubscription, Action: "new",
		PlanCode: "plan-basic", PriceRef: "price-2026", PriceSnapshot: `{"amount":100}`,
		AmountMinor: 100, Currency: "CNY", IdempotencyKey: key,
		PayerPrincipal: f.ownA, ActorPrincipal: f.ownA, ActorRole: "org_owner",
	}, time.Now().UTC().Format(time.RFC3339))
	if err != nil || replayed || order.ID == "" {
		t.Fatalf("seed order: err=%v replayed=%v id=%q", err, replayed, order.ID)
	}
	return order
}

func TestOriginalOrderCheckoutUnwiredWithoutProducer(t *testing.T) {
	f := billingFixture(t, false)
	order := checkoutOrderSeed(t, f, "idem-checkout-1")
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("order view = %d %+v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["available"] != false || checkout["reason"] != "checkout_unwired" {
		t.Fatalf("nil producer 必须保持 checkout_unwired: %+v", checkout)
	}
	row, err := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if err != nil || row.CheckoutRef != "" {
		t.Fatalf("unwired 不得写 checkout 列: err=%v ref=%q", err, row.CheckoutRef)
	}
}

func TestOriginalOrderCheckoutCreatedPersistsRef(t *testing.T) {
	f := billingFixture(t, false)
	producer := &scriptedCheckoutProducer{ready: true, result: billing.OriginalOrderCheckoutResult{
		ResultKind: "created", CheckoutRef: "chk_pub_001",
		ExpiresAt: time.Now().Add(time.Hour).UTC(), ReturnContext: `{"return_to":"/work/billing"}`,
	}}
	f.s.CheckoutProducer = producer
	order := checkoutOrderSeed(t, f, "idem-checkout-2")
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("order view = %d %+v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["available"] != true || checkout["checkout_ref"] != "chk_pub_001" || checkout["resumed"] != false {
		t.Fatalf("created 态应 available+引用: %+v", checkout)
	}
	row, err := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if err != nil || row.CheckoutRef != "chk_pub_001" || row.ReturnContext != `{"return_to":"/work/billing"}` || row.CheckoutRequestedBy != f.ownA {
		t.Fatalf("引用未落库: err=%v row=%+v", err, row)
	}
}

func TestOriginalOrderCheckoutResumesLiveRefWithoutSecondIntent(t *testing.T) {
	f := billingFixture(t, false)
	producer := &scriptedCheckoutProducer{ready: true, result: billing.OriginalOrderCheckoutResult{
		ResultKind: "created", CheckoutRef: "chk_pub_002",
	}}
	f.s.CheckoutProducer = producer
	order := checkoutOrderSeed(t, f, "idem-checkout-3")
	if status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``); status != 200 {
		t.Fatalf("首次签发 = %d %+v", status, out)
	}
	producer.calls = 0
	producer.result = billing.OriginalOrderCheckoutResult{ResultKind: "created", CheckoutRef: "chk_SHOULD_NOT"}
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("查回 = %d %+v", status, out)
	}
	if producer.calls != 0 {
		t.Fatalf("活引用查回不得再调 producer（同 key 单意图）: calls=%d", producer.calls)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["resumed"] != true || checkout["checkout_ref"] != "chk_pub_002" {
		t.Fatalf("查回态 resumed+原引用: %+v", checkout)
	}
}

func TestOriginalOrderCheckoutPaidAlreadyNeverAdvances(t *testing.T) {
	f := billingFixture(t, false)
	f.s.CheckoutProducer = &scriptedCheckoutProducer{ready: true, result: billing.OriginalOrderCheckoutResult{
		ResultKind: "paid_already",
	}}
	order := checkoutOrderSeed(t, f, "idem-checkout-4")
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("view = %d %+v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["available"] != false || checkout["reason"] != "already_paid" {
		t.Fatalf("paid_already 诚实不可用+指引: %+v", checkout)
	}
	row, _ := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if row.Status != billing.OrderProcessing {
		t.Fatalf("本调用绝不推进订单状态: %s", row.Status)
	}
	if row.CheckoutRef != "" {
		t.Fatalf("paid_already 不写引用: %q", row.CheckoutRef)
	}
}

func TestOriginalOrderCheckoutProducerErrorStaysUnavailable(t *testing.T) {
	f := billingFixture(t, false)
	f.s.CheckoutProducer = &scriptedCheckoutProducer{ready: true, err: context.DeadlineExceeded}
	order := checkoutOrderSeed(t, f, "idem-checkout-5")
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("view = %d %+v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["reason"] != "checkout_unavailable" {
		t.Fatalf("Unknown/失败诚实不可用: %+v", checkout)
	}
	row, _ := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if row.CheckoutRef != "" {
		t.Fatalf("失败不得写引用: %q", row.CheckoutRef)
	}
}

func TestSetBillingOrderCheckoutRefConflict(t *testing.T) {
	f := billingFixture(t, false)
	order := checkoutOrderSeed(t, f, "idem-checkout-6")
	if err := f.s.St.SetBillingOrderCheckout(f.tenA, order.ID, store.BillingOrderRow{
		CheckoutRef: "chk_shared", CheckoutStatus: "created",
	}, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("首次绑定: %v", err)
	}
	other := checkoutOrderSeed(t, f, "idem-checkout-7")
	err := f.s.St.SetBillingOrderCheckout(f.tenA, other.ID, store.BillingOrderRow{
		CheckoutRef: "chk_shared",
	}, time.Now().UTC().Format(time.RFC3339))
	if err != store.ErrCheckoutRefConflict {
		t.Fatalf("一引用一单，抢注必须冲突: %v", err)
	}
}

func TestBillingOrderCheckoutLive(t *testing.T) {
	now := time.Now()
	if store.BillingOrderCheckoutLive(store.BillingOrderRow{}, now) {
		t.Fatal("空引用=未签发")
	}
	live := store.BillingOrderRow{CheckoutRef: "chk_x", CheckoutExpiresAt: now.Add(time.Hour).UTC().Format(time.RFC3339)}
	if !store.BillingOrderCheckoutLive(live, now) {
		t.Fatal("未到期引用=活")
	}
	expired := store.BillingOrderRow{CheckoutRef: "chk_x", CheckoutExpiresAt: now.Add(-time.Minute).UTC().Format(time.RFC3339)}
	if store.BillingOrderCheckoutLive(expired, now) {
		t.Fatal("已到期=不活")
	}
	noExpiry := store.BillingOrderRow{CheckoutRef: "chk_x"}
	if !store.BillingOrderCheckoutLive(noExpiry, now) {
		t.Fatal("合同未声明期限=按活处理，真到期由权威查单裁决")
	}
}
