package httpapi

// server_dashboard_ops_test.go: HUI-1677 经营看板增量 HTTP 验收矩阵。
//
// 纪律(与 server_dashboard_test.go 同一纪律):
//   - FEATURE_DASHBOARD off = 两条新路由也不注册(handler 兜底双 404);
//   - 窗口必填 RFC3339,非法 400;筛选白名单校验,未知值 400(绝不静默匹配空);
//   - 作用域服务端单点裁决:店经理收窄本店,越店筛选 403,畸形经理 fail-closed;
//   - 跨租户 0 行;OperationsSummary 契约字段固定五项。

import (
	"net/http"
	"testing"
	"time"
)

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
	if len(out) != 5 {
		t.Fatalf("summary contract is exactly five fields, got %v", out)
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
