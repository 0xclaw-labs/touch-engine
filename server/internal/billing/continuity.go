// continuity.go: HUI-4664 开通到交付可接续的纯函数投影。
//
// 纪律:
//   - 清晰分离六类业务阶段:pending_payment / payment_paid / service_activating /
//     service_active / hardware_pending / refund_in_progress (+ failed / cancelled)。
//   - paid 绝不等于 service_active,更绝不等于实体 NFC 卡已交付(hardware_delivered)。
//   - 已付款但开通同步中(service_activating / payment_paid)只提供原单回查,
//     forbid_repeat_purchase 恒为 true,绝不提示重复购买或二次付款。
//   - 硬件进度真实消费 HUI-1663(绑定/写卡/回读/现场验收/补换/binding_stale)与
//     HUI-4591 五步履约事实,不编造已完成步骤。
//   - 配置代理无独立 payer 权限时,收费下一步明确归属 merchant(交回商家负责人),
//     非收费获准事项不受影响。
package billing

import "fmt"

// Authoritative lifecycle stages surfaced to M12 / A1.
const (
	StagePendingPayment    = "pending_payment"
	StagePaymentPaid       = "payment_paid"
	StageServiceActivating = "service_activating"
	StageServiceActive     = "service_active"
	StageHardwarePending   = "hardware_pending"
	StageRefundInProgress  = "refund_in_progress"
	StageFailed            = "failed"
	StageCancelled         = "cancelled"
)

// Milestone status values.
const (
	MilestoneDone       = "done"
	MilestoneInProgress = "in_progress"
	MilestonePending    = "pending"
	MilestoneFailed     = "failed"
	MilestoneStale      = "stale"
)

// Exception / next-step ownership domains.
const (
	DomainPayment          = "payment"
	DomainActivation       = "activation"
	DomainHardwareDelivery = "hardware_delivery"
	DomainRefund           = "refund"
	DomainAuthorization    = "authorization"
	DomainCatalog          = "catalog"
	DomainCheckoutChannel  = "checkout_channel"
)

