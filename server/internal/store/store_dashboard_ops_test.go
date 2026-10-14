package store

// store_dashboard_ops_test.go: HUI-1677 经营看板只读汇总层的固定事实集验收。
//
// 纪律:
//   - 本文件只测 store 层 SQL 聚合本身:固定事实集用 raw 夹具播种(仓内
//     insertRawBenefit 先例),完整真实表面全链在 httpapi e2e 测试里做;
//   - 窗口语义与 PR#7 一致:秒粒度 datetime 比较、边界含;日粒度 public_view_stats;
//   - 租户隔离 fail-closed:空租户/跨租户 0 行;店经理收窄本店;
//   - 漏斗计数=distinct 参与;晚到事实按事件时间落窗;筛选只落有该维度的查询。

import (
	"testing"
	"time"
)

type opsWorld struct {
	s              *Store
	tenA, tenB     string
	store1, store2 string
	campA1, campA2 string // campA1 bound to store1; campA2 unbound
	linkA1         string
	invA1          string
}

func seedOpsWorld(t *testing.T) *opsWorld {
	t.Helper()
	s := openStore(t)
	w := &opsWorld{}
	tenA, err := s.CreateTenant("看板商家A")
	must(t, err)
	tenB, err := s.CreateTenant("看板商家B")
	must(t, err)
	s1, err := s.CreateStore(tenA.ID, "旗舰店", "地址1", "seed")
	must(t, err)
	s2, err := s.CreateStore(tenA.ID, "分店二", "地址2", "seed")
	must(t, err)
	cA1, err := s.CreateCampaign(NewCampaign{TenantID: tenA.ID, Title: "活动一", StoreID: s1.ID, CreatedBy: "seed"})
	must(t, err)
	cA2, err := s.CreateCampaign(NewCampaign{TenantID: tenA.ID, Title: "活动二", CreatedBy: "seed"})
	must(t, err)
	cB1, err := s.CreateCampaign(NewCampaign{TenantID: tenB.ID, Title: "B家活动", CreatedBy: "seed"})
	must(t, err)
	_ = cB1
	linkA1, err := s.CreateLink(tenA.ID, cA1.ID, "seed")
	must(t, err)
	invA1, err := s.CreateRewardInventory(tenA.ID, NewRewardInventory{
		CampaignID: cA1.ID, Kind: RewardKindStoreCoupon, Name: "开业券",
		BenefitSlot: "first_publish", RuleVersion: 3,
		AllowedEvidence: []string{"staff_observed_submission", "staff_verified_post", "official_publish_confirmed"},
		TotalQty:        100, FaceValueMinor: 500, CostMinor: 300, Currency: "FEN", CreatedBy: "seed",
	})
	must(t, err)
	w.tenA, w.tenB, w.store1, w.store2 = tenA.ID, tenB.ID, s1.ID, s2.ID
	w.campA1, w.campA2, w.linkA1, w.invA1 = cA1.ID, cA2.ID, linkA1.Code, invA1.ID
	w.s = s
	return w
}

// raw fixture helpers (schema-contract precedent: insertRawBenefit).

func (w *opsWorld) rawView(t *testing.T, code, day, channel string, views int64) {
	t.Helper()
	must(t, w.exec(`INSERT INTO public_view_stats(code,day,channel,views) VALUES(?,?,?,?)`, code, day, channel, views))
}

func (w *opsWorld) rawParticipation(t *testing.T, id string, campID, storeID, source, cpVersion, status, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO participations(id,tenant_id,store_id,campaign_id,link_code,touchpoint_source,rule_version,
		content_pack_version,visitor_id,status,idempotency_key,benefit_slot,benefit_reservation_state,starts_at,expires_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, storeID, campID, "LNK-"+id, source, 3, cpVersion, "vis-"+id, status,
		"idem-"+id, id+"-slot", "not_requested", createdAt, createdAt, createdAt, createdAt))
}

