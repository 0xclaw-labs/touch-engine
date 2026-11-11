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

// ---- TOUCH-ALL-20261109-v1:六层证据/线索口径/费用对账/待办摘要 ---------------------

// seedLayerFactSet 在固定事实集之上补六层/线索/费用事实:
//   - 会话: ses-a(窗口内 created + resumed)+ ses-b(窗外 created)→distinct 1;
//   - 服务事件: sa_1 open(窗口)+ sv_9 open(窗口,另一 id)→open=2;
//     jump_attempt×2(1 个 ios_wechat、1 个窗外)→attempt=1(ios_wechat 筛选亦 1);
//   - 交接: ph-1 platform_confirmed(带 editor_open_proved/staff_saw_submit/
//     staff_verified_work/official_confirmed 各一)、ph-2 handoff_ready(带重复
//     editor_open_proved)→requested=2 completed=2 editor=2 staff=1/1 platform=1;
//     ph-3 previewed(窗外创建)不计;
//   - 自报: sr-2(窗口内)+ sr-3(窗外)→user_reported=1+固定集 sr-1=1 → 2;
//   - 线索: lead-c1 campaign pending_sync、lead-c2 campaign crm_received stored、
//     lead-c3 store pending_sync、lead-c4 campaign revoked、lead-c5 campaign
//     receiver 声明 revoked(receiver_state='revoked',本地仍 pending_sync)、
//     lead-c6 窗外 crm_received;窗口内提交 5 行,unique contacts 4(重号一个);
//   - 费用: sub-1 active 29900(窗口)、sub-2 failed、hw-1 active 120000(窗口)+
//     refund 20000、sub-9 active 窗外、ai 4 状态各一行(其中 refunded/failed 不计支出);
//   - 动作: act-on(启用无内容)、act-off(停用)、act-exp(启用但 expires_at 已过)、
//     act-ok(启用且绑定内容);
//   - 准入: admitted 且 valid_until 已过一行 + admitted 未到期一行。
func (w *opsWorld) seedLayerFactSet(t *testing.T) {
	t.Helper()
	// 会话事件(3546): action_session_events
	w.rawSessionEvent(t, "se-1", w.store1, "ses-a", "act-1", "created", "ev-1", "2026-10-14T08:00:00Z")
	w.rawSessionEvent(t, "se-2", w.store1, "ses-a", "act-1", "resumed", "ev-2", "2026-10-14T08:05:00Z")
	w.rawSessionEvent(t, "se-3", w.store1, "ses-b", "act-2", "created", "ev-3", "2026-10-13T08:00:00Z") // 窗外
	// 服务事件(4589): store_service_events
	w.rawServiceEvent(t, "svc-1", w.store1, "sa_1", "open", "ios_wechat", "2026-10-14T09:00:00Z")
	w.rawServiceEvent(t, "svc-2", w.store1, "sv_9", "open", "ios_wechat", "2026-10-14T09:01:00Z")
	w.rawServiceEvent(t, "svc-3", w.store1, "sa_1", "jump_attempt", "android_wechat", "2026-10-14T09:02:00Z")
	w.rawServiceEvent(t, "svc-4", w.store1, "sa_1", "jump_attempt", "ios_wechat", "2026-10-13T09:02:00Z") // 窗外
	// 参与行(交接外键要求)+ 交接行(1670)
	w.rawParticipation(t, "part-ph1", w.campA1, w.store1, "nfc", "v3", "in_progress", "2026-10-14T08:00:00Z")
	w.rawAttempt(t, "att-ph1", "part-ph1", "douyin", "h5", "v3", "handed_off", "2026-10-14T08:05:00Z")
	w.rawParticipation(t, "part-ph2", w.campA1, w.store1, "qr", "", "awaiting_review", "2026-10-14T08:20:00Z")
	w.rawAttempt(t, "att-ph2", "part-ph2", "douyin", "h5", "", "handed_off", "2026-10-14T08:25:00Z")
	w.rawHandoff(t, "ph-1", w.store1, "part-ph1", "att-ph1", "platform_confirmed", "2026-10-14T09:10:00Z")
	w.rawHandoff(t, "ph-2", w.store1, "part-ph2", "att-ph2", "handoff_ready", "2026-10-14T09:20:00Z")
	w.rawHandoff(t, "ph-3", w.store1, "part-ph2", "att-ph2", "previewed", "2026-10-13T09:20:00Z") // 窗外
	w.rawHandoffEvent(t, "phe-1", "ph-1", "editor_open_proved", "2026-10-14T09:12:00Z")
	w.rawHandoffEvent(t, "phe-2", "ph-2", "editor_open_proved", "2026-10-14T09:22:00Z")
	w.rawHandoffEvent(t, "phe-3", "ph-2", "editor_open_proved", "2026-10-14T09:23:00Z") // 重放
	w.rawHandoffEvent(t, "phe-4", "ph-1", "staff_saw_submit", "2026-10-14T09:30:00Z")
	w.rawHandoffEvent(t, "phe-5", "ph-1", "staff_verified_work", "2026-10-14T09:31:00Z")
	w.rawHandoffEvent(t, "phe-6", "ph-1", "official_confirmed", "2026-10-14T09:32:00Z")
	// 1670 v2 六层事实 kind(冻结词汇):
	w.rawHandoffEvent(t, "phe-7", "ph-1", "customer_click", "2026-10-14T09:05:00Z")
	w.rawHandoffEvent(t, "phe-8", "ph-1", "customer_click", "2026-10-14T09:06:00Z") // 第二次点击逐行计
	w.rawHandoffEvent(t, "phe-9", "ph-1", "wake_attempt", "2026-10-14T09:08:00Z")
	w.rawHandoffEvent(t, "phe-10", "ph-1", "media_handoff", "2026-10-14T09:09:00Z")
	w.rawHandoffEvent(t, "phe-11", "ph-1", "customer_self_report", "2026-10-14T09:28:00Z")
	w.rawHandoffEvent(t, "phe-12", "ph-1", "staff_confirm", "2026-10-14T09:29:00Z")
	w.rawHandoffEvent(t, "phe-13", "ph-1", "platform_confirm", "2026-10-14T09:33:00Z")
	// v2 动作交接行(action_id/environment 维度)+ 其点击事件(筛选断言用)。
	w.rawParticipation(t, "part-v2", w.campA1, w.store1, "nfc", "v3", "in_progress", "2026-10-14T08:00:00Z")
	w.rawAttempt(t, "att-v2", "part-v2", "douyin", "h5", "v3", "handed_off", "2026-10-14T08:05:00Z")
	w.rawHandoffV2(t, "ph-v2", w.store1, "part-v2", "att-v2", "handoff_ready", "sa_1", "ios_wechat", "2026-10-14T09:40:00Z")
	w.rawHandoffEvent(t, "phe-14", "ph-v2", "customer_click", "2026-10-14T09:41:00Z")
	// 自报(固定集已有 sr-1)
	w.rawSelfReport(t, "sr-2", w.campA1, "exported", 1)
	// 窗外自报:直接 UPDATE created_at(夹具函数固定时间,这里改窗)
	must(t, w.exec(`INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,
		copy_text,content_version,account_label,status,self_reported,created_at,updated_at)
		VALUES('sr-3',?,?,'LNK-x','douyin','activity_customer','文案','v1','账号','exported',1,'2026-10-13T09:00:00Z','2026-10-13T09:00:00Z')`,
		w.tenA, w.campA1))
	// 线索(4594 口径)
	w.rawLead(t, "lead-c1", w.campA1, w.store1, "campaign", "pending_sync", "", 0, "2026-10-14T10:00:00Z", "13800000001")
	w.rawLead(t, "lead-c2", w.campA1, w.store1, "campaign", "crm_received", "stored", 0, "2026-10-14T10:01:00Z", "13800000002")
	w.rawLead(t, "lead-c3", "", w.store1, "store", "pending_sync", "", 0, "2026-10-14T10:02:00Z", "13800000003")
	w.rawLead(t, "lead-c4", w.campA1, w.store1, "campaign", "revoked", "", 1, "2026-10-14T10:03:00Z", "13800000004")
	w.rawLead(t, "lead-c5", w.campA1, w.store1, "campaign", "pending_sync", "revoked", 0, "2026-10-14T10:04:00Z", "13800000001") // 重号
	w.rawLead(t, "lead-c6", w.campA1, w.store1, "campaign", "crm_received", "stored", 0, "2026-10-13T10:05:00Z", "13800000009")  // 窗外
	// 费用(4591 投影)
	w.rawBillingOrder(t, "sub-1", "subscription", "new", 29900, "active", "2026-10-14T06:00:00Z")
	w.rawBillingOrder(t, "sub-2", "subscription", "renew", 29900, "failed", "2026-10-14T06:05:00Z")
	w.rawBillingOrder(t, "hw-1", "hardware", "purchase", 120000, "active", "2026-10-14T06:10:00Z")
	w.rawBillingOrder(t, "sub-9", "subscription", "new", 99900, "active", "2026-10-13T06:00:00Z") // 窗外
	w.rawRefund(t, "ref-1", "hw-1", 20000, "2026-10-14T07:00:00Z")
	w.rawAIUsage(t, "ai-1", "q-1", "consumed", 100, "2026-10-14T07:10:00Z")
	w.rawAIUsage(t, "ai-2", "q-2", "confirmed", 120, "2026-10-14T07:11:00Z")
	w.rawAIUsage(t, "ai-3", "q-3", "refunded", 60, "2026-10-14T07:12:00Z")
	w.rawAIUsage(t, "ai-4", "q-4", "failed", 30, "2026-10-14T07:13:00Z")
	// 动作(4584)+ 准入(3535)
	w.rawStoreAction(t, "act-on", w.store1, "douyin", "review", true, "", true)
	w.rawStoreAction(t, "act-off", w.store1, "douyin", "review", false, "", true)
	w.rawStoreAction(t, "act-exp", w.store1, "meituan", "review", true, "2026-10-14T00:00:01Z", true)
	w.rawStoreAction(t, "act-ok", w.store1, "xiaohongshu", "follow", true, "", false)
	w.rawAdmission(t, "adm-1", "douyin", "h5", "publish_media", "*", "admitted", "2026-10-13T00:00:00Z") // 已过期
	w.rawAdmission(t, "adm-2", "kuaishou", "h5", "publish_media", "*", "admitted", "2027-01-01T00:00:00Z")
}

