// handlers_billing.go: HUI-4591 订购、收费与账单商家面(M12 后端)。
//
// 纪律(裁定书):
//   - 管理面不加 FEATURE 开关(仓内惯例);读=billing:view(org_owner 恒有;
//     agent 仅 renewal grant);订阅/硬件下单=billing:manage_payer(org_owner;
//     agent 需激活关系含 renewal 且 payer_grant_ref 非空——服务端解析,客户
//     端声明一律忽略,4593 payer_grant_ref 真值接线)。
//   - 本面永不推进支付状态:下单只落 processing 投影;开通只由受信权威缝的
//     事件驱动(浏览器从收银台跳回 = 读面回查 GET orders/{id},不当支付证明)。
//   - 金额全部来自目录快照;代码零价格常量;响应永不出现 balance/wallet 字段。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// registerBillingRoutes wires the M12 merchant surface (admin side; no flag).
func (s *Server) registerBillingRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/billing/subscription", s.requireSession(s.handleBillingSubscription))
	mux.Handle("GET /api/v1/billing/price-catalog", s.requireSession(s.handleBillingCatalog))
	mux.Handle("POST /api/v1/billing/subscription-orders", s.requireSession(s.handleBillingSubscriptionOrder))
	mux.Handle("POST /api/v1/billing/hardware-orders", s.requireSession(s.handleBillingHardwareOrder))
	mux.Handle("GET /api/v1/billing/orders", s.requireSession(s.handleBillingOrdersList))
	mux.Handle("GET /api/v1/billing/orders/{id}", s.requireSession(s.handleBillingOrderGet))
	mux.Handle("GET /api/v1/billing/ai-usage", s.requireSession(s.handleBillingAIUsage))
	mux.Handle("GET /api/v1/billing/refunds", s.requireSession(s.handleBillingRefunds))
}

// ---- shared helpers -------------------------------------------------------------

// billingSubscriptionFact is the single tenant-fact read (HUI-1671 接线的真源):
// projection row first; without one the process-level config falls back with
// byte-identical legacy semantics (no row = unconfirmed; "expired" config =
// honest expired with no service date).
func (s *Server) billingSubscriptionFact(tenantID string) (billing.SubscriptionFact, string) {
	f, err := s.St.GetBillingSubscription(tenantID)
	if err == nil {
		return f, f.EffectiveStatus(nowRFC3339())
	}
	status := strings.TrimSpace(s.Cfg.SubscriptionStatus)
	if status == "" {
		status = billing.SubStatusUnconfirmed
	}
	f = billing.SubscriptionFact{TenantID: tenantID, Status: status}
	return f, status
}

// billingEntitlementStatus maps the fact onto the activityspend status vocabulary.
func billingEntitlementStatus(effective string) string {
	switch effective {
	case billing.SubStatusActive:
		return "active"
	case billing.SubStatusExpired:
		return "expired"
	default:
		// processing/opening/failed/unconfirmed = 尚未成立的有效服务期。
		return "unconfirmed"
	}
}

// callerHasPayerGrant resolves the payer-management dimension server-side
// (frozen matrix: org_owner always; agent only with renewal+payer_grant_ref).
func (s *Server) callerHasPayerGrant(c *caller) bool {
	a := s.resolveAccess(c)
	return authz.Can(a, string(authz.PermBillingManagePayer), "").Allowed
}

// payerDecisionFor resolves the order-payer snapshot for order creation.
// The grant view comes from server-side agency rows only (never request input).
func (s *Server) payerDecisionFor(c *caller) billing.PayerDecision {
	role := c.Member.Role
	if role != string(authz.RoleAgent) {
		return billing.DecidePayerForOrder(role, c.Member.PrincipalRef, nil)
	}
	rel, err := s.St.GetActiveAgencyRelation(c.Member.TenantID, c.Member.PrincipalRef)
	if err != nil {
		return billing.DecidePayerForOrder(billing.RoleAgent, c.Member.PrincipalRef, nil)
	}
	grant := &billing.AgencyGrantView{
		RelationRef: rel.ID, AllowedActions: rel.AllowedActions,
		PayerGrantRef: rel.PayerGrantRef, GrantRevision: rel.GrantRevision,
	}
	return billing.DecidePayerForOrder(billing.RoleAgent, c.Member.PrincipalRef, grant)
}

