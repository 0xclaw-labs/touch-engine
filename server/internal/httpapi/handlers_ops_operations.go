// handlers_ops_operations.go: HUI-4592 统一操作信封(POST /api/v1/ops/operations)
// 与权威结果追踪(GET /api/v1/ops/operations/{id})。
//
// 纪律(票面):
//   - 白名单 action 冻结在 ops 包;每个动作分派到各域已有的唯一写面
//     (2053 lifecycle / 4591 目录 / 3535 能力行 / 3553 内容下架 / 4593 分发),
//     本票不新造第二写面;O4 不能关闭无关导航(白名单外动作结构上不可分派);
//   - 全部写操作重查权限、revision 与幂等:同 idempotency_key 同负载=幂等
//     读回(不重复执行);同键异负载=409 IDEMPOTENCY_CONFLICT;stale
//     revision=409;缺原因=400;
//   - 审计三件套硬性:操作前后值/操作者(服务端解析)/发生时间/原因;操作
//     结果按 operation_id 可追踪到权威结果;失败保留可恢复操作记录,
//     不通过手工改库假装成功。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/ops"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// opsOperationRequest is the unified envelope body plus the per-action extras.
type opsOperationRequest struct {
	Action           string `json:"action"`
	ResourceRef      string `json:"resource_ref"`
	TenantID         string `json:"tenant_id"`
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
	IdempotencyKey   string `json:"idempotency_key"`

	// ticket extras
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Severity    string `json:"severity"`
	Detail      string `json:"detail"`
	OwnerRef    string `json:"owner_ref"`
	Resolution  string `json:"resolution"`
	CloseReason string `json:"close_reason"`

	// content.retract extras
	PackID string `json:"pack_id"`

	// drill.record extras
	DrillKind          string `json:"drill_kind"`
	DrillTarget        string `json:"drill_target"`
	DrillResult        string `json:"drill_result"`
	VerificationReport string `json:"verification_report"`
	Notes              string `json:"notes"`
}

// canonicalRequest is the idempotency payload: the semantic fields only
// (同键不同负载判等的依据)。
func (in opsOperationRequest) canonicalRequest() string {
	return jsonSnap(map[string]any{
		"action": in.Action, "resource_ref": in.ResourceRef, "tenant_id": in.TenantID,
		"expected_revision": in.ExpectedRevision, "reason": in.Reason,
		"title": in.Title, "kind": in.Kind, "severity": in.Severity, "detail": in.Detail,
		"owner_ref": in.OwnerRef, "resolution": in.Resolution, "close_reason": in.CloseReason,
		"pack_id": in.PackID, "drill_kind": in.DrillKind, "drill_target": in.DrillTarget,
		"drill_result": in.DrillResult, "verification_report": in.VerificationReport, "notes": in.Notes,
	})
}

func (in opsOperationRequest) runningRow(actorRef, actorRole, authority string) store.OpsOperation {
	return store.OpsOperation{
		IdempotencyKey: in.IdempotencyKey, Action: in.Action, ResourceRef: in.ResourceRef,
		TenantID: in.TenantID, ExpectedRevision: in.ExpectedRevision, Reason: in.Reason,
		ActorRef: actorRef, ActorRole: actorRole, AuthoritySource: authority,
		RequestJSON: in.canonicalRequest(),
	}
}

