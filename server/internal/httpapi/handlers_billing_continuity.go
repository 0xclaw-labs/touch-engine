// handlers_billing_continuity.go: HUI-4664 M12 账单与交付连续性权威投影读面。
//
// 纪律:
//   - 全真实数据投影,零示例数据、零伪造价格;
//   - 优先展示当前服务、期限、门店额度与待处理原单(含里程碑与单一下一步);
//   - 缺生效目录、缺独立付款授权或收费通道未接通时,逐项解释具体缺项与受控去向;
//   - 硬件交付事实真实消费 HUI-1663 (touchpoint_deliveries) 与 HUI-4591
//     (billing_hardware_fulfillment),绝不把 paid 等同于实卡已交付。
package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func deliveryViewsToFacts(views []store.TouchpointDeliveryView) []billing.HardwareDeliveryFact {
	facts := make([]billing.HardwareDeliveryFact, 0, len(views))
	for _, dv := range views {
		f := billing.HardwareDeliveryFact{
			ID:                  dv.ID,
			TouchpointID:        dv.TouchpointID,
			StoreID:             dv.StoreID,
			Status:              dv.Status,
			BindingStale:        dv.BindingStale,
			LastReadbackMatches: dv.LastReadbackMatches,
			LastReadbackReason:  dv.LastReadbackReason,
			ReplacementOf:       dv.ReplacementOf,
		}
		if st, ok := dv.Steps["dispatched"]; ok {
			f.DispatchedAt = st.OccurredAt
		}
		if st, ok := dv.Steps["bound"]; ok {
			f.BoundAt = st.OccurredAt
		}
		if st, ok := dv.Steps["written"]; ok {
			f.WrittenAt = st.OccurredAt
		}
		if st, ok := dv.Steps["readback"]; ok {
			f.ReadbackAt = st.OccurredAt
		}
		if st, ok := dv.Steps["accepted"]; ok {
			f.AcceptedAt = st.OccurredAt
		}
		if st, ok := dv.Steps["delivered"]; ok {
			f.DeliveredAt = st.OccurredAt
		}
		facts = append(facts, f)
	}
	return facts
}

func refundRowsToFacts(rows []store.BillingRefundRow) []billing.OrderRefundFact {
	facts := make([]billing.OrderRefundFact, 0, len(rows))
	for _, rf := range rows {
		facts = append(facts, billing.OrderRefundFact{
			RefundRef:     rf.RefundRef,
			AmountMinor:   rf.AmountMinor,
			Currency:      rf.Currency,
			Reason:        rf.Reason,
			InvoiceStatus: rf.InvoiceStatus,
			Auditor:       rf.Auditor,
			OccurredAt:    rf.OccurredAt,
		})
	}
	return facts
}

