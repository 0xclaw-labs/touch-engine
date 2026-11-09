// activation.go: 订单四态开通状态机(HUI-4591 F16,纯函数)。
//
// 纪律(裁定书):
//   - 四态可区分:processing(处理中,订单已建等待真实支付)→opening(开通中,
//     已收到真实服务端支付事实)→active(已开通,写订阅投影)|failed(失败,
//     带原因);cancelled = 支付前撤单。
//   - 本函数只应被受信权威缝调用(服务生产者 token):事件的到达本身就是
//     "真实服务端支付状态"的唯一通道;浏览器返回(收银台跳回)走读面回查,
//     结构上进不了本函数。
//   - 幂等开通:同键重放由 store 层去重;本层保证不跳步、已生效服务期不被
//     迟到事件追溯削减、订单外部引用不可漂移。

package billing

import "errors"

// Order kinds / actions.
const (
	KindSubscription = "subscription"
	KindHardware     = "hardware"

	ActionNew       = "new"
	ActionRenew     = "renew"
	ActionExpand    = "expand"
	ActionDowngrade = "downgrade"
	ActionPurchase  = "purchase"
)

// Order lifecycle states (F16 四态 + 撤单)。
const (
	OrderProcessing = "processing"
	OrderOpening    = "opening"
	OrderActive     = "active"
	OrderFailed     = "failed"
	OrderCancelled  = "cancelled"
)

// Authoritative order-event types (billing_order_events.event_type).
const (
	EventOrderRegistered   = "order_registered"
	EventPaymentPaid       = "payment_paid"
	EventActivationOpening = "activation_opening"
	EventActivationActive  = "activation_active"
	EventActivationFailed  = "activation_failed"
	EventOrderCancelled    = "order_cancelled"
)

var (
	// ErrInvalidTransition:状态机不允许的跃迁(缺支付事实/追溯翻盘/迟到事件)。
	ErrInvalidTransition = errors.New("billing: order_invalid_transition")
	// ErrEventMismatch:事件负载与订单既有事实矛盾(外部单引用漂移/二次支付)。
	ErrEventMismatch = errors.New("billing: order_event_mismatch")
)

// OrderState is the projection of one billing_orders row the machine judges.
type OrderState struct {
	ID            string
	TenantID      string
	Kind          string
	Action        string
	PlanCode      string
	PriceRef      string
	PriceSnapshot string
	AmountMinor   int64
	Currency      string
	Status        string
	OrderRef      string
	PaymentRef    string
	FailReason    string
}

// EventPayload is the typed body of one authoritative event.
type EventPayload struct {
	OrderRef              string `json:"order_ref"`
	PaymentRef            string `json:"payment_ref"`
	SubscriptionRef       string `json:"subscription_ref"`
	PlanCode              string `json:"plan_code"`
	PricingVersion        string `json:"pricing_version"`
	PaidUntil             string `json:"paid_until"`
	StoreLimit            int    `json:"store_limit"`
	GraceDays             int    `json:"grace_days"`
	EntitlementProjection string `json:"entitlement_projection"`
	FailReason            string `json:"fail_reason"`
}

// AuthorityEventInput is one trusted-seam event before dedup (store layer owns
// source+event_id dedup and payload-equality replay).
type AuthorityEventInput struct {
	Source     string
	EventID    string
	Type       string
	OccurredAt string
	Payload    EventPayload
}

// SubscriptionProjectionUpdate rides an activation_active event: the upsert
// payload for billing_subscriptions (rebuilt projection, never a balance).
type SubscriptionProjectionUpdate struct {
	SubscriptionRef       string
	PlanCode              string
	PricingVersion        string
	PaidUntil             string
	StoreLimit            int
	GraceDays             int
	EntitlementProjection string
}

// OrderEventResult is the machine's verdict: the next order state and, on
// activation, the subscription projection upsert.
type OrderEventResult struct {
	Subscription *SubscriptionProjectionUpdate
}

// ApplyOrderEvent judges one authoritative event against the current order
// state and returns the next state. Pure: no clock, no store, no HTTP.
func ApplyOrderEvent(st OrderState, in AuthorityEventInput) (OrderState, OrderEventResult, error) {
	next := st
	switch in.Type {
	case EventOrderRegistered:
		if st.Status != OrderProcessing {
			return st, OrderEventResult{}, ErrInvalidTransition
		}
		if st.OrderRef != "" && st.OrderRef != in.Payload.OrderRef {
			return st, OrderEventResult{}, ErrEventMismatch
		}
		next.OrderRef = in.Payload.OrderRef
		return next, OrderEventResult{}, nil

	case EventPaymentPaid:
		if st.Status == OrderOpening {
			// 重放或同单重复回调:引用一致 = 幂等通过,不一致 = 矛盾。
			if st.PaymentRef == in.Payload.PaymentRef {
				return st, OrderEventResult{}, nil
			}
			return st, OrderEventResult{}, ErrEventMismatch
		}
		if st.Status != OrderProcessing {
			// active/failed/cancelled 之后迟到或追溯的支付事实不收。
			return st, OrderEventResult{}, ErrInvalidTransition
		}
		if st.PaymentRef != "" && st.PaymentRef != in.Payload.PaymentRef {
			return st, OrderEventResult{}, ErrEventMismatch
		}
		next.Status = OrderOpening
		next.PaymentRef = in.Payload.PaymentRef
		if in.Payload.OrderRef != "" {
			if st.OrderRef != "" && st.OrderRef != in.Payload.OrderRef {
				return st, OrderEventResult{}, ErrEventMismatch
			}
			next.OrderRef = in.Payload.OrderRef
		}
		return next, OrderEventResult{}, nil

	case EventActivationOpening:
		// 开通中是支付已确认后的显式信号;没有支付事实绝不进入开通中。
		if st.Status != OrderOpening {
			return st, OrderEventResult{}, ErrInvalidTransition
		}
		return st, OrderEventResult{}, nil

	case EventActivationActive:
		if st.Status != OrderOpening {
			// 处理中直达已开通 = 跳过支付事实,拒绝(F16 四态纪律)。
			return st, OrderEventResult{}, ErrInvalidTransition
		}
		next.Status = OrderActive
		return next, OrderEventResult{Subscription: &SubscriptionProjectionUpdate{
			SubscriptionRef:       in.Payload.SubscriptionRef,
			PlanCode:              firstNonEmpty(in.Payload.PlanCode, st.PlanCode),
			PricingVersion:        in.Payload.PricingVersion,
			PaidUntil:             in.Payload.PaidUntil,
			StoreLimit:            in.Payload.StoreLimit,
			GraceDays:             in.Payload.GraceDays,
			EntitlementProjection: in.Payload.EntitlementProjection,
		}}, nil

	case EventActivationFailed:
		if st.Status != OrderProcessing && st.Status != OrderOpening {
			// 已开通服务期不被迟到的失败事件追溯削减。
			return st, OrderEventResult{}, ErrInvalidTransition
		}
		next.Status = OrderFailed
		next.FailReason = firstNonEmpty(in.Payload.FailReason, "activation_failed")
		return next, OrderEventResult{}, nil

	case EventOrderCancelled:
		// 支付后撤单不走这里(资金已动,退款走 refund 通道)。
		if st.Status != OrderProcessing {
			return st, OrderEventResult{}, ErrInvalidTransition
		}
		next.Status = OrderCancelled
		return next, OrderEventResult{}, nil

	default:
		return st, OrderEventResult{}, ErrEventMismatch
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
