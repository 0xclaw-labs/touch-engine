// handlers_billing_checkout.go: HUI-4664 原单收银接续 HTTP 面(统一收敛公共
// Mint/Inspect 消费者,覆盖内容制作原单 cph_* 与订阅/续费/扩店/硬件原单 bo_*)。
//
// 纪律(裁定书 / touch-order-checkout/v1.1 / 2222 红线):
//   - 续付 = 读回原单发起/恢复公共 Mint,不是新购:一切资金与身份事实(tenant/
//     order/quote/pricing_version/金额/币种/付款主体/代付授权快照)全部由服务端
//     从既有不可变快照装配;请求体最多接受 provider(wechat/alipay,缺省 wechat),
//     浏览器声明的价格/金额/付款人一律忽略;
//   - 真实调用公共 Mint/Inspect:绝不在本地硬拼假收银路径,也绝不伪造本地 paid
//     状态。code_url / return_url 只在权威响应中即时给出,永不落库;
//   - seam 配置驱动:TouchCheckout 客户端就绪(或 FEATURE_BILLING_CHECKOUT=on 且
//     PLATFORM_BILLING_BASE_URL + PLATFORM_BILLING_TOKEN 齐备)才允许 Mint/Inspect;
//     未配置 = 诚实 503 checkout_unavailable,原单与报价保留,回查不受影响;
//   - paid 禁再付:一旦原单已付(order.status!=processing / payment_ref!="" /
//     checkout.state!=checkout_pending / 已有同单退款),再次发起 Mint 一律 409
//     order_already_paid,返回原单只读投影与下一步;
//   - 普通返回(GET)只读回查:忽略所有 query 参数,绝不推进本地支付/开通状态。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

type checkoutInitiateBody struct {
	Provider string `json:"provider"`
}

func parseCheckoutProvider(r *http.Request) (string, error) {
	if r.Body == nil {
		return billing.TouchProviderWechat, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return billing.TouchProviderWechat, nil
	}
	var body checkoutInitiateBody
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil {
		return "", err
	}
	provider := strings.TrimSpace(body.Provider)
	if provider == "" {
		return billing.TouchProviderWechat, nil
	}
	if !billing.ValidTouchProvider(provider) {
		return "", errors.New("unsupported checkout provider")
	}
	return provider, nil
}

// billingCheckoutSeamReady answers honestly whether the real public-ai
// Mint/Inspect consumer seam is wired.
func (s *Server) billingCheckoutSeamReady() bool {
	if s.TouchCheckout != nil {
		return true
	}
	return s.Cfg.FeatureBillingCheckout &&
		strings.TrimSpace(s.Cfg.BillingBaseURL) != "" &&
		strings.TrimSpace(s.Cfg.BillingToken) != ""
}

func (s *Server) touchCheckoutClient() billing.TouchCheckoutClient {
	if s.TouchCheckout != nil {
		return s.TouchCheckout
	}
	if !s.billingCheckoutSeamReady() {
		return nil
	}
	return billing.NewHTTPTouchCheckoutClient(s.Cfg.BillingBaseURL, s.Cfg.BillingToken, checkoutActivationClient)
}

func (s *Server) checkoutReturnTarget() string {
	if t := strings.TrimSpace(s.Cfg.CashierTargetID); t != "" {
		return t
	}
	return billing.TouchCheckoutReturnTarget
}

