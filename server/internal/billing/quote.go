// quote.go: AI 用量报价引用校验缝(HUI-4591 F13 投影 × HUI-4595 消费契约)。
//
// 契约(4595 裁定书:「quote 校验函数接口对齐其 brief 契约」;本包 exported,
// 4595 的 content-production-handoffs 在发起制作前调用):
//   - 输入:已确认 quote_ref(+调用方租户,来自服务端行,永不信客户端声明)。
//   - 拒绝码五值:quote_not_found / quote_cross_tenant / quote_not_confirmed /
//     quote_already_consumed / quote_capability_mismatch。
//   - AI 用量按实际合约用量扣费、重试幂等(UNIQUE(tenant,quote_ref))、
//     浏览/复制不计费(不计费动作在调用侧约束,结构上进不了投影表)。

package billing

// Quote states (库层 billing_ai_usage.state CHECK 同值域)。
const (
	QuoteStateConfirmed = "confirmed"
	QuoteStateConsumed  = "consumed"
	QuoteStateRefunded  = "refunded"
	QuoteStateFailed    = "failed"
)

// Quote-ref verdict reasons (exported;4595 按码分诊)。
const (
	QuoteReasonNotFound           = "quote_not_found"
	QuoteReasonCrossTenant        = "quote_cross_tenant"
	QuoteReasonNotConfirmed       = "quote_not_confirmed"
	QuoteReasonAlreadyConsumed    = "quote_already_consumed"
	QuoteReasonCapabilityMismatch = "quote_capability_mismatch"
)

// QuoteProjection mirrors one billing_ai_usage row (rebuilt projection from
// the 4595 quote/charge references; never a balance).
type QuoteProjection struct {
	QuoteRef       string
	TenantID       string
	Capability     string
	Quantity       int64
	AmountMinor    int64
	Currency       string
	PricingVersion string
	State          string
	ConsumedByRef  string
}

// QuoteRefQuery is the caller's claim about the quote it wants to use.
type QuoteRefQuery struct {
	TenantID   string // 服务端解析的租户(调用侧从会话/行取,绝不信请求体)
	QuoteRef   string
	Capability string // '' = 不校验能力
	// RequireUnconsumed:发起制作(handoff)必须 true;只读查询可 false。
	RequireUnconsumed bool
}

// QuoteRefVerdict is the seam's answer; Allowed=false carries a machine reason.
type QuoteRefVerdict struct {
	Allowed bool
	Reason  string
}

// AdjudicateQuoteRef judges one fetched projection row (nil = 未找到,同形掩蔽
// 不泄漏存在性)。纯函数:取行归调用侧的 store 层。
func AdjudicateQuoteRef(row *QuoteProjection, q QuoteRefQuery) QuoteRefVerdict {
	if row == nil || row.QuoteRef == "" {
		return QuoteRefVerdict{Reason: QuoteReasonNotFound}
	}
	if row.TenantID != q.TenantID {
		return QuoteRefVerdict{Reason: QuoteReasonCrossTenant}
	}
	if row.State != QuoteStateConfirmed {
		if row.State == QuoteStateConsumed {
			return QuoteRefVerdict{Reason: QuoteReasonAlreadyConsumed}
		}
		// refunded / failed:不可用,且不是"已消费"。
		return QuoteRefVerdict{Reason: QuoteReasonNotConfirmed}
	}
	if q.RequireUnconsumed && row.ConsumedByRef != "" {
		return QuoteRefVerdict{Reason: QuoteReasonAlreadyConsumed}
	}
	if q.Capability != "" && row.Capability != q.Capability {
		return QuoteRefVerdict{Reason: QuoteReasonCapabilityMismatch}
	}
	return QuoteRefVerdict{Allowed: true}
}