// cashierCheckout reports the eco-cashier jump params (empty = 未接入,页面诚实
// 显示;URL 由生态 shell launch 铸造,本服务从不自造跳转地址)。
func (s *Server) cashierCheckout() map[string]any {
	app := strings.TrimSpace(s.Cfg.CashierAppID)
	if app == "" {
		return map[string]any{"available": false, "app_id": "", "launch_target_id": ""}
	}
	return map[string]any{"available": true, "app_id": app, "launch_target_id": strings.TrimSpace(s.Cfg.CashierTargetID)}
}

// subscriptionView is the GET /billing/subscription payload.
func (s *Server) subscriptionView(tenantID string) (map[string]any, error) {
	fact, effective := s.billingSubscriptionFact(tenantID)
	limit, live, err := s.St.BillingStoreUsage(tenantID)
	if err != nil {
		return nil, err
	}
	retainUntil, _ := fact.RetainUntil()
	return map[string]any{
		"subscription": map[string]any{
			"subscription_ref":       fact.SubscriptionRef,
			"plan_code":              fact.PlanCode,
			"pricing_version":        fact.PricingVersion,
			"status":                 effective, // 到期按租户事实推导(F18)
			"stored_status":          fact.Status,
			"paid_until":             fact.PaidUntil,
			"retain_until":           retainUntil, // 维护保留期(只读保留,不删除)
			"store_limit":            fact.StoreLimit,
			"entitlement_projection": fact.EntitlementProjection,
			"restricts_new_premium":  effective != billing.SubStatusActive,
			"locks_existing":         false, // 已承诺权益不追溯削减(T16)
		},
		"store_usage": map[string]any{"limit": limit, "live": live},
		"checkout":    s.cashierCheckout(),
	}, nil
}

// ---- GET /api/v1/billing/subscription -------------------------------------------

