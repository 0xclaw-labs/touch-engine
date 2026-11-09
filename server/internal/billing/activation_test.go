package billing

// activation_test.go: 四态开通状态机测试(HUI-4591 F16,TDD 红先行)。
//
// 纪律(裁定书):处理中/开通中/已开通/失败四态可区分;真实服务端支付状态才
// 开通(本函数只应从受信权威缝触达——浏览器返回不当支付证明,那只是读面);
// 幂等开通(重复回调不重复收费或开通);开通必须先有支付事实。

import (
	"errors"
	"testing"
)

func baseOrder() OrderState {
	return OrderState{
		ID: "bo_1", TenantID: "ten_a", Kind: KindSubscription, Action: ActionRenew,
		PlanCode: PlanMonthly, PriceRef: "price_p1", AmountMinor: 990000, Currency: "CNY",
		Status: OrderProcessing,
	}
}

func ev(t, id, typ string) AuthorityEventInput {
	return AuthorityEventInput{
		Source: "eco-billing", EventID: id, Type: typ,
		OccurredAt: "2026-06-01T12:00:00Z",
		Payload:    EventPayload{OrderRef: "ord_ext_1", PaymentRef: "pay_1"},
	}
}

func TestPaymentPaidOpensOrder(t *testing.T) {
	st, res, err := ApplyOrderEvent(baseOrder(), ev("t", "e1", EventPaymentPaid))
	if err != nil {
		t.Fatalf("payment: %v", err)
	}
	if st.Status != OrderOpening || st.PaymentRef != "pay_1" || st.OrderRef != "ord_ext_1" {
		t.Fatalf("after payment: %+v", st)
	}
	if res.Subscription != nil {
		t.Fatal("payment alone must not activate")
	}
}

func TestActivationRequiresRealPaymentFact(t *testing.T) {
	// 开通事实在支付事实之前到达:拒绝(四态可区分,不跳步)。
	_, _, err := ApplyOrderEvent(baseOrder(), ev("t", "e2", EventActivationActive))
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("activate before payment = %v", err)
	}
	// 显式开通中信号也一样:无支付事实不开通中。
	if _, _, err := ApplyOrderEvent(baseOrder(), ev("t", "e3", EventActivationOpening)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("opening before payment = %v", err)
	}
}

func TestActivationActiveWritesSubscriptionProjection(t *testing.T) {
	st := baseOrder()
	st.Status = OrderOpening
	in := ev("t", "e4", EventActivationActive)
	in.Payload = EventPayload{
		SubscriptionRef: "sub_eco_9", PlanCode: PlanMonthly, PricingVersion: "v2026",
		PaidUntil: "2027-06-01T00:00:00Z", StoreLimit: 5, GraceDays: 30,
		EntitlementProjection: `{"plan":"monthly","stores":5}`,
	}
	st2, res, err := ApplyOrderEvent(st, in)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if st2.Status != OrderActive {
		t.Fatalf("status = %s", st2.Status)
	}
	if res.Subscription == nil || res.Subscription.SubscriptionRef != "sub_eco_9" ||
		res.Subscription.PaidUntil != "2027-06-01T00:00:00Z" || res.Subscription.StoreLimit != 5 {
		t.Fatalf("projection payload: %+v", res.Subscription)
	}
}

func TestActivationFailedRecordsReason(t *testing.T) {
	for _, from := range []string{OrderProcessing, OrderOpening} {
		st := baseOrder()
		st.Status = from
		in := ev("t", "e5", EventActivationFailed)
		in.Payload.FailReason = "gateway_refused"
		st2, _, err := ApplyOrderEvent(st, in)
		if err != nil || st2.Status != OrderFailed || st2.FailReason != "gateway_refused" {
			t.Fatalf("fail from %s: %v %+v", from, err, st2)
		}
	}
	// 已开通的订单不能翻成失败(已生效服务期不受迟到的失败事件追溯削减)。
	st := baseOrder()
	st.Status = OrderActive
	if _, _, err := ApplyOrderEvent(st, ev("t", "e6", EventActivationFailed)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("failed after active = %v", err)
	}
}

func TestOrderRegisteredLandsExternalRef(t *testing.T) {
	st, _, err := ApplyOrderEvent(baseOrder(), ev("t", "e7", EventOrderRegistered))
	if err != nil || st.Status != OrderProcessing || st.OrderRef != "ord_ext_1" {
		t.Fatalf("register: %v %+v", err, st)
	}
	// 同一外部单引用重放:幂等通过;不同引用:冲突(订单绑定不可漂移)。
	st2, _, err := ApplyOrderEvent(st, ev("t", "e8", EventOrderRegistered))
	if err != nil || st2.OrderRef != "ord_ext_1" {
		t.Fatalf("replay register: %v %+v", err, st2)
	}
	in := ev("t", "e9", EventOrderRegistered)
	in.Payload.OrderRef = "ord_ext_OTHER"
	if _, _, err := ApplyOrderEvent(st, in); !errors.Is(err, ErrEventMismatch) {
		t.Fatalf("mismatched order_ref = %v", err)
	}
}

func TestCancelOnlyBeforePayment(t *testing.T) {
	st, _, err := ApplyOrderEvent(baseOrder(), ev("t", "e10", EventOrderCancelled))
	if err != nil || st.Status != OrderCancelled {
		t.Fatalf("cancel processing: %v %+v", err, st)
	}
	st2, _, err := ApplyOrderEvent(st, ev("t", "e11", EventPaymentPaid))
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pay after cancel = %v %+v", err, st2)
	}
	st = baseOrder()
	st.Status = OrderOpening
	if _, _, err := ApplyOrderEvent(st, ev("t", "e12", EventOrderCancelled)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("cancel after payment must refuse (退款走 refund 通道)")
	}
}

func TestPaymentTwiceDifferentRefConflicts(t *testing.T) {
	st := baseOrder()
	st.Status = OrderOpening
	st.PaymentRef = "pay_1"
	in := ev("t", "e13", EventPaymentPaid)
	in.Payload.PaymentRef = "pay_OTHER"
	if _, _, err := ApplyOrderEvent(st, in); !errors.Is(err, ErrEventMismatch) {
		t.Fatalf("second payment ref = %v", err)
	}
	// 同引用重放:幂等
	in.Payload.PaymentRef = "pay_1"
	if st2, _, err := ApplyOrderEvent(st, in); err != nil || st2.Status != OrderOpening {
		t.Fatalf("same payment replay: %v %+v", err, st2)
	}
}

func TestUnknownEventTypeRefused(t *testing.T) {
	if _, _, err := ApplyOrderEvent(baseOrder(), ev("t", "e14", "bogus_type")); !errors.Is(err, ErrEventMismatch) {
		t.Fatalf("unknown type = %v", err)
	}
}
