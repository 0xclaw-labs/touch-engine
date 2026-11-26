package billing

import "testing"

func TestBuildOrderContinuityDistinctStagesAndNextSteps(t *testing.T) {
	// 1. 待支付 + 配置代理无独立 payer 权限 → 交回商家负责人,禁止自己扣款。
	agentNoPayer := BuildOrderContinuity(OrderContinuityInput{
		Order:             OrderState{ID: "bo_1", Kind: KindSubscription, Status: OrderProcessing, AmountMinor: 9900, Currency: "CNY"},
		CheckoutAvailable: false,
		CheckoutReason:    "original_payer_required",
		ActorCanPay:       false,
		ActorIsAgent:      true,
		CreatedAt:         "2026-11-17T01:00:00Z",
	})
	if agentNoPayer.LifecycleStage != StagePendingPayment || agentNoPayer.Paid {
		t.Fatalf("unexpected stage: %+v", agentNoPayer)
	}
	if agentNoPayer.NextStep.Owner != "merchant" || agentNoPayer.NextStep.AllowRetryPayment || agentNoPayer.NextStep.Code != "delegate_to_merchant_payer" {
		t.Fatalf("agent without payer must route to merchant: %+v", agentNoPayer.NextStep)
	}

	// 2. 已付款未开通(opening) → service_activating,提供原单回查,严禁重复购买。
	activating := BuildOrderContinuity(OrderContinuityInput{
		Order:       OrderState{ID: "bo_2", Kind: KindSubscription, Status: OrderOpening, PaymentRef: "pay_2", AmountMinor: 9900, Currency: "CNY"},
		ActorCanPay: true,
		CreatedAt:   "2026-11-17T01:00:00Z",
		UpdatedAt:   "2026-11-17T01:02:00Z",
	})
	if activating.LifecycleStage != StageServiceActivating || !activating.Paid || activating.ServiceActive {
		t.Fatalf("expected service_activating: %+v", activating)
	}
	if activating.NextStep.AllowRetryPayment || !activating.NextStep.ForbidRepeatPurchase || activating.NextStep.Code != "query_original_order_activating" {
		t.Fatalf("activating order must forbid repeat purchase: %+v", activating.NextStep)
	}

	// 3. 硬件已付款但实卡未验收(HUI-1663 bound/written/readback 未 delivered) → hardware_pending, paid != delivered。
	hwPending := BuildOrderContinuity(OrderContinuityInput{
		Order: OrderState{ID: "bo_hw_1", Kind: KindHardware, Status: OrderOpening, PaymentRef: "pay_hw_1", AmountMinor: 29900, Currency: "CNY"},
		Fulfillment: &HardwareFulfillment{
			Payment:  HardwareStep{State: StepDone, Operator: "authority:pai-billing", OccurredAt: "2026-11-17T01:05:00Z"},
			Shipment: HardwareStep{State: StepDone, Operator: "ops", OccurredAt: "2026-11-17T02:00:00Z"},
		},
		PhysicalDeliveries: []HardwareDeliveryFact{
			{ID: "dlv_1", Status: "written", BindingStale: false},
		},
	})
	if hwPending.LifecycleStage != StageHardwarePending || !hwPending.Paid || hwPending.HardwareDelivered || !hwPending.HardwarePending {
		t.Fatalf("paid hardware without acceptance must be hardware_pending: %+v", hwPending)
	}
	if hwPending.NextStep.Code != "hardware_delivery_in_progress" || hwPending.NextStep.AllowRetryPayment || !hwPending.NextStep.ForbidRepeatPurchase {
		t.Fatalf("hardware pending next step wrong: %+v", hwPending.NextStep)
	}

	// 4. 硬件实卡曾经 delivered 但门店换绑后 binding_stale=true → 必须回到 hardware_pending,绝不冒充已交付!
	hwStale := BuildOrderContinuity(OrderContinuityInput{
		Order: OrderState{ID: "bo_hw_2", Kind: KindHardware, Status: OrderActive, PaymentRef: "pay_hw_2", AmountMinor: 29900, Currency: "CNY"},
		PhysicalDeliveries: []HardwareDeliveryFact{
			{ID: "dlv_2", Status: "delivered", BindingStale: true},
		},
	})
	if hwStale.LifecycleStage != StageHardwarePending || hwStale.HardwareDelivered || hwStale.BindingStaleCount != 1 {
		t.Fatalf("stale binding must invalidate hardware_delivered: %+v", hwStale)
	}
	if hwStale.NextStep.Code != "hardware_reverify_after_rebind" {
		t.Fatalf("stale next step wrong: %+v", hwStale.NextStep)
	}

	// 5. 同单退款处理中 → refund_in_progress, 与已付款未开通/卡未验收明显不同。
	refunding := BuildOrderContinuity(OrderContinuityInput{
		Order: OrderState{ID: "bo_3", Kind: KindSubscription, Status: OrderActive, PaymentRef: "pay_3", AmountMinor: 9900, Currency: "CNY"},
		Refunds: []OrderRefundFact{
			{RefundRef: "ref_3", AmountMinor: 9900, Currency: "CNY", Reason: "商家申请全额退", InvoiceStatus: "pending", Auditor: "ops_1", OccurredAt: "2026-11-17T05:00:00Z"},
		},
	})
	if refunding.LifecycleStage != StageRefundInProgress || !refunding.RefundInProgress || refunding.RefundedMinor != 9900 {
		t.Fatalf("expected refund_in_progress: %+v", refunding)
	}
	if refunding.NextStep.Code != "refund_reconciliation_in_progress" || refunding.NextStep.AllowRetryPayment || !refunding.NextStep.ForbidRepeatPurchase {
		t.Fatalf("refund next step wrong: %+v", refunding.NextStep)
	}

	// 确认三种核心场景的 stage 和 next_step.code 两两不同。
	if activating.LifecycleStage == hwPending.LifecycleStage ||
		hwPending.LifecycleStage == refunding.LifecycleStage ||
		activating.LifecycleStage == refunding.LifecycleStage {
		t.Fatalf("stages must be distinct: activating=%s hw=%s refund=%s",
			activating.LifecycleStage, hwPending.LifecycleStage, refunding.LifecycleStage)
	}
	if activating.NextStep.Code == hwPending.NextStep.Code ||
		hwPending.NextStep.Code == refunding.NextStep.Code ||
		activating.NextStep.Code == refunding.NextStep.Code {
		t.Fatalf("next step codes must be distinct: %s / %s / %s",
			activating.NextStep.Code, hwPending.NextStep.Code, refunding.NextStep.Code)
	}
}

func TestBuildProductionContinuitySeparatesPaidAndActivated(t *testing.T) {
	paidOnly := BuildProductionContinuity(ProductionContinuityInput{
		CheckoutRef:   "ck_1",
		OrderRef:      "cph_1",
		HandoffStatus: "recorded",
		State:         CheckoutStatePaid,
		PaymentRef:    "pay_1",
		ActorCanPay:   true,
	})
	if paidOnly.LifecycleStage != StagePaymentPaid || !paidOnly.Paid || paidOnly.ServiceActive {
		t.Fatalf("expected payment_paid: %+v", paidOnly)
	}
	if paidOnly.NextStep.AllowRetryPayment || !paidOnly.NextStep.ForbidRepeatPurchase {
		t.Fatalf("paid production must forbid repeat purchase: %+v", paidOnly.NextStep)
	}

	activated := BuildProductionContinuity(ProductionContinuityInput{
		CheckoutRef:   "ck_1",
		OrderRef:      "cph_1",
		HandoffStatus: "submitted",
		State:         CheckoutStateActivated,
		PaymentRef:    "pay_1",
	})
	if activated.LifecycleStage != StageServiceActive || !activated.ServiceActive {
		t.Fatalf("expected service_active: %+v", activated)
	}
}
