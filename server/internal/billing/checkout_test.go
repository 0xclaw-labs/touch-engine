// checkout_test.go: HUI-4664 原单接续纯函数测试(裁决 + 状态机全迁移路径)。
//
// 覆盖(R3 §4.1 单测点 1/2 的任务适配):
//   - AdjudicatePaymentFact:金额不符/币种不符/payment_ref 空/引用不符/
//     匹配通过/同形掩蔽不泄漏方向;
//   - ApplyCheckoutEvent 状态机:pending→paid→activated 主路径;paid→failed
//     如实留因;failed→activated 补偿(可重试激活);幂等重放零改写;
//     pending 直达 activated 拒绝;activated 迟到失败不追溯;二次支付矛盾。

package billing

import "testing"

func checkoutStateForTest() CheckoutRefState {
	return CheckoutRefState{
		ID: "bcr_1", TenantID: "tenA", CheckoutRef: "ck_abc", OrderRef: "cph_1",
		State: CheckoutStatePending, AmountMinor: 990000, Currency: "CNY",
	}
}

func TestAdjudicatePaymentFactVerdicts(t *testing.T) {
	ref := checkoutStateForTest()
	cases := []struct {
		name    string
		ref     CheckoutRefState
		fact    PaymentFact
		allowed bool
	}{
		{"match", ref, PaymentFact{PaymentRef: "pay_1", CheckoutRef: "ck_abc", AmountMinor: 990000, Currency: "CNY"}, true},
		{"amount_mismatch", ref, PaymentFact{PaymentRef: "pay_1", CheckoutRef: "ck_abc", AmountMinor: 880000, Currency: "CNY"}, false},
		{"currency_mismatch", ref, PaymentFact{PaymentRef: "pay_1", CheckoutRef: "ck_abc", AmountMinor: 990000, Currency: "USD"}, false},
		{"empty_payment_ref", ref, PaymentFact{PaymentRef: "", CheckoutRef: "ck_abc", AmountMinor: 990000, Currency: "CNY"}, false},
		{"checkout_ref_drift", ref, PaymentFact{PaymentRef: "pay_1", CheckoutRef: "ck_other", AmountMinor: 990000, Currency: "CNY"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := AdjudicatePaymentFact(tc.ref, tc.fact)
			if v.Allowed != tc.allowed {
				t.Fatalf("allowed = %v, want %v (verdict %+v)", v.Allowed, tc.allowed, v)
			}
			// 同形掩蔽:一切不相容都是同一个 mismatch 码,不泄漏方向。
			if !v.Allowed && v.Reason != PaymentReasonMismatch {
				t.Fatalf("reason = %q, want %q", v.Reason, PaymentReasonMismatch)
			}
		})
	}
}

func paidStateForTest() CheckoutRefState {
	st := checkoutStateForTest()
	st.State = CheckoutStatePaid
	st.PaymentRef = "pay_1"
	return st
}

func TestCheckoutStateMachineHappyPath(t *testing.T) {
	st := checkoutStateForTest()
	paid, err := ApplyCheckoutEvent(st, AuthorityEventInput{Type: EventPaymentPaid, Payload: EventPayload{PaymentRef: "pay_1"}})
	if err != nil || paid.State != CheckoutStatePaid || paid.PaymentRef != "pay_1" {
		t.Fatalf("pending→paid: %+v %v", paid, err)
	}
	activated, err := ApplyCheckoutEvent(paid, AuthorityEventInput{Type: EventActivationActive})
	if err != nil || activated.State != CheckoutStateActivated || activated.PaymentRef != "pay_1" {
		t.Fatalf("paid→activated: %+v %v", activated, err)
	}
}

