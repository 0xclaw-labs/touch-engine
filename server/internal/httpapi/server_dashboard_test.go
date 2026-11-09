package httpapi

// server_dashboard_test.go: HUI-1677 FEAT-0178 数据统计看板 HTTP 验收矩阵。
//
// 纪律(与工单一致):
//   - 已知事件集全部经真实表面播种(guest beacon / lead-submissions /
//     lead-revocations / forwarder 真实状态机),绝不直接插库;
//   - API 输出与独立 SQL 复算逐项对照(测试自己写聚合,不走实现路径);
//   - 同窗口重算幂等;去重分母;撤销不计正向;跨租户 0 行;
//   - UNKNOWN 指标 available=false 且值不出现(绝不是 0);
//   - 窗口非法 4xx;FEATURE_DASHBOARD off = 路由不注册,404 不可见。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// ---- dashboard fixture -------------------------------------------------------------

type dashboardFixture struct {
	s         *Server
	ts        *httptest.Server
	tenA      string
	tenB      string
	stub      *notifyStub
	forwarder *leads.Forwarder

	store1ID string
	camp1ID  string // bound to store1, lead form on
	camp2ID  string // unbound, lead form on
	code1    string
	code2    string
	tag1ID   string
}

func dashboardTestConfig(notifyURL string, dashboardOn bool) config.Config {
	dash := "off"
	if dashboardOn {
		dash = "on"
	}
	return config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return notifyURL
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-app"
		case "LEADS_PHONE_PEPPER":
			return "test-pepper"
		case "FEATURE_LEADS_CAPTURE":
			return "on"
		case "FEATURE_DASHBOARD":
			return dash
		}
		return ""
	})
}

func newDashboardFixture(t *testing.T) *dashboardFixture {
	return newDashboardFixtureOpt(t, true)
}

// newDashboardFixtureOpt lets a test boot with FEATURE_DASHBOARD off to prove
// the route is never registered (off = invisible surface at the mux level).
func newDashboardFixtureOpt(t *testing.T, dashboardOn bool) *dashboardFixture {
	t.Helper()
	stub := newNotifyStub(t)
	cfg := dashboardTestConfig(stub.srv.URL, dashboardOn)

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("商家B")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.CreateStore(tenA.ID, "旗舰店", "地址1", "test")
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
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_sm_a", "store_manager", "sm", "test", true, s1.ID); err != nil {
		t.Fatalf("seed sm: %v", err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-sm-a":    {"usr_sm_a", "sm@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
		// 畸形经理会话:principal 先行注册,member 在具体测试中创建
		"sess-sm-bad": {"usr_sm_bad", "bad@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	f := &dashboardFixture{
		s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, stub: stub, store1ID: s1.ID,
		forwarder: leads.NewForwarder(s.St, newTestPoster(stub), log.New(io.Discard, "", 0)),
	}

	// 活动 1:绑 store1,启用了留资表单(经 admin 真实表面)
	mkCamp := func(title, storeID string) (string, string) {
		body := fmt.Sprintf(`{"title":%q,"public_content":"到店礼","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`, title)
		if storeID != "" {
			body = fmt.Sprintf(`{"title":%q,"public_content":"到店礼","store_id":%q,"starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`, title, storeID)
		}
		status, _, out := f.admin(t, "POST", "/api/v1/campaigns", body)
		if status != 201 {
			t.Fatalf("campaign create %s: %d %v", title, status, out)
		}
		id := out["id"].(string)
		if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/status", `{"status":"active"}`); status != 200 {
			t.Fatalf("activate %s failed", title)
		}
		status, _, link := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/links", ``)
		if status != 201 {
			t.Fatalf("link create %s failed", title)
		}
		if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/lead-form", `{"marketing_optin_enabled":true}`); status != 201 {
			t.Fatalf("lead form %s failed", title)
		}
		return id, link["code"].(string)
	}
	f.camp1ID, f.code1 = mkCamp("店头活动A", s1.ID)
	f.camp2ID, f.code2 = mkCamp("线上活动B", "")

	// NFC 标签 1 枚挂 camp1 的短码(绑 store1)—— 触发下钻 by_tag 的来源
	status, _, link := f.admin(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/links", ``)
	if status != 200 {
		t.Fatal("link list failed")
	}
	linkID := link["items"].([]any)[0].(map[string]any)["id"].(string)
	status, _, batch := f.admin(t, "POST", "/api/v1/nfc/tags/batch",
		batchReq(f.camp1ID, []string{linkID}, 1, s1.ID, "", "入口A", "shared"))
	if status != 201 {
		t.Fatalf("tag batch: %d %v", status, batch)
	}
	f.tag1ID = batch["items"].([]any)[0].(map[string]any)["id"].(string)
	return f
}