// checkoutRefView projects one billing_checkout_refs row plus optional live
// authoritative Mint/Inspect payload onto the merchant checkout gate vocabulary.
// code_url / checkout_url ONLY come from authoritative remote responses and are
// NEVER fabricated locally or stored in SQLite.
func (s *Server) checkoutRefView(row store.BillingCheckoutRefRow, remote *billing.TouchCheckoutResult, inspectUnavailable bool) map[string]any {
	remoteStatus := row.RemoteStatus
	remoteRef := row.RemoteCheckoutRef
	outTradeNo := row.OutTradeNo
	expiresAt := row.ExpiresAt
	priceDigest := row.PriceDigest
	returnTarget := row.ReturnTarget
	codeURL := ""
	returnURL := ""
	if remote != nil {
		if remote.CheckoutRef != "" {
			remoteRef = remote.CheckoutRef
		}
		if remote.OutTradeNo != "" {
			outTradeNo = remote.OutTradeNo
		}
		if remote.Status != "" {
			remoteStatus = remote.Status
		}
		if remote.ExpiresAt != "" {
			expiresAt = remote.ExpiresAt
		}
		if remote.PriceDigest != "" {
			priceDigest = remote.PriceDigest
		}
		if remote.ReturnTarget != "" {
			returnTarget = remote.ReturnTarget
		}
		codeURL = strings.TrimSpace(remote.CodeURL)
		returnURL = strings.TrimSpace(remote.ReturnURL)
	}

	view := map[string]any{
		"checkout_ref":        row.CheckoutRef,
		"remote_checkout_ref": remoteRef,
		"out_trade_no":        outTradeNo,
		"provider":            row.Provider,
		"order_ref":           row.OrderRef,
		"order_kind":          row.OrderKind,
		"quote_ref":           row.QuoteRef,
		"state":               row.State,
		"remote_status":       remoteStatus,
		"amount_minor":        row.AmountMinor,
		"currency":            row.Currency,
		"expires_at":          expiresAt,
		"price_digest":        priceDigest,
		"return_target":       returnTarget,
		"available":           false,
		"app_id":              "",
		"launch_target_id":    "",
	}
	if returnURL != "" {
		view["return_url"] = returnURL
	}
	if inspectUnavailable {
		view["inspect_unavailable"] = true
	}

	switch {
	case row.State != billing.CheckoutStatePending:
		view["reason"] = "not_awaiting_payment"
		if row.State == billing.CheckoutStateFailed && row.FailReason != "" {
			view["fail_reason"] = row.FailReason
		}
	case remoteStatus == "paid":
		// 公共侧已结算,等待 billing_authority payment_paid 事实同步至本地:
		// 绝不再开放支付按钮,避免二次扣费。
		view["reason"] = "not_awaiting_payment"
		view["sync_pending"] = true
	case remoteStatus == "cancelled" || remoteStatus == "mismatch":
		view["reason"] = "not_awaiting_payment"
	case !s.billingCheckoutSeamReady():
		view["reason"] = "checkout_unavailable"
	case codeURL != "":
		view["available"] = true
		view["code_url"] = codeURL
		view["checkout_url"] = codeURL
		view["app_id"] = s.Cfg.CashierAppID
		view["launch_target_id"] = returnTarget
	default:
		// 已有本地引用但本次未拿到权威即时 code_url(例如 Inspect 暂不可用或尚未 Mint):
		// 诚实告知 checkout_unavailable,不伪造本地 URL。
		view["reason"] = "checkout_unavailable"
	}
	return view
}

// inspectCheckoutRef performs a strictly read-only Inspect against public-ai
// when the seam is wired. It refreshes non-URL metadata (remote_status/out_trade_no/
// remote_checkout_ref) without EVER advancing local payment/activation state.
func (s *Server) inspectCheckoutRef(ctx context.Context, tenantID string, row store.BillingCheckoutRefRow) (store.BillingCheckoutRefRow, *billing.TouchCheckoutResult, bool) {
	client := s.touchCheckoutClient()
	if client == nil {
		return row, nil, false
	}
	res, err := client.Inspect(ctx, row.CheckoutRef)
	if err != nil {
		if errors.Is(err, billing.ErrRemoteCheckoutNotFound) {
			return row, nil, false
		}
		return row, nil, true
	}
	if updated, uerr := s.St.UpdateBillingCheckoutRemoteMetadata(tenantID, row.CheckoutRef, store.BillingCheckoutRemoteMeta{
		RemoteCheckoutRef: res.CheckoutRef,
		OutTradeNo:        res.OutTradeNo,
		RemoteStatus:      res.Status,
		ReturnTarget:      res.ReturnTarget,
		PriceDigest:       res.PriceDigest,
		ExpiresAt:         res.ExpiresAt,
	}, nowRFC3339()); uerr == nil {
		row = updated
	}
	return row, &res, false
}

// orderCheckoutGate upgrades the cashier read for ONE original order:
//   - 无 checkout 引用:若 seam 就绪则诚实标注可发起 Mint(available=false,
//     reason=checkout_mint_ready / checkout_unwired);为兼容既有未接线读面纪律,
//     当 seam 未就绪时保持字节兼容的 checkout_unwired。
//   - 有 checkout 引用:通过只读 Inspect 透出权威远端状态与即时 code_url。
func (s *Server) orderCheckoutGate(tenantID, orderRef string) map[string]any {
	gate := s.cashierCheckout() // base: available=false, reason=checkout_unwired
	row, err := s.St.GetBillingCheckoutRefByOrder(tenantID, orderRef)
	if err != nil {
		if s.billingCheckoutSeamReady() {
			gate["mint_supported"] = true
		}
		return gate
	}
	row, remote, inspectErr := s.inspectCheckoutRef(context.Background(), tenantID, row)
	for k, v := range s.checkoutRefView(row, remote, inspectErr) {
		gate[k] = v
	}
	return gate
}

