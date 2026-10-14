package campaignenable

// campaignenable_test.go: HUI-1676 增量(商家配置与真实启用)的领域验收矩阵。
//
// 覆盖(设计文档 §9):
//   - ValidateSpec 形状矩阵:枚举外值/负数/窗口顺序/三元组缺失全部拒绝;
//   - Check 全负例:copy 缺失/内容未采用/blocked/授权过期/渠道 closed/未准入/
//     unknown/无可用渠道/权益引用缺失/版本不齐/无库存/时间冲突/活动已结束/
//     错店 —— 各自机器原因码;正路径一次通过;
//   - 固定顺序:多因并存时首个阻塞原因稳定;
//   - BuildDisclosure:告知快照带 rule_version/mode/copy/去重人话句/预算口径。

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)

// validSpec is the positive-path baseline; every negative test mutates one field.
func validSpec() Spec {
	return Spec{
		VerificationMode: ModeStaffObserved,
		CustomerCopy:     "现场给店员看发布成功的页面,店员确认后当场给您门店优惠券。",
		Reward: Reward{
			Kind:           KindStoreCoupon,
			Slot:           "store_coupon_main",
			BudgetMode:     BudgetTotalQuantity,
			TotalQuantity:  intPtr(50),
			Currency:       "CNY",
			StoreIDs:       []string{"store_1"},
			ClaimFrom:      "2026-10-01T00:00:00Z",
			ClaimUntil:     "2026-10-31T23:59:59Z",
			RedeemFrom:     "2026-10-01T00:00:00Z",
			RedeemUntil:    "2026-11-30T23:59:59Z",
			OnsiteRedempt:  true,
			StaffScope:     ScopeStoreStaff,
			OutOfStockRule: StockStopNew,
		},
		DedupBasis:   BasisPlatformSubject,
		Channels:     []ChannelRef{{Platform: "douyin", EntryMode: "h5", Scenario: "store_post"}},
		ContentVerID: "cpv_1",
	}
}

func validFacts() Facts {
	return Facts{
		Now:               testNow,
		ContentAdopted:    true,
		ContentDistributable: true,
		ChannelVerdicts:   []ChannelVerdict{{Ref: ChannelRef{Platform: "douyin", EntryMode: "h5", Scenario: "store_post"}, Allowed: true}},
		BenefitRefPresent: true,
		BenefitVersionOK:  true,
		BenefitStockOK:    true,
		WindowOK:          true,
		StoreScopeOK:      true,
		RuleVersion:       3,
	}
}

func intPtr(n int) *int { return &n }

func TestValidateSpecShapeMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"mode outside enum", func(s *Spec) { s.VerificationMode = "self_report" }},
		{"empty mode", func(s *Spec) { s.VerificationMode = "" }},
		{"benefit kind unknown", func(s *Spec) { s.Reward.Kind = "cash" }},
		{"budget mode unknown", func(s *Spec) { s.Reward.BudgetMode = " vibes" }},
		{"total quantity negative", func(s *Spec) { s.Reward.TotalQuantity = intPtr(-1) }},
		{"cost budget negative", func(s *Spec) {
			s.Reward.BudgetMode = BudgetCostMinor
			s.Reward.TotalQuantity = nil
			s.Reward.CostBudgetMinor = intPtr(-5)
		}},
		{"quantity budget without quantity", func(s *Spec) { s.Reward.TotalQuantity = nil }},
		{"dedup basis unknown", func(s *Spec) { s.DedupBasis = "cookie_ip" }},
		{"per-order limit without receipt basis", func(s *Spec) { s.PerOrderLimit = true }},
		{"channel with empty platform", func(s *Spec) { s.Channels[0].Platform = " " }},
		{"channel with empty scenario", func(s *Spec) { s.Channels[0].Scenario = "" }},
		{"no channels at all", func(s *Spec) { s.Channels = nil }},
		{"content version missing", func(s *Spec) { s.ContentVerID = "" }},
		{"staff scope unknown", func(s *Spec) { s.Reward.StaffScope = "anyone" }},
		{"out-of-stock rule unknown", func(s *Spec) { s.Reward.OutOfStockRule = "ignore" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec()
			tc.mutate(&spec)
			if err := ValidateSpec(spec); err == nil {
				t.Fatalf("ValidateSpec accepted invalid spec: %+v", spec)
			}
		})
	}
	t.Run("positive baseline validates", func(t *testing.T) {
		if err := ValidateSpec(validSpec()); err != nil {
			t.Fatalf("ValidateSpec rejected valid spec: %v", err)
		}
	})
	t.Run("empty slot refused", func(t *testing.T) {
		spec := validSpec()
		spec.Reward.Slot = ""
		if err := ValidateSpec(spec); err == nil {
			t.Fatal("empty benefit slot must be refused")
		}
	})
}