// OrderMilestone is one distinct lifecycle step fact on an order.
type OrderMilestone struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	OccurredAt  string `json:"occurred_at,omitempty"`
	Operator    string `json:"operator,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// ContinuityNextStep is the single controlled next step for the current actor.
type ContinuityNextStep struct {
	Code                 string `json:"code"`
	Domain               string `json:"domain"`
	Summary              string `json:"summary"`
	ActionLabel          string `json:"action_label"`
	TargetPath           string `json:"target_path"`
	Owner                string `json:"owner"` // merchant | agent | platform
	AllowRetryPayment    bool   `json:"allow_retry_payment"`
	ForbidRepeatPurchase bool   `json:"forbid_repeat_purchase"`
}

// HardwareDeliveryFact mirrors the HUI-1663 physical NFC delivery truth for one card.
type HardwareDeliveryFact struct {
	ID                  string
	TouchpointID        string
	StoreID             string
	Status              string // awaiting_dispatch | dispatched | bound | written | readback | accepted | delivered | superseded
	BindingStale        bool
	LastReadbackMatches bool
	LastReadbackReason  string
	ReplacementOf       string
	DispatchedAt        string
	BoundAt             string
	WrittenAt           string
	ReadbackAt          string
	AcceptedAt          string
	DeliveredAt         string
}

// OrderRefundFact mirrors one HUI-4591 refund/invoice reconciliation row for the same order.
type OrderRefundFact struct {
	RefundRef     string
	AmountMinor   int64
	Currency      string
	Reason        string
	InvoiceStatus string
	Auditor       string
	OccurredAt    string
}

// OrderContinuityInput is the complete server-assembled fact bundle for one billing_orders row.
type OrderContinuityInput struct {
	Order              OrderState
	CreatedAt          string
	UpdatedAt          string
	CheckoutState      string // "", checkout_pending, paid, activated, failed
	RemoteStatus       string // "", pending, paid, mismatch, failed, cancelled
	CheckoutAvailable  bool
	CheckoutReason     string
	ActorCanPay        bool
	ActorIsAgent       bool
	Fulfillment        *HardwareFulfillment
	PhysicalDeliveries []HardwareDeliveryFact
	Refunds            []OrderRefundFact
}

// OrderContinuityProjection is the honest lifecycle, milestone, and next-step projection.
type OrderContinuityProjection struct {
	OrderRef           string             `json:"order_ref"`
	OrderKind          string             `json:"order_kind"`
	LifecycleStage     string             `json:"lifecycle_stage"`
	Paid               bool               `json:"paid"`
	ServiceActive      bool               `json:"service_active"`
	HardwareDelivered  bool               `json:"hardware_delivered"`
	HardwarePending    bool               `json:"hardware_pending"`
	RefundInProgress   bool               `json:"refund_in_progress"`
	RefundedMinor      int64              `json:"refunded_minor"`
	RefundCount        int                `json:"refund_count"`
	BindingStaleCount  int                `json:"binding_stale_count"`
	Milestones         []OrderMilestone   `json:"milestones"`
	NextStep           ContinuityNextStep `json:"next_step"`
}

// BuildOrderContinuity derives the honest lifecycle stage, milestones, and
// controlled next step for a subscription or hardware billing_orders row.
func BuildOrderContinuity(in OrderContinuityInput) OrderContinuityProjection {
	out := OrderContinuityProjection{
		OrderRef:  in.Order.ID,
		OrderKind: in.Order.Kind,
	}

	// Payment truth: only server-recorded payment_ref, opening/active status,
	// checkout_ref paid/activated, or hardware payment step done counts as paid.
	paid := in.Order.PaymentRef != "" ||
		in.Order.Status == OrderOpening ||
		in.Order.Status == OrderActive ||
		in.CheckoutState == CheckoutStatePaid ||
		in.CheckoutState == CheckoutStateActivated ||
		(in.Fulfillment != nil && in.Fulfillment.Payment.State == StepDone)
	out.Paid = paid

	// Refund facts on the same original order.
	var refundedMinor int64
	for _, rf := range in.Refunds {
		refundedMinor += rf.AmountMinor
	}
	out.RefundedMinor = refundedMinor
	out.RefundCount = len(in.Refunds)
	hasRefund := len(in.Refunds) > 0
	out.RefundInProgress = hasRefund

	// Active service truth (subscription orders only).
	if in.Order.Kind == KindSubscription {
		out.ServiceActive = in.Order.Status == OrderActive && !hasRefund
	}

	// Hardware physical delivery truth (HUI-1663 + HUI-4591).
	// paid NEVER implies hardware_delivered.
	hwSummary := evaluateHardwareFacts(in.Fulfillment, in.PhysicalDeliveries)
	out.BindingStaleCount = hwSummary.staleCount
	if in.Order.Kind == KindHardware {
		out.HardwareDelivered = paid && hwSummary.allDelivered && !hasRefund
		out.HardwarePending = paid && !out.HardwareDelivered && !hasRefund && in.Order.Status != OrderFailed && in.Order.Status != OrderCancelled
	}

	// Build milestones chronologically.
	out.Milestones = buildOrderMilestones(in, paid, hwSummary)

	// Determine authoritative lifecycle_stage & single next_step.
	switch {
	case hasRefund:
		out.LifecycleStage = StageRefundInProgress
		out.NextStep = buildRefundNextStep(in, refundedMinor)

	case in.Order.Status == OrderCancelled:
		out.LifecycleStage = StageCancelled
		out.NextStep = ContinuityNextStep{
			Code:                 "order_cancelled",
			Domain:               DomainPayment,
			Summary:              "该原单在支付前已撤单，如需开通或采购可重新查看生效目录后再下单。",
			ActionLabel:          "查看生效目录",
			TargetPath:           "/work/billing",
			Owner:                ownerForActor(in.ActorIsAgent, in.ActorCanPay),
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: false,
		}

	case in.Order.Status == OrderFailed:
		out.LifecycleStage = StageFailed
		if paid {
			out.NextStep = ContinuityNextStep{
				Code:                 "paid_activation_failed_escalate",
				Domain:               DomainActivation,
				Summary:              fmt.Sprintf("已收到支付事实但服务开通失败（%s），请保留原订单并联系平台核对补开，切勿重复付款。", firstNonEmpty(in.Order.FailReason, "原因待平台核查")),
				ActionLabel:          "回查原订单并联系平台",
				TargetPath:           "/work/billing",
				Owner:                "platform",
				AllowRetryPayment:    false,
				ForbidRepeatPurchase: true,
			}
		} else {
			out.NextStep = ContinuityNextStep{
				Code:                 "unpaid_order_failed",
				Domain:               DomainPayment,
				Summary:              fmt.Sprintf("订单办理失败（%s），尚未确认付款，请先回查原订单核对原因后再决定下一步。", firstNonEmpty(in.Order.FailReason, "未完成")),
				ActionLabel:          "回查原订单",
				TargetPath:           "/work/billing",
				Owner:                ownerForActor(in.ActorIsAgent, in.ActorCanPay),
				AllowRetryPayment:    false,
				ForbidRepeatPurchase: false,
			}
		}

	case in.Order.Kind == KindHardware && paid && !out.HardwareDelivered:
		out.LifecycleStage = StageHardwarePending
		out.NextStep = buildHardwarePendingNextStep(hwSummary)

	case in.Order.Kind == KindHardware && out.HardwareDelivered:
		out.LifecycleStage = StageServiceActive
		out.NextStep = ContinuityNextStep{
			Code:                 "hardware_delivered",
			Domain:               DomainHardwareDelivery,
			Summary:              "硬件订单已付款且实体 NFC 卡已完成写卡、回读与现场验收交付。",
			ActionLabel:          "查看门店投放入口",
			TargetPath:           "/work/touchpoints",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}

	case in.Order.Status == OrderOpening || (in.Order.Kind == KindSubscription && paid && in.Order.Status != OrderActive):
		out.LifecycleStage = StageServiceActivating
		out.NextStep = ContinuityNextStep{
			Code:                 "query_original_order_activating",
			Domain:               DomainActivation,
			Summary:              "已收到服务端支付事实，服务正在同步开通中；请回查原订单确认生效状态，切勿重复购买或再次付款。",
			ActionLabel:          "回查原订单",
			TargetPath:           "/work/billing",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}

	case in.Order.Status == OrderActive:
		out.LifecycleStage = StageServiceActive
		out.NextStep = ContinuityNextStep{
			Code:                 "service_active",
			Domain:               DomainActivation,
			Summary:              "服务已生效，付款主体、价格版本与服务期限已落档；如涉及实体卡，请以实卡验收记录为准。",
			ActionLabel:          "查看当前服务",
			TargetPath:           "/work/billing",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}

	case !paid && in.RemoteStatus == "paid":
		// Remote inspect reports paid, waiting for authoritative payment_paid webhook sync.
		out.LifecycleStage = StageServiceActivating
		out.NextStep = ContinuityNextStep{
			Code:                 "payment_sync_pending",
			Domain:               DomainPayment,
			Summary:              "公共收银侧已记录支付结果，正在等待服务端权威同步至本地原订单；请回查原订单，切勿重复付款。",
			ActionLabel:          "回查原订单",
			TargetPath:           "/work/billing",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}

	default:
		out.LifecycleStage = StagePendingPayment
		out.NextStep = buildPendingPaymentNextStep(in)
	}

	return out
}

type hardwareEval struct {
	liveCount        int
	deliveredCount   int
	staleCount       int
	readbackMismatch bool
	allDelivered     bool
	nextBlocker      string
	shipmentDone     bool
	bindDone         bool
	writeDone        bool
	readbackDone     bool
	acceptanceDone   bool
	deliveredDone    bool
}

func evaluateHardwareFacts(f *HardwareFulfillment, deliveries []HardwareDeliveryFact) hardwareEval {
	ev := hardwareEval{}
	if f != nil {
		ev.shipmentDone = f.Shipment.State == StepDone
		ev.writeDone = f.WriteCard.State == StepDone
		ev.readbackDone = f.Readback.State == StepDone
		ev.acceptanceDone = f.Acceptance.State == StepDone
	}
	var live []HardwareDeliveryFact
	for _, d := range deliveries {
		if d.Status == "superseded" {
			continue
		}
		live = append(live, d)
		if d.BindingStale {
			ev.staleCount++
		}
		if d.LastReadbackReason != "" && !d.LastReadbackMatches {
			ev.readbackMismatch = true
		}
		switch d.Status {
		case "dispatched":
			ev.shipmentDone = true
		case "bound":
			ev.shipmentDone, ev.bindDone = true, true
		case "written":
			ev.shipmentDone, ev.bindDone, ev.writeDone = true, true, true
		case "readback":
			ev.shipmentDone, ev.bindDone, ev.writeDone, ev.readbackDone = true, true, true, true
		case "accepted":
			ev.shipmentDone, ev.bindDone, ev.writeDone, ev.readbackDone, ev.acceptanceDone = true, true, true, true, true
		case "delivered":
			ev.shipmentDone, ev.bindDone, ev.writeDone, ev.readbackDone, ev.acceptanceDone, ev.deliveredDone = true, true, true, true, true, true
			if !d.BindingStale && (d.LastReadbackReason == "" || d.LastReadbackMatches) {
				ev.deliveredCount++
			}
		}
	}
	ev.liveCount = len(live)

	switch {
	case ev.staleCount > 0:
		ev.allDelivered = false
		ev.nextBlocker = "binding_stale"
	case ev.readbackMismatch:
		ev.allDelivered = false
		ev.nextBlocker = "readback_mismatch"
	case ev.liveCount > 0:
		ev.allDelivered = ev.deliveredCount == ev.liveCount
		if !ev.allDelivered {
			ev.nextBlocker = firstIncompleteDeliveryStage(live)
		}
	default:
		// 无 HUI-1663 实卡交付行时,绝不能仅凭付款认定已交付;仅当 4591 五步验收已完成才算验收完成。
		ev.allDelivered = ev.acceptanceDone
		if !ev.allDelivered {
			switch {
			case !ev.shipmentDone:
				ev.nextBlocker = "awaiting_dispatch"
			case !ev.writeDone:
				ev.nextBlocker = "awaiting_write_card"
			case !ev.readbackDone:
				ev.nextBlocker = "awaiting_readback"
			default:
				ev.nextBlocker = "awaiting_site_acceptance"
			}
		}
	}
	return ev
}

func firstIncompleteDeliveryStage(live []HardwareDeliveryFact) string {
	for _, d := range live {
		if d.Status != "delivered" {
			return d.Status
		}
	}
	return "awaiting_site_acceptance"
}

func buildOrderMilestones(in OrderContinuityInput, paid bool, hw hardwareEval) []OrderMilestone {
	ms := []OrderMilestone{
		{
			Key:         "order_created",
			Label:       "原单已登记",
			Status:      MilestoneDone,
			OccurredAt:  in.CreatedAt,
			EvidenceRef: in.Order.ID,
		},
	}

	payStatus := MilestonePending
	payDetail := "等待公共收银侧支付结果"
	if paid {
		payStatus = MilestoneDone
		payDetail = "已收到服务端可信支付事实"
	} else if in.RemoteStatus == "paid" {
		payStatus = MilestoneInProgress
		payDetail = "收银侧已记录付款，等待权威事件同步"
	} else if in.Order.Status == OrderCancelled {
		payStatus = MilestoneFailed
		payDetail = "支付前已撤单"
	}
	payAt := ""
	payOp := ""
	if in.Fulfillment != nil && in.Fulfillment.Payment.State == StepDone {
		payAt = in.Fulfillment.Payment.OccurredAt
		payOp = in.Fulfillment.Payment.Operator
	}
	ms = append(ms, OrderMilestone{
		Key:         "payment",
		Label:       "支付结果",
		Status:      payStatus,
		OccurredAt:  payAt,
		Operator:    payOp,
		EvidenceRef: in.Order.PaymentRef,
		Detail:      payDetail,
	})

	if in.Order.Kind == KindSubscription {
		actStatus := MilestonePending
		actDetail := "等待付款后开通服务"
		switch {
		case in.Order.Status == OrderActive:
			actStatus = MilestoneDone
			actDetail = "服务已生效"
		case in.Order.Status == OrderOpening || (paid && in.Order.Status != OrderFailed):
			actStatus = MilestoneInProgress
			actDetail = "付款成功，服务开通同步中"
		case in.Order.Status == OrderFailed:
			actStatus = MilestoneFailed
			actDetail = firstNonEmpty(in.Order.FailReason, "服务开通失败")
		}
		ms = append(ms, OrderMilestone{
			Key:         "service_activation",
			Label:       "服务生效",
			Status:      actStatus,
			OccurredAt:  in.UpdatedAt,
			EvidenceRef: in.Order.OrderRef,
			Detail:      actDetail,
		})
	} else if in.Order.Kind == KindHardware {
		stepStatus := func(done bool, prevDone bool) string {
			if done {
				return MilestoneDone
			}
			if prevDone {
				return MilestoneInProgress
			}
			return MilestonePending
		}
		var shipAt, shipOp, writeAt, writeOp, rbAt, rbOp, accAt, accOp string
		if in.Fulfillment != nil {
			shipAt, shipOp = in.Fulfillment.Shipment.OccurredAt, in.Fulfillment.Shipment.Operator
			writeAt, writeOp = in.Fulfillment.WriteCard.OccurredAt, in.Fulfillment.WriteCard.Operator
			rbAt, rbOp = in.Fulfillment.Readback.OccurredAt, in.Fulfillment.Readback.Operator
			accAt, accOp = in.Fulfillment.Acceptance.OccurredAt, in.Fulfillment.Acceptance.Operator
		}
		ms = append(ms,
			OrderMilestone{Key: "shipment", Label: "硬件出库", Status: stepStatus(hw.shipmentDone, paid), OccurredAt: shipAt, Operator: shipOp},
			OrderMilestone{Key: "bind", Label: "触点绑定", Status: stepStatus(hw.bindDone, hw.shipmentDone)},
			OrderMilestone{Key: "write_card", Label: "写卡", Status: stepStatus(hw.writeDone, hw.shipmentDone), OccurredAt: writeAt, Operator: writeOp},
		)
		rbStatus := stepStatus(hw.readbackDone, hw.writeDone)
		rbDetail := ""
		if hw.readbackMismatch {
			rbStatus = MilestoneFailed
			rbDetail = "回读不一致，尚未通过验证"
		}
		ms = append(ms, OrderMilestone{Key: "readback", Label: "写卡回读", Status: rbStatus, OccurredAt: rbAt, Operator: rbOp, Detail: rbDetail})
		accStatus := stepStatus(hw.acceptanceDone, hw.readbackDone && !hw.readbackMismatch)
		accDetail := ""
		if hw.staleCount > 0 {
			accStatus = MilestoneStale
			accDetail = "门店触点绑定已变更，旧实测已失效，需重新核对"
		}
		ms = append(ms, OrderMilestone{Key: "site_acceptance", Label: "现场验收", Status: accStatus, OccurredAt: accAt, Operator: accOp, Detail: accDetail})
		delivStatus := MilestonePending
		if hw.allDelivered {
			delivStatus = MilestoneDone
		} else if hw.staleCount > 0 {
			delivStatus = MilestoneStale
		} else if hw.acceptanceDone {
			delivStatus = MilestoneInProgress
		}
		ms = append(ms, OrderMilestone{Key: "delivered", Label: "实卡交付完成", Status: delivStatus})
	}

	if len(in.Refunds) > 0 {
		last := in.Refunds[len(in.Refunds)-1]
		ms = append(ms, OrderMilestone{
			Key:         "refund",
			Label:       "同单退款对账",
			Status:      MilestoneInProgress,
			OccurredAt:  last.OccurredAt,
			Operator:    last.Auditor,
			EvidenceRef: last.RefundRef,
			Detail:      fmt.Sprintf("已登记 %d 笔退款，发票状态：%s", len(in.Refunds), firstNonEmpty(last.InvoiceStatus, "none")),
		})
	}
	return ms
}

func buildRefundNextStep(in OrderContinuityInput, refundedMinor int64) ContinuityNextStep {
	last := in.Refunds[len(in.Refunds)-1]
	return ContinuityNextStep{
		Code:                 "refund_reconciliation_in_progress",
		Domain:               DomainRefund,
		Summary:              fmt.Sprintf("同单已登记退款（引用 %s，累计 %.2f %s，原因：%s）；原订单禁止再次付款，请到「发票与退款」核对对账进度。", last.RefundRef, float64(refundedMinor)/100, firstNonEmpty(last.Currency, in.Order.Currency, "CNY"), last.Reason),
		ActionLabel:          "查看退款与发票对账",
		TargetPath:           "/work/billing",
		Owner:                "merchant",
		AllowRetryPayment:    false,
		ForbidRepeatPurchase: true,
	}
}

func buildHardwarePendingNextStep(hw hardwareEval) ContinuityNextStep {
	switch hw.nextBlocker {
	case "binding_stale":
		return ContinuityNextStep{
			Code:                 "hardware_reverify_after_rebind",
			Domain:               DomainHardwareDelivery,
			Summary:              "硬件已付款，但门店触点绑定已变更导致旧验收失效；请前往「门店投放入口」重新写卡回读与现场验收，不可将旧验收视为已交付。",
			ActionLabel:          "前往门店投放入口重新验收",
			TargetPath:           "/work/touchpoints",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	case "readback_mismatch":
		return ContinuityNextStep{
			Code:                 "hardware_readback_mismatch",
			Domain:               DomainHardwareDelivery,
			Summary:              "硬件已付款，但实卡回读内容与门店触点不一致；请前往「门店投放入口」重做写卡与回读核对，切勿重复购买。",
			ActionLabel:          "前往核对写卡与回读",
			TargetPath:           "/work/touchpoints",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	case "awaiting_dispatch", "dispatched", "bound", "written", "readback", "accepted":
		return ContinuityNextStep{
			Code:                 "hardware_delivery_in_progress",
			Domain:               DomainHardwareDelivery,
			Summary:              "硬件订单已付款，但实体 NFC 卡尚未完成出库/绑定/写卡/回读/现场验收全流程（已付款不等于实体卡已交付）；请前往「门店投放入口」跟进实卡交付，无需重复下单。",
			ActionLabel:          "查看实卡交付进度",
			TargetPath:           "/work/touchpoints",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	default:
		return ContinuityNextStep{
			Code:                 "hardware_acceptance_pending",
			Domain:               DomainHardwareDelivery,
			Summary:              "硬件订单已付款，尚未完成实体 NFC 卡现场验收与交付；请前往「门店投放入口」登记实卡交付与验收，无需重复付款。",
			ActionLabel:          "查看实卡交付进度",
			TargetPath:           "/work/touchpoints",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	}
}

func buildPendingPaymentNextStep(in OrderContinuityInput) ContinuityNextStep {
	if in.CheckoutReason == "original_payer_required" || !in.ActorCanPay {
		return ContinuityNextStep{
			Code:                 "delegate_to_merchant_payer",
			Domain:               DomainAuthorization,
			Summary:              "原单等待支付，当前账号没有独立付款授权或原付款授权已变化；请交由商家负责人或原付款人完成支付，绝不误扣个人或未授权钱包。",
			ActionLabel:          "交由商家负责人付款",
			TargetPath:           "/work/billing",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	}
	if !in.CheckoutAvailable {
		return ContinuityNextStep{
			Code:                 "checkout_channel_unavailable",
			Domain:               DomainCheckoutChannel,
			Summary:              "原单已登记并保留，公共收银通道尚未就绪；请保留原订单号并联系平台恢复收银接入，无需重复下单。",
			ActionLabel:          "回查原订单",
			TargetPath:           "/work/billing",
			Owner:                "platform",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	}
	return ContinuityNextStep{
		Code:                 "continue_original_checkout",
		Domain:               DomainPayment,
		Summary:              "原单等待支付，请直接继续支付原订单（复用同一原单与价格快照，无需重新下单）。",
		ActionLabel:          "继续支付原订单",
		TargetPath:           "/work/billing",
		Owner:                ownerForActor(in.ActorIsAgent, in.ActorCanPay),
		AllowRetryPayment:    true,
		ForbidRepeatPurchase: true,
	}
}

func ownerForActor(isAgent, canPay bool) string {
	if isAgent && !canPay {
		return "merchant"
	}
	if isAgent {
		return "agent"
	}
	return "merchant"
}

// ProductionContinuityInput builds the honest lifecycle projection for one
// content-production continuation checkout row (billing_checkout_refs with order_kind=content_production).
type ProductionContinuityInput struct {
	CheckoutRef       string
	OrderRef          string
	HandoffStatus     string
	State             string // checkout_pending | paid | activated | failed
	RemoteStatus      string
	PaymentRef        string
	FailReason        string
	CheckoutAvailable bool
	ActorCanPay       bool
	ActorIsAgent      bool
	CreatedAt         string
	UpdatedAt         string
}

// BuildProductionContinuity projects a content-production checkout onto the same
// honest lifecycle vocabulary (pending_payment / payment_paid / service_activating / service_active / failed).
func BuildProductionContinuity(in ProductionContinuityInput) OrderContinuityProjection {
	paid := in.State == CheckoutStatePaid || in.State == CheckoutStateActivated || in.PaymentRef != ""
	out := OrderContinuityProjection{
		OrderRef:      in.OrderRef,
		OrderKind:     "content_production",
		Paid:          paid,
		ServiceActive: in.State == CheckoutStateActivated,
	}

	payStatus := MilestonePending
	if paid {
		payStatus = MilestoneDone
	} else if in.RemoteStatus == "paid" {
		payStatus = MilestoneInProgress
	}
	actStatus := MilestonePending
	switch in.State {
	case CheckoutStateActivated:
		actStatus = MilestoneDone
	case CheckoutStatePaid:
		actStatus = MilestoneInProgress
	case CheckoutStateFailed:
		actStatus = MilestoneFailed
	}
	out.Milestones = []OrderMilestone{
		{Key: "order_created", Label: "制作原单已成立", Status: MilestoneDone, OccurredAt: in.CreatedAt, EvidenceRef: in.OrderRef},
		{Key: "payment", Label: "支付结果", Status: payStatus, EvidenceRef: in.PaymentRef},
		{Key: "production_activation", Label: "制作开通确认", Status: actStatus, OccurredAt: in.UpdatedAt, Detail: in.FailReason},
	}

	switch {
	case in.State == CheckoutStateActivated:
		out.LifecycleStage = StageServiceActive
		out.NextStep = ContinuityNextStep{
			Code:                 "production_activated",
			Domain:               DomainActivation,
			Summary:              "制作原单已付款并确认开动，请在内容制作记录中查看产出并预览采用。",
			ActionLabel:          "查看制作进度",
			TargetPath:           "/work/materials",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	case in.State == CheckoutStatePaid:
		// 已收到支付事实，等待制作任务提交或工程开动确认。
		if in.HandoffStatus == "submitted" || in.HandoffStatus == "produced" {
			out.LifecycleStage = StageServiceActivating
		} else {
			out.LifecycleStage = StagePaymentPaid
		}
		out.NextStep = ContinuityNextStep{
			Code:                 "query_original_production_order",
			Domain:               DomainActivation,
			Summary:              "已收到服务端支付事实，正在等待制作开通同步；请回查原单状态，切勿重复付款或重新报价。",
			ActionLabel:          "回查制作原单",
			TargetPath:           "/work/billing",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	case in.State == CheckoutStateFailed:
		out.LifecycleStage = StageFailed
		out.NextStep = ContinuityNextStep{
			Code:                 "production_activation_failed",
			Domain:               DomainActivation,
			Summary:              fmt.Sprintf("制作原单已付款但开通失败（%s）；请保留原单并联系平台重试激活，切勿重复付款。", firstNonEmpty(in.FailReason, "activation_failed")),
			ActionLabel:          "回查原单并联系平台",
			TargetPath:           "/work/billing",
			Owner:                "platform",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	case !paid && in.RemoteStatus == "paid":
		out.LifecycleStage = StageServiceActivating
		out.NextStep = ContinuityNextStep{
			Code:                 "payment_sync_pending",
			Domain:               DomainPayment,
			Summary:              "公共收银侧已记录支付结果，正在同步至制作原单；请回查原单，切勿重复付款。",
			ActionLabel:          "回查制作原单",
			TargetPath:           "/work/billing",
			Owner:                "merchant",
			AllowRetryPayment:    false,
			ForbidRepeatPurchase: true,
		}
	default:
		out.LifecycleStage = StagePendingPayment
		if !in.ActorCanPay {
			out.NextStep = ContinuityNextStep{
				Code:                 "delegate_to_merchant_payer",
				Domain:               DomainAuthorization,
				Summary:              "制作原单等待支付，当前账号没有独立付款授权；请交由商家负责人确认付款。",
				ActionLabel:          "交由商家负责人付款",
				TargetPath:           "/work/billing",
				Owner:                "merchant",
				AllowRetryPayment:    false,
				ForbidRepeatPurchase: true,
			}
		} else if !in.CheckoutAvailable {
			out.NextStep = ContinuityNextStep{
				Code:                 "checkout_channel_unavailable",
				Domain:               DomainCheckoutChannel,
				Summary:              "制作原单与报价已保留，公共收银通道尚未就绪；请保留原单并联系平台恢复接入，无需重新报价。",
				ActionLabel:          "回查制作原单",
				TargetPath:           "/work/billing",
				Owner:                "platform",
				AllowRetryPayment:    false,
				ForbidRepeatPurchase: true,
			}
		} else {
			out.NextStep = ContinuityNextStep{
				Code:                 "continue_original_checkout",
				Domain:               DomainPayment,
				Summary:              "制作原单等待支付，请继续支付原单（复用同一引用与报价，无需重复购买）。",
				ActionLabel:          "去续付原单",
				TargetPath:           "/work/billing",
				Owner:                ownerForActor(in.ActorIsAgent, in.ActorCanPay),
				AllowRetryPayment:    true,
				ForbidRepeatPurchase: true,
			}
		}
	}
	return out
}
