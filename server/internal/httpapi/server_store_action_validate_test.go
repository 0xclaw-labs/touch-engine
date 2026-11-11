package httpapi

import (
	"net/http"
	"testing"
)

// 会让这个测试失败的生产改动：validate 写任何行/触发任何生成、跨租户 action id
// 可被他人校验（存在性泄漏）、或把平台动作说成已验证/已成功。

// TestStoreActionValidateHonestEvidence 覆盖合法行三分证据、legacy 坏目标行、
// 权限矩阵与只读性。
func TestStoreActionValidateHonestEvidence(t *testing.T) {
	af := newActionsHTTPFixture(t, false)
	f := af.f

	// 建一个合法动作。
	status, _, saved := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, `{"expected_revision":0,"items":[
		{"catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/ok","order":1,"enabled":true}
	]}`)
	mustEqual(t, status, http.StatusOK)
	actionID := itemsOf(t, saved)[0]["action_id"].(string)

	// 造 legacy 态行：撤销 + 过期 + 非正式目标（直接入库，模拟 0044 迁移产物）。
	legacy := func(id, catalog, target, extra string) {
		t.Helper()
		if _, err := f.s.St.DB.Exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
			presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,created_at,updated_at)
			VALUES('` + id + `','` + af.tenA + `','` + af.stoA + `','` + catalog + `','','review','','旧点评','` + target + `',
			'link',9,1,1,0,'','cmp_old','review','now','now') ` + extra); err != nil {
			t.Fatalf("legacy seed %s: %v", id, err)
		}
	}
	legacy("sa_legacy_evil", "A09", "http://127.0.0.1/legacy", "")
	legacy("sa_legacy_revoked", "A10", "https://www.amap.com/x", "")
	if _, err := f.s.St.DB.Exec(`UPDATE store_actions SET revoked=1 WHERE id='sa_legacy_revoked'`); err != nil {
		t.Fatal(err)
	}
	legacy("sa_legacy_expired", "A12", "https://www.meituan.com/x", "")
	if _, err := f.s.St.DB.Exec(`UPDATE store_actions SET expires_at='2020-01-01T00:00:00Z' WHERE id='sa_legacy_expired'`); err != nil {
		t.Fatal(err)
	}
	legacy("sa_legacy_pending", "", "https://shop.example.com/pending", "")

	revBefore := revisionOf(t, f, af.stoA)
	rowsBefore := actionRows(t, f)

	// 合法行：field_errors 空、三分证据（web configured / client unverified / platform unknown）、
	// 准入 evaluated=false。
	status, _, good := f.do(t, "POST", "/api/v1/actions/"+actionID+"/validate", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if good["action_id"] != actionID || good["action_revision"] != float64(1) {
		t.Fatalf("validate id/revision = %v/%v", good["action_id"], good["action_revision"])
	}
	if errs := fieldErrorList(t, good["field_errors"]); len(errs) != 0 {
		t.Fatalf("field_errors = %v", errs)
	}
	if codes := codeList(t, good["reason_codes"]); len(codes) != 0 {
		t.Fatalf("reason_codes = %v", codes)
	}
	methods := methodStates(t, good["method_states"])
	if methods["web"] != "configured" || methods["client_launch"] != "unverified" || methods["platform_action"] != "unknown" {
		t.Fatalf("method states = %v", methods)
	}
	admission, _ := good["reward_admission"].(map[string]any)
	if admission["evaluated"] != false {
		t.Fatalf("reward admission must not be evaluated this ticket: %v", admission)
	}
	if good["reward_triggered"] != false || good["publish_success"] != false || good["creates_lead"] != false {
		t.Fatalf("validate claimed an effect: %v", good)
	}

	// legacy 坏目标：target_not_official + web closed address_not_official。
	status, _, evil := f.do(t, "POST", "/api/v1/actions/sa_legacy_evil/validate", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if codes := codeList(t, evil["reason_codes"]); len(codes) != 1 || codes[0] != "target_not_official" {
		t.Fatalf("evil reason_codes = %v", evil["reason_codes"])
	}
	if methods := methodStates(t, evil["method_states"]); methods["web"] != "closed" {
		t.Fatalf("evil web state = %v", methods)
	}
	if webReason := methodReason(t, evil["method_states"], "web"); webReason != "address_not_official" {
		t.Fatalf("evil web reason = %q", webReason)
	}

	// 撤销 / 过期 / 待映射。
	status, _, revoked := f.do(t, "POST", "/api/v1/actions/sa_legacy_revoked/validate", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if codes := codeList(t, revoked["reason_codes"]); len(codes) != 1 || codes[0] != "revoked" {
		t.Fatalf("revoked codes = %v", revoked["reason_codes"])
	}
	status, _, expired := f.do(t, "POST", "/api/v1/actions/sa_legacy_expired/validate", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if codes := codeList(t, expired["reason_codes"]); len(codes) != 1 || codes[0] != "expired" {
		t.Fatalf("expired codes = %v", expired["reason_codes"])
	}
	status, _, pending := f.do(t, "POST", "/api/v1/actions/sa_legacy_pending/validate", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if codes := codeList(t, pending["reason_codes"]); len(codes) != 1 || codes[0] != "catalog_unassigned" {
		t.Fatalf("pending codes = %v", pending["reason_codes"])
	}

	// 只读性：revision 与行数都不变。
	if revisionOf(t, f, af.stoA) != revBefore || actionRows(t, f) != rowsBefore {
		t.Fatal("validate mutated store action state")
	}

	// 权限：本店经理可校验；他店经理 404 掩码；跨租户 404 掩码；未知 id 404；未登录 401。
	status, _, _ = f.do(t, "POST", "/api/v1/actions/"+actionID+"/validate", af.sessMgrA, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	status, _, masked := f.do(t, "POST", "/api/v1/actions/"+actionID+"/validate", af.sessMgrB, af.tenA, "")
	if status != http.StatusNotFound {
		t.Fatalf("other-store manager validate = %d %v", status, masked)
	}
	status, _, foreign := f.do(t, "POST", "/api/v1/actions/"+actionID+"/validate", af.sessOwnerB, af.tenB, "")
	// 跨租户按仓内既有纪律答 403 cross_tenant（requireIdentity 文档口径）。
	if status != http.StatusForbidden {
		t.Fatalf("cross-tenant validate = %d %v", status, foreign)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/actions/sa_missing/validate", af.sessOwner, af.tenA, "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown validate = %d", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/actions/"+actionID+"/validate", "", af.tenA, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("guest validate = %d", status)
	}
}

// TestStoreActionValidateGroupBuyPlatformGate 是 HUI-4590 A19-A21 target
// 验证器接线验收（wave2 集成）：团购类目（A19 美团/A20 点评/A21 抖音）的
// target 除公网 https 外还必须是该类目自己平台的官方商品页域；失败原因如实
// 进三分证据（field_errors/reason_codes/web 态+原因）。会让本测试失败的改动：
// 团购目标串挂他平台域/后缀伪装域被放行、非团购类目被平台门误伤、validate
// 变更任何状态。
func TestStoreActionValidateGroupBuyPlatformGate(t *testing.T) {
	af := newActionsHTTPFixture(t, false)
	f := af.f

	// 正例与可写入负例走 PUT：https 非平台域/伪装域/串挂渠道都能通过 PUT 的
	// 通用公网 https 门（写面不拦），由 validate 面按 catalog_code 判别如实报。
	status, _, saved := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, `{"expected_revision":0,"items":[
		{"catalog_code":"A19","label":"美团团购","target_ref":"https://www.meituan.com/deal/8891","order":1,"enabled":true},
		{"catalog_code":"A20","label":"点评团购","target_ref":"https://evil.example.com/deal/1","order":2,"enabled":true},
		{"catalog_code":"A19","label":"伪装域","target_ref":"https://www.meituan.com.evil.com/x","order":3,"enabled":true},
		{"catalog_code":"A19","label":"串挂点评","target_ref":"https://www.dianping.com/shop/9","order":4,"enabled":true},
		{"catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":5,"enabled":true}
	]}`)
	mustEqual(t, status, http.StatusOK)
	items := itemsOf(t, saved)
	if len(items) != 5 {
		t.Fatalf("5 actions expected, got %d", len(items))
	}

	// http 目标仍先撞通用公网 https 门（legacy 行直插，PUT 拒收 http 写入）。
	if _, err := f.s.St.DB.Exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_legacy_gb_http','` + af.tenA + `','` + af.stoA + `','A21','douyin','groupbuy','','旧抖音团购','http://www.douyin.com/x',
		'link',9,1,1,0,'','cmp_old','groupbuy','now','now')`); err != nil {
		t.Fatalf("legacy seed: %v", err)
	}
	// 只读基线（在全部 validate 调用之前捕获）。
	revBefore := revisionOf(t, f, af.stoA)
	rowsBefore := actionRows(t, f)
	idOf := func(order float64) string {
		for _, it := range items {
			if it["order"].(float64) == order {
				return it["action_id"].(string)
			}
		}
		t.Fatalf("action with order %v missing", order)
		return ""
	}
	validate := func(actionID string) ([]map[string]any, []string, map[string]string, string) {
		t.Helper()
		vstatus, _, doc := f.do(t, "POST", "/api/v1/actions/"+actionID+"/validate", af.sessOwner, af.tenA, "")
		mustEqual(t, vstatus, http.StatusOK)
		return fieldErrorList(t, doc["field_errors"]), codeList(t, doc["reason_codes"]),
			methodStates(t, doc["method_states"]), methodReason(t, doc["method_states"], "web")
	}

	// 正例：合法美团团单 URL（公网 https + 平台官方域）→ 三分证据全干净。
	errs, codes, methods, webReason := validate(idOf(1))
	if len(errs) != 0 || len(codes) != 0 || methods["web"] != "configured" || webReason != "" {
		t.Fatalf("legit group-buy URL must pass clean: errs=%v codes=%v methods=%v reason=%q", errs, codes, methods, webReason)
	}

	// 负例：https 非平台域 → target_not_platform_official + web closed。
	_, codes, methods, webReason = validate(idOf(2))
	if len(codes) != 1 || codes[0] != "target_not_platform_official" {
		t.Fatalf("non-platform https codes = %v", codes)
	}
	if methods["web"] != "closed" || webReason != "address_not_platform_official" {
		t.Fatalf("non-platform https web evidence = %v/%q", methods, webReason)
	}

	// 负例：后缀伪装域（meituan.com.evil.com）→ 同门拒绝。
	_, codes, _, _ = validate(idOf(3))
	if len(codes) != 1 || codes[0] != "target_not_platform_official" {
		t.Fatalf("impostor-suffix codes = %v", codes)
	}

	// 负例：串挂他平台域名（A19 挂点评域）→ 同门拒绝（渠道错挂）。
	_, codes, _, _ = validate(idOf(4))
	if len(codes) != 1 || codes[0] != "target_not_platform_official" {
		t.Fatalf("cross-channel codes = %v", codes)
	}

	// 非团购类目不受平台门约束：A17 Wi-Fi 任意公网 https 域照常干净。
	errs, codes, methods, webReason = validate(idOf(5))
	if len(errs) != 0 || len(codes) != 0 || methods["web"] != "configured" || webReason != "" {
		t.Fatalf("non-groupbuy code must not hit the platform gate: errs=%v codes=%v", errs, codes)
	}

	// http 目标仍先撞通用公网 https 门（legacy 行已直插，PUT 拒收 http 写入）。
	_, codes, _, webReason = validate("sa_legacy_gb_http")
	if len(codes) != 1 || codes[0] != "target_not_official" || webReason != "address_not_official" {
		t.Fatalf("http target must answer the general https gate: codes=%v reason=%q", codes, webReason)
	}

	// 只读性：validate 不改任何行（revision 与行数原样）。
	if revisionOf(t, f, af.stoA) != revBefore || actionRows(t, f) != rowsBefore {
		t.Fatal("group-buy gate validate mutated store action state")
	}
}

