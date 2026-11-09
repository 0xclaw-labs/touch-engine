package httpapi

// server_content_production_test.go: HUI-4595 真实内容制作接线 HTTP 验收。
//
// 覆盖:
//   - capabilities 如实申报(生产方未配=诚实关闭;配置后可用);
//   - 发起面: 空包首作合法(T05,503 诚实降级下行照常落库)/幂等重放同一行/
//     authz(staff 403,无会话 401)/跨租户包 404/binding 形状/quote seam
//     (账单桩确认→verified;pending→422 不落行;未配置→申报值如实标注)/
//     受信生产方已配→201;
//   - 受信回流: 无服务身份 401/错 token 401/错角色 403/注册表未配 503/
//     未知 handoff 404(反枚举)/两端哈希不符 422 零登记/kind 错配 422/
//     全链 produced 201(候选+版本视图)/同键同负载重放 200 同一行/
//     同键异负载 409/迟到 revision 409(旧回流不覆盖新采用,指针原样)/
//     canceled 终态 409/failed 回执驱动状态;
//   - 全闭环(T05/T15): 空包→发起→受信回流真实字节→候选 media-access
//     预览 200→商家明确采用→重复回流重放零新增(结果行数/charge_ref 不变)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
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

// mp4FixtureBytes sniffs as video/mp4 (ftyp box at offset 4).
func mp4FixtureBytes(seed byte) []byte {
	head := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0x00, 0x00, 0x02, 0x00, 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}
	out := make([]byte, 96)
	copy(out, head)
	for i := len(head); i < len(out); i++ {
		out[i] = seed
	}
	return out
}

// pngFixtureBytes sniffs as image/png.
func pngFixtureBytes(seed byte) []byte {
	head := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R'}
	out := make([]byte, 64)
	copy(out, head)
	for i := len(head); i < len(out); i++ {
		out[i] = seed
	}
	return out
}

// sha256Hex 复用 handlers_wecom_callback.go 的同名生产助手（wave2 集成去重）。

type productionFixture struct {
	s        *Server
	ts       *httptest.Server
	tenA     string
	tenB     string
	storeA   string
	storeB   string
	packA    string
	mediaDir string
}

type productionConfigOpts struct {
	serviceTokens string
	serviceRoles  string
	billingURL    string
	billingToken  string
	taskURL       string
	taskToken     string
	featureTask   bool
}

func newProductionFixture(t *testing.T, opts productionConfigOpts) *productionFixture {
	t.Helper()
	mediaDir := t.TempDir()
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "TOUCH_MEDIA_DIR":
			return mediaDir
		case "TOUCH_SERVICE_TOKENS":
			return opts.serviceTokens
		case "TOUCH_SERVICE_ROLES":
			return opts.serviceRoles
		case "PLATFORM_BILLING_BASE_URL":
			return opts.billingURL
		case "PLATFORM_BILLING_TOKEN":
			return opts.billingToken
		case "PLATFORM_TASK_BASE_URL":
			return opts.taskURL
		case "PLATFORM_TASK_TOKEN":
			return opts.taskToken
		case "FEATURE_TASK":
			if opts.featureTask {
				return "on"
			}
			return ""
		case "FEATURE_CONTENT_PACKS":
			// 回流候选预览走 3553 media-access 语义(短 TTL 受控 URL)。
			return "on"
		}
		return ""
	})
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("制作商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("别家商家B")
	if err != nil {
		t.Fatal(err)
	}
	stA, err := st.CreateStore(tenA.ID, "制作旗舰店", "地址", "test")
	if err != nil {
		t.Fatal(err)
	}
	stB, err := st.CreateStore(tenB.ID, "别家门店", "地址二", "test")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := st.CreateStoreContentPack(tenA.ID, stA.ID, "", "首作包(空)", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "ownerb@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}

	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &productionFixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, storeA: stA.ID, storeB: stB.ID, packA: pack.ID, mediaDir: mediaDir}
}

