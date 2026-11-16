package store

import (
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
)

// 沿原订单/权威事件写路径验证统计；这是本地工程事实，不是正式支付证据。
// 已有支付不能因为开通尚未完成或失败而从实际费用中消失。
func TestDashboardPaidOrdersSurviveActivationStateChanges(t *testing.T) {
	w := seedOpsWorld(t)
	const createdAt = "2026-10-14T08:00:00Z"
	start, end := opsWinBounds(t)
	for _, kind := range []string{billing.KindSubscription, billing.KindHardware} {
		t.Run(kind, func(t *testing.T) {
			order, _, err := w.s.CreateBillingOrder(NewBillingOrder{
				TenantID: w.tenA, Kind: kind, Action: billing.ActionNew,
				PlanCode: billing.PlanMonthly, PriceRef: "engineering-price", PriceSnapshot: `{"price_minor":1200}`,
				AmountMinor: 1200, Currency: "CNY", IdempotencyKey: "paid-" + kind,
				ActorPrincipal: "engineering-owner", ActorRole: "org_owner",
			}, createdAt)
			must(t, err)
			paid := billing.AuthorityEventInput{
				Source: "engineering-authority", EventID: "pay-" + kind, Type: billing.EventPaymentPaid,
				OccurredAt: "2026-10-14T09:00:00Z", Payload: billing.EventPayload{PaymentRef: "engineering-pay-" + kind},
			}
			order, replayed, err := w.s.ApplyBillingOrderEvent(w.tenA, order.ID, paid, "2026-10-14T10:00:00Z")
			must(t, err)
			if replayed || order.Status != billing.OrderOpening {
				t.Fatalf("payment should leave opening order, got %+v replay=%v", order, replayed)
			}
			_, replayed, err = w.s.ApplyBillingOrderEvent(w.tenA, order.ID, paid, "2026-10-14T10:01:00Z")
			must(t, err)
			if !replayed {
				t.Fatal("payment replay must not create another payment fact")
			}
			for _, state := range []string{billing.OrderOpening, billing.OrderFailed} {
				if state == billing.OrderFailed {
					_, _, err = w.s.ApplyBillingOrderEvent(w.tenA, order.ID, billing.AuthorityEventInput{
						Source: "engineering-authority", EventID: "fail-" + kind, Type: billing.EventActivationFailed,
						OccurredAt: "2026-10-14T11:00:00Z", Payload: billing.EventPayload{FailReason: "engineering-activation-failed"},
					}, "2026-10-14T11:00:00Z")
					must(t, err)
				}
				facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
				must(t, err)
				amount, count := facts.CostRecon.SubscriptionPaidMinor, facts.CostRecon.SubscriptionPaidOrders
				if kind == billing.KindHardware {
					amount, count = facts.CostRecon.HardwarePaidMinor, facts.CostRecon.HardwarePaidOrders
				}
				if amount == nil || *amount != 1200 || count != 1 {
					t.Errorf("%s paid order in %s disappeared: amount=%v orders=%d; want 1200 and 1", kind, state, amount, count)
				}
			}
		})
	}
}

func newAnalyticsPaidOrder(t *testing.T, w *opsWorld, key, kind, currency, createdAt, paidAt, receivedAt string, amount int64) BillingOrderRow {
	t.Helper()
	order, _, err := w.s.CreateBillingOrder(NewBillingOrder{
		TenantID: w.tenA, Kind: kind, Action: billing.ActionNew, PlanCode: billing.PlanMonthly,
		PriceRef: "engineering-price", PriceSnapshot: `{}`, AmountMinor: amount, Currency: currency,
		IdempotencyKey: key, ActorPrincipal: "engineering-owner", ActorRole: "org_owner",
	}, createdAt)
	must(t, err)
	order, _, err = w.s.ApplyBillingOrderEvent(w.tenA, order.ID, billing.AuthorityEventInput{
		Source: "engineering-authority", EventID: "pay-" + key, Type: billing.EventPaymentPaid,
		OccurredAt: paidAt, Payload: billing.EventPayload{PaymentRef: "engineering-pay-" + key},
	}, receivedAt)
	must(t, err)
	return order
}