func (w *opsWorld) rawSessionEvent(t *testing.T, id, storeID, sessionID, actionID, kind, eventID, at string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO action_session_events(id,tenant_id,store_id,session_id,action_id,action_revision,kind,
		source,event_id,participation_id,credential_id,content_pack_version,occurred_at)
		VALUES(?,?,?,?,?,1,?,'touch-engine',?,'','','',?)`,
		id, w.tenA, storeID, sessionID, actionID, kind, eventID, at))
}

func (w *opsWorld) rawServiceEvent(t *testing.T, id, storeID, serviceID, kind, env, at string) {
	t.Helper()
	tier := "click"
	if kind == "jump_attempt" {
		tier = "jump_attempt"
	}
	must(t, w.exec(`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
		VALUES(?,?,?,?,?,?,?,?)`, id, w.tenA, storeID, serviceID, kind, env, tier, at))
}

func (w *opsWorld) rawHandoff(t *testing.T, id, storeID, participationID, attemptID, status, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO publish_handoffs(id,tenant_id,store_id,campaign_id,link_code,participation_id,attempt_id,
		platform,entry_mode,scenario,idempotency_key,status,evidence_source,media_json,text_json,visitor_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, storeID, w.campA1, "LNK-"+id, participationID, attemptID, "douyin", "h5", "storefront_publish",
		"idem-"+id, status, "customer_action", "[]", "{}", "vis-"+id, createdAt, createdAt))
}

// rawHandoffV2 播种 1670 v2 动作交接行(带 action_id/environment 维度)。
func (w *opsWorld) rawHandoffV2(t *testing.T, id, storeID, participationID, attemptID, status, actionID, env, createdAt string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO publish_handoffs(id,tenant_id,store_id,campaign_id,link_code,participation_id,attempt_id,
		platform,entry_mode,scenario,idempotency_key,status,evidence_source,media_json,text_json,visitor_id,
		action_id,action_session_id,action_revision,method,environment,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, storeID, w.campA1, "LNK-"+id, participationID, attemptID, "douyin", "h5", "storefront_publish",
		"idem-"+id, status, "customer_action", "[]", "{}", "vis-"+id,
		actionID, "sess-"+id, 1, "h5", env, createdAt, createdAt))
}

func (w *opsWorld) rawHandoffEvent(t *testing.T, id, handoffID, kind, at string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO publish_handoff_events(id,tenant_id,handoff_id,kind,actor,evidence_source,receipt_id,payload,created_at)
		VALUES(?,?,?,?,?,?,?, '{}',?)`,
		id, w.tenA, handoffID, kind, "test", "staff_observation", "", at))
}