func (w *opsWorld) rawAttempt(t *testing.T, id, participationID, platform, method, cpVersion, status, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO participation_attempts(id,tenant_id,participation_id,platform,handoff_method,
		content_pack_version,rule_version,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, participationID, platform, method, cpVersion, 3, status, createdAt, createdAt))
}

func (w *opsWorld) rawCredential(t *testing.T, id, participationID, attemptID, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO handoff_credentials(id,tenant_id,participation_id,attempt_id,purpose,target_scope,
		token_hash,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, participationID, attemptID, "customer_resume", "h5", "hash-"+id,
		"2026-10-14T23:59:59Z", createdAt))
}

func (w *opsWorld) rawBenefit(t *testing.T, id, participationID, evidence, status, storeID string, face, cost int64, issuedAt, redeemedAt, reversedAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO reward_benefits(id,tenant_id,campaign_id,inventory_id,participation_id,benefit_slot,
		rule_version,evidence,code,status,store_id,face_value_minor,cost_minor,currency,reserved_at,issued_at,redeemed_at,reversed_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, w.campA1, w.invA1, participationID, id+"-slot", 3, evidence, "CODE-"+id, status, storeID,
		face, cost, "FEN", issuedAt, issuedAt, redeemedAt, reversedAt, issuedAt, issuedAt))
}

func (w *opsWorld) rawOfficialFact(t *testing.T, id, attemptID, platform, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO reward_official_facts(id,tenant_id,campaign_id,attempt_id,platform,platform_post_id,
		receipt_source,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		id, w.tenA, w.campA1, attemptID, platform, "post-"+id, "official_query", createdAt))
}

func (w *opsWorld) rawOp(t *testing.T, id, benefitID, op, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO reward_operations(id,tenant_id,benefit_id,op,created_at) VALUES(?,?,?,?,?)`,
		id, w.tenA, benefitID, op, createdAt))
}

func (w *opsWorld) rawCopyJob(t *testing.T, id, campaignID, payload, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO copy_jobs(id,tenant_id,campaign_id,idempotency_key,snapshot_hash,state,charge_count,payload,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, campaignID, "idem-"+id, "hash", "saved", 1, payload, createdAt, createdAt))
}

func (w *opsWorld) rawSelfReport(t *testing.T, id string, campID, status string, selfReported int) {
	t.Helper()
	must(t, w.exec(`INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,
		copy_text,content_version,account_label,status,self_reported,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, campID, "LNK-x", "douyin", "activity_customer", "文案", "v1", "账号",
		status, selfReported, "2026-10-14T09:00:00Z", "2026-10-14T09:00:00Z"))
}

var opsWinStart = "2026-10-14T00:00:00Z"
var opsWinEnd = "2026-10-14T23:59:59Z"

func opsWinBounds(t *testing.T) (time.Time, time.Time) {
	t.Helper()
	start, err := time.Parse(time.RFC3339, opsWinStart)
	must(t, err)
	end, err := time.Parse(time.RFC3339, opsWinEnd)
	must(t, err)
	return start, end
}

