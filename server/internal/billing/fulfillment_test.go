package billing

// fulfillment_test.go: 退款发票对账(F19)/硬件五状态(F14)/quote seam(4595)/
// 付款授权解析(4593 接线)测试。TDD 红先行。

import (
	"errors"
	"testing"
)

// ---- 退款发票对账(F19) ----

func refundInput(amount int64) RefundInput {
	return RefundInput{
		OrderID: "bo_1", RefundRef: "ref_1", AmountMinor: amount, Currency: "CNY",
		Reason: "七天无理由", InvoiceTitle: "派诺商业管理有限公司", InvoiceStatus: InvoicePending,
		Auditor: "auditor_wang", Source: "eco-billing", EventID: "re_1",
		OccurredAt: "2026-06-02T00:00:00Z",
	}
}

func TestValidateRefundRequiresFacts(t *testing.T) {
	if err := ValidateRefund(refundInput(5000)); err != nil {
		t.Fatalf("valid refund: %v", err)
	}
	// 零/负金额拒绝
	if err := ValidateRefund(refundInput(0)); !errors.Is(err, ErrRefundBadInput) {
		t.Fatalf("zero amount = %v", err)
	}
	// 缺原因拒绝
	in := refundInput(5000)
	in.Reason = ""
	if err := ValidateRefund(in); !errors.Is(err, ErrRefundBadInput) {
		t.Fatalf("missing reason = %v", err)
	}
	// 缺审核人拒绝
	in = refundInput(5000)
	in.Auditor = ""
	if err := ValidateRefund(in); !errors.Is(err, ErrRefundBadInput) {
		t.Fatalf("missing auditor = %v", err)
	}
	// 非法发票状态拒绝
	in = refundInput(5000)
	in.InvoiceStatus = "bogus"
	if err := ValidateRefund(in); !errors.Is(err, ErrRefundBadInput) {
		t.Fatalf("bad invoice status = %v", err)
	}
}

func TestRefundWithinRemainingBalance(t *testing.T) {
	// 订单 990000,已退 400000 → 未退余额 590000;再退 500000 OK;退 600000 超余额拒绝。
	if err := ValidateRefundAgainstOrder(refundInput(500000), 990000, 400000); err != nil {
		t.Fatalf("within remaining: %v", err)
	}
	if err := ValidateRefundAgainstOrder(refundInput(600000), 990000, 400000); !errors.Is(err, ErrRefundExceedsOrder) {
		t.Fatalf("exceeding = %v", err)
	}
	// 已退满后再退任何金额都拒绝。
	if err := ValidateRefundAgainstOrder(refundInput(1), 990000, 990000); !errors.Is(err, ErrRefundExceedsOrder) {
		t.Fatalf("fully refunded = %v", err)
	}
}

// ---- 硬件五状态(F14) ----

func TestHardwareStepOrderCannotSkip(t *testing.T) {
	f := HardwareFulfillment{}
	// 付款是首步:未付款直接出库/写卡都拒绝(不可跳跃)。
	if _, err := ApplyHardwareStep(f, HardwareStepInput{Step: StepShipment, Operator: "op1", OccurredAt: "2026-06-02T00:00:00Z"}); !errors.Is(err, ErrStepSkipped) {
		t.Fatalf("shipment before payment = %v", err)
	}
	if _, err := ApplyHardwareStep(f, HardwareStepInput{Step: StepWriteCard, Operator: "op1", OccurredAt: "2026-06-02T00:00:00Z"}); !errors.Is(err, ErrStepSkipped) {
		t.Fatalf("write before payment = %v", err)
	}
	inPay := HardwareStepInput{Step: StepPayment, Operator: "op1", OccurredAt: "2026-06-01T00:00:00Z"}
	f1, err := ApplyHardwareStep(f, inPay)
	if err != nil || f1.Payment.State != StepDone {
		t.Fatalf("payment step: %v %+v", err, f1)
	}
	// 写卡前必须已出库(裁定书/1663 语义)。
	in := HardwareStepInput{Step: StepWriteCard, Operator: "op1", OccurredAt: "2026-06-02T00:00:00Z"}
	if _, err := ApplyHardwareStep(f1, in); !errors.Is(err, ErrStepSkipped) {
		t.Fatalf("skip to write = %v", err)
	}
	in.Step = StepShipment
	f2, err := ApplyHardwareStep(f1, in)
	if err != nil || f2.Shipment.State != StepDone || f2.Shipment.Operator != "op1" {
		t.Fatalf("shipment: %v %+v", err, f2)
	}
	// 出库重做(不同事实)拒绝:事实不覆盖
	in2 := HardwareStepInput{Step: StepShipment, Operator: "op2", OccurredAt: "2026-06-03T00:00:00Z"}
	if _, err := ApplyHardwareStep(f2, in2); !errors.Is(err, ErrStepConflict) {
		t.Fatalf("overwrite shipment = %v", err)
	}
	// 同事实重放:幂等
	in3 := HardwareStepInput{Step: StepShipment, Operator: "op1", OccurredAt: "2026-06-02T00:00:00Z"}
	if f3, err := ApplyHardwareStep(f2, in3); err != nil || f3.Shipment.OccurredAt != "2026-06-02T00:00:00Z" {
		t.Fatalf("replay shipment: %v %+v", err, f3)
	}
	// 全链推进到验收
	in4 := HardwareStepInput{Step: StepWriteCard, Operator: "op1", OccurredAt: "2026-06-03T01:00:00Z", DeviceRef: "dev_1"}
	f4, err := ApplyHardwareStep(f2, in4)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	in5 := HardwareStepInput{Step: StepReadback, Operator: "op1", OccurredAt: "2026-06-03T02:00:00Z", DeviceRef: "dev_1"}
	f5, err := ApplyHardwareStep(f4, in5)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	// 未回读不能验收
	in6 := HardwareStepInput{Step: StepAcceptance, Operator: "op1", OccurredAt: "2026-06-03T03:00:00Z"}
	if _, err := ApplyHardwareStep(f4, in6); !errors.Is(err, ErrStepSkipped) {
		t.Fatalf("accept before readback = %v", err)
	}
	f6, err := ApplyHardwareStep(f5, in6)
	if err != nil || f6.Acceptance.State != StepDone {
		t.Fatalf("acceptance: %v %+v", err, f6)
	}
	// 未知步骤拒绝
	if _, err := ApplyHardwareStep(f, HardwareStepInput{Step: "bogus"}); !errors.Is(err, ErrStepConflict) {
		t.Fatalf("unknown step = %v", err)
	}
}

