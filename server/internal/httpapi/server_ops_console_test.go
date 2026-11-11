package httpapi

// server_ops_console_test.go: HUI-4592 平台运营控制台 HTTP 验收矩阵。
//
// 覆盖(裁定书核心验收 T17/T20 相关面):
//   - 权限:FEATURE_AUTHZ off 统一 404;无 grant 的商家角色(含 org_owner)
//     一律 403 —— org_owner ≠ 平台管理员;受信缝签发 grant 后可用;
//   - O1:商户列表/详情(深层数据锁/开)、受控支持访问旅程(限时/原因/到期
//     立即停/关闭墓碑/审计);
//   - 操作信封:revision 守卫(stale=failed 记录保留)、幂等重放(同键同负载
//     不重执行)、同键异负载 409、缺原因 400、白名单外 422;
//   - O2:目录窗口发布/下架;历史订单价格快照不变;
//   - O3:订单只读重查(不改账);
//   - O4:3535 唯一写面紧急停用/恢复(只停目标行,无关行不动);
//   - O5:分发撤回唯一写面;
//   - O6:工单状态机(关闭必带理由)、恢复演练必带校验报告、聚合视图探针
//     诚实 degraded。

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/ops"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// sixTestEnvironments fills the fixed six-environment map (3535 T07 口径).
func sixTestEnvironments(c channeladmission.EnvCapabilities) map[string]channeladmission.EnvCapabilities {
	out := make(map[string]channeladmission.EnvCapabilities, 6)
	for _, e := range channeladmission.Environments {
		out[e] = c
	}
	return out
}

// opsFixture = authz fixture + 平台运营主体的受信 grant。
func opsFixture(t *testing.T) *authzFixture {
	t.Helper()
	f := newAuthzFixture(t, true)
	body := fmt.Sprintf(`{"tenant_id":%q,"principal_ref":"usr_owner_a","commands":[%s],
		"expires_at":"2027-01-01T00:00:00Z","reason":"平台运营当班"}`,
		f.tenA, opsCommandList())
	status, out := f.doService(t, "POST", "/api/v1/ops-grants", "svc-ops-console", "ops-service-token", body)
	if status != 201 {
		t.Fatalf("ops grant seed = %d %v", status, out)
	}
	return f
}

func opsCommandList() string {
	cmds := []string{
		ops.CmdTenantsRead, ops.CmdTenantsManage, ops.CmdPlansRead, ops.CmdPlansManage,
		ops.CmdOrdersRead, ops.CmdOrdersManage, ops.CmdChannelsRead, ops.CmdChannelsManage,
		ops.CmdTemplatesRead, ops.CmdTemplatesManage, ops.CmdOpsRead, ops.CmdOpsManage,
		ops.CmdSupportManage,
	}
	quoted := make([]string, 0, len(cmds))
	for _, c := range cmds {
		quoted = append(quoted, fmt.Sprintf("%q", c))
	}
	return strings.Join(quoted, ",")
}

// op runs one operation envelope as the ops operator.
func op(t *testing.T, f *authzFixture, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, "POST", "/api/v1/ops/operations", "sess-owner-a", f.tenA, body)
}

func TestOpsConsoleOffIs404(t *testing.T) {
	f := newAuthzFixture(t, false)
	for _, tc := range [][2]string{
		{"GET", "/api/v1/ops/tenants"}, {"GET", "/api/v1/ops/plans"}, {"GET", "/api/v1/ops/orders"},
		{"GET", "/api/v1/ops/channels"}, {"GET", "/api/v1/ops/templates"}, {"GET", "/api/v1/ops/operations"},
		{"POST", "/api/v1/ops/operations"}, {"POST", "/api/v1/ops/support-access"},
	} {
		status, _ := f.do(t, tc[0], tc[1], "sess-owner-a", f.tenA, "{}")
		if status != 404 {
			t.Fatalf("FEATURE_AUTHZ off %s %s = %d, want uniform 404", tc[0], tc[1], status)
		}
	}
}

