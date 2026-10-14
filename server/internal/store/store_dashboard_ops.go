// store_dashboard_ops.go: HUI-1677 经营看板(经营数据增量)的只读 SQL 汇总层。
//
// 纪律(与 store_dashboard.go 同一纪律,零改动):
//   - 只读:本文件没有任何写路径;汇总按需计算,不落任何汇总表;
//   - 租户隔离:所有查询以 tenant_id 为必选绑定参数;storeScope 非空时进一步
//     收窄(参与/权益按自身 store 列,批次含租户级批次 store_id=”,访问按
//     campaigns.store_id 绑定);跨租户/跨店行从不进入聚合(fail-closed 0 行);
//   - 窗口语义:RFC3339(Nano) UTC 列用 datetime(col) 与 datetime(?) 比较
//     (整秒粒度,边界含);public_view_stats.day 用日期字符串比较(日粒度);
//   - 漏斗计数=去重后的真实事实:参与按 participation 唯一;内容选择/交接
//     取该参与最早一次事实的事件时间落窗(晚到按事件时间);权益按
//     tenant+participation+benefit_slot 唯一;官方按 distinct attempt;
//   - 库存面板照抄账本占用口径(available 语义在 dashboard 包组装:
//     total − (reserved+issued+redeemed)),本层只报各状态事实计数;
//   - 撤销/过期从 append-only reward_operations 流水按窗口计数(可重算不抹历史);
//   - 筛选只落有该维度事实的查询:平台/承接方式只在冻结 attempt 行与官方
//     事实上生效;访问级没有内容版本/平台维度,不假装过滤。
package store

import (
	"database/sql"
	"fmt"
	"time"
)

// OpsFilter is the validated slice (whitelist validated upstream in the
// dashboard package). Empty strings mean "no filter".
type OpsFilter struct {
	CampaignID         string
	StoreID            string
	ContentPackVersion string
	Touchpoint         string
	Platform           string
	HandoffMethod      string
}

// DashboardOpsFacts is the store-computed fact set for the operations
// dashboard. Zero values are TRUE zeros over the scope/window.
type DashboardOpsFacts struct {
	VisitTotal int64

	StartedTotal          int64
	ContentSelectedTotal  int64
	HandoffInitiatedTotal int64

	EvidenceByClass                map[string]int64
	EvidenceDistinctParticipations int64
	OfficialConfirmedAttempts      int64

	IssuedTotal       int64
	IssuedFaceMinor   int64
	IssuedCostMinor   int64
	RedeemedTotal     int64
	RedeemedCostMinor int64

	Inventory []InventoryFactLine
	Anomalies OpsAnomalyFacts
	Cost      OpsCostFacts
}

// InventoryFactLine is one reward_inventory batch with its benefit counts.
type InventoryFactLine struct {
	InventoryID    string
	CampaignID     string
	StoreID        string
	Name           string
	Kind           string
	BenefitSlot    string
	RuleVersion    int
	Currency       string
	TotalQty       int64
	Reserved       int64
	Issued         int64
	Redeemed       int64
	Reversed       int64
	Expired        int64
	FaceValueMinor int64
	CostMinor      int64
}

// OpsAnomalyFacts: pending review / failed reservation are current-state
// queues; reversed/expired come from the append-only operations ledger.
type OpsAnomalyFacts struct {
	PendingReview     int64
	ReservationFailed int64
	ReversedInWindow  int64
	ExpiredInWindow   int64
}

// OpsCostFacts: content generation is a pointer — nil = 未录入 (never a fake 0).
type OpsCostFacts struct {
	ContentGenerationAmountMinor *int64
	ContentGenerationBilledRuns  int64
	RewardCostMinor              int64
	RewardRedeemedCostMinor      int64
	RewardFaceMinor              int64
	CostUnrecordedBatches        int64
}

// opsWindow carries both granularities: second-granular UTC bounds for
// RFC3339 columns and day bounds for public_view_stats (inclusive).
type opsWindow struct{ startTS, endTS, startDay, endDay string }

