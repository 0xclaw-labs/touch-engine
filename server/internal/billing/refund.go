// refund.go: 退款发票对账判定(HUI-4591 F19,纯函数)。
//
// 纪律(裁定书):
//   - 原订单/退款金额/原因/开票主体/发票状态/审核人逐字段落库(缺一拒绝)。
//   - 重复回调不重复退款:同 source+event_id 由 store 层去重;本层守卫金额
//     不超原订单未退余额。
//   - 核销(活动账)与现金账分开:本包永不触碰 activity_benefit_facts,也
//     不产生任何余额变动。

package billing

import "errors"

// Invoice status values (权威事实投影,库层 CHECK 同值域)。
const (
	InvoiceNone    = "none"
	InvoicePending = "pending"
	InvoiceIssued  = "issued"
	InvoiceVoid    = "void"
)

var (
	// ErrRefundBadInput:退款事实缺关键字段或金额非法。
	ErrRefundBadInput = errors.New("billing: refund_bad_input")
	// ErrRefundExceedsOrder:退款金额超过原订单未退余额。
	ErrRefundExceedsOrder = errors.New("billing: refund_exceeds_order")
)

// RefundInput is one refund fact from the trusted authority seam.
type RefundInput struct {
	OrderID       string
	RefundRef     string
	AmountMinor   int64
	Currency      string
	Reason        string
	InvoiceTitle  string
	InvoiceStatus string
	InvoiceRef    string
	Auditor       string
	Source        string
	EventID       string
	OccurredAt    string
}

func validInvoiceStatus(v string) bool {
	switch v {
	case InvoiceNone, InvoicePending, InvoiceIssued, InvoiceVoid:
		return true
	}
	return false
}

// ValidateRefund checks one refund fact's required fields: positive amount,
// a reason, an auditor, and a known invoice status.
func ValidateRefund(in RefundInput) error {
	if in.AmountMinor <= 0 || in.Reason == "" || in.Auditor == "" || !validInvoiceStatus(in.InvoiceStatus) {
		return ErrRefundBadInput
	}
	return nil
}

// ValidateRefundAgainstOrder guards the amount against the order's remaining
// un-refunded balance (order amount minus already-refunded total).
func ValidateRefundAgainstOrder(in RefundInput, orderAmountMinor, refundedSoFarMinor int64) error {
	if err := ValidateRefund(in); err != nil {
		return err
	}
	remaining := orderAmountMinor - refundedSoFarMinor
	if remaining < 0 || in.AmountMinor > remaining {
		return ErrRefundExceedsOrder
	}
	return nil
}