func (f *dashboardFixture) do(t *testing.T, method, path, session, tenant, body string) (int, http.Header, map[string]any) {
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
	return res.StatusCode, res.Header, out
}

func (f *dashboardFixture) admin(t *testing.T, method, path, body string) (int, http.Header, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

func (f *dashboardFixture) guest(t *testing.T, method, path, body string) (int, http.Header, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "", "", body)
}

func (f *dashboardFixture) submitLead(t *testing.T, code, body string) (int, map[string]any) {
	t.Helper()
	status, _, out := f.guest(t, "POST", "/api/v1/public/links/"+code+"/lead-submissions", body)
	return status, out
}

func (f *dashboardFixture) beacon(t *testing.T, code, channel string) {
	t.Helper()
	body := ""
	if channel != "" {
		body = fmt.Sprintf(`{"channel":%q}`, channel)
	}
	if status, _, out := f.guest(t, "POST", "/api/v1/public/links/"+code+"/view-events", body); status != http.StatusNoContent {
		t.Fatalf("beacon %s: %d %v", code, status, out)
	}
}

// dashboardGet fetches the dashboard over the window and returns status, raw
// bytes (for idempotency) and parsed body.
func (f *dashboardFixture) dashboardGet(t *testing.T, session, tenant, winStart, winEnd string) (int, []byte, map[string]any) {
	t.Helper()
	q := "/api/v1/dashboard?window_start=" + winStart + "&window_end=" + winEnd
	req, err := http.NewRequest("GET", f.ts.URL+q, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", session)
	req.Header.Set("X-Tenant-ID", tenant)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, raw, out
}

// dashWindow returns an RFC3339 window comfortably covering "now" facts.
func dashWindow() (string, string) {
	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	end := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

func dashMetric(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	items, ok := body["metrics"].([]any)
	if !ok {
		t.Fatalf("metrics missing: %v", body)
	}
	for _, it := range items {
		m := it.(map[string]any)
		if m["key"] == key {
			return m
		}
	}
	t.Fatalf("metric %q missing from response", key)
	return nil
}

// dashBuckets flattens one breakdown (JSON array of {key,value}) into a map.
func dashBuckets(t *testing.T, m map[string]any, name string) map[string]int64 {
	t.Helper()
	bks, ok := m["breakdowns"].(map[string]any)
	if !ok {
		t.Fatalf("%s: breakdowns missing: %v", m["key"], m)
	}
	arr, ok := bks[name].([]any)
	if !ok {
		t.Fatalf("%s: breakdown %s missing: %v", m["key"], name, bks)
	}
	out := map[string]int64{}
	for _, it := range arr {
		b := it.(map[string]any)
		out[b["key"].(string)] = int64(b["value"].(float64))
	}
	return out
}

func dashSQLScalar(t *testing.T, f *dashboardFixture, label, q string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := f.s.St.DB.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("sql recompute %s: %v", label, err)
	}
	return v
}

// seedDashboardEvents 播种已知事件集(全部经真实表面):
//   - 触发:code1 wecom ×2,code2 web(缺省)×1
//   - 留资:联系人甲(138…)提交 + 重复提交(幂等,同 ref);联系人乙(137…)提交
//   - 状态机:Tick→pending_sync,DeliverAll+Tick 后仍 pending_sync(传输层事实),
//     受信接收方 ACK(store.AckLeadReceipt)→crm_received(HUI-4595 前真实链)
//   - 撤销:甲撤销(revoked 为终态,updated_at 落在窗口内)
func seedDashboardEvents(t *testing.T, f *dashboardFixture) (refA string) {
	t.Helper()
	f.beacon(t, f.code1, "wecom")
	f.beacon(t, f.code1, "wecom")
	f.beacon(t, f.code2, "")

	st, out := f.submitLead(t, f.code1, `{"name":"甲","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"wecom"}`)
	if st != 201 || out["state"] != "accepted" {
		t.Fatalf("submit A: %d %v", st, out)
	}
	refA = out["submission_ref"].(string)
	if st, out := f.submitLead(t, f.code1, `{"name":"甲","phone":"+8613800138000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"wecom"}`); st != 200 || out["submission_ref"] != refA || out["duplicate"] != true {
		t.Fatalf("duplicate submit must be idempotent same ref: %d %v", st, out)
	}
	if st, out := f.submitLead(t, f.code1, `{"name":"乙","phone":"13700137000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"web"}`); st != 201 {
		t.Fatalf("submit B: %d %v", st, out)
	}

	f.forwarder.Tick(context.Background()) // publish -> pending_sync
	f.stub.DeliverAll()
	f.forwarder.Tick(context.Background()) // delivered: transport fact only
	refB := ""
	for _, l := range func() []store.LeadSubmission { v, _ := f.s.St.ListLeadSubmissions(f.tenA, f.camp1ID); return v }() {
		if l.Phone == "13700137000" {
			refB = l.SubmissionRef
		}
	}
	if refB == "" {
		t.Fatal("lead B missing")
	}
	// HUI-4594: the receiver's persisted ACK is the only crm_received path
	if _, _, err := f.s.St.AckLeadReceipt(f.tenA, refB, "leads-ingest", store.LeadAck{
		ReceiptID: "rcp_dash_b", SourceEventID: refB, SubmissionID: refB,
		SourceRevision: 1, ReceiverState: store.ReceiverStateStored,
	}); err != nil {
		t.Fatalf("ack B: %v", err)
	}

	revoke := fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, refA)
	if st, _, resp := f.guest(t, "POST", "/api/v1/public/links/"+f.code1+"/lead-revocations", revoke); st != 200 || resp["state"] != "revoked" {
		t.Fatalf("revoke: %d %v", st, resp)
	}
	f.stub.DeliverAll()
	f.forwarder.Tick(context.Background()) // drain revocation event
	return refA
}

