package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
)

func checkoutFixtureWithProducer(t *testing.T) (*fixture, *fakeTouchProducer) {
	t.Helper()
	p := newFakeTouchProducer(t)
	f := billingFixture(t, true)
	f.s.Cfg.FeatureBillingCheckout = true
	f.s.Cfg.FeatureAgency = true
	f.s.Cfg.FeatureAuthz = true
	f.s.Cfg.BillingBaseURL = p.ts.URL
	f.s.Cfg.BillingToken = "hui4664-billing-token"
	return f, p
}

func TestHUI4664SubscriptionCheckoutMintInspectActivateAndPaidForbidden(t *testing.T) {
	f, producer := checkoutFixtureWithProducer(t)
	seedCatalog(t, f)

	// 1. 查服务 → 扩店报价 → 创建订阅原单(冻结价格版本与金额)。
	status, quote := f.do2(t, "POST", "/api/v1/billing/subscription-quote", "sess-owner-a", f.tenA,
		`{"action":"expand","plan_code":"store.plan.month","additional_stores":2}`)
	if status != 200 || quote["price_ref"] != "price_m_v2026" || quote["pricing_version"] != "v2026" {
		t.Fatalf("quote failed: %d %v", status, quote)
	}
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"expand","plan_code":"store.plan.month","price_ref":"price_m_v2026","additional_stores":2,"idempotency_key":"hui4664-sub-1"}`)
	if status != 201 {
		t.Fatalf("create order failed: %d %v", status, out)
	}
	order := out["order"].(map[string]any)
	orderID := order["id"].(string)
	origAmount := int64(order["amount_minor"].(float64))
	if order["pricing_version"] != "v2026" {
		t.Fatalf("order must freeze pricing_version: %v", order)
	}

	// 2. 发起真实公共 Mint: 返回权威 remote_checkout_ref / out_trade_no / code_url。
	status, mintOut := f.do2(t, "POST", "/api/v1/billing/orders/"+orderID+"/checkout", "sess-owner-a", f.tenA, `{"provider":"wechat"}`)
	if status != 201 {
		t.Fatalf("order mint failed: %d %v", status, mintOut)
	}
	ck := mintOut["checkout"].(map[string]any)
	ckRef := ck["checkout_ref"].(string)
	if ck["available"] != true || ck["remote_checkout_ref"] != "tco_"+ckRef || ck["out_trade_no"] != "OTN_"+ckRef || ck["code_url"] == "" {
		t.Fatalf("authoritative mint payload incomplete: %v", ck)
	}

	// 3. 浏览器普通返回(带恶意 query 参数)走 GET 只读 Inspect: 永不推进本地支付状态。
	status, backOut := f.do2(t, "GET", "/api/v1/billing/orders/"+orderID+"/checkout?paid=1&status=active", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("return readback failed: %d %v", status, backOut)
	}
	cont := backOut["continuity"].(map[string]any)
	if cont["lifecycle_stage"] != billing.StagePendingPayment || cont["paid"] != false {
		t.Fatalf("browser return must never advance payment state: %v", cont)
	}

	// 4. 权威 payment_paid 事件(按 checkout_ref 路由)推进至 opening(已付开通同步中)。
	status, evOut := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"eco-billing","event_id":"evt-sub-paid-1","type":"payment_paid","payload":{"order_ref":"OTN_%s","payment_ref":"pay_sub_1"},"amount_minor":%d,"currency":"CNY"}`,
		f.tenA, ckRef, ckRef, origAmount))
	if status != 201 || evOut["state"] != billing.CheckoutStatePaid {
		t.Fatalf("payment_paid failed: %d %v", status, evOut)
	}

	// 5. 已付款未开通: lifecycle_stage=service_activating, 严禁重复购买, 再次发起支付被 409 拒绝!
	status, getOut := f.do2(t, "GET", "/api/v1/billing/orders/"+orderID, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("get opening order: %d %v", status, getOut)
	}
	cont = getOut["continuity"].(map[string]any)
	nextStep := cont["next_step"].(map[string]any)
	if cont["lifecycle_stage"] != billing.StageServiceActivating || cont["paid"] != true || cont["service_active"] != false {
		t.Fatalf("expected service_activating after payment_paid: %v", cont)
	}
	if nextStep["forbid_repeat_purchase"] != true || nextStep["allow_retry_payment"] != false {
		t.Fatalf("activating order must forbid repeat purchase: %v", nextStep)
	}
	// paid 禁再付!
	status, repayOut := f.do2(t, "POST", "/api/v1/billing/orders/"+orderID+"/checkout", "sess-owner-a", f.tenA, `{"provider":"wechat"}`)
	if status != 409 || repayOut["error"] != "order_already_paid" {
		t.Fatalf("paid order must refuse second checkout with 409 order_already_paid, got %d %v", status, repayOut)
	}

	// 6. 乱序/重复 activation_opening 与 payment_paid 重放: 幂等零重复开通。
	status, opOut := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"eco-billing","event_id":"evt-sub-open-1","type":"activation_opening","payload":{}}`,
		f.tenA, ckRef))
	if status != 200 || opOut["replayed"] != true {
		t.Fatalf("activation_opening must be no-op ack: %d %v", status, opOut)
	}

	// 7. 权威 activation_active 推进至 active, 写入订阅投影并回发 §2b 终态戳。
	paidUntil := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	status, actOut := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"eco-billing","event_id":"evt-sub-act-1","type":"activation_active","payload":{"subscription_ref":"sub_4664_1","plan_code":"store.plan.month","pricing_version":"v2026","paid_until":%q,"store_limit":3,"grace_days":7}}`,
		f.tenA, ckRef, paidUntil))
	if status != 201 || actOut["state"] != billing.CheckoutStateActivated {
		t.Fatalf("activation_active failed: %d %v", status, actOut)
	}
	producer.mu.Lock()
	notifies := producer.notifyCalls
	producer.mu.Unlock()
	if notifies != 1 {
		t.Fatalf("expected 1 §2b activation_active notify to producer, got %d", notifies)
	}

	// 8. 验证最终读面: service_active + 历史价格与生效日一致; 修改新目录不改写历史订单与旧权益!
	_, err := f.s.St.UpsertBillingCatalogEntry(billing.CatalogEntry{
		PlanCode: billing.PlanMonthly, PricingVersion: "v2027", PriceRef: "price_m_v2027",
		Currency: "CNY", PriceMinor: 19900, ServiceDays: 30, IncludedStoreCount: 1,
		ExtraStorePriceMinor: 5000, GraceDays: 7,
		EffectiveFrom: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)

	status, finalOrder := f.do2(t, "GET", "/api/v1/billing/orders/"+orderID, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("final order read: %d %v", status, finalOrder)
	}
	ordFinal := finalOrder["order"].(map[string]any)
	contFinal := finalOrder["continuity"].(map[string]any)
	if int64(ordFinal["amount_minor"].(float64)) != origAmount || ordFinal["pricing_version"] != "v2026" || contFinal["lifecycle_stage"] != billing.StageServiceActive {
		t.Fatalf("historical order or active stage drifted after catalog update: order=%v cont=%v", ordFinal, contFinal)
	}
	status, subOut := f.do2(t, "GET", "/api/v1/billing/subscription", "sess-owner-a", f.tenA, "")
	subPane := subOut["subscription"].(map[string]any)
	if status != 200 || subPane["status"] != "active" || subPane["pricing_version"] != "v2026" || subPane["paid_until"] != paidUntil {
		t.Fatalf("active subscription entitlement drifted: %d %v", status, subOut)
	}
}