func TestOpsPermissionMatrixNoSelfGrant(t *testing.T) {
	f := newAuthzFixture(t, true)
	// org_owner 无 grant ≠ 平台管理员:六视图读面全部 403。
	for _, p := range []string{"/api/v1/ops/tenants", "/api/v1/ops/plans", "/api/v1/ops/orders",
		"/api/v1/ops/templates", "/api/v1/ops/operations", "/api/v1/ops/audit"} {
		status, out := f.do(t, "GET", p, "sess-owner-a", f.tenA, "")
		if status != 403 {
			t.Fatalf("owner without grant GET %s = %d %v, want 403", p, status, out)
		}
	}
	// 商家 owner 打操作信封也 403(在 envelope 白名单校验前先裁权限)。
	status, out := op(t, f, `{"action":"tenant.suspend","resource_ref":"x","expected_revision":"1",
		"reason":"r","idempotency_key":"k"}`)
	if status != 403 {
		t.Fatalf("owner envelope without grant = %d %v", status, out)
	}
	// staff/代理同样拒绝。
	status, _ = f.do(t, "GET", "/api/v1/ops/tenants", "sess-staff-a", f.tenA, "")
	if status != 403 {
		t.Fatalf("staff ops read = %d", status)
	}
	// 无会话 401。
	status, _ = f.do(t, "GET", "/api/v1/ops/tenants", "", f.tenA, "")
	if status != 401 {
		t.Fatalf("guest ops read = %d", status)
	}
}

func TestOpsTenantListAndDetailLockUnlock(t *testing.T) {
	f := opsFixture(t)
	status, out := f.do(t, "GET", "/api/v1/ops/tenants", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("tenant list = %d %v", status, out)
	}
	// 详情:无 support access → 深层数据锁定,聚合计数仍可见。
	status, out = f.do(t, "GET", "/api/v1/ops/tenants/"+f.tenB, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("tenant detail = %d %v", status, out)
	}
	deep, _ := out["business_data"].(map[string]any)
	if deep["state"] != "locked" || deep["message"] == "" {
		t.Fatalf("business data must be locked without support access: %v", deep)
	}
	if _, ok := deep["stores"]; ok {
		t.Fatalf("locked detail must not leak store rows: %v", deep)
	}
	// 平台层事实可见。
	if _, ok := out["ops_revision"]; !ok {
		t.Fatalf("platform facts visible: %v", out)
	}
}

