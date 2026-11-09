package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// actionCapsGet GETs a public action-capabilities surface.
func actionCapsGet(t *testing.T, f *admissionFixture, path string) (int, map[string]any) {
	t.Helper()
	return f.get(t, path, "", "")
}

// actionRoute finds one exploded route by method+environment in the payload.
func actionRoute(t *testing.T, body map[string]any, method, environment string) map[string]any {
	t.Helper()
	routes, ok := body["routes"].([]any)
	if !ok {
		t.Fatalf("payload has no routes: %v", body)
	}
	for _, raw := range routes {
		r, _ := raw.(map[string]any)
		if r["method"] == method && r["environment"] == environment {
			return r
		}
	}
	t.Fatalf("no route method=%s environment=%s in %d routes", method, environment, len(routes))
	return nil
}

func routeBool(r map[string]any, key string) bool {
	v, _ := r[key].(bool)
	return v
}

func routeStr(r map[string]any, key string) string {
	v, _ := r[key].(string)
	return v
}

// TestPublicActionCapabilitiesHonest: B04 公开面按动作解析——抖音通用投稿
// not_admitted 且带 human 说明;手工绕过行同样拒绝;环境过滤生效;非法环境 400;
// 未知动作 404。
func TestPublicActionCapabilitiesHonest(t *testing.T) {
	f := newAdmissionFixture(t, true)

	status, body := actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities")
	if status != http.StatusOK {
		t.Fatalf("A01 = %d, want 200: %v", status, body)
	}
	if body["catalog_code"] != "A01" || body["action_kind"] != "publish" || body["label"] != "发抖音视频" {
		t.Fatalf("action header wrong: %v", body)
	}
	r := actionRoute(t, body, "h5_share", "desktop_browser")
	if routeBool(r, "allowed") || routeStr(r, "reason") != "not_admitted" {
		t.Fatalf("douyin h5_share = allowed=%v reason=%s, want false/not_admitted", routeBool(r, "allowed"), routeStr(r, "reason"))
	}
	if routeStr(r, "reason_text") == "" {
		t.Fatal("refused route must carry a human explanation (reason_text)")
	}
	// 技术能力与不准入同时表达(四分独立)。
	if routeStr(r, "navigation") != "supported" || routeStr(r, "media_handoff") != "supported" ||
		routeStr(r, "reward_eligibility") != "unsupported" {
		t.Fatalf("four-way = nav=%s media=%s reward=%s, want supported/supported/unsupported",
			routeStr(r, "navigation"), routeStr(r, "media_handoff"), routeStr(r, "reward_eligibility"))
	}
	// 手工绕过行也是显式 not_admitted(不做 unknown 兜底)。
	m := actionRoute(t, body, "manual_material", "desktop_browser")
	if routeBool(m, "allowed") || routeStr(m, "reason") != "not_admitted" {
		t.Fatalf("douyin manual_material must be explicitly refused, got %v/%s", routeBool(m, "allowed"), routeStr(m, "reason"))
	}

	// 环境过滤:ios_safari 请求返回该环境值。
	status, body = actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities?environment=ios_safari")
	if status != http.StatusOK {
		t.Fatalf("A01 env filter = %d", status)
	}
	r = actionRoute(t, body, "h5_share", "ios_safari")
	if routeStr(r, "environment") != "ios_safari" {
		t.Fatalf("route environment = %s", routeStr(r, "environment"))
	}
	routes, _ := body["routes"].([]any)
	for _, raw := range routes {
		rr, _ := raw.(map[string]any)
		if rr["environment"] != "ios_safari" {
			t.Fatalf("env filter leaked %v", rr["environment"])
		}
	}

	// 非法环境 = 400;未知动作 = 404。
	if status, _ = actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities?environment=chrome_os"); status != http.StatusBadRequest {
		t.Fatalf("bogus env = %d, want 400", status)
	}
	if status, _ = actionCapsGet(t, f, "/api/v1/public/actions/A99/capabilities"); status != http.StatusNotFound {
		t.Fatalf("unknown action = %d, want 404", status)
	}
	if status, _ = actionCapsGet(t, f, "/api/v1/public/actions/a01/capabilities"); status != http.StatusOK {
		t.Fatalf("lowercase code must resolve: %d", status)
	}
}

