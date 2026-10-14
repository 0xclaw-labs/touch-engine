// Package publishhandoff is HUI-1670 顾客发布交接(真实渠道交接与恢复).
//
// 职责: 交接前强制过 3535 渠道准入闸、attempt 级发布交接状态机(带证据
// 来源)、渠道适配(准入才自动化,未知/未准入诚实降级)、官方回执验签与
// 重放/乱序/超时的先回查语义、三种事实(人工见提交/人工核对作品/官方
// 确认)分别落库。
//
// 纪律(票面红线,全文见 docs/superpowers/specs/2026-10-14-hui-1670-design.md):
//   - 窗口失焦、网页点击、顾客自报不是 editor_opened / platform_confirmed
//     的证据;点击按钮或客户端自报永远不算发布成功;
//   - 人工事实永不转换成官方确认;顾客"我已完成"只产生核验请求事件;
//   - 重放、乱序和超时先回查同一次交接(幂等键),不盲目重新投稿;
//   - 复用 3535/3546/3553/1671 四个权威服务,只 import 不改:不另写平台
//     白名单、不建第二套参与模型、不写奖励账;
//   - 本包零平台外呼、零付费生成调用(结构性保证:交接失败不触发任何
//     付费生成,顾客路径无生成调用)。
//
// 本包是叶子包:publish_handoffs / publish_handoff_events 两表只由本包
// 修改(迁移 0034/0035);httpapi 仅经 Service 消费。
package publishhandoff

import (
	"errors"
	"fmt"
)

// State is the handoff lifecycle (九态). platform_confirmed is the ONLY
// confirmed state; no boolean collapses waiting/canceled/confirmed.
type State string

// Handoff states. 票面: 已预览、交接准备中、已发起打开、可证实的编辑器
// 打开、提交中、待核验、平台确认、取消、失败、未知。
const (
	StatePreviewed         State = "previewed"          // 已预览并选定内容
	StateHandoffPreparing  State = "handoff_preparing"  // 交接准备中(媒体受控 URL 铸造中)
	StateHandoffReady      State = "handoff_ready"      // 交接就绪(载荷/导出包就绪)
	StateEditorOpened      State = "editor_opened"      // 可证实的编辑器打开(仅验证证明)
	StateSubmitting        State = "submitting"         // 提交中(适配器已把请求交出)
	StateAwaitingReview    State = "awaiting_review"    // 待核验(店员/官方核验中)
	StatePlatformConfirmed State = "platform_confirmed" // 平台确认(仅验签回执)
	StateCanceled          State = "canceled"           // 顾客/店员取消(终态)
	StateFailed            State = "failed"             // 交接失败(终态;不触发付费生成)
	StateUnknown           State = "unknown"            // 结果未知(先回查,不重投)
)

// EvidenceSource answers "谁证明的" for every state write. 状态落库必带
// 证据来源;不在枚举内的来源(失焦/自报/点击)在编译期就进不来。
type EvidenceSource string

const (
	EvidenceCustomerAction   EvidenceSource = "customer_action"   // 顾客显式动作(确认/取消)
	EvidenceStaffObservation EvidenceSource = "staff_observation" // 店员目测/核对(人工事实)
	EvidenceOfficialReceipt  EvidenceSource = "official_receipt"  // 验签通过的官方回执
	EvidenceVerifierProof    EvidenceSource = "verifier_proof"    // 可证实的编辑器打开证明
	EvidenceSystemTimeout    EvidenceSource = "system_timeout"    // 超时(转 unknown,不重投)
	EvidenceSystemRecovery   EvidenceSource = "system_recovery"   // 重启后回查
	EvidenceSystemAdapter    EvidenceSource = "system_adapter"    // 适配器/媒体准备(系统侧)
)

// IsValidEvidence reports whether s is one of the six evidence sources.
// window_blur / self_report / click deliberately do not exist.
func IsValidEvidence(s EvidenceSource) bool {
	switch s {
	case EvidenceCustomerAction, EvidenceStaffObservation, EvidenceOfficialReceipt,
		EvidenceVerifierProof, EvidenceSystemTimeout, EvidenceSystemRecovery,
		EvidenceSystemAdapter:
		return true
	}
	return false
}

