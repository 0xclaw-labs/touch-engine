package dashboard

// funnel_test.go: HUI-1677 经营数据增量的纯函数层验收。
//
// 纪律(票面 2026-10-14 经营数据增量):
//   - 漏斗七级每级带 definition(source/dedup_key/denominator/event_time/window);
//   - 三类核验证据单列,绝不相加成"发布总人数";总体覆盖=distinct 参与数;
//     官方发布单独按可信尝试(attempt)计数;
//   - 库存面板照抄 1671 账本占用口径;成本三分,未知标"未录入";
//   - nfc 只是入口标记;匿名参与不冒充自然人数——两条注记是常量必在;
//   - 筛选只对有该维度事实的级生效,不适用级照实披露(applied_filters);
//   - 纯函数:同输入→同输出;OperationsSummary 契约字段名固定。
import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var funnelWindow = mustWindow(t_time())

func t_time() (time.Time, time.Time) {
	start, _ := time.Parse(time.RFC3339, "2026-10-14T00:00:00Z")
	end, _ := time.Parse(time.RFC3339, "2026-10-14T23:59:59Z")
	return start, end
}

func mustWindow(s, e time.Time) Window {
	w, err := ParseWindow(s.Format(time.RFC3339), e.Format(time.RFC3339))
	if err != nil {
		panic(err)
	}
	return w
}

// ---- 1. filter parsing ---------------------------------------------------------

func TestParseFilterSetAcceptsEmpty(t *testing.T) {
	f, err := ParseFilterSet("", "", "", "", "", "")
	if err != nil {
		t.Fatalf("empty filters: %v", err)
	}
	if f.HasAny() {
		t.Fatalf("empty filters must report HasAny=false")
	}
}

func TestParseFilterSetValidatesWhitelists(t *testing.T) {
	if _, err := ParseFilterSet("", "", "", "nfc", "", ""); err != nil {
		t.Fatalf("nfc touchpoint: %v", err)
	}
	if _, err := ParseFilterSet("", "", "", "carrier_pigeon", "", ""); err == nil {
		t.Fatalf("unknown touchpoint must be refused")
	}
	if _, err := ParseFilterSet("", "", "", "", "", "h5"); err != nil {
		t.Fatalf("h5 method: %v", err)
	}
	if _, err := ParseFilterSet("", "", "", "", "", "telepathy"); err == nil {
		t.Fatalf("unknown handoff method must be refused")
	}
}

// ---- 2. funnel shape: seven levels, every definition block filled --------------

func TestBuildOperationsFunnelShape(t *testing.T) {
	resp := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{VisitTotal: 12, StartedTotal: 5}, FilterSet{})
	if len(resp.Funnel) != 7 {
		t.Fatalf("funnel stages = %d, want 7", len(resp.Funnel))
	}
	wantOrder := []string{
		FunnelVisits, FunnelStarted, FunnelContentSelected, FunnelHandoffInitiated,
		FunnelPublishVerified, FunnelRewardsIssued, FunnelRedeemed,
	}
	for i, st := range resp.Funnel {
		if st.Key != wantOrder[i] {
			t.Fatalf("funnel[%d].key = %q, want %q", i, st.Key, wantOrder[i])
		}
		if !st.Available {
			t.Fatalf("%s: funnel stages are real touch-domain facts, must be available", st.Key)
		}
		d := st.Definition
		if d.Source == "" || d.DedupKey == "" || d.Denominator == "" || d.EventTime == "" || d.WindowNote == "" {
			t.Fatalf("%s: definition incomplete: %+v", st.Key, d)
		}
	}
	if resp.Funnel[0].Value != 12 || resp.Funnel[1].Value != 5 {
		t.Fatalf("visit/started values = %d/%d, want 12/5", resp.Funnel[0].Value, resp.Funnel[1].Value)
	}
}

// 同窗口重算幂等:同输入→同输出(深比较)。
func TestBuildOperationsIdempotent(t *testing.T) {
	facts := OpsFacts{VisitTotal: 3, StartedTotal: 2, IssuedTotal: 1,
		EvidenceByClass: map[string]int64{"staff_observed_submission": 1}}
	a := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, facts, FilterSet{CampaignID: "c1"})
	b := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, facts, FilterSet{CampaignID: "c1"})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("same inputs must produce identical output")
	}
}

// ---- 3. evidence: three classes listed, never additive --------------------------

