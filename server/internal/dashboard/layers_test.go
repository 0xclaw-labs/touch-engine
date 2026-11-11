package dashboard

// layers_test.go: HUI-1677 TOUCH-ALL-20261109-v1 轮的纯函数契约——六层证据
// 分开计数、线索口径（待同步不算已获客）、费用对账四分、首页待办摘要。
// 纪律同 funnel_test.go:本包纯函数,事实由 store 层只读 SQL 提供。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func layersWindow() Window { return funnelWindow }

func nowUTC() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }

// ---- 1. 新筛选维度:action_id / environment --------------------------------------

func TestParseFilterSetExtValidatesEnvironment(t *testing.T) {
	for _, env := range []string{"desktop_browser", "ios_safari", "android_browser", "ios_wechat", "android_wechat", "target_app"} {
		if _, err := ParseFilterSetExt("", "", "", "", "", "", "", env); err != nil {
			t.Fatalf("environment %q must be accepted (3535 六环境同口径): %v", env, err)
		}
	}
	if _, err := ParseFilterSetExt("", "", "", "", "", "", "", "wechat_desktop"); err == nil {
		t.Fatalf("unknown environment must be refused, not silently match nothing")
	}
	f, err := ParseFilterSetExt("c1", "", "", "", "", "", "sa_abc", "ios_wechat")
	if err != nil {
		t.Fatalf("valid ext filters: %v", err)
	}
	if !f.HasAny() {
		t.Fatalf("ext filters must report HasAny=true")
	}
	if f.ActionID != "sa_abc" || f.Environment != "ios_wechat" {
		t.Fatalf("action/environment not carried: %+v", f)
	}
}

// ---- 2. 六层证据:逐层独立、每层 definition 齐备、绝不相加 ------------------------

var layersFacts = OpsFacts{
	LayerActionSessions:   7,
	LayerServiceOpens:     3,
	LayerJumpAttempts:     5,
	LayerCustomerClick:    6,
	LayerWakeAttempt:      4,
	LayerMediaHandoff:     3,
	LayerEditorProved:     2,
	LayerHandoffRequested: 4,
	LayerHandoffCompleted: 3,
	LayerUserReported:     2,
	LayerSelfReportEvent:  1,
	LayerStaffSaw:         2,
	LayerStaffVerified:    1,
	LayerStaffConfirm:     1,
	LayerPlatformHandoffs: 1,
	LayerOfficialEvents:   1,
	LayerPlatformConfirm:  1,
	EvidenceByClass:       map[string]int64{"staff_observed_submission": 2},
}

func TestSixLayersPresentWithDefinitions(t *testing.T) {
	resp := BuildOperations(layersWindow(), Scope{TenantID: "t1"}, layersFacts, FilterSet{})
	want := []string{
		LayerActionClicked, LayerInvokeAttempt, LayerHandoffMedia,
		LayerUserReported, LayerStaffConfirmed, LayerPlatformVerified,
	}
	if len(resp.EvidenceLayers.Layers) != len(want) {
		t.Fatalf("evidence layers = %d, want %d", len(resp.EvidenceLayers.Layers), len(want))
	}
	for i, key := range want {
		l := resp.EvidenceLayers.Layers[i]
		if l.Key != key {
			t.Fatalf("layer[%d].key = %q, want %q", i, l.Key, key)
		}
		if !l.Available {
			t.Fatalf("%s: layer facts are real touch-domain rows, must be available", key)
		}
		d := l.Definition
		if d.Source == "" || d.DedupKey == "" || d.Denominator == "" || d.EventTime == "" || d.WindowNote == "" {
			t.Fatalf("%s: definition incomplete: %+v", key, d)
		}
		if len(l.Counts) == 0 {
			t.Fatalf("%s: layer must carry at least one named count line", key)
		}
	}
}

