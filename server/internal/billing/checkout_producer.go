package billing

import (
	"context"
	"errors"
	"time"
)

// HUI-4664 原单收银接续的公共收银台 producer 客户端合同（v1.1 对齐）。
//
// 权威合同：public-ai docs/contracts/touch-order-checkout/v1/contract.md
//（ADV-9 统一契约裁定版）。与 ae1bd0c 首版骨架的关键差异（v1.1 §0 统一
// 契约）：**checkout_ref 主路由键 = Touch 本地铸的续付引用 ck_***——铸造
// 职责在 Touch（handler 层），producer 的 tco_* 只是映射存储的远端值。
// 双向事件（payment_paid / activation_active 回发）一律以 ck_* 路由，
// source_order_ref 为 Touch 本地原单映射回声。
//
// 在 producer 合同端点（platform-billing /internal/v1/billing/touch-orders/*）
// 可达且 BILLING_APP_TOKENS 登记 touch-engine 族之前，本客户端 Ready=false，
// 收银保持 checkout_unwired（fail-closed）。

var (
	// ErrCheckoutProducerUnavailable 是 producer 不可达/未装配的统一错误。
	// handler 层折叠为 checkout_unwired 诚实回执，绝不降级本地推算。
	ErrCheckoutProducerUnavailable = errors.New("billing: checkout producer unavailable")
	// ErrCheckoutProducerRejected 是 producer 明确拒绝（价格未认可/付款人
	// 未过/payer 不存在等 4xx 类）——透传 reason 给确认面。
	ErrCheckoutProducerRejected = errors.New("billing: checkout producer rejected")
)

// MintLine 是原单商品行（价格冻结承认的输入，逐字段与公共侧已发布价格行
// 比对——producer 永不重新计价，合同 §1 受理校验链 5）。
type MintLine struct {
	Kind               string `json:"kind"`
	PlanCode           string `json:"plan_code"`
	PriceRef           string `json:"price_ref"`
	PricingVersion     string `json:"pricing_version"`
	AmountMinor        int64  `json:"amount_minor"`
	Currency           string `json:"currency"`
	ServiceDays        int    `json:"service_days"`
	IncludedStoreCount int    `json:"included_store_count"`
	AdditionalStores   int    `json:"additional_stores"`
}

// MintPayer 是付款主体快照（producer 只存不裁：授权裁定权在 Touch 的
// originalOrderCheckout 实时复核链；producer 裁存在性）。
type MintPayer struct {
	PrincipalRef  string `json:"principal_ref"`
	GrantRef      string `json:"grant_ref"`
	GrantRevision int64  `json:"grant_revision"`
}

// MintRequest 是 mint 端点的请求形状（DisallowUnknownFields——字段集合
// 逐字对齐合同 §1 表，多字段会被 producer 400 拒绝）。
type MintRequest struct {
	SourceApp      string    `json:"source_app"`       // 恒 "touch-engine"
	SourceOrderRef string    `json:"source_order_ref"` // Touch 本地 ck_*（主路由键）
	IntentKey      string    `json:"intent_key"`       // 原单幂等键快照（跨侧镜像）
	TenantRef      string    `json:"tenant_ref"`       // tnt_* verbatim
	Payer          MintPayer `json:"payer"`
	Line           MintLine  `json:"line"`
	Provider       string    `json:"provider"`      // wechat | alipay
	ReturnTarget   string    `json:"return_target"` // 冻结值 ti-touch-engine-billing
}

// MintResult 是 mint/查回的响应投影（producer wire 超集字段按需消费；
// status: pending|paid|mismatch|failed|cancelled）。
type MintResult struct {
	CheckoutRef     string `json:"checkout_ref"` // tco_*（远端值，映射存储）
	OutTradeNo      string `json:"out_trade_no"`
	SourceApp       string `json:"source_app"`
	SourceOrderRef  string `json:"source_order_ref"` // ck_* 回声
	Status          string `json:"status"`
	CodeURL         string `json:"code_url,omitempty"`
	Replayed        bool   `json:"replayed"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	ReturnTarget    string `json:"return_target,omitempty"`
	ReturnURL       string `json:"return_url,omitempty"`
	PriceDigest     string `json:"price_digest,omitempty"`
	ActivatedAtUnix int64  `json:"activated_at_unix,omitempty"`
}

// CheckoutProducer 是公共收银台的出站合同面（HTTP 客户端实现于
// touch_checkout_client.go）。nil/Ready=false = 未接线，收银保持
// checkout_unwired（fail-closed）。
type CheckoutProducer interface {
	Ready() bool
	// Mint 对应 POST /touch-orders/checkout：同键幂等（replayed=true 表示
	// 响应丢失后查回同一支付意图，绝不二次调渠道）。
	Mint(ctx context.Context, in MintRequest) (MintResult, error)
	// Inspect 对应 GET 查单（只读回查；未知引用折 ErrCheckoutProducerRejected）。
	Inspect(ctx context.Context, ckRef string) (MintResult, error)
	// NotifyActivated 对应 POST activation-active 回发（§2b）：非阻塞语义，
	// 失败返回 error 由调用方记日志——绝不因此失败已落地的结算或履约。
	NotifyActivated(ctx context.Context, ckRef string, occurredAt time.Time, detail map[string]string) error
}