func (w *opsWorld) rawLead(t *testing.T, id, campID, storeID, capture, syncState, receiverState string, revoked int, at, phone string) {
	t.Helper()
	revokedAt := ""
	if revoked == 1 {
		revokedAt = at
	}
	must(t, w.exec(`INSERT INTO lead_submissions(id,tenant_id,campaign_id,store_id,link_id,form_id,session_id,capture_source,
		submission_ref,dedup_key,name,phone,wechat,marketing_optin,consent_notice_version,consent_at,consent_ip_fp,
		sync_state,sync_error,attempts,source_version,revoked_at,created_at,updated_at,receiver_state,receipt_id,revoke_source_revision)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		id, w.tenA, campID, storeID, "", "form-"+id, "", capture,
		"ref-"+id, "dk-"+id, "顾客", phone, "", 1, "v1", at, "fp",
		syncState, "", 0, 1, revokedAt, at, at, receiverState, ""))
}

func (w *opsWorld) rawBillingOrder(t *testing.T, id, kind, action string, amount int64, status, at string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO billing_orders(id,tenant_id,kind,action,plan_code,price_ref,price_snapshot,amount_minor,
		currency,status,order_ref,payment_ref,idempotency_key,payer_grant_ref,agency_relation_ref,payer_principal,
		actor_principal,actor_role,fail_reason,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, kind, action, "plan-basic", "price-1", "{}", amount, "FEN", status,
		"ord-"+id, "pay-"+id, "idem-"+id, "", "", "", "", "org_owner", "", at, at))
}

func (w *opsWorld) rawRefund(t *testing.T, id, orderID string, amount int64, at string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO billing_refunds(id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,
		invoice_title,invoice_status,invoice_ref,auditor,source,event_id,payload,occurred_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, orderID, "rr-"+id, amount, "FEN", "hardware return", "", "none", "", "auditor",
		"authority", "evt-"+id, "{}", at, at))
}

func (w *opsWorld) rawAIUsage(t *testing.T, id, quoteRef, state string, amount int64, at string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO billing_ai_usage(id,tenant_id,quote_ref,capability,quantity,amount_minor,currency,
		pricing_version,state,business_ref,consumed_by_ref,occurred_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, quoteRef, "video_gen", 1, amount, "FEN", "pv1", state, "", "", at, at))
}

func (w *opsWorld) rawStoreAction(t *testing.T, id, storeID, platform, kind string, enabled bool, expiresAt string, withContent bool) {
	t.Helper()
	binding := ""
	if withContent {
		binding = "pack:1"
	}
	revoked := 0
	if !enabled {
		revoked = 1
	}
	must(t, w.exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		content_binding_ref,capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.tenA, storeID, "A05", platform, kind, "", "动作"+id, "https://example.com",
		binding, "", "link", 0, boolInt(enabled), 1, revoked, expiresAt, "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"))
}

func (w *opsWorld) rawAdmission(t *testing.T, id, platform, entryMode, scenario, tenantScope, status, validUntil string) {
	t.Helper()
	must(t, w.exec(`INSERT INTO channel_admissions(id,platform,entry_mode,scenario,app_binding,tenant_scope,status,
		reward_admitted,reason_code,reason_text,cap_media,cap_title,cap_body,cap_topic,cap_poi,cap_receipt,cap_callback,
		missing,official_sources,last_checked_at,valid_until,seed_version,payload_hash,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, platform, entryMode, scenario, "platform_app", tenantScope, status, 0, "ok", "",
		"supported", "supported", "supported", "supported", "unknown", "supported", "supported",
		"[]", "[]", validUntil, validUntil, 1, "hash", validUntil, validUntil))
}

// 六层事实:窗口内的固定事实集→逐层逐行核对;筛选/作用域/跨租户 fail-closed。
func TestDashboardOpsLayerFacts(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t)
	w.seedLayerFactSet(t)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)

	// L1 点击: distinct 会话 1(ses-b 窗外)+ 卡片打开 2(sa_1 + sv_9)
	if facts.LayerActionSessions != 1 {
		t.Fatalf("action sessions = %d, want 1 (distinct session, out-of-window excluded)", facts.LayerActionSessions)
	}
	if facts.LayerServiceOpens != 2 {
		t.Fatalf("service opens = %d, want 2", facts.LayerServiceOpens)
	}
	// L2 唤起尝试: jump_attempt 1(窗外不计)+ editor_open_proved distinct 2(重放不计)
	if facts.LayerJumpAttempts != 1 {
		t.Fatalf("jump attempts = %d, want 1", facts.LayerJumpAttempts)
	}
	if facts.LayerEditorProved != 2 {
		t.Fatalf("editor_open_proved distinct = %d, want 2", facts.LayerEditorProved)
	}
	// L3 媒体交接: requested=3(ph-1/ph-2/ph-v2;ph-3 窗外不计),
	// completed=3(ph-1 confirmed 蕴含 ready 已达 + ph-2 ready + ph-v2 ready)
	if facts.LayerHandoffRequested != 3 {
		t.Fatalf("handoff requested = %d, want 3", facts.LayerHandoffRequested)
	}
	if facts.LayerHandoffCompleted != 3 {
		t.Fatalf("handoff completed = %d, want 3", facts.LayerHandoffCompleted)
	}
	// L4 顾客自报: sr-1(固定集)+ sr-2(窗口内)= 2;sr-3 窗外不计
	if facts.LayerUserReported != 2 {
		t.Fatalf("user reported = %d, want 2", facts.LayerUserReported)
	}
	// L5 店员确认: staff_saw_submit 1 + staff_verified_work 1(distinct handoff)
	if facts.LayerStaffSaw != 1 || facts.LayerStaffVerified != 1 {
		t.Fatalf("staff facts = %d/%d, want 1/1", facts.LayerStaffSaw, facts.LayerStaffVerified)
	}
	// L6 平台证实: handoff platform_confirmed 1 + official_confirmed 事件 distinct 1
	if facts.LayerPlatformHandoffs != 1 || facts.LayerOfficialEvents != 1 {
		t.Fatalf("platform facts = %d/%d, want 1/1", facts.LayerPlatformHandoffs, facts.LayerOfficialEvents)
	}
	// 1670 v2 六层 kind:点击逐行 3(2+1)、wake 1、media/self/staff/platform distinct 1/1/1/1
	if facts.LayerCustomerClick != 3 {
		t.Fatalf("customer_click = %d, want 3 (逐事件行)", facts.LayerCustomerClick)
	}
	if facts.LayerWakeAttempt != 1 || facts.LayerMediaHandoff != 1 {
		t.Fatalf("wake/media = %d/%d, want 1/1", facts.LayerWakeAttempt, facts.LayerMediaHandoff)
	}
	if facts.LayerSelfReportEvent != 1 || facts.LayerStaffConfirm != 1 || facts.LayerPlatformConfirm != 1 {
		t.Fatalf("self/staff/platform confirm = %d/%d/%d, want 1/1/1",
			facts.LayerSelfReportEvent, facts.LayerStaffConfirm, facts.LayerPlatformConfirm)
	}
	// v2 行的 action/environment 维度收窄发布域(ph-v2 带 sa_1/ios_wechat)。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{ActionID: "sa_1", Environment: "ios_wechat"})
	must(t, err)
	if facts.LayerHandoffRequested != 1 || facts.LayerCustomerClick != 1 {
		t.Fatalf("action+environment filter must narrow publish rows: requested=%d click=%d, want 1/1",
			facts.LayerHandoffRequested, facts.LayerCustomerClick)
	}

	// 筛选:环境维度只落在服务事件行;iOS 微信下 open=2、attempt=0(窗口内 attempt 是 android)。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{Environment: "ios_wechat"})
	must(t, err)
	if facts.LayerServiceOpens != 2 || facts.LayerJumpAttempts != 0 {
		t.Fatalf("environment filter wrong: opens=%d attempts=%d, want 2/0", facts.LayerServiceOpens, facts.LayerJumpAttempts)
	}
	// 动作筛选:action_id 收窄服务事件与会话。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{ActionID: "sa_1"})
	must(t, err)
	if facts.LayerServiceOpens != 1 || facts.LayerJumpAttempts != 1 || facts.LayerActionSessions != 0 {
		t.Fatalf("action filter wrong: opens=%d attempts=%d sessions=%d, want 1/1/0(sa_1 上没有会话)",
			facts.LayerServiceOpens, facts.LayerJumpAttempts, facts.LayerActionSessions)
	}
	// 平台筛选落在发布域行。
	facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{Platform: "kuaishou"})
	must(t, err)
	if facts.LayerHandoffRequested != 0 || facts.LayerPlatformHandoffs != 0 {
		t.Fatalf("platform filter must narrow publish-domain lines: %+v", facts)
	}
	// 店经理作用域:store2 视角六层发布域/会话域全零(store1 的事实)。
	facts, _, err = w.s.DashboardOps(w.tenA, w.store2, start, end, OpsFilter{})
	must(t, err)
	if facts.LayerActionSessions != 0 || facts.LayerHandoffRequested != 0 || facts.LayerServiceOpens != 0 {
		t.Fatalf("manager scope must zero store1 facts: %+v", facts)
	}
	// 平台筛选落在发布域行(全部 douyin;kuaishou 为 0)——前置覆盖在下方保留。
	// 跨租户 fail-closed。
	facts, _, err = w.s.DashboardOps(w.tenB, "", start, end, OpsFilter{})
	must(t, err)
	if facts.LayerActionSessions != 0 || facts.LayerHandoffRequested != 0 || facts.LayerServiceOpens != 0 {
		t.Fatalf("cross-tenant must be zero: %+v", facts)
	}
}