// TestPublicActionCapabilitiesPendingExternal: 资格未知=PENDING_EXTERNAL 语义
// (pending_external=true, promised=false);promised 只出现在诚实可完成态。
func TestPublicActionCapabilitiesPendingExternal(t *testing.T) {
	f := newAdmissionFixture(t, true)

	_, body := actionCapsGet(t, f, "/api/v1/public/actions/A04/capabilities")
	r := actionRoute(t, body, "h5_share", "desktop_browser")
	if routeBool(r, "allowed") || !routeBool(r, "pending_external") || routeBool(r, "promised") {
		t.Fatalf("wechat h5 (未核实契约) = allowed=%v pending=%v promised=%v, want false/true/false "+
			"(unknown 永不自动当 manual 许可,不显示可完成态)", routeBool(r, "allowed"), routeBool(r, "pending_external"), routeBool(r, "promised"))
	}

	_, body = actionCapsGet(t, f, "/api/v1/public/actions/A02/capabilities")
	r = actionRoute(t, body, "js_media_handoff", "desktop_browser")
	if !routeBool(r, "pending_external") || routeBool(r, "promised") {
		t.Fatalf("xhs js (暂停新接入) pending=%v promised=%v, want true/false", routeBool(r, "pending_external"), routeBool(r, "promised"))
	}
	// 同动作的 manual 路线是可交付的诚实可完成态(admitted)。
	m := actionRoute(t, body, "manual_material", "desktop_browser")
	if !routeBool(m, "allowed") || !routeBool(m, "promised") || routeBool(m, "pending_external") {
		t.Fatalf("xhs manual = allowed=%v promised=%v pending=%v, want true/true/false",
			routeBool(m, "allowed"), routeBool(m, "promised"), routeBool(m, "pending_external"))
	}

	// 全量守卫:任何 promised=true 的路由必是 admitted 且(系统开目标 supported
	// ∨ manual_material)——不承诺语义的结构化证明。
	for _, code := range []string{"A01", "A02", "A04", "A08", "A22", "A27"} {
		_, body = actionCapsGet(t, f, "/api/v1/public/actions/"+code+"/capabilities")
		routes, _ := body["routes"].([]any)
		for _, raw := range routes {
			rr, _ := raw.(map[string]any)
			if routeBool(rr, "promised") {
				if rr["status"] != "admitted" {
					t.Fatalf("%s %s: promised with status=%v", code, rr["method"], rr["status"])
				}
				nav := routeStr(rr, "navigation")
				if nav != "supported" && rr["method"] != "manual_material" {
					t.Fatalf("%s %s: promised with nav=%s non-manual", code, rr["method"], nav)
				}
			}
			if routeBool(rr, "pending_external") && (routeBool(rr, "promised") || routeBool(rr, "allowed")) {
				t.Fatalf("%s %s: pending route shows completable state", code, rr["method"])
			}
		}
	}
}

// TestPublicActionCapabilitiesLinkShareNoInputs: A27 纯链接分享合法且无需
// 内容包/媒体授权;媒体发布路线要求真实采用版本。
func TestPublicActionCapabilitiesLinkShareNoInputs(t *testing.T) {
	f := newAdmissionFixture(t, true)

	_, body := actionCapsGet(t, f, "/api/v1/public/actions/A27/capabilities")
	r := actionRoute(t, body, "link_share", "desktop_browser")
	if !routeBool(r, "allowed") {
		t.Fatalf("A27 link_share must be allowed, got %v (%s)", routeBool(r, "allowed"), routeStr(r, "reason"))
	}
	inputs, _ := r["required_inputs"].([]any)
	if len(inputs) != 0 {
		t.Fatalf("A27 link_share required_inputs = %v, want [] (合法 link_share 不被视频库为空拦截)", inputs)
	}

	_, body = actionCapsGet(t, f, "/api/v1/public/actions/A02/capabilities")
	r = actionRoute(t, body, "js_media_handoff", "desktop_browser")
	inputs, _ = r["required_inputs"].([]any)
	if len(inputs) == 0 || inputs[0] != "content_pack_version" {
		t.Fatalf("A02 media route required_inputs = %v, want [content_pack_version]", inputs)
	}
}

