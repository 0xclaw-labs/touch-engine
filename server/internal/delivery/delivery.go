// Package delivery: HUI-1663 实体 NFC 交付七步状态机(纯函数,零 IO)。
//
// 裁定书纪律:
//   - 付款→出库→绑定→写卡→回读→现场验收→交付,每步独立事实记录(操作者/时间/
//     设备标识),不可跳跃(写卡前必须已出库+绑定)。
//   - 付款步事实归 HUI-4591 订单履约投影(billing_hardware_fulfillment.payment,
//     只读前置);出库/写卡/回读/现场验收四步同步推 4591 投影
//     (HardwareStepFor);绑定确认与交付是本域两步(4591 报告 §concerns-3:
//     「绑定归 1665/1663 域,在其侧校验」)。
//   - 事实不覆盖:同事件幂等读回、异负载冲突的裁决在 store 层事件账本
//     (0130 UNIQUE(tenant_id,source,event_id));本包只裁决状态推进。
//   - 回读一致性核对(CheckReadback)是「读回内容与绑定 code」的纯判定:
//     错误域名/错误码一律不匹配——回读不一致不得显示「部署已验证」。
//   - superseded(补换封存)与 delivered(终态)拒写任何推进。
package delivery

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Delivery status / step values (库层 0129 status 列同值语义)。
const (
	// StatusAwaitingDispatch: 初始态(付款事实已验——创建闸在 store 层)。
	StatusAwaitingDispatch = "awaiting_dispatch"
	// StatusSuperseded: 该物理交付已被补换新行取代(封存,只读)。
	StatusSuperseded = "superseded"

	StepDispatch  = "dispatched"
	StepBound     = "bound"
	StepWritten   = "written"
	StepReadback  = "readback"
	StepAccepted  = "accepted"
	StepDelivered = "delivered"
)

// Sentinel errors (stable for store/httpapi layers).
var (
	// ErrUnknownStep: 不是七步事实词表内的步。
	ErrUnknownStep = errors.New("delivery: unknown step")
	// ErrStepSkipped: 前置步未完成(不可跳跃)。
	ErrStepSkipped = errors.New("delivery: step skipped")
	// ErrDeliveryClosed: 终态(delivered)或封存(superseded)拒写。
	ErrDeliveryClosed = errors.New("delivery: delivery is closed")
)

// StepOrder is the closed, forward-only pipeline (付款只读前置,不入列)。
var StepOrder = []string{StepDispatch, StepBound, StepWritten, StepReadback, StepAccepted, StepDelivered}

// NextStep returns the one step that must come next at `status`; ok=false for
// terminal/unknown statuses (delivered, superseded, malformed).
func NextStep(status string) (string, bool) {
	if status == StatusAwaitingDispatch && len(StepOrder) > 0 {
		return StepOrder[0], true
	}
	for i, s := range StepOrder {
		if s == status && i+1 < len(StepOrder) {
			return StepOrder[i+1], true
		}
	}
	return "", false
}

// ApplyStep judges one status advance: `status` must currently expect exactly
// `step`. Replayed facts never reach here (the event ledger dedups first), so
// the machine is strictly forward-only.
func ApplyStep(status, step string) (string, error) {
	if !knownStep(step) {
		return status, ErrUnknownStep
	}
	if status == StatusSuperseded || status == StepDelivered {
		return status, ErrDeliveryClosed
	}
	next, ok := NextStep(status)
	if !ok {
		return status, ErrDeliveryClosed
	}
	if step != next {
		return status, fmt.Errorf("%w: %s -> %s (expected %s)", ErrStepSkipped, status, step, next)
	}
	return next, nil
}

func knownStep(s string) bool {
	for _, k := range StepOrder {
		if k == s {
			return true
		}
	}
	return false
}

// HardwareStepFor maps a delivery fact onto the HUI-4591 order fulfillment
// projection step (billing.StepShipment/WriteCard/Readback/Acceptance).
// Empty for local-domain steps (bound/delivered) — 付款事实只读消费,不推投影。
func HardwareStepFor(step string) string {
	switch step {
	case StepDispatch:
		return "shipment"
	case StepWritten:
		return "write_card"
	case StepReadback:
		return "readback"
	case StepAccepted:
		return "acceptance"
	}
	return ""
}

// IsStepEvent reports whether an event_type is one of the six step facts
// (payment is a read-only projection fact, never a ledger event).
func IsStepEvent(eventType string) bool { return knownStep(eventType) }

// IsReplacementEvent reports whether the event is the replacement marker.
func IsReplacementEvent(eventType string) bool { return eventType == "replaced" }

// ---- readback consistency verdict -------------------------------------------------

// ReadbackVerdict is the recorded readback-consistency fact.
type ReadbackVerdict struct {
	Matches bool
	// Reason: ok | empty_read | host_mismatch | code_mismatch。
	Reason string
}

// normalizeCode folds a printed/typed code into the Crockford alphabet form
// (uppercase; I/L→1, O→0 — 同 campaign.NewShortcode 词表的常见形近折叠)。
func normalizeCode(s string) string {
	r := strings.NewReplacer("i", "1", "I", "1", "l", "1", "L", "1", "o", "0", "O", "0")
	return r.Replace(strings.ToUpper(strings.TrimSpace(s)))
}

// CheckReadback judges the read-back content against the bound touchpoint.
//
//	expectedURL is server-built: the absolute /t/{code} URL when PUBLIC_BASE_URL
//	is configured, else the path form "/t/{code}". When the expectation is an
//	absolute URL the read-back must be an absolute URL on the SAME host (错误
//	域名不得通过——票面反例「错误域名不得显示部署已验证」), and its final path
//	segment must normalize to the bound code. Path-form reads only pass while
//	the expectation itself is path-form (no public base configured).
func CheckReadback(readURL, expectedURL, boundCode string) ReadbackVerdict {
	read := strings.TrimSpace(readURL)
	if read == "" {
		return ReadbackVerdict{Matches: false, Reason: "empty_read"}
	}
	readURLParsed, err := url.Parse(read)
	if err != nil {
		return ReadbackVerdict{Matches: false, Reason: "code_mismatch"}
	}
	expectedParsed, err := url.Parse(strings.TrimSpace(expectedURL))
	if err != nil {
		return ReadbackVerdict{Matches: false, Reason: "code_mismatch"}
	}
	// host 闸:期望是绝对 URL 时,回读也必须是同 host 的绝对 URL;期望是路径形式
	// 时,回读必须是路径形式(host 均空)。
	if (expectedParsed.Host != "" || readURLParsed.Host != "") && expectedParsed.Host != readURLParsed.Host {
		return ReadbackVerdict{Matches: false, Reason: "host_mismatch"}
	}
	// 路径形状闸:目录段(如 /t)必须与期望一致——/c 旧活动链冒充 /t 触点不得通过;
	// 末段(码)经 Crockford 归一后必须等于绑定码。
	expDir, _ := splitPath(expectedParsed.Path)
	readDir, readSeg := splitPath(readURLParsed.Path)
	if readDir != expDir {
		return ReadbackVerdict{Matches: false, Reason: "code_mismatch"}
	}
	if normalizeCode(readSeg) != normalizeCode(boundCode) {
		return ReadbackVerdict{Matches: false, Reason: "code_mismatch"}
	}
	return ReadbackVerdict{Matches: true, Reason: "ok"}
}

// splitPath splits a URL path into (directory prefix, final segment) after
// tolerating a trailing slash.
func splitPath(p string) (dir, seg string) {
	p = strings.TrimSuffix(p, "/")
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}