func (s *Server) handleBillingSubscription(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	view, err := s.subscriptionView(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "subscription lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// ---- GET /api/v1/billing/price-catalog -------------------------------------------

func (s *Server) handleBillingCatalog(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	entries, err := s.St.ListBillingCatalog(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "catalog lookup failed")
		return
	}
	// 解析出当前生效行(租户专属>全局,窗口内最新修订),供页面直接消费。
	effective := map[string]billing.CatalogEntry{}
	for _, plan := range []string{billing.PlanMonthly, billing.PlanYearly} {
		if e, err := billing.ResolveCatalog(entries, c.Member.TenantID, plan, nowRFC3339()); err == nil {
			effective[plan] = e
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries, "effective": effective})
}

// ---- POST /api/v1/billing/subscription-orders ------------------------------------

type billingSubscriptionOrderBody struct {
	Action           string `json:"action"` // new | renew | expand
	PlanCode         string `json:"plan_code"`
	PriceRef         string `json:"price_ref"`
	AdditionalStores int    `json:"additional_stores"` // expand 专用
	Reason           string `json:"reason"`
	IdempotencyKey   string `json:"idempotency_key"`
}

func (s *Server) handleBillingSubscriptionOrder(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	// 付款主体管理资格(下单资格):org_owner 恒有;agent 需 renewal+payer grant。
	if !s.callerHasPayerGrant(c) {
		fail(w, http.StatusForbidden, billing.PayerReasonRequired,
			"subscription orders require a resolved payer authorization (agent needs renewal + payer grant)")
		return
	}
	var body billingSubscriptionOrderBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "order body is not valid JSON")
		return
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" {
		fail(w, http.StatusBadRequest, "idempotency_key_required", "an idempotency key is required for billing orders")
		return
	}
	action := strings.TrimSpace(body.Action)
	if action != billing.ActionNew && action != billing.ActionRenew && action != billing.ActionExpand {
		fail(w, http.StatusBadRequest, "bad_action", "action must be new/renew/expand (downgrade rides renewal at period end)")
		return
	}
	plan := strings.TrimSpace(body.PlanCode)
	if plan != billing.PlanMonthly && plan != billing.PlanYearly {
		fail(w, http.StatusBadRequest, "bad_plan", "plan_code must be store.plan.month or store.plan.year")
		return
	}
	// 目录解析(零价格常量):订单引用此刻生效行并整行快照。
	entries, err := s.St.ListBillingCatalog(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "catalog lookup failed")
		return
	}
	entry, err := billing.ResolveCatalog(entries, c.Member.TenantID, plan, nowRFC3339())
	if err != nil {
		// 目录无生效行:诚实拒绝(不发明价格)。
		fail(w, http.StatusConflict, "catalog_unavailable", "no effective catalog row for this plan; the price authority has not published one")
		return
	}
	if strings.TrimSpace(body.PriceRef) != "" && body.PriceRef != entry.PriceRef {
		fail(w, http.StatusConflict, "catalog_changed", "price_ref does not match the current effective catalog row; refetch the catalog")
		return
	}
	// 金额按动作推导(快照进订单;目录此后变更不改本订单)。
	amount := entry.PriceMinor
	expandExtra := 0
	if action == billing.ActionExpand {
		if body.AdditionalStores <= 0 {
			fail(w, http.StatusBadRequest, "bad_expand", "additional_stores must be positive for expand orders")
			return
		}
		expandExtra = body.AdditionalStores
		amount, err = billing.ExpansionQuote(entry, body.AdditionalStores)
		if err != nil {
			fail(w, http.StatusBadRequest, "bad_expand", "expansion quote failed")
			return
		}
	}
	// 付款授权(4593 真值):org_owner=本人;agent=激活关系快照(payer_grant_ref)。
	payer := s.payerDecisionFor(c)
	if !payer.Authorized {
		fail(w, http.StatusForbidden, payer.Reason, "no live payer authorization for this principal")
		return
	}
	snapshot, _ := json.Marshal(map[string]any{
		"price_ref": entry.PriceRef, "currency": entry.Currency, "price_minor": entry.PriceMinor,
		"service_days": entry.ServiceDays, "included_store_count": entry.IncludedStoreCount,
		"extra_store_price_minor": entry.ExtraStorePriceMinor, "tier_json": entry.TierJSON,
		"grace_days": entry.GraceDays, "pricing_version": entry.PricingVersion,
		"action": action, "additional_stores": expandExtra, "reason": strings.TrimSpace(body.Reason),
	})
	order, replayed, err := s.St.CreateBillingOrder(store.NewBillingOrder{
		TenantID: c.Member.TenantID, Kind: billing.KindSubscription, Action: action,
		PlanCode: plan, PriceRef: entry.PriceRef, PriceSnapshot: string(snapshot),
		AmountMinor: amount, Currency: entry.Currency, IdempotencyKey: body.IdempotencyKey,
		PayerGrantRef: payer.PayerGrantRef, AgencyRelationRef: payer.RelationRef,
		PayerPrincipal: payer.PayerPrincipal, ActorPrincipal: c.Member.PrincipalRef,
		ActorRole: c.Member.Role,
	}, nowRFC3339())
	if err != nil {
		if errors.Is(err, store.ErrBillingBadInput) {
			fail(w, http.StatusBadRequest, "bad_request", "order input is invalid")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "order was not recorded")
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"order":    order,
		"replayed": replayed,
		"checkout": s.cashierCheckout(), // 收银台跳转参数(生态 shell launch 铸造 URL)
	})
}

// ---- POST /api/v1/billing/hardware-orders -----------------------------------------

type billingHardwareItemBody struct {
	SKU             string `json:"sku"`
	Name            string `json:"name"`
	Kind            string `json:"kind"` // goods | service
	Qty             int    `json:"qty"`
	UnitAmountMinor int64  `json:"unit_amount_minor"`
}