func TestDashboardPaymentFactsUseAuthorityWindowAndDeduplicateOrder(t *testing.T) {
	w := seedOpsWorld(t)
	order := newAnalyticsPaidOrder(t, w, "late", billing.KindSubscription, "CNY", "2026-10-13T08:00:00Z", "2026-10-14T09:00:00Z", "2026-10-15T10:00:00Z", 1200)
	// 同一笔付款的新回调标识也不能加成第二单或移到新回调的到达日。
	_, _, err := w.s.ApplyBillingOrderEvent(w.tenA, order.ID, billing.AuthorityEventInput{
		Source: "engineering-authority", EventID: "pay-late-repeat", Type: billing.EventPaymentPaid,
		OccurredAt: "2026-10-14T09:00:00Z", Payload: billing.EventPayload{PaymentRef: "engineering-pay-late"},
	}, "2026-10-16T10:00:00Z")
	must(t, err)
	newAnalyticsPaidOrder(t, w, "next-day", billing.KindSubscription, "CNY", "2026-10-14T10:00:00Z", "2026-10-15T11:00:00Z", "2026-10-15T11:00:00Z", 800)
	// 历史 active 投影没有支付事实时不能仅凭状态猜成已付款。
	w.rawBillingOrder(t, "legacy-active", billing.KindSubscription, billing.ActionNew, 9000, billing.OrderActive, "2026-10-14T08:00:00Z")
	for _, tc := range []struct {
		day   string
		count int64
		paid  int64
	}{{"2026-10-13", 0, 0}, {"2026-10-14", 1, 1200}, {"2026-10-15", 1, 800}, {"2026-10-16", 0, 0}} {
		start, err := time.Parse(time.RFC3339, tc.day+"T00:00:00Z")
		must(t, err)
		facts, _, err := w.s.DashboardOps(w.tenA, "", start, start.Add(24*time.Hour-time.Second), OpsFilter{})
		must(t, err)
		cr := facts.CostRecon
		if cr.SubscriptionPaidOrders != tc.count || (tc.count == 0 && cr.SubscriptionPaidMinor != nil) ||
			(tc.count > 0 && (cr.SubscriptionPaidMinor == nil || *cr.SubscriptionPaidMinor != tc.paid)) {
			t.Errorf("payment window %s: count=%d amount=%v, want count=%d amount=%d (none remains unknown)", tc.day, cr.SubscriptionPaidOrders, cr.SubscriptionPaidMinor, tc.count, tc.paid)
		}
	}
}

func TestDashboardPaymentAmountsRequireOneKnownCurrency(t *testing.T) {
	for _, tc := range []struct {
		name, firstCurrency, secondCurrency string
		amount                              int64
		known                               bool
	}{
		{"mixed-currency", "CNY", "USD", 1200, false},
		{"missing-currency", "CNY", "", 1200, false},
		{"known-zero", "CNY", "CNY", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := seedOpsWorld(t)
			const stamp = "2026-10-14T09:00:00Z"
			for i, currency := range []string{tc.firstCurrency, tc.secondCurrency} {
				key := "currency-first"
				if i == 1 {
					key = "currency-second"
				}
				order := newAnalyticsPaidOrder(t, w, key, billing.KindSubscription, currency, stamp, stamp, stamp, tc.amount)
				_, _, err := w.s.ApplyBillingOrderEvent(w.tenA, order.ID, billing.AuthorityEventInput{
					Source: "engineering-authority", EventID: "active-" + key, Type: billing.EventActivationActive,
					OccurredAt: stamp, Payload: billing.EventPayload{SubscriptionRef: "engineering-sub-" + key},
				}, stamp)
				must(t, err)
				if !tc.known {
					_, _, err := w.s.ApplyBillingRefund(w.tenA, order.ID, billing.RefundInput{
						RefundRef: "engineering-refund-" + key, AmountMinor: 100, Currency: currency,
						Reason: "工程退款事实", InvoiceStatus: billing.InvoiceNone, Auditor: "engineering-auditor",
						Source: "engineering-authority", EventID: "refund-" + key, OccurredAt: stamp,
					}, stamp)
					must(t, err)
				}
			}
			start, end := opsWinBounds(t)
			facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
			must(t, err)
			amount := facts.CostRecon.SubscriptionPaidMinor
			if facts.CostRecon.SubscriptionPaidOrders != 2 || (amount != nil) != tc.known || (tc.known && *amount != 0) {
				t.Fatalf("currency completeness: count=%d amount=%v, want 2 and known=%v", facts.CostRecon.SubscriptionPaidOrders, amount, tc.known)
			}
			if !tc.known && (facts.CostRecon.SubscriptionRefunds != 2 || facts.CostRecon.SubscriptionRefundMinor != nil || facts.CostRecon.SubscriptionRefundCurrency != "") {
				t.Fatalf("incomplete refund currency must keep total unknown: %+v", facts.CostRecon)
			}
		})
	}
}

