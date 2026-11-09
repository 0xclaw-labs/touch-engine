// hardware.go: 硬件订单履约五状态机(HUI-4591 F14,纯函数)。
//
// 纪律(裁定书):
//   - 付款/出库/写卡/回读/现场验收五状态独立记录,每步带操作者/时间/设备
//     标识;不可跳跃(写卡前必须已出库,回读前必须已写卡,验收前必须已回读)。
//   - 事实不覆盖:同步骤同事实重放幂等通过,不同事实拒绝(交付事实只增不改)。
//   - 卡片交付状态机的应用侧归 HUI-1663;本包是订单侧可重建投影的判定核
//     (受信权威缝写入)。

package billing

import "errors"

// Fulfillment steps (库层列名同值语义)。
const (
	StepPayment    = "payment"
	StepShipment   = "shipment"
	StepWriteCard  = "write_card"
	StepReadback   = "readback"
	StepAcceptance = "acceptance"
)

// Step states.
const (
	StepPending = "pending"
	StepDone    = "done"
)

var (
	// ErrStepSkipped:前置步骤未完成(不可跳跃)。
	ErrStepSkipped = errors.New("billing: hardware_step_skipped")
	// ErrStepConflict:同步骤不同事实(覆盖拒绝)或未知步骤。
	ErrStepConflict = errors.New("billing: hardware_step_conflict")
)

// HardwareStep is one step's recorded fact.
type HardwareStep struct {
	State      string
	Operator   string
	OccurredAt string
	DeviceRef  string
}

// HardwareFulfillment is the order-side delivery projection (five independent
// step facts; 库层 billing_hardware_fulfillment 一行五组列)。
type HardwareFulfillment struct {
	Payment    HardwareStep
	Shipment   HardwareStep
	WriteCard  HardwareStep
	Readback   HardwareStep
	Acceptance HardwareStep
}

// HardwareStepInput is one trusted delivery fact.
type HardwareStepInput struct {
	Step       string
	Operator   string
	OccurredAt string
	DeviceRef  string
}

func (f HardwareFulfillment) step(s string) HardwareStep {
	switch s {
	case StepPayment:
		return f.Payment
	case StepShipment:
		return f.Shipment
	case StepWriteCard:
		return f.WriteCard
	case StepReadback:
		return f.Readback
	case StepAcceptance:
		return f.Acceptance
	}
	return HardwareStep{State: "unknown"}
}

// hardwareOrder defines the mandatory sequence (写卡前必须已出库…).
var hardwareOrder = []string{StepPayment, StepShipment, StepWriteCard, StepReadback, StepAcceptance}

// ApplyHardwareStep records one delivery fact on the projection. Replaying the
// identical fact is idempotent; a different fact for a done step conflicts
// (delivery facts are append-only); skipping forward refuses.
func ApplyHardwareStep(f HardwareFulfillment, in HardwareStepInput) (HardwareFulfillment, error) {
	valid := false
	idx := -1
	for i, s := range hardwareOrder {
		if s == in.Step {
			valid, idx = true, i
			break
		}
	}
	if !valid {
		return f, ErrStepConflict
	}
	if in.Operator == "" || in.OccurredAt == "" {
		return f, ErrStepConflict
	}
	current := f.step(in.Step)
	if current.State == StepDone {
		// 同事实重放 = 幂等;不同事实 = 覆盖拒绝。
		if current.Operator == in.Operator && current.OccurredAt == in.OccurredAt && current.DeviceRef == in.DeviceRef {
			return f, nil
		}
		return f, ErrStepConflict
	}
	// 前置步骤必须全部完成(不可跳跃;首步 payment 无前置)。
	if idx > 0 {
		prev := f.step(hardwareOrder[idx-1])
		if prev.State != StepDone {
			return f, ErrStepSkipped
		}
	}
	done := HardwareStep{State: StepDone, Operator: in.Operator, OccurredAt: in.OccurredAt, DeviceRef: in.DeviceRef}
	switch in.Step {
	case StepPayment:
		f.Payment = done
	case StepShipment:
		f.Shipment = done
	case StepWriteCard:
		f.WriteCard = done
	case StepReadback:
		f.Readback = done
	case StepAcceptance:
		f.Acceptance = done
	}
	return f, nil
}
