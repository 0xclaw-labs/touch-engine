package billing

import (
	"context"
	"errors"
	"time"
)

// HUI-4664 原单收银接续的公共收银台 producer 合同需求面。
//
// 纪律来源：docs/acceptance/touch-resume-20261115/checkout-implementation-feasibility.md
// 第三/四节。本文件只钉 Touch 侧对公共权威的**合同需求**（接口、输入装配
// 来源、结果分型语义），不定义也不猜测公共侧的端点、字段名或签发流程——
// 那些必须由公共权威侧的 producer 合同（第四节 1-5 项）确认后，才能写出
// 真实的 HTTP 客户端。在合同落地并注入实现之前：
//   - Server 侧 CheckoutProducer 为 nil，收银保持 checkout_unwired
//     （handlers_billing.go 的 cashierCheckout 原样，fail-closed）；
//   - 任何实现都不得把钱包充值、GoBoost 商品购买或应用登记成功当作
//     Touch 收银已接通。
//
// 合同需求（对应可行性文档第四节，逐项钉入下方字段注释）：
//  1. 权威入口必须消费/创建与 Touch 原单稳定绑定的公共订单（tenant+原单
//     幂等身份），付款组织与有效 payer 由权威侧判定——服务凭据与任意
//     account_id 不得代替。
//  2. 原商品/原价格由已批准目录或有效报价确认；原单价格快照冻结，动态
//     重新报价不满足 Touch 原价要求。
//  3. checkout 读写合同：可恢复支付引用、查单、到期、关闭、已付款未开通、
//     并发重试语义；绑定真实渠道预下单与 verified payment fact。
//  4. 结果映射：公共 order/payment/refund 稳定身份 → Touch 的
//     order_ref/payment_ref/source/event_id 投影；重复投递稳定去重。
//  5. 原任务受控返回：返回上下文签发/保存/消费责任明确；返回只触发权威
//     回查，不因 success query 推进 paid/active。

// ErrCheckoutProducerUnavailable 是 producer 未配置或不可达时的统一错误。
// handler 层将其折叠为 checkout_unwired 诚实回执，绝不降级为本地推算。
var ErrCheckoutProducerUnavailable = errors.New("billing: checkout producer unavailable")

// OriginalOrderCheckoutRequest 由服务端从 Touch 原单装配——不从浏览器信任
// 金额、tenant、payer 或任意 URL（可行性文档三节 adapter 行为红线）。
type OriginalOrderCheckoutRequest struct {
	// Touch 原单身份（幂等键：公共侧据此保证同原单只生成一个支付意图，
	// 响应丢失后同 key 查回同一意图——合同需求 3）。
	TenantID       string
	OrderID        string
	IdempotencyKey string
	// 原单冻结快照（合同需求 2）：公共侧必须认可原商品/原价格，不得动态
	// 重新报价后让 Touch 原单按新价支付。
	PlanCode      string
	PriceRef      string
	PriceSnapshot string // 原单冻结 JSON，原样透传
	AmountMinor   int64
	Currency      string
	// 原付款主体与授权快照（合同需求 1）：公共侧据此判定付款组织与有效
	// payer；实时授权复核在 Touch 侧 originalOrderCheckout 已先行完成，
	// 权威侧仍须独立判定，不信任 Touch 转发的判定结论。
	PayerPrincipal    string
	PayerGrantRef     string
	AgencyRelationRef string
	// 受控返回上下文请求（合同需求 5）：绑定原 tenant/order 的允许目标由
	// 权威侧签发；Touch 侧保存于 billing_orders.return_context。
	RequestedBy string
	Now         time.Time
}

// OriginalOrderCheckoutResult 是公共收银台的一次确定性结果。ResultKind
// 五态互斥；Unknown/响应丢失绝不推进 Touch 订单状态（kept read-only）。
type OriginalOrderCheckoutResult struct {
	// created = 新签发支付引用；found = 同原单同 key 查回既有引用（响应
	// 丢失恢复，不二次购买）；paid_already = 原单已付款（Touch 读权威回执
	// 投影，不在本调用内推进状态）；expired/rejected = 引用失效或权威拒绝。
	ResultKind string // created | found | paid_already | expired | rejected
	// CheckoutRef 是可恢复支付引用（写入 billing_orders.checkout_ref；
	// 合同需求 3）。Unknown/失败态必须为空。
	CheckoutRef string
	// 权威侧声明的引用有效期（合同需求 3 的到期语义）；零值=合同未声明。
	ExpiresAt time.Time
	// 受控返回上下文（合同需求 5）：权威签发的原任务返回绑定；Touch 原样
	// 存 return_context 列，受控读面消费。
	ReturnContext string
	// 权威侧拒绝/失效的稳定原因码（rejected/expired 态必填；透传给确认面，
	// 不在 Touch 侧二次解读）。
	ReasonCode string
}

// CheckoutProducer 是公共收银台原单接续的出站合同面。nil 实现 = 未接线。
// 实现必须：服务配置注入（不可由请求参数构造）；幂等（同请求重复调用产
// 出同一支付意图）；对 Unknown/网络失败返回 error（调用方保持原单只读），
// 绝不返回臆造结果。
type CheckoutProducer interface {
	Ready() bool
	CreateOrResumeCheckout(ctx context.Context, in OriginalOrderCheckoutRequest) (OriginalOrderCheckoutResult, error)
}
