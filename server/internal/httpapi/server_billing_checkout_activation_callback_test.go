// server_billing_checkout_activation_callback_test.go: 统一契约 v1.1(ADV-9
// FIX-B 落地 + FIX-C/D5 回发通道收敛)touch 侧验收矩阵——
//
//   - v1.1 §1:activation_opening 携带 checkout_ref = FYI no-op ack(HTTP 面,
//     状态零改写);order_registered 带 checkout_ref 仍 400 未知类型拒;
//   - v1.1 §2/D3:payment_paid 落 paid + handoff 已提交任务 → 自主 activated
//     + activation_active 回发 producer(§2b 冻结端点 POST /internal/v1/
//     billing/touch-orders/activation-active,X-App-ID=touch-engine + app
//     token;checkout_ref 主路由键,occurred_at/event_id 确定性:重放/重试
//     字节恒等);回发可重试失败 → 503(渠道重试→重放→再触发自愈);404
//     (producer 未知 checkout_ref)=照实留痕放行;缝未配置 → 本地事实照落、
//     诚实跳过;handoff 未提交 → 保持 paid(不伪造本地事实);
//   - v1.1 §3/D2 防御:order_registered 对未知订单 = no-op ack(旧订单分支);
//   - v1.1 §4/D4:旧订单分支未知订单(非 checkout_ref)未找到 = 404 同形
//     掩蔽(不再 500)。

package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// checkoutCallbackStub is a minimal producer-side §2b receiver
// (/internal/v1/billing/touch-orders/activation-active): it records every
// request (path/body/app identity/token) and answers 201 by default (status
// flips via code).
type checkoutCallbackStub struct {
	ts     *httptest.Server
	code   atomic.Int32
	mu     sync.Mutex
	bodies []string
	paths  []string
	appIDs []string
	tokens []string
}

func newCheckoutCallbackStub(t *testing.T) *checkoutCallbackStub {
	t.Helper()
	st := &checkoutCallbackStub{}
	st.code.Store(http.StatusCreated)
	st.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/billing/touch-orders/checkout" {
			if r.Method == http.MethodPost {
				var req struct {
					SourceOrderRef string `json:"source_order_ref"`
					ReturnTarget   string `json:"return_target"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"checkout_ref":     "tco_" + req.SourceOrderRef,
					"out_trade_no":     "OTN_" + req.SourceOrderRef,
					"source_app":       "touch-engine",
					"source_order_ref": req.SourceOrderRef,
					"status":           "pending",
					"code_url":         "weixin://wxpay/bizpayurl?pr=" + req.SourceOrderRef,
					"return_target":    req.ReturnTarget,
					"return_url":       "https://touch.test/work/billing",
				})
				return
			}
			ref := r.URL.Query().Get("source_order_ref")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checkout_ref":     "tco_" + ref,
				"out_trade_no":     "OTN_" + ref,
				"source_app":       "touch-engine",
				"source_order_ref": ref,
				"status":           "pending",
				"code_url":         "weixin://wxpay/bizpayurl?pr=" + ref,
			})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		st.mu.Lock()
		st.bodies = append(st.bodies, string(raw))
		st.paths = append(st.paths, r.URL.Path)
		st.appIDs = append(st.appIDs, r.Header.Get("X-App-ID"))
		st.tokens = append(st.tokens, r.Header.Get("X-PilotSeaview-Internal-Token"))
		st.mu.Unlock()
		w.WriteHeader(int(st.code.Load()))
		_, _ = w.Write([]byte(`{"replayed":false}`))
	}))
	t.Cleanup(st.ts.Close)
	return st
}

func (st *checkoutCallbackStub) snapshot(t *testing.T) ([]string, []string, []string, []string) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]string, len(st.bodies))
	copy(out, st.bodies)
	paths := make([]string, len(st.paths))
	copy(paths, st.paths)
	appIDs := make([]string, len(st.appIDs))
	copy(appIDs, st.appIDs)
	tokens := make([]string, len(st.tokens))
	copy(tokens, st.tokens)
	return out, paths, appIDs, tokens
}

// callbackFixture = checkoutFixture pointed at a stub producer with a token
// (回发缝已配置)。
func callbackFixture(t *testing.T) (*fixture, *checkoutCallbackStub) {
	t.Helper()
	stub := newCheckoutCallbackStub(t)
	f := billingFixture(t, true)
	f.s.Cfg.FeatureBillingCheckout = true
	f.s.Cfg.BillingBaseURL = stub.ts.URL
	f.s.Cfg.BillingToken = "cb-secret-token"
	return f, stub
}

func TestAuthorityActivationOpeningIsNoOpAckHTTP(t *testing.T) {
	f := checkoutFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_open_1", "idem-open-1")
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 201 {
		t.Fatalf("continuation = %d %v", status, out)
	}
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	opening := func(eventID string) string {
		return fmt.Sprintf(`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":%q,"type":"activation_opening","payload":{}}`,
			f.tenA, ref, eventID)
	}
	// pending 态:opening = FYI no-op ack(200 replayed,状态不变,仍可续付)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", opening("evt-open-h1"))
	if status != 200 || out["replayed"] != true || out["state"] != "checkout_pending" {
		t.Fatalf("opening on pending = %d %v", status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if checkout["state"] != "checkout_pending" || checkout["available"] != true {
		t.Fatalf("opening must not change pending projection: %v", checkout)
	}
	// producer 的 payment_paid(接续 handoff 未提交 → 停 paid,不触发回发);
	// 随后的 opening 仍 no-op ack(producer 事实流在 paid 后恒有 opening)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-h1","type":"payment_paid","payload":{"payment_ref":"pay_h1"},"amount_minor":12345600,"currency":"CNY"}`,
		f.tenA, ref))
	if status != 201 || out["state"] != "paid" {
		t.Fatalf("payment fact = %d %v", status, out)
	}
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", opening("evt-open-h2"))
	if status != 200 || out["replayed"] != true || out["state"] != "paid" {
		t.Fatalf("opening on paid = %d %v", status, out)
	}
	// producer 权威 activation_active 仍推进 activated(v1.1 分型不变)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-active-h1","type":"activation_active","payload":{}}`,
		f.tenA, ref))
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("producer activation = %d %v", status, out)
	}
	// order_registered 带 checkout_ref = 未知类型 400 拒(v1.1 §1 不变)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-ord-h1","type":"order_registered","payload":{}}`,
		f.tenA, ref))
	if status != 400 || out["error"] != "bad_event_type" {
		t.Fatalf("order_registered with checkout_ref = %d %v", status, out)
	}
}

