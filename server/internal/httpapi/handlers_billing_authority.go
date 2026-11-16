// handlers_billing_authority.go: 受信权威缝(HUI-4591)——公共订单权威(派诺生态
// 统一账单)向本仓写入可重建投影的唯一通道。
//
// 纪律(裁定书):
//   - 双保险身份:requireBillingAuthority = X-Service-ID + 独立 token(常量时间
//     比较)+ billing_authority 角色(同 requireOpsGrantProducer 模式);无凭据
//     注册 = 503 fail-closed(真实支付网关无凭据时工程面诚实关闭,不伪造成功)。
//   - 商家会话打到本面一律 401(资金事实永不自授)。
//   - source+event_id 去重:同键同负载=幂等读回;同键异负载=409
//     IDEMPOTENCY_CONFLICT;状态推进唯一入口(store.ApplyBillingOrderEvent →
//     billing.ApplyOrderEvent 纯状态机)。
//   - 目录/事件/退款/AI 用量/硬件步骤都是投影写入;本缝永不返回任何余额。
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// billingAuthorityRole is the TOUCH_SERVICE_ROLES value for the eco billing
// authority service (模式同 ops_grant_manager)。
const billingAuthorityRole = "billing_authority"

// requireBillingAuthority gates the authority seam. No service identity = 401
// (merchant sessions can never self-authorize billing facts).
func (s *Server) requireBillingAuthority(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceID := strings.TrimSpace(r.Header.Get(serviceIDHeader))
		if serviceID == "" {
			fail(w, http.StatusUnauthorized, "unauthorized", "billing facts are written through the trusted authority seam only")
			return
		}
		if len(s.Cfg.ServiceTokens) == 0 {
			s.auditServiceProducer("unavailable", serviceID, "registry_not_configured")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "service_producer_unavailable",
				"message": "trusted service producer registry is not configured; refusing to act (fail-closed)",
			})
			return
		}
		want, registered := s.Cfg.ServiceTokens[serviceID]
		got := strings.TrimSpace(r.Header.Get(serviceTokenHeader))
		if !registered || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			s.auditServiceProducer("rejected", serviceID, "bad_service_identity")
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong service identity")
			return
		}
		if s.Cfg.ServiceProducerRole(serviceID) != billingAuthorityRole {
			s.auditServiceProducer("rejected", serviceID, "role_not_billing_authority")
			fail(w, http.StatusForbidden, "forbidden", "service identity lacks the billing_authority role")
			return
		}
		s.auditServiceProducer("accepted", serviceID, "")
		ctx := context.WithValue(r.Context(), serviceProducerKey, &serviceProducer{ServiceID: serviceID})
		next(w, r.WithContext(ctx))
	})
}

// registerBillingAuthorityRoutes wires the trusted authority seam (no FEATURE
// flag: the 503 fail-closed comes from the unconfigured token registry).
func (s *Server) registerBillingAuthorityRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/billing/authority/catalog", s.requireBillingAuthority(s.handleBillingAuthorityCatalog))
	mux.Handle("POST /api/v1/billing/authority/events", s.requireBillingAuthority(s.handleBillingAuthorityEvent))
}

// authorityProducer re-checks the verified service identity inside the handler.
func (s *Server) authorityProducer(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := serviceProducerFrom(r.Context())
	if p == nil || p.ServiceID == "" {
		fail(w, http.StatusUnauthorized, "unauthorized", "billing facts are written through the trusted authority seam only")
		return "", false
	}
	if s.Cfg.ServiceProducerRole(p.ServiceID) != billingAuthorityRole {
		fail(w, http.StatusForbidden, "forbidden", "service identity lacks the billing_authority role")
		return "", false
	}
	return p.ServiceID, true
}

// ---- POST /api/v1/billing/authority/catalog ---------------------------------------

