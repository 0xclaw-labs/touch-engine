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
	ActionID           string // 4584 动作实例 id;SQL 内 tenant/store 再收窄(fail-closed)
	Environment        string // 3535 六环境;只落在有该维度的事实(store_service_events)
}

// OpsLeadsFacts is the store-side lead-truth fact set (4594 口径):
// 待同步不算已获客——pending_sync 与 receiver_confirmed 是独立桶。
type OpsLeadsFacts struct {
	CampaignSubmitted       int64
	StoreSubmitted          int64
	PendingSync             int64
	ReceiverConfirmed       int64
	Revoked                 int64
	ReceiverDeclaredRevoked int64
	UniqueContacts          int64
}

// OpsCostReconFacts: 4591 投影只读。指针 = 窗口内没有该类支付事实(未录入,不冒充 0)。
type OpsCostReconFacts struct {
	SubscriptionPaidMinor    *int64
	SubscriptionPaidOrders   int64
	SubscriptionFailedOrders int64
	AIConfirmedMinor         int64
	AIConfirmedCount         int64
	AIConsumedMinor          int64
	AIConsumedCount          int64
	AIRefundedMinor          int64
	AIRefundedCount          int64
	AIFailedMinor            int64
	AIFailedCount            int64
	HardwarePaidMinor        *int64
	HardwarePaidOrders       int64
	HardwareRefundMinor      int64
	HardwareRefunds          int64
}