// 线索口径:待同步不算已获客;两分来源;撤销与接收方申报分列;unique contacts。
func TestDashboardOpsLeadsFacts(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedLayerFactSet(t)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	lp := facts.Leads
	if lp.CampaignSubmitted != 4 {
		t.Fatalf("campaign submitted = %d, want 4 (c1/c2/c4/c5)", lp.CampaignSubmitted)
	}
	if lp.StoreSubmitted != 1 {
		t.Fatalf("store submitted = %d, want 1 (c3)", lp.StoreSubmitted)
	}
	if lp.PendingSync != 3 {
		t.Fatalf("pending_sync = %d, want 3 (c1/c3/c5) — 待同步不算已获客", lp.PendingSync)
	}
	if lp.ReceiverConfirmed != 1 {
		t.Fatalf("receiver confirmed = %d, want 1 (c2; c6 窗外)", lp.ReceiverConfirmed)
	}
	if lp.Revoked != 1 {
		t.Fatalf("revoked = %d, want 1 (c4)", lp.Revoked)
	}
	if lp.ReceiverDeclaredRevoked != 1 {
		t.Fatalf("receiver declared revoked = %d, want 1 (c5)", lp.ReceiverDeclaredRevoked)
	}
	if lp.UniqueContacts != 4 {
		t.Fatalf("unique contacts = %d, want 4 (c5 重号 c1)", lp.UniqueContacts)
	}
	// 门店筛选收窄(事实都在 store1,store2 视角全零)。
	facts, _, err = w.s.DashboardOps(w.tenA, w.store2, start, end, OpsFilter{})
	must(t, err)
	if facts.Leads.CampaignSubmitted != 0 || facts.Leads.ReceiverConfirmed != 0 {
		t.Fatalf("manager scope leads must be zero: %+v", facts.Leads)
	}
	// 跨租户零行。
	facts, _, err = w.s.DashboardOps(w.tenB, "", start, end, OpsFilter{})
	must(t, err)
	if facts.Leads.CampaignSubmitted != 0 || facts.Leads.PendingSync != 0 {
		t.Fatalf("cross-tenant leads must be zero: %+v", facts.Leads)
	}
}