// TestPublicActionCapabilitiesReviewsNavigationOnly: 点评类=navigation only +
// reward_eligibility=unsupported(裁定书钉子)。
func TestPublicActionCapabilitiesReviewsNavigationOnly(t *testing.T) {
	f := newAdmissionFixture(t, true)
	for _, code := range []string{"A08", "A09", "A10", "A11", "A12"} {
		_, body := actionCapsGet(t, f, "/api/v1/public/actions/"+code+"/capabilities")
		routes, _ := body["routes"].([]any)
		if len(routes) != 6 { // 一个 method × 六环境
			t.Fatalf("%s routes = %d, want 6", code, len(routes))
		}
		r := actionRoute(t, body, "direct_open", "desktop_browser")
		if routeStr(r, "navigation") != "supported" || routeStr(r, "media_handoff") != "unsupported" ||
			routeStr(r, "reward_eligibility") != "unsupported" {
			t.Fatalf("%s four-way = %s/%s/%s, want supported/unsupported/unsupported",
				code, routeStr(r, "navigation"), routeStr(r, "media_handoff"), routeStr(r, "reward_eligibility"))
		}
		if !strings.Contains(routeStr(r, "reason_text"), "评价") {
			t.Fatalf("%s reason_text must carry 评价纪律: %s", code, routeStr(r, "reason_text"))
		}
		if body["action_kind"] != "review" {
			t.Fatalf("%s action_kind = %v", code, body["action_kind"])
		}
	}
}

// TestPublicActionCapabilitiesSuggestedOnly: 建议补充 9 项带建议标签,必需 21 项不带。
func TestPublicActionCapabilitiesSuggestedOnly(t *testing.T) {
	f := newAdmissionFixture(t, true)
	_, body := actionCapsGet(t, f, "/api/v1/public/actions/A22/capabilities")
	if body["suggested_only"] != true {
		t.Fatalf("A22 suggested_only = %v, want true (建议补充标识不得丢失)", body["suggested_only"])
	}
	_, body = actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities")
	if body["suggested_only"] != false {
		t.Fatalf("A01 suggested_only = %v, want false", body["suggested_only"])
	}
}

// TestPublicActionCapabilitiesOffInvisible: 开关 off = 路由未注册+handler gate
// 统一 404(双保险同款)。
func TestPublicActionCapabilitiesOffInvisible(t *testing.T) {
	f := newAdmissionFixture(t, false)
	status, _ := actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities")
	if status != http.StatusNotFound {
		t.Fatalf("off = %d, want 404", status)
	}
}

// TestPublicActionCapabilitiesNoSecrets: 公开投影白名单——种子版本/载荷指纹/
// 租户作用域等内部字段绝不出现。
func TestPublicActionCapabilitiesNoSecrets(t *testing.T) {
	f := newAdmissionFixture(t, true)
	status, body := actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities")
	if status != http.StatusOK {
		t.Fatal(status)
	}
	for _, banned := range []string{"seed_version", "payload_hash", "tenant_scope", "id"} {
		if _, ok := body[banned]; ok {
			t.Fatalf("public payload leaks internal field %q", banned)
		}
	}
	routes, _ := body["routes"].([]any)
	for _, raw := range routes {
		r, _ := raw.(map[string]any)
		for _, banned := range []string{"seed_version", "payload_hash", "tenant_scope", "id"} {
			if _, ok := r[banned]; ok {
				t.Fatalf("public route leaks internal field %q", banned)
			}
		}
	}
}

// ---- Task 5: 管理端受控读写(internal token 平台权威 + 会话租户视图) ----------

