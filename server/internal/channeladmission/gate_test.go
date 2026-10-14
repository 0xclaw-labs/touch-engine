package channeladmission

import (
	"testing"
	"time"
)

func gateNow() time.Time { return time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) }

func admittedRecord(mod func(*Record)) Record {
	r := Record{
		Platform: "douyin", EntryMode: EntryMiniapp, Scenario: "reward_task",
		AppBinding: "platform_app", TenantScope: "*", Status: StatusAdmitted,
		ReasonCode: "ok", ReasonText: "获批（测试合成记录，非官方事实）",
		LastCheckedAt: "2026-10-14", ValidUntil: "2027-01-12",
		Capabilities: Capabilities{Media: CapSupported, Title: CapSupported, Body: CapSupported,
			Topic: CapSupported, POI: CapUnknown, Receipt: CapUnknown, Callback: CapUnknown},
	}
	if mod != nil {
		mod(&r)
	}
	return r
}

// TestGateSeedHasNoAdmittedRoute locks the honest baseline: as of the
// 2026-10-14 research, no route is admitted. Every seed record must refuse,
// with its own machine reason (1x not_admitted + 4x admission_unknown) — the
// acceptance "尚无获准路线时诚实输出缺项".
func TestGateSeedHasNoAdmittedRoute(t *testing.T) {
	seed, err := EmbeddedSeed()
	if err != nil {
		t.Fatalf("embedded seed: %v", err)
	}
	notAdmitted, unknown := 0, 0
	for _, r := range seed.Records {
		d := Gate(GateInput{Now: gateNow(), TenantID: "t_any"}, r)
		if d.Allowed {
			t.Errorf("%s must not be allowed as of 2026-10-14 (no platform approval exists)", r.IdentityKey())
		}
		if d.Reason == "" {
			t.Errorf("%s refusal must carry a machine reason", r.IdentityKey())
		}
		if len(d.Missing) == 0 {
			t.Errorf("%s refusal must carry honest missing-item hints", r.IdentityKey())
		}
		switch d.Reason {
		case "not_admitted":
			notAdmitted++
		case "admission_unknown":
			unknown++
		default:
			t.Errorf("%s unexpected reason %q", r.IdentityKey(), d.Reason)
		}
	}
	if notAdmitted != 1 || unknown != 4 {
		t.Fatalf("seed refusals = %d not_admitted + %d unknown, want 1 + 4", notAdmitted, unknown)
	}
}

// TestGateUnknownNeverAllowed: unknown is neither allowed nor "missing but
// proceed" — all three unknown seed routes refuse with admission_unknown.
func TestGateUnknownNeverAllowed(t *testing.T) {
	for _, key := range []string{"douyin|miniapp|reward_task", "xiaohongshu|miniapp|store_promo", "bilibili|h5|store_promo", "kuaishou|native|store_promo"} {
		seed, _ := EmbeddedSeed()
		var rec *Record
		for i := range seed.Records {
			if seed.Records[i].IdentityKey() == key {
				rec = &seed.Records[i]
			}
		}
		if rec == nil {
			t.Fatalf("seed lost %s", key)
		}
		if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, *rec); d.Allowed || d.Reason != "admission_unknown" {
			t.Errorf("%s: unknown must refuse with admission_unknown, got allowed=%v reason=%q", key, d.Allowed, d.Reason)
		}
	}
}

// TestGateNotAdmittedDouyinH5: the officially excluded marketing scenario
// refuses with not_admitted, regardless of any technical capability hope.
func TestGateNotAdmittedDouyinH5(t *testing.T) {
	seed, _ := EmbeddedSeed()
	for i := range seed.Records {
		if seed.Records[i].IdentityKey() == "douyin|h5|store_promo" {
			d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, seed.Records[i])
			if d.Allowed || d.Reason != "not_admitted" {
				t.Fatalf("douyin h5 store_promo: want refused/not_admitted, got allowed=%v reason=%q", d.Allowed, d.Reason)
			}
			return
		}
	}
	t.Fatal("seed lost douyin|h5|store_promo")
}

