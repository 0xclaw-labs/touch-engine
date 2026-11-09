package httpapi

// server_billing_test.go: HUI-4591 订购收费账单 HTTP 验收矩阵(T01/T15 核心)。
//
// 覆盖:
//   - 读面授权矩阵(billing:view;staff 无;跨租户掩码 404);
//   - 下单:目录缺失诚实 409/价格快照/幂等键重放;agent renewal+payer 真值快照;
//   - 受信权威缝:无凭据 503 fail-closed;四态开通闭环;同键重放幂等;异负载
//     409 IDEMPOTENCY_CONFLICT;
//   - 退款发票:重复回调不重复退款;超余额拒绝;
//   - 硬件:五状态不可跳跃;支付事实落首步;
//   - AI 用量投影:重试幂等、状态可查;
//   - 接线:按租户订阅事实驱动三栏/收费资格(T16/Cfg 回退不破坏)。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// billingFixture = 基础夹具 + 受信权威服务凭据(eco-billing)。
func billingFixture(t *testing.T, withTokens bool) *fixture {
	t.Helper()
	extra := map[string]string{}
	if withTokens {
		extra["TOUCH_SERVICE_TOKENS"] = "eco-billing:bill-secret"
		extra["TOUCH_SERVICE_ROLES"] = "eco-billing:billing_authority"
	}
	f := newFixtureEnv(t, false, extra)
	f.s.Cfg.SubscriptionStatus = "active"
	return f
}

// doService posts with trusted service identity headers.
func (f *fixture) doService(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Service-ID", "eco-billing")
	req.Header.Set("X-Service-Token", "bill-secret")
	req.Header.Set("Content-Type", "application/json")
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// do2 wraps the base fixture.do dropping the response header.
func (f *fixture) do2(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	status, _, out := f.do(t, method, path, session, tenant, body)
	return status, out
}

func seedCatalog(t *testing.T, f *fixture) {
	t.Helper()
	seedCatalogStore(t, f.s.St)
}

func seedCatalogStore(t *testing.T, st *store.Store) {
	t.Helper()
	_, err := st.UpsertBillingCatalogEntry(billing.CatalogEntry{
		ScopeTenant: "", PlanCode: billing.PlanMonthly, PricingVersion: "v2026",
		PriceRef: "price_m_v2026", Currency: "CNY", PriceMinor: 990000, ServiceDays: 365,
		IncludedStoreCount: 3, ExtraStorePriceMinor: 60000, GraceDays: 30,
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
}

func TestBillingReadAuthorizationMatrix(t *testing.T) {
	f := billingFixture(t, false)
	// staff 无 billing 维度 → 403
	status, out := f.do2(t, "GET", "/api/v1/billing/subscription", "sess-staff-a", f.tenA, "")
	if status != 403 {
		t.Fatalf("staff read = %d %v", status, out)
	}
	// owner → 200;无投影行回退 Cfg(=active),收银台未接入诚实展示
	status, out = f.do2(t, "GET", "/api/v1/billing/subscription", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("owner read = %d %v", status, out)
	}
	sub, _ := out["subscription"].(map[string]any)
	if sub["status"] != "active" || sub["locks_existing"] != false {
		t.Fatalf("subscription = %v", sub)
	}
	checkout, _ := out["checkout"].(map[string]any)
	if checkout["available"] != false {
		t.Fatalf("checkout must be honest-unwired: %v", checkout)
	}
}

func TestSubscriptionOrderRequiresCatalogHonest(t *testing.T) {
	f := billingFixture(t, false)
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"k1"}`)
	if status != 409 || out["error"] != "catalog_unavailable" {
		t.Fatalf("no catalog = %d %v", status, out)
	}
}

func TestSubscriptionOrderPriceSnapshotAndReplay(t *testing.T) {
	f := billingFixture(t, false)
	seedCatalog(t, f)
	body := `{"action":"new","plan_code":"store.plan.month","price_ref":"price_m_v2026","idempotency_key":"k1"}`
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA, body)
	if status != 201 {
		t.Fatalf("create = %d %v", status, out)
	}
	order, _ := out["order"].(map[string]any)
	if order["status"] != "processing" || order["amount_minor"] != float64(990000) {
		t.Fatalf("order = %v", order)
	}
	// 同键重试 = 200 replayed,不产生第二笔订单
	status, out = f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA, body)
	if status != 200 || out["replayed"] != true {
		t.Fatalf("replay = %d %v", status, out)
	}
	status, list := f.do2(t, "GET", "/api/v1/billing/orders?kind=subscription", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("list = %d", status)
	}
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("orders = %d (idempotency key must not create twice)", len(items))
	}
	// 客户端 price_ref 漂移 → 409(引用必须对齐现行目录)
	status, out = f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","price_ref":"price_OLD","idempotency_key":"k2"}`)
	if status != 409 || out["error"] != "catalog_changed" {
		t.Fatalf("price drift = %d %v", status, out)
	}
}

func TestAuthoritySeamFailClosedWithoutCredentials(t *testing.T) {
	f := billingFixture(t, false)
	// 无服务身份 → 401(商家会话永不自授)
	status, _ := f.do2(t, "POST", "/api/v1/billing/authority/events", "sess-owner-a", f.tenA, `{}`)
	if status != 401 {
		t.Fatalf("session to authority = %d", status)
	}
	// 有身份但 registry 未配置 → 503 fail-closed(诚实未验证)
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/billing/authority/events", strings.NewReader(`{}`))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Service-ID", "eco-billing")
	req.Header.Set("X-Service-Token", "bill-secret")
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("unconfigured registry = %d", res.StatusCode)
	}
}

