// checkout.go: 原单 checkout 接续引用的状态机与支付事实裁决(HUI-4664,
// R3 §1.3/§2.2 的任务适配纯函数层)。
//
// 纪律(裁定书/实现方案):
//   - billing_checkout_refs 是工程投影,不是支付权威:状态只由受信权威缝
//     (billing_authority 服务身份)的事实事件驱动;浏览器返回(收银台跳回)
//     走读面回查,结构上进不了本文件。
//   - 四值状态:checkout_pending(等待支付)→paid(支付事实已回流,activation
//     就绪)→activated(服务生效确认);paid 而 activation 失败=failed(如实
//     留因,可重试激活)。已生效(activated)不被迟到的失败事件追溯削减。
//   - 幂等:同 payment_ref 重放 = 幂等通过零改写;同单换 payment_ref =
//     矛盾(一次接续只承认一次支付);引用不可漂移。
//   - 金额/币种/payment_ref 逐字节比对(AdjudicatePaymentFact):不匹配不
//     推进,宁可挂起不可错开;同形掩蔽不泄漏方向。
//   - 统一契约 v1.1(ADV-9 FIX,2026-11-16 裁定):事件路由键=checkout_ref,
//     分型 payment_paid(→paid)/activation_opening(FYI no-op ack,状态不变)/
//     activation_active(→activated)/order_registered(Touch 单不收到,带
//     checkout_ref 一律未知类型拒)。activation 语义:paid=付款完成可开工
//    (制作任务发起时已提交),activated=制作工程确认开动(本地事实,见
//     handlers_billing_checkout_callback.go 的自主激活+回发)。
//   - 纯函数:取行归 store 层;无时钟无 IO。

package billing

// Checkout-ref continuation states (billing_checkout_refs.state CHECK 同值域)。
const (
	CheckoutStatePending   = "checkout_pending"
	CheckoutStatePaid      = "paid"
	CheckoutStateActivated = "activated"
	CheckoutStateFailed    = "failed"
)

// PaymentVerdict reason codes (exported;按码分诊)。
const (
	PaymentReasonMismatch = "payment_mismatch"
)

// CheckoutRefState is the projection of one billing_checkout_refs row the
// machine judges (store layer owns dedup and row updates).
type CheckoutRefState struct {
	ID          string
	TenantID    string
	CheckoutRef string
	OrderRef    string
	State       string
	AmountMinor int64
	Currency    string
	PaymentRef  string
	FailReason  string
}

// PaymentFact is one payment_paid fact carried by a trusted-seam event
// (R3 §1.3 producer 合同:payment_ref 稳定非空;金额币种与原单快照比对)。
type PaymentFact struct {
	PaymentRef  string
	CheckoutRef string
	AmountMinor int64
	Currency    string
}

// PaymentVerdict is the seam's answer; Allowed=false carries a machine reason.
type PaymentVerdict struct {
	Allowed bool
	Reason  string
}

// AdjudicatePaymentFact 裁决一条 payment_paid 事实能否匹配本单接续引用。
// 纯函数:取行归 store 层。payment_ref 为空、金额或币种与原单快照不一致
// = 事实与订单不相容(同形掩蔽为 mismatch,不泄漏方向)。
func AdjudicatePaymentFact(ref CheckoutRefState, fact PaymentFact) PaymentVerdict {
	if fact.PaymentRef == "" || fact.CheckoutRef != ref.CheckoutRef ||
		fact.AmountMinor != ref.AmountMinor || fact.Currency != ref.Currency {
		return PaymentVerdict{Allowed: false, Reason: PaymentReasonMismatch}
	}
	return PaymentVerdict{Allowed: true}
}

// ApplyCheckoutEvent judges one authoritative fact event against the current
// checkout-ref projection and returns the next state. Pure: no clock, no
// store, no HTTP. 承认支付事实、开通事实与开通中 FYI 信号(状态不变);
// 其它事件类型一律矛盾(order_registered 对 Touch 单就是未知类型)。
func ApplyCheckoutEvent(st CheckoutRefState, in AuthorityEventInput) (CheckoutRefState, error) {
	next := st
	switch in.Type {
	case EventPaymentPaid:
		if st.State == CheckoutStatePaid || st.State == CheckoutStateActivated || st.State == CheckoutStateFailed {
			// 重放或同单重复回调:引用一致 = 幂等通过零改写;不一致 = 矛盾
			//(一次接续只承认一次支付;failed 已携带支付事实)。
			if st.PaymentRef == in.Payload.PaymentRef {
				return st, nil
			}
			return st, ErrEventMismatch
		}
		if st.State != CheckoutStatePending {
			return st, ErrInvalidTransition
		}
		if in.Payload.PaymentRef == "" {
			return st, ErrEventMismatch
		}
		next.State = CheckoutStatePaid
		next.PaymentRef = in.Payload.PaymentRef
		next.FailReason = ""
		return next, nil

	case EventActivationOpening:
		// 统一契约 v1.1:activation_opening 是 producer 在 payment_paid 之后
		// 的显式「开通中」FYI 信号——对本投影一律 no-op ack,状态不变(store
		// 层按零改写答 replayed)。真正推进 activated 的事实只有
		// activation_active(producer 权威)或本地制作工程确认(回发前落行)。
		return st, nil

	case EventActivationActive:
		switch st.State {
		case CheckoutStatePaid, CheckoutStateFailed:
			// paid → activated 是主路径;failed → activated 是补偿路径
			//(paid 而 activation 失败=可重试激活:重试成功如实生效)。
			next.State = CheckoutStateActivated
			next.FailReason = ""
			return next, nil
		case CheckoutStateActivated:
			return st, nil // 幂等重放
		default:
			// checkout_pending 直达 activated = 跳过支付事实,拒绝。
			return st, ErrInvalidTransition
		}

	case EventActivationFailed:
		switch st.State {
		case CheckoutStatePaid, CheckoutStateFailed:
			// 已生效(activated)不被迟到的失败事件追溯削减;failed 重申
			// 如实更新留因(投影跟随权威最新事实)。
			next.State = CheckoutStateFailed
			next.FailReason = firstNonEmpty(in.Payload.FailReason, "activation_failed")
			return next, nil
		default:
			return st, ErrInvalidTransition
		}

	default:
		return st, ErrEventMismatch
	}
}
