package httpapi

// HUI-1663 实体交付 HTTP 验收矩阵(真 sqlite + stub identity + 受信权威缝):
//   - 完整旅程:创建(付款事实前置)→出库→绑定→写卡→回读→验收→交付;
//     4591 订单投影经受信缝推进,四步事实同事务接线;
//   - 权限矩阵:游客 401/staff 403/门店经理本店/他店与跨租户 404 掩码;
//   - 事件纪律:幂等重放 replayed 零推进/同键异负载 IDEMPOTENCY_CONFLICT/
//     跳步 409/replaced 词表 400/回读不一致 matches=false 状态不推进;
//   - stale:换绑后 binding_stale=true(不得沿用旧「已验证」标识);
//   - 补换:recoverable 同触点换载体;lost 撤销旧入口(公共解析停用)+新触点。

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// dlvFixture = tpFixture + 受信权威缝凭据(付款事实经真实 authority 缝推进)。
func dlvFixture(t *testing.T) *tpFixture {
	return newTPFixture(t, map[string]string{
		"TOUCH_SERVICE_TOKENS": "eco-billing:bill-secret",
		"TOUCH_SERVICE_ROLES":  "eco-billing:billing_authority",
	})
}

func dlvSeedOrder(t *testing.T, f *tpFixture, key string) string {
	t.Helper()
	status, out := f.do2(t, "POST", "/api/v1/billing/hardware-orders", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"idempotency_key":%q,"items":[{"sku":"SKU-CARD","name":"碰一碰桌贴","kind":"goods","qty":1,"unit_amount_minor":4900}]}`, key))
	mustEqual(t, status, http.StatusCreated)
	return out["order"].(map[string]any)["id"].(string)
}

func dlvPayOrder(t *testing.T, f *tpFixture, orderID string) {
	t.Helper()
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events",
		fmt.Sprintf(`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"pay-%s",
			"type":"payment_paid","occurred_at":%q}`,
			f.tenA, orderID, orderID, time.Now().UTC().Format(time.RFC3339)))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("pay order: %d %v", status, out)
	}
}

func dlvCreateDelivery(t *testing.T, f *tpFixture, tpID, orderID string) map[string]any {
	t.Helper()
	status, _, out := f.do(t, "POST", "/api/v1/touchpoint-deliveries", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"touchpoint_id":%q,"hardware_order_id":%q,"physical_unit_ref":"unit-1","tag_batch":"b1"}`, tpID, orderID))
	mustEqual(t, status, http.StatusCreated)
	return out
}

func dlvEvent(t *testing.T, f *tpFixture, dlvID, eventType, eventID, body string) (int, map[string]any) {
	t.Helper()
	return f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/events", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"event_type":%q,"event_id":%q,"device_ref":"dev-1"%s}`, eventType, eventID, body))
}

