package httpapi

// server_dashboard_ops_test.go: HUI-1677 经营看板增量 HTTP 验收矩阵。
//
// 纪律(与 server_dashboard_test.go 同一纪律):
//   - FEATURE_DASHBOARD off = 两条新路由也不注册(handler 兜底双 404);
//   - 窗口必填 RFC3339,非法 400;筛选白名单校验,未知值 400(绝不静默匹配空);
//   - 作用域服务端单点裁决:店经理收窄本店,越店筛选 403,畸形经理 fail-closed;
//   - 跨租户 0 行;OperationsSummary 契约字段固定五项。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestDashboardScopedStaffCannotReadOtherStoreFacts(t *testing.T) {
	f := newDashboardFixture(t)
	m, err := f.s.St.GetMemberByPrincipal(f.tenA, "usr_staff_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.St.UpdateMemberScoped(m.ID, nil, nil, nil, &f.store1ID); err != nil {
		t.Fatal(err)
	}
	f.beacon(t, f.code1, "nfc")
	f.beacon(t, f.code2, "nfc")
	start, end := opsDayWindow()
	for _, route := range []string{"/api/v1/dashboard", "/api/v1/dashboard/operations"} {
		status, _, out := f.do(t, "GET", route+"?window_start="+start+"&window_end="+end, "sess-staff-a", f.tenA, ``)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", route, status, out)
		}
		if out["scope"].(map[string]any)["store_scope"] != f.store1ID {
			t.Fatalf("staff scope ignored: %v", out["scope"])
		}
		dims, _ := json.Marshal(out["dimensions"])
		if bytes.Contains(dims, []byte(f.camp2ID)) {
			t.Fatalf("out-of-scope campaign leaked: %s", dims)
		}
	}
	status, _, _ := f.do(t, "GET", "/api/v1/dashboard/operations?window_start="+start+"&window_end="+end+"&store_id=other-store", "sess-staff-a", f.tenA, ``)
	if status != http.StatusForbidden {
		t.Fatalf("staff escaping scope = %d", status)
	}
	disabled := false
	if _, err = f.s.St.UpdateMember(m.ID, nil, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	status, _, _ = f.do(t, "GET", "/api/v1/dashboard/operations-summary", "sess-staff-a", f.tenA, ``)
	if status != http.StatusForbidden {
		t.Fatalf("revoked staff kept summary access: %d", status)
	}
}

func opsDayWindow() (string, string) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return start.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339)
}

func TestDashboardOpsRoutesInvisibleWhenOff(t *testing.T) {
	f := newDashboardFixtureOpt(t, false)
	start, end := opsDayWindow()
	for _, path := range []string{
		"/api/v1/dashboard/operations?window_start=" + start + "&window_end=" + end,
		"/api/v1/dashboard/operations-summary",
	} {
		if status, _, _ := f.admin(t, "GET", path, ``); status != http.StatusNotFound {
			t.Fatalf("%s: FEATURE_DASHBOARD off must be 404, got %d", path, status)
		}
	}
}

func TestDashboardOpsWindowAndFilterValidation(t *testing.T) {
	f := newDashboardFixture(t)
	if status, _, _ := f.admin(t, "GET", "/api/v1/dashboard/operations", ``); status != http.StatusBadRequest {
		t.Fatalf("missing window must be 400, got %d", status)
	}
	start, end := opsDayWindow()
	if status, _, _ := f.admin(t, "GET", "/api/v1/dashboard/operations?window_start=garbage&window_end="+end, ``); status != http.StatusBadRequest {
		t.Fatalf("garbage window must be 400, got %d", status)
	}
	if status, _, _ := f.admin(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end+"&touchpoint=smoke", ``); status != http.StatusBadRequest {
		t.Fatalf("unknown touchpoint must be 400, got %d", status)
	}
	if status, _, _ := f.admin(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end+"&handoff_method=telepathy", ``); status != http.StatusBadRequest {
		t.Fatalf("unknown handoff_method must be 400, got %d", status)
	}
}