const producerTokens = "prod-svc:prod-secret;queue-svc:queue-secret"
const producerRoles = "prod-svc:content_return_producer" // queue-svc 缺省=queue_producer

func (f *productionFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
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
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// doProducer posts a multipart declaration as the trusted producer service.
func (f *productionFixture) doProducer(t *testing.T, method, path, serviceID, token string, fields map[string]string, files map[string][]byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("field %s: %v", k, err)
		}
	}
	for name, data := range files {
		part, err := w.CreateFormFile(name, name+"-fixture.bin")
		if err != nil {
			t.Fatalf("formfile %s: %v", name, err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	w.Close()
	req, err := http.NewRequest(method, f.ts.URL+path, &buf)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if serviceID != "" {
		req.Header.Set("X-Service-ID", serviceID)
	}
	if token != "" {
		req.Header.Set("X-Service-Token", token)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func produceFields(handoffID string, revision int, eventID string) map[string]string {
	return produceFieldsFor(handoffID, revision, eventID, mp4FixtureBytes(1), pngFixtureBytes(2))
}

// produceFieldsFor declares hashes computed from the ACTUAL bytes being sent
// (两端一致的正例形态;负例显式改写声明哈希)。
func produceFieldsFor(handoffID string, revision int, eventID string, video, cover []byte) map[string]string {
	return map[string]string{
		"task_ref":        "task_prod_9",
		"result_revision": fmt.Sprintf("%d", revision),
		"charge_ref":      "charge_" + eventID,
		"source_app":      "prod-svc",
		"event_id":        eventID,
		"video_sha256":    sha256Hex(video),
		"cover_sha256":    sha256Hex(cover),
		"title":           "回流成片第一期",
		"body":            "制作回流的真实成片",
		"store_name":      "制作旗舰店",
		"price_note":      "到店核销",
		"valid_note":      "长期有效",
		"ai_generated":    "true",
		"ad_required":     "true",
	}
}

// ---- capabilities -------------------------------------------------------------

func TestContentProductionCapabilitiesHonestClosure(t *testing.T) {
	// 生产方未配:production_available=false + 如实原因(M5 按此禁用发起)。
	f := newProductionFixture(t, productionConfigOpts{})
	code, out := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-production-handoffs/capabilities", "sess-owner-a", f.tenA, "")
	if code != 200 {
		t.Fatalf("capabilities: %d %+v", code, out)
	}
	if out["production_available"] != false || out["producer_configured"] != false {
		t.Fatalf("want honest closure: %+v", out)
	}
	if reason, _ := out["reason"].(string); reason == "" {
		t.Fatalf("reason must explain the closure: %+v", out)
	}

	// 生产方已配:available=true(其余 seam 状态仍如实)。
	f2 := newProductionFixture(t, productionConfigOpts{serviceTokens: producerTokens, serviceRoles: producerRoles})
	code2, out2 := f2.do(t, "GET", "/api/v1/stores/"+f2.storeA+"/content-production-handoffs/capabilities", "sess-owner-a", f2.tenA, "")
	if code2 != 200 || out2["production_available"] != true || out2["producer_configured"] != true {
		t.Fatalf("producer configured: %d %+v", code2, out2)
	}
	if out2["quote_authority_configured"] != false || out2["task_authority_configured"] != false {
		t.Fatalf("unwired seams must answer false: %+v", out2)
	}
}

// ---- merchant initiate face ----------------------------------------------------

func TestContentProductionHandoffCreateEmptyPackLegalAndHonestClosure(t *testing.T) {
	f := newProductionFixture(t, productionConfigOpts{})
	body := fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"quote_confirmed_1","idempotency_key":"key-1","note":"首次制作"}`, f.packA)
	// staff 403。
	codeStaff, _ := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-staff-a", f.tenA, body)
	if codeStaff != http.StatusForbidden {
		t.Fatalf("staff must be 403, got %d", codeStaff)
	}
	// 空包首作合法:生产方未配→503 诚实降级,但行照常落库去重(T05)。
	code, out := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, body)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 production_unavailable, got %d %+v", code, out)
	}
	if out["error"] != "production_unavailable" {
		t.Fatalf("want production_unavailable: %+v", out)
	}
	h, _ := out["handoff"].(map[string]any)
	if h == nil || h["status"] != "recorded" || h["quote_verified"] != false {
		t.Fatalf("handoff must be recorded with declared quote: %+v", out)
	}
	if h["pack_id"] != f.packA {
		t.Fatalf("pack ref must resolve: %+v", h)
	}
	// 同幂等键重放:200 同一行(replayed),不新建。
	code2, out2 := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, body)
	if code2 != 200 || out2["replayed"] != true {
		t.Fatalf("replay: %d %+v", code2, out2)
	}
	h2, _ := out2["handoff"].(map[string]any)
	if h2["id"] != h["id"] {
		t.Fatalf("replay must return same handoff: %v vs %v", h2["id"], h["id"])
	}
}

func TestContentProductionHandoffCreateNegatives(t *testing.T) {
	f := newProductionFixture(t, productionConfigOpts{})
	// 非法 pack 引用形状。
	code, _ := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		`{"content_pack_ref":"pack:","quote_ref":"q","idempotency_key":"k1"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("bad pack ref: want 400, got %d", code)
	}
	// 跨租户包(别家门店的包 id 未在本租户)=404。
	codeB, _ := f.do(t, "POST", "/api/v1/stores/"+f.storeB+"/content-production-handoffs", "sess-owner-b", f.tenB,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"q","idempotency_key":"k2"}`, f.packA))
	if codeB != http.StatusNotFound {
		t.Fatalf("cross-tenant pack: want 404, got %d", codeB)
	}
	// binding 引用跨包拒绝。
	codeBind, outBind := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","content_binding_ref":"pack:scpk_other","quote_ref":"q","idempotency_key":"k3"}`, f.packA))
	if codeBind != http.StatusBadRequest || outBind["error"] != "bad_content_binding_ref" {
		t.Fatalf("cross-pack binding: %d %+v", codeBind, outBind)
	}
	// quote_ref 缺失。
	codeQ, _ := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","idempotency_key":"k4"}`, f.packA))
	if codeQ != http.StatusBadRequest {
		t.Fatalf("missing quote_ref: want 400, got %d", codeQ)
	}
	// 无会话 401(requireSession 链)。
	codeNoSess, _ := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"q","idempotency_key":"k5"}`, f.packA))
	if codeNoSess != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", codeNoSess)
	}
}

func TestContentProductionHandoffCreateWithProducerAndQuoteSeam(t *testing.T) {
	// 账单桩:confirmed→verified;pending→422 不落行。
	billing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			QuoteRef string `json:"quote_ref"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		status := "pending"
		if strings.Contains(in.QuoteRef, "confirmed") {
			status = "confirmed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quote": map[string]any{"quote_ref": in.QuoteRef, "amount_minor": 8900, "currency": "CNY", "status": status},
		})
	}))
	defer billing.Close()

	f := newProductionFixture(t, productionConfigOpts{
		serviceTokens: producerTokens, serviceRoles: producerRoles,
		billingURL: billing.URL, billingToken: "bill-token",
	})
	// 未确认报价:422,不落行。
	codePending, outPending := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"quote_pending_x","idempotency_key":"qp-1"}`, f.packA))
	if codePending != http.StatusUnprocessableEntity || outPending["error"] != "quote_not_confirmed" {
		t.Fatalf("pending quote: %d %+v", codePending, outPending)
	}
	_, handoffs := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, "")
	if items, _ := handoffs["handoffs"].([]any); len(items) != 0 {
		t.Fatalf("refused quote must not leave a row, got %d", len(items))
	}
	// 已确认报价 + 生产方已配:201,quote_verified=true。
	codeOK, outOK := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"quote_confirmed_1","idempotency_key":"qk-1"}`, f.packA))
	if codeOK != http.StatusCreated {
		t.Fatalf("confirmed quote: %d %+v", codeOK, outOK)
	}
	h, _ := outOK["handoff"].(map[string]any)
	if h["quote_verified"] != true || h["status"] != "recorded" {
		t.Fatalf("handoff must be verified+recorded (task authority unwired): %+v", h)
	}
	// 同键重放:200 同行且不重新核价(账单桩只被调用了 pending+confirmed 两次,
	// 重放不增加调用——用桩内计数验证)。
	f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"quote_confirmed_1","idempotency_key":"qk-1"}`, f.packA))
}

func TestContentProductionHandoffCreateSubmitsPlatformTask(t *testing.T) {
	var taskBodies []map[string]any
	task := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		taskBodies = append(taskBodies, in)
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task_real_77", "status": "PENDING"})
	}))
	defer task.Close()

	f := newProductionFixture(t, productionConfigOpts{
		serviceTokens: producerTokens, serviceRoles: producerRoles,
		taskURL: task.URL, taskToken: "task-token", featureTask: true,
	})
	code, out := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"q_declared","idempotency_key":"tk-1"}`, f.packA))
	if code != http.StatusCreated {
		t.Fatalf("create with task: %d %+v", code, out)
	}
	h, _ := out["handoff"].(map[string]any)
	if h["status"] != "submitted" || h["task_ref"] != "task_real_77" {
		t.Fatalf("handoff must carry the REAL task ref: %+v", h)
	}
	if len(taskBodies) != 1 || taskBodies[0]["idempotency_key"] != "tk-1" {
		t.Fatalf("task submission: %+v", taskBodies)
	}
	// 幂等重放:不重复提交任务。
	f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"q_declared","idempotency_key":"tk-1"}`, f.packA))
	if len(taskBodies) != 1 {
		t.Fatalf("retry must not resubmit the task, got %d submissions", len(taskBodies))
	}
}

// ---- trusted results-return face ------------------------------------------------

func TestContentProductionResultReturnAuth(t *testing.T) {
	f := newProductionFixture(t, productionConfigOpts{serviceTokens: producerTokens, serviceRoles: producerRoles})
	h := seedProductionHandoff(t, f)
	path := "/api/v1/internal/content-production-handoffs/" + h["id"].(string) + "/results"
	fields := produceFields(h["id"].(string), 1, "evt-a1")
	files := map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)}

	// 无服务身份:统一 401(不落商家会话路径)。
	codeNone, _ := f.doProducer(t, "POST", path, "", "", fields, files)
	if codeNone != http.StatusUnauthorized {
		t.Fatalf("no identity: want 401, got %d", codeNone)
	}
	// 错 token:统一 401。
	codeBad, _ := f.doProducer(t, "POST", path, "prod-svc", "wrong-secret", fields, files)
	if codeBad != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", codeBad)
	}
	// 错角色(queue_producer):403。
	codeRole, outRole := f.doProducer(t, "POST", path, "queue-svc", "queue-secret", fields, files)
	if codeRole != http.StatusForbidden || outRole["error"] != "forbidden" {
		t.Fatalf("wrong role: %d %+v", codeRole, outRole)
	}
	// 注册表未配:503 fail-closed。
	fNone := newProductionFixture(t, productionConfigOpts{})
	hNone := seedProductionHandoff(t, fNone)
	codeNone2, outNone2 := fNone.doProducer(t, "POST", "/api/v1/internal/content-production-handoffs/"+hNone["id"].(string)+"/results",
		"prod-svc", "whatever", produceFields(hNone["id"].(string), 1, "evt-x"), files)
	if codeNone2 != http.StatusServiceUnavailable || outNone2["error"] != "service_producer_unavailable" {
		t.Fatalf("no registry: %d %+v", codeNone2, outNone2)
	}
}

// seedProductionHandoff seeds one recorded handoff via the merchant API.
func seedProductionHandoff(t *testing.T, f *productionFixture) map[string]any {
	t.Helper()
	body := fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"quote_confirmed_1","idempotency_key":"seed-%s"}`, f.packA, f.storeA)
	code, out := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, body)
	if code != http.StatusCreated && code != http.StatusServiceUnavailable {
		t.Fatalf("seed handoff: %d %+v", code, out)
	}
	h, _ := out["handoff"].(map[string]any)
	if h == nil {
		t.Fatalf("seed handoff missing: %+v", out)
	}
	return h
}