func TestHUI4664NegativeAndRecoveryCases(t *testing.T) {
	f, producer := checkoutFixtureWithProducer(t)
	seedCatalog(t, f)

	// 1. 报价过期/变价(旧 price_ref 被目录新版本取代) → 409 catalog_changed。
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","price_ref":"price_m_expired","idempotency_key":"idem-expired-1"}`)
	if status != 409 || out["error"] != "catalog_changed" {
		t.Fatalf("expired price_ref = %d %v, want 409 catalog_changed", status, out)
	}

	// 2. 创建原单后同键异参 → 409 idempotency_conflict, 零新增订单。
	status, out = f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","price_ref":"price_m_v2026","idempotency_key":"idem-recover-1"}`)
	if status != 201 {
		t.Fatalf("create order = %d %v", status, out)
	}
	orderID := out["order"].(map[string]any)["id"].(string)

	status, out = f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"renew","plan_code":"store.plan.month","price_ref":"price_m_v2026","idempotency_key":"idem-recover-1"}`)
	if status != 409 || out["error"] != "idempotency_conflict" {
		t.Fatalf("same key different params = %d %v, want 409", status, out)
	}

	// 3. 受理丢响应/重启恢复: 首次 Mint 上游 503 → 本地 ck_* 保留 → 重试复用同一 ck_* 成功拿回权威收银单!
	producer.mu.Lock()
	producer.failNextMint = 1
	producer.mu.Unlock()

	status, out = f.do2(t, "POST", "/api/v1/billing/orders/"+orderID+"/checkout", "sess-owner-a", f.tenA, `{"provider":"wechat"}`)
	if status != 503 || out["error"] != "checkout_unavailable" {
		t.Fatalf("transient mint failure = %d %v, want 503", status, out)
	}
	ckFirst := out["checkout"].(map[string]any)["checkout_ref"].(string)
	if ckFirst == "" {
		t.Fatalf("transient 503 must preserve stable local checkout_ref for recovery: %v", out)
	}

	// 重试(模拟丢响应后重启重发): 复用同一个 ck_* 并成功 Mint, 不产生第二个原单。
	status, out = f.do2(t, "POST", "/api/v1/billing/orders/"+orderID+"/checkout", "sess-owner-a", f.tenA, `{"provider":"wechat"}`)
	if status != 200 && status != 201 {
		t.Fatalf("recovery retry failed: %d %v", status, out)
	}
	ckRecovered := out["checkout"].(map[string]any)["checkout_ref"].(string)
	if ckRecovered != ckFirst {
		t.Fatalf("recovery must reuse exact checkout_ref %q, got %q", ckFirst, ckRecovered)
	}
	orders, err := f.s.St.ListBillingOrders(f.tenA, "subscription")
	mustNoErr(t, err)
	if len(orders) != 1 {
		t.Fatalf("recovery created duplicate orders: %d", len(orders))
	}

	// 4. 远端价格变更拒绝(producer 报 touch_checkout_price_changed) → 409 price_changed, 原单保留。
	status, out2 := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","price_ref":"price_m_v2026","idempotency_key":"idem-price-changed"}`)
	if status != 201 {
		t.Fatalf("order2 = %d %v", status, out2)
	}
	order2ID := out2["order"].(map[string]any)["id"].(string)
	producer.mu.Lock()
	producer.mintErrStatus = http.StatusConflict
	producer.mintErrCode = "touch_checkout_price_changed"
	producer.mu.Unlock()
	status, pcOut := f.do2(t, "POST", "/api/v1/billing/orders/"+order2ID+"/checkout", "sess-owner-a", f.tenA, `{"provider":"wechat"}`)
	if status != 409 || pcOut["error"] != "price_changed" {
		t.Fatalf("remote price_changed = %d %v, want 409 price_changed", status, pcOut)
	}
}