func TestDashboardHardwareRefundsUseOrderKindAndAuthorityWindow(t *testing.T) {
	for _, receivedAt := range []string{"2026-10-14T12:00:00Z", "2026-10-15T12:00:00Z"} {
		t.Run(receivedAt[:10], func(t *testing.T) {
			w := seedOpsWorld(t)
			const stamp = "2026-10-14T09:00:00Z"
			sub := newAnalyticsPaidOrder(t, w, "refund-sub", billing.KindSubscription, "CNY", stamp, stamp, stamp, 2000)
			hw := newAnalyticsPaidOrder(t, w, "refund-hw", billing.KindHardware, "CNY", stamp, stamp, stamp, 1000)
			for _, row := range []struct {
				key, order string
				amount     int64
			}{{"subscription", sub.ID, 600}, {"hardware-part-one", hw.ID, 300}, {"hardware-part-two", hw.ID, 200}} {
				in := billing.RefundInput{
					RefundRef: "engineering-refund-" + row.key, AmountMinor: row.amount, Currency: "CNY",
					Reason: "工程退款事实", InvoiceStatus: billing.InvoiceNone, Auditor: "engineering-auditor", Source: "engineering-authority",
					EventID: row.key, OccurredAt: "2026-10-14T11:00:00Z",
				}
				_, replayed, err := w.s.ApplyBillingRefund(w.tenA, row.order, in, receivedAt)
				must(t, err)
				if replayed {
					t.Fatal("first refund is not a replay")
				}
				_, replayed, err = w.s.ApplyBillingRefund(w.tenA, row.order, in, receivedAt)
				must(t, err)
				if !replayed {
					t.Fatal("same refund event must replay")
				}
			}
			start, end := opsWinBounds(t)
			facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
			must(t, err)
			if facts.CostRecon.HardwareRefundMinor == nil || *facts.CostRecon.HardwareRefundMinor != 500 || facts.CostRecon.HardwareRefunds != 2 {
				t.Fatalf("hardware refund must exclude subscription and use authority time: amount/count=%v/%d, want 500/2", facts.CostRecon.HardwareRefundMinor, facts.CostRecon.HardwareRefunds)
			}
			if facts.CostRecon.SubscriptionRefundMinor == nil || *facts.CostRecon.SubscriptionRefundMinor != 600 || facts.CostRecon.SubscriptionRefunds != 1 {
				t.Fatalf("subscription refund must remain separate: %+v", facts.CostRecon)
			}
			if facts.CostRecon.SubscriptionPaidMinor == nil || *facts.CostRecon.SubscriptionPaidMinor != 2000 || facts.CostRecon.HardwarePaidMinor == nil || *facts.CostRecon.HardwarePaidMinor != 1000 {
				t.Fatalf("partial refunds must not net away original payment: %+v", facts.CostRecon)
			}
			_, _, err = w.s.ApplyBillingRefund(w.tenA, hw.ID, billing.RefundInput{
				RefundRef: "engineering-final-refund", AmountMinor: 500, Currency: "CNY",
				Reason: "退回剩余金额", InvoiceStatus: billing.InvoiceNone, Auditor: "engineering-auditor",
				Source: "engineering-authority", EventID: "hardware-final", OccurredAt: "2026-10-14T13:00:00Z",
			}, receivedAt)
			must(t, err)
			facts, _, err = w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
			must(t, err)
			if facts.CostRecon.HardwareRefundMinor == nil || *facts.CostRecon.HardwareRefundMinor != 1000 || facts.CostRecon.HardwareRefunds != 3 || facts.CostRecon.HardwarePaidMinor == nil || *facts.CostRecon.HardwarePaidMinor != 1000 {
				t.Fatalf("full refund remains separate from original payment: %+v", facts.CostRecon)
			}
		})
	}
}

