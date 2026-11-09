package httpapi

import (
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