func TestEvidencePanelListsThreeClassesSeparately(t *testing.T) {
	resp := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{
		EvidenceByClass: map[string]int64{
			"staff_observed_submission":  3,
			"staff_verified_post":        2,
			"official_publish_confirmed": 4,
		},
		EvidenceDistinctParticipations: 5, // 同一参与可同时具备多种证据
		OfficialConfirmedAttempts:      4,
	}, FilterSet{})
	ev := resp.Evidence
	if ev.ByClass["staff_observed_submission"] != 3 || ev.ByClass["staff_verified_post"] != 2 || ev.ByClass["official_publish_confirmed"] != 4 {
		t.Fatalf("evidence classes wrong: %+v", ev.ByClass)
	}
	if ev.DistinctParticipations != 5 {
		t.Fatalf("coverage must be distinct participations = 5, got %d", ev.DistinctParticipations)
	}
	if ev.OfficialConfirmedAttempts != 4 {
		t.Fatalf("official must be counted by trusted attempts = 4, got %d", ev.OfficialConfirmedAttempts)
	}
	// 3+2+4=9 ≠ 5:注记必须明说不能相加。
	if ev.Note == "" || !strings.Contains(ev.Note, "不能相加") {
		t.Fatalf("evidence panel must carry the not-additive note, got %q", ev.Note)
	}
}

// ---- 4. inventory & cost: ledger-owned, unknown stays unrecorded ----------------

func TestInventoryLineMirrorsLedgerOccupancy(t *testing.T) {
	resp := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{
		Inventory: []InventoryFactLine{{
			InventoryID: "inv1", Name: "满减券", Kind: "store_coupon", BenefitSlot: "main",
			RuleVersion: 2, TotalQty: 100, Reserved: 3, Issued: 5, Redeemed: 2,
			Reversed: 1, Expired: 4, FaceValueMinor: 500, CostMinor: 100, Currency: "CNY",
		}},
	}, FilterSet{})
	if len(resp.Inventory) != 1 {
		t.Fatalf("inventory lines = %d", len(resp.Inventory))
	}
	line := resp.Inventory[0]
	// available 照抄账本占用口径:total − (reserved+issued+redeemed) = 90。
	if line.Available != 90 {
		t.Fatalf("available = %d, want 90 (ledger occupancy semantics)", line.Available)
	}
	if line.Reversed != 1 || line.Expired != 4 {
		t.Fatalf("corrections must stay disclosed: %+v", line)
	}
}

func TestCostPanelKeepsThreeNumbersApart(t *testing.T) {
	resp := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{
		Cost: CostFacts{
			ContentGenerationAmountMinor: nil, // 未录入
			RewardCostMinor:              300,
			RewardRedeemedCostMinor:      100,
			RewardFaceMinor:              700,
			CostUnrecordedBatches:        2,
		},
	}, FilterSet{})
	cost := resp.Cost
	if cost.ContentGenerationAvailable {
		t.Fatalf("content generation cost without facts must be 未录入 (available=false)")
	}
	if cost.RewardCostMinor != 300 || cost.RewardRedeemedCostMinor != 100 {
		t.Fatalf("reward cost wrong: %+v", cost)
	}
	if cost.RewardFaceMinor != 700 {
		t.Fatalf("face = %d, want 700", cost.RewardFaceMinor)
	}
	if cost.UnrecordedBatches != 2 {
		t.Fatalf("unrecorded batches = %d, want 2", cost.UnrecordedBatches)
	}
	// 面额不是支出:注记必须在场。
	if !strings.Contains(cost.FaceNote, "不是") {
		t.Fatalf("face note must say face is not spend, got %q", cost.FaceNote)
	}
}

// ---- 5. honesty notes: nfc entry marker + anonymous visitor ----------------------

func TestBuildOperationsCarriesHonestyNotes(t *testing.T) {
	resp := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{}, FilterSet{})
	if !strings.Contains(resp.Notes.EntryMarker, "nfc") || !strings.Contains(resp.Notes.EntryMarker, "入口标记") {
		t.Fatalf("entry marker note missing: %q", resp.Notes.EntryMarker)
	}
	if !strings.Contains(resp.Notes.Visitor, "自然") {
		t.Fatalf("visitor note must deny natural-person equivalence: %q", resp.Notes.Visitor)
	}
	// 未知外部指标(播放/点赞/POI)不在漏斗里冒充事实。
	for _, st := range resp.Funnel {
		for _, unknown := range []string{"ugc_plays", "ugc_likes", "poi_exposure", "published_videos"} {
			if strings.Contains(st.Key, unknown) {
				t.Fatalf("external unknown metric %s leaked into funnel", st.Key)
			}
		}
	}
}