func TestCheckoutStateMachineCompensationPaths(t *testing.T) {
	// paid→failed 如实留因;failed→activated 补偿(可重试激活)。
	failed, err := ApplyCheckoutEvent(paidStateForTest(), AuthorityEventInput{Type: EventActivationFailed, Payload: EventPayload{FailReason: "provider_timeout"}})
	if err != nil || failed.State != CheckoutStateFailed || failed.FailReason != "provider_timeout" {
		t.Fatalf("paid→failed: %+v %v", failed, err)
	}
	retried, err := ApplyCheckoutEvent(failed, AuthorityEventInput{Type: EventActivationActive})
	if err != nil || retried.State != CheckoutStateActivated || retried.FailReason != "" {
		t.Fatalf("failed→activated(可重试激活): %+v %v", retried, err)
	}
	// failed 重申失败事实:留因如实更新(投影跟随权威最新事实)。
	refailed, err := ApplyCheckoutEvent(failed, AuthorityEventInput{Type: EventActivationFailed, Payload: EventPayload{FailReason: "provider_down"}})
	if err != nil || refailed.FailReason != "provider_down" {
		t.Fatalf("failed 留因更新: %+v %v", refailed, err)
	}
	// 缺省留因落到确定性缺省值。
	defaultReason, err := ApplyCheckoutEvent(paidStateForTest(), AuthorityEventInput{Type: EventActivationFailed})
	if err != nil || defaultReason.FailReason != "activation_failed" {
		t.Fatalf("缺省留因: %+v %v", defaultReason, err)
	}
}

func TestCheckoutStateMachineIdempotentReplays(t *testing.T) {
	paid := paidStateForTest()
	replay, err := ApplyCheckoutEvent(paid, AuthorityEventInput{Type: EventPaymentPaid, Payload: EventPayload{PaymentRef: "pay_1"}})
	if err != nil || replay != paid {
		t.Fatalf("同 payment_ref 重放必须零改写: %+v %v", replay, err)
	}
	activated, _ := ApplyCheckoutEvent(paid, AuthorityEventInput{Type: EventActivationActive})
	again, err := ApplyCheckoutEvent(activated, AuthorityEventInput{Type: EventActivationActive})
	if err != nil || again != activated {
		t.Fatalf("activated 重放必须零改写: %+v %v", again, err)
	}
	// failed 后同 payment_ref 重放同样幂等(已携带支付事实)。
	failed, _ := ApplyCheckoutEvent(paid, AuthorityEventInput{Type: EventActivationFailed, Payload: EventPayload{FailReason: "x"}})
	if _, err := ApplyCheckoutEvent(failed, AuthorityEventInput{Type: EventPaymentPaid, Payload: EventPayload{PaymentRef: "pay_1"}}); err != nil {
		t.Fatalf("failed 后同引用支付重放: %v", err)
	}
}

func TestCheckoutStateMachineRefusals(t *testing.T) {
	pending := checkoutStateForTest()
	paid := paidStateForTest()
	activated, _ := ApplyCheckoutEvent(paid, AuthorityEventInput{Type: EventActivationActive})

	// pending 直达 activated = 跳过支付事实,拒绝。
	if _, err := ApplyCheckoutEvent(pending, AuthorityEventInput{Type: EventActivationActive}); err != ErrInvalidTransition {
		t.Fatalf("pending→activated = %v, want ErrInvalidTransition", err)
	}
	// pending 阶段没有支付事实可失败。
	if _, err := ApplyCheckoutEvent(pending, AuthorityEventInput{Type: EventActivationFailed}); err != ErrInvalidTransition {
		t.Fatalf("pending→failed = %v, want ErrInvalidTransition", err)
	}
	// 已生效服务期不被迟到的失败事件追溯削减。
	if _, err := ApplyCheckoutEvent(activated, AuthorityEventInput{Type: EventActivationFailed}); err != ErrInvalidTransition {
		t.Fatalf("activated→failed = %v, want ErrInvalidTransition", err)
	}
	// 二次支付(换 payment_ref)= 矛盾,一次接续只承认一次支付。
	if _, err := ApplyCheckoutEvent(paid, AuthorityEventInput{Type: EventPaymentPaid, Payload: EventPayload{PaymentRef: "pay_2"}}); err != ErrEventMismatch {
		t.Fatalf("二次支付 = %v, want ErrEventMismatch", err)
	}
	// payment_paid 缺 payment_ref = 事实不相容。
	if _, err := ApplyCheckoutEvent(pending, AuthorityEventInput{Type: EventPaymentPaid}); err != ErrEventMismatch {
		t.Fatalf("空 payment_ref = %v, want ErrEventMismatch", err)
	}
	// 其它事件类型一律矛盾。
	if _, err := ApplyCheckoutEvent(pending, AuthorityEventInput{Type: EventOrderCancelled}); err != ErrEventMismatch {
		t.Fatalf("未知事件 = %v, want ErrEventMismatch", err)
	}
}