func (s *Server) handleBillingAuthorityCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorityProducer(w, r); !ok {
		return
	}
	var body struct {
		Entries []billing.CatalogEntry `json:"entries"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "catalog body is not valid JSON")
		return
	}
	if len(body.Entries) == 0 {
		fail(w, http.StatusBadRequest, "entries_required", "catalog upsert needs at least one entry")
		return
	}
	at := nowRFC3339()
	items := make([]map[string]any, 0, len(body.Entries))
	okCount := 0
	for _, e := range body.Entries {
		// 租户专属行必须引用真实租户(''=全局默认)。
		if e.ScopeTenant != "" {
			if _, err := s.St.GetTenant(e.ScopeTenant); err != nil {
				items = append(items, map[string]any{"price_ref": e.PriceRef, "ok": false, "error": "bad_tenant"})
				continue
			}
		}
		saved, err := s.St.UpsertBillingCatalogEntry(e, at)
		if err != nil {
			items = append(items, map[string]any{"price_ref": e.PriceRef, "ok": false, "error": "bad_entry"})
			continue
		}
		okCount++
		items = append(items, map[string]any{"price_ref": saved.PriceRef, "plan_code": saved.PlanCode,
			"pricing_version": saved.PricingVersion, "ok": true})
	}
	status := http.StatusCreated
	if okCount == 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, map[string]any{"items": items})
}

// ---- POST /api/v1/billing/authority/events -----------------------------------------

type authorityEventBody struct {
	TenantID string `json:"tenant_id"`
	OrderID  string `json:"order_id"`
	// CheckoutRef（touch-order-checkout/v1.1 §0/§2）：producer 外发事实的
	// 顶层主路由键（Touch 本地 ck_* 回声）——payment_paid 按它路由映射行。
	CheckoutRef string               `json:"checkout_ref"`
	Source      string               `json:"source"`
	EventID     string               `json:"event_id"`
	Type        string               `json:"type"`
	OccurredAt  string               `json:"occurred_at"`
	Payload     billing.EventPayload `json:"payload"`
	// refund_registered 专用字段(F19:原订单=order_id;金额/原因/开票主体/审核人)。
	RefundRef     string `json:"refund_ref"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Reason        string `json:"reason"`
	InvoiceTitle  string `json:"invoice_title"`
	InvoiceStatus string `json:"invoice_status"`
	InvoiceRef    string `json:"invoice_ref"`
	Auditor       string `json:"auditor"`
	// ai_usage_recorded 专用字段(F13 投影:quote/charge 引用)。
	QuoteRef       string `json:"quote_ref"`
	Capability     string `json:"capability"`
	Quantity       int64  `json:"quantity"`
	PricingVersion string `json:"ai_pricing_version"`
	BusinessRef    string `json:"business_ref"`
	// hardware_step 专用字段(F14:操作者/时间/设备标识)。
	Step      string `json:"step"`
	Operator  string `json:"operator"`
	DeviceRef string `json:"device_ref"`
}

