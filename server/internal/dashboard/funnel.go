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

// FilterSet is the customer-facing slice. Empty strings mean "no filter".
// Values are validated here so the store layer only ever sees whitelisted
// tokens; ids (campaign/store) are tenant-scoped again in SQL (fail-closed).
type FilterSet struct {
	CampaignID         string
	StoreID            string
	ContentPackVersion string
	Touchpoint         string // nfc/qr/shorturl/manual
	Platform           string
	HandoffMethod      string // h5/platform_miniprogram/staff_view
}

// HasAny reports whether any filter was requested (the response echoes
// requested filters; applied_filters stays empty when none were).
func (f FilterSet) HasAny() bool {
	return f.CampaignID != "" || f.StoreID != "" || f.ContentPackVersion != "" ||
		f.Touchpoint != "" || f.Platform != "" || f.HandoffMethod != ""
}

var validTouchpoints = map[string]bool{"nfc": true, "qr": true, "shorturl": true, "manual": true}
var validHandoffMethods = map[string]bool{"h5": true, "platform_miniprogram": true, "staff_view": true}

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

	// 漏斗第 6/7 级 + 成本(granted=issued+redeemed;面额与成本分列)。
	IssuedTotal       int64
	IssuedFaceMinor   int64
	IssuedCostMinor   int64
	RedeemedTotal     int64
	RedeemedCostMinor int64

	Inventory []InventoryFactLine
	Anomalies AnomalyFacts
	Cost      CostFacts
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
	Inventory  []InventoryLine   `json:"inventory"`
	Cost       CostPanel         `json:"cost"`
	Anomalies  AnomalyPanel      `json:"anomalies"`
	Notes      OperationsNotes   `json:"notes"`
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
	}
	return ""
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
}

// SummaryFacts is the store-computed fact set for the summary port.
type SummaryFacts struct {
	PendingConfirm  int64
	RewardsIssued   int64
	RewardsRedeemed int64
	Anomalies       int64
}

// BuildOperationsSummary assembles the fixed contract. Pure function.
func BuildOperationsSummary(now time.Time, f SummaryFacts) OperationsSummary {
	return OperationsSummary{
		AsOf:            now.UTC().Format(time.RFC3339),
		PendingConfirm:  f.PendingConfirm,
		RewardsIssued:   f.RewardsIssued,
		RewardsRedeemed: f.RewardsRedeemed,
		Anomalies:       f.Anomalies,
	}
}