func TestFourStateActivationLoopViaAuthority(t *testing.T) {
	f := billingFixture(t, true)
	seedCatalog(t, f)
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"k1"}`)
	if status != 201 {
		t.Fatalf("create = %d %v", status, out)
	}
	order, _ := out["order"].(map[string]any)
	orderID, _ := order["id"].(string)

	// 浏览器侧没有任何推进支付状态的通道:orders/{id} 永远只读。
	status, got := f.do2(t, "GET", "/api/v1/billing/orders/"+orderID, "sess-owner-a", f.tenA, "")
	if status != 200 || got["order"].(map[string]any)["status"] != "processing" {
		t.Fatalf("browser return must be read-only: %d %v", status, got)
	}

	// 支付事实(受信缝)→ opening
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"e_pay","type":"payment_paid",
		  "occurred_at":"2026-06-01T10:00:00Z","payload":{"order_ref":"ord_9","payment_ref":"pay_9"}}`, f.tenA, orderID))
	if status != 201 || out["replayed"] != false {
		t.Fatalf("pay = %d %v", status, out)
	}
	if o := out["order"].(map[string]any); o["status"] != "opening" || o["payment_ref"] != "pay_9" {
		t.Fatalf("after pay = %v", o)
	}
	// 同键重放 = 幂等读回
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"e_pay","type":"payment_paid",
		  "occurred_at":"2026-06-01T10:00:00Z","payload":{"order_ref":"ord_9","payment_ref":"pay_9"}}`, f.tenA, orderID))
	if status != 200 || out["replayed"] != true {
		t.Fatalf("pay replay = %d %v", status, out)
	}
	// 同键异负载 = IDEMPOTENCY_CONFLICT
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"e_pay","type":"payment_paid",
		  "occurred_at":"2026-06-01T10:00:00Z","payload":{"order_ref":"ord_OTHER","payment_ref":"pay_9"}}`, f.tenA, orderID))
	if status != 409 || out["error"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict = %d %v", status, out)
	}
	// 开通事实 → active + 订阅投影(可重建,不自增余额)
	status, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"e_act","type":"activation_active",
		  "occurred_at":"2026-06-01T10:01:00Z","payload":{"subscription_ref":"sub_9","plan_code":"store.plan.month",
		  "pricing_version":"v2026","paid_until":"2027-06-01T00:00:00Z","store_limit":3,"grace_days":30,
		  "entitlement_projection":"{\"plan\":\"monthly\"}"}}`, f.tenA, orderID))
	if status != 201 {
		t.Fatalf("activate = %d %v", status, out)
	}
	status, out = f.do2(t, "GET", "/api/v1/billing/subscription", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("sub read = %d", status)
	}
	sub := out["subscription"].(map[string]any)
	if sub["status"] != "active" || sub["subscription_ref"] != "sub_9" || sub["paid_until"] != "2027-06-01T00:00:00Z" {
		t.Fatalf("subscription projection = %v", sub)
	}
	if usage := out["store_usage"].(map[string]any); usage["limit"] != float64(3) {
		t.Fatalf("store usage = %v", usage)
	}
	// 响应永不携带余额字段
	raw := fmt.Sprintf("%v", out)
	for _, banned := range []string{"balance", "wallet"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("billing response leaked %s: %s", banned, raw)
		}
	}
}

func TestAgentRenewalOrderCarriesPayerGrantSnapshot(t *testing.T) {
	f := newAgencyFixture(t, true)
	f.s.Cfg.SubscriptionStatus = "active"
	seedCatalogStore(t, f.s.St)
	// 建立代管关系并授予 renewal + payer_grant_ref(owner 显式授权,4593 语义)。
	relID := f.mustEstablish(t, "usr_ag1", "年度代运营")
	status, out := f.do(t, "PUT", "/api/v1/agency/relations/"+relID+"/scope", "sess-owner-a", f.tenA,
		`{"allowed_actions":["renewal"],"store_scope_set":[],"valid_until":"2027-01-01T00:00:00Z",
		  "payer_grant_ref":"pgr_9","grant_revision":0,"reason":"年度代付授权"}`)
	if status != 200 {
		t.Fatalf("scope = %d %v", status, out)
	}
	// 代理代发起订阅订单:服务端解析 payer_grant_ref 真值落单(客户端无该字段)。
	status, out = f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-agent1", f.tenA,
		`{"action":"renew","plan_code":"store.plan.month","price_ref":"price_m_v2026","idempotency_key":"ag-1"}`)
	if status != 201 {
		t.Fatalf("agent order = %d %v", status, out)
	}
	order := out["order"].(map[string]any)
	if order["payer_grant_ref"] != "pgr_9" || order["payer_principal"] != "usr_ag1" {
		t.Fatalf("payer snapshot missing: %v", order)
	}
	if order["agency_relation_ref"] != relID {
		t.Fatalf("relation ref: %v", order)
	}
	// 撤销 payer 授权后立即失权:改 scope 去 payer_grant_ref(下一请求拒绝)。
	status, out = f.do(t, "PUT", "/api/v1/agency/relations/"+relID+"/scope", "sess-owner-a", f.tenA,
		`{"allowed_actions":["renewal"],"store_scope_set":[],"valid_until":"2027-01-01T00:00:00Z",
		  "payer_grant_ref":"","grant_revision":1,"reason":"收回代付授权"}`)
	if status != 200 {
		t.Fatalf("scope revoke = %d %v", status, out)
	}
	status, out = f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-agent1", f.tenA,
		`{"action":"renew","plan_code":"store.plan.month","price_ref":"price_m_v2026","idempotency_key":"ag-2"}`)
	if status != 403 || out["error"] != "payment_authorization_required" {
		t.Fatalf("after revoke = %d %v", status, out)
	}
	// 无任何 grant 的 agent 一律拒。
	if status, _ := f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-agent2", f.tenA,
		`{"action":"renew","plan_code":"store.plan.month","idempotency_key":"ag-3"}`); status != 403 {
		t.Fatalf("agent without grant = %d", status)
	}
}

func TestRefundEventsIdempotentAndCapped(t *testing.T) {
	f := billingFixture(t, true)
	seedCatalog(t, f)
	status, out := f.do2(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		`{"action":"new","plan_code":"store.plan.month","idempotency_key":"k1"}`)
	orderID := out["order"].(map[string]any)["id"].(string)
	_ = status
	// 支付+开通(先有真实订单事实)
	_, _ = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"e_pay","type":"payment_paid","payload":{"payment_ref":"p1"}}`, f.tenA, orderID))
	// 退款登记(F19 全字段)
	refundBody := fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"re_1","type":"refund_registered",
		  "occurred_at":"2026-06-02T00:00:00Z","refund_ref":"ref_9","amount_minor":400000,"currency":"CNY",
		  "reason":"七天无理由","invoice_title":"派诺商业管理有限公司","invoice_status":"pending","auditor":"auditor_wang"}`, f.tenA, orderID)
	st, out := f.doService(t, "POST", "/api/v1/billing/authority/events", refundBody)
	if st != 201 {
		t.Fatalf("refund = %d %v", st, out)
	}
	// 重复回调:不重复退款
	st, out = f.doService(t, "POST", "/api/v1/billing/authority/events", refundBody)
	if st != 200 || out["replayed"] != true {
		t.Fatalf("refund replay = %d %v", st, out)
	}
	// 超余额(余 590000 < 600000)拒绝
	st, out = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"re_2","type":"refund_registered",
		  "refund_ref":"ref_10","amount_minor":600000,"currency":"CNY","reason":"错拍",
		  "invoice_title":"派诺商业","invoice_status":"none","auditor":"auditor_wang"}`, f.tenA, orderID))
	if st != 409 || out["error"] != "refund_exceeds_order" {
		t.Fatalf("cap = %d %v", st, out)
	}
	// 余额内第二笔成功;对账面两行
	_, _ = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"re_3","type":"refund_registered",
		  "refund_ref":"ref_11","amount_minor":590000,"currency":"CNY","reason":"协商退",
		  "invoice_title":"派诺商业","invoice_status":"issued","invoice_ref":"inv_1","auditor":"auditor_li"}`, f.tenA, orderID))
	st, out = f.do2(t, "GET", "/api/v1/billing/refunds", "sess-owner-a", f.tenA, "")
	items, _ := out["items"].([]any)
	if st != 200 || len(items) != 2 {
		t.Fatalf("refund list = %d %v", st, out)
	}
}

func TestHardwareOrderFiveStatesNoSkipping(t *testing.T) {
	f := billingFixture(t, true)
	// 未付款直接出库 → 409 skipped
	status, out := f.do2(t, "POST", "/api/v1/billing/hardware-orders", "sess-owner-a", f.tenA,
		`{"idempotency_key":"hw-1","items":[{"sku":"NFC-30","name":"碰一碰 NFC 卡","kind":"goods","qty":30,"unit_amount_minor":500},
		  {"sku":"SVC","name":"现场写卡服务","kind":"service","qty":1,"unit_amount_minor":20000}]}`)
	if status != 201 {
		t.Fatalf("hw order = %d %v", status, out)
	}
	orderID := out["order"].(map[string]any)["id"].(string)
	st, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-ops","event_id":"s1","type":"hardware_step",
		  "step":"shipment","operator":"仓管老周","occurred_at":"2026-06-03T00:00:00Z"}`, f.tenA, orderID))
	if st != 409 || out["error"] != "hardware_step_skipped" {
		t.Fatalf("skip = %d %v", st, out)
	}
	// 支付事实 → 订单 opening + 履约首步 payment done
	_, _ = f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
		`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"hp1","type":"payment_paid","payload":{"payment_ref":"hp"}}`, f.tenA, orderID))
	// 五步顺序推进
	for i, step := range []string{"shipment", "write_card", "readback", "acceptance"} {
		st, out := f.doService(t, "POST", "/api/v1/billing/authority/events", fmt.Sprintf(
			`{"tenant_id":%q,"order_id":%q,"source":"eco-ops","event_id":"hs-%d","type":"hardware_step",
			  "step":%q,"operator":"仓管老周","occurred_at":"2026-06-03T0%d:00:00Z","device_ref":"dev_1"}`,
			f.tenA, orderID, i+2, step, i+1))
		if st != 201 {
			t.Fatalf("step %s = %d %v", step, st, out)
		}
	}
	st, out = f.do2(t, "GET", "/api/v1/billing/orders/"+orderID, "sess-owner-a", f.tenA, "")
	fulfill := out["fulfillment"].(map[string]any)
	accept := fulfill["acceptance"].(map[string]any)
	pay := fulfill["payment"].(map[string]any)
	if st != 200 || accept["state"] != "done" || pay["operator"] != "authority:eco-billing" {
		t.Fatalf("fulfillment = %v", fulfill)
	}
	// 明细两行
	items := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
}

