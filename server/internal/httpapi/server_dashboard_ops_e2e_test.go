package httpapi

// server_dashboard_ops_e2e_test.go: HUI-1677 真实性验收(票面硬性项)——
// 固定已知事件集经真实表面播种(顾客→店员→兑奖),API 输出与测试自写的
// 独立 SQL 逐项对照;覆盖:同人/同参与多证据、跨平台尝试、重复/晚到回调、
// 人工更正、规则版本改变在途按原快照、库存竞争与一次核销。
//
// 播种纪律与唯一例外:
//   - 参与/内容/尝试/交接/确认/核销/撤销/访问 beacon 全部走真实 HTTP 表面;
//   - reward_official_facts 的写入面按 1671 设计只属于「受信官方查询集成」,
//     今天不存在(0025 表注释),故夹具直接写一行模拟该未来集成——这是事实
//     模拟,不是绕过任何校验;确认路径仍走真实 reward-confirm(锚定+领取)。

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
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type opsE2EFixture struct {
	s       *Server
	ts      *httptest.Server
	st      *store.Store
	tenA    string
	tenB    string
	storeID string
	campID  string
	code    string
}

func newOpsE2EFixture(t *testing.T) *opsE2EFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("看板E2E商家")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("隔壁商家")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.CreateStore(tenA.ID, "真实测试门店", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test" // 本流程不提交留资,不触网;仅满足配置门
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-app"
		case "LEADS_PHONE_PEPPER":
			return "test-pepper"
		case "FEATURE_DASHBOARD":
			return "on"
		case "FEATURE_PARTICIPATION":
			return "on"
		case "FEATURE_LEADS_CAPTURE":
			return "on" // 访问 beacon(view-events)挂在该开关下
		case "PUBLIC_BASE_URL":
			return "https://h5.example.com"
		}
		return ""
	})
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	now := time.Now().UTC()
	cmp, err := st.CreateCampaign(store.NewCampaign{
		TenantID: tenA.ID, Title: "真实走查活动", PublicContent: "发布领券",
		StartsAt: now.Add(-time.Hour).Format(time.RFC3339),
		EndsAt:   now.Add(48 * time.Hour).Format(time.RFC3339),
		StoreID:  s1.ID, CreatedBy: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionCampaign(cmp.ID, tenA.ID, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	lnk, err := st.CreateLink(tenA.ID, cmp.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &opsE2EFixture{s: s, ts: ts, st: st, tenA: tenA.ID, tenB: tenB.ID, storeID: s1.ID, campID: cmp.ID, code: lnk.Code}
}

func (f *opsE2EFixture) do(t *testing.T, method, path, session, tenant, cookie, body string) (int, http.Header, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, res.Header, out
}

func (f *opsE2EFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	status, _, out := f.do(t, method, path, "sess-owner-a", f.tenA, "", body)
	return status, out
}

// TestDashboardOpsRealWalkthrough 真实测试门店走查:顾客碰一碰→开始参与→选内容→
// 发起交接→店员确认发券→现场核销→经营看板显示真实结果(非 unknown)。
func TestDashboardOpsRealWalkthrough(t *testing.T) {
	f := newOpsE2EFixture(t)

	// ---- 顾客面(真实表面)----
	// 1. 访问 beacon(nfc 入口标记)。
	if status, _, _ := f.do(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", "", "", "", `{"channel":"nfc"}`); status != http.StatusNoContent {
		t.Fatalf("beacon: %d", status)
	}
	// 2. 明确参与(显式开始;重放同幂等键不建新行)。
	startBody := fmt.Sprintf(`{"link_code":%q,"touchpoint_source":"nfc","idempotency_key":"idem-1","rule_version":3}`, f.code)
	status, hdr, out := f.do(t, "POST", "/api/v1/participations", "", "", "", startBody)
	if status != http.StatusCreated {
		t.Fatalf("participation start = %d: %v", status, out)
	}
	ptID := out["id"].(string)
	cookie := cookiePair(hdr)
	// 重放同幂等键:同一访客(同 cookie)回原参与,绝不建第二行。
	if status, _, out2 := f.do(t, "POST", "/api/v1/participations", "", "", cookie, startBody); status != http.StatusOK || out2["id"].(string) != ptID {
		t.Fatalf("replay must return the same participation: %d %v", status, out2)
	}
	// 3. 内容选择(顾客挑定版本)。
	if status, _, out = f.do(t, "PATCH", "/api/v1/participations/"+ptID+"/content", "", "", cookie, `{"content_pack_version":"v3"}`); status != http.StatusOK {
		t.Fatalf("content: %d %v", status, out)
	}
	// 4. 跨平台尝试(douyin/h5 + kuaishou/platform_miniprogram)。
	if status, _, out = f.do(t, "POST", "/api/v1/participations/"+ptID+"/attempts", "", "", cookie, `{"platform":"douyin","handoff_method":"h5"}`); status != http.StatusCreated {
		t.Fatalf("attempt A: %d %v", status, out)
	}
	attA := out["id"].(string)
	if status, _, out = f.do(t, "POST", "/api/v1/participations/"+ptID+"/attempts", "", "", cookie, `{"platform":"kuaishou","handoff_method":"platform_miniprogram"}`); status != http.StatusCreated {
		t.Fatalf("attempt B: %d %v", status, out)
	}
	// 5. 发起平台交接(冻结 attA 并铸发凭据)。
	if status, _, out = f.do(t, "POST", "/api/v1/participations/"+ptID+"/handoff", "", "", cookie,
		fmt.Sprintf(`{"attempt_id":%q,"purpose":"customer_resume"}`, attA)); status != http.StatusCreated {
		t.Fatalf("handoff: %d %v", status, out)
	}
	// 6. 可证实的提交锚:顾客真实发布准备(预览,经 1670 公共表面)。
	status, _, out = f.do(t, "POST", "/api/v1/public/links/"+f.code+"/publish-attempts", "", "", "",
		`{"platform":"douyin","publisher":"activity_customer","copy":"今天打卡","account_label":"店主号"}`)
	if status != http.StatusCreated {
		t.Fatalf("publish preview: %d %v", status, out)
	}
	pubAttemptID := out["attempt_id"].(string)

	// 官方事实(受信官方查询集成写入面按 1671 设计今天不存在;夹具模拟晚到):
	// 人工先发,官方确认晚到——只补证据,不增加发券数。
	if _, err := f.st.DB.Exec(
		`INSERT INTO reward_official_facts(id,tenant_id,campaign_id,attempt_id,platform,platform_post_id,receipt_source,created_at)
		 VALUES('fact-late-1',?,?,?,'douyin','post-88','official_query',?)`,
		f.tenA, f.campID, pubAttemptID, time.Now().UTC().Add(time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	// ---- 商家面(真实表面)----
	// 批次 v3(slot first_publish)+ 批次 bonus_gift + 干扰批次 v4(同 slot 新版本,
	// 在途参与按原快照 v3 结算,v4 行必须保持零占用)。
	mkInv := func(slot string, version int, evidence []string, face, cost int64) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"kind": "store_coupon", "name": "开业券 " + slot, "benefit_slot": slot,
			"rule_version": version, "allowed_evidence": evidence,
			"total_qty": 5, "face_value_minor": face, "cost_minor": cost,
			"currency": "FEN", "store_id": f.storeID,
		})
		status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campID+"/reward-inventory", string(raw))
		if status != http.StatusOK {
			t.Fatalf("inventory %s v%d: %d %v", slot, version, status, out)
		}
		return out["id"].(string)
	}
	_ = mkInv("first_publish", 3, []string{"staff_observed_submission", "official_publish_confirmed"}, 500, 300)
	_ = mkInv("bonus_gift", 3, []string{"official_publish_confirmed"}, 200, 100)
	_ = mkInv("first_publish", 4, []string{"staff_observed_submission"}, 500, 300)

	// 店员确认(人工证据,现场见到提交)→ 发放。
	confirm := func(participationID, slot string, evidence, eventID string, official map[string]any) map[string]any {
		t.Helper()
		body := map[string]any{
			"participation_id": participationID, "benefit_slot": slot, "rule_version": 3,
			"event_id": eventID, "evidence": evidence, "store_id": f.storeID,
			"note": "现场核对",
		}
		if official != nil {
			body["official"] = official
		}
		raw, _ := json.Marshal(body)
		status, out := f.owner(t, "POST", "/api/v1/campaigns/"+f.campID+"/reward-confirm", string(raw))
		if status != http.StatusOK {
			t.Fatalf("confirm %s(%s): %d %v", slot, evidence, status, out)
		}
		return out
	}
	benA := confirm(ptID, "first_publish", "staff_observed_submission", "evt-confirm-1", nil)["benefit"].(map[string]any)
	benAID := benA["id"].(string)
	// 确认事件重放:同 event_id 回放原权益,绝不发第二份。
	status, replayOut := f.owner(t, "POST", "/api/v1/campaigns/"+f.campID+"/reward-confirm",
		fmt.Sprintf(`{"participation_id":%q,"benefit_slot":"first_publish","rule_version":3,"event_id":"evt-confirm-1","evidence":"staff_observed_submission","store_id":%q}`,
			ptID, f.storeID))
	if status != http.StatusOK || replayOut["benefit"].(map[string]any)["id"].(string) != benAID {
		t.Fatalf("confirm replay must answer unchanged: %d %v", status, replayOut)
	}
	// 同一参与的第二槽位走官方证据(同一参与两种证据,不能相加)。
	benB := confirm(ptID, "bonus_gift", "official_publish_confirmed", "evt-confirm-2",
		map[string]any{"fact_id": pubAttemptID, "platform_post_id": "post-88", "receipt_source": "official_query"})["benefit"].(map[string]any)
	_ = benB["id"]
	// 人工先发的证据永不改写成官方:benA 仍是 staff_observed_submission。
	if got := benA["evidence"].(string); got != "staff_observed_submission" {
		t.Fatalf("manual evidence must never be rewritten: %q", got)
	}

	// 现场核销 + 竞争重放(一次核销)。
	redeem := func(benefitID, eventID string) (int, map[string]any) {
		t.Helper()
		return f.owner(t, "POST", "/api/v1/campaigns/"+f.campID+"/reward-benefits/"+benefitID+"/redeem",
			fmt.Sprintf(`{"event_id":%q,"store_id":%q}`, eventID, f.storeID))
	}
	benefitStatus := func(m map[string]any) string { return m["benefit"].(map[string]any)["status"].(string) }
	if status, out = redeem(benAID, "evt-redeem-1"); status != http.StatusOK || benefitStatus(out) != "redeemed" {
		t.Fatalf("redeem: %d %v", status, out)
	}
	if status, out = redeem(benAID, "evt-redeem-2"); status != http.StatusOK || benefitStatus(out) != "redeemed" {
		t.Fatalf("second staff must read already-redeemed state: %d %v", status, out)
	}
	// 第二位顾客(qr 入口):店员确认发放后人工更正撤销(追加更正,保留历史)。
	// bonus_gift 保持 issued——同一参与(pt1)的两种证据都留在 granted 账面。
	status, _, out = f.do(t, "POST", "/api/v1/participations", "", "", "",
		fmt.Sprintf(`{"link_code":%q,"touchpoint_source":"qr","idempotency_key":"idem-2","rule_version":3}`, f.code))
	if status != http.StatusCreated {
		t.Fatalf("participation 2: %d %v", status, out)
	}
	pt2 := out["id"].(string)
	_ = pt2 // 第二位顾客不选内容、不交接:内容选择/交接 distinct 仍为 pt1
	benC := confirm(pt2, "first_publish", "staff_observed_submission", "evt-confirm-3", nil)["benefit"].(map[string]any)
	benCID := benC["id"].(string)
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.campID+"/reward-benefits/"+benCID+"/reverse",
		`{"reason":"券码录错","event_id":"evt-reverse-1"}`)
	if status != http.StatusOK || benefitStatus(out) != "reversed" {
		t.Fatalf("reverse: %d %v", status, out)
	}

	// ---- 经营看板:API 输出 vs 独立 SQL 逐项复算 ----
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	end := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	status, dash := f.owner(t, "GET",
		fmt.Sprintf("/api/v1/dashboard/operations?window_start=%s&window_end=%s",
			start.Format(time.RFC3339), end.Format(time.RFC3339)), ``)
	if status != http.StatusOK {
		t.Fatalf("operations = %d: %v", status, dash)
	}
	assertOpsAgainstIndependentSQL(t, f, dash, start, end)

	// 真实结果而非永久 unknown:漏斗各级都是具体数字。
	funnel, _ := dash["funnel"].([]any)
	wantValues := map[string]float64{
		"funnel_visits": 1, "funnel_started": 2, "funnel_content_selected": 1,
		"funnel_handoff_initiated": 1, "funnel_publish_verified": 1,
		"funnel_rewards_issued": 2, "funnel_redeemed": 1,
	}
	for _, stageAny := range funnel {
		stage := stageAny.(map[string]any)
		key := stage["key"].(string)
		if want, ok := wantValues[key]; ok {
			if stage["value"].(float64) != want {
				t.Fatalf("funnel %s = %v, want %v", key, stage["value"], want)
			}
			delete(wantValues, key)
		}
	}
	if len(wantValues) != 0 {
		t.Fatalf("funnel stages missing: %v", wantValues)
	}

	// 同一参与两种证据:证据类计数 1+1,但 distinct 覆盖必须是 1(不能相加)。
	ev := dash["evidence"].(map[string]any)
	byClass := ev["by_class"].(map[string]any)
	if byClass["staff_observed_submission"].(float64) != 1 || byClass["official_publish_confirmed"].(float64) != 1 {
		t.Fatalf("evidence classes = %v, want 1+1 (reversed benC excluded)", byClass)
	}
	if ev["distinct_participations"].(float64) != 1 {
		t.Fatalf("distinct participation coverage = %v, want 1 (pt1 carries both classes; never additive)", ev["distinct_participations"])
	}
	if ev["official_attempts"].(float64) != 1 {
		t.Fatalf("official attempts = %v, want 1", ev["official_attempts"])
	}

	// 库存(账本当前态):v3 first_publish 批次 redeemed 1(available 4);
	// v4 干扰批次零占用(在途按原快照);bonus_gift 批次 reversed 1。
	inv := dash["inventory"].([]any)
	bySlotVersion := map[string]map[string]any{}
	for _, lineAny := range inv {
		line := lineAny.(map[string]any)
		bySlotVersion[fmt.Sprintf("%s/v%d", line["benefit_slot"], int(line["rule_version"].(float64)))] = line
	}
	num := func(m map[string]any, key string) float64 {
		t.Helper()
		v, ok := m[key].(float64)
		if !ok {
			t.Fatalf("field %q missing or not a number in %v", key, m)
		}
		return v
	}
	fp3, ok3 := bySlotVersion["first_publish/v3"]
	if !ok3 {
		t.Fatalf("first_publish/3 line missing: %v", bySlotVersion)
	}
	if num(fp3, "redeemed") != 1 || num(fp3, "reversed") != 1 || num(fp3, "available") != 4 || num(fp3, "issued") != 0 {
		t.Fatalf("first_publish v3 line wrong: %v", fp3)
	}
	fp4, ok4 := bySlotVersion["first_publish/v4"]
	if !ok4 {
		t.Fatalf("first_publish/4 line missing: %v", bySlotVersion)
	}
	if num(fp4, "reserved") != 0 || num(fp4, "issued") != 0 || num(fp4, "redeemed") != 0 {
		t.Fatalf("v4 batch must stay untouched (in-flight keeps v3 snapshot): %v", fp4)
	}
	bg, ok5 := bySlotVersion["bonus_gift/v3"]
	if !ok5 {
		t.Fatalf("bonus_gift/3 line missing: %v", bySlotVersion)
	}
	if num(bg, "issued") != 1 || num(bg, "available") != 4 {
		t.Fatalf("bonus_gift line wrong: %v", bg)
	}

	// 成本三分:奖励实际成本=300(issued+redeemed:benA);已兑现成本=300;
	// 面额=500;撤销的 benB 不计;内容生成费用=未录入。
	cost := dash["cost"].(map[string]any)
	if cost["reward_cost_minor"].(float64) != 400 || cost["reward_redeemed_cost_minor"].(float64) != 300 || cost["reward_face_minor"].(float64) != 700 {
		t.Fatalf("reward cost wrong: %v", cost)
	}
	if cost["content_generation_available"].(bool) {
		t.Fatalf("content generation cost without billed runs must be 未录入: %v", cost)
	}
	// 异常:更正 1(reversed),其余 0。
	an := dash["anomalies"].(map[string]any)
	if an["reversed_in_window"].(float64) != 1 || an["pending_review"].(float64) != 0 || an["reservation_failed"].(float64) != 0 {
		t.Fatalf("anomalies wrong: %v", an)
	}

	// 首页摘要(2628 契约):今天已发 1、已兑现 1、异常 1、待确认 0。
	status, sum := f.owner(t, "GET", "/api/v1/dashboard/operations-summary", ``)
	if status != http.StatusOK {
		t.Fatalf("summary = %d", status)
	}
	if sum["rewards_issued"].(float64) != 2 || sum["rewards_redeemed"].(float64) != 1 ||
		sum["anomalies"].(float64) != 1 || sum["pending_confirm"].(float64) != 0 {
		t.Fatalf("summary wrong: %v", sum)
	}

	// 重算幂等:同窗口再取一次,漏斗与库存逐字节一致。
	status, dash2 := f.owner(t, "GET",
		fmt.Sprintf("/api/v1/dashboard/operations?window_start=%s&window_end=%s",
			start.Format(time.RFC3339), end.Format(time.RFC3339)), ``)
	if status != http.StatusOK {
		t.Fatalf("recompute = %d", status)
	}
	a, _ := json.Marshal(dash["funnel"])
	b, _ := json.Marshal(dash2["funnel"])
	if string(a) != string(b) {
		t.Fatalf("recompute must be idempotent")
	}
}

