package httpapi

// server_content_handoffs_test.go: HUI-3553 MX-OPS R2 队列投影接缝 HTTP 验收。
//
// 覆盖:
//   - 创建交接带 execution_preference/policy_ref/policy_version(引擎仍未接,
//     503 handoff_unavailable 如实降级);priority 等未知字段继续 400;
//   - 同 idempotency_key 重放 = 同一 handoff id,零新建;
//   - GET campaigns/{id}/content-handoffs 恢复列表;
//   - POST content-handoffs/{handoffId}/queue-view:合同 job_queue_view 严格校验,
//     internal_debug 整键剥离;jobs_ahead/0/null 语义保持;错版本 422;
//   - 跨租户一律 404(不泄露存在性)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func handoffCreateBody(preference string) string {
	return `{"purpose":"周末活动推广","store_brief":"旗舰店","desired_form":"竖版短视频","asset_ids":[],"idempotency_key":"idem-ho-1","execution_preference":"` + preference + `","policy_ref":"policy://night-batch","policy_version":7}`
}

func TestContentHandoffMXOpsSeam(t *testing.T) {
	f := newContentPackFixture(t, false)

	status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", handoffCreateBody("night_batch"))
	if status != 503 || out["error"] != "handoff_unavailable" {
		t.Fatalf("create: want honest 503 handoff_unavailable, got %d %v", status, out)
	}
	handoff := out["handoff"].(map[string]any)
	handoffID, _ := handoff["id"].(string)
	if handoffID == "" {
		t.Fatalf("handoff id missing: %v", handoff)
	}
	if handoff["execution_preference"] != "night_batch" || handoff["policy_ref"] != "policy://night-batch" {
		t.Fatalf("execution preference/policy not preserved: %v", handoff)
	}

	// Idempotent replay: same id, no new production project.
	status, replay := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", handoffCreateBody("night_batch"))
	if status != 503 || replay["replayed"] != true {
		t.Fatalf("replay: want replayed=true 503, got %d %v", status, replay)
	}
	if replay["handoff"].(map[string]any)["id"] != handoffID {
		t.Fatalf("replay must return the same handoff id")
	}

	// Strict decode keeps rejecting invented fields (no numeric priority).
	if status, _ := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", `{"purpose":"x","priority":5}`); status != 400 {
		t.Fatalf("priority must be rejected with 400, got %d", status)
	}
	if status, _ := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", `{"purpose":"x","execution_preference":"priority"}`); status != 400 {
		t.Fatalf("invalid execution_preference must be 400, got %d", status)
	}

	// Restore list (same handoff id).
	status, list := f.owner(t, "GET", "/api/v1/campaigns/"+f.campA+"/content-handoffs", "")
	if status != 200 {
		t.Fatalf("list: %d %v", status, list)
	}
	handoffs := list["handoffs"].([]any)
	if len(handoffs) != 1 {
		t.Fatalf("want 1 handoff, got %d", len(handoffs))
	}

	// Queue-view sync seam: contract-valid view is stored as the PUBLIC projection.
	raw, err := os.ReadFile(filepath.Join("..", "mxopsview", "testdata", "job-queue-view.waiting.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	status, synced := f.owner(t, "POST", "/api/v1/content-handoffs/"+handoffID+"/queue-view", string(raw))
	if status != 200 {
		t.Fatalf("queue-view sync: %d %v", status, synced)
	}
	if synced["job_ref"] != "mx_0123456789abcdef" {
		t.Fatalf("job_ref not synced: %v", synced)
	}
	view := synced["job_queue_view"].(map[string]any)
	if _, exists := view["internal_debug"]; exists {
		t.Fatalf("internal_debug must be stripped from the stored public projection")
	}
	if view["jobs_ahead"].(float64) != 3 {
		t.Fatalf("jobs_ahead lost: %v", view)
	}
	if view["eta_seconds_min"] != nil || view["eta_seconds_max"] != nil {
		t.Fatalf("unknown ETA must stay null: %v", view)
	}

	// The GET list exposes the same public projection.
	_, list2 := f.owner(t, "GET", "/api/v1/campaigns/"+f.campA+"/content-handoffs", "")
	row := list2["handoffs"].([]any)[0].(map[string]any)
	stored := row["job_queue_view"].(map[string]any)
	if _, exists := stored["internal_debug"]; exists {
		t.Fatalf("GET list must not leak internal_debug")
	}

	// Cross-tenant: 404 for sync and list (existence never leaked).
	if status, _ := f.do(t, "POST", "/api/v1/content-handoffs/"+handoffID+"/queue-view", "sess-owner-b", f.tenB, string(raw)); status != 404 {
		t.Fatalf("cross-tenant queue-view want 404, got %d", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/campaigns/"+f.campA+"/content-handoffs", "sess-owner-b", f.tenB, ""); status != 404 {
		t.Fatalf("cross-tenant list want 404, got %d", status)
	}
}

func TestContentHandoffQueueViewRejectsBadContract(t *testing.T) {
	f := newContentPackFixture(t, false)
	status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campA+"/content-handoffs", handoffCreateBody("immediate"))
	if status != 503 {
		t.Fatalf("create: %d %v", status, out)
	}
	handoffID := out["handoff"].(map[string]any)["id"].(string)

	raw, err := os.ReadFile(filepath.Join("..", "mxopsview", "testdata", "job-queue-view.waiting.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var base map[string]any
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("decode: %v", err)
	}
	base["contract_version"] = "mx-ops-c0/v2"
	bad, _ := json.Marshal(base)
	status, out = f.owner(t, "POST", "/api/v1/content-handoffs/"+handoffID+"/queue-view", string(bad))
	if status != 422 || out["error"] != "mx_ops_c0_invalid_queue_view" {
		t.Fatalf("bad contract: want 422 mx_ops_c0_invalid_queue_view, got %d %v", status, out)
	}
	if status, _ := f.owner(t, "POST", "/api/v1/content-handoffs/"+handoffID+"/queue-view", "not-json"); status != 422 {
		t.Fatalf("non-json view want 422, got %d", status)
	}
}
