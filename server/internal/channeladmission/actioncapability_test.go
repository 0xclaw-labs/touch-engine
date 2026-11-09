package channeladmission

import (
	"testing"
	"time"
)

// judgeNow is a fixed evaluation instant inside every seed's validity window.
var judgeNow = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

// actionFixture builds a valid admitted publish route with the four-way split
// given per environment (all six environments receive the same values unless
// a test overrides them).
func actionFixture(mut func(*ActionCapability)) ActionCapability {
	caps := EnvCapabilities{
		Navigation: CapSupported, MediaHandoff: CapSupported,
		PlatformReceipt: CapUnknown, RewardEligibility: CapUnknown,
	}
	r := ActionCapability{
		CatalogCode: "A99", Platform: "test", Method: ActionMethodH5Share,
		Scenario: ActionScenarioStorePromo, ActionKind: ActionKindPublish,
		Label: "测试动作", AppBinding: "none", TenantScope: "*", Status: StatusAdmitted,
		FieldSupport:    map[string]string{"media": CapSupported},
		RequiredInputs:  []string{"content_pack_version"},
		EvidenceGrade:   EvidenceOfficialDoc,
		OfficialSources: []string{"https://example.test/doc"},
		CheckedAt:       "2026-11-09", ValidUntil: "2027-02-07",
		Missing:     []string{"缺项示例"},
		ReasonCode:  ReasonPendingExternal,
		ReasonText:  "允许路线的原因说明",
		SeedVersion: 1,
	}
	r.Environments = sixEnvironments(caps)
	if mut != nil {
		mut(&r)
	}
	return r
}

// sixEnvironments fills all six environment keys with the same four-way split.
func sixEnvironments(c EnvCapabilities) map[string]EnvCapabilities {
	out := make(map[string]EnvCapabilities, 6)
	for _, e := range Environments {
		out[e] = c
	}
	return out
}

func q(env string, mut func(*ActionQuery)) ActionQuery {
	query := ActionQuery{Now: judgeNow, TenantID: "t_a", Environment: env}
	if mut != nil {
		mut(&query)
	}
	return query
}

// TestActionCapabilityValidate locks the enum matrices at the type layer: a
// bogus status/method/scenario/kind/evidence grade, an incomplete or unknown
// environment map, or an illegal four-way state must never parse.
func TestActionCapabilityValidate(t *testing.T) {
	caps := EnvCapabilities{
		Navigation: CapSupported, MediaHandoff: CapSupported,
		PlatformReceipt: CapUnknown, RewardEligibility: CapUnknown,
	}
	valid := actionFixture(nil)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	bad := []struct {
		name string
		mut  func(*ActionCapability)
	}{
		{"status allowed", func(r *ActionCapability) { r.Status = "allowed" }},
		{"method bogus", func(r *ActionCapability) { r.Method = "magic" }},
		{"scenario bogus", func(r *ActionCapability) { r.Scenario = "viral" }},
		{"kind bogus", func(r *ActionCapability) { r.ActionKind = "meta" }},
		{"evidence bogus", func(r *ActionCapability) { r.EvidenceGrade = "gut_feeling" }},
		{"missing env key", func(r *ActionCapability) {
			m := sixEnvironments(caps)
			delete(m, EnvIOSWechat)
			r.Environments = m
		}},
		{"unknown env key", func(r *ActionCapability) {
			m := sixEnvironments(caps)
			m["chrome_os"] = caps
			r.Environments = m
		}},
		{"illegal four-way value", func(r *ActionCapability) {
			m := sixEnvironments(caps)
			c := m[EnvDesktopBrowser]
			c.MediaHandoff = "maybe"
			m[EnvDesktopBrowser] = c
			r.Environments = m
		}},
		{"empty label", func(r *ActionCapability) { r.Label = "" }},
		{"reward open on unknown status", func(r *ActionCapability) {
			r.Status = StatusUnknown
			m := sixEnvironments(caps)
			for e, c := range m {
				c.RewardEligibility = CapSupported
				m[e] = c
			}
			r.Environments = m
		}},
		{"reward open on not_admitted status", func(r *ActionCapability) {
			r.Status = StatusNotAdmitted
			m := sixEnvironments(caps)
			for e, c := range m {
				c.RewardEligibility = CapSupported
				m[e] = c
			}
			r.Environments = m
		}},
		{"bad field support key", func(r *ActionCapability) { r.FieldSupport = map[string]string{"magic": CapSupported} }},
		{"bad field support value", func(r *ActionCapability) { r.FieldSupport = map[string]string{"media": "yes"} }},
	}
	for _, b := range bad {
		r := actionFixture(b.mut)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: must be rejected", b.name)
		}
	}
}

