package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// HUI-4664 原单收银接续（touch-order-checkout/v1.1）合同面测试。
// producer 由测试注入 Server；生产默认 nil = checkout_unwired（fail-closed）。

// scriptedCheckoutProducer 是合同面测试替身：按脚本回放 producer 状态之一。
type scriptedCheckoutProducer struct {
	ready  bool
	result billing.MintResult
	err    error
	calls  int
}

func (p *scriptedCheckoutProducer) Ready() bool { return p.ready }

func (p *scriptedCheckoutProducer) Mint(ctx context.Context, in billing.MintRequest) (billing.MintResult, error) {
	p.calls++
	if in.SourceOrderRef == "" || in.SourceApp != "touch-engine" || in.TenantRef == "" {
		panic("mint 合同：source_order_ref（本地 ck_*）必须为主路由键且完整装配")
	}
	return p.result, p.err
}

func (p *scriptedCheckoutProducer) Inspect(ctx context.Context, ckRef string) (billing.MintResult, error) {
	return p.result, p.err
}

func (p *scriptedCheckoutProducer) NotifyActivated(ctx context.Context, ckRef string, occurredAt time.Time, detail map[string]string) error {
	return nil
}

func checkoutOrderSeed(t *testing.T, f *fixture, key string) store.BillingOrderRow {
	t.Helper()
	order, replayed, err := f.s.St.CreateBillingOrder(store.NewBillingOrder{
		TenantID: f.tenA, Kind: billing.KindSubscription, Action: "new",
		PlanCode: "plan-basic", PriceRef: "price-2026", PriceSnapshot: `{"amount_minor":100,"currency":"CNY","pricing_version":"pv1","service_days":30,"included_store_count":1}`,
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

func TestOriginalOrderCheckoutMintCastsLocalRefAndMapsRemote(t *testing.T) {
	f := billingFixture(t, false)
	producer := &scriptedCheckoutProducer{ready: true, result: billing.MintResult{
		Status: "pending", CheckoutRef: "tco_pub_001",
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		CodeURL:   "https://pay.example/qr",
	}}
	f.s.CheckoutProducer = producer
	order := checkoutOrderSeed(t, f, "idem-checkout-2")
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("order view = %d %+v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	ck, _ := checkout["checkout_ref"].(string)
	if checkout == nil || checkout["available"] != true || !strings.HasPrefix(ck, "ck_") {
		t.Fatalf("受理应 available+本地 ck_* 主路由键: %+v", checkout)
	}
	// billing_orders.checkout_ref 存本地 ck_*；映射表存远端 tco_*。
	row, err := f.s.St.GetBillingOrder(f.tenA, order.ID)
	if err != nil || row.CheckoutRef != ck {
		t.Fatalf("ck 未落列: err=%v ref=%q", err, row.CheckoutRef)
	}
	refRow, err := f.s.St.GetBillingCheckoutRef(f.tenA, order.ID)
	if err != nil || refRow.CheckoutRef != ck || refRow.RemoteCheckoutRef != "tco_pub_001" || refRow.Status != "pending" {
		t.Fatalf("映射行缺失/字段错: err=%v row=%+v", err, refRow)
	}
	if producer.calls != 1 {
		t.Fatalf("mint 调用次数: %d", producer.calls)
	}
}

func TestOriginalOrderCheckoutResumesLiveRefWithoutSecondMint(t *testing.T) {
	f := billingFixture(t, false)
	producer := &scriptedCheckoutProducer{ready: true, result: billing.MintResult{
		Status: "pending", CheckoutRef: "tco_pub_002",
	}}
	f.s.CheckoutProducer = producer
	order := checkoutOrderSeed(t, f, "idem-checkout-3")
	if status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``); status != 200 {
		t.Fatalf("首次 = %d %+v", status, out)
	}
	producer.calls = 0
	producer.result = billing.MintResult{Status: "pending", CheckoutRef: "tco_SHOULD_NOT"}
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("查回 = %d %+v", status, out)
	}
	if producer.calls != 0 {
		t.Fatalf("活引用查回不得再 mint（同 key 单意图）: calls=%d", producer.calls)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["resumed"] != true {
		t.Fatalf("查回态 resumed: %+v", checkout)
	}
}

func TestOriginalOrderCheckoutProducerErrorStaysUnavailable(t *testing.T) {
	f := billingFixture(t, false)
	f.s.CheckoutProducer = &scriptedCheckoutProducer{ready: true, err: context.DeadlineExceeded}
	order := checkoutOrderSeed(t, f, "idem-checkout-4")
	status, out := f.do2(t, "GET", "/api/v1/billing/orders/"+order.ID, "sess-owner-a", f.tenA, ``)
	if status != 200 {
		t.Fatalf("view = %d %+v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout == nil || checkout["reason"] != "checkout_unavailable" {
		t.Fatalf("Unknown/失败诚实不可用: %+v", checkout)
	}
	refRow, _ := f.s.St.GetBillingCheckoutRef(f.tenA, order.ID)
	if refRow.RemoteCheckoutRef != "" {
		t.Fatalf("失败不得写远端引用: %q", refRow.RemoteCheckoutRef)
	}
}

func TestBillingCheckoutRefRefConstantAndPaidGate(t *testing.T) {
	f := billingFixture(t, false)
	order := checkoutOrderSeed(t, f, "idem-checkout-5")
	seeded, err := f.s.St.UpsertBillingCheckoutRef(store.BillingCheckoutRefRow{
		TenantID: f.tenA, OrderID: order.ID, CheckoutRef: "ck_const",
		Status: "pending", AmountMinor: 100, Currency: "CNY",
	}, time.Now().UTC().Format(time.RFC3339))
	if err != nil || seeded.CheckoutRef != "ck_const" {
		t.Fatalf("首次绑定: %+v err=%v", seeded, err)
	}
	// 同单换键 = 拒绝（ref 恒定合同）。
	_, err = f.s.St.UpsertBillingCheckoutRef(store.BillingCheckoutRefRow{
		TenantID: f.tenA, OrderID: order.ID, CheckoutRef: "ck_other",
	}, time.Now().UTC().Format(time.RFC3339))
	if err != store.ErrCheckoutRefMapState {
		t.Fatalf("换键必须拒绝: err=%v", err)
	}
	// paid gating：裁决通过推进；重复推进拒绝。
	if err = f.s.St.AdvanceBillingCheckoutRefPaid(f.tenA, order.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("paid 推进: %v", err)
	}
	if err = f.s.St.AdvanceBillingCheckoutRefPaid(f.tenA, order.ID, time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Fatal("重复 paid 推进必须拒绝")
	}
}