func writeRemoteCheckoutError(w http.ResponseWriter, err error, extra map[string]any) {
	body := map[string]any{}
	for k, v := range extra {
		body[k] = v
	}
	switch {
	case errors.Is(err, billing.ErrRemotePriceChanged):
		body["error"] = "price_changed"
		body["message"] = "原报价版本已变更或已过期，未生成新的收费单；原单记录已保留，请先刷新生效目录后再确认。"
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, billing.ErrRemoteIdempotencyConflict):
		body["error"] = "idempotency_conflict"
		body["message"] = "同一原单或幂等键携带了不同参数（金额/付款主体/价格版本不一致），已拒绝重复下单。"
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, billing.ErrRemoteTargetNotAllowed):
		body["error"] = "return_target_not_allowed"
		body["message"] = "公共收银侧拒绝了当前受控回跳目标，请联系平台核对配置。"
		writeJSON(w, http.StatusForbidden, body)
	case errors.Is(err, billing.ErrRemotePayerUnknown):
		body["error"] = "payer_unknown"
		body["message"] = "公共结算侧未找到当前付款主体锚点，拒绝收款；请核对付款主体身份后重试。"
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, billing.ErrRemoteInvalidRequest):
		body["error"] = "invalid_checkout_request"
		body["message"] = "收银单参数不符合公共结算契约，已拒绝发起。"
		writeJSON(w, http.StatusBadRequest, body)
	default:
		body["error"] = "checkout_unavailable"
		body["message"] = "公共结算侧收银暂时不可用：原单与报价均已保留，稍后重试会沿用同一原单引用，切勿重复下单。"
		writeJSON(w, http.StatusServiceUnavailable, body)
	}
}