// 费用对账:订阅/AI/硬件分明细;退款分列;窗外不计;failed 不是支出。
func TestDashboardOpsCostReconciliationFacts(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedLayerFactSet(t)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	cr := facts.CostRecon
	if cr.SubscriptionPaidMinor == nil || *cr.SubscriptionPaidMinor != 29900 {
		t.Fatalf("subscription paid = %v, want 29900 (sub-9 窗外不计)", cr.SubscriptionPaidMinor)
	}
	if cr.SubscriptionPaidOrders != 1 || cr.SubscriptionFailedOrders != 1 {
		t.Fatalf("subscription order counts = %d/%d, want 1/1", cr.SubscriptionPaidOrders, cr.SubscriptionFailedOrders)
	}
	if cr.AIConsumedMinor != 100 || cr.AIConfirmedMinor != 120 || cr.AIRefundedMinor != 60 || cr.AIFailedMinor != 30 {
		t.Fatalf("AI state split wrong: %+v", cr)
	}
	if cr.AIRefundedCount != 1 || cr.AIFailedCount != 1 || cr.AIConsumedCount != 1 || cr.AIConfirmedCount != 1 {
		t.Fatalf("AI state counts wrong: %+v", cr)
	}
	if cr.HardwarePaidMinor == nil || *cr.HardwarePaidMinor != 120000 {
		t.Fatalf("hardware paid = %v, want 120000", cr.HardwarePaidMinor)
	}
	if cr.HardwareRefundMinor != 20000 || cr.HardwareRefunds != 1 {
		t.Fatalf("hardware refunds = %d/%d, want 20000/1 分列不冲销", cr.HardwareRefundMinor, cr.HardwareRefunds)
	}
	// 跨租户零。
	facts, _, err = w.s.DashboardOps(w.tenB, "", start, end, OpsFilter{})
	must(t, err)
	if facts.CostRecon.SubscriptionPaidMinor != nil || facts.CostRecon.HardwarePaidMinor != nil || facts.CostRecon.AIConsumedMinor != 0 {
		t.Fatalf("cross-tenant cost recon must be empty/zero: %+v", facts.CostRecon)
	}
}