// 固定事实集:
//   - part-1: store1/campA1, nfc, 版本 v3, attempt douyin/h5 冻结 v3, 一枚凭据;
//     权益 issued, 证据 staff_observed_submission, face 500 cost 300;
//   - part-2: store1/campA1, qr, 无版本;权益 redeemed(人工先发), 官方事实晚到
//     (次日,窗外)→证据在,官方计数在窗外;
//   - part-3: store2/campA1, manual, attempt websim/platform_miniprogram 冻结 v3,
//     两枚凭据(重铸)→交接 distinct=2 个参与;权益 reserved(未发放);
//   - part-4: campA2, qr, 版本 v9;权益 issued 证据 staff_verified_post,
//     在窗外发放(昨日)→发放计数不含;
//   - part-5: store1/campA1, nfc, 版本 v3;权益 reversed(更正)+reverse 流水;
//   - part-6: campA1, shorturl;benefit_reservation_state='failed'(异常);
//   - 访问: linkA1 上 12 views(nfc,窗口日) + 5 views(qr,窗口日) + 昨日 99(窗外)。
//   - copy job: billed=true amount 8000(窗口内) + billed=false(不计)。
func (w *opsWorld) seedFixedFactSet(t *testing.T) {
	t.Helper()
	const inDay = "2026-10-14"
	w.rawView(t, w.linkA1, inDay, "nfc", 12)
	w.rawView(t, w.linkA1, inDay, "qr", 5)
	w.rawView(t, w.linkA1, "2026-10-13", "nfc", 99)

	w.rawParticipation(t, "part-1", w.campA1, w.store1, "nfc", "v3", "in_progress", "2026-10-14T08:00:00Z")
	w.rawAttempt(t, "att-1", "part-1", "douyin", "h5", "v3", "handed_off", "2026-10-14T08:10:00Z")
	w.rawCredential(t, "cred-1", "part-1", "att-1", "2026-10-14T08:11:00Z")
	w.rawBenefit(t, "ben-1", "part-1", "staff_observed_submission", "issued", w.store1, 500, 300, "2026-10-14T09:00:00Z", "", "")

	w.rawParticipation(t, "part-2", w.campA1, w.store1, "qr", "", "awaiting_review", "2026-10-14T08:30:00Z")
	w.rawBenefit(t, "ben-2", "part-2", "staff_observed_submission", "redeemed", w.store1, 500, 300, "2026-10-14T10:00:00Z", "2026-10-14T11:00:00Z", "")
	// 官方事实锚定真实发布尝试行(attempt_id 外键):人工先发,官方晚到(次日,窗外)。
	w.rawSelfReport(t, "att-official", w.campA1, "exported", 0)
	w.rawOfficialFact(t, "fact-1", "att-official", "douyin", "2026-10-15T09:00:00Z")

	w.rawParticipation(t, "part-3", w.campA1, w.store2, "manual", "v3", "in_progress", "2026-10-14T08:40:00Z")
	w.rawAttempt(t, "att-3", "part-3", "websim", "platform_miniprogram", "v3", "handed_off", "2026-10-14T08:45:00Z")
	w.rawCredential(t, "cred-3a", "part-3", "att-3", "2026-10-14T08:46:00Z")
	w.rawCredential(t, "cred-3b", "part-3", "att-3", "2026-10-14T08:50:00Z") // 弱网重铸
	w.rawBenefit(t, "ben-3", "part-3", "", "reserved", w.store2, 500, 300, "2026-10-14T08:47:00Z", "", "")

	w.rawParticipation(t, "part-4", w.campA2, w.store1, "qr", "v9", "fulfilled", "2026-10-13T09:00:00Z")
	w.rawBenefit(t, "ben-4", "part-4", "staff_verified_post", "issued", "", 500, 300, "2026-10-13T10:00:00Z", "", "")

	w.rawParticipation(t, "part-5", w.campA1, w.store1, "nfc", "v3", "in_progress", "2026-10-14T09:30:00Z")
	w.rawBenefit(t, "ben-5", "part-5", "official_publish_confirmed", "reversed", w.store1, 500, 300, "2026-10-14T09:40:00Z", "", "2026-10-14T12:00:00Z")
	w.rawOp(t, "op-rev-5", "ben-5", "reverse", "2026-10-14T12:00:00Z")

	w.rawParticipation(t, "part-6", w.campA1, w.store1, "shorturl", "", "pending", "2026-10-14T09:50:00Z")
	must(t, w.exec(`UPDATE participations SET benefit_reservation_state='failed' WHERE id='part-6'`))

	w.rawCopyJob(t, "job-1", w.campA1, `{"trace":{"billed":true,"amount_minor":8000}}`, "2026-10-14T07:00:00Z")
	w.rawCopyJob(t, "job-2", w.campA1, `{"trace":{"billed":false,"amount_minor":5000}}`, "2026-10-14T07:30:00Z")

	w.rawSelfReport(t, "sr-1", w.campA1, "exported", 1) // 待确认队列
}

func (w *opsWorld) exec(q string, args ...any) error {
	_, err := w.s.DB.Exec(q, args...)
	return err
}