func TestAuthorityPaymentPaidSelfActivatesAndNotifiesProducer(t *testing.T) {
	f, stub := callbackFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cb_1", "idem-cb-1")
	// 制作任务已提交(v1.1 D3 的本地事实)。
	if _, err := f.s.St.MarkProductionHandoffSubmitted(f.tenA, handoffID, "task_cb_1"); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 201 {
		t.Fatalf("continuation = %d %v", status, out)
	}
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	payEvent := fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-cb1","type":"payment_paid","payload":{"payment_ref":"pay_cb1"},"amount_minor":12345600,"currency":"CNY"}`,
		f.tenA, ref)
	// payment_paid:落 paid → handoff 已提交 → 自主 activated。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent)
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("self-activated payment = %d %v", status, out)
	}
	// 回发到达 producer:§2b 冻结端点 + app 身份闸(X-App-ID=touch-engine +
	// app token;D5 收敛:不再打 authority-events 缝形状)。
	bodies, paths, appIDs, tokens := stub.snapshot(t)
	if len(bodies) != 1 {
		t.Fatalf("exactly one send-back expected, got %d", len(bodies))
	}
	if paths[0] != "/internal/v1/billing/touch-orders/activation-active" {
		t.Fatalf("send-back path = %q, want the frozen §2b endpoint", paths[0])
	}
	if appIDs[0] != "touch-engine" {
		t.Fatalf("send-back X-App-ID = %q, want touch-engine", appIDs[0])
	}
	if tokens[0] != "cb-secret-token" {
		t.Fatalf("send-back app token = %q, want the configured PLATFORM_BILLING_TOKEN", tokens[0])
	}
	// §2b 冻结体(DisallowUnknownFields:恰好五字段;checkout_ref 主路由键,
	// source_order_ref=原单引用回声,occurred_at=确定性回声)。
	var wire struct {
		CheckoutRef     string `json:"checkout_ref"`
		SourceOrderRef  string `json:"source_order_ref"`
		OccurredAt      string `json:"occurred_at"`
		SubscriptionRef string `json:"subscription_ref"`
		PaidUntil       string `json:"paid_until"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &wire); err != nil {
		t.Fatalf("send-back body: %v (%s)", err, bodies[0])
	}
	if wire.CheckoutRef != ref || wire.SourceOrderRef == "" || wire.OccurredAt == "" ||
		wire.SubscriptionRef != "" || wire.PaidUntil != "" {
		t.Fatalf("send-back wire wrong: %+v (raw %s)", wire, bodies[0])
	}
	// 严格五字段(producer DisallowUnknownFields:多余字段即 400 不可回发)。
	var raw map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &raw); err != nil {
		t.Fatalf("send-back body not json: %v", err)
	}
	if len(raw) != 5 {
		t.Fatalf("send-back body must carry exactly the five §2b fields, got %d: %s", len(raw), bodies[0])
	}
	// 商家读门:activated/not_awaiting_payment(本地事实落行)。
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if checkout["state"] != "activated" || checkout["reason"] != "not_awaiting_payment" {
		t.Fatalf("activated readback = %v", checkout)
	}
	// 渠道重试触发 payment_paid 重放:回发字节恒定(确定性纪律,producer
	// 按 (source,event_id) 幂等读回,绝不产生同键异负载 409)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent)
	if status != 200 || out["replayed"] != true || out["state"] != "activated" {
		t.Fatalf("payment replay = %d %v", status, out)
	}
	bodies2, _, _, _ := stub.snapshot(t)
	if len(bodies2) != 2 {
		t.Fatalf("replay must re-fire the send-back, got %d deliveries", len(bodies2))
	}
	if bodies2[1] != bodies2[0] {
		t.Fatalf("send-back must be byte-stable across re-fires:\n%s\n%s", bodies2[0], bodies2[1])
	}
}

func TestAuthorityCallbackRetryableFailureAnswers503AndHeals(t *testing.T) {
	f, stub := callbackFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cb_2", "idem-cb-2")
	if _, err := f.s.St.MarkProductionHandoffSubmitted(f.tenA, handoffID, "task_cb_2"); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	payEvent := fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-cb2","type":"payment_paid","payload":{"payment_ref":"pay_cb2"},"amount_minor":12345600,"currency":"CNY"}`,
		f.tenA, ref)
	// 回发可重试失败(producer 500):本请求答 503,producer 按既有
	// webhook FAIL→渠道重试→replay 姿态回来补投(桥式自愈)。
	stub.code.Store(500)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent)
	if status != 503 || out["error"] != "activation_callback_pending" {
		t.Fatalf("retryable callback failure = %d %v", status, out)
	}
	// 本地事实已落行(503 只是回发待重试,不回滚激活)。
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if checkout["state"] != "activated" {
		t.Fatalf("local fact must stand after 503: %v", checkout)
	}
	// producer 恢复:重放同一 payment_paid → 回发补投成功 → 200。
	stub.code.Store(201)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent)
	if status != 200 || out["replayed"] != true || out["state"] != "activated" {
		t.Fatalf("healed replay = %d %v", status, out)
	}
	bodies, _, _, _ := stub.snapshot(t)
	if len(bodies) != 2 {
		t.Fatalf("two send-back attempts expected, got %d", len(bodies))
	}
	if bodies[1] != bodies[0] {
		t.Fatalf("healed re-fire must be byte-identical:\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestAuthorityCallbackProducerUnknownRefIsLoudReleaseNotPending(t *testing.T) {
	// D5 收敛后的 404 语义:producer 照实报告未知 checkout_ref(§2b 冻结错误
	// 字典 touch_checkout_not_found)= 4xx 违例类,重试不可修复——大声留痕后
	// 放行本请求(本地事实照落),绝不伪装成 503 activation_callback_pending
	// 把已消费的支付事实拖进无限 FAIL。
	f, stub := callbackFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cb_404", "idem-cb-404")
	if _, err := f.s.St.MarkProductionHandoffSubmitted(f.tenA, handoffID, "task_cb_404"); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	stub.code.Store(http.StatusNotFound)
	payEvent := fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-cb404","type":"payment_paid","payload":{"payment_ref":"pay_cb404"},"amount_minor":12345600,"currency":"CNY"}`,
		f.tenA, ref)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent)
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("404 send-back must release the request loudly = %d %v", status, out)
	}
	// 重放(渠道重试姿态)同形放行:违例不进入重试通道,无退避风暴。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", payEvent)
	if status != 200 || out["replayed"] != true || out["state"] != "activated" {
		t.Fatalf("404 replay must stay released = %d %v", status, out)
	}
	// 两次回发都打在 §2b 端点上(producer 404 是对正确端点的业务应答
	// touch_checkout_not_found,不是路由 404;字节恒定纪律照旧)。
	bodies, paths, _, _ := stub.snapshot(t)
	if len(bodies) != 2 {
		t.Fatalf("two send-backs expected, got %d", len(bodies))
	}
	for i, p := range paths {
		if p != "/internal/v1/billing/touch-orders/activation-active" {
			t.Fatalf("send-back #%d path = %q", i, p)
		}
	}
	if bodies[1] != bodies[0] {
		t.Fatalf("send-back must stay byte-stable across re-fires:\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestAuthorityCallbackUnconfiguredKeepsLocalFact(t *testing.T) {
	// 先通过已接通的 seam 建立原单引用,随后撤销 BillingToken 模拟回发缝未配置:
	// 诚实跳过回发,本地事实照落。
	f := checkoutFixture(t)
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cb_3", "idem-cb-3")
	if _, err := f.s.St.MarkProductionHandoffSubmitted(f.tenA, handoffID, "task_cb_3"); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	if status != 201 {
		t.Fatalf("continuation = %d %v", status, out)
	}
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	f.s.Cfg.FeatureBillingCheckout = false // 关闭出站能力(回发缝未配置)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-cb3","type":"payment_paid","payload":{"payment_ref":"pay_cb3"},"amount_minor":12345600,"currency":"CNY"}`,
		f.tenA, ref))
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("unconfigured callback = %d %v", status, out)
	}
	status, out = f.do2(t, "GET", path, "sess-owner-a", f.tenA, "")
	checkout, _ := out["checkout"].(map[string]any)
	if checkout["state"] != "activated" {
		t.Fatalf("readback = %v", checkout)
	}
	if ref == "" {
		t.Fatal("ref must be set")
	}
}