func TestDashboardOpsWiringAndEcho(t *testing.T) {
	f := newDashboardFixture(t)
	f.beacon(t, f.code1, "nfc")
	start, end := opsDayWindow()
	status, _, out := f.admin(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end+"&touchpoint=nfc", ``)
	if status != http.StatusOK {
		t.Fatalf("operations = %d, want 200: %v", status, out)
	}
	if got, _ := out["scope"].(map[string]any)["tenant_id"].(string); got != f.tenA {
		t.Fatalf("scope tenant = %q, want %q", got, f.tenA)
	}
	echo, _ := out["filter_echo"].(map[string]any)
	if echo["touchpoint"] != "nfc" {
		t.Fatalf("filter echo missing touchpoint: %v", echo)
	}
	funnel, _ := out["funnel"].([]any)
	if len(funnel) != 7 {
		t.Fatalf("funnel stages = %d, want 7", len(funnel))
	}
	first := funnel[0].(map[string]any)
	if first["value"].(float64) != 1 {
		t.Fatalf("beaconed visit must be counted: %v", first)
	}
	notes, _ := out["notes"].(map[string]any)
	if notes["entry_marker"] == "" || notes["visitor"] == "" {
		t.Fatalf("honesty notes must be present: %v", notes)
	}
	if _, ok := out["evidence"]; !ok {
		t.Fatal("evidence block missing")
	}
	if _, ok := out["cost"]; !ok {
		t.Fatal("cost block missing")
	}
	if _, ok := out["anomalies"]; !ok {
		t.Fatal("anomalies block missing")
	}
}

func TestDashboardOpsStoreManagerScoping(t *testing.T) {
	f := newDashboardFixture(t)
	start, end := opsDayWindow()
	// 店经理:作用域收窄到本店。
	status, _, out := f.do(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end, "sess-sm-a", f.tenA, ``)
	if status != http.StatusOK {
		t.Fatalf("manager operations = %d, want 200", status)
	}
	if got, _ := out["scope"].(map[string]any)["store_scope"].(string); got != f.store1ID {
		t.Fatalf("manager store_scope = %q, want %q", got, f.store1ID)
	}
	// 越店筛选:403(fail-closed,不静默收窄)。
	status, _, _ = f.do(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end+"&store_id=store-other", "sess-sm-a", f.tenA, ``)
	if status != http.StatusForbidden {
		t.Fatalf("manager escaping store filter must be 403, got %d", status)
	}
	// 畸形经理(无 store_scope):fail-closed 403。
	if _, err := f.s.St.CreateMemberScoped(f.tenA, "usr_sm_bad", "store_manager", "bad", "test", true, ""); err != nil {
		t.Fatalf("seed deformed manager: %v", err)
	}
	status, _, _ = f.do(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end, "sess-sm-bad", f.tenA, ``)
	if status != http.StatusForbidden {
		t.Fatalf("deformed manager must be 403, got %d", status)
	}
}

func TestDashboardOpsCrossTenantZero(t *testing.T) {
	f := newDashboardFixture(t)
	f.beacon(t, f.code1, "nfc")
	start, end := opsDayWindow()
	status, _, out := f.do(t, "GET",
		"/api/v1/dashboard/operations?window_start="+start+"&window_end="+end, "sess-owner-b", f.tenB, ``)
	if status != http.StatusOK {
		t.Fatalf("owner-b = %d, want 200", status)
	}
	funnel, _ := out["funnel"].([]any)
	first := funnel[0].(map[string]any)
	if first["value"].(float64) != 0 {
		t.Fatalf("cross-tenant visits must be 0 rows, got %v", first)
	}
}