// ---- 内容制作原单 cph_* 收银接续面 -------------------------------------------

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
// 真实调用公共 Mint(touch-order-checkout/v1.1), 返回权威 checkout_ref / remote_checkout_ref /
// code_url。已付原单禁止再付(409 order_already_paid); 受理丢响应/重启后同单重试复用同一 ck_*。
func (s *Server) handleProductionHandoffCheckout(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	payer := s.payerDecisionFor(c)
	if !s.callerHasPayerGrant(c) || !payer.Authorized {
		fail(w, http.StatusForbidden, billing.PayerReasonRequired,
			"当前账号没有独立付款授权，不能为制作原单发起付款。请交由商家负责人确认办理；原单与报价均已保留。")
		return
	}
	provider, perr := parseCheckoutProvider(r)
	if perr != nil {
		fail(w, http.StatusBadRequest, "bad_provider", "provider must be wechat or alipay")
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
	// 先查既有引用:一旦本地已落 paid / activated, 严格禁止再次支付(409)。
	if existing, gerr := s.St.GetBillingCheckoutRefByOrder(tenantID, h.ID); gerr == nil &&
		(existing.State == billing.CheckoutStatePaid || existing.State == billing.CheckoutStateActivated) {
		view := s.checkoutRefView(existing, nil, false)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "order_already_paid",
			"message":    "该制作原单已收到支付事实，禁止再次发起付款；请回查原单同步状态。",
			"handoff":    productionHandoffView(h),
			"checkout":   view,
			"continuity": buildProductionContinuityForCaller(c, h, existing, false),
		})
		return
	}

	payerPrincipal := strings.TrimSpace(payer.PayerPrincipal)
	if payerPrincipal == "" {
		payerPrincipal = s.productionAccount(tenantID)
	}
	pricingVer := strings.TrimSpace(usage.PricingVersion)
	if pricingVer == "" {
		pricingVer = firstNonEmptyStr(strings.TrimSpace(s.Cfg.ProductionPricingVersion), "v1")
	}
	row, replayed, err := s.St.UpsertBillingCheckoutRef(store.NewBillingCheckoutRef{
		TenantID:           tenantID,
		HandoffID:          h.ID,
		QuoteRef:           h.QuoteRef,
		OrderRef:           h.ID,
		OrderKind:          "content_production",
		AmountMinor:        usage.AmountMinor,
		Currency:           usage.Currency,
		PayerRef:           payerPrincipal,
		PayerGrantRef:      payer.PayerGrantRef,
		PayerGrantRevision: payer.GrantRevision,
		PricingVersion:     pricingVer,
		IntentKey:          firstNonEmptyStr(h.IdempotencyKey, h.ID),
		ReturnTarget:       s.checkoutReturnTarget(),
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
	if row.State == billing.CheckoutStatePaid || row.State == billing.CheckoutStateActivated {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "order_already_paid",
			"message":    "该制作原单已收到支付事实，禁止再次发起付款；请回查原单状态。",
			"handoff":    productionHandoffView(h),
			"checkout":   s.checkoutRefView(row, nil, false),
			"continuity": buildProductionContinuityForCaller(c, h, row, false),
		})
		return
	}

	// 真实调用公共 Mint(source_order_ref = 本地稳定 ck_* 路由键)。
	client := s.touchCheckoutClient()
	mintRes, merr := client.Mint(r.Context(), billing.TouchCheckoutMintRequest{
		SourceApp:      billing.TouchCheckoutAppID,
		SourceOrderRef: row.CheckoutRef,
		IntentKey:      firstNonEmptyStr(row.IntentKey, row.CheckoutRef),
		TenantRef:      tenantID,
		Provider:       provider,
		Payer: billing.TouchCheckoutPayer{
			PrincipalRef:  firstNonEmptyStr(row.PayerRef, payerPrincipal),
			GrantRef:      row.PayerGrantRef,
			GrantRevision: row.PayerGrantRevision,
		},
		Line: billing.TouchCheckoutLine{
			Kind:           firstNonEmptyStr(usage.Capability, "content_production"),
			PlanCode:       "",
			PriceRef:       usage.QuoteRef,
			PricingVersion: pricingVer,
			AmountMinor:    row.AmountMinor,
			Currency:       firstNonEmptyStr(row.Currency, "CNY"),
		},
		ReturnTarget: s.checkoutReturnTarget(),
	})
	if merr != nil {
		writeRemoteCheckoutError(w, merr, map[string]any{
			"handoff": productionHandoffView(h),
			"quote":   map[string]any{"quote_ref": h.QuoteRef, "amount_minor": usage.AmountMinor, "currency": usage.Currency},
			"checkout": map[string]any{
				"checkout_ref": row.CheckoutRef, "order_ref": row.OrderRef, "state": row.State,
				"available": false, "app_id": "", "launch_target_id": "", "reason": "checkout_unavailable",
			},
		})
		return
	}
	if mintRes.Status == "paid" {
		// 公共侧已付款但本地尚未收到 webhook:禁止重复付款,回写只读远端状态并返回 409。
		row, _ = s.St.UpdateBillingCheckoutRemoteMetadata(tenantID, row.CheckoutRef, store.BillingCheckoutRemoteMeta{
			RemoteCheckoutRef: mintRes.CheckoutRef,
			OutTradeNo:        mintRes.OutTradeNo,
			Provider:          provider,
			RemoteStatus:      mintRes.Status,
			ReturnTarget:      mintRes.ReturnTarget,
			PriceDigest:       mintRes.PriceDigest,
			ExpiresAt:         mintRes.ExpiresAt,
		}, nowRFC3339())
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "order_already_paid",
			"message":    "公共收银侧显示该原单已付款，正在等待服务端权威同步；请回查原单，切勿重复付款。",
			"handoff":    productionHandoffView(h),
			"checkout":   s.checkoutRefView(row, &mintRes, false),
			"continuity": buildProductionContinuityForCaller(c, h, row, false),
		})
		return
	}
	updated, uerr := s.St.UpdateBillingCheckoutRemoteMetadata(tenantID, row.CheckoutRef, store.BillingCheckoutRemoteMeta{
		RemoteCheckoutRef: mintRes.CheckoutRef,
		OutTradeNo:        mintRes.OutTradeNo,
		Provider:          provider,
		RemoteStatus:      firstNonEmptyStr(mintRes.Status, "pending"),
		ReturnTarget:      firstNonEmptyStr(mintRes.ReturnTarget, s.checkoutReturnTarget()),
		PriceDigest:       mintRes.PriceDigest,
		ExpiresAt:         mintRes.ExpiresAt,
	}, nowRFC3339())
	if uerr != nil {
		if errors.Is(uerr, store.ErrBillingIdempotencyConflict) {
			fail(w, http.StatusConflict, "idempotency_conflict", "公共侧返回了与既有原单不一致的远端收银引用，已拒绝覆盖。")
			return
		}
		packStoreFail(w, uerr)
		return
	}
	row = updated
	isReplay := replayed || mintRes.Replayed
	status := http.StatusCreated
	if isReplay {
		status = http.StatusOK
	}
	view := s.checkoutRefView(row, &mintRes, false)
	avail, _ := view["available"].(bool)
	writeJSON(w, status, map[string]any{
		"handoff":    productionHandoffView(h),
		"replayed":   isReplay,
		"checkout":   view,
		"continuity": buildProductionContinuityForCaller(c, h, row, avail),
	})
}

