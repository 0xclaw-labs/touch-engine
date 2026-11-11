package delivery

// HUI-1663 交付七步状态机纯函数测试。
// 裁定书:付款→出库→绑定→写卡→回读→现场验收→交付,每步独立事实记录,
// 不可跳跃(写卡前必须已出库+绑定);事实不覆盖;回读一致性核对。

import (
	"errors"
	"testing"
)

func TestNextStepFollowsStrictPipeline(t *testing.T) {
	// 顺序走完整链:awaiting_dispatch 起,七步中后六步逐一推进。
	got := []string{}
	status := StatusAwaitingDispatch
	for {
		next, ok := NextStep(status)
		if !ok {
			break
		}
		var err error
		status, err = ApplyStep(status, next)
		if err != nil {
			t.Fatalf("apply %s from %s: %v", next, status, err)
		}
		got = append(got, next)
	}
	want := []string{StepDispatch, StepBound, StepWritten, StepReadback, StepAccepted, StepDelivered}
	if len(got) != len(want) {
		t.Fatalf("pipeline = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pipeline = %v, want %v", got, want)
		}
	}
	if status != StepDelivered {
		t.Fatalf("final status = %q", status)
	}
}

func TestApplyStepRefusesSkipping(t *testing.T) {
	// 跳步拒绝:每一步只接受紧邻的下一步(裁定书「写卡前必须已出库+绑定」)。
	matrix := []struct {
		status string
		step   string
	}{
		{StatusAwaitingDispatch, StepBound},     // 未出库先绑定
		{StatusAwaitingDispatch, StepWritten},   // 未出库先写卡
		{StatusAwaitingDispatch, StepDelivered}, // 直接交付
		{StepDispatch, StepWritten},             // 未绑定先写卡(裁定书明示)
		{StepBound, StepReadback},               // 未写卡先回读
		{StepWritten, StepAccepted},             // 未回读先验收
		{StepReadback, StepDelivered},           // 未验收先交付
	}
	for _, m := range matrix {
		if _, err := ApplyStep(m.status, m.step); !errors.Is(err, ErrStepSkipped) {
			t.Fatalf("ApplyStep(%q,%q) err = %v, want ErrStepSkipped", m.status, m.step, err)
		}
	}
}

func TestApplyStepRefusesUnknownAndClosed(t *testing.T) {
	if _, err := ApplyStep(StatusAwaitingDispatch, "teleported"); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("unknown step err = %v, want ErrUnknownStep", err)
	}
	// 终态:已交付/已封存(superseded)拒写任何事实推进。
	for _, closed := range []string{StepDelivered, StatusSuperseded} {
		if _, err := ApplyStep(closed, StepDispatch); !errors.Is(err, ErrDeliveryClosed) {
			t.Fatalf("closed %q err = %v, want ErrDeliveryClosed", closed, err)
		}
	}
	if _, err := ApplyStep("some_unknown_status", StepDispatch); !errors.Is(err, ErrDeliveryClosed) {
		t.Fatalf("unknown status err = %v, want ErrDeliveryClosed", err)
	}
}

func TestHardwareStepForMapsOrderProjection(t *testing.T) {
	// 4591 五状态接线:出库/写卡/回读/验收推订单投影;付款只读、绑定/交付本域。
	matrix := map[string]string{
		StepDispatch:  "shipment",
		StepWritten:   "write_card",
		StepReadback:  "readback",
		StepAccepted:  "acceptance",
		StepBound:     "",
		StepDelivered: "",
	}
	for step, want := range matrix {
		if got := HardwareStepFor(step); got != want {
			t.Fatalf("HardwareStepFor(%q) = %q, want %q", step, got, want)
		}
	}
}

func TestEventKindGuards(t *testing.T) {
	for _, ev := range []string{StepDispatch, StepBound, StepWritten, StepReadback, StepAccepted, StepDelivered} {
		if !IsStepEvent(ev) {
			t.Fatalf("%q should be a step event", ev)
		}
	}
	if IsStepEvent("replaced") {
		t.Fatalf("replaced is not a step event")
	}
	if !IsReplacementEvent("replaced") {
		t.Fatalf("replaced should be the replacement event")
	}
	if IsReplacementEvent(StepDispatch) {
		t.Fatalf("dispatch should not be a replacement event")
	}
}

func TestCheckReadback(t *testing.T) {
	code := "9H5K2M7VXQ4A"
	abs := "https://h5.example.com/t/" + code
	cases := []struct {
		name       string
		read       string
		expected   string
		bound      string
		wantMatch  bool
		wantReason string
	}{
		{"逐字符一致(绝对 URL)", abs, abs, code, true, "ok"},
		{"尾部斜杠容忍", abs + "/", abs, code, true, "ok"},
		{"首尾空白容忍", "  " + abs + " ", abs, code, true, "ok"},
		{"码段小写经 Crockford 归一", "https://h5.example.com/t/9h5k2m7vxq4a", abs, code, true, "ok"},
		{"路径形式一致(未配置域名)", "/t/" + code, "/t/" + code, code, true, "ok"},
		{"错误域名拒绝(反例钉死)", "https://evil.example.com/t/" + code, abs, code, false, "host_mismatch"},
		{"错误码拒绝", "https://h5.example.com/t/AAAAAAAAAAAA", abs, code, false, "code_mismatch"},
		{"路径形式冒充绝对(配置后拒绝)", "/t/" + code, abs, code, false, "host_mismatch"},
		{"绝对冒充路径(未配置时拒绝)", abs, "/t/" + code, code, false, "host_mismatch"},
		{"空回读", "", abs, code, false, "empty_read"},
		{"非 /t 路径", "https://h5.example.com/c/" + code, abs, code, false, "code_mismatch"},
	}
	for _, c := range cases {
		got := CheckReadback(c.read, c.expected, c.bound)
		if got.Matches != c.wantMatch || got.Reason != c.wantReason {
			t.Fatalf("%s: got %+v, want matches=%v reason=%q", c.name, got, c.wantMatch, c.wantReason)
		}
	}
}