func TestOperationsSummaryEndpointContract(t *testing.T) {
	f := newDashboardFixture(t)
	status, _, out := f.admin(t, "GET", "/api/v1/dashboard/operations-summary", ``)
	if status != http.StatusOK {
		t.Fatalf("summary = %d, want 200: %v", status, out)
	}
	for _, field := range []string{"as_of", "pending_confirm", "rewards_issued", "rewards_redeemed", "anomalies"} {
		if _, ok := out[field]; !ok {
			t.Fatalf("contract field %q missing: %v", field, out)
		}
	}
	// 老契约五个字段名逐字不动;TOUCH-ALL-20261109-v1 轮新增 todos 块(附加字段)。
	if len(out) != 6 {
		t.Fatalf("summary contract is five legacy fields plus the additive todos block, got %v", out)
	}
	if _, ok := out["todos"].(map[string]any); !ok {
		t.Fatalf("todos block missing: %v", out)
	}
	if out["as_of"].(string) == "" {
		t.Fatal("as_of must be present")
	}
	// 店经理作用域:可读,但按本店(空账本=真实 0)。
	status, _, out = f.do(t, "GET", "/api/v1/dashboard/operations-summary", "sess-sm-a", f.tenA, ``)
	if status != http.StatusOK {
		t.Fatalf("manager summary = %d, want 200", status)
	}
	if out["rewards_issued"].(float64) != 0 {
		t.Fatalf("manager summary must be scoped, got %v", out)
	}
}

// HUI-4665：租户 B 没有门店、活动、素材或事件。断言真正 HTTP JSON，
// 避免只断言 Go len(nil)==0 而漏掉 null 导致浏览器崩溃。
func TestDashboardOpsEmptyTenantCollectionsAreReadable(t *testing.T) {
	f := newDashboardFixture(t)
	start, end := opsDayWindow()
	status, _, out := f.do(t, "GET", "/api/v1/dashboard/operations?window_start="+start+"&window_end="+end, "sess-owner-b", f.tenB, ``)
	if status != http.StatusOK {
		t.Fatalf("empty tenant = %d: %v", status, out)
	}
	if echo, ok := out["filter_echo"].(map[string]any); !ok || len(echo) != 0 {
		t.Fatalf("empty filter_echo must be {}, got %#v", out["filter_echo"])
	}
	if inventory, ok := out["inventory"].([]any); !ok || len(inventory) != 0 {
		t.Fatalf("empty inventory must be [], got %#v", out["inventory"])
	}
	for _, stage := range out["funnel"].([]any) {
		row := stage.(map[string]any)
		if row["available"] != true || row["value"] != float64(0) {
			t.Fatalf("empty local fact must be known zero: %v", row)
		}
	}
	cost := out["cost"].(map[string]any)
	if cost["content_generation_available"] != false {
		t.Fatalf("missing generation cost must remain unavailable: %v", cost)
	}
}

func TestDashboardOpsBillingProjectionUsesBillingPermission(t *testing.T) {
	f := newDashboardFixture(t)
	start, end := opsDayWindow()
	path := "/api/v1/dashboard/operations?window_start=" + start + "&window_end=" + end
	for _, session := range []string{"sess-sm-a", "sess-staff-a"} {
		status, _, out := f.do(t, "GET", path, session, f.tenA, ``)
		if status != http.StatusOK {
			t.Fatalf("%s operations = %d", session, status)
		}
		recon := out["cost_reconciliation"].(map[string]any)
		if recon["availability"] != "forbidden" {
			t.Fatalf("%s must obey billing:view: %v", session, recon)
		}
		if len(recon) != 2 {
			t.Fatalf("restricted projection must not serialize amounts or counts: %v", recon)
		}
		if len(out["funnel"].([]any)) != 7 {
			t.Fatalf("unrelated store facts remain readable")
		}
	}
	status, _, owner := f.admin(t, "GET", path, ``)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	if _, ok := owner["cost_reconciliation"].(map[string]any)["ai_consumed_minor"]; !ok {
		t.Fatalf("owner billing projection must stay compatible")
	}
}