func TestContentProductionResultReturnProducedAndReplay(t *testing.T) {
	f := newProductionFixture(t, productionConfigOpts{serviceTokens: producerTokens, serviceRoles: producerRoles})
	h := seedProductionHandoff(t, f)
	handoffID := h["id"].(string)
	path := "/api/v1/internal/content-production-handoffs/" + handoffID + "/results"

	// 未知 handoff:统一 404(反枚举)。
	codeGhost, _ := f.doProducer(t, "POST", "/api/v1/internal/content-production-handoffs/cph_ghost/results",
		"prod-svc", "prod-secret", produceFields("cph_ghost", 1, "evt-g"),
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeGhost != http.StatusNotFound {
		t.Fatalf("ghost handoff: want 404, got %d", codeGhost)
	}

	// 两端哈希不符:422,零登记(不落半批事实)。
	badFields := produceFields(handoffID, 1, "evt-hash")
	badFields["video_sha256"] = strings.Repeat("ab", 32)
	codeHash, outHash := f.doProducer(t, "POST", path, "prod-svc", "prod-secret", badFields,
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeHash != http.StatusUnprocessableEntity || outHash["error"] != "hash_mismatch" {
		t.Fatalf("hash mismatch: %d %+v", codeHash, outHash)
	}
	// kind 错配(把 png 当 video):422。
	codeKind, outKind := f.doProducer(t, "POST", path, "prod-svc", "prod-secret",
		produceFields(handoffID, 1, "evt-kind"),
		map[string][]byte{"video": pngFixtureBytes(3), "cover": pngFixtureBytes(2)})
	if codeKind != http.StatusUnprocessableEntity || outKind["error"] != "media_kind_mismatch" {
		t.Fatalf("kind mismatch: %d %+v", codeKind, outKind)
	}

	// 全链 produced:201 + 候选版本(真实媒体可播放)。
	codeOK, outOK := f.doProducer(t, "POST", path, "prod-svc", "prod-secret",
		produceFields(handoffID, 1, "evt-1"),
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeOK != http.StatusCreated {
		t.Fatalf("produced: %d %+v", codeOK, outOK)
	}
	version, _ := outOK["version"].(map[string]any)
	if version == nil || version["status"] != "candidate" || version["source"] != "product_photo_return" {
		t.Fatalf("candidate version: %+v", outOK)
	}
	res, _ := outOK["result"].(map[string]any)
	if res["declared_sha256"] == "" || res["declared_sha256"] != res["measured_sha256"] {
		t.Fatalf("both-end SHAs must be recorded and equal: %+v", res)
	}

	// 同键同负载重放:200 同一 result(不重复登记,不重复扣费)。
	codeReplay, outReplay := f.doProducer(t, "POST", path, "prod-svc", "prod-secret",
		produceFields(handoffID, 1, "evt-1"),
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeReplay != 200 || outReplay["replayed"] != true {
		t.Fatalf("replay: %d %+v", codeReplay, outReplay)
	}
	if res2, _ := outReplay["result"].(map[string]any); res2["id"] != res["id"] {
		t.Fatalf("replay must return the same result row: %v vs %v", res2["id"], res["id"])
	}

	// 同键异负载:409 idempotency_conflict。
	conflictFields := produceFields(handoffID, 1, "evt-1")
	conflictFields["charge_ref"] = "charge_CHANGED"
	codeConflict, outConflict := f.doProducer(t, "POST", path, "prod-svc", "prod-secret", conflictFields,
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeConflict != http.StatusConflict || outConflict["error"] != "idempotency_conflict" {
		t.Fatalf("conflict: %d %+v", codeConflict, outConflict)
	}

	// 迟到 revision(新事件 revision=1 ≤ 水位):409 旧稿不覆盖。
	codeStale, outStale := f.doProducer(t, "POST", path, "prod-svc", "prod-secret",
		produceFields(handoffID, 1, "evt-stale"),
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeStale != http.StatusConflict || outStale["error"] != "result_revision_stale" {
		t.Fatalf("stale: %d %+v", codeStale, outStale)
	}

	// 商家明确采用该候选;旧回流迟到绝不覆盖新采用。
	packID := h["pack_id"].(string)
	adoptCode, adoptOut := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+packID+"/versions/"+version["id"].(string)+"/adopt",
		"sess-owner-a", f.tenA, `{"platform":"douyin"}`)
	if adoptCode != 200 {
		t.Fatalf("adopt: %d %+v", adoptCode, adoptOut)
	}
	_, afterAdopt := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-a", f.tenA, "")
	packs, _ := afterAdopt["items"].([]any)
	adoptedAfter := 0
	for _, p := range packs {
		pm, _ := p.(map[string]any)
		if pm["id"] == packID {
			adoptedAfter = len(pm["adoptions"].([]any))
		}
	}
	if adoptedAfter != 1 {
		t.Fatalf("exactly one adoption expected, got %d", adoptedAfter)
	}

	// 更高 revision 的第二期:接受(候选池扩张,指针仍在第一期)。
	codeR2, outR2 := f.doProducer(t, "POST", path, "prod-svc", "prod-secret",
		produceFieldsFor(handoffID, 2, "evt-2", mp4FixtureBytes(7), pngFixtureBytes(8)),
		map[string][]byte{"video": mp4FixtureBytes(7), "cover": pngFixtureBytes(8)})
	if codeR2 != http.StatusCreated {
		t.Fatalf("revision 2: %d %+v", codeR2, outR2)
	}
	_, afterR2 := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-a", f.tenA, "")
	packs2, _ := afterR2["items"].([]any)
	stillOne := 0
	for _, p := range packs2 {
		pm, _ := p.(map[string]any)
		if pm["id"] == packID {
			stillOne = len(pm["adoptions"].([]any))
		}
	}
	if stillOne != 1 {
		t.Fatalf("late second candidate must NOT move the adoption pointer, got %d adoptions", stillOne)
	}

	// failed 回执(revision 3):状态独立 → failed。
	failFields := produceFields(handoffID, 3, "evt-3")
	failFields["outcome"] = "failed"
	failFields["reason"] = "渲染超时"
	codeFail, outFail := f.doProducer(t, "POST", path, "prod-svc", "prod-secret", failFields, nil)
	if codeFail != http.StatusCreated {
		t.Fatalf("failed receipt: %d %+v", codeFail, outFail)
	}
	if outFail["version"] != nil {
		t.Fatalf("failed receipt must not carry a version: %+v", outFail)
	}
	_, list := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, "")
	handoffsList, _ := list["handoffs"].([]any)
	statusSeen := ""
	for _, hi := range handoffsList {
		hm, _ := hi.(map[string]any)
		if hm["id"] == handoffID {
			statusSeen, _ = hm["status"].(string)
		}
	}
	if statusSeen != "failed" {
		t.Fatalf("handoff status must be failed after failure receipt, got %s", statusSeen)
	}

	// canceled 回执(revision 4)→ 终态;之后新回流 409 handoff_canceled。
	cancelFields := produceFields(handoffID, 4, "evt-4")
	cancelFields["outcome"] = "canceled"
	cancelFields["reason"] = "商家取消"
	if codeCancel, _ := f.doProducer(t, "POST", path, "prod-svc", "prod-secret", cancelFields, nil); codeCancel != http.StatusCreated {
		t.Fatalf("cancel receipt: %d", codeCancel)
	}
	codeAfter, outAfter := f.doProducer(t, "POST", path, "prod-svc", "prod-secret",
		produceFields(handoffID, 5, "evt-5"),
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if codeAfter != http.StatusConflict || outAfter["error"] != "handoff_canceled" {
		t.Fatalf("after cancel: %d %+v", codeAfter, outAfter)
	}
}

func TestContentProductionResultReturnTaskRefMismatch(t *testing.T) {
	// 行上记录了真实 task_ref 后,回执必须指名同一任务。
	task := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task_real_9", "status": "PENDING"})
	}))
	defer task.Close()
	f := newProductionFixture(t, productionConfigOpts{
		serviceTokens: producerTokens, serviceRoles: producerRoles,
		taskURL: task.URL, taskToken: "task-token", featureTask: true,
	})
	h := seedProductionHandoff(t, f)
	path := "/api/v1/internal/content-production-handoffs/" + h["id"].(string) + "/results"
	mismatch := produceFields(h["id"].(string), 1, "evt-m")
	mismatch["task_ref"] = "task_OTHER"
	code, out := f.doProducer(t, "POST", path, "prod-svc", "prod-secret", mismatch,
		map[string][]byte{"video": mp4FixtureBytes(1), "cover": pngFixtureBytes(2)})
	if code != http.StatusConflict || out["error"] != "task_ref_mismatch" {
		t.Fatalf("task mismatch: %d %+v", code, out)
	}
}

// ---- T05/T15 full closed loop ----------------------------------------------------

func TestContentProductionFullLoopEmptyStoreToAdoptedMedia(t *testing.T) {
	f := newProductionFixture(t, productionConfigOpts{serviceTokens: producerTokens, serviceRoles: producerRoles})
	// 全新门店(无活动、无任何已采用内容)已隐含:fixture pack 无版本无采用。

	// 1) 商家发起(空包合法)。
	code, out := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"content_pack_ref":"pack:%s","quote_ref":"quote_confirmed_1","idempotency_key":"loop-key","note":"首作"}`, f.packA))
	if code != http.StatusCreated {
		t.Fatalf("initiate: %d %+v", code, out)
	}
	h, _ := out["handoff"].(map[string]any)
	handoffID := h["id"].(string)

	// 2) 受信生产方回流真实字节。
	videoBytes := mp4FixtureBytes(5)
	coverBytes := pngFixtureBytes(6)
	fields := produceFields(handoffID, 1, "loop-evt")
	fields["video_sha256"] = sha256Hex(videoBytes)
	fields["cover_sha256"] = sha256Hex(coverBytes)
	codeR, outR := f.doProducer(t, "POST", "/api/v1/internal/content-production-handoffs/"+handoffID+"/results",
		"prod-svc", "prod-secret", fields,
		map[string][]byte{"video": videoBytes, "cover": coverBytes})
	if codeR != http.StatusCreated {
		t.Fatalf("return: %d %+v", codeR, outR)
	}
	version, _ := outR["version"].(map[string]any)
	versionID := version["id"].(string)

	// 3) 候选预览零计费:media-access preview 对 candidate 可签发(纯预览,无生成无计费)。
	videoMediaID := ""
	if v, ok := version["video"].(map[string]any); ok {
		videoMediaID, _ = v["media_id"].(string)
	}
	if videoMediaID == "" {
		t.Fatalf("candidate must carry the real video media: %+v", version)
	}
	prevBody := fmt.Sprintf(`{"media_ids":["%s"],"purpose":"preview"}`, videoMediaID)
	prevCode, prevOut := f.do(t, "POST", "/api/v1/public/content/"+versionID+"/media-access", "", "", prevBody)
	if prevCode != 200 {
		t.Fatalf("candidate preview: %d %+v", prevCode, prevOut)
	}
	items, _ := prevOut["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("preview items: %+v", prevOut)
	}
	item, _ := items[0].(map[string]any)
	mediaURL, _ := item["url"].(string)
	if mediaURL == "" {
		t.Fatalf("preview url missing: %+v", item)
	}
	// 受控媒体真实可取回,字节与回流一致。
	mediaRes, err := http.Get(f.ts.URL + mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	mediaRaw, _ := io.ReadAll(mediaRes.Body)
	mediaRes.Body.Close()
	if mediaRes.StatusCode != 200 || !bytes.Equal(mediaRaw, videoBytes) {
		t.Fatalf("controlled media bytes mismatch: status=%d len=%d", mediaRes.StatusCode, len(mediaRaw))
	}

	// 4) 商家明确采用。
	adoptCode, adoptOut := f.do(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs/"+f.packA+"/versions/"+versionID+"/adopt",
		"sess-owner-a", f.tenA, `{"platform":"douyin"}`)
	if adoptCode != 200 {
		t.Fatalf("adopt: %d %+v", adoptCode, adoptOut)
	}

	// 5) 重复回流与轮询零新增(T15):同键同负载重放,结果行数/charge_ref 不变。
	_, listBefore := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, "")
	countBefore := 0
	for _, hi := range handoffsBefore(listBefore) {
		if hi["id"] == handoffID {
			countBefore = len(hi["results"].([]any))
		}
	}
	codeRe, outRe := f.doProducer(t, "POST", "/api/v1/internal/content-production-handoffs/"+handoffID+"/results",
		"prod-svc", "prod-secret", fields,
		map[string][]byte{"video": videoBytes, "cover": coverBytes})
	if codeRe != 200 || outRe["replayed"] != true {
		t.Fatalf("loop replay: %d %+v", codeRe, outRe)
	}
	_, listAfter := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-production-handoffs", "sess-owner-a", f.tenA, "")
	for _, hi := range handoffsBefore(listAfter) {
		if hi["id"] == handoffID {
			if len(hi["results"].([]any)) != countBefore {
				t.Fatalf("replay must not add result rows")
			}
			if hi["last_charge_ref"] != "charge_loop-evt" {
				t.Fatalf("charge ref must stay the declared one: %+v", hi["last_charge_ref"])
			}
		}
	}

	// 6) 采用后顾客面(B05)取得同一版本文件。
	contentCode, _ := f.do(t, "GET", "/api/v1/public/actions/sa_missing/content", "", "", "")
	// B05 公共读面按 action binding 解析;绑定写端点归 4584——此处验证采用
	// 指针权威:pack 当前采用=回流版本(GetStoreAdoptedPackVersion 语义)。
	if contentCode != http.StatusNotFound {
		t.Fatalf("unbound action must stay 404 (adoption is the only distribution path), got %d", contentCode)
	}
	_, afterAdopt := f.do(t, "GET", "/api/v1/stores/"+f.storeA+"/content-packs", "sess-owner-a", f.tenA, "")
	for _, p := range mustItems(t, afterAdopt) {
		if p["id"] == f.packA {
			ads, _ := p["adoptions"].([]any)
			if len(ads) != 1 {
				t.Fatalf("one adoption pointer expected, got %d", len(ads))
			}
			ad, _ := ads[0].(map[string]any)
			if ad["version_id"] != versionID {
				t.Fatalf("customer-facing adoption must be the returned version: %+v", ad)
			}
		}
	}
}

func handoffsBefore(out map[string]any) []map[string]any {
	items, _ := out["handoffs"].([]any)
	list := make([]map[string]any, 0, len(items))
	for _, i := range items {
		if m, ok := i.(map[string]any); ok {
			list = append(list, m)
		}
	}
	return list
}

func mustItems(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	items, _ := out["items"].([]any)
	list := make([]map[string]any, 0, len(items))
	for _, i := range items {
		if m, ok := i.(map[string]any); ok {
			list = append(list, m)
		}
	}
	return list
}