func (c opsCond) with(extra string, args ...any) opsCond {
	return opsCond{clause: c.clause + extra, args: append(append([]any{}, c.args...), args...)}
}

// newOpsWindow formats the pre-validated window (bounds inclusive).
func newOpsWindow(start, end time.Time) opsWindow {
	return opsWindow{
		startTS:  start.Format(time.RFC3339),
		endTS:    end.Format(time.RFC3339),
		startDay: start.Format("2006-01-02"),
		endDay:   end.Format("2006-01-02"),
	}
}

// opsCond carries one query's scope + filter conditions. Every value is a
// bound parameter; the scope comes from the server-side membership row.
type opsCond struct {
	clause string
	args   []any
}

// opsScalar / opsRows run one labeled statement; errors carry the label.
func (s *Store) opsScalar(label, q string, args []any) (n int64, err error) {
	err = s.DB.QueryRow(q, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dashboard_ops[%s]: %w", label, err)
	}
	return n, nil
}

// DashboardOps aggregates the operations facts of one tenant (optionally
// narrowed to one store) over the window. Read-only and idempotent: same
// inputs → same outputs. tenantID is mandatory (fail-closed: empty tenant
// matches no rows); storeScope "" means 总部/租户级.
func (s *Store) DashboardOps(tenantID, storeScope string, start, end time.Time, f OpsFilter) (facts DashboardOpsFacts, dims DashboardDimensions, err error) {
	if tenantID == "" {
		return facts, dims, nil // fail-closed
	}
	w := newOpsWindow(start, end)
	inWin := fmt.Sprintf(" AND datetime(%s)>=datetime(?) AND datetime(%s)<=datetime(?)", "%s", "%s")

	// Effective store condition: the manager scope wins (the handler refuses
	// a filter that escapes the scope); otherwise the filter applies.
	storeRef := func(col string) string {
		if storeScope != "" {
			return col + "=?"
		}
		if f.StoreID != "" {
			return col + "=?"
		}
		return ""
	}
	storeArg := func() []any {
		if storeScope != "" {
			return []any{storeScope}
		}
		if f.StoreID != "" {
			return []any{f.StoreID}
		}
		return nil
	}

	// ---- 漏斗第 1 级:访问(public_view_stats,日粒度;campaign/store/touchpoint 渠道)----
	visitsCond := opsCond{clause: "c.tenant_id=?", args: []any{tenantID}}
	if cond := storeRef("c.store_id"); cond != "" {
		visitsCond = visitsCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		visitsCond = visitsCond.with(" AND c.id=?", f.CampaignID)
	}
	if f.Touchpoint != "" {
		// touchpoint 只映射页面显式上报的 channel(入口标记,见 dashboard 注记)。
		visitsCond = visitsCond.with(" AND v.channel=?", f.Touchpoint)
	}
	visitsFrom := `
		FROM public_view_stats v
		JOIN campaign_links l ON l.code=v.code
		JOIN campaigns c ON c.id=l.campaign_id
		WHERE ` + visitsCond.clause + ` AND v.day>=? AND v.day<=?`
	if facts.VisitTotal, err = s.opsScalar("visit_total",
		`SELECT COALESCE(SUM(v.views),0)`+visitsFrom,
		append(append([]any{}, visitsCond.args...), w.startDay, w.endDay)); err != nil {
		return facts, dims, err
	}

	// ---- 漏斗第 2 级:明确参与(participations 幂等行)----
	pCond := opsCond{clause: "p.tenant_id=?", args: []any{tenantID}}
	if cond := storeRef("p.store_id"); cond != "" {
		pCond = pCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		pCond = pCond.with(" AND p.campaign_id=?", f.CampaignID)
	}
	if f.Touchpoint != "" {
		pCond = pCond.with(" AND p.touchpoint_source=?", f.Touchpoint)
	}
	if f.ContentPackVersion != "" {
		pCond = pCond.with(" AND p.content_pack_version=?", f.ContentPackVersion)
	}
	if facts.StartedTotal, err = s.opsScalar("started_total",
		`SELECT COUNT(DISTINCT p.id) FROM participations p WHERE `+pCond.clause+
			fmt.Sprintf(inWin, "p.created_at", "p.created_at"),
		append(append([]any{}, pCond.args...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// ---- 漏斗第 3 级:内容选择(attempt 冻结版本,MIN 事件时间落窗)----
	selExtra := ""
	selArgs := []any{}
	if f.Platform != "" {
		selExtra += " AND a.platform=?"
		selArgs = append(selArgs, f.Platform)
	}
	if f.HandoffMethod != "" {
		selExtra += " AND a.handoff_method=?"
		selArgs = append(selArgs, f.HandoffMethod)
	}
	if facts.ContentSelectedTotal, err = s.opsScalar("content_selected_total",
		`SELECT COUNT(*) FROM (
			SELECT a.participation_id
			FROM participation_attempts a
			JOIN participations p ON p.id=a.participation_id AND p.tenant_id=a.tenant_id
			WHERE `+pCond.clause+` AND a.content_pack_version<>''`+selExtra+`
			GROUP BY a.participation_id
			HAVING datetime(MIN(a.created_at))>=datetime(?) AND datetime(MIN(a.created_at))<=datetime(?)
		)`,
		concat(pCond.args, selArgs, []any{w.startTS, w.endTS})); err != nil {
		return facts, dims, err
	}

	// ---- 漏斗第 4 级:发起平台交接(凭据铸发,MIN 事件时间落窗)----
	if facts.HandoffInitiatedTotal, err = s.opsScalar("handoff_initiated_total",
		`SELECT COUNT(*) FROM (
			SELECT h.participation_id
			FROM handoff_credentials h
			JOIN participations p ON p.id=h.participation_id AND p.tenant_id=h.tenant_id
			WHERE `+pCond.clause+`
			GROUP BY h.participation_id
			HAVING datetime(MIN(h.created_at))>=datetime(?) AND datetime(MIN(h.created_at))<=datetime(?)
		)`,
		append(append([]any{}, pCond.args...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// ---- 三类核验证据(granted 权益行;参与维度筛选走权益→参与连接)----
	rbJoinP := ` FROM reward_benefits rb
		JOIN participations p ON p.id=rb.participation_id AND p.tenant_id=rb.tenant_id`
	evCond := opsCond{clause: "rb.tenant_id=? AND rb.evidence<>'' AND rb.status IN ('issued','redeemed')", args: []any{tenantID}}
	if cond := storeRef("rb.store_id"); cond != "" {
		evCond = evCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		evCond = evCond.with(" AND rb.campaign_id=?", f.CampaignID)
	}
	if f.Touchpoint != "" {
		evCond = evCond.with(" AND p.touchpoint_source=?", f.Touchpoint)
	}
	if f.ContentPackVersion != "" {
		evCond = evCond.with(" AND p.content_pack_version=?", f.ContentPackVersion)
	}
	evWhere := evCond.clause + fmt.Sprintf(inWin, "rb.issued_at", "rb.issued_at")
	evArgs := append(append([]any{}, evCond.args...), w.startTS, w.endTS)
	if facts.EvidenceByClass, err = s.opsGrouped("evidence_by_class",
		`SELECT rb.evidence, COUNT(*)`+rbJoinP+` WHERE `+evWhere+` GROUP BY rb.evidence`, evArgs); err != nil {
		return facts, dims, err
	}
	if facts.EvidenceDistinctParticipations, err = s.opsScalar("evidence_distinct",
		`SELECT COUNT(DISTINCT rb.participation_id)`+rbJoinP+` WHERE `+evWhere, evArgs); err != nil {
		return facts, dims, err
	}

	// 官方确认:distinct attempt(reward_official_facts;平台筛选有原生列)。
	ofCond := opsCond{clause: "f.tenant_id=?", args: []any{tenantID}}
	if cond := storeRef("c.store_id"); cond != "" {
		ofCond = ofCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		ofCond = ofCond.with(" AND f.campaign_id=?", f.CampaignID)
	}
	if f.Platform != "" {
		ofCond = ofCond.with(" AND f.platform=?", f.Platform)
	}
	if facts.OfficialConfirmedAttempts, err = s.opsScalar("official_attempts",
		`SELECT COUNT(DISTINCT f.attempt_id)
		 FROM reward_official_facts f
		 JOIN campaigns c ON c.id=f.campaign_id
		 WHERE `+ofCond.clause+fmt.Sprintf(inWin, "f.created_at", "f.created_at"),
		append(append([]any{}, ofCond.args...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// ---- 漏斗第 6 级:奖励发放(granted;面额/成本分列)----
	if err = s.opsScanRow("issued_total",
		`SELECT COUNT(*), COALESCE(SUM(rb.face_value_minor),0), COALESCE(SUM(rb.cost_minor),0)`+rbJoinP+
			` WHERE `+evCond.clause+` AND rb.status IN ('issued','redeemed')`+
			fmt.Sprintf(inWin, "rb.issued_at", "rb.issued_at"),
		evArgs, func(scan func(dest ...any) error) error {
			return scan(&facts.IssuedTotal, &facts.IssuedFaceMinor, &facts.IssuedCostMinor)
		}); err != nil {
		return facts, dims, err
	}

	// ---- 漏斗第 7 级:实际核销 ----
	if err = s.opsScanRow("redeemed_total",
		`SELECT COUNT(*), COALESCE(SUM(rb.cost_minor),0)`+rbJoinP+
			` WHERE `+evCond.clause+` AND rb.status='redeemed'`+
			fmt.Sprintf(inWin, "rb.redeemed_at", "rb.redeemed_at"),
		evArgs, func(scan func(dest ...any) error) error {
			return scan(&facts.RedeemedTotal, &facts.RedeemedCostMinor)
		}); err != nil {
		return facts, dims, err
	}

	// ---- 库存面板(批次 × 权益状态计数;租户级批次对店经理可见)----
	invCond := opsCond{clause: "i.tenant_id=?", args: []any{tenantID}}
	if f.CampaignID != "" {
		invCond = invCond.with(" AND i.campaign_id=?", f.CampaignID)
	}
	if storeScope != "" {
		invCond = invCond.with(" AND (i.store_id=? OR i.store_id='')", storeScope)
	} else if f.StoreID != "" {
		invCond = invCond.with(" AND (i.store_id=? OR i.store_id='')", f.StoreID)
	}
	facts.Inventory, err = s.opsInventory(invCond)
	if err != nil {
		return facts, dims, err
	}

	// ---- 异常待处理 ----
	// 待确认:自报未确认尝试(与 1671 RewardSummary 口径一致,当前态)。
	prCond := opsCond{clause: "a.tenant_id=? AND a.self_reported=1", args: []any{tenantID}}
	if f.CampaignID != "" {
		prCond = prCond.with(" AND a.campaign_id=?", f.CampaignID)
	}
	if cond := storeRef("c.store_id"); cond != "" {
		prCond = prCond.with(" AND "+cond, storeArg()...)
	}
	if facts.Anomalies.PendingReview, err = s.opsScalar("pending_review",
		`SELECT COUNT(*) FROM customer_publish_attempts a
		 JOIN campaigns c ON c.id=a.campaign_id
		 WHERE `+prCond.clause+`
		   AND NOT EXISTS (SELECT 1 FROM reward_benefits b
		     WHERE b.tenant_id=a.tenant_id AND b.participation_id=a.id)`, prCond.args); err != nil {
		return facts, dims, err
	}
	// 预留失败(当前态)。
	rfCond := opsCond{clause: "p.tenant_id=? AND p.benefit_reservation_state='failed'", args: []any{tenantID}}
	if f.CampaignID != "" {
		rfCond = rfCond.with(" AND p.campaign_id=?", f.CampaignID)
	}
	if cond := storeRef("p.store_id"); cond != "" {
		rfCond = rfCond.with(" AND "+cond, storeArg()...)
	}
	if facts.Anomalies.ReservationFailed, err = s.opsScalar("reservation_failed",
		`SELECT COUNT(*) FROM participations p WHERE `+rfCond.clause, rfCond.args); err != nil {
		return facts, dims, err
	}
	// 撤销/过期:append-only 流水按窗口计数(可重算,不抹历史)。
	opExtra := ""
	opArgs := []any{}
	if f.CampaignID != "" {
		opExtra += " AND rb.campaign_id=?"
		opArgs = append(opArgs, f.CampaignID)
	}
	if cond := storeRef("rb.store_id"); cond != "" {
		opExtra += " AND " + cond
		opArgs = append(opArgs, storeArg()...)
	}
	for target, op := range map[*int64]string{&facts.Anomalies.ReversedInWindow: "reverse", &facts.Anomalies.ExpiredInWindow: "expire"} {
		if *target, err = s.opsScalar("op_"+op,
			`SELECT COUNT(*) FROM reward_operations o
			 JOIN reward_benefits rb ON rb.id=o.benefit_id
			 WHERE o.tenant_id=? AND o.op=?`+opExtra+
				fmt.Sprintf(inWin, "o.created_at", "o.created_at"),
			concat([]any{tenantID, op}, opArgs, []any{w.startTS, w.endTS})); err != nil {
			return facts, dims, err
		}
	}

	// ---- 成本三分 ----
	costCond := opsCond{clause: "j.tenant_id=? AND CAST(json_extract(j.payload,'$.trace.billed') AS INTEGER)=1", args: []any{tenantID}}
	if f.CampaignID != "" {
		costCond = costCond.with(" AND j.campaign_id=?", f.CampaignID)
	}
	var contentAmount sql.NullInt64
	err = s.DB.QueryRow(
		`SELECT COUNT(*), SUM(CAST(json_extract(j.payload,'$.trace.amount_minor') AS INTEGER))
		 FROM copy_jobs j
		 WHERE `+costCond.clause+fmt.Sprintf(inWin, "j.created_at", "j.created_at"),
		append(append([]any{}, costCond.args...), w.startTS, w.endTS)...).Scan(&facts.Cost.ContentGenerationBilledRuns, &contentAmount)
	if err != nil {
		return facts, dims, fmt.Errorf("dashboard_ops[content_cost]: %w", err)
	}
	if contentAmount.Valid {
		v := contentAmount.Int64
		facts.Cost.ContentGenerationAmountMinor = &v
	}
	if facts.Cost.RewardCostMinor, err = s.opsScalar("reward_cost",
		`SELECT COALESCE(SUM(rb.cost_minor),0)`+rbJoinP+
			` WHERE `+evCond.clause+` AND rb.status IN ('issued','redeemed')`+
			fmt.Sprintf(inWin, "rb.issued_at", "rb.issued_at"), evArgs); err != nil {
		return facts, dims, err
	}
	if facts.Cost.RewardRedeemedCostMinor, err = s.opsScalar("reward_redeemed_cost",
		`SELECT COALESCE(SUM(rb.cost_minor),0)`+rbJoinP+
			` WHERE `+evCond.clause+` AND rb.status='redeemed'`+
			fmt.Sprintf(inWin, "rb.redeemed_at", "rb.redeemed_at"), evArgs); err != nil {
		return facts, dims, err
	}
	if facts.Cost.RewardFaceMinor, err = s.opsScalar("reward_face",
		`SELECT COALESCE(SUM(rb.face_value_minor),0)`+rbJoinP+
			` WHERE `+evCond.clause+` AND rb.status IN ('issued','redeemed')`+
			fmt.Sprintf(inWin, "rb.issued_at", "rb.issued_at"), evArgs); err != nil {
		return facts, dims, err
	}
	urCond := opsCond{clause: "i.tenant_id=? AND i.face_value_minor>0 AND i.cost_minor=0", args: []any{tenantID}}
	if f.CampaignID != "" {
		urCond = urCond.with(" AND i.campaign_id=?", f.CampaignID)
	}
	if storeScope != "" {
		urCond = urCond.with(" AND (i.store_id=? OR i.store_id='')", storeScope)
	} else if f.StoreID != "" {
		urCond = urCond.with(" AND (i.store_id=? OR i.store_id='')", f.StoreID)
	}
	if facts.Cost.CostUnrecordedBatches, err = s.opsScalar("unrecorded_batches",
		`SELECT COUNT(*) FROM reward_inventory i WHERE `+urCond.clause, urCond.args); err != nil {
		return facts, dims, err
	}

	// ---- 维度来源引用(与 PR#7 同一来源;活动/门店引用供下钻)----
	campCond := "c.tenant_id=?"
	campArgs := []any{tenantID}
	if storeScope != "" {
		campCond += " AND c.store_id=?"
		campArgs = append(campArgs, storeScope)
	}
	dims.Campaigns, err = s.dashCampaignRefs(campCond, campArgs)
	if err != nil {
		return facts, dims, err
	}
	storeCond := "tenant_id=?"
	storeArgs := []any{tenantID}
	if storeScope != "" {
		storeCond += " AND id=?"
		storeArgs = append(storeArgs, storeScope)
	}
	dims.Stores, err = s.dashStoreRefs(storeCond, storeArgs)
	return facts, dims, err
}

// DashboardOpsSummary computes the fixed OperationsSummary fact set
// (HUI-2628 port): today = [dayStart, asOf] in UTC, scope only, no filters.
func (s *Store) DashboardOpsSummary(tenantID, storeScope string, dayStart, asOf time.Time) (sum SummaryOpsFacts, err error) {
	if tenantID == "" {
		return sum, nil // fail-closed
	}
	w := newOpsWindow(dayStart, asOf)
	storeCond := ""
	storeArgs := []any{}
	if storeScope != "" {
		storeCond = " AND rb.store_id=?"
		storeArgs = []any{storeScope}
	}
	granted := " FROM reward_benefits rb WHERE rb.tenant_id=? AND rb.status IN ('issued','redeemed')" + storeCond
	if sum.RewardsIssued, err = s.opsScalar("summary_issued",
		`SELECT COUNT(*)`+granted+` AND datetime(rb.issued_at)>=datetime(?) AND datetime(rb.issued_at)<=datetime(?)`,
		append(append([]any{}, tenantID), append(storeArgs, w.startTS, w.endTS)...)); err != nil {
		return sum, err
	}
	if sum.RewardsRedeemed, err = s.opsScalar("summary_redeemed",
		`SELECT COUNT(*) FROM reward_benefits rb WHERE rb.tenant_id=? AND rb.status='redeemed'`+storeCond+
			` AND datetime(rb.redeemed_at)>=datetime(?) AND datetime(rb.redeemed_at)<=datetime(?)`,
		append(append([]any{}, tenantID), append(storeArgs, w.startTS, w.endTS)...)); err != nil {
		return sum, err
	}
	campCond := ""
	campArgs := []any{}
	if storeScope != "" {
		campCond = " JOIN campaigns c ON c.id=a.campaign_id WHERE a.tenant_id=? AND c.store_id=?"
		campArgs = []any{tenantID, storeScope}
	} else {
		campCond = " WHERE a.tenant_id=?"
		campArgs = []any{tenantID}
	}
	if sum.PendingConfirm, err = s.opsScalar("summary_pending",
		`SELECT COUNT(*) FROM customer_publish_attempts a`+campCond+`
		 AND a.self_reported=1
		 AND NOT EXISTS (SELECT 1 FROM reward_benefits b
		   WHERE b.tenant_id=a.tenant_id AND b.participation_id=a.id)`, campArgs); err != nil {
		return sum, err
	}
	anomalyExtra := ""
	anomalyArgs := []any{tenantID}
	if storeScope != "" {
		anomalyExtra = " AND rb.store_id=?"
		anomalyArgs = append(anomalyArgs, storeScope)
	}
	reversals, err := s.opsScalar("summary_reversed",
		`SELECT COUNT(*) FROM reward_operations o
		 JOIN reward_benefits rb ON rb.id=o.benefit_id
		 WHERE o.tenant_id=? AND o.op='reverse'`+anomalyExtra+
			` AND datetime(o.created_at)>=datetime(?) AND datetime(o.created_at)<=datetime(?)`,
		append(append([]any{}, anomalyArgs...), w.startTS, w.endTS))
	if err != nil {
		return sum, err
	}
	expired, err := s.opsScalar("summary_expired",
		`SELECT COUNT(*) FROM reward_operations o
		 JOIN reward_benefits rb ON rb.id=o.benefit_id
		 WHERE o.tenant_id=? AND o.op='expire'`+anomalyExtra+
			` AND datetime(o.created_at)>=datetime(?) AND datetime(o.created_at)<=datetime(?)`,
		append(append([]any{}, anomalyArgs...), w.startTS, w.endTS))
	if err != nil {
		return sum, err
	}
	failedCond := " WHERE p.tenant_id=? AND p.benefit_reservation_state='failed'"
	failedArgs := []any{tenantID}
	if storeScope != "" {
		failedCond += " AND p.store_id=?"
		failedArgs = append(failedArgs, storeScope)
	}
	failed, err := s.opsScalar("summary_failed",
		`SELECT COUNT(*) FROM participations p`+failedCond, failedArgs)
	if err != nil {
		return sum, err
	}
	sum.Anomalies = reversals + expired + failed
	return sum, nil
}

// SummaryOpsFacts is the store-side fact set of the OperationsSummary port.
type SummaryOpsFacts struct {
	PendingConfirm  int64
	RewardsIssued   int64
	RewardsRedeemed int64
	Anomalies       int64
}

// ---- helpers -----------------------------------------------------------------

func concat(groups ...[]any) []any {
	out := []any{}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func (s *Store) opsGrouped(label, q string, args []any) (map[string]int64, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard_ops[%s]: %w", label, err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("dashboard_ops[%s]: %w", label, err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) opsScanRow(label, q string, args []any, use func(scan func(dest ...any) error) error) error {
	row := s.DB.QueryRow(q, args...)
	if err := use(row.Scan); err != nil {
		return fmt.Errorf("dashboard_ops[%s]: %w", label, err)
	}
	return nil
}

func (s *Store) opsInventory(cond opsCond) ([]InventoryFactLine, error) {
	rows, err := s.DB.Query(`
		SELECT i.id, i.campaign_id, COALESCE(i.store_id,''), i.name, i.kind, i.benefit_slot,
		       i.rule_version, i.currency, i.total_qty, i.face_value_minor, i.cost_minor,
		       COALESCE(SUM(CASE WHEN b.status='reserved' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN b.status='issued'   THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN b.status='redeemed' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN b.status='reversed' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN b.status='expired'  THEN 1 ELSE 0 END),0)
		FROM reward_inventory i
		LEFT JOIN reward_benefits b ON b.inventory_id=i.id
		WHERE `+cond.clause+`
		GROUP BY i.id
		ORDER BY i.created_at, i.id`, cond.args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard_ops[inventory]: %w", err)
	}
	defer rows.Close()
	out := []InventoryFactLine{}
	for rows.Next() {
		var l InventoryFactLine
		if err := rows.Scan(&l.InventoryID, &l.CampaignID, &l.StoreID, &l.Name, &l.Kind, &l.BenefitSlot,
			&l.RuleVersion, &l.Currency, &l.TotalQty, &l.FaceValueMinor, &l.CostMinor,
			&l.Reserved, &l.Issued, &l.Redeemed, &l.Reversed, &l.Expired); err != nil {
			return nil, fmt.Errorf("dashboard_ops[inventory]: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