// handleProductionHandoffCheckoutGet answers
// GET /api/v1/stores/{id}/content-production-handoffs/{handoffId}/checkout —
// 只读回查投影(浏览器从收银台返回的落地调用;永不信 query 参数,永不写本地支付状态)。
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
		writeJSON(w, http.StatusOK, map[string]any{
			"handoff":  productionHandoffView(h),
			"checkout": map[string]any{"available": false, "app_id": "", "launch_target_id": "", "reason": "checkout_unwired"},
		})
		return
	}
	row, remote, inspectErr := s.inspectCheckoutRef(r.Context(), tenantID, row)
	view := s.checkoutRefView(row, remote, inspectErr)
	avail, _ := view["available"].(bool)
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff":    productionHandoffView(h),
		"checkout":   view,
		"continuity": buildProductionContinuityForCaller(c, h, row, avail),
	})
}

func buildProductionContinuityForCaller(c *caller, h store.ProductionHandoff, row store.BillingCheckoutRefRow, checkoutAvailable bool) billing.OrderContinuityProjection {
	isAgent := c != nil && c.Member != nil && c.Member.Role == string(authz.RoleAgent)
	canPay := c != nil && c.Member != nil && (c.Member.Role == string(authz.RoleOrgOwner) || (isAgent && row.PayerGrantRef != ""))
	return billing.BuildProductionContinuity(billing.ProductionContinuityInput{
		CheckoutRef:       row.CheckoutRef,
		OrderRef:          row.OrderRef,
		HandoffStatus:     h.Status,
		State:             row.State,
		RemoteStatus:      row.RemoteStatus,
		PaymentRef:        row.PaymentRef,
		FailReason:        row.FailReason,
		CheckoutAvailable: checkoutAvailable,
		ActorCanPay:       canPay,
		ActorIsAgent:      isAgent,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	})
}

// ---- 订阅/续费/扩店/硬件原单 bo_* 统一公共 Mint/Inspect 面 --------------------

type frozenOrderSnapshot struct {
	PriceRef           string `json:"price_ref"`
	PricingVersion     string `json:"pricing_version"`
	ServiceDays        int    `json:"service_days"`
	IncludedStoreCount int    `json:"included_store_count"`
	AdditionalStores   int    `json:"additional_stores"`
}

func parseFrozenOrderSnapshot(order store.BillingOrderRow) frozenOrderSnapshot {
	var snap frozenOrderSnapshot
	_ = json.Unmarshal([]byte(order.PriceSnapshot), &snap)
	if snap.PriceRef == "" {
		snap.PriceRef = firstNonEmptyStr(order.PriceRef, order.Kind)
	}
	if snap.PricingVersion == "" {
		snap.PricingVersion = firstNonEmptyStr(order.PricingVersion, "v1")
	}
	return snap
}