// ---- assertions -------------------------------------------------------------------

func TestDashboardOpsFixedFactSet(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)

	// 漏斗七级逐项(独立复算基准写在断言里,不读实现路径)。
	if facts.VisitTotal != 17 { // 12 nfc + 5 qr;昨日 99 不落窗
		t.Fatalf("VisitTotal = %d, want 17", facts.VisitTotal)
	}
	if facts.StartedTotal != 5 { // part-1..part-6 中 5 个在本窗口(part-4 昨日)
		t.Fatalf("StartedTotal = %d, want 5", facts.StartedTotal)
	}
	if facts.ContentSelectedTotal != 2 { // part-1/part-3 有冻结版本;part-5 无 attempt 不冒充
		t.Fatalf("ContentSelectedTotal = %d, want 2", facts.ContentSelectedTotal)
	}
	if facts.HandoffInitiatedTotal != 2 { // part-1(1 枚) + part-3(2 枚去重)
		t.Fatalf("HandoffInitiatedTotal = %d, want 2", facts.HandoffInitiatedTotal)
	}
	// 证据:ben-1 人工见到提交 + ben-2 人工见到提交 + ben-4 现场核对(窗外发放不计)
	if got := facts.EvidenceByClass["staff_observed_submission"]; got != 2 {
		t.Fatalf("evidence staff_observed = %d, want 2", got)
	}
	if got := facts.EvidenceByClass["staff_verified_post"]; got != 0 {
		t.Fatalf("evidence staff_verified (issued outside window) = %d, want 0", got)
	}
	if facts.EvidenceDistinctParticipations != 2 {
		t.Fatalf("evidence distinct participations = %d, want 2", facts.EvidenceDistinctParticipations)
	}
	if facts.OfficialConfirmedAttempts != 0 { // 官方晚到(次日),窗外
		t.Fatalf("official attempts = %d, want 0 (late fact outside window)", facts.OfficialConfirmedAttempts)
	}
	if facts.IssuedTotal != 2 { // ben-1 issued + ben-2 redeemed(也占发放)
		t.Fatalf("IssuedTotal = %d, want 2", facts.IssuedTotal)
	}
	if facts.IssuedFaceMinor != 1000 || facts.IssuedCostMinor != 600 {
		t.Fatalf("issued face/cost = %d/%d, want 1000/600", facts.IssuedFaceMinor, facts.IssuedCostMinor)
	}
	if facts.RedeemedTotal != 1 || facts.RedeemedCostMinor != 300 {
		t.Fatalf("redeemed = %d/%d, want 1/300", facts.RedeemedTotal, facts.RedeemedCostMinor)
	}
	// 库存是账本当前态(不落窗):reserved 1(ben-3)+ issued 2(ben-1、ben-4)+
	// redeemed 1(ben-2),reversed 1(ben-5),expired 0。占用=4,available=96
	// (available 在 dashboard 包组装:total−占用)。
	if len(facts.Inventory) != 1 {
		t.Fatalf("inventory lines = %d, want 1", len(facts.Inventory))
	}
	inv := facts.Inventory[0]
	if inv.Reserved != 1 || inv.Issued != 2 || inv.Redeemed != 1 || inv.Reversed != 1 || inv.Expired != 0 {
		t.Fatalf("inventory counts wrong: %+v", inv)
	}
	if inv.TotalQty != 100 || inv.FaceValueMinor != 500 || inv.CostMinor != 300 {
		t.Fatalf("inventory batch snapshot wrong: %+v", inv)
	}
	// 异常:待确认 1;预留失败 1;窗口内撤销 1;过期 0。
	if facts.Anomalies.PendingReview != 1 || facts.Anomalies.ReservationFailed != 1 ||
		facts.Anomalies.ReversedInWindow != 1 || facts.Anomalies.ExpiredInWindow != 0 {
		t.Fatalf("anomalies wrong: %+v", facts.Anomalies)
	}
	// 成本:内容生成=8000(billed;未结算不计);已发奖励成本=600;面额=1000;
	// 未录入成本批次=0(face>0 且 cost=0 的批次为零)。
	if facts.Cost.ContentGenerationAmountMinor == nil || *facts.Cost.ContentGenerationAmountMinor != 8000 {
		t.Fatalf("content generation = %v, want 8000", facts.Cost.ContentGenerationAmountMinor)
	}
	if facts.Cost.ContentGenerationBilledRuns != 1 {
		t.Fatalf("billed runs = %d, want 1", facts.Cost.ContentGenerationBilledRuns)
	}
	if facts.Cost.RewardCostMinor != 600 || facts.Cost.RewardRedeemedCostMinor != 300 || facts.Cost.RewardFaceMinor != 1000 {
		t.Fatalf("reward cost wrong: %+v", facts.Cost)
	}
	if facts.Cost.CostUnrecordedBatches != 0 {
		t.Fatalf("unrecorded batches = %d, want 0", facts.Cost.CostUnrecordedBatches)
	}
}