func TestAuthorityPaymentPaidWithoutCommittedTaskStaysPaid(t *testing.T) {
	f, stub := callbackFixture(t)
	// handoff 停在 recorded(任务未提交):本地事实不构成「制作工程确认开动」。
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cb_4", "idem-cb-4")
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-paid-cb4","type":"payment_paid","payload":{"payment_ref":"pay_cb4"},"amount_minor":12345600,"currency":"CNY"}`,
		f.tenA, ref))
	if status != 201 || out["state"] != "paid" {
		t.Fatalf("no-task payment = %d %v", status, out)
	}
	bodies, _, _, _ := stub.snapshot(t)
	if len(bodies) != 0 {
		t.Fatalf("no send-back without a committed task, got %d", len(bodies))
	}
	// producer 自身 activation_active 仍可推进(v1.1 权威路径不变)。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"checkout_ref":%q,"source":"pai-billing","event_id":"evt-active-cb4","type":"activation_active","payload":{}}`,
		f.tenA, ref))
	if status != 201 || out["state"] != "activated" {
		t.Fatalf("producer activation = %d %v", status, out)
	}
}

func TestAuthorityUnknownOrderOldBranchMasking(t *testing.T) {
	// v1.1 D4:旧订单分支未知订单(非 checkout_ref)未找到 = 404 同形掩蔽,
	// 不再透传 500;producer 侧据此把「wiring 违例」大声失败而非无限重试。
	f := checkoutFixture(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"payment_paid", fmt.Sprintf(`{"tenant_id":%q,"order_id":"cph_unknown","source":"pai-billing","event_id":"e-unk-1","type":"payment_paid","payload":{"payment_ref":"p_unk"},"amount_minor":1,"currency":"CNY"}`, f.tenA)},
		{"activation_opening", fmt.Sprintf(`{"tenant_id":%q,"order_id":"cph_unknown","source":"pai-billing","event_id":"e-unk-2","type":"activation_opening","payload":{}}`, f.tenA)},
		{"activation_active", fmt.Sprintf(`{"tenant_id":%q,"order_id":"cph_unknown","source":"pai-billing","event_id":"e-unk-3","type":"activation_active","payload":{}}`, f.tenA)},
		{"order_cancelled", fmt.Sprintf(`{"tenant_id":%q,"order_id":"cph_unknown","source":"pai-billing","event_id":"e-unk-4","type":"order_cancelled","payload":{}}`, f.tenA)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, out := f.doService(t, "POST", "/api/v1/billing/authority/events", tc.body)
			if status != 404 || out["error"] != "not_found" {
				t.Fatalf("unknown order %s = %d %v, want 404 not_found", tc.name, status, out)
			}
		})
	}
	// v1.1 D2 防御:order_registered 对未知订单 = no-op ack(mint/replay 不被
	// 该事实误判失败;producer 侧已裁定不再发送,此为防御性保留)。
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":"cph_unknown","source":"pai-billing","event_id":"e-unk-5","type":"order_registered","payload":{"order_ref":"pb-ord-1"}}`, f.tenA))
	if status != 200 || out["replayed"] != true || out["ignored"] != "untracked_order" {
		t.Fatalf("order_registered no-op ack = %d %v", status, out)
	}
	// 已知订单的 order_registered 仍正常绑定(旧订单机零回归)。
	seedCatalog(t, f)
	status, out = f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"d2-known-order"}`)
	if status != 201 {
		t.Fatal(status, out)
	}
	id := out["order"].(map[string]any)["id"].(string)
	knownEvent := fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"pai-billing","event_id":"e-known-1","type":"order_registered","occurred_at":"2026-11-16T08:00:00Z","payload":{"order_ref":"pb-ord-known"}}`, f.tenA, id)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", knownEvent)
	if status != 200 && status != 201 {
		t.Fatalf("known-order order_registered = %d %v", status, out)
	}
	// 重放同一事件(同 occurred_at,producer 确定性纪律):幂等读回。
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", knownEvent)
	if status != 200 || out["replayed"] != true {
		t.Fatalf("known-order replay = %d %v", status, out)
	}
}