// handleBillingOrderCheckout answers POST /api/v1/billing/orders/{id}/checkout —
// 为既有订阅(new/renew/expand)或硬件(purchase)原单发起/恢复公共 Mint。
// 绝不按今日目录重计价,绝不转移付款主体,已付/退款中原单严禁再付。
func (s *Server) handleBillingOrderCheckout(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if d := authz.Can(s.resolveAccess(c), string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing access is not allowed for this member")
		return
	}
	provider, perr := parseCheckoutProvider(r)
	if perr != nil {
		fail(w, http.StatusBadRequest, "bad_provider", "provider must be wechat or alipay")
		return
	}
	order, err := s.St.GetBillingOrder(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "order not found in this tenant scope")
		return
	}
	// 严格复核实时付款授权与原单不可变付款主体一致(撤权立即拦截)。
	payer := s.payerDecisionFor(c)
	if !s.callerHasPayerGrant(c) || !payer.Authorized ||
		payer.PayerPrincipal != order.PayerPrincipal ||
		payer.PayerGrantRef != order.PayerGrantRef ||
		payer.RelationRef != order.AgencyRelationRef {
		fail(w, http.StatusForbidden, "original_payer_required",
			"当前账号不具备这笔原单的有效付款授权（代理无独立付款权或原授权已变更/撤回）；请交由商家负责人或原付款人处理，绝不误扣他人账户。")
		return
	}
	refunds, rerr := s.St.ListBillingRefundsByOrder(c.Member.TenantID, order.ID)
	if rerr != nil {
		fail(w, http.StatusInternalServerError, "internal", "order refunds lookup failed")
		return
	}
	// paid / opening / active / cancelled / failed / refunding 一律禁止再付!
	if order.Status != billing.OrderProcessing || order.PaymentRef != "" || len(refunds) > 0 {
		checkout := s.originalOrderCheckout(c, order)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "order_already_paid",
			"message":    "该原单已不在待支付状态（已付款/开通中/已生效/已撤单或已进入退款对账），禁止再次付款；请按原单回查结果继续办理。",
			"order":      order,
			"checkout":   checkout,
			"refunds":    refunds,
			"continuity": s.buildOrderContinuityForCaller(c, order, checkout, nil, nil, refunds),
		})
		return
	}
	if !s.billingCheckoutSeamReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "checkout_unavailable",
			"message": "公共结算侧收银尚未接通：原单与价格快照均已保留，可继续回查，无需重新下单。",
			"order":   order,
			"checkout": map[string]any{
				"available": false, "app_id": "", "launch_target_id": "", "reason": "checkout_unavailable",
			},
		})
		return
	}

	snap := parseFrozenOrderSnapshot(order)
	row, replayed, err := s.St.UpsertBillingCheckoutRef(store.NewBillingCheckoutRef{
		TenantID:           c.Member.TenantID,
		OrderRef:           order.ID,
		OrderKind:          order.Kind,
		QuoteRef:           snap.PriceRef,
		AmountMinor:        order.AmountMinor,
		Currency:           firstNonEmptyStr(order.Currency, "CNY"),
		PayerRef:           order.PayerPrincipal,
		PayerGrantRef:      order.PayerGrantRef,
		PayerGrantRevision: order.PayerGrantRevision,
		PricingVersion:     snap.PricingVersion,
		IntentKey:          order.IdempotencyKey,
		ReturnTarget:       s.checkoutReturnTarget(),
	}, nowRFC3339())
	if err != nil {
		if errors.Is(err, store.ErrBillingIdempotencyConflict) {
			fail(w, http.StatusConflict, "idempotency_conflict", "原单已绑定不同的收银引用，已拒绝重复发起。")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "checkout reference registration failed")
		return
	}
	if row.State == billing.CheckoutStatePaid || row.State == billing.CheckoutStateActivated {
		view := s.checkoutRefView(row, nil, false)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "order_already_paid",
			"message":    "该原单已收到支付事实，禁止再次付款；请回查原单状态。",
			"order":      order,
			"checkout":   view,
			"continuity": s.buildOrderContinuityForCaller(c, order, view, nil, nil, refunds),
		})
		return
	}

	client := s.touchCheckoutClient()
	mintRes, merr := client.Mint(r.Context(), billing.TouchCheckoutMintRequest{
		SourceApp:      billing.TouchCheckoutAppID,
		SourceOrderRef: row.CheckoutRef,
		IntentKey:      order.IdempotencyKey,
		TenantRef:      c.Member.TenantID,
		Provider:       provider,
		Payer: billing.TouchCheckoutPayer{
			PrincipalRef:  order.PayerPrincipal,
			GrantRef:      order.PayerGrantRef,
			GrantRevision: order.PayerGrantRevision,
		},
		Line: billing.TouchCheckoutLine{
			Kind:               order.Kind,
			PlanCode:           order.PlanCode,
			PriceRef:           snap.PriceRef,
			PricingVersion:     snap.PricingVersion,
			AmountMinor:        order.AmountMinor,
			Currency:           firstNonEmptyStr(order.Currency, "CNY"),
			ServiceDays:        snap.ServiceDays,
			IncludedStoreCount: snap.IncludedStoreCount,
			AdditionalStores:   snap.AdditionalStores,
		},
		ReturnTarget: s.checkoutReturnTarget(),
	})
	if merr != nil {
		writeRemoteCheckoutError(w, merr, map[string]any{
			"order": order,
			"checkout": map[string]any{
				"checkout_ref": row.CheckoutRef, "order_ref": order.ID, "state": row.State,
				"available": false, "app_id": "", "launch_target_id": "", "reason": "checkout_unavailable",
			},
		})
		return
	}
	if mintRes.Status == "paid" {
		row, _ = s.St.UpdateBillingCheckoutRemoteMetadata(c.Member.TenantID, row.CheckoutRef, store.BillingCheckoutRemoteMeta{
			RemoteCheckoutRef: mintRes.CheckoutRef,
			OutTradeNo:        mintRes.OutTradeNo,
			Provider:          provider,
			RemoteStatus:      mintRes.Status,
			ReturnTarget:      mintRes.ReturnTarget,
			PriceDigest:       mintRes.PriceDigest,
			ExpiresAt:         mintRes.ExpiresAt,
		}, nowRFC3339())
		view := s.checkoutRefView(row, &mintRes, false)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "order_already_paid",
			"message":    "公共收银侧显示该原单已付款，正在等待权威事实同步；请回查原单，切勿重复付款。",
			"order":      order,
			"checkout":   view,
			"continuity": s.buildOrderContinuityForCaller(c, order, view, nil, nil, refunds),
		})
		return
	}
	updated, uerr := s.St.UpdateBillingCheckoutRemoteMetadata(c.Member.TenantID, row.CheckoutRef, store.BillingCheckoutRemoteMeta{
		RemoteCheckoutRef: mintRes.CheckoutRef,
		OutTradeNo:        mintRes.OutTradeNo,
		Provider:          provider,
		RemoteStatus:      firstNonEmptyStr(mintRes.Status, "pending"),
		ReturnTarget:      firstNonEmptyStr(mintRes.ReturnTarget, s.checkoutReturnTarget()),
		PriceDigest:       mintRes.PriceDigest,
		ExpiresAt:         mintRes.ExpiresAt,
	}, nowRFC3339())
	if uerr != nil {
		if errors.Is(uerr, store.ErrBillingIdempotencyConflict) {
			fail(w, http.StatusConflict, "idempotency_conflict", "远端收银引用与既有原单不一致，已拒绝覆盖。")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "checkout metadata persist failed")
		return
	}
	row = updated
	isReplay := replayed || mintRes.Replayed
	status := http.StatusCreated
	if isReplay {
		status = http.StatusOK
	}
	view := s.checkoutRefView(row, &mintRes, false)
	writeJSON(w, status, map[string]any{
		"order":      order,
		"replayed":   isReplay,
		"checkout":   view,
		"continuity": s.buildOrderContinuityForCaller(c, order, view, nil, nil, refunds),
	})
}