// ---- 6. filters: applied honestly, not pretended ---------------------------------

func TestAppliedFiltersDisclosed(t *testing.T) {
	all := FilterSet{CampaignID: "c1", StoreID: "s1", ContentPackVersion: "v3", Touchpoint: "nfc", Platform: "douyin", HandoffMethod: "h5"}
	if !all.HasAny() {
		t.Fatalf("HasAny must be true")
	}
	resp := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{VisitTotal: 1}, all)
	byKey := map[string]FunnelStage{}
	for _, st := range resp.Funnel {
		byKey[st.Key] = st
	}
	// 访问级:campaign/store/touchpoint 可用;content_pack_version/platform/handoff_method 无关联事实,不得假装过滤。
	visits := byKey[FunnelVisits]
	got := map[string]bool{}
	for _, k := range visits.AppliedFilters {
		got[k] = true
	}
	for _, must := range []string{"campaign_id", "store_id", "touchpoint"} {
		if !got[must] {
			t.Fatalf("visits must apply %s, got %v", must, visits.AppliedFilters)
		}
	}
	for _, forbidden := range []string{"content_pack_version", "platform", "handoff_method"} {
		if got[forbidden] {
			t.Fatalf("visits must NOT apply %s (no linked facts)", forbidden)
		}
	}
	// 参与级:campaign/store/touchpoint/content_pack_version 全适用;平台/交接方式不适用。
	started := byKey[FunnelStarted]
	gotStarted := map[string]bool{}
	for _, k := range started.AppliedFilters {
		gotStarted[k] = true
	}
	for _, must := range []string{"campaign_id", "store_id", "touchpoint", "content_pack_version"} {
		if !gotStarted[must] {
			t.Fatalf("started must apply %s, got %v", must, started.AppliedFilters)
		}
	}
	if gotStarted["platform"] || gotStarted["handoff_method"] {
		t.Fatalf("started must not pretend platform/handoff_method filtering")
	}
	// 内容选择级:平台/承接方式有原生事实列,必须适用。
	selected := byKey[FunnelContentSelected]
	gotSelected := map[string]bool{}
	for _, k := range selected.AppliedFilters {
		gotSelected[k] = true
	}
	if !gotSelected["platform"] || !gotSelected["handoff_method"] {
		t.Fatalf("content_selected must apply platform/handoff_method, got %v", selected.AppliedFilters)
	}
	// 空筛选时 applied_filters 为空(没筛就说没筛)。
	plain := BuildOperations(funnelWindow, Scope{TenantID: "t1"}, OpsFacts{}, FilterSet{})
	if len(plain.Funnel[0].AppliedFilters) != 0 {
		t.Fatalf("no filters requested: applied_filters must be empty")
	}
	if len(resp.FilterEcho) == 0 {
		t.Fatalf("requested filters must be echoed back")
	}
}

// ---- 7. OperationsSummary contract: fixed field names for 2628 -------------------

func TestOperationsSummaryContract(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-14T09:30:00Z")
	sum := BuildOperationsSummary(now, SummaryFacts{
		PendingConfirm: 2, RewardsIssued: 7, RewardsRedeemed: 3, Anomalies: 1,
	})
	if sum.AsOf != "2026-10-14T09:30:00Z" {
		t.Fatalf("as_of = %q", sum.AsOf)
	}
	if sum.PendingConfirm != 2 || sum.RewardsIssued != 7 || sum.RewardsRedeemed != 3 || sum.Anomalies != 1 {
		t.Fatalf("summary numbers wrong: %+v", sum)
	}
	// 契约:字段名逐字固定,消费者(2628)按此先行测试。
	raw, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"as_of", "pending_confirm", "rewards_issued", "rewards_redeemed", "anomalies"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("contract field %q missing in %s", field, raw)
		}
	}
	if len(decoded) != 5 {
		t.Fatalf("contract is exactly five fields, got %s", raw)
	}
	// 零事实=真实 0,不是 UNKNOWN:今天没发券就是 0。
	zero := BuildOperationsSummary(now, SummaryFacts{})
	if zero.RewardsIssued != 0 || zero.RewardsRedeemed != 0 || zero.PendingConfirm != 0 || zero.Anomalies != 0 {
		t.Fatalf("zero facts must be true zeros: %+v", zero)
	}
}