func TestDashboardOpsContentSelectedDistinct(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	// 有冻结版本的参与:part-1(att-1)、part-3(att-3)。part-5 无 attempt 不冒充。
	if facts.ContentSelectedTotal != 2 {
		t.Fatalf("ContentSelectedTotal = %d, want 2", facts.ContentSelectedTotal)
	}
}

func TestDashboardOpsFilters(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	start, end := opsWinBounds(t)

	// 活动筛选:campA2 只有窗外参与 → 全零。
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{CampaignID: w.campA2})
	must(t, err)
	if facts.StartedTotal != 0 || facts.VisitTotal != 0 {
		t.Fatalf("campA2 must be empty in window: %+v", facts)
	}

	// 触点筛选 nfc:访问=12;参与=part-1/part-5=2。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{Touchpoint: "nfc"})
	must(t, err)
	if facts.VisitTotal != 12 || facts.StartedTotal != 2 {
		t.Fatalf("nfc filter: visits=%d started=%d, want 12/2", facts.VisitTotal, facts.StartedTotal)
	}

	// 内容版本 v3:参与 part-1/part-3/part-5=3;不适用于访问级(无关联)→访问不筛=17。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{ContentPackVersion: "v3"})
	must(t, err)
	if facts.StartedTotal != 3 {
		t.Fatalf("cpversion filter started = %d, want 3", facts.StartedTotal)
	}
	if facts.VisitTotal != 17 {
		t.Fatalf("cpversion must not pretend to filter visits: %d", facts.VisitTotal)
	}

	// 平台筛选:只落内容选择级与官方事实;参与级不假装。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{Platform: "douyin"})
	must(t, err)
	if facts.ContentSelectedTotal != 1 { // att-1 douyin
		t.Fatalf("platform filter content_selected = %d, want 1", facts.ContentSelectedTotal)
	}
	if facts.StartedTotal != 5 {
		t.Fatalf("platform must not filter started: %d, want 5", facts.StartedTotal)
	}

	// 承接方式 platform_miniprogram:内容选择=part-3。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{HandoffMethod: "platform_miniprogram"})
	must(t, err)
	if facts.ContentSelectedTotal != 1 {
		t.Fatalf("method filter content_selected = %d, want 1", facts.ContentSelectedTotal)
	}

	// 门店筛选 store1:参与 part-1/part-2/part-5/part-6=4;访问=17(campA1 绑 store1)。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{StoreID: w.store1})
	must(t, err)
	if facts.StartedTotal != 4 || facts.VisitTotal != 17 {
		t.Fatalf("store filter started/visits = %d/%d, want 4/17", facts.StartedTotal, facts.VisitTotal)
	}
	if facts.IssuedTotal != 2 { // ben-3 在 store2 reserved,ben-4 窗外
		t.Fatalf("store filter issued = %d, want 2", facts.IssuedTotal)
	}
}