// handleOpsOperationCreate receives one envelope, enforces envelope/permission/
// idempotency/revision discipline and dispatches to the owning domain write face.
func (s *Server) handleOpsOperationCreate(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	var in opsOperationRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	in.Action = strings.TrimSpace(in.Action)
	spec, known := ops.LookupAction(in.Action)
	if !known {
		fail(w, http.StatusUnprocessableEntity, "action_not_allowed",
			"action is not in the frozen ops whitelist")
		return
	}
	c := callerFrom(r)
	access := s.resolveAccess(c)
	// 每个动作在分派前重查权限;命令不在受信 grant 内 → 403。
	if !authz.Can(access, spec.Command, "").Allowed {
		fail(w, http.StatusForbidden, "forbidden", "this action requires the trusted ops grant "+spec.Command)
		return
	}
	if err := ops.ValidateEnvelope(in.Action, strings.TrimSpace(in.Reason),
		strings.TrimSpace(in.IdempotencyKey), strings.TrimSpace(in.ExpectedRevision)); err != nil {
		opsFail(w, err)
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)

	// 幂等:同键存在 → 同负载读回(不重执行);异负载拒绝。
	if prev, err := s.St.GetOpsOperationByIdempotencyKey(in.IdempotencyKey); err == nil {
		if prev.RequestJSON == in.canonicalRequest() && prev.Action == in.Action {
			writeJSON(w, http.StatusOK, map[string]any{
				"operation": prev, "replayed": true,
				"note": "retry returns the recorded operation; it is not executed again",
			})
			return
		}
		fail(w, http.StatusConflict, "idempotency_conflict",
			"the idempotency key was used with a different payload")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "operation lookup failed")
		return
	}

	authority := s.opsAuthoritySource(c, in.TenantID)
	recorded, err := s.St.RecordOpsOperation(in.runningRow(c.Member.PrincipalRef, c.Member.Role, authority))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "operation record failed")
		return
	}
	// 分派前语义预检(客户端错误在落操作记录前拒绝;执行失败才落 failed 记录)。
	if err := validateDispatchTarget(in); err != nil {
		_, _ = s.St.FinishOpsOperation(recorded.ID, "rejected", "invalid_target", jsonSnap(map[string]any{"error": err.Error()}))
		opsFail(w, err)
		return
	}
	result, dispatchErr := s.dispatchOpsOperation(c, in, authority, recorded.ID)
	op := s.finishOperation(recorded.ID, dispatchErr, result)
	writeJSON(w, http.StatusOK, map[string]any{"operation": op})
}

// finishOperation records the terminal state and always returns the row.
func (s *Server) finishOperation(id string, dispatchErr error, result any) store.OpsOperation {
	if dispatchErr != nil {
		code, msg := opsErrorFields(dispatchErr)
		op, err := s.St.FinishOpsOperation(id, "failed", code, jsonSnap(map[string]any{"error": msg}))
		if err != nil {
			return store.OpsOperation{ID: id, State: "failed", ErrorCode: code}
		}
		return op
	}
	op, err := s.St.FinishOpsOperation(id, "succeeded", "", jsonSnap(result))
	if err != nil {
		return store.OpsOperation{ID: id, State: "succeeded"}
	}
	return op
}

// dispatchOpsOperation routes one validated envelope to the owning domain
// write face. 返回 (result, err):err 非 nil 时操作记录为 failed(可重试,
// 幂等键不换)。
func (s *Server) dispatchOpsOperation(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	switch in.Action {
	case ops.ActionTenantSuspend, ops.ActionTenantResume:
		lifecycle := "suspended"
		if in.Action == ops.ActionTenantResume {
			lifecycle = "active"
		}
		return s.opsTenantLifecycle(c, in, authority, lifecycle, operationID)
	case ops.ActionChargeHoldSet, ops.ActionChargeHoldClear:
		return s.opsChargeHold(c, in, authority, in.Action == ops.ActionChargeHoldSet, operationID)
	case ops.ActionPlanPublish, ops.ActionPlanRetire:
		return s.opsPlanWindow(c, in, authority, operationID)
	case ops.ActionOrderRecheck:
		return s.opsOrderRecheck(c, in, authority, operationID)
	case ops.ActionChannelDisable, ops.ActionChannelRestore:
		return s.opsChannelEmergency(c, in, authority, operationID)
	case ops.ActionContentRetract:
		return s.opsContentRetract(c, in, authority, operationID)
	case ops.ActionAssignmentCancel:
		return s.opsAssignmentCancel(c, in, authority, operationID)
	case ops.ActionTicketOpen:
		return s.opsTicketOpen(c, in, authority, operationID)
	case ops.ActionTicketConfirm, ops.ActionTicketResolve, ops.ActionTicketClose:
		return s.opsTicketTransition(c, in, authority, operationID)
	case ops.ActionDrillRecord:
		return s.opsDrillRecord(c, in, authority, operationID)
	}
	return nil, ops.ErrActionUnknown
}