func TestTouchpointDeliveryHTTPFullJourney(t *testing.T) {
	f := dlvFixture(t)
	_, _, _, _, _, _, tp1, _ := tpSeedScene(t, f)
	// 回读核对的是触点别名码(/t/{alias}),不是 campaign 短码。
	status, tpv := f.do2(t, "GET", "/api/v1/touchpoints/"+tp1, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	code1 := tpv["code"].(string)
	orderID := dlvSeedOrder(t, f, "ord-1")
	dlvPayOrder(t, f, orderID)
	d := dlvCreateDelivery(t, f, tp1, orderID)
	dlvID := d["id"].(string)

	// 未付款订单不可建交付(不可跳跃首步)。
	order2 := dlvSeedOrder(t, f, "ord-2")
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"touchpoint_id":%q,"hardware_order_id":%q}`, tp1, order2))
	mustEqual(t, status, http.StatusConflict)

	// 事件缺 event_id = 400(幂等键必填)。
	status, _ = dlvEvent(t, f, dlvID, "dispatched", "", ``)
	mustEqual(t, status, http.StatusBadRequest)

	// 出库 → 绑定 → 写卡 → 回读(逐字符一致)→ 验收 → 交付。
	status, out := dlvEvent(t, f, dlvID, "dispatched", "j1", ``)
	mustEqual(t, status, http.StatusCreated)
	status, out = dlvEvent(t, f, dlvID, "bound", "j2", ``)
	mustEqual(t, status, http.StatusCreated)
	if rev, ok := out["delivery"].(map[string]any)["bound_binding_revision"].(float64); !ok || rev != 1 {
		t.Fatalf("bound revision = %v", out)
	}
	status, _ = dlvEvent(t, f, dlvID, "written", "j3", `,"evidence_ref":"ev-write-1"`)
	mustEqual(t, status, http.StatusCreated)

	// 回读:错误域名 = matches=false,状态不推进(事实已记录)。
	status, out = dlvEvent(t, f, dlvID, "readback", "j4-bad",
		fmt.Sprintf(`,"read_url":"https://evil.example.com/t/%s","note":"现场抄录"`, code1))
	mustEqual(t, status, http.StatusOK)
	if out["readback_matches"] != false || out["advanced"] != false {
		t.Fatalf("bad-host readback = %v", out)
	}
	// 正确回读。
	status, out = dlvEvent(t, f, dlvID, "readback", "j4",
		fmt.Sprintf(`,"read_url":"https://h5.test/t/%s"`, code1))
	mustEqual(t, status, http.StatusCreated)
	if out["readback_matches"] != true || out["advanced"] != true {
		t.Fatalf("good readback = %v", out)
	}
	status, _ = dlvEvent(t, f, dlvID, "accepted", "j5", ``)
	mustEqual(t, status, http.StatusCreated)
	status, out = dlvEvent(t, f, dlvID, "delivered", "j6", ``)
	mustEqual(t, status, http.StatusCreated)
	if got := out["delivery"].(map[string]any)["status"]; got != "delivered" {
		t.Fatalf("final status = %v", got)
	}

	// GET 详情:七步事实/付款投影/回读摘要齐备。
	status, out = f.do2(t, "GET", "/api/v1/touchpoint-deliveries/"+dlvID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	view := out
	if view["payment"].(map[string]any)["state"] != "done" {
		t.Fatalf("payment = %v", view["payment"])
	}
	steps := view["steps"].(map[string]any)
	for _, want := range []string{"dispatched", "bound", "written", "readback", "accepted", "delivered"} {
		if _, ok := steps[want]; !ok {
			t.Fatalf("steps missing %s: %v", want, steps)
		}
	}
	if view["binding_stale"] != false {
		t.Fatalf("fresh view must not be stale: %v", view["binding_stale"])
	}
	if _, ok := view["events"].([]any); !ok {
		t.Fatalf("events missing")
	}

	// 列表(含 steps 投影)。
	status, out = f.do2(t, "GET", "/api/v1/touchpoint-deliveries", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if out["count"].(float64) < 1 {
		t.Fatalf("list = %v", out)
	}
}

func TestTouchpointDeliveryHTTPReplayAndConflicts(t *testing.T) {
	f := dlvFixture(t)
	_, _, _, _, _, _, tp1, _ := tpSeedScene(t, f)
	orderID := dlvSeedOrder(t, f, "ord-r")
	dlvPayOrder(t, f, orderID)
	d := dlvCreateDelivery(t, f, tp1, orderID)
	dlvID := d["id"].(string)

	// 幂等重放:同键同负载 replayed=true 零推进。
	body := `{"event_type":"dispatched","event_id":"rep-1","device_ref":"dev-9","occurred_at":"2026-11-11T01:00:00Z"}`
	status, out := f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/events", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusCreated)
	status, out = f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/events", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)
	if out["replayed"] != true || out["advanced"] != false {
		t.Fatalf("replay = %v", out)
	}
	// 同键异负载 → IDEMPOTENCY_CONFLICT。
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/events", "sess-owner-a", f.tenA,
		`{"event_type":"dispatched","event_id":"rep-1","device_ref":"dev-other","occurred_at":"2026-11-11T01:00:00Z"}`)
	mustEqual(t, status, http.StatusConflict)
	// 已完成步再来一步事实(新键)→ 409 step_skipped。
	status, code := dlvEvent(t, f, dlvID, "dispatched", "rep-2", ``)
	mustEqual(t, status, http.StatusConflict)
	if code["error"] != "step_skipped" {
		t.Fatalf("error code = %v", code["error"])
	}
	// replaced 词表只归补换流程。
	status, _ = dlvEvent(t, f, dlvID, "replaced", "rep-3", ``)
	mustEqual(t, status, http.StatusBadRequest)
}

func TestTouchpointDeliveryHTTPPermissionMatrix(t *testing.T) {
	f := dlvFixture(t)
	_, _, _, _, _, _, tp1, _ := tpSeedScene(t, f)
	orderID := dlvSeedOrder(t, f, "ord-p")
	dlvPayOrder(t, f, orderID)
	d := dlvCreateDelivery(t, f, tp1, orderID)
	dlvID := d["id"].(string)

	// 游客 401
	status, _ := f.do2(t, "GET", "/api/v1/touchpoint-deliveries", "", f.tenA, "")
	mustEqual(t, status, http.StatusUnauthorized)
	// staff 403
	status, _ = f.do2(t, "GET", "/api/v1/touchpoint-deliveries", "sess-staff-a", f.tenA, "")
	mustEqual(t, status, http.StatusForbidden)
	// 门店经理本店可见可录
	status, _ = f.do2(t, "GET", "/api/v1/touchpoint-deliveries", "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	status, _ = dlvEvent(t, f, dlvID, "dispatched", "pm-1", ``)
	mustEqual(t, status, http.StatusCreated)
	// 跨租户 404 掩码
	status, _ = f.do2(t, "GET", "/api/v1/touchpoint-deliveries/"+dlvID, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/events", "sess-owner-b", f.tenB,
		`{"event_type":"bound","event_id":"pm-x"}`)
	mustEqual(t, status, http.StatusNotFound)
	// 跨租户建交付(触点不可见)404
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries", "sess-owner-b", f.tenB,
		fmt.Sprintf(`{"touchpoint_id":%q,"hardware_order_id":%q}`, tp1, orderID))
	mustEqual(t, status, http.StatusNotFound)
	// 经受信缝给 B 租户付款订单,B owner 依然拿不到 A 的触点。
	orderB := dlvSeedOrderB(t, f)
	dlvPayOrderB(t, f, f.tenB, orderB)
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries", "sess-owner-b", f.tenB,
		fmt.Sprintf(`{"touchpoint_id":%q,"hardware_order_id":%q}`, tp1, orderB))
	mustEqual(t, status, http.StatusNotFound)
}

func dlvSeedOrderB(t *testing.T, f *tpFixture) string {
	t.Helper()
	status, out := f.do2(t, "POST", "/api/v1/billing/hardware-orders", "sess-owner-b", f.tenB,
		`{"idempotency_key":"ord-b","items":[{"sku":"S","name":"n","kind":"goods","qty":1,"unit_amount_minor":100}]}`)
	mustEqual(t, status, http.StatusCreated)
	return out["order"].(map[string]any)["id"].(string)
}

func dlvPayOrderB(t *testing.T, f *tpFixture, tenID, orderID string) {
	t.Helper()
	status, out := f.doService(t, "POST", "/api/v1/billing/authority/events",
		fmt.Sprintf(`{"tenant_id":%q,"order_id":%q,"source":"eco-billing","event_id":"pay-b","type":"payment_paid"}`, tenID, orderID))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("pay order b: %d %v", status, out)
	}
}

func TestTouchpointDeliveryHTTPStaleAfterRebind(t *testing.T) {
	f := dlvFixture(t)
	_, camp2, _, tag3, _, _, tp1, _ := tpSeedScene(t, f)
	orderID := dlvSeedOrder(t, f, "ord-s")
	dlvPayOrder(t, f, orderID)
	d := dlvCreateDelivery(t, f, tp1, orderID)
	dlvID := d["id"].(string)
	dlvEvent(t, f, dlvID, "dispatched", "st-1", ``)
	status, out := dlvEvent(t, f, dlvID, "bound", "st-2", ``)
	mustEqual(t, status, http.StatusCreated)

	// 换绑 tag3 的卡到 camp2 —— 对 tp1 换目标(改目标后当前绑定版本漂移)。
	// tpSeedScene 的 tp1 绑定 store 根;换绑到 legacy campaign 目标触发 revision+1。
	status, prev := f.do2(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind-preview", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"target_type":"legacy_campaign","campaign_id":%q}`, camp2))
	mustEqual(t, status, http.StatusOK)
	rev := int64(prev["binding_revision"].(float64))
	status, _ = f.do2(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"target_type":"legacy_campaign","campaign_id":%q,"expected_binding_revision":%d}`, camp2, rev))
	mustEqual(t, status, http.StatusOK)

	status, out = f.do2(t, "GET", "/api/v1/touchpoint-deliveries/"+dlvID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if out["binding_stale"] != true {
		t.Fatalf("after rebind binding_stale = %v (view %v)", out["binding_stale"], out)
	}
	if out["current_binding_revision"].(float64) == out["bound_binding_revision"].(float64) {
		t.Fatalf("current vs bound revision must differ: %v", out)
	}
	_ = out
	_ = tag3
}

func TestTouchpointDeliveryHTTPReplace(t *testing.T) {
	f := dlvFixture(t)
	_, _, _, _, _, tag3, tp1, tp2 := tpSeedScene(t, f)
	orderID := dlvSeedOrder(t, f, "ord-c")
	dlvPayOrder(t, f, orderID)
	d := dlvCreateDelivery(t, f, tp1, orderID)
	dlvID := d["id"].(string)
	dlvEvent(t, f, dlvID, "dispatched", "rp-0", ``)

	// damaged_recoverable:同触点换载体(保留逻辑入口)。
	status, rep := f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/replace", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"reason_category":"damaged_recoverable","reason_detail":"卡面破损","new_touchpoint_id":%q,"physical_unit_ref":"unit-2"}`, tp1))
	mustEqual(t, status, http.StatusCreated)
	if rep["new_delivery"].(map[string]any)["status"] != "awaiting_dispatch" {
		t.Fatalf("replacement status = %v", rep)
	}
	if rep["audit"].(map[string]any)["old_entry_revoked"] != float64(0) {
		t.Fatalf("recoverable audit = %v", rep["audit"])
	}
	// 旧交付已封存:GET 可见 superseded;再补换 409。
	status, old := f.do2(t, "GET", "/api/v1/touchpoint-deliveries/"+dlvID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if old["status"] != "superseded" {
		t.Fatalf("old status = %v", old["status"])
	}
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlvID+"/replace", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"reason_category":"damaged_recoverable","new_touchpoint_id":%q}`, tp1))
	mustEqual(t, status, http.StatusConflict)

	// lost_uncontrolled:必须换新触点;旧触点停用(撤销旧入口)。
	orderID2 := dlvSeedOrder(t, f, "ord-c2")
	dlvPayOrder(t, f, orderID2)
	d2 := dlvCreateDelivery(t, f, tp2, orderID2)
	dlv2 := d2["id"].(string)
	// 同触点 + lost = 400
	status, _ = f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlv2+"/replace", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"reason_category":"lost_uncontrolled","new_touchpoint_id":%q}`, tp2))
	mustEqual(t, status, http.StatusBadRequest)
	// 新卡(tag3)先开触点 —— tag3 还没有稳定入口,先 provision。
	status, tp3v := f.do2(t, "POST", "/api/v1/touchpoints", "sess-owner-a", f.tenA, `{"tag_id":"`+tag3+`"}`)
	mustEqual(t, status, http.StatusCreated)
	tp3 := tp3v["id"].(string)
	status, rep2 := f.do2(t, "POST", "/api/v1/touchpoint-deliveries/"+dlv2+"/replace", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"reason_category":"lost_uncontrolled","reason_detail":"卡片遗失","new_touchpoint_id":%q,"physical_unit_ref":"unit-3"}`, tp3))
	mustEqual(t, status, http.StatusCreated)
	if rep2["audit"].(map[string]any)["old_entry_revoked"] != float64(1) {
		t.Fatalf("lost audit = %v", rep2["audit"])
	}
	// 旧触点已停用(遗失卡不能再成为有效入口)。
	status, tpOld := f.do2(t, "GET", "/api/v1/touchpoints/"+tp2, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if tpOld["status"] != "disabled" {
		t.Fatalf("old touchpoint status = %v", tpOld["status"])
	}
	// 审计随详情返回。
	status, det := f.do2(t, "GET", "/api/v1/touchpoint-deliveries/"+dlv2, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if !strings.Contains(strings.Join([]string{fmt.Sprint(det["replacements"])}, ""), "lost_uncontrolled") {
		t.Fatalf("replacements = %v", det["replacements"])
	}
}