// ---- 1. 开关 off = 路由不注册,404 不可见 -----------------------------------------------

func TestDashboardFeatureOffIsInvisible404(t *testing.T) {
	f := newDashboardFixture(t)
	f.s.Cfg.FeatureDashboard = false
	start, end := dashWindow()
	status, _, out := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-owner-a", f.tenA, "")
	if status != 404 {
		t.Fatalf("flag off dashboard = %d %v, want 404 (route not registered)", status, out)
	}
}

// ---- 1b. 开关 off 开机 = 路由根本不注册(带合法会话也 404) ------------------------------

func TestDashboardFlagOffAtBootRouteNotRegistered(t *testing.T) {
	f := newDashboardFixtureOpt(t, false)
	start, end := dashWindow()
	status, _, out := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-owner-a", f.tenA, "")
	if status != 404 {
		t.Fatalf("boot-off dashboard = %d %v, want 404 (route never registered)", status, out)
	}
}

// ---- 2. 需要会话 ---------------------------------------------------------------------

func TestDashboardRequiresSession(t *testing.T) {
	f := newDashboardFixture(t)
	start, end := dashWindow()
	status, _, _ := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "", f.tenA, "")
	if status != 401 {
		t.Fatalf("anonymous dashboard = %d, want 401", status)
	}
}

// ---- 3. 已知事件集:API 输出 vs 独立 SQL 复算逐项一致 + UNKNOWN 诚实 ---------------------