// OpsTodoFacts is the store-side to-do digest fact set (当前态队列+准入到期)。
type OpsTodoFacts struct {
	UnavailableActions  int64
	MissingContent      int64
	ActionAuthExpired   int64
	AdmissionExpired    int64
	PendingRedeem       int64
	BillingFailedOrders int64
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

	// 六层证据(TOUCH-ALL-20261109-v1):各层来源/去重键独立,永不相加。
	LayerActionSessions   int64 // 3546 action_session_events kind='created'(distinct session)
	LayerServiceOpens     int64 // 4589 store_service_events kind='open'
	LayerJumpAttempts     int64 // 4589 store_service_events kind='jump_attempt'
	LayerEditorProved     int64 // 1670 publish_handoff_events kind='editor_open_proved'(distinct handoff)
	LayerHandoffRequested int64 // 1670 publish_handoffs 创建行
	LayerHandoffCompleted int64 // 1670 状态蕴含 handoff_ready 已达的行
	LayerUserReported     int64 // 顾客自报 attempt(self_reported=1)
	LayerSelfReportEvent  int64 // 1670 v2 kind='customer_self_report'(distinct handoff)
	LayerStaffSaw         int64 // publish_handoff_events kind='staff_saw_submit'(distinct handoff)
	LayerStaffVerified    int64 // publish_handoff_events kind='staff_verified_work'(distinct handoff)
	LayerStaffConfirm     int64 // 1670 v2 kind='staff_confirm'(distinct handoff)
	LayerPlatformHandoffs int64 // publish_handoffs status='platform_confirmed'
	LayerOfficialEvents   int64 // publish_handoff_events kind='official_confirmed'(distinct handoff)
	LayerPlatformConfirm  int64 // 1670 v2 kind='platform_confirm'(distinct handoff)
	LayerCustomerClick    int64 // 1670 v2 kind='customer_click'(逐事件行)
	LayerWakeAttempt      int64 // 1670 v2 kind='wake_attempt'(逐事件行)
	LayerMediaHandoff     int64 // 1670 v2 kind='media_handoff'(distinct handoff)

	Leads     OpsLeadsFacts
	CostRecon OpsCostReconFacts

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

	// ---- 六层证据(TOUCH-ALL-20261109-v1):各层独立计数,只读,永不相加 ----
	// L1 点击:3546 动作会话创建(distinct session;action_id 维度有原生列)。
	sesCond := opsCond{clause: "e.tenant_id=? AND e.kind='created'", args: []any{tenantID}}
	if cond := storeRef("e.store_id"); cond != "" {
		sesCond = sesCond.with(" AND "+cond, storeArg()...)
	}
	if f.ActionID != "" {
		sesCond = sesCond.with(" AND e.action_id=?", f.ActionID)
	}
	if facts.LayerActionSessions, err = s.opsScalar("layer_action_sessions",
		`SELECT COUNT(DISTINCT e.session_id) FROM action_session_events e WHERE `+sesCond.clause+
			fmt.Sprintf(inWin, "e.occurred_at", "e.occurred_at"),
		append(append([]any{}, sesCond.args...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// L1/L2 服务行为(4589):store_service_events;service_id 兼收 sa_*/sv_*,
	// action_id 筛选直接落该列;environment 是该表原生维度。
	svcLine := func(kind string) (int64, error) {
		cond := opsCond{clause: "v.tenant_id=? AND v.event_kind=?", args: []any{tenantID, kind}}
		if cond2 := storeRef("v.store_id"); cond2 != "" {
			cond = cond.with(" AND "+cond2, storeArg()...)
		}
		if f.ActionID != "" {
			cond = cond.with(" AND v.service_id=?", f.ActionID)
		}
		if f.Environment != "" {
			cond = cond.with(" AND v.environment=?", f.Environment)
		}
		return s.opsScalar("layer_service_"+kind,
			`SELECT COUNT(*) FROM store_service_events v WHERE `+cond.clause+
				fmt.Sprintf(inWin, "v.occurred_at", "v.occurred_at"),
			append(append([]any{}, cond.args...), w.startTS, w.endTS))
	}
	if facts.LayerServiceOpens, err = svcLine("open"); err != nil {
		return facts, dims, err
	}
	if facts.LayerJumpAttempts, err = svcLine("jump_attempt"); err != nil {
		return facts, dims, err
	}

	// 发布域(1670/自报):publish_handoffs/事件流经 campaigns 挂店与活动维度。
	phJoin := ` FROM publish_handoffs ph JOIN campaigns c ON c.id=ph.campaign_id`
	phCond := opsCond{clause: "ph.tenant_id=?", args: []any{tenantID}}
	if cond := storeRef("c.store_id"); cond != "" {
		phCond = phCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		phCond = phCond.with(" AND ph.campaign_id=?", f.CampaignID)
	}
	if f.Platform != "" {
		phCond = phCond.with(" AND ph.platform=?", f.Platform)
	}
	// v2 动作交接行的原生维度(action_id/environment);v1 活动行为空串,
	// 指定该筛选时自然不匹配——是诚实收窄,不是静默全量。
	if f.ActionID != "" {
		phCond = phCond.with(" AND ph.action_id=?", f.ActionID)
	}
	if f.Environment != "" {
		phCond = phCond.with(" AND ph.environment=?", f.Environment)
	}
	phArgs := append([]any{}, phCond.args...)

	// L3 requested:创建行(idempotency_key UNIQUE,重放回原行)。
	if facts.LayerHandoffRequested, err = s.opsScalar("layer_handoff_requested",
		`SELECT COUNT(*)`+phJoin+` WHERE `+phCond.clause+
			fmt.Sprintf(inWin, "ph.created_at", "ph.created_at"),
		append(append([]any{}, phArgs...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}
	// L3 completed:状态机只前进,当前状态 ∈ 六态蕴含 handoff_ready 已达
	// (publishhandoff.transitions 结构保证;当前态披露,可随推进变化)。
	if facts.LayerHandoffCompleted, err = s.opsScalar("layer_handoff_completed",
		`SELECT COUNT(*)`+phJoin+` WHERE `+phCond.clause+
			` AND ph.status IN ('handoff_ready','editor_opened','submitting','awaiting_review','unknown','platform_confirmed')`+
			fmt.Sprintf(inWin, "ph.created_at", "ph.created_at"),
		append(append([]any{}, phArgs...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}
	// L6 platform_confirmed(当前态)。
	if facts.LayerPlatformHandoffs, err = s.opsScalar("layer_platform_handoffs",
		`SELECT COUNT(*)`+phJoin+` WHERE `+phCond.clause+` AND ph.status='platform_confirmed'`+
			fmt.Sprintf(inWin, "ph.created_at", "ph.created_at"),
		append(append([]any{}, phArgs...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// 事件流行:distinct handoff(source+event_id/UNIQUE receipt 幂等;append-only)。
	phEventLine := func(kind string) (int64, error) {
		return s.opsScalar("layer_phe_"+kind,
			`SELECT COUNT(DISTINCT e.handoff_id) FROM publish_handoff_events e
			 JOIN publish_handoffs ph ON ph.id=e.handoff_id
			 JOIN campaigns c ON c.id=ph.campaign_id
			 WHERE `+phCond.clause+` AND e.kind=?`+
				fmt.Sprintf(inWin, "e.created_at", "e.created_at"),
			append(append(append([]any{}, phArgs...), kind), w.startTS, w.endTS))
	}
	if facts.LayerEditorProved, err = phEventLine("editor_open_proved"); err != nil {
		return facts, dims, err
	}
	if facts.LayerStaffSaw, err = phEventLine("staff_saw_submit"); err != nil {
		return facts, dims, err
	}
	if facts.LayerStaffVerified, err = phEventLine("staff_verified_work"); err != nil {
		return facts, dims, err
	}
	if facts.LayerOfficialEvents, err = phEventLine("official_confirmed"); err != nil {
		return facts, dims, err
	}
	// 1670 v2 六层事实 kind(click/wake 逐事件行;handoff/self-report/staff/platform
	// 按 distinct handoff;幂等=(tenant,handoff,source,event_id) UNIQUE/ receipt UNIQUE)。
	if facts.LayerCustomerClick, err = s.opsScalar("layer_phe_customer_click",
		`SELECT COUNT(*) FROM publish_handoff_events e
		 JOIN publish_handoffs ph ON ph.id=e.handoff_id
		 JOIN campaigns c ON c.id=ph.campaign_id
		 WHERE `+phCond.clause+` AND e.kind='customer_click'`+
			fmt.Sprintf(inWin, "e.created_at", "e.created_at"),
		append(append([]any{}, phArgs...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}
	if facts.LayerWakeAttempt, err = s.opsScalar("layer_phe_wake_attempt",
		`SELECT COUNT(*) FROM publish_handoff_events e
		 JOIN publish_handoffs ph ON ph.id=e.handoff_id
		 JOIN campaigns c ON c.id=ph.campaign_id
		 WHERE `+phCond.clause+` AND e.kind='wake_attempt'`+
			fmt.Sprintf(inWin, "e.created_at", "e.created_at"),
		append(append([]any{}, phArgs...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}
	if facts.LayerMediaHandoff, err = phEventLine("media_handoff"); err != nil {
		return facts, dims, err
	}
	if facts.LayerSelfReportEvent, err = phEventLine("customer_self_report"); err != nil {
		return facts, dims, err
	}
	if facts.LayerStaffConfirm, err = phEventLine("staff_confirm"); err != nil {
		return facts, dims, err
	}
	if facts.LayerPlatformConfirm, err = phEventLine("platform_confirm"); err != nil {
		return facts, dims, err
	}

	// L4 顾客自报(distinct attempt;自报不是发布,只进入待确认队列)。
	urCond := opsCond{clause: "a.tenant_id=? AND a.self_reported=1", args: []any{tenantID}}
	if cond := storeRef("c.store_id"); cond != "" {
		urCond = urCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		urCond = urCond.with(" AND a.campaign_id=?", f.CampaignID)
	}
	if f.Platform != "" {
		urCond = urCond.with(" AND a.platform=?", f.Platform)
	}
	if facts.LayerUserReported, err = s.opsScalar("layer_user_reported",
		`SELECT COUNT(*) FROM customer_publish_attempts a JOIN campaigns c ON c.id=a.campaign_id
		 WHERE `+urCond.clause+fmt.Sprintf(inWin, "a.created_at", "a.created_at"),
		append(append([]any{}, urCond.args...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// ---- 线索口径(4594):待同步不算已获客;纯浏览/微信点击不在任何线索桶 ----
	leadCond := opsCond{clause: "l.tenant_id=?", args: []any{tenantID}}
	if cond := storeRef("l.store_id"); cond != "" {
		leadCond = leadCond.with(" AND "+cond, storeArg()...)
	}
	if f.CampaignID != "" {
		leadCond = leadCond.with(" AND l.campaign_id=?", f.CampaignID)
	}
	leadWin := leadCond.clause + fmt.Sprintf(inWin, "l.created_at", "l.created_at")
	leadArgs := append(append([]any{}, leadCond.args...), w.startTS, w.endTS)
	if facts.Leads.CampaignSubmitted, err = s.opsScalar("leads_campaign",
		`SELECT COUNT(*) FROM lead_submissions l WHERE `+leadWin+` AND l.capture_source='campaign'`, leadArgs); err != nil {
		return facts, dims, err
	}
	if facts.Leads.StoreSubmitted, err = s.opsScalar("leads_store",
		`SELECT COUNT(*) FROM lead_submissions l WHERE `+leadWin+` AND l.capture_source='store'`, leadArgs); err != nil {
		return facts, dims, err
	}
	if facts.Leads.PendingSync, err = s.opsScalar("leads_pending_sync",
		`SELECT COUNT(*) FROM lead_submissions l WHERE `+leadWin+` AND l.sync_state='pending_sync'`, leadArgs); err != nil {
		return facts, dims, err
	}
	// 已获客确认:ACK 五元组落库(crm_received + receiver_state='stored');Notify
	// 投递成功(delivered)只停 pending_sync,绝不算已获客。
	if facts.Leads.ReceiverConfirmed, err = s.opsScalar("leads_receiver_confirmed",
		`SELECT COUNT(*) FROM lead_submissions l WHERE `+leadWin+
			` AND l.sync_state='crm_received' AND l.receiver_state='stored'`, leadArgs); err != nil {
		return facts, dims, err
	}
	if facts.Leads.Revoked, err = s.opsScalar("leads_revoked",
		`SELECT COUNT(*) FROM lead_submissions l WHERE `+leadWin+
			` AND (l.sync_state='revoked' OR l.revoke_source_revision>0)`, leadArgs); err != nil {
		return facts, dims, err
	}
	// 接收方申报 revoked:接收方申报事实,只记录不发明本地状态。
	if facts.Leads.ReceiverDeclaredRevoked, err = s.opsScalar("leads_receiver_revoked",
		`SELECT COUNT(*) FROM lead_submissions l WHERE `+leadWin+` AND l.receiver_state='revoked'`, leadArgs); err != nil {
		return facts, dims, err
	}
	if facts.Leads.UniqueContacts, err = s.opsScalar("leads_unique_contacts",
		`SELECT COUNT(DISTINCT l.phone) FROM lead_submissions l WHERE `+leadWin, leadArgs); err != nil {
		return facts, dims, err
	}

	// ---- 费用对账(4591 投影只读):订阅/AI/硬件分明细,退款分列不冲销 ----
	// 订阅:支付事实订单(active)金额投影;failed 单列(失败不是支出)。
	subCond := opsCond{clause: "o.tenant_id=? AND o.kind='subscription'", args: []any{tenantID}}
	var subAmount sql.NullInt64
	if err = s.opsScanRow("cost_subscription",
		`SELECT COUNT(*), SUM(o.amount_minor) FROM billing_orders o WHERE `+subCond.clause+
			` AND o.status='active'`+fmt.Sprintf(inWin, "o.created_at", "o.created_at"),
		append(append([]any{}, subCond.args...), w.startTS, w.endTS),
		func(scan func(dest ...any) error) error {
			return scan(&facts.CostRecon.SubscriptionPaidOrders, &subAmount)
		}); err != nil {
		return facts, dims, err
	}
	if subAmount.Valid {
		v := subAmount.Int64
		facts.CostRecon.SubscriptionPaidMinor = &v
	}
	if facts.CostRecon.SubscriptionFailedOrders, err = s.opsScalar("cost_subscription_failed",
		`SELECT COUNT(*) FROM billing_orders o WHERE `+subCond.clause+` AND o.status='failed'`+
			fmt.Sprintf(inWin, "o.created_at", "o.created_at"),
		append(append([]any{}, subCond.args...), w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}

	// AI 用量:按 state 分列(refunded/failed 单列,不计支出)。business_ref 的
	// 语义由消费方(4595 handoff 引用)决定,不猜作 campaign 维度,不假装过滤。
	aiCond := opsCond{clause: "u.tenant_id=?", args: []any{tenantID}}
	aiRows, err := s.DB.Query(
		`SELECT u.state, COUNT(*), COALESCE(SUM(u.amount_minor),0) FROM billing_ai_usage u
		 WHERE `+aiCond.clause+fmt.Sprintf(inWin, "u.created_at", "u.created_at")+` GROUP BY u.state`,
		append(append([]any{}, aiCond.args...), w.startTS, w.endTS)...)
	if err != nil {
		return facts, dims, fmt.Errorf("dashboard_ops[cost_ai]: %w", err)
	}
	for aiRows.Next() {
		var state string
		var cnt, amount int64
		if err := aiRows.Scan(&state, &cnt, &amount); err != nil {
			aiRows.Close()
			return facts, dims, fmt.Errorf("dashboard_ops[cost_ai]: %w", err)
		}
		switch state {
		case "confirmed":
			facts.CostRecon.AIConfirmedCount, facts.CostRecon.AIConfirmedMinor = cnt, amount
		case "consumed":
			facts.CostRecon.AIConsumedCount, facts.CostRecon.AIConsumedMinor = cnt, amount
		case "refunded":
			facts.CostRecon.AIRefundedCount, facts.CostRecon.AIRefundedMinor = cnt, amount
		case "failed":
			facts.CostRecon.AIFailedCount, facts.CostRecon.AIFailedMinor = cnt, amount
		}
	}
	if err = aiRows.Err(); err != nil {
		aiRows.Close()
		return facts, dims, fmt.Errorf("dashboard_ops[cost_ai]: %w", err)
	}
	aiRows.Close()

	// 硬件:支付事实订单(active)投影;退款分列对账,不冲销。
	hwCond := opsCond{clause: "o.tenant_id=? AND o.kind='hardware'", args: []any{tenantID}}
	var hwAmount sql.NullInt64
	if err = s.opsScanRow("cost_hardware",
		`SELECT COUNT(*), SUM(o.amount_minor) FROM billing_orders o WHERE `+hwCond.clause+
			` AND o.status='active'`+fmt.Sprintf(inWin, "o.created_at", "o.created_at"),
		append(append([]any{}, hwCond.args...), w.startTS, w.endTS),
		func(scan func(dest ...any) error) error {
			return scan(&facts.CostRecon.HardwarePaidOrders, &hwAmount)
		}); err != nil {
		return facts, dims, err
	}
	if hwAmount.Valid {
		v := hwAmount.Int64
		facts.CostRecon.HardwarePaidMinor = &v
	}
	// 退款金额与单数分开查:SUM 为 0 时也要如实报 0 元/0 单。
	if facts.CostRecon.HardwareRefundMinor, err = s.opsScalar("cost_hardware_refunds",
		`SELECT COALESCE(SUM(r.amount_minor),0) FROM billing_refunds r WHERE r.tenant_id=?`+
			fmt.Sprintf(inWin, "r.created_at", "r.created_at"),
		append([]any{tenantID}, w.startTS, w.endTS)); err != nil {
		return facts, dims, err
	}
	if facts.CostRecon.HardwareRefunds, err = s.opsScalar("cost_hardware_refund_count",
		`SELECT COUNT(*) FROM billing_refunds r WHERE r.tenant_id=?`+
			fmt.Sprintf(inWin, "r.created_at", "r.created_at"),
		append([]any{tenantID}, w.startTS, w.endTS)); err != nil {
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
	unrecCond := opsCond{clause: "i.tenant_id=? AND i.face_value_minor>0 AND i.cost_minor=0", args: []any{tenantID}}
	if f.CampaignID != "" {
		unrecCond = unrecCond.with(" AND i.campaign_id=?", f.CampaignID)
	}
	if storeScope != "" {
		unrecCond = unrecCond.with(" AND (i.store_id=? OR i.store_id='')", storeScope)
	} else if f.StoreID != "" {
		unrecCond = unrecCond.with(" AND (i.store_id=? OR i.store_id='')", f.StoreID)
	}
	if facts.Cost.CostUnrecordedBatches, err = s.opsScalar("unrecorded_batches",
		`SELECT COUNT(*) FROM reward_inventory i WHERE `+unrecCond.clause, unrecCond.args); err != nil {
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
	if err != nil {
		return facts, dims, err
	}
	// 动作引用行(4584):筛选下拉的数据源;含停用行(商家能看到筛选为何无果)。
	actCond := "tenant_id=?"
	actArgs := []any{tenantID}
	if storeScope != "" {
		actCond += " AND store_id=?"
		actArgs = append(actArgs, storeScope)
	} else if f.StoreID != "" {
		actCond += " AND store_id=?"
		actArgs = append(actArgs, f.StoreID)
	}
	dims.Actions, err = s.dashActionRefs(actCond, actArgs)
	return facts, dims, err
}

// dashActionRefs reads the scope-narrowed store-action reference rows.
func (s *Store) dashActionRefs(cond string, args []any) ([]DashboardActionRef, error) {
	rows, err := s.DB.Query(
		`SELECT id, label, platform, kind, enabled FROM store_actions WHERE `+cond+` ORDER BY created_at, id`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard_ops[action_refs]: %w", err)
	}
	defer rows.Close()
	out := []DashboardActionRef{}
	for rows.Next() {
		var r DashboardActionRef
		var enabled int
		if err := rows.Scan(&r.ID, &r.Label, &r.Platform, &r.Kind, &enabled); err != nil {
			return nil, fmt.Errorf("dashboard_ops[action_refs]: %w", err)
		}
		r.Enabled = enabled == 1
		out = append(out, r)
	}
	return out, rows.Err()
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

	// ---- 待办摘要(TOUCH-ALL-20261109-v1):当前态队列 + 准入到期;asOf 为准 ----
	todoScope := ""
	todoArgs := []any{tenantID}
	if storeScope != "" {
		todoScope = " AND store_id=?"
		todoArgs = append(todoArgs, storeScope)
	}
	// 不可用动作:enabled=0 或 revoked=1(expires_at 过期另列 action_auth_expired)。
	if sum.Todos.UnavailableActions, err = s.opsScalar("todo_unavailable",
		`SELECT COUNT(*) FROM store_actions WHERE tenant_id=?`+todoScope+` AND (enabled=0 OR revoked=1)`,
		todoArgs); err != nil {
		return sum, err
	}
	// 授权到期:启用未撤销动作 expires_at 已过(<= asOf)。
	if sum.Todos.ActionAuthExpired, err = s.opsScalar("todo_action_auth_expired",
		`SELECT COUNT(*) FROM store_actions WHERE tenant_id=?`+todoScope+
			` AND enabled=1 AND revoked=0 AND expires_at<>'' AND datetime(expires_at)<=datetime(?)`,
		append(append([]any{}, todoArgs...), asOf.Format(time.RFC3339))); err != nil {
		return sum, err
	}
	// 缺素材:启用且未撤销未过期、未绑定已采用内容(content_binding_ref='')。
	if sum.Todos.MissingContent, err = s.opsScalar("todo_missing_content",
		`SELECT COUNT(*) FROM store_actions WHERE tenant_id=?`+todoScope+
			` AND enabled=1 AND revoked=0 AND content_binding_ref='' AND (expires_at='' OR datetime(expires_at)>datetime(?))`,
		append(append([]any{}, todoArgs...), asOf.Format(time.RFC3339))); err != nil {
		return sum, err
	}
	// 渠道准入到期:admitted 且 valid_until 已过;tenant_scope='*' 平台级对每租户
	// 可见,定向行只算自己的(tenant_scope=<tenant>)。准入是租户级事实:店经理
	// 视角无门店维度,fail-closed 答 0(口径在 dashboard 包披露)。
	if storeScope == "" {
		if sum.Todos.AdmissionExpired, err = s.opsScalar("todo_admission_expired",
			`SELECT COUNT(*) FROM channel_admissions
			 WHERE status='admitted' AND valid_until<>'' AND datetime(valid_until)<=datetime(?)
			 AND (tenant_scope='*' OR tenant_scope=?)`,
			[]any{asOf.Format(time.RFC3339), tenantID}); err != nil {
			return sum, err
		}
	}
	// 待核销:已发放未核销(当前态;store_scope 走权益行自身 store 列)。
	redeemCond := " WHERE rb.tenant_id=? AND rb.status='issued'"
	redeemArgs := []any{tenantID}
	if storeScope != "" {
		redeemCond += " AND rb.store_id=?"
		redeemArgs = append(redeemArgs, storeScope)
	}
	if sum.Todos.PendingRedeem, err = s.opsScalar("todo_pending_redeem",
		`SELECT COUNT(*) FROM reward_benefits rb`+redeemCond, redeemArgs); err != nil {
		return sum, err
	}
	// 账务异常:failed 订单数(当前态队列;失败不是支出,是待处理)。订单是租户级
	// 事实(无 store 列):店经理视角 fail-closed 答 0。
	if storeScope == "" {
		if sum.Todos.BillingFailedOrders, err = s.opsScalar("todo_billing_failed",
			`SELECT COUNT(*) FROM billing_orders o WHERE o.tenant_id=? AND o.status='failed'`,
			[]any{tenantID}); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

// SummaryOpsFacts is the store-side fact set of the OperationsSummary port.
type SummaryOpsFacts struct {
	PendingConfirm  int64
	RewardsIssued   int64
	RewardsRedeemed int64
	Anomalies       int64
	Todos           OpsTodoFacts
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