func (s *Server) buildOrderContinuityForCaller(
	c *caller,
	order store.BillingOrderRow,
	checkout map[string]any,
	fulfillment *billing.HardwareFulfillment,
	deliveries []store.TouchpointDeliveryView,
	refunds []store.BillingRefundRow,
) billing.OrderContinuityProjection {
	checkoutState, _ := checkout["state"].(string)
	remoteStatus, _ := checkout["remote_status"].(string)
	checkoutAvailable, _ := checkout["available"].(bool)
	checkoutReason, _ := checkout["reason"].(string)
	if !checkoutAvailable && s.billingCheckoutSeamReady() && checkoutReason == "checkout_unwired" {
		// 尚未 Mint 但收银通道就绪、且调用方通过了原付款人校验 → 可发起原单 Mint。
		if mintReady, _ := checkout["mint_supported"].(bool); mintReady {
			checkoutAvailable = true
		}
	}
	isAgent := c != nil && c.Member != nil && c.Member.Role == string(authz.RoleAgent)
	payer := s.payerDecisionFor(c)
	actorCanPay := s.callerHasPayerGrant(c) && payer.Authorized &&
		payer.PayerPrincipal == order.PayerPrincipal &&
		payer.PayerGrantRef == order.PayerGrantRef &&
		payer.RelationRef == order.AgencyRelationRef

	return billing.BuildOrderContinuity(billing.OrderContinuityInput{
		Order: billing.OrderState{
			ID:            order.ID,
			TenantID:      order.TenantID,
			Kind:          order.Kind,
			Action:        order.Action,
			PlanCode:      order.PlanCode,
			PriceRef:      order.PriceRef,
			PriceSnapshot: order.PriceSnapshot,
			AmountMinor:   order.AmountMinor,
			Currency:      order.Currency,
			Status:        order.Status,
			OrderRef:      order.OrderRef,
			PaymentRef:    order.PaymentRef,
			FailReason:    order.FailReason,
		},
		CreatedAt:          order.CreatedAt,
		UpdatedAt:          order.UpdatedAt,
		CheckoutState:      checkoutState,
		RemoteStatus:       remoteStatus,
		CheckoutAvailable:  checkoutAvailable,
		CheckoutReason:     checkoutReason,
		ActorCanPay:        actorCanPay,
		ActorIsAgent:       isAgent,
		Fulfillment:        fulfillment,
		PhysicalDeliveries: deliveryViewsToFacts(deliveries),
		Refunds:            refundRowsToFacts(refunds),
	})
}

type billingContinuityException struct {
	Code        string `json:"code"`
	Domain      string `json:"domain"`
	ObjectRef   string `json:"object_ref,omitempty"`
	Summary     string `json:"summary"`
	ActionLabel string `json:"action_label"`
	TargetPath  string `json:"target_path"`
	Owner       string `json:"owner"`
}

