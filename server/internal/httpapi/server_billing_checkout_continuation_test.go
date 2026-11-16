// server_billing_checkout_continuation_test.go: HUI-4664 原单收银接续 HTTP
// 验收矩阵(ADV-9 PR12)。
//
// 覆盖:
//   - 续付登记面:seam 未配置诚实 503(报价与原单保留);seam 已配置→生成
//     checkout_ref,同单重试复用同一引用(同单恢复);GET 只读回查;
//   - 原单成立判定:quote 未消费 = 409;跨租户/跨店 = 同形 404;
//   - 权威事实回流:payment_paid(带金额/币种)→paid;同 payment_ref 重放
//     零新增;金额不符 409 event_mismatch 且不推进;activation_active→
//     activated;failed 如实留因后可重试激活;已生效不追溯削减;
//   - 未授权写入:商家会话 401;registry 未配置 503 fail-closed;错角色 403;
//   - 浏览器返回不当支付证明:回查端点带任意 query 参数零状态变化;
//   - 收银门判定升级:原单有 checkout 引用=可续付(报 checkout_ref 与续付
//     URL);无引用=诚实 checkout_unwired;订单已付=not_awaiting_payment。

package httpapi

import (
	"fmt"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// checkoutFixture wires FEATURE_BILLING_CHECKOUT=on with the public billing
// base URL and the trusted authority producer (pai-billing → billing_authority
// role); off-fixture = billingFixture(t, false) default honest state.
func checkoutFixture(t *testing.T) *fixture {
	t.Helper()
	f := billingFixture(t, true)
	f.s.Cfg.FeatureBillingCheckout = true
	f.s.Cfg.BillingBaseURL = "https://billing.test"
	return f
}

// seedContinuationHandoff walks the real merchant faces: store + pack +
// confirmed quote projection → POST content-production-handoffs (which
// consumes the quote — 原单成立) → returns (storeID, handoffID, quoteRef).
func seedContinuationHandoff(t *testing.T, f *fixture, quoteRef, idemKey string) (string, string) {
	t.Helper()
	stA, err := f.s.St.CreateStore(f.tenA, "接续旗舰店", "地址", "test")
	mustNoErr(t, err)
	pack, err := f.s.St.CreateStoreContentPack(f.tenA, stA.ID, "", "接续首作包", "usr_owner_a")
	mustNoErr(t, err)
	_, _, err = f.s.St.RecordBillingAIUsage(store.NewBillingAIUsage{
		TenantID: f.tenA, QuoteRef: quoteRef, Capability: "content_production", Quantity: 1,
		AmountMinor: 12345600, Currency: "CNY", PricingVersion: "v2026",
		State: billing.QuoteStateConfirmed, BusinessRef: "pack:" + pack.ID,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
	body := fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":%q,"idempotency_key":%q,"note":"收银接续链"}`, pack.ID, quoteRef, idemKey)
	status, out := f.do2(t, "POST", "/api/v1/stores/"+stA.ID+"/content-production-handoffs", "sess-owner-a", f.tenA, body)
	if status != 200 && status != 201 && status != 503 {
		t.Fatalf("seed handoff: %d %v", status, out)
	}
	h, _ := out["handoff"].(map[string]any)
	if h == nil || h["id"] == "" {
		t.Fatalf("seed handoff missing row: %d %v", status, out)
	}
	// 发起面已消费报价(ConsumeQuoteProjection 已发生 = 原单成立)。
	usage, err := f.s.St.GetBillingAIUsage(f.tenA, quoteRef)
	mustNoErr(t, err)
	if usage.State != billing.QuoteStateConsumed || usage.ConsumedByRef != h["id"].(string) {
		t.Fatalf("seed quote not consumed: %+v", usage)
	}
	return stA.ID, h["id"].(string)
}

func TestHandoffCheckoutHonestWhenSeamUnconfigured(t *testing.T) {
	f := billingFixture(t, false) // FEATURE_BILLING_CHECKOUT off(默认)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_unwired_1", "idem-unwired-1")
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"

	// 无付款资格:403(staff)。
	status, out := f.do2(t, "POST", path, "sess-staff-a", f.tenA, "")
	if status != 403 {
		t.Fatalf("staff checkout = %d %v", status, out)
	}
	// 跨租户:他租户会话同形 404(不泄漏存在性)。
	status, out = f.do2(t, "POST", path, "sess-owner-b", f.tenB, "")
	if status != 404 {
		t.Fatalf("foreign tenant checkout = %d %v", status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-b", f.tenB, "")
	if status != 404 {
		t.Fatalf("foreign tenant readback = %d %v", status, out)
	}
	// seam 未配置:诚实 503 checkout_unavailable,报价与原单保留。
	status, out = f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 503 || out["error"] != "checkout_unavailable" {
		t.Fatalf("unconfigured seam = %d %v", status, out)
	}
	if _, ok := out["handoff"].(map[string]any); !ok {
		t.Fatalf("503 must keep the original handoff for recovery: %v", out)
	}
	if _, ok := out["quote"].(map[string]any); !ok {
		t.Fatalf("503 must keep the quote for recovery: %v", out)
	}
	// 未生成引用:GET 回查=诚实未接通。
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != false || checkout["reason"] != "checkout_unwired" {
		t.Fatalf("readback without ref = %d %v", status, out)
	}
	// 未授权写事实:商家会话打权威缝一律 401(资金事实永不自授)。
	status, out = f.do2(t, "POST", "/api/v1/billing/authority/events", "sess-owner-a", f.tenA, fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":"ck_x","source":"pai-billing","event_id":"e1","type":"payment_paid","payload":{"payment_ref":"p1"},"amount_minor":1,"currency":"CNY"}`, f.tenA))
	if status != 401 {
		t.Fatalf("session on authority seam = %d %v", status, out)
	}
}

func TestHandoffCheckoutContinuationRecoveryAndFacts(t *testing.T) {
	f := checkoutFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cont_1", "idem-cont-1")
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"

	// 续付登记:生成 checkout_ref + seam 配置驱动的续付 URL;请求体零输入。
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 201 {
		t.Fatalf("first continuation = %d %v", status, out)
	}
	checkout, _ := out["checkout"].(map[string]any)
	ref, _ := checkout["checkout_ref"].(string)
	if checkout["available"] != true || ref == "" {
		t.Fatalf("continuation not offered: %v", checkout)
	}
	if checkout["checkout_url"] != "https://billing.test/billing/touch-checkout/"+ref || checkout["state"] != "checkout_pending" {
		t.Fatalf("checkout_url/state wrong: %v", checkout)
	}
	// 同单重试:复用同一引用(replayed=true,零新增)。
	status, out = f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 200 || out["replayed"] != true {
		t.Fatalf("same-order retry = %d %v", status, out)
	}
	checkout, _ = out["checkout"].(map[string]any)
	if checkout["checkout_ref"] != ref {
		t.Fatalf("same order must reuse one checkout_ref: %v vs %v", checkout["checkout_ref"], ref)
	}
	// GET 只读回查(浏览器返回落地;带任意 query 参数零状态变化)。
	status, out = f.do2(t, "GET", path+"?success=1&cb=https://evil.example", "sess-owner-a", f.tenA, "")
	checkout, _ = out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != true || checkout["checkout_ref"] != ref || checkout["state"] != "checkout_pending" {
		t.Fatalf("readback = %d %v", status, out)
	}

	payEvent := func(eventID, paymentRef string, amount int64) string {
		return fmt.Sprintf(`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":%q,"type":"payment_paid","payload":{"payment_ref":%q},"amount_minor":%d,"currency":"CNY"}`,
			f.tenA, ref, eventID, paymentRef, amount)
	}
	// 金额不符:409 event_mismatch,状态不推进。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent("evt-bad", "pay_bad", 1))
	if status != 409 || out["error"] != "event_mismatch" {
		t.Fatalf("mismatched payment = %d %v", status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ = out["checkout"].(map[string]any)
	if checkout["state"] != "checkout_pending" || checkout["available"] != true {
		t.Fatalf("mismatch must not advance the projection: %v", checkout)
	}
	// 正确支付事实:pending→paid(activation 就绪)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent("evt-paid-1", "pay_1", 12345600))
	if status != 201 || out["state"] != "paid" {
		t.Fatalf("payment fact = %d %v", status, out)
	}
	// 同 payment_ref 重放:幂等读回,零新增零改写。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent("evt-paid-1", "pay_1", 12345600))
	if status != 200 || out["replayed"] != true || out["state"] != "paid" {
		t.Fatalf("payment replay = %d %v", status, out)
	}
	// paid 之后:没有可续付的款项(诚实 not_awaiting_payment,附状态)。
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ = out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != false || checkout["reason"] != "not_awaiting_payment" || checkout["state"] != "paid" {
		t.Fatalf("paid readback = %d %v", status, out)
	}
	// activation_active:paid→activated;重放幂等;已生效不被迟到失败追溯。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-active-1","type":"activation_active","payload":{}}`, f.tenA, ref))
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("activation = %d %v", status, out)
	}
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-active-1","type":"activation_active","payload":{}}`, f.tenA, ref))
	if status != 200 || out["replayed"] != true {
		t.Fatalf("activation replay = %d %v", status, out)
	}
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-late-fail","type":"activation_failed","payload":{"fail_reason":"late"}}`, f.tenA, ref))
	if status != 409 || out["error"] != "invalid_transition" {
		t.Fatalf("late failed = %d %v", status, out)
	}
}

func TestHandoffCheckoutFailedKeepsReasonAndRetriesActivation(t *testing.T) {
	f := checkoutFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cont_2", "idem-cont-2")
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 201 {
		t.Fatalf("continuation = %d %v", status, out)
	}
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	mustPaid := func() {
		t.Helper()
		s, o := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
			`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-2","type":"payment_paid","payload":{"payment_ref":"pay_2"},"amount_minor":12345600,"currency":"CNY"}`,
			f.tenA, ref))
		if s != 201 && s != 200 {
			t.Fatalf("paid: %d %v", s, o)
		}
	}
	mustPaid()
	// paid→failed:如实留因。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-fail-2","type":"activation_failed","payload":{"fail_reason":"provider_timeout"}}`, f.tenA, ref))
	if status != 201 || out["state"] != "failed" {
		t.Fatalf("failed = %d %v", status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if checkout["state"] != "failed" || checkout["fail_reason"] != "provider_timeout" || checkout["available"] != false {
		t.Fatalf("failed readback = %v", checkout)
	}
	// 补偿:paid 而 activation 失败=可重试激活(failed→activated,支付事实保持)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-retry-2","type":"activation_active","payload":{}}`, f.tenA, ref))
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("retry activation = %d %v", status, out)
	}
}

func TestHandoffCheckoutRefusesUnestablishedOriginal(t *testing.T) {
	f := checkoutFixture(t)
	stA, err := f.s.St.CreateStore(f.tenA, "未成立店", "地址", "test")
	mustNoErr(t, err)
	pack, err := f.s.St.CreateStoreContentPack(f.tenA, stA.ID, "", "未成立包", "usr_owner_a")
	mustNoErr(t, err)
	// 直接建行,不消费报价(原单未成立)。
	h, _, err := f.s.St.CreateProductionHandoff(f.tenA, store.ProductionHandoffInput{
		StoreID: stA.ID, PackID: pack.ID, QuoteRef: "q_ghost", Note: "幽灵原单", IdempotencyKey: "idem-ghost-1",
	}, "usr_owner_a")
	mustNoErr(t, err)
	path := "/api/v1/stores/" + stA.ID + "/content-production-handoffs/" + h.ID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 409 || out["error"] != "quote_not_consumed" {
		t.Fatalf("unestablished original = %d %v", status, out)
	}
	// 跨店挂接:handoff 属于 stA,从 stB 路径访问 = 同形 404。
	stB, err := f.s.St.CreateStore(f.tenA, "别店", "地址", "test")
	mustNoErr(t, err)
	status, out = f.do2(t, "POST", "/api/v1/stores/"+stB.ID+"/content-production-handoffs/"+h.ID+"/checkout", "sess-owner-a", f.tenA, "")
	if status != 404 {
		t.Fatalf("cross-store checkout = %d %v", status, out)
	}
}

func TestAuthorityCheckoutSeamFailsClosed(t *testing.T) {
	// registry 配了服务但角色不是 billing_authority:403(最小权限)。
	//(doService 固定用 eco-billing/bill-secret 身份头,故注册同一 id 换角色。)
	f := billingFixture(t, false)
	f.s.Cfg.ServiceTokens = map[string]string{"eco-billing": "bill-secret"}
	f.s.Cfg.ServiceRoles = map[string]string{"eco-billing": "queue_producer"}
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":"ck_y","source":"eco-billing","event_id":"e2","type":"payment_paid","payload":{"payment_ref":"p2"},"amount_minor":1,"currency":"CNY"}`, f.tenA))
	if status != 403 {
		t.Fatalf("wrong role = %d %v", status, out)
	}
	// registry 未配置:503 fail-closed(不伪造成功)。
	f2 := billingFixture(t, false)
	status, out = f2.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":"ck_z","source":"eco-billing","event_id":"e3","type":"payment_paid","payload":{"payment_ref":"p3"},"amount_minor":1,"currency":"CNY"}`, f2.tenA))
	if status != 503 {
		t.Fatalf("unconfigured registry = %d %v", status, out)
	}
	// 未知 checkout_ref:404 同形掩蔽。
	f3 := checkoutFixture(t)
	status, out = f3.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":"ck_missing","source":"eco-billing","event_id":"e4","type":"payment_paid","payload":{"payment_ref":"p4"},"amount_minor":1,"currency":"CNY"}`, f3.tenA))
	if status != 404 {
		t.Fatalf("unknown ref = %d %v", status, out)
	}
	// checkout_ref 搭配非法事件类型:400。
	status, out = f3.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":"ck_missing","source":"eco-billing","event_id":"e5","type":"order_registered","payload":{}}`, f3.tenA))
	if status != 400 {
		t.Fatalf("bad checkout event type = %d %v", status, out)
	}
}

func TestOrderCheckoutGateUpgradesOnlyWithReference(t *testing.T) {
	f := checkoutFixture(t)
	seedCatalog(t, f)
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"gate-upgrade"}`)
	if status != 201 {
		t.Fatal(status, out)
	}
	order := out["order"].(map[string]any)
	id := order["id"].(string)
	path := "/api/v1/billing/orders/" + id
	// 无引用:诚实 checkout_unwired(与既有门字段逐字节兼容)。
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != false || checkout["reason"] != "checkout_unwired" {
		t.Fatalf("no-ref gate = %d %v", status, out)
	}
	amount, _ := order["amount_minor"].(float64)
	row, replayed, err := f.s.St.UpsertBillingCheckoutRef(store.NewBillingCheckoutRef{
		TenantID: f.tenA, OrderRef: id, AmountMinor: int64(amount), Currency: "CNY",
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
	if replayed {
		t.Fatal("seed ref should be new")
	}
	// 有引用:可续付(报 checkout_ref 与续付 URL)。
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ = out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != true || checkout["checkout_ref"] != row.CheckoutRef ||
		checkout["checkout_url"] != "https://billing.test/billing/touch-checkout/"+row.CheckoutRef {
		t.Fatalf("ref-bound gate = %d %v", status, out)
	}
	// 订单已付:not_awaiting_payment 优先(门不再提供续付)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"gate-paid","type":"payment_paid","payload":{"payment_ref":"gate-pay"}}`, f.tenA, id))
	if status != 200 && status != 201 {
		t.Fatal(status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ = out["checkout"].(map[string]any)
	if status != 200 || checkout["available"] != false || checkout["reason"] != "not_awaiting_payment" {
		t.Fatalf("paid gate = %d %v", status, out)
	}
	// 跨租户掩码回归:他租户查单仍 404。
	status, _ = f.do2(t, "GET", path, "sess-owner-b", f.tenB, "")
	if status != 404 {
		t.Fatalf("foreign order = %d", status)
	}
}