// opsTenantLifecycle: 2053 lifecycle 语义 + 单语句 revision 守卫。
func (s *Server) opsTenantLifecycle(c *caller, in opsOperationRequest, authority, lifecycle, operationID string) (any, error) {
	before, err := s.St.GetTenant(in.ResourceRef)
	if err != nil {
		return nil, store.ErrNotFound
	}
	rev, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	if err != nil {
		return nil, errRevisionFormat
	}
	newRev, err := s.St.SetTenantLifecycleOps(in.ResourceRef, lifecycle, rev)
	if err != nil {
		return nil, err
	}
	after, _ := s.St.GetTenant(in.ResourceRef)
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdTenantsManage, Action: in.Action, TargetType: "tenant",
		TargetID: in.ResourceRef, TenantID: in.ResourceRef,
		BeforeJSON: jsonSnap(map[string]any{"lifecycle": before.Lifecycle}),
		AfterJSON:  jsonSnap(map[string]any{"lifecycle": after.Lifecycle, "ops_revision": newRev}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return map[string]any{"tenant_id": in.ResourceRef, "lifecycle": after.Lifecycle, "ops_revision": newRev}, nil
}

// opsChargeHold: 2053 charge-hold 语义(收费保持不是商家开关)。
func (s *Server) opsChargeHold(c *caller, in opsOperationRequest, authority string, hold bool, operationID string) (any, error) {
	before, err := s.St.GetTenant(in.ResourceRef)
	if err != nil {
		return nil, store.ErrNotFound
	}
	rev, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	if err != nil {
		return nil, errRevisionFormat
	}
	newRev, err := s.St.SetChargeHoldOps(in.ResourceRef, hold, rev)
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdTenantsManage, Action: in.Action, TargetType: "tenant",
		TargetID: in.ResourceRef, TenantID: in.ResourceRef,
		BeforeJSON: jsonSnap(map[string]any{"charge_hold": before.ChargeHold}),
		AfterJSON:  jsonSnap(map[string]any{"charge_hold": hold, "ops_revision": newRev}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return map[string]any{"tenant_id": in.ResourceRef, "charge_hold": hold, "ops_revision": newRev}, nil
}

// opsPlanWindow: 只调 4591 目录唯一写入函数(UpsertBillingCatalogEntry),
// 只改生效窗口——金额/条款不动,零价格常量;历史订单钉自己的快照。
func (s *Server) opsPlanWindow(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	entry, err := s.St.GetCatalogEntryByID(in.ResourceRef)
	if err != nil {
		return nil, store.ErrNotFound
	}
	nowS := time.Now().UTC().Format(time.RFC3339)
	before := map[string]any{"effective_from": entry.EffectiveFrom, "effective_to": entry.EffectiveTo}
	if in.Action == ops.ActionPlanPublish {
		entry.EffectiveFrom = nowS
		entry.EffectiveTo = ""
	} else {
		entry.EffectiveTo = nowS
	}
	saved, err := s.St.UpsertBillingCatalogEntry(entry, nowS)
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdPlansManage, Action: in.Action, TargetType: "catalog_entry",
		TargetID: in.ResourceRef, TenantID: entry.ScopeTenant,
		BeforeJSON: jsonSnap(before),
		AfterJSON:  jsonSnap(map[string]any{"effective_from": saved.EffectiveFrom, "effective_to": saved.EffectiveTo}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return map[string]any{"entry": saved, "order_price_snapshot_immutable": true}, nil
}

// opsOrderRecheck: 只读重查(订单+退款+硬件履约投影),快照落结果——
// 不直接修改账单金额/权益投影,付款不等于硬件交付。
func (s *Server) opsOrderRecheck(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	if strings.TrimSpace(in.TenantID) == "" {
		return nil, errTenantRequired
	}
	order, err := s.St.GetBillingOrder(in.TenantID, in.ResourceRef)
	if err != nil {
		return nil, store.ErrNotFound
	}
	refunds, _ := s.St.ListBillingRefunds(in.TenantID)
	mine := []store.BillingRefundRow{}
	for _, rf := range refunds {
		if rf.OrderID == order.ID {
			mine = append(mine, rf)
		}
	}
	snapshot := map[string]any{
		"order": order, "refunds": mine, "rechecked_at": time.Now().UTC().Format(time.RFC3339),
	}
	if order.Kind == "hardware" {
		if ful, err := s.St.GetHardwareFulfillment(in.TenantID, order.ID); err == nil {
			snapshot["fulfillment"] = ful
		} else {
			snapshot["fulfillment"] = map[string]any{"state": "unavailable"}
		}
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdOrdersManage, Action: in.Action, TargetType: "billing_order",
		TargetID: order.ID, TenantID: in.TenantID,
		BeforeJSON: "null",
		AfterJSON:  jsonSnap(map[string]any{"status": order.Status, "refund_count": len(mine)}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return snapshot, nil
}

// opsChannelEmergency: 3535 唯一写面(SetActionCapabilityEmergency),
// 只停对应方法行;actor 以 ops-console 标识平台运营(3535 审计行照落);
// FEATURE_CHANNEL_ADMISSION off = 写面不存在,诚实拒绝。
func (s *Server) opsChannelEmergency(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	if !s.Cfg.FeatureChannelAdmission {
		return nil, errChannelAdmissionOff
	}
	disable := in.Action == ops.ActionChannelDisable
	changed, err := s.St.SetActionCapabilityEmergency(in.ResourceRef, disable,
		store.ControlAudit{Actor: "ops-console", Reason: in.Reason,
			Action: emergencyAuditAction(disable)})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdChannelsManage, Action: in.Action, TargetType: "action_capability",
		TargetID: in.ResourceRef, TenantID: in.TenantID,
		BeforeJSON: jsonSnap(map[string]any{"emergency_disabled": !disable}),
		AfterJSON:  jsonSnap(map[string]any{"emergency_disabled": disable}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return map[string]any{"capability_id": in.ResourceRef, "emergency_disabled": disable, "changed": changed}, nil
}

func emergencyAuditAction(disable bool) string {
	if disable {
		return "emergency_disable"
	}
	return "emergency_enable"
}

// opsContentRetract: 3553 风险下架唯一写面(BlockStoreContentPackVersion),
// blocked_reason = 运营原因;撤回不覆盖历史证据(版本行仍在,状态可回查)。
func (s *Server) opsContentRetract(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	if strings.TrimSpace(in.TenantID) == "" || strings.TrimSpace(in.PackID) == "" {
		return nil, errTenantRequired
	}
	v, err := s.St.BlockStoreContentPackVersion(in.TenantID, in.PackID, in.ResourceRef,
		in.Reason, "ops-console", time.Now())
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdTemplatesManage, Action: in.Action, TargetType: "content_version",
		TargetID: v.ID, TenantID: in.TenantID,
		BeforeJSON: "null",
		AfterJSON:  jsonSnap(map[string]any{"status": v.Status, "blocked_reason": in.Reason}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return map[string]any{"version": v}, nil
}

// opsAssignmentCancel: 4593 分发撤回唯一写面(UpdateTemplateAssignmentStatus
// → cancelled,总部撤回语义;终态不可再改)。
func (s *Server) opsAssignmentCancel(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	if strings.TrimSpace(in.TenantID) == "" {
		return nil, errTenantRequired
	}
	t, err := s.St.UpdateTemplateAssignmentStatus(in.ResourceRef, in.TenantID, "cancelled", "", "ops-console")
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdTemplatesManage, Action: in.Action, TargetType: "template_assignment",
		TargetID: t.ID, TenantID: in.TenantID,
		BeforeJSON: jsonSnap(map[string]any{"status": "distributed"}),
		AfterJSON:  jsonSnap(map[string]any{"status": t.Status}),
		Reason:     in.Reason, OperationID: operationID,
	})
	return map[string]any{"assignment": t}, nil
}

func (s *Server) opsTicketOpen(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, errTitleRequired
	}
	t, err := s.St.CreateOpsTicket(in.TenantID, in.Kind, in.Severity, strings.TrimSpace(in.Title),
		in.Detail, in.OwnerRef, "manual", c.Member.PrincipalRef, time.Now())
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdOpsManage, Action: in.Action, TargetType: "ops_ticket",
		TargetID: t.ID, TenantID: t.TenantID, BeforeJSON: "null",
		AfterJSON: jsonSnap(map[string]any{"status": t.Status, "title": t.Title}),
		Reason:    in.Reason, OperationID: operationID,
	})
	return map[string]any{"ticket": t}, nil
}

func (s *Server) opsTicketTransition(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	to := ""
	switch in.Action {
	case ops.ActionTicketConfirm:
		to = ops.TicketConfirmed
	case ops.ActionTicketResolve:
		to = ops.TicketResolved
	case ops.ActionTicketClose:
		to = ops.TicketClosed
	}
	before, err := s.St.GetOpsTicket(in.ResourceRef)
	if err != nil {
		return nil, err
	}
	t, err := s.St.TransitionOpsTicket(in.ResourceRef, to, in.Resolution, in.CloseReason,
		c.Member.PrincipalRef, time.Now())
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdOpsManage, Action: in.Action, TargetType: "ops_ticket",
		TargetID: t.ID, TenantID: t.TenantID,
		BeforeJSON: jsonSnap(map[string]any{"status": before.Status}),
		AfterJSON: jsonSnap(map[string]any{"status": t.Status, "resolution": t.Resolution,
			"closed_reason": t.ClosedReason}),
		Reason: in.Reason, OperationID: operationID,
	})
	return map[string]any{"ticket": t}, nil
}