func TestDashboardKnownEventSetMatchesSQLRecompute(t *testing.T) {
	f := newDashboardFixture(t)
	seedDashboardEvents(t, f)
	start, end := dashWindow()
	startT, _ := time.Parse(time.RFC3339, start)
	endT, _ := time.Parse(time.RFC3339, end)
	dayStart, dayEnd := startT.Format("2006-01-02"), endT.Format("2006-01-02")

	status, _, body := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("dashboard = %d %v", status, body)
	}

	// 独立复算:不复用实现的 JOIN 路径,直接按 API 已知短码/活动过滤。
	sqlTriggers := dashSQLScalar(t, f, "triggers",
		`SELECT COALESCE(SUM(views),0) FROM public_view_stats WHERE code IN (?,?) AND day>=? AND day<=?`,
		f.code1, f.code2, dayStart, dayEnd)
	sqlByChannel := map[string]int64{}
	func() {
		rows, err := f.s.St.DB.Query(
			`SELECT channel, SUM(views) FROM public_view_stats WHERE code IN (?,?) AND day>=? AND day<=? GROUP BY channel`,
			f.code1, f.code2, dayStart, dayEnd)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var v int64
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			sqlByChannel[k] = v
		}
	}()
	sqlSubmissions := dashSQLScalar(t, f, "submissions",
		`SELECT COUNT(1) FROM lead_submissions WHERE campaign_id IN (?,?) AND sync_state<>'revoked' AND datetime(created_at)>=datetime(?) AND datetime(created_at)<=datetime(?)`,
		f.camp1ID, f.camp2ID, start, end)
	sqlUnique := dashSQLScalar(t, f, "unique",
		`SELECT COUNT(DISTINCT phone) FROM lead_submissions WHERE campaign_id IN (?,?) AND sync_state<>'revoked' AND datetime(created_at)>=datetime(?) AND datetime(created_at)<=datetime(?)`,
		f.camp1ID, f.camp2ID, start, end)
	sqlCRM := dashSQLScalar(t, f, "crm",
		`SELECT COUNT(1) FROM lead_submissions WHERE campaign_id IN (?,?) AND sync_state='crm_received' AND datetime(updated_at)>=datetime(?) AND datetime(updated_at)<=datetime(?)`,
		f.camp1ID, f.camp2ID, start, end)

	// 手工期望(已知事件集逐项推导)
	wantTriggers := int64(3)
	if sqlTriggers != wantTriggers {
		t.Fatalf("sql triggers = %d, want %d (event set broken)", sqlTriggers, wantTriggers)
	}
	if len(sqlByChannel) != 2 || sqlByChannel["wecom"] != 2 || sqlByChannel["web"] != 1 {
		t.Fatalf("sql by_channel = %v", sqlByChannel)
	}
	if sqlSubmissions != 1 || sqlUnique != 1 || sqlCRM != 1 {
		t.Fatalf("sql facts = subs %d unique %d crm %d, want 1/1/1 (甲 revoked; 重复提交去重)", sqlSubmissions, sqlUnique, sqlCRM)
	}

	// API 逐项对照 SQL
	mTrig := dashMetric(t, body, "touch_triggers")
	if mTrig["available"] != true {
		t.Fatalf("touch_triggers available = %v", mTrig["available"])
	}
	if got := int64(mTrig["value"].(float64)); got != sqlTriggers {
		t.Fatalf("touch_triggers API %d != SQL %d", got, sqlTriggers)
	}
	if ch := dashBuckets(t, mTrig, "by_channel"); ch["wecom"] != 2 || ch["web"] != 1 {
		t.Fatalf("by_channel API = %v", ch)
	}
	if bc := dashBuckets(t, mTrig, "by_campaign"); bc[f.camp1ID] != 2 || bc[f.camp2ID] != 1 {
		t.Fatalf("by_campaign API = %v", bc)
	}
	if bs := dashBuckets(t, mTrig, "by_store"); bs[f.store1ID] != 2 || bs[""] != 1 {
		t.Fatalf("by_store API = %v", bs)
	}
	if bt := dashBuckets(t, mTrig, "by_tag"); bt[f.tag1ID] != 2 || len(bt) != 1 {
		t.Fatalf("by_tag API = %v, want only tag1:2", bt)
	}

	mVisit := dashMetric(t, body, "lead_page_visits")
	if int64(mVisit["value"].(float64)) != 3 {
		t.Fatalf("lead_page_visits = %v, want 3", mVisit["value"])
	}

	mSub := dashMetric(t, body, "lead_submissions")
	if int64(mSub["value"].(float64)) != sqlSubmissions {
		t.Fatalf("lead_submissions API %v != SQL %d", mSub["value"], sqlSubmissions)
	}
	if int64(mSub["unique_contacts"].(float64)) != sqlUnique {
		t.Fatalf("unique_contacts API %v != SQL %d", mSub["unique_contacts"], sqlUnique)
	}
	subState := dashBuckets(t, mSub, "by_state")
	// 全状态披露:revoked 出现在分桶披露,但不计入正向值
	if subState["revoked"] != 1 || subState["crm_received"] != 1 {
		t.Fatalf("by_state API = %v, want crm_received:1 revoked:1", subState)
	}

	mCRM := dashMetric(t, body, "crm_received")
	if int64(mCRM["value"].(float64)) != sqlCRM {
		t.Fatalf("crm_received API %v != SQL %d", mCRM["value"], sqlCRM)
	}

	// 每个指标必须携带完整口径定义(source/dedup_key/denominator/event_time/window)
	for _, key := range []string{"touch_triggers", "lead_page_visits", "lead_submissions", "crm_received",
		"published_videos", "ugc_plays", "ugc_likes", "poi_exposure_delta", "coupon_redemptions"} {
		m := dashMetric(t, body, key)
		def, ok := m["definition"].(map[string]any)
		if !ok {
			t.Fatalf("%s: definition missing", key)
		}
		for _, f := range []string{"source", "dedup_key", "denominator", "event_time", "window"} {
			if s, _ := def[f].(string); strings.TrimSpace(s) == "" {
				t.Fatalf("%s: definition.%s empty: %v", key, f, def)
			}
		}
	}

	// UNKNOWN 指标:available=false,值绝不出现(不是 0),原因+上游引用非空。
	// 发布视频数也在其中:本部署存不下官方回执,空计数不能显示成 0。
	for _, key := range []string{"published_videos", "ugc_plays", "ugc_likes", "poi_exposure_delta", "coupon_redemptions"} {
		m := dashMetric(t, body, key)
		if m["available"] != false {
			t.Fatalf("%s available = %v, want false", key, m["available"])
		}
		if v, present := m["value"]; present && v != nil {
			t.Fatalf("%s value present = %v, want absent/null (UNKNOWN 绝不冒充 0)", key, v)
		}
		if r, _ := m["reason"].(string); r == "" {
			t.Fatalf("%s reason empty", key)
		}
		if up, ok := m["upstream"].([]any); !ok || len(up) == 0 {
			t.Fatalf("%s upstream refs missing", key)
		}
	}
}

