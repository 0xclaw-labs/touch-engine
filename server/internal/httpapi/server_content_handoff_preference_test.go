package httpapi

// server_content_handoff_preference_test.go: MX-OPS 增量 — 未指定执行偏好时
// 保持「未指定」，不得静默当成商家选择了立即。

import (
	"net/http"
	"testing"
)

func TestContentHandoffWithoutPreferenceStaysUnspecified(t *testing.T) {
	f := newContentPackFixture(t, false)
	status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs",
		`{"purpose":"还没决定怎么排","idempotency_key":"handoff-no-pref"}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want honest 503", status)
	}
	handoff, ok := out["handoff"].(map[string]any)
	if !ok {
		t.Fatalf("missing handoff: %v", out)
	}
	if handoff["execution_preference"] != "" {
		t.Fatalf("absent preference = %v, want empty (unspecified)", handoff["execution_preference"])
	}
}