func TestLayerCountsMapFromFacts(t *testing.T) {
	resp := BuildOperations(layersWindow(), Scope{TenantID: "t1"}, layersFacts, FilterSet{})
	byKey := map[string]map[string]int64{}
	for _, l := range resp.EvidenceLayers.Layers {
		m := map[string]int64{}
		for _, c := range l.Counts {
			m[c.Key] = c.Value
		}
		byKey[l.Key] = m
	}
	if byKey[LayerActionClicked]["action_sessions"] != 7 || byKey[LayerActionClicked]["service_opens"] != 3 || byKey[LayerActionClicked]["customer_click"] != 6 {
		t.Fatalf("click layer lines wrong: %v", byKey[LayerActionClicked])
	}
	if byKey[LayerInvokeAttempt]["service_jump_attempts"] != 5 || byKey[LayerInvokeAttempt]["wake_attempt"] != 4 || byKey[LayerInvokeAttempt]["editor_open_proved"] != 2 {
		t.Fatalf("invoke layer lines wrong: %v", byKey[LayerInvokeAttempt])
	}
	if byKey[LayerHandoffMedia]["handoff_requested"] != 4 || byKey[LayerHandoffMedia]["handoff_completed"] != 3 || byKey[LayerHandoffMedia]["media_handoff_events"] != 3 {
		t.Fatalf("media handoff layer lines wrong: %v", byKey[LayerHandoffMedia])
	}
	if byKey[LayerUserReported]["user_reported"] != 2 || byKey[LayerUserReported]["customer_self_report"] != 1 {
		t.Fatalf("user_reported lines wrong: %v", byKey[LayerUserReported])
	}
	if byKey[LayerStaffConfirmed]["staff_saw_submit"] != 2 || byKey[LayerStaffConfirmed]["staff_verified_work"] != 1 || byKey[LayerStaffConfirmed]["staff_confirm"] != 1 {
		t.Fatalf("staff layer lines wrong: %v", byKey[LayerStaffConfirmed])
	}
	if byKey[LayerPlatformVerified]["platform_confirmed_handoffs"] != 1 || byKey[LayerPlatformVerified]["official_confirmed_events"] != 1 || byKey[LayerPlatformVerified]["platform_confirm"] != 1 {
		t.Fatalf("platform layer lines wrong: %v", byKey[LayerPlatformVerified])
	}
}

func TestLayersNeverSummedNote(t *testing.T) {
	resp := BuildOperations(layersWindow(), Scope{TenantID: "t1"}, layersFacts, FilterSet{})
	note := resp.EvidenceLayers.Note
	if !strings.Contains(note, "不相加") {
		t.Fatalf("layers note must forbid summing: %q", note)
	}
}

// ---- 3. 线索口径:待同步不算已获客 -------------------------------------------------

func TestLeadsPanelHonestBuckets(t *testing.T) {
	resp := BuildOperations(layersWindow(), Scope{TenantID: "t1"}, OpsFacts{
		Leads: LeadsFacts{
			CampaignSubmitted: 10, StoreSubmitted: 4,
			PendingSync: 6, ReceiverConfirmed: 3,
			Revoked: 1, ReceiverDeclaredRevoked: 1, UniqueContacts: 9,
		},
	}, FilterSet{})
	lp := resp.Leads
	if lp.CampaignSubmitted != 10 || lp.StoreSubmitted != 4 {
		t.Fatalf("lead source split wrong: %+v", lp)
	}
	if lp.PendingSync != 6 || lp.ReceiverConfirmed != 3 {
		t.Fatalf("pending_sync and receiver_confirmed must be separate buckets: %+v", lp)
	}
	if lp.ReceiverConfirmed+lp.PendingSync != 9 {
		t.Fatalf("buckets are disclosure lines, but the two together are the window submissions: %d", lp.ReceiverConfirmed+lp.PendingSync)
	}
	if !strings.Contains(lp.Note, "待同步不算已获客") {
		t.Fatalf("leads note must state pending-sync is not acquisition: %q", lp.Note)
	}
	if lp.Definition.Source == "" || lp.Definition.DedupKey == "" {
		t.Fatalf("leads panel must carry its provenance: %+v", lp.Definition)
	}
}

// ---- 4. 费用对账:订阅/AI/硬件分明细,退款分列,未知不冒充 0 --------------------------