func TestHUI4664AgencyNonPayerBoundaryAndRevocation(t *testing.T) {
	p := newFakeTouchProducer(t)
	f := newAgencyFixture(t, true)
	f.s.Cfg.FeatureBillingCheckout = true
	f.s.Cfg.FeatureAuthz = true
	f.s.Cfg.BillingBaseURL = p.ts.URL
	f.s.Cfg.BillingToken = "hui4664-billing-token"
	f.s.Cfg.ServiceTokens = map[string]string{"eco-billing": "bill-secret"}
	f.s.Cfg.ServiceRoles = map[string]string{"eco-billing": "billing_authority"}
	seedCatalogStore(t, f.s.St)

	// 建立代理关系: 授予 storefront + renewal, 但不授予独立 payer_grant_ref!
	relID := f.mustEstablish(t, "usr_ag1", "无付款权配置代理")
	status, scopeOut := f.do(t, "PUT", "/api/v1/agency/relations/"+relID+"/scope", "sess-owner-a", f.tenA,
		`{"allowed_actions":["storefront","renewal"],"store_scope_set":[],"payer_grant_ref":"","grant_revision":0,"reason":"配置与续费协助但不代付"}`)
	if status != 200 {
		t.Fatalf("set scope: %d %v", status, scopeOut)
	}

	// (a) 获准的非收费事项畅通: 查门店、查报价、查 M12 连续性投影。
	if status, _ = f.do(t, "GET", "/api/v1/stores", "sess-agent1", f.tenA, ""); status != 200 {
		t.Fatalf("agent without payer must still read stores: %d", status)
	}
	if status, _ = f.do(t, "POST", "/api/v1/billing/subscription-quote", "sess-agent1", f.tenA,
		`{"action":"new","plan_code":"store.plan.month"}`); status != 200 {
		t.Fatalf("agent with renewal must still read quote: %d", status)
	}
	status, contView := f.do(t, "GET", "/api/v1/billing/continuity", "sess-agent1", f.tenA, "")
	if status != 200 {
		t.Fatalf("continuity read: %d %v", status, contView)
	}
	payerAuth := contView["payer_authority"].(map[string]any)
	if payerAuth["can_pay"] != false || payerAuth["missing_reason"] != billing.PayerReasonRequired {
		t.Fatalf("agent without payer_grant_ref must show can_pay=false: %v", payerAuth)
	}

	// (b) 收费动作 fail-closed 拒绝, 绝不误扣个人或商家钱包!
	status, ordDeny := f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-agent1", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"agent-no-payer-try"}`)
	if status != 403 || ordDeny["error"] != billing.PayerReasonRequired {
		t.Fatalf("agent without payer must be refused 403 on order create: %d %v", status, ordDeny)
	}

	// (c) 授予独立 payer_grant_ref → 代理可下单, 但商家撤回/更换代付授权后, 代理再次发起支付被 403 拦截!
	status, scopeOut = f.do(t, "PUT", "/api/v1/agency/relations/"+relID+"/scope", "sess-owner-a", f.tenA,
		`{"allowed_actions":["storefront","renewal"],"store_scope_set":[],"payer_grant_ref":"pg_agent_ok_1","grant_revision":1,"reason":"授予独立代付"}`)
	if status != 200 {
		t.Fatalf("grant payer: %d %v", status, scopeOut)
	}
	status, agentOrderOut := f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-agent1", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"agent-with-payer-1"}`)
	if status != 201 {
		t.Fatalf("agent with payer grant order create = %d %v", status, agentOrderOut)
	}
	agentOrderID := agentOrderOut["order"].(map[string]any)["id"].(string)

	// 商家撤回代付授权(payer_grant_ref 置空) → 代理尝试 Mint 立即 403 original_payer_required!
	status, scopeOut = f.do(t, "PUT", "/api/v1/agency/relations/"+relID+"/scope", "sess-owner-a", f.tenA,
		`{"allowed_actions":["storefront","renewal"],"store_scope_set":[],"payer_grant_ref":"","grant_revision":2,"reason":"收回独立代付授权"}`)
	if status != 200 {
		t.Fatalf("revoke payer grant: %d %v", status, scopeOut)
	}
	status, mintDenied := f.do(t, "POST", "/api/v1/billing/orders/"+agentOrderID+"/checkout", "sess-agent1", f.tenA, `{"provider":"wechat"}`)
	if status != 403 || mintDenied["error"] != "original_payer_required" {
		t.Fatalf("revoked payer grant must block checkout with 403 original_payer_required: %d %v", status, mintDenied)
	}
}

