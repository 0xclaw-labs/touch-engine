// server_billing_checkout_activation_callback_test.go: 统一契约 v1.1(ADV-9
// FIX-B)touch 侧验收矩阵——
//
//   - v1.1 §1:activation_opening 携带 checkout_ref = FYI no-op ack(HTTP 面,
//     状态零改写);order_registered 带 checkout_ref 仍 400 未知类型拒;
//   - v1.1 §2/D3:payment_paid 落 paid + handoff 已提交任务 → 自主 activated
//     + activation_active 回发 producer(authority-events 缝形状,
//     source=touch-engine,event_id 确定性:重放/重试字节恒等);
//     回发可重试失败 → 503(渠道重试→重放→再触发自愈);缝未配置 → 本地
//     事实照落、诚实跳过;handoff 未提交 → 保持 paid(不伪造本地事实);
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

// checkoutCallbackStub is a minimal producer-side authority-events receiver:
// it records every request and answers 201 by default (status flips via code).
type checkoutCallbackStub struct {
	ts     *httptest.Server
	code   atomic.Int32
	mu     sync.Mutex
	bodies []string
	paths  []string
	svcIDs []string
}

func newCheckoutCallbackStub(t *testing.T) *checkoutCallbackStub {
	t.Helper()
	st := &checkoutCallbackStub{}
	st.code.Store(http.StatusCreated)
	st.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		st.mu.Lock()
		st.bodies = append(st.bodies, string(raw))
		st.paths = append(st.paths, r.URL.Path)
		st.svcIDs = append(st.svcIDs, r.Header.Get("X-Service-ID"))
		st.mu.Unlock()
		w.WriteHeader(int(st.code.Load()))
		_, _ = w.Write([]byte(`{"replayed":false}`))
	}))
	t.Cleanup(st.ts.Close)
	return st
}

func (st *checkoutCallbackStub) snapshot(t *testing.T) ([]string, []string, []string) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]string, len(st.bodies))
	copy(out, st.bodies)
	paths := make([]string, len(st.paths))
	copy(paths, st.paths)
	ids := make([]string, len(st.svcIDs))
	copy(ids, st.svcIDs)
	return out, paths, ids
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
	// 回发到达 producer:authority-events 缝形状,source=touch-engine。
	bodies, paths, ids := stub.snapshot(t)
	if len(bodies) != 1 {
		t.Fatalf("exactly one send-back expected, got %d", len(bodies))
	}
	if paths[0] != "/api/v1/billing/authority/events" {
		t.Fatalf("send-back path = %q", paths[0])
	}
	if ids[0] != "touch-engine" {
		t.Fatalf("send-back X-Service-ID = %q, want touch-engine", ids[0])
	}
	var wire struct {
		Source      string `json:"source"`
		EventID     string `json:"event_id"`
		Type        string `json:"type"`
		CheckoutRef string `json:"checkout_ref"`
		TenantID    string `json:"tenant_id"`
		Payload     struct {
			PaymentRef string `json:"payment_ref"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &wire); err != nil {
		t.Fatalf("send-back body: %v (%s)", err, bodies[0])
	}
	if wire.Source != "touch-engine" || wire.Type != "activation_active" ||
		wire.EventID != "touch-act-"+ref || wire.CheckoutRef != ref ||
		wire.TenantID != f.tenA || wire.Payload.PaymentRef != "pay_cb1" {
		t.Fatalf("send-back wire wrong: %+v", wire)
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
	bodies2, _, _ := stub.snapshot(t)
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
	bodies, _, _ := stub.snapshot(t)
	if len(bodies) != 2 {
		t.Fatalf("two send-back attempts expected, got %d", len(bodies))
	}
	if bodies[1] != bodies[0] {
		t.Fatalf("healed re-fire must be byte-identical:\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestAuthorityCallbackUnconfiguredKeepsLocalFact(t *testing.T) {
	// BillingBaseURL 在场但 token 缺席 = 回发缝未配置:诚实跳过,本地事实照落。
	f := checkoutFixture(t) // BillingBaseURL=https://billing.test,无 BillingToken
	storeID, handoffID := seedContinuationHandoff(t, f, "q_cb_3", "idem-cb-3")
	if _, err := f.s.St.MarkProductionHandoffSubmitted(f.tenA, handoffID, "task_cb_3"); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}
	path := "/api/v1/stores/" + storeID + "/content-production-handoffs/" + handoffID + "/checkout"
	status, out := f.do2(t, "POST", path, "sess-owner-a", f.tenA, "")
	ref := out["checkout"].(map[string]any)["checkout_ref"].(string)
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
	bodies, _, _ := stub.snapshot(t)
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