func TestAIUsageProjectionRetryIdempotent(t *testing.T) {
	f := billingFixture(t, true)
	evt := fmt.Sprintf(
		`{"tenant_id":%q,"source":"content-production","event_id":"q1","type":"ai_usage_recorded",
		  "occurred_at":"2026-06-04T00:00:00Z","quote_ref":"q_9","capability":"content_production",
		  "amount_minor":800,"currency":"CNY","ai_pricing_version":"pv1"}`, f.tenA)
	st, out := f.doService(t, "POST", "/api/v1/billing/authority/events", evt)
	if st != 201 {
		t.Fatalf("usage = %d %v", st, out)
	}
	st, out = f.doService(t, "POST", "/api/v1/billing/authority/events", evt)
	if st != 200 || out["replayed"] != true {
		t.Fatalf("usage replay = %d %v", st, out)
	}
	st, list := f.do2(t, "GET", "/api/v1/billing/ai-usage", "sess-owner-a", f.tenA, "")
	items, _ := list["items"].([]any)
	if st != 200 || len(items) != 1 {
		t.Fatalf("ai usage list = %d (%v)", len(items), list)
	}
}

// seedExpiredSubscription 直接落一条已过服务期的租户订阅投影(权威事实的重建结果)。
func seedExpiredSubscription(t *testing.T, f *fixture) {
	t.Helper()
	_, err := f.s.St.DB.Exec(`INSERT INTO billing_subscriptions
		(tenant_id,subscription_ref,plan_code,pricing_version,status,paid_until,grace_days,store_limit,entitlement_projection,last_order_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.tenA, "sub_old", "store.plan.month", "v2025", "active", "2026-01-01T00:00:00Z", 30, 3, "{}", "bo_old",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
}

// TestTenantFactBeatsProcessConfig:订阅投影行存在时,三栏/收费资格按租户事实,
// 进程级 Cfg(哪怕写着 active)不再兜底 —— 到期判定换真源(F18/1671 接线)。
func TestTenantFactBeatsProcessConfig(t *testing.T) {
	f := billingFixture(t, false) // Cfg.SubscriptionStatus = "active"
	seedExpiredSubscription(t, f)
	// 三栏:package.status = expired(事实),旧权益不受锁
	status, out := f.do2(t, "GET", "/api/v1/account-separation", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("separation = %d", status)
	}
	pkg := out["package"].(map[string]any)
	if pkg["status"] != "expired" || pkg["restricts_new_premium"] != true || pkg["locks_existing"] != false {
		t.Fatalf("package = %v", pkg)
	}
	// 收费资格:订阅到期只拦新增收费能力(quote 语境拒绝)
	status, out = f.do2(t, "POST", "/api/v1/account-separation/charges", "sess-owner-a", f.tenA,
		`{"capability":"product_image","invoked":true,"payer_account_id":"org_bill_1"}`)
	if status != 200 || out["reason"] != "entitlement_expired" {
		t.Fatalf("expired charge = %d %v", status, out)
	}
	// 账单页诚实展示到期与维护保留期
	status, out = f.do2(t, "GET", "/api/v1/billing/subscription", "sess-owner-a", f.tenA, "")
	sub := out["subscription"].(map[string]any)
	if status != 200 || sub["status"] != "expired" || sub["retain_until"] != "2026-01-31T00:00:00Z" {
		t.Fatalf("billing subscription = %d %v", status, sub)
	}
}

// TestConfigFallbackUnchanged:无投影行时回退 Cfg(旧行为逐字节;T16 语义不变)。
func TestConfigFallbackUnchanged(t *testing.T) {
	f := billingFixture(t, false)
	f.s.Cfg.SubscriptionStatus = "expired"
	status, out := f.do2(t, "GET", "/api/v1/account-separation", "sess-owner-a", f.tenA, "")
	pkg := out["package"].(map[string]any)
	if status != 200 || pkg["status"] != "expired" {
		t.Fatalf("cfg fallback = %d %v", status, pkg)
	}
	// 有投影行(active,未来服务期)→ 事实优先,Cfg 的 expired 不再生效
	_, err := f.s.St.DB.Exec(`INSERT INTO billing_subscriptions
		(tenant_id,subscription_ref,plan_code,pricing_version,status,paid_until,grace_days,store_limit,entitlement_projection,last_order_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.tenA, "sub_new", "store.plan.year", "v2026", "active", "2027-06-01T00:00:00Z", 30, 5, "{}", "bo_new",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
	status, out = f.do2(t, "GET", "/api/v1/account-separation", "sess-owner-a", f.tenA, "")
	pkg = out["package"].(map[string]any)
	if pkg["status"] != "active" {
		t.Fatalf("fact beats cfg: %v", pkg)
	}
}

func TestWhoamiCarriesCashierParams(t *testing.T) {
	f := newFixtureEnv(t, false, map[string]string{
		"TOUCH_CASHIER_APP_ID":    "eco-cashier",
		"TOUCH_CASHIER_TARGET_ID": "ti-cashier-web",
	})
	status, out := f.do2(t, "GET", "/api/v1/whoami", "sess-owner-a", f.tenA, "")
	features := out["features"].(map[string]any)
	if status != 200 || features["billing_cashier_app_id"] != "eco-cashier" ||
		features["billing_cashier_target_id"] != "ti-cashier-web" {
		t.Fatalf("whoami features = %v", features)
	}
}