// 待办摘要:当前态队列 + 渠道准入到期;作用域收窄;跨租户零。
func TestDashboardOpsSummaryTodos(t *testing.T) {
	w := seedOpsWorld(t)
	w.seedFixedFactSet(t) // 权益账(ben-1 issued/ben-4 issued)供待核销当前态
	w.seedLayerFactSet(t)
	dayStart, _ := time.Parse(time.RFC3339, "2026-10-14T00:00:00Z")
	asOf, _ := time.Parse(time.RFC3339, "2026-10-14T12:00:00Z")
	sum, err := w.s.DashboardOpsSummary(w.tenA, "", dayStart, asOf)
	must(t, err)
	td := sum.Todos
	if td.UnavailableActions != 1 { // act-off(停用);act-exp 过期另列
		t.Fatalf("unavailable actions = %d, want 1", td.UnavailableActions)
	}
	if td.MissingContent != 1 { // act-on 启用未绑定;act-off 停用/act-exp 过期都不入缺素材
		t.Fatalf("missing content = %d, want 1 (act-on)", td.MissingContent)
	}
	if td.ActionAuthExpired != 1 {
		t.Fatalf("action auth expired = %d, want 1 (act-exp)", td.ActionAuthExpired)
	}
	if td.AdmissionExpired != 1 {
		t.Fatalf("admission expired = %d, want 1 (adm-1)", td.AdmissionExpired)
	}
	if td.ActionAuthExpired+td.AdmissionExpired != 2 {
		t.Fatalf("authorization expired = action+admission, want 2, got %d+%d", td.ActionAuthExpired, td.AdmissionExpired)
	}
	if td.PendingRedeem != 2 { // 当前态 issued:ben-1 + ben-4(ben-2 已核销/ben-3 reserved/ben-5 reversed 不算;无窗口)
		t.Fatalf("pending redeem = %d, want 2", td.PendingRedeem)
	}
	if td.BillingFailedOrders != 1 {
		t.Fatalf("billing failed orders = %d, want 1 (sub-2)", td.BillingFailedOrders)
	}
	if sum.PendingConfirm != 3 { // sr-1(固定集)+ sr-2 + sr-3:当前态队列,与窗口无关
		t.Fatalf("todos pending confirm = %d, want 3", sum.PendingConfirm)
	}
	// 跨租户:自有事实零;admission tenant_scope='*' 是平台级真值,对每租户可见
	// (adm-1 过期对 B 家同样成立),不是泄漏。
	sum, err = w.s.DashboardOpsSummary(w.tenB, "", dayStart, asOf)
	must(t, err)
	if sum.Todos.UnavailableActions != 0 || sum.Todos.BillingFailedOrders != 0 || sum.Todos.ActionAuthExpired != 0 {
		t.Fatalf("cross-tenant own facts must be zero: %+v", sum.Todos)
	}
	if sum.Todos.AdmissionExpired != 1 {
		t.Fatalf("platform-scope (*) expired admission is visible to every tenant, want 1: %+v", sum.Todos)
	}
}