// TestJudgeFixedRefusalOrder pins the deterministic refusal order:
// emergency_disabled -> channel_closed -> not_admitted -> pending_external
// -> admission_expired -> wrong_binding -> reward_play_not_authorized.
func TestJudgeFixedRefusalOrder(t *testing.T) {
	env := EnvDesktopBrowser

	// 1) emergency_disabled wins over everything (独立否决,第一位).
	r := actionFixture(func(x *ActionCapability) {
		x.EmergencyDisabled = true
		x.CloseReason = "closed"
		x.Status = StatusNotAdmitted
	})
	d := JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonEmergencyDisabled {
		t.Fatalf("emergency order = %v/%s, want false/emergency_disabled", d.Allowed, d.Reason)
	}

	// 2) channel_closed next.
	r = actionFixture(func(x *ActionCapability) {
		x.CloseReason = "平台故障停用"
		x.Status = StatusNotAdmitted
	})
	d = JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonChannelClosed {
		t.Fatalf("closed order = %v/%s, want false/channel_closed", d.Allowed, d.Reason)
	}

	// 3) not_admitted next.
	r = actionFixture(func(x *ActionCapability) { x.Status = StatusNotAdmitted })
	d = JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonNotAdmitted {
		t.Fatalf("not_admitted order = %v/%s, want false/not_admitted", d.Allowed, d.Reason)
	}

	// 4) unknown -> pending_external (PENDING_EXTERNAL 语义).
	r = actionFixture(func(x *ActionCapability) { x.Status = StatusUnknown })
	d = JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonPendingExternal || !d.PendingExternal {
		t.Fatalf("unknown = %v/%s pending=%v, want false/pending_external/true", d.Allowed, d.Reason, d.PendingExternal)
	}

	// 5) never-established validity window = unknown rule, not expired.
	r = actionFixture(func(x *ActionCapability) { x.ValidUntil = "" })
	d = JudgeActionRoute(q(env, nil), r)
	if d.Reason != ReasonPendingExternal || !d.PendingExternal {
		t.Fatalf("no horizon = %s, want pending_external (fail-closed, never expired-never-valid)", d.Reason)
	}

	// 6) expired horizon.
	r = actionFixture(func(x *ActionCapability) { x.ValidUntil = "2026-11-10" })
	d = JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonAdmissionExpired {
		t.Fatalf("expired = %v/%s, want false/admission_expired", d.Allowed, d.Reason)
	}

	// 7) wrong tenant scope.
	r = actionFixture(func(x *ActionCapability) { x.TenantScope = "t_other" })
	d = JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonWrongBinding {
		t.Fatalf("cross tenant = %v/%s, want false/wrong_binding", d.Allowed, d.Reason)
	}

	// 7b) merchant_authorized without a verified app ref.
	r = actionFixture(func(x *ActionCapability) { x.AppBinding = "merchant_authorized" })
	d = JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Reason != ReasonWrongBinding {
		t.Fatalf("merchant binding without ref = %v/%s, want false/wrong_binding", d.Allowed, d.Reason)
	}
	d = JudgeActionRoute(q(env, func(x *ActionQuery) { x.MerchantAppRef = "app_verified" }), r)
	if !d.Allowed {
		t.Fatalf("merchant binding with verified ref = %v, want allowed", d.Allowed)
	}

	// 8) reward play requires supported reward_eligibility in that environment.
	r = actionFixture(func(x *ActionCapability) {
		m := sixEnvironments(EnvCapabilities{
			Navigation: CapSupported, MediaHandoff: CapSupported,
			PlatformReceipt: CapSupported, RewardEligibility: CapUnknown,
		})
		x.Environments = m
	})
	d = JudgeActionRoute(q(env, func(x *ActionQuery) { x.RewardPlay = true }), r)
	if d.Allowed || d.Reason != ReasonRewardNotAuthorized {
		t.Fatalf("reward on unknown = %v/%s, want false/reward_play_not_authorized", d.Allowed, d.Reason)
	}

	// 8b) review route: reward explicitly unsupported refuses the same way.
	r = actionFixture(func(x *ActionCapability) {
		x.Scenario = ActionScenarioReview
		x.ActionKind = ActionKindReview
		x.Method = ActionMethodDirectOpen
		m := sixEnvironments(EnvCapabilities{
			Navigation: CapSupported, MediaHandoff: CapUnsupported,
			PlatformReceipt: CapUnsupported, RewardEligibility: CapUnsupported,
		})
		x.Environments = m
	})
	d = JudgeActionRoute(q(env, func(x *ActionQuery) { x.RewardPlay = true }), r)
	if d.Allowed || d.Reason != ReasonRewardNotAuthorized {
		t.Fatalf("reward on review = %v/%s, want false/reward_play_not_authorized", d.Allowed, d.Reason)
	}

	// 8c) supported reward eligibility + admitted route passes the reward check.
	r = actionFixture(nil)
	for e, c := range r.Environments {
		c.RewardEligibility = CapSupported
		r.Environments[e] = c
	}
	d = JudgeActionRoute(q(env, func(x *ActionQuery) { x.RewardPlay = true }), r)
	if !d.Allowed {
		t.Fatalf("reward play on approved route = %v (%s), want allowed", d.Allowed, d.Reason)
	}

	// 9) the happy path: allowed with promised navigation.
	d = JudgeActionRoute(q(env, nil), actionFixture(nil))
	if !d.Allowed || !d.Promised || d.Reason != "" {
		t.Fatalf("admitted = %v/%s promised=%v, want true/''/true", d.Allowed, d.Reason, d.Promised)
	}
}

