package httpapi

// HUI-1663:实体 NFC 交付管理面(/api/v1/touchpoint-deliveries)。
//
// 纪律:
//   - 鉴权与触点面同一矩阵:requireTagFace(org_owner 全租户/store_manager 本店)
//     + 记录级 store scope(越店/跨租户 404 掩码,同 touchpointScoped);
//   - 操作者与门店从受信权限解析:operator_ref = 服务端会话主体(客户端声明的
//     操作者不采信);交付 store 从触点派生(客户端输入永不作为绑定来源);
//   - 期望回读 URL 由服务端构造(PUBLIC_BASE_URL;未配置退化为路径形式并如实
//     记录),客户端只提供回读抄录内容;
//   - 幂等:event_id 必填(客户端幂等键);同键同负载 replayed 零推进;
//     状态推进 = 201,事实记录不推进(回读不一致)= 200 + readback_matches=false;
//   - 真机/硬件事实只有现场录入;本面绝不代签「已验证」(跳步/回读不一致/
//     stale 全部如实暴露)。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/delivery"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// deliveryFail maps domain errors onto honest HTTP codes.
func deliveryFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "delivery or referenced record not found")
	case errors.Is(err, delivery.ErrUnknownStep):
		fail(w, http.StatusBadRequest, "bad_step", "event_type is not a delivery step")
	case errors.Is(err, delivery.ErrStepSkipped):
		fail(w, http.StatusConflict, "step_skipped", "delivery steps cannot skip their predecessor")
	case errors.Is(err, delivery.ErrDeliveryClosed):
		fail(w, http.StatusConflict, "delivery_closed", "this delivery is delivered or superseded and refuses new facts")
	case errors.Is(err, store.ErrDeliveryIdempotencyConflict):
		fail(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "same event key with a different payload")
	case errors.Is(err, store.ErrDeliveryBadInput), errors.Is(err, store.ErrReplacementBadInput):
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, store.ErrPaymentRequired):
		fail(w, http.StatusConflict, "payment_required", "the hardware order has no payment fact yet (4591 projection)")
	case errors.Is(err, store.ErrStoreMismatch):
		fail(w, http.StatusConflict, "store_mismatch", "the touchpoint store does not match the delivery store")
	case errors.Is(err, store.ErrTouchpointDisabled):
		fail(w, http.StatusConflict, "touchpoint_disabled", "the touchpoint is disabled")
	case errors.Is(err, store.ErrLiveDeliveryExists):
		fail(w, http.StatusConflict, "delivery_exists", "this touchpoint already has a live delivery")
	default:
		fail(w, http.StatusInternalServerError, "internal", "delivery operation failed")
	}
}

// deliveryScoped fetches the delivery view in the caller's tenant and enforces
// the member's store scope (missing and out-of-scope both 404).
func (s *Server) deliveryScoped(c *caller, id string, w http.ResponseWriter) (store.TouchpointDeliveryView, bool) {
	v, err := s.St.GetTouchpointDeliveryView(c.Member.TenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "delivery not found")
		return store.TouchpointDeliveryView{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "delivery lookup failed")
		return store.TouchpointDeliveryView{}, false
	}
	if !s.requireScopedAction(c, authz.ActionManageTags, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: v.StoreID}, true, w) {
		return store.TouchpointDeliveryView{}, false
	}
	return v, true
}

// ---- POST /api/v1/touchpoint-deliveries -------------------------------------------

func (s *Server) handleTouchpointDeliveryCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	var in struct {
		TouchpointID    string `json:"touchpoint_id"`
		HardwareOrderID string `json:"hardware_order_id"`
		DeliveryRef     string `json:"delivery_ref"`
		PhysicalUnitRef string `json:"physical_unit_ref"`
		TagBatch        string `json:"tag_batch"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		fail(w, http.StatusBadRequest, "bad_request", "delivery body is not valid JSON")
		return
	}
	if strings.TrimSpace(in.TouchpointID) == "" || strings.TrimSpace(in.HardwareOrderID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "touchpoint_id and hardware_order_id are required")
		return
	}
	// 门店经理只能为本店触点建交付(服务端强制;触点不可见即 404 掩码)。
	tp, err := s.St.GetTouchpointView(in.TouchpointID, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "touchpoint not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
		return
	}
	if !s.requireScopedAction(c, authz.ActionManageTags, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: tp.StoreID}, true, w) {
		return
	}
	d, err := s.St.CreateTouchpointDelivery(store.NewTouchpointDelivery{
		TenantID: c.Member.TenantID, TouchpointID: in.TouchpointID,
		HardwareOrderID: strings.TrimSpace(in.HardwareOrderID),
		DeliveryRef:     strings.TrimSpace(in.DeliveryRef),
		PhysicalUnitRef: strings.TrimSpace(in.PhysicalUnitRef),
		TagBatch:        strings.TrimSpace(in.TagBatch),
		CreatedBy:       c.Member.PrincipalRef,
	})
	if err != nil {
		deliveryFail(w, err)
		return
	}
	view, err := s.St.GetTouchpointDeliveryView(c.Member.TenantID, d.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "delivery lookup failed")
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// ---- GET list / detail -------------------------------------------------------------

func (s *Server) handleTouchpointDeliveryList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	f := store.TouchpointDeliveryFilter{
		StoreID:      strings.TrimSpace(r.URL.Query().Get("store_id")),
		TouchpointID: strings.TrimSpace(r.URL.Query().Get("touchpoint_id")),
	}
	// 服务端强制作用域——客户端传来的 store_id 永不放宽经理视野。
	if scope != "" {
		f.StoreID = scope
	}
	items, err := s.St.ListTouchpointDeliveryViews(c.Member.TenantID, f)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "delivery list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleTouchpointDeliveryGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.deliveryScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	events, err := s.St.ListTouchpointDeliveryEvents(c.Member.TenantID, v.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "delivery events lookup failed")
		return
	}
	replacements, err := s.St.ListTouchpointDeliveryReplacements(c.Member.TenantID, v.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "replacement audit lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": v.ID, "tenant_id": v.TenantID, "store_id": v.StoreID,
		"touchpoint_id": v.TouchpointID, "hardware_order_id": v.HardwareOrderID,
		"delivery_ref": v.DeliveryRef, "physical_unit_ref": v.PhysicalUnitRef,
		"tag_batch": v.TagBatch, "status": v.Status,
		"bound_binding_revision":    v.BoundBindingRevision,
		"verified_binding_revision": v.VerifiedBindingRevision,
		"replacement_of":            v.ReplacementOf,
		"created_by":                v.CreatedBy, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt,
		"order_ref": v.OrderRef, "payment": v.Payment, "steps": v.Steps,
		"current_binding_revision": v.CurrentBindingRevision,
		"binding_stale":            v.BindingStale,
		"last_readback":            v.LastReadback, "last_readback_matches": v.LastReadbackMatches,
		"events": events, "replacements": replacements,
	})
}

// ---- POST /api/v1/touchpoint-deliveries/{id}/events ---------------------------------

func (s *Server) handleTouchpointDeliveryEvent(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.deliveryScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	var in struct {
		EventType   string `json:"event_type"`
		EventID     string `json:"event_id"`
		DeviceRef   string `json:"device_ref"`
		EvidenceRef string `json:"evidence_ref"`
		OccurredAt  string `json:"occurred_at"`
		ReadURL     string `json:"read_url"`
		Note        string `json:"note"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		fail(w, http.StatusBadRequest, "bad_request", "event body is not valid JSON")
		return
	}
	if strings.TrimSpace(in.EventID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "event_id is required (client idempotency key)")
		return
	}
	// 期望回读 URL 服务端构造:配置了 PUBLIC_BASE_URL 用绝对 URL;否则退化为
	// 路径形式 /t/{code}(判等按 delivery.CheckReadback 的 host/码段闸)。
	expectedURL := ""
	if in.EventType == delivery.StepReadback {
		tp, err := s.St.GetTouchpointAlias(v.TouchpointID, c.Member.TenantID)
		if err != nil {
			deliveryFail(w, err)
			return
		}
		if u := s.touchpointURL(tp.Code); u != "" {
			expectedURL = u
		} else {
			expectedURL = "/t/" + tp.Code
		}
	}
	res, err := s.St.ApplyTouchpointDeliveryEvent(store.DeliveryEventInput{
		TenantID: c.Member.TenantID, DeliveryID: v.ID,
		EventType: strings.TrimSpace(in.EventType), EventID: strings.TrimSpace(in.EventID),
		OperatorRef: c.Member.PrincipalRef, // 服务端解析;客户端声明的操作者不采信
		DeviceRef:   strings.TrimSpace(in.DeviceRef), EvidenceRef: strings.TrimSpace(in.EvidenceRef),
		OccurredAt: strings.TrimSpace(in.OccurredAt),
		ReadURL:    in.ReadURL, ExpectedURL: expectedURL, Note: in.Note,
	}, nowRFC3339())
	if err != nil {
		deliveryFail(w, err)
		return
	}
	view, err := s.St.GetTouchpointDeliveryView(c.Member.TenantID, v.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "delivery lookup failed")
		return
	}
	status := http.StatusOK
	if res.Advanced {
		status = http.StatusCreated // 状态推进 = 新的步事实落位
	}
	writeJSON(w, status, map[string]any{
		"delivery": view, "replayed": res.Replayed, "advanced": res.Advanced,
		"readback_matches": res.ReadbackMatches, "readback_reason": res.ReadbackReason,
	})
}