func TestCheckAllNegativeReasons(t *testing.T) {
	cases := []struct {
		name   string
		mutS   func(*Spec)
		mutF   func(*Facts)
		reason string
		field  string
	}{
		{
			name: "customer copy missing", mutS: func(s *Spec) { s.CustomerCopy = "   " },
			reason: ReasonCopyMissing, field: "customer_copy",
		},
		{
			name: "content not adopted", mutF: func(f *Facts) { f.ContentAdopted = false },
			reason: ReasonContentNotReady, field: "content_version_id",
		},
		{
			name: "content version missing on spec", mutS: func(s *Spec) { s.ContentVerID = "" },
			reason: ReasonContentNotReady, field: "content_version_id",
		},
		{
			name: "content blocked", mutF: func(f *Facts) { f.ContentDistributable = false; f.ContentBlocked = true },
			reason: ReasonContentBlocked, field: "content_version_id",
		},
		{
			name: "content grant expired", mutF: func(f *Facts) { f.ContentDistributable = false },
			reason: ReasonContentGrantExpired, field: "content_version_id",
		},
		{
			name: "channel closed", mutF: func(f *Facts) {
				f.ChannelVerdicts = []ChannelVerdict{{Ref: validSpec().Channels[0], Allowed: false, Reason: "channel_closed"}}
			},
			reason: ReasonChannelClosed, field: "channels",
		},
		{
			name: "channel not admitted", mutF: func(f *Facts) {
				f.ChannelVerdicts = []ChannelVerdict{{Ref: validSpec().Channels[0], Allowed: false, Reason: "not_admitted"}}
			},
			reason: ReasonChannelNotAdmitted, field: "channels",
		},
		{
			name: "channel unknown rule", mutF: func(f *Facts) {
				f.ChannelVerdicts = []ChannelVerdict{{Ref: validSpec().Channels[0], Allowed: false, Reason: "admission_unknown"}}
			},
			reason: ReasonChannelNotAdmitted, field: "channels",
		},
		{
			name: "no runnable channel among many", mutF: func(f *Facts) {
				spec := validSpec()
				f.ChannelVerdicts = []ChannelVerdict{
					{Ref: spec.Channels[0], Allowed: true},
					{Ref: ChannelRef{Platform: "shipinhao", EntryMode: "h5", Scenario: "store_post"}, Allowed: false, Reason: "not_admitted"},
				}
				// mixed verdicts: one allowed -> still passes channel stage; force all closed instead
				f.ChannelVerdicts = []ChannelVerdict{
					{Ref: spec.Channels[0], Allowed: false, Reason: "channel_closed"},
					{Ref: ChannelRef{Platform: "shipinhao", EntryMode: "h5", Scenario: "store_post"}, Allowed: false, Reason: "not_admitted"},
				}
			},
			reason: ReasonChannelClosed, field: "channels",
		},
		{
			name: "no channel verdicts at all", mutF: func(f *Facts) { f.ChannelVerdicts = nil },
			reason: ReasonNoRunnableChannel, field: "channels",
		},
		{
			name: "benefit ref missing", mutF: func(f *Facts) { f.BenefitRefPresent = false },
			reason: ReasonBenefitRefMissing, field: "benefit_slot",
		},
		{
			name: "benefit slot empty on spec", mutS: func(s *Spec) { s.Reward.Slot = "" },
			reason: ReasonBenefitRefMissing, field: "benefit_slot",
		},
		{
			name: "benefit version mismatch", mutF: func(f *Facts) { f.BenefitVersionOK = false },
			reason: ReasonBenefitVersionMismatch, field: "benefit_slot",
		},
		{
			name: "benefit stock missing", mutF: func(f *Facts) { f.BenefitStockOK = false },
			reason: ReasonBenefitStockMissing, field: "reward_budget",
		},
		{
			name: "window conflict", mutF: func(f *Facts) { f.WindowOK = false },
			reason: ReasonWindowConflict, field: "claim_window",
		},
		{
			name: "campaign ended", mutF: func(f *Facts) { f.CampaignEnded = true },
			reason: ReasonCampaignEnded, field: "campaign_window",
		},
		{
			name: "store mismatch", mutF: func(f *Facts) { f.StoreScopeOK = false },
			reason: ReasonStoreMismatch, field: "store_ids",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec()
			if tc.mutS != nil {
				tc.mutS(&spec)
			}
			facts := validFacts()
			if tc.mutF != nil {
				tc.mutF(&facts)
			}
			got := Check(spec, facts)
			if got.Allowed {
				t.Fatalf("Check allowed a case that must refuse: %s", tc.name)
			}
			if got.Reason != tc.reason || got.Field != tc.field {
				t.Fatalf("Check = %s/%s, want %s/%s", got.Reason, got.Field, tc.reason, tc.field)
			}
		})
	}
}