// handleBillingOrderCheckoutGet answers GET /api/v1/billing/orders/{id}/checkout —
// 只读回查原单收银状态(浏览器普通返回落地,忽略一切 query 参数,永不推进本地状态)。
func (s *Server) handleBillingOrderCheckoutGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if d := authz.Can(s.resolveAccess(c), string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "billing view is not allowed for this member")
		return
	}
	order, err := s.St.GetBillingOrder(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "order not found in this tenant scope")
		return
	}
	checkout := s.originalOrderCheckout(c, order)
	refunds, _ := s.St.ListBillingRefundsByOrder(c.Member.TenantID, order.ID)
	var fulfillmentPtr *billing.HardwareFulfillment
	var deliveries []store.TouchpointDeliveryView
	if order.Kind == billing.KindHardware {
		if f, ferr := s.St.GetHardwareFulfillment(c.Member.TenantID, order.ID); ferr == nil {
			fulfillmentPtr = &f
		}
		if all, derr := s.St.ListTouchpointDeliveryViews(c.Member.TenantID, store.TouchpointDeliveryFilter{}); derr == nil {
			for _, dv := range all {
				if dv.HardwareOrderID == order.ID {
					deliveries = append(deliveries, dv)
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order":      order,
		"checkout":   checkout,
		"refunds":    refunds,
		"continuity": s.buildOrderContinuityForCaller(c, order, checkout, fulfillmentPtr, deliveries, refunds),
	})
}