// assertOpsAgainstIndependentSQL 用测试内自写的独立 SQL(不经过实现路径)
// 逐项复算固定事件集,并与 API 输出对照。
func assertOpsAgainstIndependentSQL(t *testing.T, f *opsE2EFixture, dash map[string]any, start, end time.Time) {
	t.Helper()
	q := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := f.st.DB.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("independent sql: %v", err)
		}
		return n
	}
	s, e := start.Format(time.RFC3339), end.Format(time.RFC3339)
	visits := q(`SELECT COALESCE(SUM(views),0) FROM public_view_stats v
		JOIN campaign_links l ON l.code=v.code JOIN campaigns c ON c.id=l.campaign_id
		WHERE c.tenant_id=? AND v.day>=? AND v.day<=?`, f.tenA, start.Format("2006-01-02"), end.Format("2006-01-02"))
	started := q(`SELECT COUNT(DISTINCT id) FROM participations WHERE tenant_id=? AND datetime(created_at)>=datetime(?) AND datetime(created_at)<=datetime(?)`, f.tenA, s, e)
	selected := q(`SELECT COUNT(*) FROM (
		SELECT participation_id FROM participation_attempts WHERE tenant_id=? AND content_pack_version<>''
		GROUP BY participation_id HAVING datetime(MIN(created_at))>=datetime(?) AND datetime(MIN(created_at))<=datetime(?))`, f.tenA, s, e)
	handoff := q(`SELECT COUNT(*) FROM (
		SELECT participation_id FROM handoff_credentials WHERE tenant_id=?
		GROUP BY participation_id HAVING datetime(MIN(created_at))>=datetime(?) AND datetime(MIN(created_at))<=datetime(?))`, f.tenA, s, e)
	evidence := q(`SELECT COUNT(DISTINCT rb.participation_id) FROM reward_benefits rb
		WHERE rb.tenant_id=? AND rb.evidence<>'' AND rb.status IN ('issued','redeemed')
		AND datetime(rb.issued_at)>=datetime(?) AND datetime(rb.issued_at)<=datetime(?)`, f.tenA, s, e)
	issued := q(`SELECT COUNT(*) FROM reward_benefits rb
		WHERE rb.tenant_id=? AND rb.status IN ('issued','redeemed')
		AND datetime(rb.issued_at)>=datetime(?) AND datetime(rb.issued_at)<=datetime(?)`, f.tenA, s, e)
	redeemed := q(`SELECT COUNT(*) FROM reward_benefits rb
		WHERE rb.tenant_id=? AND rb.status='redeemed'
		AND datetime(rb.redeemed_at)>=datetime(?) AND datetime(rb.redeemed_at)<=datetime(?)`, f.tenA, s, e)
	official := q(`SELECT COUNT(DISTINCT attempt_id) FROM reward_official_facts
		WHERE tenant_id=? AND datetime(created_at)>=datetime(?) AND datetime(created_at)<=datetime(?)`, f.tenA, s, e)

	funnel, _ := dash["funnel"].([]any)
	got := map[string]float64{}
	for _, stageAny := range funnel {
		stage := stageAny.(map[string]any)
		got[stage["key"].(string)] = stage["value"].(float64)
	}
	for key, want := range map[string]int64{
		"funnel_visits": visits, "funnel_started": started, "funnel_content_selected": selected,
		"funnel_handoff_initiated": handoff, "funnel_publish_verified": evidence,
		"funnel_rewards_issued": issued, "funnel_redeemed": redeemed,
	} {
		if got[key] != float64(want) {
			t.Fatalf("%s: api=%v independent-sql=%d", key, got[key], want)
		}
	}
	ev := dash["evidence"].(map[string]any)
	if ev["distinct_participations"].(float64) != float64(evidence) {
		t.Fatalf("evidence coverage: api=%v sql=%d", ev["distinct_participations"], evidence)
	}
	if ev["official_attempts"].(float64) != float64(official) {
		t.Fatalf("official attempts: api=%v sql=%d", ev["official_attempts"], official)
	}
}