// handleBillingContinuity answers GET /api/v1/billing/continuity (M12 连续性总投影)。
func (s *Server) handleBillingContinuity(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	tenantID := c.Member.TenantID
	subView, err := s.subscriptionView(tenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "subscription lookup failed")
		return
	}

	// 1. 生效目录就绪态(绝不编造虚构价格)。
	entries, catErr := s.St.ListBillingCatalog(tenantID)
	effectivePlans := []string{}
	if catErr == nil {
		nowStr := nowRFC3339()
		for _, plan := range []string{billing.PlanMonthly, billing.PlanYearly} {
			if _, rerr := billing.ResolveCatalog(entries, tenantID, plan, nowStr); rerr == nil {
				effectivePlans = append(effectivePlans, plan)
			}
		}
	}
	catalogReady := catErr == nil && len(effectivePlans) > 0
	catalogReadiness := map[string]any{
		"ready":           catalogReady,
		"available_plans": effectivePlans,
	}
	var exceptions []billingContinuityException
	if !catalogReady {
		msg := "当前暂无生效价格目录，暂不展示可购买价格；请联系平台运营发布生效价格版本后刷新。已有原订单仍可正常回查。"
		if catErr != nil {
			msg = "价格目录暂时读取失败，请刷新重试；若持续不可用请联系平台运营核查。"
		}
		catalogReadiness["missing_reason"] = "catalog_unavailable"
		catalogReadiness["next_step"] = map[string]any{
			"summary":      msg,
			"action_label": "联系平台运营发布生效目录",
			"target_path":  "/work/billing",
			"owner":        "platform",
		}
		exceptions = append(exceptions, billingContinuityException{
			Code:        "catalog_unavailable",
			Domain:      billing.DomainCatalog,
			Summary:     msg,
			ActionLabel: "联系平台运营发布生效目录",
			TargetPath:  "/work/billing",
			Owner:       "platform",
		})
	}

	// 2. 付款主体与独立代付授权裁决(配置代理无独立 payer 权时明确交回商家负责人)。
	isAgent := c.Member.Role == string(authz.RoleAgent)
	payer := s.payerDecisionFor(c)
	canPay := s.callerHasPayerGrant(c) && payer.Authorized
	payerAuthority := map[string]any{
		"can_pay":         canPay,
		"actor_role":      c.Member.Role,
		"payer_principal": payer.PayerPrincipal,
		"payer_grant_ref": payer.PayerGrantRef,
		"relation_ref":    payer.RelationRef,
		"grant_revision":  payer.GrantRevision,
	}
	if !canPay {
		reason := billing.PayerReasonRequired
		summary := "当前账号没有独立付款授权（配置或只读权限不包含付款权）；非收费获准事项仍可正常办理，收费动作请交由商家负责人完成，绝不误扣个人或未授权商家账户。"
		payerAuthority["missing_reason"] = reason
		payerAuthority["next_step"] = map[string]any{
			"summary":      summary,
			"action_label": "交由商家负责人付款或补充独立代付授权",
			"target_path":  "/work/settings",
			"owner":        "merchant",
		}
		exceptions = append(exceptions, billingContinuityException{
			Code:        reason,
			Domain:      billing.DomainAuthorization,
			Summary:     summary,
			ActionLabel: "交由商家负责人办理",
			TargetPath:  "/work/settings",
			Owner:       "merchant",
		})
	}

	// 3. 公共收银通道就绪态。
	channelReady := s.billingCheckoutSeamReady()
	checkoutChannel := map[string]any{
		"ready": channelReady,
	}
	if !channelReady {
		summary := "公共结算侧收银通道尚未接通（不会提供虚构支付提交入口）；已有报价与原订单均完整保留，可继续回查，请联系平台完成接入。"
		checkoutChannel["reason"] = "checkout_unwired"
		checkoutChannel["next_step"] = map[string]any{
			"summary":      summary,
			"action_label": "回查既有原订单",
			"target_path":  "/work/billing",
			"owner":        "platform",
		}
		exceptions = append(exceptions, billingContinuityException{
			Code:        "checkout_unwired",
			Domain:      billing.DomainCheckoutChannel,
			Summary:     summary,
			ActionLabel: "联系平台接入收银通道",
			TargetPath:  "/work/billing",
			Owner:       "platform",
		})
	}

	// 4. 订单、同单退款与 HUI-1663 实卡交付聚合投影。
	orders, err := s.St.ListBillingOrders(tenantID, "")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "order list failed")
		return
	}
	allRefunds, err := s.St.ListBillingRefunds(tenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "refund list failed")
		return
	}
	refundsByOrder := map[string][]store.BillingRefundRow{}
	var totalRefundedMinor int64
	for _, rf := range allRefunds {
		refundsByOrder[rf.OrderID] = append(refundsByOrder[rf.OrderID], rf)
		totalRefundedMinor += rf.AmountMinor
	}
	allDeliveries, delErr := s.St.ListTouchpointDeliveryViews(tenantID, store.TouchpointDeliveryFilter{})
	deliveriesByOrder := map[string][]store.TouchpointDeliveryView{}
	deliveredCards, pendingCards, staleCards := 0, 0, 0
	if delErr == nil {
		for _, dv := range allDeliveries {
			deliveriesByOrder[dv.HardwareOrderID] = append(deliveriesByOrder[dv.HardwareOrderID], dv)
			if dv.Status == "superseded" {
				continue
			}
			if dv.BindingStale {
				staleCards++
			}
			if dv.Status == "delivered" && !dv.BindingStale && (dv.LastReadbackReason == "" || dv.LastReadbackMatches) {
				deliveredCards++
			} else {
				pendingCards++
			}
		}
	}

	pendingOrders := make([]map[string]any, 0)
	orderContinuities := make([]map[string]any, 0, len(orders))
	hwTotalOrders, hwPaidOrders, hwPaidNotDelivered := 0, 0, 0
	for _, o := range orders {
		var fulfillmentPtr *billing.HardwareFulfillment
		if o.Kind == billing.KindHardware {
			hwTotalOrders++
			if f, ferr := s.St.GetHardwareFulfillment(tenantID, o.ID); ferr == nil {
				fulfillmentPtr = &f
			} else if !errors.Is(ferr, sql.ErrNoRows) {
				fail(w, http.StatusInternalServerError, "internal", "fulfillment lookup failed")
				return
			}
		}
		checkout := s.originalOrderCheckout(c, o)
		cont := s.buildOrderContinuityForCaller(c, o, checkout, fulfillmentPtr, deliveriesByOrder[o.ID], refundsByOrder[o.ID])
		entry := map[string]any{
			"order":      o,
			"checkout":   checkout,
			"continuity": cont,
		}
		if o.Kind == billing.KindHardware {
			entry["deliveries"] = deliveriesByOrder[o.ID]
			if cont.Paid {
				hwPaidOrders++
			}
			if cont.HardwarePending {
				hwPaidNotDelivered++
			}
		}
		if len(refundsByOrder[o.ID]) > 0 {
			entry["refunds"] = refundsByOrder[o.ID]
		}
		orderContinuities = append(orderContinuities, entry)

		if cont.LifecycleStage != billing.StageServiceActive && cont.LifecycleStage != billing.StageCancelled {
			pendingOrders = append(pendingOrders, entry)
		}
		if cont.LifecycleStage == billing.StageFailed || cont.LifecycleStage == billing.StageRefundInProgress || cont.LifecycleStage == billing.StageHardwarePending {
			exceptions = append(exceptions, billingContinuityException{
				Code:        cont.NextStep.Code,
				Domain:      cont.NextStep.Domain,
				ObjectRef:   o.ID,
				Summary:     cont.NextStep.Summary,
				ActionLabel: cont.NextStep.ActionLabel,
				TargetPath:  cont.NextStep.TargetPath,
				Owner:       cont.NextStep.Owner,
			})
		}
	}

	// 5. 内容制作原单接续投影(直接从 billing_checkout_refs 读取,免去前端 N+1 猜测)。
	prodRefs, err := s.St.ListBillingCheckoutRefs(tenantID, "content_production")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "checkout continuation list failed")
		return
	}
	prodContinuations := make([]map[string]any, 0, len(prodRefs))
	for _, ref := range prodRefs {
		h, herr := s.St.GetProductionHandoff(tenantID, ref.HandoffID)
		if herr != nil {
			continue
		}
		refRow, remote, inspectErr := s.inspectCheckoutRef(r.Context(), tenantID, ref)
		view := s.checkoutRefView(refRow, remote, inspectErr)
		avail, _ := view["available"].(bool)
		cont := buildProductionContinuityForCaller(c, h, refRow, avail)
		prodContinuations = append(prodContinuations, map[string]any{
			"store_id":   h.StoreID,
			"handoff_id": h.ID,
			"checkout":   view,
			"continuity": cont,
		})
	}

	if exceptions == nil {
		exceptions = []billingContinuityException{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subscription":             subView["subscription"],
		"store_usage":              subView["store_usage"],
		"catalog_readiness":        catalogReadiness,
		"payer_authority":          payerAuthority,
		"checkout_channel":         checkoutChannel,
		"pending_orders":           pendingOrders,
		"orders":                   orderContinuities,
		"production_continuations": prodContinuations,
		"hardware_summary": map[string]any{
			"total_orders":             hwTotalOrders,
			"paid_orders":              hwPaidOrders,
			"delivered_cards":          deliveredCards,
			"pending_acceptance_cards": pendingCards,
			"stale_cards":              staleCards,
			"paid_not_delivered_count": hwPaidNotDelivered,
		},
		"refund_summary": map[string]any{
			"count":          len(allRefunds),
			"refunded_minor": totalRefundedMinor,
		},
		"exceptions": exceptions,
		"actor_context": map[string]any{
			"tenant_id": tenantID,
			"role":      c.Member.Role,
			"is_agent":  isAgent,
			"can_pay":   canPay,
		},
	})
}