// ---- 4. 同窗口重算幂等 ---------------------------------------------------------------

func TestDashboardRecomputeIdempotent(t *testing.T) {
	f := newDashboardFixture(t)
	seedDashboardEvents(t, f)
	start, end := dashWindow()
	st1, raw1, _ := f.dashboardGet(t, "sess-owner-a", f.tenA, start, end)
	st2, raw2, _ := f.dashboardGet(t, "sess-owner-a", f.tenA, start, end)
	if st1 != 200 || st2 != 200 {
		t.Fatalf("status = %d/%d", st1, st2)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Fatalf("same-window recompute differs:\n%s\n---\n%s", raw1, raw2)
	}
}

// ---- 5. 窗口参数非法 4xx -------------------------------------------------------------

func TestDashboardInvalidWindowParams(t *testing.T) {
	f := newDashboardFixture(t)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tc := range []struct{ name, q string }{
		{"missing both", "/api/v1/dashboard"},
		{"missing end", "/api/v1/dashboard?window_start=" + now},
		{"missing start", "/api/v1/dashboard?window_end=" + now},
		{"garbage", "/api/v1/dashboard?window_start=not-a-time&window_end=" + now},
		{"end before start", "/api/v1/dashboard?window_start=" + now + "&window_end=2020-01-01T00%3A00%3A00Z"},
	} {
		status, _, out := f.do(t, "GET", tc.q, "sess-owner-a", f.tenA, "")
		if status != 400 || out["error"] != "invalid_window" {
			t.Fatalf("%s: %d %v, want 400 invalid_window", tc.name, status, out)
		}
	}
}

