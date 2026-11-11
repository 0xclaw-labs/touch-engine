// funnel.go: HUI-1677 经营数据增量的纯函数层 —— 漏斗七级、三类核验证据、
// 库存、成本三分、异常待处理与 OperationsSummary 固定契约。
//
// 纪律(票面 2026-10-14 经营数据增量;与 dashboard.go 的 PR#7 层互补,零改动):
//   - 漏斗计数一律是去重后的真实事实:参与级按 participation 唯一,权益按
//     tenant+participation+benefit_slot 唯一,事件按 event/fact_id 唯一;
//   - 三类核验证据是不同核验方式,绝不相加成"发布总人数";总体覆盖=
//     distinct 参与数;官方发布单独按可信尝试(attempt)计数;
//   - 库存面板照抄 1671 账本占用口径(available = total − occupied,
//     occupied = reserved+issued+redeemed),绝不另建"统计库存";
//   - 成本三分:内容实际生成费用 / 奖励实际兑现成本 / 券面额;未知标"未录入",
//     绝不用面额算支出;
//   - source=nfc 只是入口标记,不是实测物理碰触;匿名参与不冒充自然人数——
//     两条注记是常量,响应必带;
//   - 筛选只对有该维度事实的级生效,不适用级照实披露(applied_filters),
//     绝不假装过滤;
//   - 本包保持纯函数:同输入→同输出;事实由 store 层只读 SQL 提供。
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Funnel stage keys, fixed order (票面漏斗七级;第 1 级复用 PR#7 的触发事实).
const (
	FunnelVisits           = "funnel_visits"
	FunnelStarted          = "funnel_started"
	FunnelContentSelected  = "funnel_content_selected"
	FunnelHandoffInitiated = "funnel_handoff_initiated"
	FunnelPublishVerified  = "funnel_publish_verified"
	FunnelRewardsIssued    = "funnel_rewards_issued"
	FunnelRedeemed         = "funnel_redeemed"
)

// Evidence classes (mirror rewardops constants by value; the dashboard never
// imports the decision kernel, it only reads the ledger rows it produced).
const (
	EvidenceStaffObserved = "staff_observed_submission"
	EvidenceStaffVerified = "staff_verified_post"
	EvidenceOfficial      = "official_publish_confirmed"
)

// Six evidence layers (TOUCH-ALL-20261109-v1: 点击/唤起尝试/媒体交接/顾客自报/
// 店员确认/平台证实 分开计数). Each layer lists its own named count lines; the
// layers are different verification stages and are never summed into a
// "natural person" total.
const (
	LayerActionClicked    = "action_clicked"     // L1 点击
	LayerInvokeAttempt    = "invoke_attempt"     // L2 唤起尝试
	LayerHandoffMedia     = "handoff_media"      // L3 媒体交接
	LayerUserReported     = "user_reported"      // L4 顾客自报
	LayerStaffConfirmed   = "staff_confirmed"    // L5 店员确认
	LayerPlatformVerified = "platform_confirmed" // L6 平台证实
)

// FilterSet is the customer-facing slice. Empty strings mean "no filter".
// Values are validated here so the store layer only ever sees whitelisted
// tokens; ids (campaign/store/action) are tenant-scoped again in SQL
// (fail-closed).
type FilterSet struct {
	CampaignID         string
	StoreID            string
	ContentPackVersion string
	Touchpoint         string // nfc/qr/shorturl/manual
	Platform           string
	HandoffMethod      string // h5/platform_miniprogram/staff_view
	ActionID           string // 4584 门店动作实例 id(不透明 sa_*/sv_*),SQL 内再收窄
	Environment        string // 3535 六环境同口径
}

// HasAny reports whether any filter was requested (the response echoes
// requested filters; applied_filters stays empty when none were).
func (f FilterSet) HasAny() bool {
	return f.CampaignID != "" || f.StoreID != "" || f.ContentPackVersion != "" ||
		f.Touchpoint != "" || f.Platform != "" || f.HandoffMethod != "" ||
		f.ActionID != "" || f.Environment != ""
}

var validTouchpoints = map[string]bool{"nfc": true, "qr": true, "shorturl": true, "manual": true}
var validHandoffMethods = map[string]bool{"h5": true, "platform_miniprogram": true, "staff_view": true}

// validEnvironments: the six environments of the 3535 capability contract —
// the only vocabulary the event producers accept, so it is the only one the
// dashboard filters accept. Unknown values are refused (400 upstream) instead
// of silently matching nothing.
var validEnvironments = map[string]bool{
	"desktop_browser": true,
	"ios_safari":      true,
	"android_browser": true,
	"ios_wechat":      true,
	"android_wechat":  true,
	"target_app":      true,
}

// ParseFilterSet validates the raw query parameters. Unknown touchpoints or
// handoff methods are refused (400 upstream) instead of silently matching
// nothing — a silent empty slice would look like "true zero".
func ParseFilterSet(campaignID, storeID, contentPackVersion, touchpoint, platform, handoffMethod string) (FilterSet, error) {
	f := FilterSet{
		CampaignID:         trimOrNull(campaignID),
		StoreID:            trimOrNull(storeID),
		ContentPackVersion: trimOrNull(contentPackVersion),
		Touchpoint:         trimOrNull(touchpoint),
		Platform:           trimOrNull(platform),
		HandoffMethod:      trimOrNull(handoffMethod),
	}
	if f.Touchpoint != "" && !validTouchpoints[f.Touchpoint] {
		return FilterSet{}, fmt.Errorf("dashboard: unknown touchpoint %q (want nfc/qr/shorturl/manual)", f.Touchpoint)
	}
	if f.HandoffMethod != "" && !validHandoffMethods[f.HandoffMethod] {
		return FilterSet{}, fmt.Errorf("dashboard: unknown handoff_method %q (want h5/platform_miniprogram/staff_view)", f.HandoffMethod)
	}
	return f, nil
}

// ParseFilterSetExt is the v1-round parser: it takes the two new dimensions
// (action_id, environment) on top of the base set. Base validation is reused
// verbatim so the old query contract cannot drift.
func ParseFilterSetExt(campaignID, storeID, contentPackVersion, touchpoint, platform, handoffMethod, actionID, environment string) (FilterSet, error) {
	f, err := ParseFilterSet(campaignID, storeID, contentPackVersion, touchpoint, platform, handoffMethod)
	if err != nil {
		return FilterSet{}, err
	}
	f.ActionID = trimOrNull(actionID)
	f.Environment = trimOrNull(environment)
	if f.Environment != "" && !validEnvironments[f.Environment] {
		return FilterSet{}, fmt.Errorf("dashboard: unknown environment %q (want desktop_browser/ios_safari/android_browser/ios_wechat/android_wechat/target_app)", f.Environment)
	}
	return f, nil
}

func trimOrNull(s string) string { return strings.TrimSpace(s) }

// OpsFacts is the store-computed fact set for the operations dashboard. Zero
// values are TRUE zeros over the scope/window — never UNKNOWN.
type OpsFacts struct {
	// 漏斗第 1 级:访问(与 PR#7 触发同源的日粒度 beacon 事实)。
	VisitTotal int64

	// 漏斗第 2-4 级:distinct 参与数(各自事件时间落窗)。
	StartedTotal          int64
	ContentSelectedTotal  int64
	HandoffInitiatedTotal int64

	// 三类核验证据(granted 权益行 evidence 列)+ distinct 参与覆盖 +
	// 官方确认(按可信尝试去重)。
	EvidenceByClass                map[string]int64
	EvidenceDistinctParticipations int64
	OfficialConfirmedAttempts      int64

	// 六层证据(TOUCH-ALL-20261109-v1):各自来源与去重键独立,层与层之间、
	// 行与行之间都不相加;分类披露见 BuildOperations 的 evidence_layers。
	LayerActionSessions   int64 // 3546 action_session_events kind='created'(distinct session)
	LayerServiceOpens     int64 // 4589 store_service_events kind='open'(逐事件行)
	LayerJumpAttempts     int64 // 4589 store_service_events kind='jump_attempt'(逐事件行)
	LayerEditorProved     int64 // 1670 publish_handoff_events kind='editor_open_proved'(distinct handoff)
	LayerHandoffRequested int64 // 1670 publish_handoffs 创建行(idempotency_key UNIQUE)
	LayerHandoffCompleted int64 // 1670 当前状态蕴含 handoff_ready 已达的 handoff 数
	LayerUserReported     int64 // 顾客自报 attempt(self_reported=1,distinct attempt)
	LayerStaffSaw         int64 // publish_handoff_events kind='staff_saw_submit'(distinct handoff)
	LayerStaffVerified    int64 // publish_handoff_events kind='staff_verified_work'(distinct handoff)
	LayerPlatformHandoffs int64 // publish_handoffs status='platform_confirmed'(distinct handoff)
	LayerOfficialEvents   int64 // publish_handoff_events kind='official_confirmed'(distinct handoff)

	// 线索口径(4594):本地提交/待同步/接收确认/撤销分列;待同步不算已获客。
	Leads LeadsFacts

	// 费用对账(4591 投影只读):订阅实付/AI 实际费用/硬件分明细;既有 Cost 块
	// 继续承载权益兑现成本与内容生成费,两块互补不重复。
	CostRecon CostReconciliationFacts

	// 漏斗第 6/7 级 + 成本(granted=issued+redeemed;面额与成本分列)。
	IssuedTotal       int64
	IssuedFaceMinor   int64
	IssuedCostMinor   int64
	RedeemedTotal     int64
	RedeemedCostMinor int64

	Inventory []InventoryFactLine
	Anomalies AnomalyFacts
	Cost      CostFacts

	// Dims carries the scope-narrowed reference rows (来源引用贯穿).
	Dims store.DashboardDimensions
}

// LeadsFacts is the lead-truth panel fact set. Every bucket is a count of
// REAL lead_submissions rows; there is no bucket for browsing or WeChat
// clicks — those are not leads.
type LeadsFacts struct {
	CampaignSubmitted int64 // 窗口内 capture_source='campaign' 的提交行
	StoreSubmitted    int64 // 窗口内 capture_source='store' 的提交行(4594 无活动留资)
	PendingSync       int64 // sync_state='pending_sync'(Notify 已投递、无 ACK)
	ReceiverConfirmed int64 // sync_state='crm_received' AND receiver_state='stored'(ACK 五元组)
	Revoked           int64 // 窗口内提交、当前已撤销(revoked 或水位>0)
	// ReceiverDeclaredRevoked: 接收方申报 revoked 的 ACK 行数(接收方申报事实,
	// 只记录不发明本地状态,4594 语义)。
	ReceiverDeclaredRevoked int64
	UniqueContacts          int64 // COUNT(DISTINCT phone) 窗口内提交
}

// CostReconciliationFacts: 4591 投影只读。指针 = 窗口内没有该类支付事实
// (未录入,不冒充 0);退款是分列的对账事实,绝不冲销改写原订单金额。
type CostReconciliationFacts struct {
	SubscriptionPaidMinor    *int64 // kind='subscription' status='active' 的 SUM(amount_minor)
	SubscriptionPaidOrders   int64
	SubscriptionFailedOrders int64 // failed 订单数(失败不是支出,单列)
	AIConfirmedMinor         int64 // state='confirmed'(已确认未消费)
	AIConfirmedCount         int64
	AIConsumedMinor          int64 // state='consumed'(已消费,已发生)
	AIConsumedCount          int64
	AIRefundedMinor          int64 // state='refunded'(单列,不计支出)
	AIRefundedCount          int64
	AIFailedMinor            int64 // state='failed'(单列,不计支出)
	AIFailedCount            int64
	HardwarePaidMinor        *int64 // kind='hardware' status='active' 的 SUM(amount_minor)
	HardwarePaidOrders       int64
	HardwareRefundMinor      int64 // billing_refunds SUM(分列对账)
	HardwareRefunds          int64
}

// LayerCount is one named count line inside an evidence layer. Each line has
// its own dedup scope stated in the layer definition; lines are never summed.
type LayerCount struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

// EvidenceLayer is one of the six verification layers.
type EvidenceLayer struct {
	Key            string       `json:"key"`
	Title          string       `json:"title"`
	Available      bool         `json:"available"`
	Counts         []LayerCount `json:"counts"`
	Definition     Definition   `json:"definition"`
	AppliedFilters []string     `json:"applied_filters,omitempty"`
	Note           string       `json:"note,omitempty"`
}

// EvidenceLayersPanel carries the six layers side by side.
type EvidenceLayersPanel struct {
	Layers []EvidenceLayer `json:"layers"`
	Note   string          `json:"note"`
}

// LeadsPanel is the lead-truth block: 本地提交两分、待同步/接收确认/撤销分列,
// 待同步不算已获客(常量注记必带)。
type LeadsPanel struct {
	CampaignSubmitted       int64      `json:"campaign_submitted"`
	StoreSubmitted          int64      `json:"store_submitted"`
	PendingSync             int64      `json:"pending_sync"`
	ReceiverConfirmed       int64      `json:"receiver_confirmed"`
	Revoked                 int64      `json:"revoked"`
	ReceiverDeclaredRevoked int64      `json:"receiver_declared_revoked"`
	UniqueContacts          int64      `json:"unique_contacts"`
	Note                    string     `json:"note"`
	Definition              Definition `json:"definition"`
}

// CostReconciliation is the read-only 4591 projection block. Subscriptions and
// hardware carry pointer amounts: nil = no payment facts in the window (kept
// unknown, never a fabricated 0). Refunds are listed apart, never netted.
type CostReconciliation struct {
	SubscriptionAvailable    bool       `json:"subscription_available"`
	SubscriptionPaidMinor    *int64     `json:"subscription_paid_minor,omitempty"`
	SubscriptionPaidOrders   int64      `json:"subscription_paid_orders"`
	SubscriptionFailedOrders int64      `json:"subscription_failed_orders"`
	SubscriptionNote         string     `json:"subscription_note,omitempty"`
	AIConfirmedMinor         int64      `json:"ai_confirmed_minor"`
	AIConfirmedCount         int64      `json:"ai_confirmed_count"`
	AIConsumedMinor          int64      `json:"ai_consumed_minor"`
	AIConsumedCount          int64      `json:"ai_consumed_count"`
	AIRefundedMinor          int64      `json:"ai_refunded_minor"`
	AIRefundedCount          int64      `json:"ai_refunded_count"`
	AIFailedMinor            int64      `json:"ai_failed_minor"`
	AIFailedCount            int64      `json:"ai_failed_count"`
	HardwareAvailable        bool       `json:"hardware_available"`
	HardwarePaidMinor        *int64     `json:"hardware_paid_minor,omitempty"`
	HardwarePaidOrders       int64      `json:"hardware_paid_orders"`
	HardwareRefundMinor      int64      `json:"hardware_refund_minor"`
	HardwareRefunds          int64      `json:"hardware_refunds"`
	Definition               Definition `json:"definition"`
	FaceNote                 string     `json:"face_note"`
}

// InventoryFactLine is one reward_inventory batch with its benefit counts.
// FaceValueMinor/CostMinor are the batch's per-unit snapshot values (the
// benefit rows copy them at allocation; the panel never re-derives them).
type InventoryFactLine struct {
	InventoryID    string
	CampaignID     string
	StoreID        string // "" = 租户级批次(全店共享)
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

// AnomalyFacts: pending review 与 failed 是当前态队列;reversed/expired 取
// append-only reward_operations 流水按窗口计数(可重算,不抹历史)。
type AnomalyFacts struct {
	PendingReview     int64
	ReservationFailed int64
	ReversedInWindow  int64
	ExpiredInWindow   int64
}

// CostFacts: content generation 为指针——nil = 未录入(不冒充 0)。
type CostFacts struct {
	ContentGenerationAmountMinor *int64
	ContentGenerationBilledRuns  int64
	RewardCostMinor              int64
	RewardRedeemedCostMinor      int64
	RewardFaceMinor              int64
	CostUnrecordedBatches        int64
}

// FunnelStage is one funnel level. AppliedFilters names the requested filter
// keys this stage could honestly honor (empty when nothing was requested).
type FunnelStage struct {
	Key            string     `json:"key"`
	Title          string     `json:"title"`
	Available      bool       `json:"available"`
	Value          int64      `json:"value"`
	Definition     Definition `json:"definition"`
	AppliedFilters []string   `json:"applied_filters,omitempty"`
	Note           string     `json:"note,omitempty"`
}

// EvidencePanel lists the three verification classes side by side. ByClass is
// keyed by the evidence constants; the note forbids summing them.
type EvidencePanel struct {
	ByClass                   map[string]int64 `json:"by_class"`
	DistinctParticipations    int64            `json:"distinct_participations"`
	OfficialConfirmedAttempts int64            `json:"official_attempts"`
	Note                      string           `json:"note"`
}

// InventoryLine is the read-only projection of one ledger batch. Available
// mirrors the ledger's own occupancy check so the panel can never disagree
// with issuance.
type InventoryLine struct {
	InventoryID    string `json:"inventory_id"`
	CampaignID     string `json:"campaign_id"`
	StoreID        string `json:"store_id,omitempty"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	BenefitSlot    string `json:"benefit_slot"`
	RuleVersion    int    `json:"rule_version"`
	Currency       string `json:"currency,omitempty"`
	TotalQty       int64  `json:"total_qty"`
	Reserved       int64  `json:"reserved"`
	Issued         int64  `json:"issued"`
	Redeemed       int64  `json:"redeemed"`
	Available      int64  `json:"available"`
	Reversed       int64  `json:"reversed"`
	Expired        int64  `json:"expired"`
	FaceValueMinor int64  `json:"face_value_minor"`
	CostMinor      int64  `json:"cost_minor"`
}

// CostPanel keeps the three cost numbers apart. Face carries the fixed note
// that face value is marketing face, never incurred spend.
type CostPanel struct {
	ContentGenerationAvailable   bool   `json:"content_generation_available"`
	ContentGenerationAmountMinor *int64 `json:"content_generation_amount_minor,omitempty"`
	ContentGenerationRuns        int64  `json:"content_generation_runs"`
	ContentGenerationNote        string `json:"content_generation_note,omitempty"`
	RewardCostMinor              int64  `json:"reward_cost_minor"`
	RewardRedeemedCostMinor      int64  `json:"reward_redeemed_cost_minor"`
	RewardFaceMinor              int64  `json:"reward_face_minor"`
	UnrecordedBatches            int64  `json:"unrecorded_batches"`
	FaceNote                     string `json:"face_note"`
}

// AnomalyPanel is the 待处理 block.
type AnomalyPanel struct {
	PendingReview     int64 `json:"pending_review"`
	ReservationFailed int64 `json:"reservation_failed"`
	ReversedInWindow  int64 `json:"reversed_in_window"`
	ExpiredInWindow   int64 `json:"expired_in_window"`
}

// OperationsNotes carries the two fixed honesty notes.
type OperationsNotes struct {
	EntryMarker string `json:"entry_marker"`
	Visitor     string `json:"visitor"`
}

// OperationsResponse is the GET /api/v1/dashboard/operations payload.
type OperationsResponse struct {
	Window struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"window"`
	Scope struct {
		TenantID   string `json:"tenant_id"`
		StoreScope string `json:"store_scope,omitempty"`
	} `json:"scope"`
	FilterEcho map[string]string `json:"filter_echo"`
	Funnel     []FunnelStage     `json:"funnel"`
	Evidence   EvidencePanel     `json:"evidence"`
	// EvidenceLayers (TOUCH-ALL-20261109-v1): the six verification layers,
	// each with its own named count lines and provenance. Additive field —
	// the legacy Evidence panel above stays byte-compatible.
	EvidenceLayers EvidenceLayersPanel `json:"evidence_layers"`
	// Leads (4594 receiver_state 口径): additive field.
	Leads     LeadsPanel      `json:"leads"`
	Inventory []InventoryLine `json:"inventory"`
	Cost      CostPanel       `json:"cost"`
	// CostReconciliation (4591 投影只读): additive field.
	CostReconciliation CostReconciliation `json:"cost_reconciliation"`
	Anomalies          AnomalyPanel       `json:"anomalies"`
	Notes              OperationsNotes    `json:"notes"`
	// Dimensions carries the reference rows the breakdown keys point into
	// (来源引用贯穿,同 PR#7 惯例), already narrowed to the caller's scope.
	Dimensions store.DashboardDimensions `json:"dimensions"`
}

const (
	entryMarkerNote = "touchpoint/source=nfc 只是入口标记(复制链接同样能访问),不是实测物理碰触;" +
		"访问级按页面显式上报的渠道统计,不从 URL 推算入口"
	visitorNote  = "参与/访客计数基于首方随机续接键,匿名设备与参与数不冒充真实独立自然人数"
	evidenceNote = "三类核验证据是不同核验方式,同一参与可能同时具备多种证据,不能相加成" +
		"「发布总人数」;总体覆盖按 distinct 参与数,官方发布单独按可信尝试计数"
	faceNote               = "券面额是营销面值,不是已发生支出;实际支出只看成本列,未知成本标未录入"
	contentCostUnknownNote = "内容实际生成费用未录入:本窗口内没有已结算(billed)的生成记录," +
		"在途预扣(hold)不算支出,不以报价推算"
)

// funnelTitles fixes the merchant-facing wording of the seven levels.
var funnelTitles = map[string]string{
	FunnelVisits:           "访问",
	FunnelStarted:          "明确参与",
	FunnelContentSelected:  "内容选择",
	FunnelHandoffInitiated: "发起平台交接",
	FunnelPublishVerified:  "可证实的提交/发布",
	FunnelRewardsIssued:    "奖励发放",
	FunnelRedeemed:         "实际核销/现场兑现",
}

// filter labels used in filter_echo.
var filterLabels = []struct{ key, label string }{
	{"campaign_id", "活动"},
	{"store_id", "门店"},
	{"content_pack_version", "内容包/采用版本"},
	{"touchpoint", "触点"},
	{"platform", "平台"},
	{"handoff_method", "承接方式"},
	{"action_id", "动作"},
	{"environment", "环境"},
}

// layerTitles fixes the merchant-facing wording of the six layers.
var layerTitles = map[string]string{
	LayerActionClicked:    "点击",
	LayerInvokeAttempt:    "唤起尝试",
	LayerHandoffMedia:     "媒体交接",
	LayerUserReported:     "顾客自报",
	LayerStaffConfirmed:   "店员确认",
	LayerPlatformVerified: "平台证实",
}

const layersNote = "六层是不同的核验阶段,各行的去重口径不同,层与层之间、行与行之间都不相加成「人数」;" +
	"页面隐藏、下载开始、测试回执都不升级为平台成功;source=nfc 只是入口标记不是物理到店证明"

// layerDefs fixes each layer's provenance block (口径披露惯例同漏斗各级)。
func layerDefs() map[string]Definition {
	return map[string]Definition{
		LayerActionClicked: {
			Source: "action_session_events kind='created'(3546 动作页会话创建)+ store_service_events kind='open'" +
				"(4589 卡片打开;service_id 兼收 sa_* 动作实例与 sv_* 服务行)",
			DedupKey:    "会话行按 distinct session(source+event_id UNIQUE);卡片打开逐事件行计数(限流入口)",
			Denominator: "distinct 动作会话 / 事件行;两者是不同事实,分列不相加",
			EventTime:   "occurred_at(事件发生时刻,UTC)",
			WindowNote:  windowNoteSecond,
		},
		LayerInvokeAttempt: {
			Source: "store_service_events kind='jump_attempt'(4589 跳转/唤起尝试)+ publish_handoff_events " +
				"kind='editor_open_proved'(1670 可证实的编辑器打开)",
			DedupKey: "尝试逐事件行;可证实唤起按 distinct handoff_id。可证实唤起比尝试更强," +
				"按名披露不冒充尝试,也不相加",
			Denominator: "事件行 / distinct handoff;分列",
			EventTime:   "occurred_at / created_at(UTC)",
			WindowNote:  windowNoteSecond + ";无官方回执的「已唤起成功」结构上不存在",
		},
		LayerHandoffMedia: {
			Source: "publish_handoffs(1670 交接权威行):requested=创建行(idempotency_key UNIQUE);" +
				"completed=当前状态 ∈ {handoff_ready,editor_opened,submitting,awaiting_review,unknown,platform_confirmed}" +
				"(状态机只前进,六态蕴含 handoff_ready 已达;状态机定义见 publishhandoff.transitions)",
			DedupKey:    "distinct handoff(UNIQUE(tenant_id,idempotency_key):重放回原行)",
			Denominator: "distinct handoff;completed 是 requested 的子集(同一批行内的状态事实)",
			EventTime:   "publish_handoffs.created_at(交接发起时刻,UTC)",
			WindowNote:  windowNoteSecond + ";completed 按窗口内发起的行在当前时点的状态披露(当前态,可随推进变化)",
		},
		LayerUserReported: {
			Source:      "customer_publish_attempts self_reported=1(顾客自报提交;自报不是发布,只进入待确认队列)",
			DedupKey:    "distinct attempt(尝试行);同一参与多次自报多次计数,不冒充自然人数",
			Denominator: "distinct attempt",
			EventTime:   "customer_publish_attempts.created_at(UTC)",
			WindowNote:  windowNoteSecond,
		},
		LayerStaffConfirmed: {
			Source: "publish_handoff_events kind='staff_saw_submit'/'staff_verified_work'(distinct handoff)" +
				"+ reward_benefits evidence='staff_observed_submission'/'staff_verified_post'(distinct participation,见 evidence 区块)",
			DedupKey:    "事件流 distinct handoff_id;权益行 distinct participation。两个分母不同,分列不相加",
			Denominator: "distinct handoff / distinct participation",
			EventTime:   "publish_handoff_events.created_at / reward_benefits.issued_at(UTC)",
			WindowNote:  windowNoteSecond,
		},
		LayerPlatformVerified: {
			Source: "publish_handoffs status='platform_confirmed'(仅验签回执可达)" +
				"+ publish_handoff_events kind='official_confirmed'(UNIQUE(tenant,receipt_id))" +
				"+ reward_official_facts(distinct attempt,官方查询回执,见 evidence 区块)",
			DedupKey:    "distinct handoff / distinct attempt;平台证实是唯一「已发布」语义",
			Denominator: "distinct handoff / distinct attempt;分列",
			EventTime:   "publish_handoff_events.created_at / reward_official_facts.created_at(UTC)",
			WindowNote:  windowNoteSecond + ";人工先发、官方晚到只补证据,不重复计数",
		},
	}
}

// layersAppliedFor: which requested filters a layer can honestly honor.
// Session/service facts carry action_id + environment; campaign-domain
// publish facts carry platform but not environment/action.
func layersAppliedFor(layer string, f FilterSet) []string {
	out := []string{}
	add := func(cond bool, key string) {
		if cond {
			out = append(out, key)
		}
	}
	switch layer {
	case LayerActionClicked, LayerInvokeAttempt:
		add(f.StoreID != "", "store_id")
		add(f.ActionID != "", "action_id")
		add(f.Environment != "", "environment")
	case LayerHandoffMedia, LayerUserReported, LayerStaffConfirmed, LayerPlatformVerified:
		add(f.CampaignID != "", "campaign_id")
		add(f.StoreID != "", "store_id")
		add(f.Platform != "", "platform")
	}
	return out
}

// appliedFor returns the filter keys a stage can honestly honor, given the
// requested set. The join-path facts are fixed here (and worded in each
// stage's definition): visits only link campaign/store/touchpoint(channel);
// participations link campaign/store/touchpoint/content_pack_version; frozen
// attempts additionally carry platform/handoff_method.
func appliedFor(stage string, f FilterSet) []string {
	out := []string{}
	add := func(cond bool, key string) {
		if cond {
			out = append(out, key)
		}
	}
	basic := func() {
		add(f.CampaignID != "", "campaign_id")
		add(f.StoreID != "", "store_id")
	}
	participationLevel := func() {
		basic()
		add(f.Touchpoint != "", "touchpoint")
		add(f.ContentPackVersion != "", "content_pack_version")
	}
	switch stage {
	case FunnelVisits:
		basic()
		add(f.Touchpoint != "", "touchpoint")
	case FunnelStarted, FunnelHandoffInitiated:
		participationLevel()
	case FunnelContentSelected:
		participationLevel()
		add(f.Platform != "", "platform")
		add(f.HandoffMethod != "", "handoff_method")
	case FunnelPublishVerified:
		// 证据走权益行(参与维度);官方事实另有 platform 列。
		participationLevel()
		add(f.Platform != "", "platform_official")
	case FunnelRewardsIssued, FunnelRedeemed:
		participationLevel()
	}
	return out
}

// funnelDefs fixes each level's provenance block (口径披露惯例同 PR#7).
func funnelDefs() map[string]Definition {
	return map[string]Definition{
		FunnelVisits: {
			Source:      "public_view_stats(匿名浏览 beacon)JOIN campaign_links→campaigns(仅本租户自有短码)",
			DedupKey:    "无幂等键:beacon 到达即 +1(H5 每次页面加载发一次);touchpoint 筛选映射页面显式上报的 channel",
			Denominator: "作用域内自有活动短码上的全部匿名浏览 beacon;按 (短码,日,渠道) 聚合",
			EventTime:   "public_view_stats.day(beacon 到达日,UTC)",
			WindowNote:  windowNoteDay,
		},
		FunnelStarted: {
			Source:      "participations(3546 参与权威行:只有顾客显式开始才有行,浏览不建行)",
			DedupKey:    "UNIQUE(tenant_id, campaign_id, idempotency_key):重放回原行,绝不产生第二条参与事实",
			Denominator: "distinct participation;访客为首方随机续接键,不代表自然人数",
			EventTime:   "participations.created_at(顾客显式开始时刻,UTC)",
			WindowNote:  windowNoteSecond,
		},
		FunnelContentSelected: {
			Source:      "participation_attempts.content_pack_version(交接时冻结在 attempt 行,append-only)",
			DedupKey:    "distinct participation:同参与多次换稿/换平台只计一次,取最早一次冻结时刻落窗",
			Denominator: "distinct participation(至少有一条冻结了内容版本的尝试)",
			EventTime:   "MIN(participation_attempts.created_at where content_pack_version<>'')",
			WindowNote:  windowNoteSecond + ";晚到的冻结行按其自身事件时间落窗",
		},
		FunnelHandoffInitiated: {
			Source:      "handoff_credentials(3546 受控交接凭据铸发行;弱网重铸多枚凭据按参与去重)",
			DedupKey:    "distinct participation(token_hash 唯一,重铸不重复计);取最早铸发时刻落窗",
			Denominator: "distinct participation(至少铸发过一枚交接凭据)",
			EventTime:   "MIN(handoff_credentials.created_at)",
			WindowNote:  windowNoteSecond,
		},
		FunnelPublishVerified: {
			Source: "reward_benefits.evidence(1671 确认时写入,永不改写)+ reward_official_facts" +
				"(官方查询回执,receipt_source=official_query);预览/导出/自报永不算发布",
			DedupKey:    "证据按 distinct participation;官方按 distinct attempt(UNIQUE(tenant,campaign,attempt_id))",
			Denominator: "总体覆盖=distinct 参与数;三类证据单列,不能相加(见 evidence.note)",
			EventTime:   "证据=reward_benefits.issued_at(确认时刻);官方=reward_official_facts.created_at",
			WindowNote:  windowNoteSecond + ";人工先发、官方晚到只补证据,不增加可证实参与覆盖",
		},
		FunnelRewardsIssued: {
			Source:      "reward_benefits status IN (issued, redeemed)(1671 账本:确认即发放)",
			DedupKey:    "UNIQUE(tenant_id, participation_id, benefit_slot):一份奖励的数据库级保证",
			Denominator: "distinct 权益行(=distinct 参与+槽位)",
			EventTime:   "reward_benefits.issued_at",
			WindowNote:  windowNoteSecond + ";撤销(reversed)不计正向、单独披露",
		},
		FunnelRedeemed: {
			Source:      "reward_benefits status='redeemed'(1671 单事务原子核销)",
			DedupKey:    "UNIQUE(tenant_id, participation_id, benefit_slot);两店员竞争恰好一份,后到者读回已核销",
			Denominator: "distinct 权益行(已核销)",
			EventTime:   "reward_benefits.redeemed_at",
			WindowNote:  windowNoteSecond + ";更正(reversed)与迟到回执经 append-only 流水可重算,不抹历史",
		},
	}
}

// BuildOperations assembles the operations dashboard. Pure function of
// (window, scope, facts, filters): same inputs → byte-identical output.
func BuildOperations(w Window, sc Scope, f OpsFacts, filters FilterSet) OperationsResponse {
	var resp OperationsResponse
	resp.Window.Start, resp.Window.End = w.Start.Format(time.RFC3339), w.End.Format(time.RFC3339)
	resp.Scope.TenantID, resp.Scope.StoreScope = sc.TenantID, sc.StoreScope
	if filters.HasAny() {
		resp.FilterEcho = map[string]string{}
		for _, fl := range filterLabels {
			v := mapGet(filters, fl.key)
			if v != "" {
				resp.FilterEcho[fl.key] = v
			}
		}
	}

	defs := funnelDefs()
	// 第 1 级值与第 6/7 级的可用性:访问事实由 store 提供;证据/官方缺 map 时按零处理。
	evidence := f.EvidenceByClass
	if evidence == nil {
		evidence = map[string]int64{}
	}
	values := map[string]int64{
		FunnelVisits:           f.VisitTotal,
		FunnelStarted:          f.StartedTotal,
		FunnelContentSelected:  f.ContentSelectedTotal,
		FunnelHandoffInitiated: f.HandoffInitiatedTotal,
		FunnelPublishVerified:  f.EvidenceDistinctParticipations,
		FunnelRewardsIssued:    f.IssuedTotal,
		FunnelRedeemed:         f.RedeemedTotal,
	}
	for _, key := range []string{
		FunnelVisits, FunnelStarted, FunnelContentSelected, FunnelHandoffInitiated,
		FunnelPublishVerified, FunnelRewardsIssued, FunnelRedeemed,
	} {
		st := FunnelStage{
			Key: key, Title: funnelTitles[key], Available: true,
			Value: values[key], Definition: defs[key],
			AppliedFilters: appliedFor(key, filters),
		}
		if key == FunnelVisits {
			st.Note = "第 1 级为入口标记口径,见 notes.entry_marker"
		}
		if key == FunnelPublishVerified {
			st.Note = "明细见 evidence 区块(三类证据单列,不相加)"
		}
		resp.Funnel = append(resp.Funnel, st)
	}

	resp.Evidence = EvidencePanel{
		ByClass: map[string]int64{
			EvidenceStaffObserved: evidence[EvidenceStaffObserved],
			EvidenceStaffVerified: evidence[EvidenceStaffVerified],
			EvidenceOfficial:      evidence[EvidenceOfficial],
		},
		DistinctParticipations:    f.EvidenceDistinctParticipations,
		OfficialConfirmedAttempts: f.OfficialConfirmedAttempts,
		Note:                      evidenceNote,
	}

	resp.EvidenceLayers = buildEvidenceLayers(f, filters)
	resp.Leads = buildLeads(f.Leads)
	resp.CostReconciliation = buildCostReconciliation(f.CostRecon)

	for _, line := range f.Inventory {
		resp.Inventory = append(resp.Inventory, InventoryLine{
			InventoryID: line.InventoryID, CampaignID: line.CampaignID, StoreID: line.StoreID,
			Name: line.Name, Kind: line.Kind, BenefitSlot: line.BenefitSlot,
			RuleVersion: line.RuleVersion, Currency: line.Currency,
			TotalQty: line.TotalQty,
			Reserved: line.Reserved, Issued: line.Issued, Redeemed: line.Redeemed,
			Available: line.TotalQty - line.Reserved - line.Issued - line.Redeemed,
			Reversed:  line.Reversed, Expired: line.Expired,
			FaceValueMinor: line.FaceValueMinor, CostMinor: line.CostMinor,
		})
	}

	resp.Cost = CostPanel{
		ContentGenerationAvailable:   f.Cost.ContentGenerationAmountMinor != nil,
		ContentGenerationAmountMinor: f.Cost.ContentGenerationAmountMinor,
		ContentGenerationRuns:        f.Cost.ContentGenerationBilledRuns,
		RewardCostMinor:              f.Cost.RewardCostMinor,
		RewardRedeemedCostMinor:      f.Cost.RewardRedeemedCostMinor,
		RewardFaceMinor:              f.Cost.RewardFaceMinor,
		UnrecordedBatches:            f.Cost.CostUnrecordedBatches,
		FaceNote:                     faceNote,
	}
	if !resp.Cost.ContentGenerationAvailable {
		resp.Cost.ContentGenerationNote = contentCostUnknownNote
	}

	resp.Anomalies = AnomalyPanel{
		PendingReview:     f.Anomalies.PendingReview,
		ReservationFailed: f.Anomalies.ReservationFailed,
		ReversedInWindow:  f.Anomalies.ReversedInWindow,
		ExpiredInWindow:   f.Anomalies.ExpiredInWindow,
	}
	resp.Notes = OperationsNotes{EntryMarker: entryMarkerNote, Visitor: visitorNote}
	resp.Dimensions = f.Dims
	return resp
}

func mapGet(f FilterSet, key string) string {
	switch key {
	case "campaign_id":
		return f.CampaignID
	case "store_id":
		return f.StoreID
	case "content_pack_version":
		return f.ContentPackVersion
	case "touchpoint":
		return f.Touchpoint
	case "platform":
		return f.Platform
	case "handoff_method":
		return f.HandoffMethod
	case "action_id":
		return f.ActionID
	case "environment":
		return f.Environment
	}
	return ""
}

// ---- six evidence layers / leads / cost reconciliation assembly (pure) -------------

// layerLine is one named count inside a layer.
func layerLine(key, label string, v int64) LayerCount {
	return LayerCount{Key: key, Label: label, Value: v}
}

// buildEvidenceLayers assembles the six-layer panel. Pure function.
func buildEvidenceLayers(f OpsFacts, filters FilterSet) EvidenceLayersPanel {
	defs := layerDefs()
	layers := []EvidenceLayer{
		{
			Key: LayerActionClicked, Title: layerTitles[LayerActionClicked], Available: true,
			Counts: []LayerCount{
				layerLine("action_sessions", "动作会话(distinct session)", f.LayerActionSessions),
				layerLine("service_opens", "卡片打开(逐事件行)", f.LayerServiceOpens),
			},
			Definition:     defs[LayerActionClicked],
			AppliedFilters: layersAppliedFor(LayerActionClicked, filters),
		},
		{
			Key: LayerInvokeAttempt, Title: layerTitles[LayerInvokeAttempt], Available: true,
			Counts: []LayerCount{
				layerLine("service_jump_attempts", "跳转/唤起尝试(逐事件行)", f.LayerJumpAttempts),
				layerLine("editor_open_proved", "可证实的编辑器打开(distinct handoff)", f.LayerEditorProved),
			},
			Definition:     defs[LayerInvokeAttempt],
			AppliedFilters: layersAppliedFor(LayerInvokeAttempt, filters),
			Note:           "可证实唤起是比尝试更强的证据,按名分列,不相加",
		},
		{
			Key: LayerHandoffMedia, Title: layerTitles[LayerHandoffMedia], Available: true,
			Counts: []LayerCount{
				layerLine("handoff_requested", "媒体交接发起", f.LayerHandoffRequested),
				layerLine("handoff_completed", "媒体交接完成(交接就绪已达)", f.LayerHandoffCompleted),
			},
			Definition:     defs[LayerHandoffMedia],
			AppliedFilters: layersAppliedFor(LayerHandoffMedia, filters),
		},
		{
			Key: LayerUserReported, Title: layerTitles[LayerUserReported], Available: true,
			Counts: []LayerCount{
				layerLine("user_reported", "顾客自报提交(distinct attempt)", f.LayerUserReported),
			},
			Definition:     defs[LayerUserReported],
			AppliedFilters: layersAppliedFor(LayerUserReported, filters),
			Note:           "自报不是发布:只进入待确认队列,确认归店员/平台层",
		},
		{
			Key: LayerStaffConfirmed, Title: layerTitles[LayerStaffConfirmed], Available: true,
			Counts: []LayerCount{
				layerLine("staff_saw_submit", "店员目测提交(distinct handoff)", f.LayerStaffSaw),
				layerLine("staff_verified_work", "店员核对作品(distinct handoff)", f.LayerStaffVerified),
				layerLine("staff_benefit_participations", "权益行店员确认(distinct participation)", countOr(evidenceMap(f), EvidenceStaffObserved)+countOr(evidenceMap(f), EvidenceStaffVerified)),
			},
			Definition:     defs[LayerStaffConfirmed],
			AppliedFilters: layersAppliedFor(LayerStaffConfirmed, filters),
		},
		{
			Key: LayerPlatformVerified, Title: layerTitles[LayerPlatformVerified], Available: true,
			Counts: []LayerCount{
				layerLine("platform_confirmed_handoffs", "平台确认交接(distinct handoff)", f.LayerPlatformHandoffs),
				layerLine("official_confirmed_events", "官方确认事件(distinct handoff)", f.LayerOfficialEvents),
				layerLine("official_attempt_facts", "官方查询回执(distinct attempt)", f.OfficialConfirmedAttempts),
			},
			Definition:     defs[LayerPlatformVerified],
			AppliedFilters: layersAppliedFor(LayerPlatformVerified, filters),
		},
	}
	return EvidenceLayersPanel{Layers: layers, Note: layersNote}
}

func evidenceMap(f OpsFacts) map[string]int64 {
	if f.EvidenceByClass == nil {
		return map[string]int64{}
	}
	return f.EvidenceByClass
}

func countOr(m map[string]int64, k string) int64 { return m[k] }

const leadsNote = "待同步不算已获客:Notify 投递成功只停在 pending_sync,只有受信 Leads 接收方的持久化 ACK" +
	"(receiver_state=stored 五元组)才计入已获客确认;已撤销线索不计正向;纯浏览与微信点击不在任何线索桶,不是线索"

// buildLeads assembles the lead-truth block. Pure function.
func buildLeads(f LeadsFacts) LeadsPanel {
	return LeadsPanel{
		CampaignSubmitted:       f.CampaignSubmitted,
		StoreSubmitted:          f.StoreSubmitted,
		PendingSync:             f.PendingSync,
		ReceiverConfirmed:       f.ReceiverConfirmed,
		Revoked:                 f.Revoked,
		ReceiverDeclaredRevoked: f.ReceiverDeclaredRevoked,
		UniqueContacts:          f.UniqueContacts,
		Note:                    leadsNote,
		Definition: Definition{
			Source:   "lead_submissions(授权留资权威行;campaign lane 与 4594 门店留资同一张表,capture_source 分列)",
			DedupKey: "dedup_key UNIQUE(同一联系人在同一活动/门店同一 UTC 日内幂等回原行);状态为当前态披露,可随 ACK 推进变化",
			Denominator: "unique_contacts=COUNT(DISTINCT phone)(D-X1 归一化指纹口径;指纹 HMAC 不出现在看板输出);" +
				"待同步(pending_sync)与接收确认(crm_received+receiver_state=stored)是两个独立桶,永不相加成已获客",
			EventTime:  "lead_submissions.created_at(=consent_at,提交时刻,UTC);撤销按当前态披露",
			WindowNote: windowNoteSecond,
		},
	}
}

const (
	subscriptionCostNote = "订阅实付是支付事实订单(active)的投影;failed/cancelled 不是支出,单列披露;" +
		"账单权威在派诺生态统一账单,本看板不维护余额,明细可回查 /work/billing"
	aiCostNote = "AI 实际费用按投影状态分列:consumed/confirmed 是已发生金额(confirmed=已确认未消费);" +
		"refunded/failed 单列,不计入支出;浏览/复制等不计费动作结构上进不了投影"
	hardwareCostNote = "硬件支出是支付事实订单(active)的投影;退款是分列的对账事实,不冲销不改写原订单金额"
	reconFaceNote    = "权益兑现成本与券面额在 cost 区块分列:面额是营销面值,不是已发生支出"
)

// buildCostReconciliation assembles the 4591 read-only reconciliation block.
// Pure function.
func buildCostReconciliation(f CostReconciliationFacts) CostReconciliation {
	return CostReconciliation{
		SubscriptionAvailable:    f.SubscriptionPaidMinor != nil,
		SubscriptionPaidMinor:    f.SubscriptionPaidMinor,
		SubscriptionPaidOrders:   f.SubscriptionPaidOrders,
		SubscriptionFailedOrders: f.SubscriptionFailedOrders,
		SubscriptionNote:         subscriptionCostNote,
		AIConfirmedMinor:         f.AIConfirmedMinor,
		AIConfirmedCount:         f.AIConfirmedCount,
		AIConsumedMinor:          f.AIConsumedMinor,
		AIConsumedCount:          f.AIConsumedCount,
		AIRefundedMinor:          f.AIRefundedMinor,
		AIRefundedCount:          f.AIRefundedCount,
		AIFailedMinor:            f.AIFailedMinor,
		AIFailedCount:            f.AIFailedCount,
		HardwareAvailable:        f.HardwarePaidMinor != nil,
		HardwarePaidMinor:        f.HardwarePaidMinor,
		HardwarePaidOrders:       f.HardwarePaidOrders,
		HardwareRefundMinor:      f.HardwareRefundMinor,
		HardwareRefunds:          f.HardwareRefunds,
		Definition: Definition{
			Source: "billing_orders/billing_ai_usage/billing_refunds(4591 投影表,受信权威缝写入;本看板只读)",
			DedupKey: "订单 UNIQUE(tenant,idempotency_key);AI 用量 UNIQUE(tenant,quote_ref);" +
				"退款 UNIQUE(tenant,source,event_id)——重复回调不重复计",
			Denominator: "窗口内按各自事件时间的真实投影行;支付金额=status='active' 订单的 amount_minor 投影",
			EventTime:   "订单/退款/用量行的 created_at(事实落投影时刻,UTC)",
			WindowNote:  windowNoteSecond + ";退款分列披露,不与支出对冲",
		},
		FaceNote: reconFaceNote,
	}
}

// ---- OperationsSummary: the fixed read-only port for HUI-2628 --------------------
//
// 契约(逐字固定,消费者按此先行测试;本票不改 TodayDesk 页面):
//   - as_of: 计算时刻(RFC3339 UTC);
//   - pending_confirm: 当前待确认数(自报未确认队列的当前态,无窗口);
//   - rewards_issued / rewards_redeemed: 「今天」=UTC 日历日 [00:00, as_of]
//     的发放/核销数(窗口口径由契约文本披露,绝不含糊);
//   - anomalies: 需要商家处理的量=预留失败(当前态)+窗口内撤销/过期流水。
//   - 零事实=真实 0(账本存在、作用域为空),不是 UNKNOWN。
type OperationsSummary struct {
	AsOf            string `json:"as_of"`
	PendingConfirm  int64  `json:"pending_confirm"`
	RewardsIssued   int64  `json:"rewards_issued"`
	RewardsRedeemed int64  `json:"rewards_redeemed"`
	Anomalies       int64  `json:"anomalies"`
	// Todos (TOUCH-ALL-20261109-v1, additive): the home-page to-do digest.
	// The five legacy fields above stay byte-stable for existing consumers.
	Todos TodoPanel `json:"todos"`
}

// TodoFacts is the store-computed fact set of the to-do digest (all
// current-state queues plus in-window ledger flows, scope-narrowed).
type TodoFacts struct {
	UnavailableActions  int64 // 停用/撤销/已过期动作(当前态)
	MissingContent      int64 // 启用且未绑定已采用内容的动作(当前态)
	ActionAuthExpired   int64 // 启用动作的 expires_at 已过(当前态)
	AdmissionExpired    int64 // 渠道准入 admitted 且 valid_until 已过(当前态)
	PendingRedeem       int64 // 已发放未核销权益(当前态)
	BillingFailedOrders int64 // failed 订单数(当前态;失败不是支出,是待处理)
}

// TodoPanel is the six-item home digest. AuthorizationExpired is the sum of
// its two sub-facts (both disclosed); every item's provenance is in
// Definitions.
type TodoPanel struct {
	UnavailableActions   int64             `json:"unavailable_actions"`
	MissingContent       int64             `json:"missing_content"`
	AuthorizationExpired int64             `json:"authorization_expired"`
	ActionAuthExpired    int64             `json:"action_auth_expired"`
	AdmissionExpired     int64             `json:"admission_expired"`
	PendingConfirm       int64             `json:"pending_confirm"`
	PendingRedeem        int64             `json:"pending_redeem"`
	BillingAnomalies     int64             `json:"billing_anomalies"`
	Definitions          map[string]string `json:"definitions"`
	Note                 string            `json:"note"`
}

// SummaryFacts is the store-computed fact set for the summary port.
type SummaryFacts struct {
	PendingConfirm  int64
	RewardsIssued   int64
	RewardsRedeemed int64
	Anomalies       int64
	Todos           TodoFacts
}

const todoNote = "待办是当前态队列(另有窗口流水在 anomalies),由首页渲染;统计只读,本票不改 TodayDesk;" +
	"每项口径见 definitions,零事实=真实 0 不是未知"

// BuildOperationsSummary assembles the fixed contract. Pure function.
func BuildOperationsSummary(now time.Time, f SummaryFacts) OperationsSummary {
	td := TodoPanel{
		UnavailableActions:   f.Todos.UnavailableActions,
		MissingContent:       f.Todos.MissingContent,
		ActionAuthExpired:    f.Todos.ActionAuthExpired,
		AdmissionExpired:     f.Todos.AdmissionExpired,
		AuthorizationExpired: f.Todos.ActionAuthExpired + f.Todos.AdmissionExpired,
		PendingConfirm:       f.PendingConfirm,
		PendingRedeem:        f.Todos.PendingRedeem,
		BillingAnomalies:     f.Todos.BillingFailedOrders,
		Note:                 todoNote,
		Definitions: map[string]string{
			"unavailable_actions":   "store_actions 当前 enabled=0 或 revoked=1(停用/撤销;expires_at 过期另计 action_auth_expired)",
			"missing_content":       "启用且未撤销未过期的动作 content_binding_ref=''(未绑定已采用内容)",
			"authorization_expired": "action_auth_expired(启用动作 expires_at 已过)+ admission_expired(渠道准入 admitted 且 valid_until 已过)",
			"pending_confirm":       "自报未确认尝试(与既有 pending_confirm 同口径,当前态)",
			"pending_redeem":        "reward_benefits status='issued'(已发放未核销,当前态)",
			"billing_anomalies":     "billing_orders status='failed' 的订单数(失败订单待处理,不是支出)",
		},
	}
	return OperationsSummary{
		AsOf:            now.UTC().Format(time.RFC3339),
		PendingConfirm:  f.PendingConfirm,
		RewardsIssued:   f.RewardsIssued,
		RewardsRedeemed: f.RewardsRedeemed,
		Anomalies:       f.Anomalies,
		Todos:           td,
	}
}
