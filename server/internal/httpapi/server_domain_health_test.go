package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// HUI-2222 (wave2, B15 工程面): the ops domain-health endpoint. It probes only
// operator-declared public/media domains from server config — the request can
// never influence a target — and answers honest states: not_configured,
// invalid_config, reachable, unreachable. It never fabricates ok.

func TestDomainHealthRequiresSession(t *testing.T) {
	f := newFixture(t, false)
	status, _, _ := f.do(t, "GET", "/api/v1/ops/domain-health", "", "", "")
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("anonymous domain-health = %d, want 401/403", status)
	}
	status, _, out := f.do(t, "GET", "/api/v1/ops/domain-health", "sess-owner-a", "", "")
	if status != http.StatusOK {
		t.Fatalf("authed domain-health (principal gate, no tenant) = %d %v", status, out)
	}
}

func TestDomainHealthNotConfiguredAnswersHonestly(t *testing.T) {
	f := newFixture(t, false)
	status, _, out := f.do(t, "GET", "/api/v1/ops/domain-health", "sess-owner-a", "", "")
	if status != http.StatusOK {
		t.Fatalf("domain-health = %d %v", status, out)
	}
	items, _ := out["targets"].([]any)
	if len(items) != 2 {
		t.Fatalf("targets = %v, want the two declared target kinds", out["targets"])
	}
	byKind := map[string]map[string]any{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		kind, _ := item["target_kind"].(string)
		byKind[kind] = item
	}
	// 公开域名未配置:诚实 not_configured,绝不伪造 ok。
	pub := byKind["public_base"]
	if pub == nil || pub["configured"] != false || pub["status"] != "not_configured" {
		t.Fatalf("unconfigured public base must answer honestly: %v", pub)
	}
	// 媒体基缺省是相对 BFF 路径:同源配置,如实 same_origin(不是错误)。
	media := byKind["media_base"]
	if media == nil || media["configured"] != true || media["status"] != "same_origin" {
		t.Fatalf("relative media base must answer same_origin: %v", media)
	}
	if out["status"] != "degraded" {
		t.Fatalf("overall status with public base missing = %v, want degraded (never fake ok)", out["status"])
	}
}

func TestDomainHealthInvalidConfigAnswersHonestly(t *testing.T) {
	f := newFixture(t, false)
	f.s.Cfg.PublicBaseURL = "not-a-url"
	status, _, out := f.do(t, "GET", "/api/v1/ops/domain-health", "sess-owner-a", "", "")
	if status != http.StatusOK {
		t.Fatalf("domain-health = %d %v", status, out)
	}
	items, _ := out["targets"].([]any)
	var pub map[string]any
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["target_kind"] == "public_base" {
			pub = item
		}
	}
	if pub == nil || pub["status"] != "invalid_config" {
		t.Fatalf("invalid public base must answer invalid_config: %v", pub)
	}
}

func TestDomainHealthProbesConfiguredDomains(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(up.Close)
	// httptest 监听 127.0.0.1:工程面探针只如实报告可达性;公共跳转白名单
	// (extrajump.OfficialAddress)才负责拒环回,两者职责不同。
	f := newFixture(t, false)
	f.s.Cfg.PublicBaseURL = up.URL + "/ok"
	f.s.Cfg.PublishHandoffMediaBaseURL = up.URL + "/broken"

	status, _, out := f.do(t, "GET", "/api/v1/ops/domain-health", "sess-owner-a", "", "")
	if status != http.StatusOK {
		t.Fatalf("domain-health = %d %v", status, out)
	}
	items, _ := out["targets"].([]any)
	var pub, media map[string]any
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		switch item["target_kind"] {
		case "public_base":
			pub = item
		case "media_base":
			media = item
		}
	}
	if pub["status"] != "reachable" || pub["http_status"] != float64(http.StatusOK) {
		t.Fatalf("public target = %v, want reachable 200", pub)
	}
	if pub["url"] != up.URL+"/ok" || pub["checked_at"] == "" || pub["latency_ms"] == nil {
		t.Fatalf("public target must carry url/checked_at/latency_ms: %v", pub)
	}
	if media["status"] != "unreachable" || media["http_status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("5xx upstream must answer unreachable honestly: %v", media)
	}
	if out["status"] != "degraded" {
		t.Fatalf("one unreachable target => overall degraded: %v", out["status"])
	}
}

func TestDomainHealthUnreachableUpstreamAnswersHonestFailure(t *testing.T) {
	f := newFixture(t, false)
	// 无人监听的回环端口:连接拒绝 → unreachable,不编造结果。
	f.s.Cfg.PublicBaseURL = "http://127.0.0.1:1/healthz"
	status, _, out := f.do(t, "GET", "/api/v1/ops/domain-health", "sess-owner-a", "", "")
	if status != http.StatusOK {
		t.Fatalf("domain-health = %d %v", status, out)
	}
	items, _ := out["targets"].([]any)
	var pub map[string]any
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["target_kind"] == "public_base" {
			pub = item
		}
	}
	if pub["status"] != "unreachable" || pub["error"] == "" {
		t.Fatalf("dead upstream must answer unreachable with a reason: %v", pub)
	}
}

func TestDomainHealthNeverEchoesRequestInput(t *testing.T) {
	f := newFixture(t, false)
	status, _, out := f.do(t, "GET", "/api/v1/ops/domain-health?probe=https://evil.example", "sess-owner-a", "", "")
	if status != http.StatusOK {
		t.Fatalf("domain-health = %d %v", status, out)
	}
	body := JSONString(out)
	if strings.Contains(body, "evil.example") {
		t.Fatalf("request input must never influence or appear in targets: %s", body)
	}
}