// ---- 6. 跨租户 0 行 ------------------------------------------------------------------

func TestDashboardCrossTenantZeroRows(t *testing.T) {
	f := newDashboardFixture(t)
	seedDashboardEvents(t, f)
	start, end := dashWindow()
	status, _, body := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-owner-b", f.tenB, "")
	if status != 200 {
		t.Fatalf("tenant B dashboard = %d %v", status, body)
	}
	for _, key := range []string{"touch_triggers", "lead_page_visits", "lead_submissions", "crm_received"} {
		m := dashMetric(t, body, key)
		if m["available"] != true || int64(m["value"].(float64)) != 0 {
			t.Fatalf("tenant B %s = %v/%v, want available true zero rows", key, m["available"], m["value"])
		}
		if bks, ok := m["breakdowns"].(map[string]any); ok {
			for name, arr := range bks {
				if a, ok := arr.([]any); !ok || len(a) != 0 {
					t.Fatalf("tenant B %s breakdown %s = %v, want empty", key, name, arr)
				}
			}
		}
	}
	// 甲的租户不受影响
	status, _, bodyA := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-owner-a", f.tenA, "")
	if status != 200 || int64(dashMetric(t, bodyA, "touch_triggers")["value"].(float64)) != 3 {
		t.Fatalf("tenant A polluted: %d %v", status, bodyA)
	}
}

// ---- 7. 门店经理作用域:只见本店;空作用域 fail-closed ---------------------------------

func TestDashboardStoreManagerScope(t *testing.T) {
	f := newDashboardFixture(t)
	seedDashboardEvents(t, f)
	start, end := dashWindow()
	status, _, body := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-sm-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("sm dashboard = %d %v", status, body)
	}
	if got := int64(dashMetric(t, body, "touch_triggers")["value"].(float64)); got != 2 {
		t.Fatalf("sm touch_triggers = %d, want 2 (camp2 未绑店,不可见)", got)
	}
	if got := int64(dashMetric(t, body, "lead_page_visits")["value"].(float64)); got != 2 {
		t.Fatalf("sm lead_page_visits = %d, want 2", got)
	}
	if got := int64(dashMetric(t, body, "lead_submissions")["value"].(float64)); got != 1 {
		t.Fatalf("sm lead_submissions = %d, want 1", got)
	}
	if got := int64(dashMetric(t, body, "crm_received")["value"].(float64)); got != 1 {
		t.Fatalf("sm crm_received = %d, want 1", got)
	}
	dims := body["dimensions"].(map[string]any)
	if got := len(dims["stores"].([]any)); got != 1 {
		t.Fatalf("sm dims stores = %d, want 1", got)
	}
	if got := len(dims["campaigns"].([]any)); got != 1 {
		t.Fatalf("sm dims campaigns = %d, want 1", got)
	}
	tags := dims["tags"].([]any)
	if len(tags) != 1 || tags[0].(map[string]any)["id"] != f.tag1ID {
		t.Fatalf("sm dims tags = %v, want only tag1", tags)
	}

	// 畸形经理(角色是 store_manager 但无门店作用域):fail-closed 403
	if _, err := f.s.St.CreateMemberScoped(f.tenA, "usr_sm_bad", "store_manager", "bad", "test", true, ""); err != nil {
		t.Fatal(err)
	}
	statusBad, _, outBad := f.do(t, "GET", "/api/v1/dashboard?window_start="+start+"&window_end="+end, "sess-sm-bad", f.tenA, "")
	if statusBad != 403 {
		t.Fatalf("scopeless store manager = %d %v, want 403 (fail-closed)", statusBad, outBad)
	}
}