func TestCostReconciliationSeparatesStreams(t *testing.T) {
	paid := int64(29900)
	hw := int64(120000)
	resp := BuildOperations(layersWindow(), Scope{TenantID: "t1"}, OpsFacts{
		CostRecon: CostReconciliationFacts{
			SubscriptionPaidMinor: &paid, SubscriptionPaidOrders: 1, SubscriptionFailedOrders: 2,
			AIConsumedMinor: 500, AIConsumedCount: 5,
			AIConfirmedMinor: 120, AIConfirmedCount: 1,
			AIRefundedMinor: 60, AIRefundedCount: 1,
			AIFailedMinor: 30, AIFailedCount: 1,
			HardwarePaidMinor: &hw, HardwarePaidOrders: 2,
			HardwareRefundMinor: 20000, HardwareRefunds: 1,
		},
	}, FilterSet{})
	cr := resp.CostReconciliation
	if !cr.SubscriptionAvailable || cr.SubscriptionPaidMinor == nil || *cr.SubscriptionPaidMinor != 29900 {
		t.Fatalf("subscription paid projection wrong: %+v", cr)
	}
	if cr.SubscriptionFailedOrders != 2 {
		t.Fatalf("failed orders are separate facts, never spend: %+v", cr)
	}
	if cr.AIConsumedMinor != 500 || cr.AIRefundedMinor != 60 || cr.AIFailedMinor != 30 || cr.AIConfirmedMinor != 120 {
		t.Fatalf("AI states must be listed apart: %+v", cr)
	}
	if cr.HardwareRefundMinor != 20000 {
		t.Fatalf("refunds listed apart, not netted into paid: %+v", cr)
	}
	if !strings.Contains(cr.FaceNote, "面额") {
		t.Fatalf("face-value note must carry over: %q", cr.FaceNote)
	}
}

func TestCostReconciliationNilStaysUnknown(t *testing.T) {
	resp := BuildOperations(layersWindow(), Scope{TenantID: "t1"}, OpsFacts{CostRecon: CostReconciliationFacts{
		SubscriptionFailedOrders: 1, // 失败单存在,但窗口内没有任何支付事实
	}}, FilterSet{})
	cr := resp.CostReconciliation
	if cr.SubscriptionAvailable {
		t.Fatalf("no payment facts = unavailable, must not pretend 0 spend")
	}
	if cr.SubscriptionPaidMinor != nil {
		t.Fatalf("nil payment facts must serialize as absent, not 0")
	}
	if cr.SubscriptionNote == "" {
		t.Fatalf("unavailable subscription spend must carry its honest note")
	}
}

// ---- 5. summary 待办摘要:老字段逐字保留 + todos 新块 ------------------------------

func TestSummaryTodosContract(t *testing.T) {
	sum := BuildOperationsSummary(nowUTC(), SummaryFacts{
		PendingConfirm: 2, RewardsIssued: 5, RewardsRedeemed: 3, Anomalies: 1,
		Todos: TodoFacts{
			UnavailableActions: 2, MissingContent: 3, ActionAuthExpired: 1,
			AdmissionExpired: 1, PendingRedeem: 5, BillingFailedOrders: 2,
		},
	})
	// 老契约字段不动(2628 按此先行测试)。
	if sum.PendingConfirm != 2 || sum.RewardsIssued != 5 || sum.RewardsRedeemed != 3 || sum.Anomalies != 1 {
		t.Fatalf("fixed summary contract must stay byte-stable: %+v", sum)
	}
	td := sum.Todos
	if td.UnavailableActions != 2 || td.MissingContent != 3 || td.PendingRedeem != 5 || td.BillingAnomalies != 2 {
		t.Fatalf("todos facts not mapped: %+v", td)
	}
	if td.AuthorizationExpired != 2 { // action 1 + admission 1
		t.Fatalf("authorization_expired = action+admission: %+v", td)
	}
	if td.PendingConfirm != 2 {
		t.Fatalf("todos.pending_confirm mirrors the fixed contract: %+v", td)
	}
	if len(td.Definitions) == 0 {
		t.Fatalf("every todo item must carry its provenance definition")
	}
	raw, _ := json.Marshal(sum)
	if !strings.Contains(string(raw), "\"todos\"") {
		t.Fatalf("todos block must serialize under todos key")
	}
}
