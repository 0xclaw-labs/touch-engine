package httpapi

// HUI-3553 W5：queue-view 受信服务生产者端到端（双 auth；tenant 从 handoff 行反查）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

const producerTestToken = "tok-producer-1"
const producerTestService = "modelxing-runtime"

func newProducerFixture(t *testing.T, tokens, roles string) (*contentPackFixture, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	f := newContentPackFixtureWith(t, false, tokens, roles, buf)
	return f, buf
}

func seedProducerHandoff(t *testing.T, f *contentPackFixture, tenantID, campaignID, idemKey string) store.ContentHandoff {
	t.Helper()
	h, _, err := f.s.St.CreateContentHandoff(tenantID, campaignID, store.HandoffInput{
		Purpose: "周末活动推广", StoreBrief: "旗舰店", DesiredForm: "竖版短视频",
		IdempotencyKey: idemKey, ExecutionPreference: "night_batch",
		PolicyRef: "policy://night-batch", PolicyVersion: 7,
	}, "owner-seed")
	if err != nil {
		t.Fatalf("seed handoff: %v", err)
	}
	return h
}

func queueViewFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "mxopsview", "testdata", "job-queue-view.waiting.json"))
	if err != nil {
		t.Fatalf("queue view fixture: %v", err)
	}
	return string(raw)
}