// ---- quote seam(4595 消费的 exported 契约) ----

func quoteRow() QuoteProjection {
	return QuoteProjection{
		QuoteRef: "q_1", TenantID: "ten_a", Capability: "content_production",
		Quantity: 1, AmountMinor: 800, Currency: "CNY", PricingVersion: "pv1",
		State: QuoteStateConfirmed,
	}
}

func TestAdjudicateQuoteRefHappyPath(t *testing.T) {
	row := quoteRow()
	v := AdjudicateQuoteRef(&row, QuoteRefQuery{TenantID: "ten_a", QuoteRef: "q_1",
		Capability: "content_production", RequireUnconsumed: true})
	if !v.Allowed || v.Reason != "" {
		t.Fatalf("happy path: %+v", v)
	}
}

func TestAdjudicateQuoteRefRejections(t *testing.T) {
	// 未找到(nil 行,同形掩蔽)
	if v := AdjudicateQuoteRef(nil, QuoteRefQuery{TenantID: "ten_a", QuoteRef: "q_x"}); v.Allowed || v.Reason != QuoteReasonNotFound {
		t.Fatalf("not found: %+v", v)
	}
	// 跨租户(防御:行存在但租户不符)
	row := quoteRow()
	if v := AdjudicateQuoteRef(&row, QuoteRefQuery{TenantID: "ten_b", QuoteRef: "q_1"}); v.Allowed || v.Reason != QuoteReasonCrossTenant {
		t.Fatalf("cross tenant: %+v", v)
	}
	// 未确认态(refunded/failed)
	row = quoteRow()
	row.State = QuoteStateRefunded
	if v := AdjudicateQuoteRef(&row, QuoteRefQuery{TenantID: "ten_a", QuoteRef: "q_1"}); v.Allowed || v.Reason != QuoteReasonNotConfirmed {
		t.Fatalf("refunded: %+v", v)
	}
	// 已消费
	row = quoteRow()
	row.State = QuoteStateConsumed
	if v := AdjudicateQuoteRef(&row, QuoteRefQuery{TenantID: "ten_a", QuoteRef: "q_1", RequireUnconsumed: true}); v.Allowed || v.Reason != QuoteReasonAlreadyConsumed {
		t.Fatalf("consumed: %+v", v)
	}
	// 能力不匹配
	row = quoteRow()
	if v := AdjudicateQuoteRef(&row, QuoteRefQuery{TenantID: "ten_a", QuoteRef: "q_1", Capability: "ai_copy"}); v.Allowed || v.Reason != QuoteReasonCapabilityMismatch {
		t.Fatalf("capability: %+v", v)
	}
}

// ---- 付款授权解析(4593 payer_grant_ref 真值接线) ----

func TestDecidePayerForOwnerIsSelf(t *testing.T) {
	d := DecidePayerForOrder(RoleOrgOwner, "usr_owner", nil)
	if !d.Authorized || d.Reason != "" || d.PayerPrincipal != "usr_owner" {
		t.Fatalf("owner payer: %+v", d)
	}
}

func TestDecidePayerForAgentRequiresLiveRenewalGrantWithPayerRef(t *testing.T) {
	// 无关系:拒
	if d := DecidePayerForOrder(RoleAgent, "usr_ag", nil); d.Authorized || d.Reason != PayerReasonRequired {
		t.Fatalf("agent without grant: %+v", d)
	}
	// 关系不含 renewal:拒
	d := DecidePayerForOrder(RoleAgent, "usr_ag", &AgencyGrantView{RelationRef: "rel1", AllowedActions: []string{"storefront"}, PayerGrantRef: "pgr_9"})
	if d.Authorized || d.Reason != PayerReasonRequired {
		t.Fatalf("agent without renewal: %+v", d)
	}
	// renewal 但 payer_grant_ref 空:拒(4593 字段语义:空=不可代付)
	d = DecidePayerForOrder(RoleAgent, "usr_ag", &AgencyGrantView{RelationRef: "rel1", AllowedActions: []string{"renewal"}})
	if d.Authorized || d.Reason != PayerReasonRequired {
		t.Fatalf("renewal without payer ref: %+v", d)
	}
	// renewal + payer_grant_ref 非空:授权成立,快照带引用(真值回填)
	d = DecidePayerForOrder(RoleAgent, "usr_ag", &AgencyGrantView{RelationRef: "rel1",
		AllowedActions: []string{"renewal"}, PayerGrantRef: "pgr_9", GrantRevision: 3})
	if !d.Authorized || d.PayerGrantRef != "pgr_9" || d.RelationRef != "rel1" || d.GrantRevision != 3 || d.PayerPrincipal != "usr_ag" {
		t.Fatalf("agent with payer grant: %+v", d)
	}
	// 其他角色(staff/manager)一律拒
	if d := DecidePayerForOrder("staff", "usr_st", nil); d.Authorized {
		t.Fatalf("staff payer: %+v", d)
	}
}