func (s *Server) handleBillingHardwareOrder(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.callerHasPayerGrant(c) {
		fail(w, http.StatusForbidden, billing.PayerReasonRequired, "hardware orders require a resolved payer authorization")
		return
	}
	var body struct {
		Items          []billingHardwareItemBody `json:"items"`
		IdempotencyKey string                    `json:"idempotency_key"`
		Reason         string                    `json:"reason"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "hardware order body is not valid JSON")
		return
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" {
		fail(w, http.StatusBadRequest, "idempotency_key_required", "an idempotency key is required for billing orders")
		return
	}
	if len(body.Items) == 0 {
		fail(w, http.StatusBadRequest, "items_required", "hardware orders need at least one one-off goods/service line")
		return
	}
	payer := s.payerDecisionFor(c)
	if !payer.Authorized {
		fail(w, http.StatusForbidden, payer.Reason, "no live payer authorization for this principal")
		return
	}
	var total int64
	items := make([]store.BillingHardwareItem, 0, len(body.Items))
	for _, it := range body.Items {
		if it.Kind != "goods" && it.Kind != "service" {
			fail(w, http.StatusBadRequest, "bad_item", "item kind must be goods or service")
			return
		}
		total += int64(it.Qty) * it.UnitAmountMinor
		items = append(items, store.BillingHardwareItem{
			SKU: strings.TrimSpace(it.SKU), Name: strings.TrimSpace(it.Name), Kind: it.Kind,
			Qty: it.Qty, UnitAmountMinor: it.UnitAmountMinor,
		})
	}
	// 明细金额是商家申报的一次性报价快照;真实扣费事实仍归公共权威(支付事件
	// 推进履约首步),本表不产生任何余额。
	snapshot, _ := json.Marshal(map[string]any{"source": "merchant_declared", "reason": strings.TrimSpace(body.Reason)})
	order, replayed, err := s.St.CreateHardwareOrder(store.NewBillingOrder{
		TenantID: c.Member.TenantID, Kind: billing.KindHardware, Action: billing.ActionPurchase,
		PriceSnapshot: string(snapshot), AmountMinor: total, Currency: "CNY",
		IdempotencyKey: body.IdempotencyKey,
		PayerGrantRef:  payer.PayerGrantRef, AgencyRelationRef: payer.RelationRef,
		PayerPrincipal: payer.PayerPrincipal, ActorPrincipal: c.Member.PrincipalRef,
		ActorRole: c.Member.Role,
	}, items, nowRFC3339())
	if err != nil {
		if errors.Is(err, store.ErrBillingBadInput) {
			fail(w, http.StatusBadRequest, "bad_request", "hardware order input is invalid")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "hardware order was not recorded")
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"order": order, "replayed": replayed, "checkout": s.cashierCheckout()})
}

// ---- orders read surface(状态回查=诚实投影读) --------------------------------------

func (s *Server) handleBillingOrdersList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	rows, err := s.St.ListBillingOrders(c.Member.TenantID, kind)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "order list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) handleBillingOrderGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	order, err := s.St.GetBillingOrder(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		// 他租户/不存在同答 404(掩码)。
		fail(w, http.StatusNotFound, "not_found", "order not found in this tenant scope")
		return
	}
	out := map[string]any{"order": order}
	if order.Kind == billing.KindHardware {
		items, err := s.St.ListHardwareItems(c.Member.TenantID, order.ID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "hardware items lookup failed")
			return
		}
		fulfillment, err := s.St.GetHardwareFulfillment(c.Member.TenantID, order.ID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "fulfillment lookup failed")
			return
		}
		out["items"] = items
		out["fulfillment"] = fulfillment
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- GET /api/v1/billing/ai-usage | refunds ----------------------------------------

func (s *Server) handleBillingAIUsage(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	rows, err := s.St.ListBillingAIUsage(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ai usage lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) handleBillingRefunds(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	rows, err := s.St.ListBillingRefunds(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "refund lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}
