// handlers_billing_checkout.go: HUI-4664 原单收银接续 HTTP 面(ADV-9 PR12,
// R3 §2.2/§2.3 的任务适配版)。
//
// 纪律(裁定书/实现方案/2222 红线):
//   - 续付=读回原单登记接续引用,不是新购:一切事实(tenant/store/handoff/
//     quote/金额/币种/付款主体)服务端从既有行装配,请求体零输入,浏览器
//     声明一律忽略;
//   - 原单成立判定:quote 已消费(ConsumeQuoteProjection 已发生)且消费方
//     =本 handoff——未成立的发起如实 409,绝不按申报放行;
//   - seam 配置驱动:FEATURE_BILLING_CHECKOUT=on 且 PLATFORM_BILLING_BASE_URL
//     在场才报可续付;未配置=诚实 503 checkout_unavailable,报价与原单保留,
//     回查不受影响(绝不伪造收银地址);
//   - 收银门语义升级(cashierCheckout):有 checkout 引用=可续付(报
//     checkout_ref 与续付 URL);无=诚实 checkout_unwired——已存在的
//     checkout.available 门,语义现在为真;
//   - checkout_url 是公共结算侧 URL:只在本响应即时给出,永不落库
//     (落库即制造可重放凭据);返回落地只走读面回查(GET),浏览器携带的
//     任何 query 参数永不推进支付状态(状态只由 billing_authority 事实驱动);
//   - 幂等键贯穿:同单(order_ref)复用同一 checkout_ref;同 payment_ref
//     重放零新增零改写。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// billingCheckoutSeamReady answers honestly whether the public checkout seam
// is wired: the capability switch is on AND the public billing base URL is
// configured (Gate already fails startup on the half-wired pairings).
func (s *Server) billingCheckoutSeamReady() bool {
	return s.Cfg.FeatureBillingCheckout && strings.TrimSpace(s.Cfg.BillingBaseURL) != ""
}

// checkoutURLFor derives the public settlement-side checkout URL for one
// checkout reference. Seam-config-driven only; the URL never lands in the
// database and is never browser-supplied. 对齐点(R3 §6.3.5):上游 PR-4 到位
// 后改为透传公共侧返回的收银 URL,本函数是唯一换源点。
func (s *Server) checkoutURLFor(checkoutRef string) string {
	return s.Cfg.BillingBaseURL + "/billing/touch-checkout/" + checkoutRef
}

// checkoutRefView projects one billing_checkout_refs row onto the merchant
// checkout gate vocabulary. available=true 只在「引用存在 + 等待支付 + seam
// 已接通」时成立;paid/activated/failed 都如实答 not_awaiting_payment
//(没有可续付的款项;failed 附留因)。
func (s *Server) checkoutRefView(row store.BillingCheckoutRefRow) map[string]any {
	view := map[string]any{
		"checkout_ref": row.CheckoutRef,
		"order_ref":    row.OrderRef,
		"quote_ref":    row.QuoteRef,
		"state":        row.State,
		"amount_minor": row.AmountMinor,
		"currency":     row.Currency,
		"available":    false,
	}
	switch {
	case row.State == billing.CheckoutStatePending && s.billingCheckoutSeamReady():
		view["available"] = true
		view["checkout_url"] = s.checkoutURLFor(row.CheckoutRef)
		view["app_id"] = s.Cfg.CashierAppID
		view["launch_target_id"] = s.Cfg.CashierTargetID
	case row.State == billing.CheckoutStatePending:
		view["reason"] = "checkout_unavailable"
	default:
		view["reason"] = "not_awaiting_payment"
		if row.State == billing.CheckoutStateFailed && row.FailReason != "" {
			view["fail_reason"] = row.FailReason
		}
	}
	return view
}

// orderCheckoutGate upgrades the cashier read for ONE original order: 有
// checkout 引用=可续付(报 checkout_ref 与续付 URL);无引用/未接通=诚实
// checkout_unwired(与既有门的字段逐字节兼容)。浏览器返回只走本读门,
// 任何 query 参数不产生状态变化。
func (s *Server) orderCheckoutGate(tenantID, orderRef string) map[string]any {
	gate := s.cashierCheckout() // base: available=false, reason=checkout_unwired
	row, err := s.St.GetBillingCheckoutRefByOrder(tenantID, orderRef)
	if err != nil {
		return gate // 无引用 = 诚实未接通(同形掩蔽不泄漏存在性)
	}
	for k, v := range s.checkoutRefView(row) {
		gate[k] = v
	}
	return gate
}

// handoffCheckoutGuard resolves the caller-owned handoff for the checkout
// faces: tenant-scoped read, URL store must match the stored row (跨店/跨租户
// 一律同形 404,不泄漏存在性——同 retry 面纪律)。
func (s *Server) handoffCheckoutGuard(w http.ResponseWriter, r *http.Request, tenantID, storeID string) (store.ProductionHandoff, bool) {
	h, err := s.St.GetProductionHandoff(tenantID, r.PathValue("handoffId"))
	if err != nil {
		packStoreFail(w, err)
		return store.ProductionHandoff{}, false
	}
	if h.StoreID != storeID {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return store.ProductionHandoff{}, false
	}
	return h, true
}