func TestDashboardOpsStoreScopeManager(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	start, end := opsWinBounds(t)
	// 店经理只看本店:store2 只有 part-3(reserved 权益)。
	facts, _, err := w.s.DashboardOps(w.tenA, w.store2, start, end, OpsFilter{})
	must(t, err)
	if facts.StartedTotal != 1 || facts.IssuedTotal != 0 {
		t.Fatalf("manager scope store2: started=%d issued=%d, want 1/0", facts.StartedTotal, facts.IssuedTotal)
	}
	if len(facts.Inventory) != 1 { // 租户级批次对全店可见(store_id='')
		t.Fatalf("tenant-wide batch must stay visible to manager")
	}
	// 空租户 fail-closed。
	empty, _, err := w.s.DashboardOps("", "", start, end, OpsFilter{})
	must(t, err)
	if empty.StartedTotal != 0 || len(empty.Inventory) != 0 {
		t.Fatalf("empty tenant must fail closed: %+v", empty)
	}
}

func TestDashboardOpsCrossTenantZero(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenB, "", start, end, OpsFilter{})
	must(t, err)
	if facts.VisitTotal != 0 || facts.StartedTotal != 0 || facts.IssuedTotal != 0 || len(facts.Inventory) != 0 {
		t.Fatalf("cross-tenant must be 0 rows: %+v", facts)
	}
}

func TestDashboardOpsLateOfficialFactCountsInItsOwnWindow(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	// 次日窗口:人工先发的发放留在原窗口(不重算),官方事实落到自己的窗口。
	start, _ := opsWinBounds(t)
	start2 := start.AddDate(0, 0, 1)
	end2 := start2.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start2, end2, OpsFilter{})
	must(t, err)
	if facts.IssuedTotal != 0 {
		t.Fatalf("issuance must stay in its own window: %d", facts.IssuedTotal)
	}
	if facts.OfficialConfirmedAttempts != 1 {
		t.Fatalf("late official fact must land in its own window: %d", facts.OfficialConfirmedAttempts)
	}
}

func TestDashboardOpsUnrecordedCostBatches(t *testing.T) {
	w := seedOpsWorld(t)
	// 一个 face>0 cost=0 的批次(未录入成本)。
	_, err := w.s.CreateRewardInventory(w.tenA, NewRewardInventory{
		CampaignID: w.campA1, Kind: RewardKindStoreCoupon, Name: "未录成本券",
		BenefitSlot: "second_slot", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"},
		TotalQty:        10, FaceValueMinor: 200, CostMinor: 0, CreatedBy: "seed",
	})
	must(t, err)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	if facts.Cost.CostUnrecordedBatches != 1 {
		t.Fatalf("unrecorded batches = %d, want 1", facts.Cost.CostUnrecordedBatches)
	}
}

func TestDashboardOpsSummaryFacts(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	dayStart, _ := time.Parse(time.RFC3339, "2026-10-14T00:00:00Z")
	asOf, _ := time.Parse(time.RFC3339, "2026-10-14T12:00:00Z")
	sum, err := w.s.DashboardOpsSummary(w.tenA, "", dayStart, asOf)
	must(t, err)
	if sum.PendingConfirm != 1 {
		t.Fatalf("pending_confirm = %d, want 1", sum.PendingConfirm)
	}
	if sum.RewardsIssued != 2 {
		t.Fatalf("issued today = %d, want 2 (ben-1 issued + ben-2 confirmed→issued)", sum.RewardsIssued)
	}
	if sum.RewardsRedeemed != 1 {
		t.Fatalf("redeemed today = %d, want 1", sum.RewardsRedeemed)
	}
	if sum.Anomalies != 2 { // reservation_failed 1 + reverse 流水 1
		t.Fatalf("anomalies = %d, want 2", sum.Anomalies)
	}
	// 店经理作用域:store2 视角全零(它只有 reserved)。
	sum2, err := w.s.DashboardOpsSummary(w.tenA, w.store2, dayStart, asOf)
	must(t, err)
	if sum2.RewardsIssued != 0 || sum2.RewardsRedeemed != 0 || sum2.PendingConfirm != 0 || sum2.Anomalies != 0 {
		t.Fatalf("manager scope summary must be scoped: %+v", sum2)
	}
}
