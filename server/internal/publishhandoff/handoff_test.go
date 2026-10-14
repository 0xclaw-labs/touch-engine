package publishhandoff

import "testing"

// T2: 九态状态机 + 证据来源门禁(纯函数)。
//
// 票面: 状态至少区分已预览、交接准备中、已发起打开、可证实的编辑器打开、
// 提交中、取消、失败、待核验、平台确认;窗口失焦、网页点击或用户自报
// 不能证明平台已打开或已发布。

func TestStateMachineHappyPathAutomated(t *testing.T) {
	steps := []struct {
		from, to State
		ev       EvidenceSource
	}{
		{StatePreviewed, StateHandoffPreparing, EvidenceCustomerAction},
		{StateHandoffPreparing, StateHandoffReady, EvidenceSystemAdapter},
		{StateHandoffReady, StateEditorOpened, EvidenceVerifierProof},
		{StateEditorOpened, StateSubmitting, EvidenceSystemAdapter},
		{StateSubmitting, StatePlatformConfirmed, EvidenceOfficialReceipt},
	}
	for _, s := range steps {
		if !CanTransition(s.from, s.to) {
			t.Fatalf("%s -> %s must be a legal transition", s.from, s.to)
		}
		if err := CheckTransition(s.from, s.to, s.ev); err != nil {
			t.Fatalf("%s -> %s with %s: %v", s.from, s.to, s.ev, err)
		}
	}
}

func TestStateMachineManualOnlyPath(t *testing.T) {
	// manual_only 降级路径:无 editor_opened/submitting(那两条要可证实/适配器发起)。
	steps := []struct {
		from, to State
		ev       EvidenceSource
	}{
		{StatePreviewed, StateHandoffPreparing, EvidenceCustomerAction},
		{StateHandoffPreparing, StateHandoffReady, EvidenceSystemAdapter},
		{StateHandoffReady, StateAwaitingReview, EvidenceStaffObservation},
		{StateAwaitingReview, StatePlatformConfirmed, EvidenceOfficialReceipt},
	}
	for _, s := range steps {
		if err := CheckTransition(s.from, s.to, s.ev); err != nil {
			t.Fatalf("%s -> %s with %s: %v", s.from, s.to, s.ev, err)
		}
	}
}

func TestEditorOpenedRequiresVerifierProof(t *testing.T) {
	badEvidence := []EvidenceSource{
		EvidenceCustomerAction, // 用户自报/点击
		EvidenceStaffObservation,
		EvidenceSystemAdapter,
		EvidenceOfficialReceipt,
		EvidenceSystemTimeout,
		"",
	}
	for _, ev := range badEvidence {
		if err := CheckTransition(StateHandoffReady, StateEditorOpened, ev); err == nil {
			t.Fatalf("handoff_ready -> editor_opened with %q must be rejected: 失焦/自报/点击不是证据", ev)
		}
	}
	if !CanTransition(StatePreviewed, StateEditorOpened) {
		// 未交接就宣称编辑器打开:非法迁移。
	} else {
		t.Fatal("previewed -> editor_opened must be illegal")
	}
}

func TestPlatformConfirmedOnlyViaOfficialReceipt(t *testing.T) {
	for _, ev := range []EvidenceSource{EvidenceCustomerAction, EvidenceStaffObservation, EvidenceSystemAdapter, EvidenceVerifierProof, EvidenceSystemTimeout, EvidenceSystemRecovery, ""} {
		if err := CheckTransition(StateSubmitting, StatePlatformConfirmed, ev); err == nil {
			t.Fatalf("submitting -> platform_confirmed with %q must be rejected: 人工/自报永不转官方", ev)
		}
	}
}

func TestCancelFromActiveStatesOnly(t *testing.T) {
	active := []State{StatePreviewed, StateHandoffPreparing, StateHandoffReady, StateEditorOpened, StateSubmitting, StateAwaitingReview, StateUnknown}
	for _, from := range active {
		if !CanTransition(from, StateCanceled) {
			t.Fatalf("%s -> canceled must be legal", from)
		}
	}
	for _, from := range []State{StatePlatformConfirmed, StateCanceled, StateFailed} {
		if CanTransition(from, StateCanceled) {
			t.Fatalf("%s -> canceled must be illegal (terminal)", from)
		}
		if CanTransition(from, StatePlatformConfirmed) {
			t.Fatalf("%s -> platform_confirmed must be illegal (terminal/no resurrection)", from)
		}
	}
	// 取消需要顾客动作或店员观察;系统超时不构成取消。
	if err := CheckTransition(StateHandoffReady, StateCanceled, EvidenceSystemTimeout); err == nil {
		t.Fatal("canceled requires customer_action or staff_observation")
	}
}

func TestUnknownTransitions(t *testing.T) {
	// submitting 超时 -> unknown(system_timeout);unknown 晚到官方回执 ->
	// platform_confirmed;unknown 店员核验 -> awaiting_review。
	if err := CheckTransition(StateSubmitting, StateUnknown, EvidenceSystemTimeout); err != nil {
		t.Fatalf("submitting -> unknown: %v", err)
	}
	if err := CheckTransition(StateUnknown, StatePlatformConfirmed, EvidenceOfficialReceipt); err != nil {
		t.Fatalf("unknown -> platform_confirmed: %v", err)
	}
	if err := CheckTransition(StateUnknown, StateAwaitingReview, EvidenceStaffObservation); err != nil {
		t.Fatalf("unknown -> awaiting_review: %v", err)
	}
	// unknown 不允许盲目重投:回到 submitting 非法(先回查)。
	if CanTransition(StateUnknown, StateSubmitting) {
		t.Fatal("unknown -> submitting must be illegal: 超时先查询,不盲目重发")
	}
}

func TestFailedAndTerminalSemantics(t *testing.T) {
	if err := CheckTransition(StateHandoffPreparing, StateFailed, EvidenceSystemAdapter); err != nil {
		t.Fatalf("handoff_preparing -> failed: %v", err)
	}
	// failed 是终态;官方晚到也不复活(回查语义另行走 unknown)。
	for _, to := range []State{StateHandoffReady, StateSubmitting, StatePlatformConfirmed, StateAwaitingReview} {
		if CanTransition(StateFailed, to) {
			t.Fatalf("failed -> %s must be illegal", to)
		}
	}
}

func TestUnknownEvidenceSourceRejected(t *testing.T) {
	if IsValidEvidence("window_blur") {
		t.Fatal("window_blur must not be an evidence source")
	}
	if IsValidEvidence("self_report") {
		t.Fatal("self_report must not be an evidence source")
	}
	if IsValidEvidence("click") {
		t.Fatal("click must not be an evidence source")
	}
}

func TestAllNineTicketStatesExist(t *testing.T) {
	got := map[State]bool{}
	for _, s := range AllStates() {
		got[s] = true
	}
	want := []string{"previewed", "handoff_preparing", "handoff_ready", "editor_opened",
		"submitting", "awaiting_review", "platform_confirmed", "canceled", "failed", "unknown"}
	for _, w := range want {
		if !got[State(w)] {
			t.Fatalf("state %q missing from AllStates", w)
		}
	}
	if len(AllStates()) != len(want) {
		t.Fatalf("AllStates has %d entries, want %d", len(AllStates()), len(want))
	}
}