func revisionOf(t *testing.T, f *fixture, storeID string) int {
	t.Helper()
	var rev int
	if err := f.s.St.DB.QueryRow(`SELECT config_revision FROM store_action_configs WHERE store_id=?`, storeID).Scan(&rev); err != nil {
		t.Fatalf("revision: %v", err)
	}
	return rev
}

func actionRows(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM store_actions`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func fieldErrorList(t *testing.T, raw any) []map[string]any {
	t.Helper()
	list, _ := raw.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["field"] == nil || row["code"] == nil {
			t.Fatalf("field error shape = %v", row)
		}
		out = append(out, row)
	}
	return out
}

func codeList(t *testing.T, raw any) []string {
	t.Helper()
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func methodStates(t *testing.T, raw any) map[string]string {
	t.Helper()
	list, _ := raw.([]any)
	out := map[string]string{}
	for _, item := range list {
		row, _ := item.(map[string]any)
		method, _ := row["method"].(string)
		state, _ := row["state"].(string)
		out[method] = state
	}
	if _, ok := out["web"]; !ok || len(out) != 3 {
		t.Fatalf("method states incomplete: %v", raw)
	}
	return out
}

func methodReason(t *testing.T, raw any, method string) string {
	t.Helper()
	list, _ := raw.([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row["method"] == method {
			reason, _ := row["reason"].(string)
			return reason
		}
	}
	t.Fatalf("method %s missing", method)
	return ""
}