// rawDo issues a request with explicit headers (no implicit internal token).
func rawDo(t *testing.T, f *admissionFixture, method, path, session string, hdr map[string]string, body string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant := f.tenA; tenant != "" && session != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func internalHdr() map[string]string {
	return map[string]string{"X-Internal-Token": "test-internal-secret", "Content-Type": "application/json"}
}

func noAuthHdr() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

// TestChannelActionsReadAuth: 无凭据 401;会话=租户视图('*'+本租户,scope=tenant);
// internal token=平台全量(scope=platform)。
func TestChannelActionsReadAuth(t *testing.T) {
	f := newAdmissionFixture(t, true)
	status, _ := rawDo(t, f, http.MethodGet, "/api/v1/channel-actions", "", noAuthHdr(), "")
	if status != http.StatusUnauthorized {
		t.Fatalf("no auth = %d, want 401", status)
	}
	// 会话读与 BFF 同形:internal token + 会话同时携带(纯 token 无会话才是平台面)。
	hdr := internalHdr()
	delete(hdr, "Content-Type")
	status, body := rawDo(t, f, http.MethodGet, "/api/v1/channel-actions", "sess-owner-a", hdr, "")
	if status != http.StatusOK || body["scope"] != "tenant" {
		t.Fatalf("session read = %d scope=%v, want 200/tenant", status, body["scope"])
	}
	recs, _ := body["records"].([]any)
	if len(recs) != 45*6 { // 45 行 × 6 环境展开
		t.Fatalf("tenant view records = %d, want %d", len(recs), 45*6)
	}
	status, body = rawDo(t, f, http.MethodGet, "/api/v1/channel-actions", "", internalHdr(), "")
	if status != http.StatusOK || body["scope"] != "platform" {
		t.Fatalf("internal read = %d scope=%v, want 200/platform", status, body["scope"])
	}
}

// TestChannelActionsUpsertControlled: 受控写入留审计;三态响应(created/updated/
// unchanged);缺 reason 400;非法枚举与 unknown 开奖励 422(模型层拒绝)。
func TestChannelActionsUpsertControlled(t *testing.T) {
	f := newAdmissionFixture(t, true)

	// 无 token → 401(requireInternal 中间件)。
	status, _ := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "sess-owner-a", noAuthHdr(), `{}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("no token upsert = %d, want 401 (商家会话永不写准入真值)", status)
	}

	rec := map[string]any{
		"catalog_code": "A91", "platform": "newplat", "method": "manual_material",
		"scenario": "store_promo", "action_kind": "publish", "label": "测试新动作",
		"suggested_only": true, "app_binding": "none", "tenant_scope": "*",
		"status": "unknown",
		"environments": map[string]any{
			"desktop_browser": map[string]string{"navigation": "unknown", "media_handoff": "unknown", "platform_receipt": "unknown", "reward_eligibility": "unknown"},
			"ios_safari":      map[string]string{"navigation": "unknown", "media_handoff": "unknown", "platform_receipt": "unknown", "reward_eligibility": "unknown"},
			"android_browser": map[string]string{"navigation": "unknown", "media_handoff": "unknown", "platform_receipt": "unknown", "reward_eligibility": "unknown"},
			"ios_wechat":      map[string]string{"navigation": "unknown", "media_handoff": "unknown", "platform_receipt": "unknown", "reward_eligibility": "unknown"},
			"android_wechat":  map[string]string{"navigation": "unknown", "media_handoff": "unknown", "platform_receipt": "unknown", "reward_eligibility": "unknown"},
			"target_app":      map[string]string{"navigation": "unknown", "media_handoff": "unknown", "platform_receipt": "unknown", "reward_eligibility": "unknown"},
		},
		"field_support": map[string]string{}, "required_inputs": []string{"content_pack_version"},
		"evidence_grade": "unverified", "official_sources": []string{"https://example.test/x"},
		"checked_at": "2026-11-09", "valid_until": "2027-02-07",
		"missing": []string{"接入核验"}, "reason_code": "pending_external", "reason_text": "未核验",
		"seed_version": 1,
	}
	write := func(mut map[string]any) string {
		b, err := json.Marshal(mut)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	full := func() map[string]any {
		m := map[string]any{}
		for k, v := range rec {
			m[k] = v
		}
		m["actor"] = "platform-ops"
		m["reason"] = "新增待核验路线"
		return m
	}

	// 缺 reason → 400。
	noReason := full()
	delete(noReason, "reason")
	if status, _ = rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "", internalHdr(), write(noReason)); status != http.StatusBadRequest {
		t.Fatalf("missing reason = %d, want 400", status)
	}
	// 非法枚举 status=allowed → 422(allowed 不是状态;unknown 永不等价 allowed)。
	bad := full()
	bad["status"] = "allowed"
	if status, _ = rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "", internalHdr(), write(bad)); status != http.StatusUnprocessableEntity {
		t.Fatalf("status allowed = %d, want 422", status)
	}
	// unknown 状态配 reward=supported → 422(unknown 永不开奖励)。
	bad = full()
	bad["status"] = "unknown"
	envs := bad["environments"].(map[string]any)["desktop_browser"].(map[string]string)
	envs["reward_eligibility"] = "supported"
	if status, _ = rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "", internalHdr(), write(bad)); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown+reward supported = %d, want 422", status)
	}
	// 还原共享底图,后续 full() 不被本次负例污染。
	envs["reward_eligibility"] = "unknown"

	// 合法创建 → 201 created。
	if status, body := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "", internalHdr(), write(full())); status != http.StatusCreated || body["result"] != "created" {
		t.Fatalf("create = %d result=%v, want 201/created", status, body["result"])
	}
	// 原样重发 → 200 unchanged,审计不增。
	if status, body := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "", internalHdr(), write(full())); status != http.StatusOK || body["result"] != "unchanged" {
		t.Fatalf("repeat = %d result=%v, want 200/unchanged", status, body["result"])
	}
	if n, err := f.s.St.CountActionCapabilityAudit("A91"); err != nil || n != 1 {
		t.Fatalf("audit after unchanged = %d err=%v, want 1", n, err)
	}
	// 内容变化 → 200 updated,审计 +1。
	chg := full()
	chg["reason"] = "主体控制台核验完成"
	chg["status"] = "admitted"
	chg["reason_code"] = "admitted"
	if status, body := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions", "", internalHdr(), write(chg)); status != http.StatusOK || body["result"] != "updated" {
		t.Fatalf("update = %d result=%v, want 200/updated", status, body["result"])
	}
	if n, err := f.s.St.CountActionCapabilityAudit("A91"); err != nil || n != 2 {
		t.Fatalf("audit after update = %d err=%v, want 2", n, err)
	}
	// 公开面立即反映更新后的真值。
	gstatus, gbody := actionCapsGet(t, f, "/api/v1/public/actions/A91/capabilities")
	gr := actionRoute(t, gbody, "manual_material", "desktop_browser")
	if gstatus != http.StatusOK || routeStr(gr, "status") != "admitted" {
		t.Fatalf("public reflects controlled update: %d %v", gstatus, gr)
	}
}

// findA01H5ID locates the seeded A01 h5_share row id via the internal read.
func findA01H5ID(t *testing.T, f *admissionFixture) string {
	t.Helper()
	status, body := rawDo(t, f, http.MethodGet, "/api/v1/channel-actions", "", internalHdr(), "")
	if status != http.StatusOK {
		t.Fatalf("internal read = %d", status)
	}
	recs, _ := body["records"].([]any)
	for _, raw := range recs {
		r, _ := raw.(map[string]any)
		if r["id"] == "cap_a01_h5_share" {
			return r["id"].(string)
		}
	}
	t.Fatal("cap_a01_h5_share not found in internal read")
	return ""
}

// TestChannelActionsEmergency: 紧急停用只停对应方法/动作(独立否决);恢复后
// 公开面回到原真值;缺 reason 400;未知 id 404。
func TestChannelActionsEmergency(t *testing.T) {
	f := newAdmissionFixture(t, true)
	id := findA01H5ID(t, f)

	// 缺 reason → 400。
	if status, _ := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions/"+id+"/emergency", "", internalHdr(), `{"disabled":true}`); status != http.StatusBadRequest {
		t.Fatalf("missing reason = %d, want 400", status)
	}
	// 未知 id → 404。
	if status, _ := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions/cap_nope/emergency", "", internalHdr(), `{"disabled":true,"reason":"x"}`); status != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", status)
	}

	// 停用 → 公开面 A01 h5_share 答 emergency_disabled。
	if status, _ := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions/"+id+"/emergency", "", internalHdr(), `{"disabled":true,"reason":"平台故障临时停用","actor":"ops-oncall"}`); status != http.StatusOK {
		t.Fatalf("disable = %d", status)
	}
	_, body := actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities")
	r := actionRoute(t, body, "h5_share", "desktop_browser")
	if routeBool(r, "allowed") || routeStr(r, "reason") != "emergency_disabled" {
		t.Fatalf("disabled route = %v/%s, want false/emergency_disabled", routeBool(r, "allowed"), routeStr(r, "reason"))
	}
	// 只停对应方法:同动作 manual_material 路由不受影响(仍是 not_admitted),
	// 其他动作(A02)不受影响。
	m := actionRoute(t, body, "manual_material", "desktop_browser")
	if routeStr(m, "reason") != "not_admitted" {
		t.Fatalf("manual route after emergency = %s, want not_admitted (紧急停用只停对应方法)", routeStr(m, "reason"))
	}
	_, body2 := actionCapsGet(t, f, "/api/v1/public/actions/A02/capabilities")
	r2 := actionRoute(t, body2, "js_media_handoff", "desktop_browser")
	if routeStr(r2, "reason") != "pending_external" {
		t.Fatalf("A02 after A01 emergency = %s, want pending_external (无关动作不受影响)", routeStr(r2, "reason"))
	}

	// 恢复 → 公开面回到原真值(not_admitted)。
	if status, _ := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions/"+id+"/emergency", "", internalHdr(), `{"disabled":false,"reason":"平台恢复"}`); status != http.StatusOK {
		t.Fatalf("enable = %d", status)
	}
	_, body = actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities")
	r = actionRoute(t, body, "h5_share", "desktop_browser")
	if routeStr(r, "reason") != "not_admitted" {
		t.Fatalf("restored route = %s, want not_admitted", routeStr(r, "reason"))
	}

	// 审计:两次 toggle 都留痕(actor/reason)。
	astatus, abody := rawDo(t, f, http.MethodGet, "/api/v1/channel-actions/A01/audit", "", internalHdr(), "")
	if astatus != http.StatusOK {
		t.Fatalf("audit read = %d", astatus)
	}
	audit, _ := abody["audit"].([]any)
	if len(audit) != 2 {
		t.Fatalf("audit entries = %d, want 2 (disable+enable)", len(audit))
	}
	first, _ := audit[0].(map[string]any)
	if first["action"] != "emergency_disable" || first["actor"] != "ops-oncall" || first["reason"] != "平台故障临时停用" {
		t.Fatalf("disable audit entry = %v", first)
	}
}

// TestChannelActionsWriteIsInternalOnly: 写面在无 token 的会话下也不可达。
func TestChannelActionsWriteIsInternalOnly(t *testing.T) {
	f := newAdmissionFixture(t, true)
	if status, _ := rawDo(t, f, http.MethodPost, "/api/v1/channel-actions/cap_a01_h5_share/emergency", "sess-owner-a", noAuthHdr(), `{"disabled":true,"reason":"x"}`); status != http.StatusUnauthorized {
		t.Fatalf("session emergency = %d, want 401", status)
	}
	if status, _ := rawDo(t, f, http.MethodGet, "/api/v1/channel-actions/A01/audit", "sess-owner-a", noAuthHdr(), ""); status != http.StatusUnauthorized {
		t.Fatalf("session audit = %d, want 401", status)
	}
}

// TestPublicActionCapabilitiesFilters: method/scenario 是便捷过滤,组合环境过滤
// 后只返回匹配 routes;全过滤为空集仍是 200(不 404——动作存在)。
func TestPublicActionCapabilitiesFilters(t *testing.T) {
	f := newAdmissionFixture(t, true)
	status, body := actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities?method=h5_share&environment=ios_wechat")
	if status != http.StatusOK {
		t.Fatalf("method+env filter = %d", status)
	}
	routes, _ := body["routes"].([]any)
	if len(routes) != 1 {
		t.Fatalf("filtered routes = %d, want 1", len(routes))
	}
	r, _ := routes[0].(map[string]any)
	if r["method"] != "h5_share" || r["environment"] != "ios_wechat" {
		t.Fatalf("filtered route = %v/%v", r["method"], r["environment"])
	}
	status, body = actionCapsGet(t, f, "/api/v1/public/actions/A01/capabilities?method=direct_open")
	if status != http.StatusOK {
		t.Fatalf("method-only filter = %d", status)
	}
	routes, _ = body["routes"].([]any)
	if len(routes) != 0 {
		t.Fatalf("empty filter result must stay 200 with empty routes, got %d routes", len(routes))
	}
}