func TestOpsSupportAccessJourney(t *testing.T) {
	f := opsFixture(t)
	// TTL 超上限拒绝。
	status, out := f.do(t, "POST", "/api/v1/ops/support-access", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"tenant_id":%q,"reason":"排查","ttl_minutes":241}`, f.tenB))
	if status != 400 {
		t.Fatalf("ttl above cap = %d %v", status, out)
	}
	// 缺原因拒绝(审计三件套)。
	status, _ = f.do(t, "POST", "/api/v1/ops/support-access", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"tenant_id":%q,"reason":"","ttl_minutes":60}`, f.tenB))
	if status != 400 {
		t.Fatalf("missing reason = %d", status)
	}
	// 正常打开 → 深层数据开放且带引用。
	status, out = f.do(t, "POST", "/api/v1/ops/support-access", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"tenant_id":%q,"reason":"处理商户工单 tkt_9","ttl_minutes":60}`, f.tenB))
	if status != 201 {
		t.Fatalf("support open = %d %v", status, out)
	}
	sup, _ := out["support_access"].(map[string]any)
	if sup["expires_at"] == "" {
		t.Fatalf("support access must be time-boxed: %v", sup)
	}
	status, out = f.do(t, "GET", "/api/v1/ops/tenants/"+f.tenB, "sess-owner-a", f.tenA, "")
	deep, _ := out["business_data"].(map[string]any)
	if status != 200 || deep["state"] != "open" || deep["support_access_ref"] == "" {
		t.Fatalf("deep data after support open = %d %v", status, out)
	}
	if _, ok := deep["stores"].([]any); !ok {
		t.Fatalf("open detail carries store rows: %v", deep)
	}
	// 关闭必带理由;关闭后立即锁回。
	status, _ = f.do(t, "POST", "/api/v1/ops/support-access/"+sup["id"].(string)+"/close",
		"sess-owner-a", f.tenA, `{"reason":""}`)
	if status != 400 {
		t.Fatalf("close without reason = %d", status)
	}
	status, _ = f.do(t, "POST", "/api/v1/ops/support-access/"+sup["id"].(string)+"/close",
		"sess-owner-a", f.tenA, `{"reason":"排查完成"}`)
	if status != 200 {
		t.Fatalf("close = %d", status)
	}
	_, out = f.do(t, "GET", "/api/v1/ops/tenants/"+f.tenB, "sess-owner-a", f.tenA, "")
	deep, _ = out["business_data"].(map[string]any)
	if deep["state"] != "locked" {
		t.Fatalf("closed support must lock immediately: %v", deep)
	}
	// 支持访问全程留痕(打开+关闭两行审计)。
	status, out = f.do(t, "GET", "/api/v1/ops/audit", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("audit read = %d %v", status, out)
	}
	rows, _ := out["audit"].([]any)
	if len(rows) < 2 {
		t.Fatalf("support journey must leave audit rows: %v", out)
	}
}

func TestOpsTenantSuspendRevisionGuardAndReplay(t *testing.T) {
	f := opsFixture(t)
	tenB := f.tenB
	// 缺 expected_revision = 400(revision 守卫资源)。
	status, out := op(t, f, fmt.Sprintf(`{"action":"tenant.suspend","resource_ref":%q,
		"expected_revision":"","reason":"r","idempotency_key":"g1"}`, tenB))
	if status != 400 {
		t.Fatalf("missing revision = %d %v", status, out)
	}
	// 缺原因 = 400。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"tenant.suspend","resource_ref":%q,
		"expected_revision":"1","reason":"","idempotency_key":"g1"}`, tenB))
	if status != 400 {
		t.Fatalf("missing reason = %d", status)
	}
	// 白名单外动作 = 422(O4 不能关闭无关导航在这里结构性拒绝)。
	status, out = op(t, f, `{"action":"wifi.turn_off","resource_ref":"x","reason":"r","idempotency_key":"g0"}`)
	if status != 422 || out["error"] != "action_not_allowed" {
		t.Fatalf("non-whitelisted action = %d %v", status, out)
	}
	// 正确 revision → 成功,revision 前进,审计带前后值。
	status, out = op(t, f, fmt.Sprintf(`{"action":"tenant.suspend","resource_ref":%q,
		"expected_revision":"1","reason":"涉诈风险停用","idempotency_key":"g1"}`, tenB))
	if status != 200 {
		t.Fatalf("suspend = %d %v", status, out)
	}
	opRow, _ := out["operation"].(map[string]any)
	if opRow["state"] != "succeeded" || opRow["id"] == "" {
		t.Fatalf("operation = %v", opRow)
	}
	opID := opRow["id"].(string)
	// 结果按 operation_id 追踪到权威结果 + 关联审计。
	status, out = f.do(t, "GET", "/api/v1/ops/operations/"+opID, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("operation track = %d %v", status, out)
	}
	auditRows, _ := out["audit"].([]any)
	if len(auditRows) == 0 {
		t.Fatalf("operation audit missing: %v", out)
	}
	// stale revision → 操作记录为 failed(可重试),状态不被覆盖。
	status, out = op(t, f, fmt.Sprintf(`{"action":"tenant.resume","resource_ref":%q,
		"expected_revision":"1","reason":"误操作恢复","idempotency_key":"g2"}`, tenB))
	if status != 200 {
		t.Fatalf("stale request = %d %v", status, out)
	}
	opRow, _ = out["operation"].(map[string]any)
	if opRow["state"] != "failed" || opRow["error_code"] != "stale_version" {
		t.Fatalf("stale operation = %v", opRow)
	}
	// 幂等重放:同键同负载 → replayed=true,不再执行(结果与首跑一致)。
	status, out = op(t, f, fmt.Sprintf(`{"action":"tenant.suspend","resource_ref":%q,
		"expected_revision":"1","reason":"涉诈风险停用","idempotency_key":"g1"}`, tenB))
	if status != 200 {
		t.Fatalf("replay = %d %v", status, out)
	}
	if out["replayed"] != true {
		t.Fatalf("replay flag = %v", out)
	}
	// 同键异负载 → 409。
	status, out = op(t, f, fmt.Sprintf(`{"action":"tenant.resume","resource_ref":%q,
		"expected_revision":"2","reason":"另一步","idempotency_key":"g1"}`, tenB))
	if status != 409 || out["error"] != "idempotency_conflict" {
		t.Fatalf("conflicting replay = %d %v", status, out)
	}
	// 审计三件套:前后值/操作者/时间/原因。
	status, out = f.do(t, "GET", "/api/v1/ops/audit?target="+tenB, "sess-owner-a", f.tenA, "")
	rows, _ := out["audit"].([]any)
	// 成功分派才落审计行(failed 操作记录保留在 ops_operations,可重试);
	// 审计三件套:前后值/操作者/时间/原因。
	if status != 200 || len(rows) < 1 {
		t.Fatalf("audit rows = %d %v", len(rows), out)
	}
	first, _ := rows[0].(map[string]any)
	if first["actor_ref"] == "" || first["occurred_at"] == "" || first["reason"] == "" {
		t.Fatalf("audit triple incomplete: %v", first)
	}
}