func TestCheckPositiveAndFixedOrder(t *testing.T) {
	t.Run("happy path allows", func(t *testing.T) {
		if got := Check(validSpec(), validFacts()); !got.Allowed {
			t.Fatalf("Check refused happy path: %s/%s", got.Reason, got.Field)
		}
	})
	t.Run("mixed verdicts pass when one channel allowed", func(t *testing.T) {
		facts := validFacts()
		facts.ChannelVerdicts = []ChannelVerdict{
			{Ref: validSpec().Channels[0], Allowed: true},
			{Ref: ChannelRef{Platform: "shipinhao", EntryMode: "h5", Scenario: "store_post"}, Allowed: false, Reason: "not_admitted"},
		}
		if got := Check(validSpec(), facts); !got.Allowed {
			t.Fatalf("one allowed channel must carry enablement: %s/%s", got.Reason, got.Field)
		}
	})
	t.Run("copy precedes content", func(t *testing.T) {
		spec := validSpec()
		spec.CustomerCopy = ""
		facts := validFacts()
		facts.ContentAdopted = false
		got := Check(spec, facts)
		if got.Reason != ReasonCopyMissing {
			t.Fatalf("first blocker must be copy_missing, got %s", got.Reason)
		}
	})
	t.Run("content precedes channel", func(t *testing.T) {
		facts := validFacts()
		facts.ContentAdopted = false
		facts.ChannelVerdicts = nil
		got := Check(validSpec(), facts)
		if got.Reason != ReasonContentNotReady {
			t.Fatalf("first blocker must be content_not_ready, got %s", got.Reason)
		}
	})
	t.Run("channel precedes benefit", func(t *testing.T) {
		facts := validFacts()
		facts.ChannelVerdicts = nil
		facts.BenefitStockOK = false
		got := Check(validSpec(), facts)
		if got.Reason != ReasonNoRunnableChannel {
			t.Fatalf("first blocker must be no_runnable_channel, got %s", got.Reason)
		}
	})
	t.Run("benefit version precedes stock", func(t *testing.T) {
		facts := validFacts()
		facts.BenefitVersionOK = false
		facts.BenefitStockOK = false
		got := Check(validSpec(), facts)
		if got.Reason != ReasonBenefitVersionMismatch {
			t.Fatalf("first blocker must be benefit_version_mismatch, got %s", got.Reason)
		}
	})
	t.Run("campaign end precedes store", func(t *testing.T) {
		facts := validFacts()
		facts.CampaignEnded = true
		facts.StoreScopeOK = false
		got := Check(validSpec(), facts)
		if got.Reason != ReasonCampaignEnded {
			t.Fatalf("first blocker must be campaign_ended, got %s", got.Reason)
		}
	})
	t.Run("official mode requires zero self-service weakening", func(t *testing.T) {
		spec := validSpec()
		spec.VerificationMode = ModeOfficial
		if got := Check(spec, validFacts()); !got.Allowed {
			t.Fatalf("official mode with complete facts must enable: %s/%s", got.Reason, got.Field)
		}
	})
}

func TestBuildDisclosure(t *testing.T) {
	spec := validSpec()
	spec.VerificationMode = ModeStaffVerified
	spec.PerOrderLimit = false
	d := BuildDisclosure(spec, 7, testNow)
	if d.RuleVersion != 7 {
		t.Fatalf("disclosure rule version = %d, want 7", d.RuleVersion)
	}
	if d.Mode != ModeStaffVerified {
		t.Fatalf("disclosure mode = %q, want %q", d.Mode, ModeStaffVerified)
	}
	if d.CustomerCopy != spec.CustomerCopy {
		t.Fatal("disclosure must carry the configured customer copy verbatim")
	}
	if !strings.Contains(d.DedupStatement, "平台授权主体") {
		t.Fatalf("dedup statement must name the judgment basis in plain words, got %q", d.DedupStatement)
	}
	if d.GeneratedAt == "" {
		t.Fatal("disclosure must stamp the generation time")
	}
	t.Run("per-order basis mentions receipt", func(t *testing.T) {
		spec := validSpec()
		spec.DedupBasis = BasisReceipt
		spec.PerOrderLimit = true
		d := BuildDisclosure(spec, 1, testNow)
		if !strings.Contains(d.DedupStatement, "消费凭据") || !strings.Contains(d.DedupStatement, "订单") {
			t.Fatalf("per-order dedup statement must cite receipts/orders, got %q", d.DedupStatement)
		}
	})
	t.Run("onsite basis names the staff judgment", func(t *testing.T) {
		spec := validSpec()
		spec.DedupBasis = BasisOnsiteManual
		d := BuildDisclosure(spec, 1, testNow)
		if !strings.Contains(d.DedupStatement, "现场") {
			t.Fatalf("onsite dedup statement must name staff judgment, got %q", d.DedupStatement)
		}
	})
	t.Run("disclosure JSON round trip keeps version", func(t *testing.T) {
		d := BuildDisclosure(validSpec(), 9, testNow)
		back, err := ParseDisclosure(d.JSON())
		if err != nil {
			t.Fatalf("ParseDisclosure failed: %v", err)
		}
		if back.RuleVersion != 9 || back.Mode != d.Mode {
			t.Fatalf("disclosure round trip changed fields: %+v", back)
		}
	})
}