// TestPendingExternalNeverManual: 资格未知永不自动当 manual 许可 —— pending
// 路由顾客侧不承诺、不显示可完成态；只有 admitted 且系统打开目标 supported
// （或 manual_material 的顾客自足路径）才允许 promised。
func TestPendingExternalNeverManual(t *testing.T) {
	env := EnvDesktopBrowser
	r := actionFixture(func(x *ActionCapability) {
		x.Status = StatusUnknown
		x.Method = ActionMethodManualMaterial
	})
	d := JudgeActionRoute(q(env, nil), r)
	if d.Allowed || d.Promised || !d.PendingExternal {
		t.Fatalf("unknown manual_material = %v promised=%v pending=%v, want false/false/true "+
			"(unknown 永不自动当 manual 许可)", d.Allowed, d.Promised, d.PendingExternal)
	}

	// unknown navigation on an admitted route is honest: not completable.
	r = actionFixture(func(x *ActionCapability) {
		m := sixEnvironments(EnvCapabilities{
			Navigation: CapUnknown, MediaHandoff: CapUnknown,
			PlatformReceipt: CapUnknown, RewardEligibility: CapUnknown,
		})
		x.Environments = m
	})
	d = JudgeActionRoute(q(env, nil), r)
	if !d.Allowed || d.Promised {
		t.Fatalf("admitted unknown-nav = %v promised=%v, want allowed but not promised", d.Allowed, d.Promised)
	}

	// manual_material admitted route: customer-self-service completable.
	r = actionFixture(func(x *ActionCapability) {
		x.Method = ActionMethodManualMaterial
		m := sixEnvironments(EnvCapabilities{
			Navigation: CapUnsupported, MediaHandoff: CapSupported,
			PlatformReceipt: CapUnsupported, RewardEligibility: CapUnknown,
		})
		x.Environments = m
	})
	d = JudgeActionRoute(q(env, nil), r)
	if !d.Allowed || !d.Promised {
		t.Fatalf("manual admitted = %v promised=%v, want allowed+promised (诚实可完成态)", d.Allowed, d.Promised)
	}
}

// TestFourWayIndependence: 四分能力独立判定,绝不互相推断 —— Judge 原样返回
// 存储值;改变 navigation 不改写其他三值,反之亦然。
func TestFourWayIndependence(t *testing.T) {
	env := EnvDesktopBrowser
	base := EnvCapabilities{
		Navigation: CapSupported, MediaHandoff: CapUnsupported,
		PlatformReceipt: CapUnsupported, RewardEligibility: CapUnsupported,
	}
	r := actionFixture(func(x *ActionCapability) {
		x.Method = ActionMethodDirectOpen
		x.Environments = sixEnvironments(base)
	})
	d := JudgeActionRoute(q(env, nil), r)
	got := d.Caps
	if got != base {
		t.Fatalf("caps mutated: %+v, want %+v", got, base)
	}

	// Flip navigation to unsupported: the other three must stay put.
	r.Environments = sixEnvironments(EnvCapabilities{
		Navigation: CapUnsupported, MediaHandoff: CapUnsupported,
		PlatformReceipt: CapUnsupported, RewardEligibility: CapUnsupported,
	})
	d = JudgeActionRoute(q(env, nil), r)
	if d.Caps.MediaHandoff != CapUnsupported || d.Caps.PlatformReceipt != CapUnsupported ||
		d.Caps.RewardEligibility != CapUnsupported {
		t.Fatalf("navigation flip leaked into other cells: %+v", d.Caps)
	}

	// Flip media_handoff to supported: navigation/receipt/reward stay put.
	r.Environments = sixEnvironments(EnvCapabilities{
		Navigation: CapUnsupported, MediaHandoff: CapSupported,
		PlatformReceipt: CapUnsupported, RewardEligibility: CapUnsupported,
	})
	d = JudgeActionRoute(q(env, nil), r)
	if d.Caps.Navigation != CapUnsupported || d.Caps.PlatformReceipt != CapUnsupported {
		t.Fatalf("media flip leaked: %+v", d.Caps)
	}
}

// TestEnvRequired: 未知环境 fail-closed 为 pending,绝不借用其他环境的值。
func TestEnvRequired(t *testing.T) {
	r := actionFixture(nil)
	for _, env := range []string{"", "chrome_os", "IOS_SAFARI"} {
		d := JudgeActionRoute(q(env, nil), r)
		if d.Allowed || d.Reason != ReasonPendingExternal || !d.PendingExternal {
			t.Fatalf("env %q = %v/%s pending=%v, want false/pending_external/true", env, d.Allowed, d.Reason, d.PendingExternal)
		}
	}
	// Canonical casing works.
	d := JudgeActionRoute(q(EnvIOSSafari, nil), r)
	if !d.Allowed {
		t.Fatalf("ios_safari = %v (%s), want allowed", d.Allowed, d.Reason)
	}
}