// 历史写路径接受过无法解析的发生时间。它们可能落入任意窗口，不能
// 通过 WHERE 过滤后把「不知道属于哪一天」写成零或完整的部分合计。
func TestDashboardUnknownRefundTimeNeverBecomesKnownTotal(t *testing.T) {
	for _, kind := range []string{billing.KindSubscription, billing.KindHardware} {
		for _, withKnown := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/only-unknown", true: "/known-and-unknown"}[withKnown], func(t *testing.T) {
				w := seedOpsWorld(t)
				const stamp = "2026-10-14T09:00:00Z"
				order := newAnalyticsPaidOrder(t, w, "unknown-refund", kind, "CNY", stamp, stamp, stamp, 2000)
				refund := func(key, occurredAt string) {
					_, _, err := w.s.ApplyBillingRefund(w.tenA, order.ID, billing.RefundInput{
						RefundRef: key, AmountMinor: 300, Currency: "CNY", Reason: "工程退款事实",
						InvoiceStatus: billing.InvoiceNone, Auditor: "engineering-auditor",
						Source: "engineering-authority", EventID: key, OccurredAt: occurredAt,
					}, stamp)
					must(t, err)
				}
				refund("unknown-time", "bad-time")
				if withKnown {
					refund("known-time", stamp)
				}
				start, end := opsWinBounds(t)
				facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
				must(t, err)
				amount := facts.CostRecon.SubscriptionRefundMinor
				if kind == billing.KindHardware {
					amount = facts.CostRecon.HardwareRefundMinor
				}
				if amount != nil {
					t.Fatalf("unknown occurrence time cannot produce a complete refund total: got %d", *amount)
				}
				// 异类订单、其他租户和真正没有退款的范围仍有确定的零。
				otherKind := billing.KindHardware
				if kind == otherKind {
					otherKind = billing.KindSubscription
				}
				for _, scope := range [][2]string{{w.tenA, otherKind}, {w.tenB, kind}} {
					other, err := w.s.opsRefunds(scope[0], scope[1], newOpsWindow(start, end))
					must(t, err)
					if other.Amount == nil || *other.Amount != 0 || other.Count != 0 {
						t.Fatalf("unrelated unknown time contaminated a known empty ledger: %+v", other)
					}
				}
			})
		}
	}
}

func TestDashboardUnknownPaymentTimeNeverBecomesKnownPartialTotal(t *testing.T) {
	w := seedOpsWorld(t)
	const stamp = "2026-10-14T09:00:00Z"
	newAnalyticsPaidOrder(t, w, "known-time", billing.KindSubscription, "CNY", stamp, stamp, stamp, 1200)
	unknown := newAnalyticsPaidOrder(t, w, "unknown-time", billing.KindSubscription, "CNY", stamp, "bad-time", stamp, 800)
	// 同单另一个回调日期有效，也不能证明无法解析的原付款时刻不更早。
	_, _, err := w.s.ApplyBillingOrderEvent(w.tenA, unknown.ID, billing.AuthorityEventInput{
		Source: "engineering-authority", EventID: "known-repeat", Type: billing.EventPaymentPaid,
		OccurredAt: stamp, Payload: billing.EventPayload{PaymentRef: "engineering-pay-unknown-time"},
	}, stamp)
	must(t, err)
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	if facts.CostRecon.SubscriptionPaidMinor != nil {
		t.Fatalf("payment with unknown earliest time cannot produce a complete total: %d", *facts.CostRecon.SubscriptionPaidMinor)
	}
}

func TestDashboardPaymentWindowUsesInclusiveUTCSeconds(t *testing.T) {
	w := seedOpsWorld(t)
	const createdAt = "2026-10-14T08:00:00Z"
	for _, row := range [][2]string{
		{"last-second", "2026-10-14T23:59:59.900Z"},
		{"same-second-offset", "2026-10-15T07:59:59.900+08:00"},
		{"next-second", "2026-10-15T00:00:00Z"},
	} {
		newAnalyticsPaidOrder(t, w, row[0], billing.KindHardware, "CNY", createdAt, row[1], "2026-10-15T08:00:00Z", 1200)
	}
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	if facts.CostRecon.HardwarePaidOrders != 2 || facts.CostRecon.HardwarePaidMinor == nil || *facts.CostRecon.HardwarePaidMinor != 2400 {
		t.Fatalf("final UTC second is inclusive, next second is excluded: %+v", facts.CostRecon)
	}
}