// ---- POST /api/v1/touchpoint-deliveries/{id}/replace ---------------------------------

func (s *Server) handleTouchpointDeliveryReplace(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.deliveryScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	var in struct {
		ReasonCategory  string `json:"reason_category"`
		ReasonDetail    string `json:"reason_detail"`
		NewTouchpointID string `json:"new_touchpoint_id"`
		HardwareOrderID string `json:"hardware_order_id"`
		PhysicalUnitRef string `json:"physical_unit_ref"`
		TagBatch        string `json:"tag_batch"`
		DeliveryRef     string `json:"delivery_ref"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		fail(w, http.StatusBadRequest, "bad_request", "replace body is not valid JSON")
		return
	}
	// 经理视野对新触点同样收口(lost 分支换店/越店 404 掩码)。
	if nt := strings.TrimSpace(in.NewTouchpointID); nt != "" && nt != v.TouchpointID {
		tp, err := s.St.GetTouchpointView(nt, c.Member.TenantID)
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "touchpoint not found")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
			return
		}
		if !s.requireScopedAction(c, authz.ActionManageTags, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: tp.StoreID}, true, w) {
			return
		}
	}
	rep, err := s.St.ReplaceTouchpointDelivery(store.ReplaceDeliveryInput{
		TenantID: c.Member.TenantID, OldDeliveryID: v.ID,
		NewTouchpointID: strings.TrimSpace(in.NewTouchpointID),
		HardwareOrderID: strings.TrimSpace(in.HardwareOrderID),
		PhysicalUnitRef: strings.TrimSpace(in.PhysicalUnitRef),
		TagBatch:        strings.TrimSpace(in.TagBatch),
		DeliveryRef:     strings.TrimSpace(in.DeliveryRef),
		ReasonCategory:  strings.TrimSpace(in.ReasonCategory),
		ReasonDetail:    strings.TrimSpace(in.ReasonDetail),
		CreatedBy:       c.Member.PrincipalRef,
	})
	if err != nil {
		deliveryFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"new_delivery": rep.NewDelivery, "audit": rep.Audit})
}
