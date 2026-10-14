package channeladmission

import (
	"time"
)

// Machine reason codes (see package doc for the table).
const (
	ReasonNotAdmitted         = "not_admitted"
	ReasonAdmissionUnknown    = "admission_unknown"
	ReasonAdmissionExpired    = "admission_expired"
	ReasonWrongBinding        = "wrong_binding"
	ReasonChannelClosed       = "channel_closed"
	ReasonRewardNotAuthorized = "reward_play_not_authorized"
)

// GateInput is the fact set the caller presents: the moment of evaluation, the
// tenant asking, whether a reward attaches to the play, and (only for
// merchant_authorized routes) the verified merchant app reference.
type GateInput struct {
	// Now is the evaluation instant (valid_until compares against it).
	Now time.Time
	// TenantID is the tenant asking to hand off. Records scoped to "*" apply
	// to every tenant; a scoped record answers only its own tenant.
	TenantID string
	// RewardPlay marks the reward-backed variant of the flow: it requires an
	// affirmative platform approval on the record (RewardAdmitted).
	RewardPlay bool
	// MerchantAppRef is the verified merchant-authorized app reference. Empty
	// is correct for platform_app routes; merchant_authorized routes refuse
	// without it (multi-tenant/white-label must never borrow another app).
	MerchantAppRef string
}

// Decision is the deterministic verdict. Allowed=true carries the record's
// missing-field hints (they are advisory); Allowed=false carries exactly one
// machine reason — the FIRST blocking check in the fixed order below.
type Decision struct {
	Allowed    bool
	Reason     string
	ReasonText string
	Missing    []string
}

// Gate is the server-side enforcement point (票面 ProvideHandoffGate 语义):
// HUI-1670 must consult it before any handoff; hiding a button in the frontend
// is not enforcement. Fixed refusal order:
//
//	channel_closed -> not_admitted -> admission_unknown -> admission_expired
//	-> wrong_binding -> reward_play_not_authorized
//
// A closed channel refuses NEW handoffs only; existing participations keep
// their state and their fulfillment entry (执行归 HUI-1670, 本闸只挡新交接).
func Gate(in GateInput, r Record) Decision {
	dec := Decision{ReasonText: r.ReasonText, Missing: append([]string(nil), r.Missing...)}
	if r.CloseReason != "" {
		dec.Reason = ReasonChannelClosed
		return dec
	}
	switch r.Status {
	case StatusNotAdmitted:
		dec.Reason = ReasonNotAdmitted
		return dec
	case StatusUnknown:
		dec.Reason = ReasonAdmissionUnknown
		return dec
	}
	fresh, known := admissionFresh(in.Now, r.ValidUntil)
	if !known {
		// 无有效期或不可解析 = 未确立核查窗的记录,按未知规则 fail-closed
		// (过期 implies 曾有效;从未确立有效性的记录是 unknown,不是 expired)。
		dec.Reason = ReasonAdmissionUnknown
		return dec
	}
	if !fresh {
		dec.Reason = ReasonAdmissionExpired
		return dec
	}
	if !bindingMatches(in, r) {
		dec.Reason = ReasonWrongBinding
		return dec
	}
	if in.RewardPlay && !r.RewardAdmitted {
		dec.Reason = ReasonRewardNotAuthorized
		return dec
	}
	dec.Allowed = true
	return dec
}

// admissionFresh reports whether the re-check horizon covers Now. The horizon
// day itself stays valid (expiry starts at the next midnight, UTC). The second
// return is false when the horizon is absent or unparsable: such a record
// never established a validity window and is an unknown rule, not an expired
// one (fail-closed either way).
func admissionFresh(now time.Time, validUntil string) (fresh, known bool) {
	if validUntil == "" {
		return false, false
	}
	vu, err := time.Parse("2006-01-02", validUntil)
	if err != nil {
		return false, false
	}
	return now.Before(vu.AddDate(0, 0, 1)), true
}

// bindingMatches enforces tenant scope and app binding. Tenant scope "*"
// applies platform-wide; a scoped record answers only its own tenant.
// merchant_authorized records require a verified MerchantAppRef; platform_app
// and none records never take a merchant app.
func bindingMatches(in GateInput, r Record) bool {
	if r.TenantScope != "*" && r.TenantScope != in.TenantID {
		return false
	}
	switch r.AppBinding {
	case "merchant_authorized":
		return in.MerchantAppRef != ""
	case "platform_app", "none":
		return true
	default:
		// 未登记的绑定形态按未知规则 fail-closed。
		return false
	}
}