// AllStates returns the ten states in ticket order (九态语义 + unknown;
// previewed 是状态机入口而非第四态之外的第十种语义).
func AllStates() []State {
	return []State{
		StatePreviewed, StateHandoffPreparing, StateHandoffReady, StateEditorOpened,
		StateSubmitting, StateAwaitingReview, StatePlatformConfirmed,
		StateCanceled, StateFailed, StateUnknown,
	}
}

// terminal reports whether s ends the lifecycle (no resurrection: 官方晚到
// 也不改写已取消/已失败——回查语义只对 unknown/submitting 有意义).
func terminal(s State) bool {
	switch s {
	case StatePlatformConfirmed, StateCanceled, StateFailed:
		return true
	}
	return false
}

// transitions maps from -> the set of legal targets. unknown -> submitting is
// deliberately ABSENT: after a timeout the handoff is queried, never resent.
var transitions = map[State]map[State]bool{
	StatePreviewed: {
		StateHandoffPreparing: true,
		StateCanceled:         true,
	},
	StateHandoffPreparing: {
		StateHandoffReady: true,
		StateFailed:       true, // 媒体准备失败(零付费生成)
		StateCanceled:     true,
	},
	StateHandoffReady: {
		StateEditorOpened:   true, // 仅可证实证据(verifier_proof)
		StateSubmitting:     true, // automated:适配器交出请求
		StateAwaitingReview: true, // manual_only:店员核验
		StateCanceled:       true,
	},
	StateEditorOpened: {
		StateSubmitting:     true,
		StateAwaitingReview: true,
		StateCanceled:       true,
	},
	StateSubmitting: {
		StatePlatformConfirmed: true,
		StateAwaitingReview:    true,
		StateUnknown:           true, // 超时(先回查)
		StateCanceled:          true,
	},
	StateAwaitingReview: {
		StatePlatformConfirmed: true, // 官方晚到(人工先核验不重复发放,权益归 1671)
		StateCanceled:          true,
	},
	StateUnknown: {
		StatePlatformConfirmed: true, // 回查到官方结果
		StateAwaitingReview:    true,
		StateCanceled:          true,
	},
	StatePlatformConfirmed: {},
	StateCanceled:          {},
	StateFailed:            {},
}

// CanTransition reports whether from→to is a legal structural transition.
// Terminal states never leave; unknown states fail closed.
func CanTransition(from, to State) bool {
	if terminal(from) {
		return false
	}
	return transitions[from][to]
}

// requiredEvidence lists which evidence sources can carry each target.
// editor_opened 只有 verifier_proof;platform_confirmed 只有官方回执;
// canceled 只有顾客动作或店员观察(系统超时不构成取消)。
var requiredEvidence = map[State]map[EvidenceSource]bool{
	StateHandoffPreparing:  {EvidenceCustomerAction: true},
	StateHandoffReady:      {EvidenceSystemAdapter: true},
	StateEditorOpened:      {EvidenceVerifierProof: true},
	StateSubmitting:        {EvidenceSystemAdapter: true},
	StateAwaitingReview:    {EvidenceStaffObservation: true},
	StatePlatformConfirmed: {EvidenceOfficialReceipt: true},
	StateUnknown:           {EvidenceSystemTimeout: true},
	StateCanceled:          {EvidenceCustomerAction: true, EvidenceStaffObservation: true},
	StateFailed:            {EvidenceSystemAdapter: true},
}

// Machine sentinel errors.
var (
	ErrIllegalTransition = errors.New("publishhandoff: illegal state transition")
	ErrEvidenceRejected  = errors.New("publishhandoff: evidence source cannot prove this transition")
)

// CheckTransition enforces BOTH the structural edge and its evidence source:
// a legal edge with the wrong evidence is refused (窗口失焦不是编辑器打开,
// 人工不是官方). Unknown evidence sources are refused outright.
func CheckTransition(from, to State, ev EvidenceSource) error {
	if !IsValidEvidence(ev) {
		return fmt.Errorf("%w: unknown evidence source %q", ErrEvidenceRejected, ev)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
	}
	if !requiredEvidence[to][ev] {
		return fmt.Errorf("%w: %s -> %s requires one of its evidence sources, got %q",
			ErrEvidenceRejected, from, to, ev)
	}
	return nil
}

// IsActive reports whether s still allows progress (not terminal).
func IsActive(s State) bool { return !terminal(s) }