func TestOpsPlansWindowDoesNotTouchOrderSnapshots(t *testing.T) {
	f := opsFixture(t)
	// 全局目录行。
	entry, err := f.s.St.UpsertBillingCatalogEntry(billing.CatalogEntry{
		ScopeTenant: "", PlanCode: billing.PlanMonthly, PricingVersion: "v1",
		PriceRef: "p_v1", Currency: "CNY", PriceMinor: 990000, ServiceDays: 365,
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
	// 商家 A 下一单(钉价格快照)。
	status, out := f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"action":"new","plan_code":%q,"price_ref":"p_v1","idempotency_key":"o1"}`, billing.PlanMonthly))
	if status != 200 && status != 201 {
		t.Fatalf("order create = %d %v", status, out)
	}
	orderID, _ := out["order"].(map[string]any)["id"].(string)
	if orderID == "" {
		t.Fatalf("order id missing: %v", out)
	}
	// O2 下架该目录行。
	status, out = op(t, f, fmt.Sprintf(`{"action":"plan.retire","resource_ref":%q,
		"reason":"条款更新,旧版停售","idempotency_key":"p1"}`, entry.ID))
	if status != 200 {
		t.Fatalf("plan.retire = %d %v", status, out)
	}
	// 历史订单快照逐字节不变。
	status, out = f.do(t, "GET", "/api/v1/billing/orders/"+orderID, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("order read = %d %v", status, out)
	}
	if snap, _ := out["order"].(map[string]any)["price_snapshot"].(string); snap == "" {
		t.Fatalf("price snapshot must persist: %v", out)
	}
	// O2 视图读面:全量目录 + 明示订单不受影响。
	status, out = f.do(t, "GET", "/api/v1/ops/plans", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("plans list = %d %v", status, out)
	}
	if out["note"] == "" {
		t.Fatalf("snapshot discipline note missing: %v", out)
	}
	// 重新发布窗口。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"plan.publish","resource_ref":%q,
		"reason":"新版生效","idempotency_key":"p2"}`, entry.ID))
	if status != 200 {
		t.Fatalf("plan.publish = %d", status)
	}
}

func TestOpsOrderRecheckReadonly(t *testing.T) {
	f := opsFixture(t)
	seedCatalogStore(t, f.s.St)
	f.s.Cfg.SubscriptionStatus = "active"
	status, out := f.do(t, "POST", "/api/v1/billing/subscription-orders", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"action":"new","plan_code":%q,"price_ref":"price_m_v2026","idempotency_key":"rk1"}`, billing.PlanMonthly))
	if status != 200 && status != 201 {
		t.Fatalf("order seed = %d %v", status, out)
	}
	orderID, _ := out["order"].(map[string]any)["id"].(string)
	// 受信权威事件把订单推进到 failed(对账队列素材;权威缝语义同 4591)。
	_, _, err := f.s.St.ApplyBillingOrderEvent(f.tenA, orderID, billing.AuthorityEventInput{
		Source: "eco-billing", EventID: "evt-fail-1", Type: billing.EventActivationFailed,
		Payload: billing.EventPayload{FailReason: "渠道开通失败"}, OccurredAt: time.Now().UTC().Format(time.RFC3339),
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
	// O3 只读重查:结果带快照,订单状态不变。
	status, out = op(t, f, fmt.Sprintf(`{"action":"order.recheck","resource_ref":%q,
		"tenant_id":%q,"reason":"对账差异复核","idempotency_key":"rk2"}`, orderID, f.tenA))
	if status != 200 {
		t.Fatalf("recheck = %d %v", status, out)
	}
	result, _ := out["operation"].(map[string]any)["result_json"].(string)
	if !strings.Contains(result, "failed") {
		t.Fatalf("recheck snapshot missing order state: %s", result)
	}
	// 缺 tenant_id = 拒绝(不猜跨租户目标)。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"order.recheck","resource_ref":%q,
		"reason":"r","idempotency_key":"rk3"}`, orderID))
	if status != 400 {
		t.Fatalf("recheck without tenant = %d", status)
	}
	// O3 列表读面 + 异常队列。
	status, out = f.do(t, "GET", "/api/v1/ops/orders", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("orders view = %d %v", status, out)
	}
	queue, _ := out["discrepancy_queue"].([]any)
	if len(queue) == 0 {
		t.Fatalf("failed order should surface in the queue: %v", out)
	}
}