func (s *Server) opsDrillRecord(c *caller, in opsOperationRequest, authority, operationID string) (any, error) {
	d, err := s.St.CreateOpsDrill(in.DrillKind, in.DrillTarget, in.DrillResult,
		in.VerificationReport, c.Member.PrincipalRef, in.Notes, time.Now())
	if err != nil {
		return nil, err
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdOpsManage, Action: in.Action, TargetType: "ops_drill",
		TargetID: d.ID, TenantID: "", BeforeJSON: "null",
		AfterJSON: jsonSnap(map[string]any{"kind": d.Kind, "result": d.Result,
			"has_verification_report": d.VerificationReport != ""}),
		Reason: in.Reason, OperationID: operationID,
	})
	return map[string]any{"drill": d}, nil
}

// handleOpsOperationGet tracks one operation's authoritative result by id.
func (s *Server) handleOpsOperationGet(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	op, err := s.St.GetOpsOperation(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "operation not found")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "operation lookup failed")
		return
	}
	audit, _ := s.St.ListOpsAudit(store.OpsAuditFilter{Operation: op.ID, Limit: 20})
	writeJSON(w, http.StatusOK, map[string]any{"operation": op, "audit": audit})
}

// validateDispatchTarget pre-checks per-action target requirements BEFORE the
// operation row is recorded (client errors answer 400; execution failures stay
// as recorded failed operations for retry).
func validateDispatchTarget(in opsOperationRequest) error {
	switch in.Action {
	case ops.ActionOrderRecheck, ops.ActionContentRetract, ops.ActionAssignmentCancel:
		if strings.TrimSpace(in.TenantID) == "" {
			return errTenantRequired
		}
	case ops.ActionTicketOpen:
		if strings.TrimSpace(in.Title) == "" {
			return errTitleRequired
		}
	case ops.ActionDrillRecord:
		if err := ops.ValidateDrill(in.DrillKind, in.DrillResult, in.VerificationReport); err != nil {
			return err
		}
	}
	return nil
}