// handoffOriginalOrder verifies 原单成立: the handoff's quote was consumed by
// THIS handoff (ConsumeQuoteProjection 已发生). Anything else answers an
// honest 409 — the continuation never acts on an unestablished original.
func (s *Server) handoffOriginalOrder(w http.ResponseWriter, tenantID string, h store.ProductionHandoff) (store.BillingAIUsageRow, bool) {
	usage, err := s.St.GetBillingAIUsage(tenantID, h.QuoteRef)
	if err != nil || usage.State != billing.QuoteStateConsumed || usage.ConsumedByRef != h.ID {
		fail(w, http.StatusConflict, "quote_not_consumed",
			"原单尚未成立(报价引用未确认消费):这笔发起没有可续付的原单;请先回查制作发起与报价状态,不要重复付款。")
		return store.BillingAIUsageRow{}, false
	}
	return usage, true
}

// handleProductionHandoffCheckout answers
// POST /api/v1/stores/{id}/content-production-handoffs/{handoffId}/checkout —
// 续付登记面:生成/复用 checkout_ref(同单恢复复用同一引用),seam 未配置=
// 诚实 503(报价与原单保留)。请求体零输入;金额/币种/付款主体全部服务端装配。
func (s *Server) handleProductionHandoffCheckout(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	// 续付资格=付款资格(同报价面):org_owner 恒有;agent 需激活关系含
	// renewal 且 payer_grant_ref 非空——服务端解析,客户端声明一律忽略。
	if !s.callerHasPayerGrant(c) || !s.payerDecisionFor(c).Authorized {
		fail(w, http.StatusForbidden, billing.PayerReasonRequired,
			"当前账号没有付款授权,不能续付原单。请付款管理员确认后续付;原单与报价都保留,不会丢失。")
		return
	}
	tenantID := c.Member.TenantID
	storeID := r.PathValue("id")
	if _, err := s.St.GetStore(storeID, tenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	h, ok := s.handoffCheckoutGuard(w, r, tenantID, storeID)
	if !ok {
		return
	}
	usage, ok := s.handoffOriginalOrder(w, tenantID, h)
	if !ok {
		return
	}
	if !s.billingCheckoutSeamReady() {
		// 诚实 503:公共结算侧收银 seam 未接通;报价与原单保留,回查不受影响。
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "checkout_unavailable",
			"message": "公共结算侧收银尚未接通:这次没有生成可续付引用;报价与原单都已保留,可继续回查,无需重新报价或重新发起制作。",
			"handoff": productionHandoffView(h),
			"quote":   map[string]any{"quote_ref": h.QuoteRef, "amount_minor": usage.AmountMinor, "currency": usage.Currency},
			"checkout": map[string]any{
				"available": false, "app_id": "", "launch_target_id": "", "reason": "checkout_unavailable",
			},
		})
		return
	}
	row, replayed, err := s.St.UpsertBillingCheckoutRef(store.NewBillingCheckoutRef{
		TenantID:    tenantID,
		HandoffID:   h.ID,
		QuoteRef:    h.QuoteRef,
		OrderRef:    h.ID, // 制作接续的原单引用=handoff id(cph_…;同恢复域,见迁移 0139 注)
		AmountMinor: usage.AmountMinor,
		Currency:    usage.Currency,
		PayerRef:    s.productionAccount(tenantID),
	}, nowRFC3339())
	if err != nil {
		if errors.Is(err, store.ErrBillingIdempotencyConflict) {
			fail(w, http.StatusConflict, "idempotency_conflict",
				"同单已存在不同的接续引用(引用恒定纪律):请回查既有续付引用,不要重复发起。")
			return
		}
		packStoreFail(w, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"handoff":  productionHandoffView(h),
		"replayed": replayed,
		"checkout": s.checkoutRefView(row),
	})
}

// handleProductionHandoffCheckoutGet answers
// GET /api/v1/stores/{id}/content-production-handoffs/{handoffId}/checkout —
// 只读回查投影(浏览器从收银台返回的落地调用;永不信 query 参数,永不写状态)。
func (s *Server) handleProductionHandoffCheckoutGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.storeContentPackRead(c, r.PathValue("id"), w) {
		return
	}
	tenantID := c.Member.TenantID
	storeID := r.PathValue("id")
	if _, err := s.St.GetStore(storeID, tenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	h, ok := s.handoffCheckoutGuard(w, r, tenantID, storeID)
	if !ok {
		return
	}
	row, err := s.St.GetBillingCheckoutRefByHandoff(tenantID, h.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			packStoreFail(w, err)
			return
		}
		// 无引用:诚实未接通(同形答 available=false,不泄漏存在性之外的信息)。
		writeJSON(w, http.StatusOK, map[string]any{
			"handoff":  productionHandoffView(h),
			"checkout": map[string]any{"available": false, "app_id": "", "launch_target_id": "", "reason": "checkout_unwired"},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff":  productionHandoffView(h),
		"checkout": s.checkoutRefView(row),
	})
}
