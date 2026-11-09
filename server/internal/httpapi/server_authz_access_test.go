package httpapi

// server_authz_access_test.go: HUI-4593 连锁、代理与权限 HTTP 验收矩阵(B11)。
//
// 覆盖(票面 T17 + 负例 + 裁定书):
//   - off 基线:FEATURE_AUTHZ 关 = access/effective、template-assignments、
//     ops-grants、审计读面统一 404(面板不可见);
//   - GET /access/effective:四维度矩阵按角色解析(owner 全量/manager 本店/
//     staff 新旧两种/agent 按 grant/ops 命令只来自受信 grant);
//   - 代理协作:scope 显式授予(STALE_VERSION/reason 必填/owner 专属)、
//     撤销后同会话立即 403(T17);
//   - 模板下发:多店逐项报告、幂等重放不重复、保留字段拒绝、本店只读;
//   - ops grant:受信服务生产者签发/撤销,商家面自授拒绝,过期即失效;
//   - 审计:成员/代理/模板特权写留痕可回查(操作者/前后值/原因)。
//
// 纪律:夹具手机号/邮箱全合成;token 一律占位。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type authzFixture struct {
	s    *Server
	ts   *httptest.Server
	tenA string
	tenB string
	st1  string
	st2  string
	relA string
}

func newAuthzFixture(t *testing.T, authzOn bool) *authzFixture {
	t.Helper()
	upSrv, _ := newFakeUpload(t)
	authzFlag, agencyFlag, tokens, roles := "", "", "", ""
	if authzOn {
		authzFlag, agencyFlag = "on", "on"
		tokens = "svc-ops-console:ops-service-token"
		roles = "svc-ops-console:ops_grant_manager"
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_AUTHZ":
			return authzFlag
		case "FEATURE_AGENCY":
			return agencyFlag
		case "TOUCH_SERVICE_TOKENS":
			return tokens
		case "TOUCH_SERVICE_ROLES":
			return roles
		}
		return ""
	})

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, _ := st.CreateTenant("商家A")
	tenB, _ := st.CreateTenant("商家B")
	s1, _ := st.CreateStore(tenA.ID, "一店", "地址1", "test")
	s2, _ := st.CreateStore(tenA.ID, "二店", "地址2", "test")
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
		{tenA.ID, "usr_ag1", "agent"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed %s: %v", m.principal, err)
		}
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_sm_a", "store_manager", "sm", "test", true, s1.ID); err != nil {
		t.Fatalf("seed sm: %v", err)
	}
	rel, err := st.CreateAgencyRelation(tenA.ID, "usr_ag1", "usr_owner_a", "代运营")
	if err != nil {
		t.Fatalf("seed relation: %v", err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner-a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff-a@example.com"},
		"sess-sm-a":    {"usr_sm_a", "sm-a@example.com"},
		"sess-owner-b": {"usr_owner_b", "owner-b@example.com"},
		"sess-agent1":  {"usr_ag1", "agent1@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upSrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upSrv.Client()}

	srv := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &authzFixture{s: srv, ts: ts, tenA: tenA.ID, tenB: tenB.ID, st1: s1.ID, st2: s2.ID, relA: rel.ID}
}

func (f *authzFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	return doJSON(t, f.ts, method, path, session, tenant, body, "", "")
}

// doService acts as a trusted service producer (X-Service-ID seam).
func (f *authzFixture) doService(t *testing.T, method, path, serviceID, serviceToken, body string) (int, map[string]any) {
	t.Helper()
	return doJSON(t, f.ts, method, path, "", "", body, serviceID, serviceToken)
}

func doJSON(t *testing.T, ts *httptest.Server, method, path, session, tenant, body, serviceID, serviceToken string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if serviceID != "" {
		req.Header.Set("X-Service-ID", serviceID)
		req.Header.Set("X-Service-Token", serviceToken)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// ---- 1. off 基线:统一 404 -------------------------------------------------------

func TestAuthzAccessOffBaseline404(t *testing.T) {
	f := newAuthzFixture(t, false)
	paths := []struct{ method, path string }{
		{"GET", "/api/v1/access/effective"},
		{"GET", "/api/v1/access/audit"},
		{"POST", "/api/v1/template-assignments"},
		{"GET", "/api/v1/template-assignments"},
		{"GET", "/api/v1/template-assignments/tas_x"},
		{"PATCH", "/api/v1/template-assignments/tas_x"},
		{"POST", "/api/v1/ops-grants"},
		{"GET", "/api/v1/ops-grants"},
		{"DELETE", "/api/v1/ops-grants/ops_x"},
	}
	for _, p := range paths {
		status, _ := f.do(t, p.method, p.path, "sess-owner-a", f.tenA, `{}`)
		if status != http.StatusNotFound {
			t.Fatalf("%s %s off = %d, want 404 (invisible surface)", p.method, p.path, status)
		}
	}
}

// ---- 2. GET /access/effective:角色矩阵 -------------------------------------------

func TestAccessEffectiveOwnerMatrix(t *testing.T) {
	f := newAuthzFixture(t, true)
	status, out := f.do(t, "GET", "/api/v1/access/effective", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("owner access = %d %v", status, out)
	}
	perms := stringList(out["permissions"])
	for _, want := range []string{"storefront:configure", "action:configure", "action:channel_target",
		"action:manage_deal", "billing:view", "billing:manage_payer"} {
		if !contains(perms, want) {
			t.Fatalf("owner missing %s in %v", want, perms)
		}
	}
	if contains(perms, "ops:") {
		t.Fatal("org_owner must never hold ops:* (trusted grants only)")
	}
	if ids := stringList(out["allowed_store_ids"]); len(ids) != 0 {
		t.Fatalf("owner allowed_store_ids must be empty(全门店), got %v", ids)
	}
	if out["role"] != "org_owner" {
		t.Fatalf("role = %v", out["role"])
	}
}

func TestAccessEffectiveManagerAndStaff(t *testing.T) {
	f := newAuthzFixture(t, true)
	status, out := f.do(t, "GET", "/api/v1/access/effective", "sess-sm-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("manager access = %d %v", status, out)
	}
	if ids := stringList(out["allowed_store_ids"]); len(ids) != 1 || ids[0] != f.st1 {
		t.Fatalf("manager allowed_store_ids = %v, want [%s]", ids, f.st1)
	}
	perms := stringList(out["permissions"])
	for _, banned := range []string{"billing:view", "billing:manage_payer", "action:manage_deal"} {
		if contains(perms, banned) {
			t.Fatalf("manager must not hold %s", banned)
		}
	}

	// legacy staff: zero NEW dimensions (T0 semantics untouched, fail-closed)
	status, out = f.do(t, "GET", "/api/v1/access/effective", "sess-staff-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("staff access = %d %v", status, out)
	}
	if perms := stringList(out["permissions"]); len(perms) != 0 {
		t.Fatalf("legacy staff permissions = %v, want none", perms)
	}
}

func TestAccessEffectiveAgentGrantFlow(t *testing.T) {
	f := newAuthzFixture(t, true)

	// no grant yet: zero new dimensions
	_, out := f.do(t, "GET", "/api/v1/access/effective", "sess-agent1", f.tenA, "")
	if perms := stringList(out["permissions"]); len(perms) != 0 {
		t.Fatalf("agent without grant = %v, want none", perms)
	}

	// owner grants storefront+renewal scoped to store 1, with payer ref
	body := fmt.Sprintf(`{"allowed_actions":["storefront","renewal"],"store_scope_set":[%q],"valid_until":"2027-01-01T00:00:00Z","payer_grant_ref":"pgr_9","grant_revision":0,"reason":"年度代运营授权"}`, f.st1)
	status, out := f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-owner-a", f.tenA, body)
	if status != 200 {
		t.Fatalf("scope set = %d %v", status, out)
	}
	if rev, _ := out["grant_revision"].(float64); rev != 1 {
		t.Fatalf("revision = %v, want 1", out["grant_revision"])
	}

	_, out = f.do(t, "GET", "/api/v1/access/effective", "sess-agent1", f.tenA, "")
	perms := stringList(out["permissions"])
	if !contains(perms, "storefront:configure") || !contains(perms, "billing:view") || !contains(perms, "billing:manage_payer") {
		t.Fatalf("agent granted perms = %v", perms)
	}
	if contains(perms, "action:configure") {
		t.Fatal("action not granted must be absent")
	}
	ids := stringList(out["allowed_store_ids"])
	if len(ids) != 1 || ids[0] != f.st1 {
		t.Fatalf("agent allowed_store_ids = %v, want [%s]", ids, f.st1)
	}
	rel, _ := out["relation"].(map[string]any)
	if rel == nil || rel["grant_revision"].(float64) != 1 || rel["payer_granted"] != true {
		t.Fatalf("relation snapshot = %v", rel)
	}

	// stale revision refused (STALE_VERSION)
	body = fmt.Sprintf(`{"allowed_actions":["storefront"],"store_scope_set":[],"grant_revision":0,"reason":"r"}`)
	status, out = f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-owner-a", f.tenA, body)
	if status != http.StatusConflict || out["error"] != "stale_version" {
		t.Fatalf("stale scope = %d %v, want 409 stale_version", status, out)
	}

	// reason required
	body = fmt.Sprintf(`{"allowed_actions":["storefront"],"store_scope_set":[],"grant_revision":1}`)
	status, out = f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-owner-a", f.tenA, body)
	if status != http.StatusBadRequest || out["error"] != "reason_required" {
		t.Fatalf("missing reason = %d %v, want 400 reason_required", status, out)
	}

	// agent can never self-expand
	body = fmt.Sprintf(`{"allowed_actions":["storefront","action","renewal"],"store_scope_set":[],"grant_revision":1,"reason":"self grant"}`)
	status, _ = f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-agent1", f.tenA, body)
	if status != http.StatusForbidden {
		t.Fatalf("agent self-expand = %d, want 403", status)
	}

	// another tenant's owner cannot scope A's relation (masked 404)
	status, _ = f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-owner-b", f.tenB,
		`{"allowed_actions":["storefront"],"grant_revision":1,"reason":"x"}`)
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant scope = %d, want 404", status)
	}
}

// T17 核心正例:撤销代理后,同一会话下一请求立即 403(实时 authz 回填)。
func TestAgencyRevokeImmediate403(t *testing.T) {
	f := newAuthzFixture(t, true)

	// grant scope so the agent holds real capabilities first
	body := fmt.Sprintf(`{"allowed_actions":["storefront"],"store_scope_set":[%q],"grant_revision":0,"reason":"r"}`, f.st1)
	if status, out := f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-owner-a", f.tenA, body); status != 200 {
		t.Fatalf("scope set = %d %v", status, out)
	}
	if status, _ := f.do(t, "GET", "/api/v1/stores", "sess-agent1", f.tenA, ""); status != 200 {
		t.Fatalf("agent read before revoke = %d, want 200", status)
	}

	// revoke with reason (M13 撤回操作闭环)
	status, out := f.do(t, "DELETE", "/api/v1/agency/relations/"+f.relA, "sess-owner-a", f.tenA, `{"reason":"合同终止"}`)
	if status != 200 {
		t.Fatalf("revoke = %d %v", status, out)
	}

	// same session, next request: immediately 403 (fail-closed)
	status, out = f.do(t, "GET", "/api/v1/stores", "sess-agent1", f.tenA, "")
	if status != http.StatusForbidden {
		t.Fatalf("agent read after revoke = %d %v, want 403 (T17)", status, out)
	}
	// access/effective also dead
	status, _ = f.do(t, "GET", "/api/v1/access/effective", "sess-agent1", f.tenA, "")
	if status != http.StatusForbidden {
		t.Fatalf("agent access after revoke = %d, want 403", status)
	}
}

// ---- 3. 模板下发 ------------------------------------------------------------------

func TestTemplateAssignmentDistribution(t *testing.T) {
	f := newAuthzFixture(t, true)

	// multi-store distribution with per-store report
	body := fmt.Sprintf(`{"template_ref":"tpl_hero","source_version":"v3","store_ids":[%q,%q,"sto_missing"],"allowed_override_fields":["hero_title"],"request_key":"req_1"}`, f.st1, f.st2)
	status, out := f.do(t, "POST", "/api/v1/template-assignments", "sess-owner-a", f.tenA, body)
	if status != http.StatusMultiStatus && status != 200 && status != 201 {
		t.Fatalf("distribute = %d %v", status, out)
	}
	items := stringListAny(out["items"])
	if len(items) != 3 {
		t.Fatalf("per-store report = %d items, want 3 (部分失败逐项报告)", len(items))
	}
	okCount, failCount := 0, 0
	for _, m := range items {
		if m["ok"] == true {
			okCount++
		} else {
			failCount++
		}
	}
	if okCount != 2 || failCount != 1 {
		t.Fatalf("report ok=%d fail=%d, want 2/1", okCount, failCount)
	}

	// idempotent retry: same request_key → the previously-failed store still
	// fails (207 逐项如实),the ok rows answer per-row replayed;no duplicates.
	status, out = f.do(t, "POST", "/api/v1/template-assignments", "sess-owner-a", f.tenA, body)
	if status != http.StatusMultiStatus {
		t.Fatalf("retry with failed store = %d, want 207 (逐项如实)", status)
	}
	replayedCount := 0
	for _, m := range stringListAny(out["items"]) {
		if m["replayed"] == true {
			replayedCount++
		}
	}
	if replayedCount != 2 {
		t.Fatalf("retry replayed rows = %d, want 2 (不重复创建)", replayedCount)
	}
	_, list := f.do(t, "GET", "/api/v1/template-assignments", "sess-owner-a", f.tenA, "")
	if got := len(stringListAny(list["items"])); got != 2 {
		t.Fatalf("list after retry = %d, want 2 (不重复创建)", got)
	}

	// pure replay (all stores exist now): 200 + top-level replayed
	body = fmt.Sprintf(`{"template_ref":"tpl_hero","source_version":"v3","store_ids":[%q,%q],"allowed_override_fields":["hero_title"],"request_key":"req_1"}`, f.st1, f.st2)
	status, out = f.do(t, "POST", "/api/v1/template-assignments", "sess-owner-a", f.tenA, body)
	if status != 200 || out["replayed"] != true {
		t.Fatalf("pure replay = %d %v, want 200 replayed", status, out)
	}

	// reserved fields refused (门店目标/账号/二维码/Wi-Fi 密码永不跨店复制)
	body = fmt.Sprintf(`{"template_ref":"tpl","source_version":"v1","store_ids":[%q],"allowed_override_fields":["wifi_password"],"request_key":"req_2"}`, f.st1)
	status, out = f.do(t, "POST", "/api/v1/template-assignments", "sess-owner-a", f.tenA, body)
	if status != http.StatusBadRequest || out["error"] != "reserved_field" {
		t.Fatalf("reserved field = %d %v, want 400 reserved_field", status, out)
	}

	// manager: own-store read only, distribution denied
	status, _ = f.do(t, "POST", "/api/v1/template-assignments", "sess-sm-a", f.tenA,
		fmt.Sprintf(`{"template_ref":"tpl","source_version":"v1","store_ids":[%q],"request_key":"m1"}`, f.st1))
	if status != http.StatusForbidden {
		t.Fatalf("manager distribute = %d, want 403", status)
	}
	status, out = f.do(t, "GET", "/api/v1/template-assignments", "sess-sm-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("manager list = %d %v", status, out)
	}
	for _, m := range stringListAny(out["items"]) {
		if m["store_id"] != f.st1 {
			t.Fatalf("manager sees foreign store %v", m["store_id"])
		}
	}

	// foreign tenant sees nothing (masked)
	status, out = f.do(t, "GET", "/api/v1/template-assignments", "sess-owner-b", f.tenB, "")
	if status != 200 {
		t.Fatalf("tenant B list = %d %v", status, out)
	}
	if len(stringListAny(out["items"])) != 0 {
		t.Fatal("cross-tenant template leak")
	}

	// cancel (总部撤回)
	items = stringListAny(list["items"])
	tasID, _ := items[0]["id"].(string)
	status, out = f.do(t, "PATCH", "/api/v1/template-assignments/"+tasID, "sess-owner-a", f.tenA, `{"status":"cancelled","reason":"模板作废"}`)
	if status != 200 || out["status"] != "cancelled" {
		t.Fatalf("cancel = %d %v", status, out)
	}
}

// ---- 4. ops grants:只来自受信服务端 ------------------------------------------------

func TestOpsGrantTrustedSeamOnly(t *testing.T) {
	f := newAuthzFixture(t, true)

	// merchant session can NEVER self-grant ops (surface refuses non-service callers)
	status, out := f.do(t, "POST", "/api/v1/ops-grants", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"tenant_id":%q,"principal_ref":"usr_owner_a","commands":["ops:inspect"]}`, f.tenA))
	if status != http.StatusUnauthorized {
		t.Fatalf("merchant ops self-grant = %d %v, want 401", status, out)
	}

	// trusted service producer mints a grant
	body := fmt.Sprintf(`{"tenant_id":%q,"principal_ref":"usr_owner_a","commands":["ops:inspect"],"expires_at":"2027-01-01T00:00:00Z","reason":"平台运营支持"}`, f.tenA)
	status, out = f.doService(t, "POST", "/api/v1/ops-grants", "svc-ops-console", "ops-service-token", body)
	if status != 201 {
		t.Fatalf("service grant = %d %v", status, out)
	}
	grantID, _ := out["id"].(string)

	// owner access/effective now carries the ops command (within grant scope)
	_, out = f.do(t, "GET", "/api/v1/access/effective", "sess-owner-a", f.tenA, "")
	if ops := stringListAny(out["ops_permissions"]); len(ops) == 0 {
		t.Fatalf("ops permissions empty: %v", out)
	}

	// fake ops declaration via headers/body never works: another service id unknown
	status, _ = f.doService(t, "POST", "/api/v1/ops-grants", "svc_fake", "whatever", body)
	if status == 201 {
		t.Fatal("unknown service must be refused")
	}

	// revoke → immediately dead
	status, _ = f.doService(t, "DELETE", "/api/v1/ops-grants/"+grantID, "svc-ops-console", "ops-service-token", "")
	if status != 200 {
		t.Fatalf("revoke = %d", status)
	}
	_, out = f.do(t, "GET", "/api/v1/access/effective", "sess-owner-a", f.tenA, "")
	if ops := stringListAny(out["ops_permissions"]); len(ops) != 0 {
		t.Fatalf("revoked grant still visible: %v", ops)
	}
	// issuer view (含墓碑)
	status, out = f.doService(t, "GET", "/api/v1/ops-grants", "svc-ops-console", "ops-service-token", "")
	if status != 200 || len(stringListAny(out["items"])) != 1 {
		t.Fatalf("issuer list = %d %v", status, out)
	}
}

// ---- 5. 审计 ----------------------------------------------------------------------

func TestAuthzAuditTrailReadable(t *testing.T) {
	f := newAuthzFixture(t, true)

	// privileged write with reason → audited
	body := fmt.Sprintf(`{"allowed_actions":["storefront"],"store_scope_set":[],"grant_revision":0,"reason":"年度授权"}`)
	if status, out := f.do(t, "PUT", "/api/v1/agency/relations/"+f.relA+"/scope", "sess-owner-a", f.tenA, body); status != 200 {
		t.Fatalf("scope set = %d %v", status, out)
	}
	// member update with reason → audited (resolve the member ROW id first)
	status, members := f.do(t, "GET", "/api/v1/admin/members", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("member list = %d %v", status, members)
	}
	staffID := ""
	for _, m := range stringListAny(members["items"]) {
		if m["principal_ref"] == "usr_staff_a" {
			staffID, _ = m["id"].(string)
		}
	}
	if staffID == "" {
		t.Fatal("staff member row not found in list")
	}
	if status, out := f.do(t, "PATCH", "/api/v1/admin/members/"+staffID, "sess-owner-a", f.tenA,
		`{"enabled":false,"reason":"离职停用"}`); status != 200 {
		t.Fatalf("member patch = %d %v", status, out)
	}

	status, out := f.do(t, "GET", "/api/v1/access/audit", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("audit read = %d %v", status, out)
	}
	items := stringListAny(out["items"])
	if len(items) < 2 {
		t.Fatalf("audit items = %d, want >=2", len(items))
	}
	sawScope, sawMember := false, false
	for _, m := range items {
		if m["action"] == "agency_scope_set" && m["reason"] == "年度授权" {
			sawScope = true
			if m["before_json"] == nil || m["after_json"] == nil {
				t.Fatal("audit must carry before/after values")
			}
		}
		if m["action"] == "member_disable" && m["reason"] == "离职停用" {
			sawMember = true
		}
	}
	if !sawScope || !sawMember {
		t.Fatalf("audit missing entries (scope=%v member=%v): %v", sawScope, sawMember, items)
	}

	// non-owner cannot read the audit trail
	status, _ = f.do(t, "GET", "/api/v1/access/audit", "sess-sm-a", f.tenA, "")
	if status != http.StatusForbidden {
		t.Fatalf("manager audit read = %d, want 403", status)
	}
	// target filter
	status, out = f.do(t, "GET", "/api/v1/access/audit?target_type=agency_relation", "sess-owner-a", f.tenA, "")
	if status != 200 || len(stringListAny(out["items"])) == 0 {
		t.Fatalf("target-filtered audit = %d %v", status, out)
	}
}

// ---- helpers -----------------------------------------------------------------------

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringListAny(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, v) {
			return true
		}
	}
	return false
}