// ---- 错误分诊 ---------------------------------------------------------------------

var (
	errRevisionFormat      = errors.New("revision_format")
	errTenantRequired      = errors.New("tenant_required")
	errTitleRequired       = errors.New("title_required")
	errChannelAdmissionOff = errors.New("channel_admission_off")
)

// opsFail maps domain errors onto honest HTTP codes.
func opsFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ops.ErrActionUnknown):
		fail(w, http.StatusUnprocessableEntity, "action_not_allowed", "action is not in the frozen ops whitelist")
	case errors.Is(err, ops.ErrReasonRequired):
		fail(w, http.StatusBadRequest, "reason_required", "every ops operation must state its reason")
	case errors.Is(err, ops.ErrIdempotencyKeyRequired):
		fail(w, http.StatusBadRequest, "idempotency_key_required", "retry semantics need an idempotency key")
	case errors.Is(err, ops.ErrRevisionRequired):
		fail(w, http.StatusBadRequest, "revision_required", "this resource guards on expected_revision")
	case errors.Is(err, ops.ErrTicketTransition):
		fail(w, http.StatusConflict, "ticket_transition_not_allowed", "ticket state machine refuses this transition")
	case errors.Is(err, ops.ErrTicketCloseReason):
		fail(w, http.StatusBadRequest, "ticket_close_reason_required", "closing a ticket must state why")
	case errors.Is(err, store.ErrOpsRevisionConflict):
		fail(w, http.StatusConflict, "stale_version", "expected_revision does not match; reload and retry")
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "target resource not found")
	default:
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

func opsErrorFields(err error) (string, string) {
	switch {
	case errors.Is(err, ops.ErrActionUnknown):
		return "action_not_allowed", "action is not whitelisted"
	case errors.Is(err, ops.ErrTicketTransition):
		return "ticket_transition_not_allowed", "illegal ticket transition"
	case errors.Is(err, ops.ErrTicketCloseReason):
		return "ticket_close_reason_required", "close reason missing"
	case errors.Is(err, store.ErrOpsRevisionConflict):
		return "stale_version", "expected_revision stale"
	case errors.Is(err, store.ErrNotFound):
		return "not_found", "target missing"
	case errors.Is(err, errRevisionFormat):
		return "revision_format", "expected_revision must be an integer"
	case errors.Is(err, errTenantRequired):
		return "tenant_required", "operation needs the owning tenant_id"
	case errors.Is(err, errTitleRequired):
		return "title_required", "ticket needs a title"
	case errors.Is(err, errChannelAdmissionOff):
		return "feature_off", "channel admission feature is off"
	case errors.Is(err, store.ErrBillingIdempotencyConflict):
		return "idempotency_conflict", "conflicting record"
	default:
		return "operation_failed", err.Error()
	}
}