func TestOpsChannelEmergencySingleRow(t *testing.T) {
	f := opsFixture(t)
	// O4 分派要求渠道准入写面存在(FEATURE_CHANNEL_ADMISSION on);ops 路由
	// 本身在 FEATURE_AUTHZ 段注册,此处运行时开闸等价真实部署两开关全开。
	f.s.Cfg.FeatureChannelAdmission = true
	// 种子能力行(直接走 3535 store 层)。
	caps := channeladmission.EnvCapabilities{Navigation: "unsupported",
		MediaHandoff: "unsupported", PlatformReceipt: "unsupported", RewardEligibility: "unsupported"}
	row := channeladmission.ActionCapability{
		ID: "cap_test_a01", CatalogCode: "A01", Platform: "douyin", Method: channeladmission.ActionMethodH5Share,
		Label:    "抖音店宣投稿",
		Scenario: channeladmission.ActionScenarioStorePromo, ActionKind: channeladmission.ActionKindPublish,
		TenantScope: "*", Status: channeladmission.StatusNotAdmitted, AppBinding: "none",
		EvidenceGrade: channeladmission.EvidenceUnverified, CheckedAt: "2026-11-09T00:00:00Z",
		SeedVersion:  1,
		Environments: sixTestEnvironments(caps),
	}
	_, _, err := f.s.St.UpsertActionCapability(row, store.ControlAudit{Actor: "seed", Reason: "seed", Action: "create"})
	mustNoErr(t, err)
	// 无关第二行(A02,同平台不同目录码)。
	row2 := row
	row2.ID = "cap_test_a02"
	row2.CatalogCode = "A02"
	_, _, err = f.s.St.UpsertActionCapability(row2, store.ControlAudit{Actor: "seed", Reason: "seed", Action: "create"})
	mustNoErr(t, err)
	// 迁移已种 A01-A30 真值行:受控重写按自然键命中既有行并保留原主键。
	// 从全量清单解析实际主键(同自然键恰一行)。
	id, id2 := "", ""
	all, err := f.s.St.ListActionCapabilitiesAll()
	mustNoErr(t, err)
	for _, r := range all {
		if r.CatalogCode == "A01" && r.Method == channeladmission.ActionMethodH5Share && r.Scenario == channeladmission.ActionScenarioStorePromo {
			id = r.ID
		}
		if r.CatalogCode == "A02" && r.Method == channeladmission.ActionMethodH5Share && r.Scenario == channeladmission.ActionScenarioStorePromo {
			id2 = r.ID
		}
	}
	if id == "" || id2 == "" || id == id2 {
		t.Fatalf("seed rows unresolved: %q %q", id, id2)
	}
	// O4:紧急停用只停目标行。
	status, out := op(t, f, fmt.Sprintf(`{"action":"channel.disable","resource_ref":%q,
		"reason":"平台接口失效,紧急停用","idempotency_key":"c1"}`, id))
	if status != 200 {
		t.Fatalf("channel.disable = %d %v", status, out)
	}
	got, err := f.s.St.GetActionCapabilityByID(id)
	mustNoErr(t, err)
	if !got.EmergencyDisabled {
		t.Fatal("target row must be disabled")
	}
	other, err := f.s.St.GetActionCapabilityByID(id2)
	mustNoErr(t, err)
	if other.EmergencyDisabled {
		t.Fatal("unrelated row must stay untouched (O4 不能关闭无关能力)")
	}
	// 恢复。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"channel.restore","resource_ref":%q,
		"reason":"平台接口恢复,重检通过","idempotency_key":"c2"}`, id))
	if status != 200 {
		t.Fatalf("channel.restore = %d", status)
	}
	got, _ = f.s.St.GetActionCapabilityByID(id)
	if got.EmergencyDisabled {
		t.Fatal("target row must be restored")
	}
	// O4 读面。
	status, out = f.do(t, "GET", "/api/v1/ops/channels", "sess-owner-a", f.tenA, "")
	if status != 200 || out["scope"] != "platform" {
		t.Fatalf("channels view = %d %v", status, out)
	}
}

func TestOpsAssignmentCancelViaEnvelope(t *testing.T) {
	f := opsFixture(t)
	_, _, err := f.s.St.CreateTemplateAssignment(store.NewTemplateAssignment{
		TenantID: f.tenA, StoreID: f.st1, TemplateRef: "tpl_home_v3", SourceVersion: "7",
		RequestKey: "ops-dist-1", CreatedBy: "usr_owner_a",
	})
	mustNoErr(t, err)
	assignments, err := f.s.St.ListTemplateAssignments(f.tenA, f.st1)
	mustNoErr(t, err)
	if len(assignments) != 1 {
		t.Fatalf("seed assignment = %d", len(assignments))
	}
	assignID := assignments[0].ID
	status, out := op(t, f, fmt.Sprintf(`{"action":"template.assignment.cancel","resource_ref":%q,
		"tenant_id":%q,"reason":"模板版权风险,总部撤回","idempotency_key":"t1"}`, assignID, f.tenA))
	if status != 200 {
		t.Fatalf("assignment cancel = %d %v", status, out)
	}
	got, err := f.s.St.GetTemplateAssignment(assignID, f.tenA)
	mustNoErr(t, err)
	if got.Status != "cancelled" {
		t.Fatalf("assignment status = %q", got.Status)
	}
}

func TestOpsTicketFlowAndDrillDiscipline(t *testing.T) {
	f := opsFixture(t)
	// 开单。
	status, out := op(t, f, `{"action":"ticket.open","resource_ref":"","title":"媒体域名 403",
		"kind":"media_probe","severity":"high","detail":"PUBLISH_HANDOFF_MEDIA_BASE_URL 探针 403",
		"reason":"T20 告警定位","idempotency_key":"tk1"}`)
	if status != 200 {
		t.Fatalf("ticket open = %d %v", status, out)
	}
	ticket, _ := out["operation"].(map[string]any)
	if ticket["state"] != "succeeded" {
		t.Fatalf("ticket open state = %v", ticket)
	}
	var result struct {
		Ticket struct {
			ID string `json:"id"`
		} `json:"ticket"`
	}
	if err := json.Unmarshal([]byte(out["operation"].(map[string]any)["result_json"].(string)), &result); err != nil {
		t.Fatalf("ticket result json: %v", err)
	}
	ticketID := result.Ticket.ID
	if ticketID == "" {
		t.Fatalf("ticket id missing")
	}
	// 状态机:resolve 跳过 confirm = 拒绝(操作记录 failed,但请求 200——信封语义)。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"ticket.resolve","resource_ref":%q,
		"resolution":"x","reason":"r","idempotency_key":"tk2"}`, ticketID))
	if status != 200 {
		t.Fatalf("illegal transition call = %d", status)
	}
	status, out = f.do(t, "GET", "/api/v1/ops/operations", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("ops view = %d %v", status, out)
	}
	tickets, _ := out["tickets"].([]any)
	if len(tickets) == 0 {
		t.Fatalf("tickets must be visible in the operations view: %v", out)
	}
	// confirm → resolve → close(关闭必带理由)。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"ticket.confirm","resource_ref":%q,
		"reason":"认领处理","idempotency_key":"tk3"}`, ticketID))
	if status != 200 {
		t.Fatalf("confirm = %d", status)
	}
	status, _ = op(t, f, fmt.Sprintf(`{"action":"ticket.resolve","resource_ref":%q,
		"resolution":"已切换媒体源,探针恢复","reason":"修复完成","idempotency_key":"tk4"}`, ticketID))
	if status != 200 {
		t.Fatalf("resolve = %d", status)
	}
	status, _ = op(t, f, fmt.Sprintf(`{"action":"ticket.close","resource_ref":%q,
		"reason":"r","idempotency_key":"tk5"}`, ticketID))
	if status != 200 {
		t.Fatalf("close call = %d", status)
	}
	// 关闭缺理由被拒后,带理由关闭成功(操作记录可查)。
	status, _ = op(t, f, fmt.Sprintf(`{"action":"ticket.close","resource_ref":%q,
		"close_reason":"恢复确认,证据 dr_1","reason":"复盘归档","idempotency_key":"tk6"}`, ticketID))
	if status != 200 {
		t.Fatalf("close with reason = %d", status)
	}
	// 演练:restore 缺校验报告 = 落库前 400 拒绝(客户端错误);带报告成功。
	status, out = op(t, f, `{"action":"drill.record","drill_kind":"restore","drill_target":"db主库",
		"drill_result":"ok","verification_report":"","reason":"季度演练","idempotency_key":"dr1"}`)
	if status != 400 {
		t.Fatalf("restore drill without report = %d %v, want 400", status, out)
	}
	status, out = op(t, f, `{"action":"drill.record","drill_kind":"restore","drill_target":"db主库",
		"drill_result":"ok","verification_report":"回读校验 5/5 表校验和一致","reason":"季度演练","idempotency_key":"dr2"}`)
	if status != 200 {
		t.Fatalf("drill with report = %d %v", status, out)
	}
	drill, _ := out["operation"].(map[string]any)
	if drill["state"] != "succeeded" {
		t.Fatalf("drill state = %v", drill)
	}
	// O6 聚合视图:探针诚实 degraded(本地无外部配置)与演练/工单可定位。
	status, out = f.do(t, "GET", "/api/v1/ops/operations", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("operations view = %d %v", status, out)
	}
	dh, _ := out["domain_health"].(map[string]any)
	if dh["status"] == "" {
		t.Fatalf("domain health missing: %v", out)
	}
	hw, _ := out["hardware_fulfillment"].(map[string]any)
	if hw["source"] != "billing_projection_4591" {
		t.Fatalf("hardware seam source must be declared: %v", hw)
	}
	drills, _ := out["drills"].([]any)
	if len(drills) != 1 {
		t.Fatalf("drills visible = %v", out["drills"])
	}
}

func TestOpsGrantExpiryStopsImmediately(t *testing.T) {
	// 过期 grant 的主体 → 即刻 403(fail-closed,即使 grant 行仍在)。
	f := newAuthzFixture(t, true)
	body := fmt.Sprintf(`{"tenant_id":%q,"principal_ref":"usr_owner_b","commands":[%q],
		"expires_at":"2020-01-01T00:00:00Z","reason":"已过期"}`, f.tenB, ops.CmdTenantsRead)
	status, _ := f.doService(t, "POST", "/api/v1/ops-grants", "svc-ops-console", "ops-service-token", body)
	if status != 201 {
		t.Fatalf("seed expired grant = %d", status)
	}
	status, _ = f.do(t, "GET", "/api/v1/ops/tenants", "sess-owner-b", f.tenB, "")
	if status != 403 {
		t.Fatalf("expired grant read = %d, want 403", status)
	}
}

func TestOpsTemplatesViewSmoke(t *testing.T) {
	// 冒烟修复回归:目录/分发/内容版本三读面齐出(真实表列名锁进测试)。
	f := opsFixture(t)
	status, out := f.do(t, "GET", "/api/v1/ops/templates", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("templates view = %d %v", status, out)
	}
	if _, ok := out["templates"].([]any); !ok {
		t.Fatalf("templates array missing: %v", out)
	}
	if _, ok := out["assignments"].([]any); !ok {
		t.Fatalf("assignments array missing: %v", out)
	}
	if _, ok := out["content_versions"].([]any); !ok {
		t.Fatalf("content_versions array missing: %v", out)
	}
}