func (f *contentPackFixture) producerPost(t *testing.T, path, serviceID, token, tenantHeader, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if serviceID != "" {
		req.Header.Set(serviceIDHeader, serviceID)
	}
	if token != "" {
		req.Header.Set(serviceTokenHeader, token)
	}
	if tenantHeader != "" {
		req.Header.Set(tenantHeader, tenantHeader)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestQueueViewServiceProducerWritesRow(t *testing.T) {
	f, _ := newProducerFixture(t, producerTestService+":"+producerTestToken, "")
	h := seedProducerHandoff(t, f, f.tenA, f.campA, "idem-producer-1")
	body := queueViewFixture(t)

	status, out := f.producerPost(t, "/api/v1/content-handoffs/"+h.ID+"/queue-view",
		producerTestService, producerTestToken, "", body)
	if status != 200 {
		t.Fatalf("service queue-view write: want 200, got %d %v", status, out)
	}
	for _, key := range []string{"handoff_id", "job_ref", "job_queue_view"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("response missing %q: %v", key, out)
		}
	}
	stored, err := f.s.St.GetContentHandoff(f.tenA, h.ID)
	if err != nil {
		t.Fatalf("reload handoff: %v", err)
	}
	if stored.TenantID != f.tenA || stored.JobRef == "" || len(stored.JobQueueView) == 0 {
		t.Fatalf("row not written from row tenant: %+v", stored)
	}
	if strings.Contains(string(stored.JobQueueView), "internal_debug") {
		t.Fatalf("internal_debug leaked into stored public projection: %s", stored.JobQueueView)
	}

	// Same JSON keys as the legacy session path (byte-shape parity).
	legacyHandoff := seedProducerHandoff(t, f, f.tenA, f.campA, "idem-producer-legacy")
	status, legacyOut := f.owner(t, "POST", "/api/v1/content-handoffs/"+legacyHandoff.ID+"/queue-view", body)
	if status != 200 {
		t.Fatalf("legacy queue-view write: want 200, got %d %v", status, legacyOut)
	}
	if len(out) != len(legacyOut) {
		t.Fatalf("key set differs: service=%v legacy=%v", keysOf(out), keysOf(legacyOut))
	}
	for key := range out {
		if _, ok := legacyOut[key]; !ok {
			t.Fatalf("service response has key %q missing from legacy: %v", key, keysOf(legacyOut))
		}
	}
	serviceView, _ := json.Marshal(out["job_queue_view"])
	legacyView, _ := json.Marshal(legacyOut["job_queue_view"])
	if string(serviceView) != string(legacyView) {
		t.Fatalf("job_queue_view projection differs between paths")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 伪造 X-Tenant-ID 无效：tenant 永远从 handoff 行反查。
func TestQueueViewServiceProducerIgnoresTenantHeader(t *testing.T) {
	f, _ := newProducerFixture(t, producerTestService+":"+producerTestToken, "")
	hA := seedProducerHandoff(t, f, f.tenA, f.campA, "idem-producer-a")
	campB, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenB, Title: "B 活动", PublicContent: "x", CreatedBy: "seed"})
	if err != nil {
		t.Fatalf("create campaign B: %v", err)
	}
	hB := seedProducerHandoff(t, f, f.tenB, campB.ID, "idem-producer-b")
	body := queueViewFixture(t)

	status, out := f.producerPost(t, "/api/v1/content-handoffs/"+hA.ID+"/queue-view",
		producerTestService, producerTestToken, f.tenB, body)
	if status != 200 {
		t.Fatalf("service write with forged tenant header: want 200, got %d %v", status, out)
	}
	storedA, err := f.s.St.GetContentHandoff(f.tenA, hA.ID)
	if err != nil || storedA.JobRef == "" {
		t.Fatalf("handoff row tenant must win: %+v err=%v", storedA, err)
	}
	storedB, err := f.s.St.GetContentHandoff(f.tenB, hB.ID)
	if err != nil {
		t.Fatalf("reload B: %v", err)
	}
	if storedB.JobRef != "" {
		t.Fatalf("forged tenant header must not write the other tenant's rows")
	}
}

func TestQueueViewServiceProducerUnknownHandoff404(t *testing.T) {
	f, _ := newProducerFixture(t, producerTestService+":"+producerTestToken, "")
	status, out := f.producerPost(t, "/api/v1/content-handoffs/cho_missing/queue-view",
		producerTestService, producerTestToken, "", queueViewFixture(t))
	if status != 404 || out["error"] != "not_found" {
		t.Fatalf("unknown handoff: want uniform 404 not_found, got %d %v", status, out)
	}
}

// 非法/超限 queue_view：服务路径与旧路径同一错误码（异常体不落库）。
func TestQueueViewServiceProducerBadContractParity(t *testing.T) {
	f, _ := newProducerFixture(t, producerTestService+":"+producerTestToken, "")
	h := seedProducerHandoff(t, f, f.tenA, f.campA, "idem-producer-bad")

	bad := `{"contract_version":"mx-ops-c0/v1","object_kind":"job_queue_view"}`
	serviceStatus, _ := f.producerPost(t, "/api/v1/content-handoffs/"+h.ID+"/queue-view",
		producerTestService, producerTestToken, "", bad)
	legacyStatus, _ := f.owner(t, "POST", "/api/v1/content-handoffs/"+h.ID+"/queue-view", bad)
	if serviceStatus != legacyStatus || serviceStatus != 422 {
		t.Fatalf("bad contract parity: service=%d legacy=%d want 422", serviceStatus, legacyStatus)
	}
	stored, err := f.s.St.GetContentHandoff(f.tenA, h.ID)
	if err != nil || stored.JobRef != "" || len(stored.JobQueueView) != 0 {
		t.Fatalf("rejected queue view must not be written: %+v err=%v", stored, err)
	}

	oversize := strings.Repeat("x", 65<<10)
	serviceOversize, _ := f.producerPost(t, "/api/v1/content-handoffs/"+h.ID+"/queue-view",
		producerTestService, producerTestToken, "", oversize)
	legacyOversize, _ := f.owner(t, "POST", "/api/v1/content-handoffs/"+h.ID+"/queue-view", oversize)
	if serviceOversize != legacyOversize {
		t.Fatalf("oversize parity: service=%d legacy=%d", serviceOversize, legacyOversize)
	}
}

// 配置了 registry 但服务身份缺失/错误：401；registry 空：503 fail-closed。
func TestQueueViewServiceProducerAuthFailClosed(t *testing.T) {
	f, _ := newProducerFixture(t, producerTestService+":"+producerTestToken, "")
	h := seedProducerHandoff(t, f, f.tenA, f.campA, "idem-producer-auth")
	path := "/api/v1/content-handoffs/" + h.ID + "/queue-view"

	if status, _ := f.producerPost(t, path, producerTestService, "tok-wrong", "", queueViewFixture(t)); status != 401 {
		t.Fatalf("wrong token want 401, got %d", status)
	}
	if status, _ := f.producerPost(t, path, "", "", "", queueViewFixture(t)); status != 401 {
		t.Fatalf("no service identity falls back to legacy (401 without internal token), got %d", status)
	}

	empty, _ := newProducerFixture(t, "", "")
	h2 := seedProducerHandoff(t, empty, empty.tenA, empty.campA, "idem-producer-empty")
	if status, _ := empty.producerPost(t, "/api/v1/content-handoffs/"+h2.ID+"/queue-view",
		producerTestService, producerTestToken, "", queueViewFixture(t)); status != 503 {
		t.Fatalf("unconfigured registry want 503 fail-closed, got %d", status)
	}
}

// 审计：含 service/handoff/tenant/job_ref/result；绝不含 token/body。
func TestQueueViewServiceProducerAudit(t *testing.T) {
	f, logs := newProducerFixture(t, producerTestService+":"+producerTestToken, "")
	h := seedProducerHandoff(t, f, f.tenA, f.campA, "idem-producer-audit")
	if status, out := f.producerPost(t, "/api/v1/content-handoffs/"+h.ID+"/queue-view",
		producerTestService, producerTestToken, "", queueViewFixture(t)); status != 200 {
		t.Fatalf("write: %d %v", status, out)
	}
	line := logs.String()
	for _, want := range []string{"mx_ops_queue_view_producer", "service=" + producerTestService, "handoff=" + h.ID, "tenant=" + f.tenA, "result=ok"} {
		if !strings.Contains(line, want) {
			t.Fatalf("audit line missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, producerTestToken) {
		t.Fatalf("audit leaked the service token: %s", line)
	}
	if strings.Contains(line, "internal_debug") {
		t.Fatalf("audit leaked the request body: %s", line)
	}
	_ = httptest.NewRecorder // keep httptest import explicit for fixture helpers
}