func TestHUI4664DistinctStagesPaidActivatingVsHardwarePendingVsRefundInProgress(t *testing.T) {
	p := newFakeTouchProducer(t)
	f := dlvFixture(t)
	f.s.Cfg.FeatureBillingCheckout = true
	f.s.Cfg.BillingBaseURL = p.ts.URL
	f.s.Cfg.BillingToken = "hui4664-billing-token"
	seedCatalogStore(t, f.s.St)
	_, _, _, _, _, _, tp1, _ := tpSeedScene(t, f)

	// 订单 A: 订阅原单已付款未开通(opening → service_activating)。
	status, outA := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"stage-sub-opening"}`)
	if status != 201 {
		t.Fatalf("order A: %d %v", status, outA)
	}
	orderA := outA["order"].(map[string]any)["id"].(string)
	status, _ = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"evt-a-paid","type":"payment_paid","payload":{"payment_ref":"pay_a"}}`, f.tenA, orderA))
	if status != 201 {
		t.Fatalf("pay A: %d", status)
	}

	// 订单 B: 硬件原单已付款 + 创建 HUI-1663 实卡交付(仅出库, 未现场验收 → hardware_pending)。
	orderB := dlvSeedOrder(t, f, "stage-hw-pending")
	dlvPayOrder(t, f, orderB)
	dlv := dlvCreateDelivery(t, f, tp1, orderB)
	dlvID := dlv["id"].(string)
	status, _ = dlvEvent(t, f, dlvID, "dispatched", "dlv-disp-1", ``)
	if status != 201 {
		t.Fatalf("dispatch B: %d", status)
	}

	// 订单 C: 已付原单进入同单退款(refund_in_progress), 严禁再次支付。
	status, outC := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"renew","plan_code":"store.plan.month","idempotency_key":"stage-sub-refund"}`)
	if status != 201 {
		t.Fatalf("order C: %d %v", status, outC)
	}
	orderC := outC["order"].(map[string]any)["id"].(string)
	status, _ = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"evt-c-paid","type":"payment_paid","payload":{"payment_ref":"pay_c"}}`, f.tenA, orderC))
	if status != 201 {
		t.Fatalf("pay C: %d", status)
	}
	status, refOut := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"evt-c-refund","type":"refund_registered","refund_ref":"ref_c_1","amount_minor":9900,"currency":"CNY","reason":"同单退款测试","invoice_title":"A公司","invoice_status":"pending","auditor":"ops_1"}`, f.tenA, orderC))
	if status != 201 {
		t.Fatalf("refund C: %d %v", status, refOut)
	}

	// 读取三笔订单详情: 验证 lifecycle_stage / milestones / next_step 清晰不同!
	getStage := func(id string) (string, string, map[string]any) {
		t.Helper()
		st, o := f.do2(t, "GET", "/api/v1/billing/orders/"+id, "sess-owner-a", f.tenA, "")
		if st != 200 {
			t.Fatalf("get order %s = %d %v", id, st, o)
		}
		c := o["continuity"].(map[string]any)
		ns := c["next_step"].(map[string]any)
		return c["lifecycle_stage"].(string), ns["code"].(string), o
	}
	stageA, codeA, _ := getStage(orderA)
	stageB, codeB, detB := getStage(orderB)
	stageC, codeC, _ := getStage(orderC)

	if stageA != billing.StageServiceActivating || stageB != billing.StageHardwarePending || stageC != billing.StageRefundInProgress {
		t.Fatalf("expected distinct stages (activating/hardware_pending/refund), got A=%s B=%s C=%s", stageA, stageB, stageC)
	}
	if codeA == codeB || codeB == codeC || codeA == codeC {
		t.Fatalf("expected distinct next steps, got A=%s B=%s C=%s", codeA, codeB, codeC)
	}
	if detB["continuity"].(map[string]any)["hardware_delivered"] != false {
		t.Fatalf("paid hardware without site acceptance must have hardware_delivered=false: %v", detB["continuity"])
	}
	// 同单退款订单禁止再次付款(409)。
	status, repayC := f.do2(t, "POST", "/api/v1/billing/orders/"+orderC+"/checkout", "sess-owner-a", f.tenA, `{"provider":"wechat"}`)
	if status != 409 || repayC["error"] != "order_already_paid" {
		t.Fatalf("refunding order must refuse checkout: %d %v", status, repayC)
	}
}

func TestHUI4664AgencyTodosAndM12ContinuityProjections(t *testing.T) {
	f := newAgencyFixture(t, true)
	f.s.Cfg.FeatureAuthz = true
	// 不种目录、收银通道默认关闭, 验证 M12 缺项诚实解释(零虚构价格)。

	status, m12 := f.do(t, "GET", "/api/v1/billing/continuity", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("m12 continuity: %d %v", status, m12)
	}
	catReady := m12["catalog_readiness"].(map[string]any)
	chanReady := m12["checkout_channel"].(map[string]any)
	if catReady["ready"] != false || chanReady["ready"] != false {
		t.Fatalf("missing catalog/channel must be reported honestly: cat=%v chan=%v", catReady, chanReady)
	}

	// A1 空态: 普通账号无任何代理授权时必须返回空 items/merchants(零示例任务)。
	status, emptyTodos := f.do(t, "GET", "/api/v1/agency/todos", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("empty agency todos: %d %v", status, emptyTodos)
	}
	if items := emptyTodos["items"].([]any); len(items) != 0 {
		t.Fatalf("A1 empty state must never generate sample/demo tasks, got %v", items)
	}

	// 建立真实代管关系(含 storefront + renewal, 无 payer_grant_ref) 并让商户无有效订阅 →
	// 出现真实待办(pending_onboarding, owner=merchant, can_actor_handle=false)。
	relID := f.mustEstablish(t, "usr_ag1", "A1待办")
	status, _ = f.do(t, "PUT", "/api/v1/agency/relations/"+relID+"/scope", "sess-owner-a", f.tenA,
		`{"allowed_actions":["storefront","renewal"],"store_scope_set":[],"payer_grant_ref":"","grant_revision":0,"reason":"A1测试"}`)
	if status != 200 {
		t.Fatalf("set scope: %d", status)
	}

	status, a1 := f.do(t, "GET", "/api/v1/agency/todos", "sess-agent1", f.tenA, "")
	if status != 200 {
		t.Fatalf("agent todos: %d %v", status, a1)
	}
	merchants := a1["merchants"].([]any)
	items := a1["items"].([]any)
	if len(merchants) != 1 || len(items) < 1 {
		t.Fatalf("expected 1 authorized merchant and >=1 real todo, got merchants=%d items=%v", len(merchants), items)
	}
	cats := map[string]map[string]any{}
	for _, raw := range items {
		it := raw.(map[string]any)
		cats[it["category"].(string)] = it
	}
	if cats["pending_onboarding"] == nil || cats["pending_onboarding"]["owner"] != "merchant" || cats["pending_onboarding"]["can_actor_handle"] != false {
		t.Fatalf("onboarding payment todo must route to merchant when agent has no payer grant: %v", cats["pending_onboarding"])
	}

	// 撤回代管关系后立即失效(代理不再看到该商户任何待办)。
	status, _ = f.do(t, "DELETE", "/api/v1/agency/relations/"+relID, "sess-owner-a", f.tenA, `{"reason":"撤回测试"}`)
	if status != 200 {
		t.Fatalf("revoke rel: %d", status)
	}
	_, err := f.s.St.CreateMember(f.tenB, "usr_ag1", "staff", "B员工", "test", true)
	mustNoErr(t, err)
	status, afterRevoke := f.do(t, "GET", "/api/v1/agency/todos", "sess-agent1", f.tenB, "")
	if status != 200 || len(afterRevoke["items"].([]any)) != 0 || len(afterRevoke["merchants"].([]any)) != 0 {
		t.Fatalf("revoked relation must disappear immediately from A1 todos: %d %v", status, afterRevoke)
	}
}