// TestGateExpiredAdmission: an admitted record past valid_until refuses
// (admission_expired); it stays valid through the last day of the horizon.
func TestGateExpiredAdmission(t *testing.T) {
	r := admittedRecord(nil)
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r); !d.Allowed {
		t.Fatalf("within valid_until must be allowed, got %q", d.Reason)
	}
	r.ValidUntil = "2026-10-13"
	d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r)
	if d.Allowed || d.Reason != "admission_expired" {
		t.Fatalf("past valid_until must refuse with admission_expired, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	// same record, Now on the last day of the horizon: still allowed.
	if d := Gate(GateInput{Now: time.Date(2026, 10, 13, 23, 0, 0, 0, time.UTC), TenantID: "t1"}, r); !d.Allowed {
		t.Fatalf("valid_until day itself must stay allowed, got %q", d.Reason)
	}
	// unparsable/empty audit date fails closed as unknown rule.
	r.ValidUntil = ""
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r); d.Allowed || d.Reason != "admission_unknown" {
		t.Fatalf("empty valid_until must fail closed, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
}

// TestGateWrongBinding: tenant-scoped records answer only their own tenant,
// and merchant_authorized routes need a verified merchant app ref — 错
// app/tenant 同样拒绝 (multi-tenant / white-label must never borrow another
// merchant's AppID).
func TestGateWrongBinding(t *testing.T) {
	r := admittedRecord(func(r *Record) { r.TenantScope = "t_merchant" })
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t_other"}, r); d.Allowed || d.Reason != "wrong_binding" {
		t.Fatalf("foreign tenant must refuse with wrong_binding, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t_merchant"}, r); !d.Allowed {
		t.Fatalf("own tenant must pass, got %q", d.Reason)
	}
	m := admittedRecord(func(r *Record) { r.AppBinding = "merchant_authorized" })
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, m); d.Allowed || d.Reason != "wrong_binding" {
		t.Fatalf("merchant_authorized without verified app ref must refuse, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1", MerchantAppRef: "merchant-app-1"}, m); !d.Allowed {
		t.Fatalf("merchant_authorized with verified ref must pass, got %q", d.Reason)
	}
}

// TestGateClosedChannelKeepsParticipation: a closed route stops new handoffs
// (channel_closed) — existing participations keep state and fulfillment entry,
// enforced downstream by HUI-1670 (documented in the package contract).
func TestGateClosedChannelKeepsParticipation(t *testing.T) {
	r := admittedRecord(func(r *Record) { r.CloseReason = "平台规则变更，暂停该路线" })
	d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r)
	if d.Allowed || d.Reason != "channel_closed" {
		t.Fatalf("closed channel must refuse new handoffs, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
}

// TestGateRewardPlayNeedsApproval: an admitted technical route still cannot
// back a reward play without an affirmative platform approval; with the
// approval recorded, the reward play passes.
func TestGateRewardPlayNeedsApproval(t *testing.T) {
	r := admittedRecord(nil) // reward_admitted=false
	d := Gate(GateInput{Now: gateNow(), TenantID: "t1", RewardPlay: true}, r)
	if d.Allowed || d.Reason != "reward_play_not_authorized" {
		t.Fatalf("reward play without approval must refuse, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	r.RewardAdmitted = true
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1", RewardPlay: true}, r); !d.Allowed {
		t.Fatalf("reward play with recorded approval must pass, got %q", d.Reason)
	}
	// plain handoff (no reward) is unaffected by reward approval state.
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r); !d.Allowed {
		t.Fatalf("plain handoff must pass, got %q", d.Reason)
	}
}

// TestGateFixedRefusalOrder: one compound record proves the documented order —
// closed beats expired beats everything else below it.
func TestGateFixedRefusalOrder(t *testing.T) {
	r := admittedRecord(func(r *Record) {
		r.CloseReason = "渠道关闭"
		r.ValidUntil = "2026-01-01"
		r.Status = StatusUnknown
	})
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r); d.Reason != "channel_closed" {
		t.Fatalf("closed must win the fixed order, got %q", d.Reason)
	}
	r.CloseReason = ""
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r); d.Reason != "admission_unknown" {
		t.Fatalf("unknown must beat expiry in the fixed order, got %q", d.Reason)
	}
	r.Status = StatusAdmitted
	if d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r); d.Reason != "admission_expired" {
		t.Fatalf("expired must beat binding/reward in the fixed order, got %q", d.Reason)
	}
}

// TestGateAllowedCarriesHints: the one allowed shape carries the missing-field
// hints and no refusal reason, so 1670/1896 surfaces can render 缺字段提示.
func TestGateAllowedCarriesHints(t *testing.T) {
	r := admittedRecord(func(r *Record) { r.Missing = []string{"POI 带入未验证"} })
	d := Gate(GateInput{Now: gateNow(), TenantID: "t1"}, r)
	if !d.Allowed || d.Reason != "" {
		t.Fatalf("want allowed with empty reason, got allowed=%v reason=%q", d.Allowed, d.Reason)
	}
	if len(d.Missing) != 1 || d.Missing[0] != "POI 带入未验证" {
		t.Fatalf("hints must pass through, got %v", d.Missing)
	}
}