func (s *Server) handleBillingAuthorityEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorityProducer(w, r); !ok {
		return
	}
	var body authorityEventBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "event body is not valid JSON")
		return
	}
	tenantID := strings.TrimSpace(body.TenantID)
	if tenantID == "" {
		fail(w, http.StatusBadRequest, "tenant_required", "tenant_id is required")
		return
	}
	if _, err := s.St.GetTenant(tenantID); err != nil {
		fail(w, http.StatusBadRequest, "bad_tenant", "tenant_id must reference an existing tenant")
		return
	}
	source := strings.TrimSpace(body.Source)
	eventID := strings.TrimSpace(body.EventID)
	if source == "" || eventID == "" {
		fail(w, http.StatusBadRequest, "event_identity_required", "source and event_id are required (dedup key)")
		return
	}
	occurredAt := strings.TrimSpace(body.OccurredAt)
	if occurredAt == "" {
		occurredAt = nowRFC3339()
	}

	switch body.Type {
	case billing.EventOrderRegistered, billing.EventPaymentPaid, billing.EventActivationOpening,
		billing.EventActivationActive, billing.EventActivationFailed, billing.EventOrderCancelled:
		orderID := strings.TrimSpace(body.OrderID)
		// touch-order-checkout/v1.1：路由优先 checkout_ref（映射行）。
		if orderID == "" {
			orderID = strings.TrimSpace(body.CheckoutRef)
		}
		if orderID == "" {
			fail(w, http.StatusBadRequest, "order_required", "order_id is required for order events")
			return
		}
		// v1.1 alphabet 裁定：order_registered 停发（mint 零事实外发）。
		// 到达即异常——老 producer 误发/伪造，大声失败不静默吞。
		if body.Type == billing.EventOrderRegistered {
			fail(w, http.StatusConflict, "event_type_retired",
				"order_registered 已从 v1.1 事件表停发（mint 零事实外发）：Touch 续付端点自持 pending 行，该事件无消费者")
			return
		}
		// payment_paid 的 checkout_ref 路由：映射行存在 → 金额裁决（gating）
		// → 映射推进 paid → orderID 纠正为原单 id；映射行不存在 → 向后兼容
		// 既有无收银单路径（order_id 直查）。
		if body.Type == billing.EventPaymentPaid {
			routeRef := strings.TrimSpace(body.CheckoutRef)
			if routeRef == "" {
				routeRef = orderID
			}
			if refRow, err := s.St.GetBillingCheckoutRefByRef(tenantID, routeRef); err == nil && refRow.OrderID != "" {
				// AdjudicatePaymentFact（v1.1 §2 消费动作）：producer 承诺
				// 金额/币种=冻结承认值；不符即资金异常大声失败（409），
				// 绝不冒充干净重放 ack 掩埋。
				if body.Payload.AmountMinor != 0 && body.Payload.AmountMinor != refRow.AmountMinor ||
					body.Payload.Currency != "" && body.Payload.Currency != refRow.Currency {
					fail(w, http.StatusConflict, "event_mismatch",
						"payment_paid 金额/币种与原单冻结承认值不符（资金异常，需人工对账）")
					return
				}
				if perr := s.St.AdvanceBillingCheckoutRefPaid(tenantID, refRow.OrderID, nowRFC3339()); perr != nil &&
					perr != store.ErrCheckoutRefMapState {
					writeAuthorityError(w, perr)
					return
				}
				// orderID 纠正为映射的原单 id（producer 外发的 order_id 是
				// ck_* 回声，原单状态机按真实 bo_* id 推进）。
				orderID = refRow.OrderID
			}
		}
		// activation_opening（v1.1 FYI 语义）：producer 侧降级为不 gate 信号
		//（Touch 履约自主）。带 checkout_ref 的 opening 不推进原单状态机
		//（状态推进已由 payment_paid 完成），记审计后如实 200。
		if body.Type == billing.EventActivationOpening && strings.TrimSpace(body.CheckoutRef) != "" {
			writeJSON(w, http.StatusOK, map[string]any{"replayed": false, "fyi": true,
				"note": "activation_opening 是 FYI 信号（v1.1）：状态推进已由 payment_paid 完成"})
			return
		}
		row, replayed, err := s.St.ApplyBillingOrderEvent(tenantID, orderID, billing.AuthorityEventInput{
			Source: source, EventID: eventID, Type: body.Type, OccurredAt: occurredAt, Payload: body.Payload,
		}, nowRFC3339())
		if err != nil {
			writeAuthorityError(w, err)
			return
		}
		status := http.StatusOK
		if !replayed {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"replayed": replayed, "order": row})

	case "refund_registered":
		refund := billing.RefundInput{
			OrderID: body.OrderID, RefundRef: strings.TrimSpace(body.RefundRef),
			AmountMinor: body.AmountMinor, Currency: body.Currency,
			Reason: strings.TrimSpace(body.Reason), InvoiceTitle: strings.TrimSpace(body.InvoiceTitle),
			InvoiceStatus: strings.TrimSpace(body.InvoiceStatus), InvoiceRef: strings.TrimSpace(body.InvoiceRef),
			Auditor: strings.TrimSpace(body.Auditor), Source: source, EventID: eventID, OccurredAt: occurredAt,
		}
		row, replayed, err := s.St.ApplyBillingRefund(tenantID, body.OrderID, refund, nowRFC3339())
		if err != nil {
			writeAuthorityError(w, err)
			return
		}
		status := http.StatusOK
		if !replayed {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"replayed": replayed, "refund": row})

	case "ai_usage_recorded":
		if strings.TrimSpace(body.QuoteRef) == "" {
			fail(w, http.StatusBadRequest, "quote_ref_required", "quote_ref is required for ai usage facts")
			return
		}
		row, replayed, err := s.St.RecordBillingAIUsage(store.NewBillingAIUsage{
			TenantID: tenantID, QuoteRef: strings.TrimSpace(body.QuoteRef),
			Capability: strings.TrimSpace(body.Capability), Quantity: body.Quantity,
			AmountMinor: body.AmountMinor, Currency: body.Currency,
			PricingVersion: body.PricingVersion, BusinessRef: body.BusinessRef, OccurredAt: occurredAt,
		}, nowRFC3339())
		if err != nil {
			writeAuthorityError(w, err)
			return
		}
		status := http.StatusOK
		if !replayed {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"replayed": replayed, "usage": row})

	case "hardware_step":
		orderID := strings.TrimSpace(body.OrderID)
		if orderID == "" {
			fail(w, http.StatusBadRequest, "order_required", "order_id is required for hardware steps")
			return
		}
		f, err := s.St.ApplyHardwareStep(tenantID, orderID, billing.HardwareStepInput{
			Step: body.Step, Operator: strings.TrimSpace(body.Operator),
			OccurredAt: occurredAt, DeviceRef: strings.TrimSpace(body.DeviceRef),
		}, nowRFC3339())
		if err != nil {
			writeAuthorityError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"replayed": false, "fulfillment": f})

	default:
		fail(w, http.StatusBadRequest, "bad_event_type", "unknown authority event type")
	}
}

// writeAuthorityError maps domain errors onto honest HTTP codes.
func writeAuthorityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrBillingIdempotencyConflict):
		fail(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "same dedup key with a different payload")
	case errors.Is(err, billing.ErrInvalidTransition):
		fail(w, http.StatusConflict, "invalid_transition", "this event does not fit the order's current state")
	case errors.Is(err, billing.ErrEventMismatch):
		fail(w, http.StatusConflict, "event_mismatch", "event payload contradicts the stored order facts")
	case errors.Is(err, billing.ErrRefundExceedsOrder):
		fail(w, http.StatusConflict, "refund_exceeds_order", "refund amount exceeds the order's remaining un-refunded balance")
	case errors.Is(err, billing.ErrRefundBadInput):
		fail(w, http.StatusBadRequest, "bad_request", "refund fact is missing required fields (reason/auditor/amount)")
	case errors.Is(err, billing.ErrStepSkipped):
		fail(w, http.StatusConflict, "hardware_step_skipped", "fulfillment steps cannot skip their predecessor")
	case errors.Is(err, billing.ErrStepConflict):
		fail(w, http.StatusConflict, "hardware_step_conflict", "step fact conflicts with the recorded delivery facts")
	case errors.Is(err, store.ErrBillingQuoteNotConsumable):
		fail(w, http.StatusConflict, "quote_not_consumable", "only confirmed quotes can be consumed")
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "referenced record not found in this tenant scope")
	default:
		fail(w, http.StatusInternalServerError, "internal", "authority event failed")
	}
}
