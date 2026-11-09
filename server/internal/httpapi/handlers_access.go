// handlers_access.go: HUI-4593 B11 公共权限契约 HTTP 面。
//
// 纪律:
//   - 开关:FEATURE_AUTHZ(默认 off)= 路由不注册(见 server.go 条件注册)且本
//     gate 再答统一 404 —— 与 FEATURE_AGENCY 的 off 双保险同一写法;
//   - scope 全部从可信 session + 服务端行解析:X-Tenant-ID 只选工作区,路径/
//     参数/体里的任何租户/门店/角色声明永不参与授权;
//   - GET /api/v1/access/effective:本票供给 2222/4592 及其他票的稳定裁决读面,
//     底层是 authz.ResolveAccess 纯函数(四维度冻结矩阵);
//   - 模板分发只记录分发/授权结果(不写 storefront 版本表,应用委托 1676);
//     多店逐项报告,部分失败绝不标全部成功;幂等重放不重复创建;
//   - ops grant 只经受信服务生产者缝(X-Service-ID + 独立 token + ops_grant_manager
//     角色);商家会话打到本面一律 401 —— 平台运营授权永不自授;
//   - 授权审计读面 org_owner 专属,他租户行不可见。
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// authzGate answers uniform 404 when FEATURE_AUTHZ is off (agencyGate 同纪律).
func (s *Server) authzGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureAuthz {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// resolveAccess builds the authz.EffectiveAccess verdict input from trusted
// server-side rows: member row (+agency validity backfill) → active grant
// snapshot → live ops grants. Errors fail closed (empty grant list).
func (s *Server) resolveAccess(c *caller) authz.EffectiveAccess {
	am := s.authzMember(c)
	if am == nil {
		return authz.EffectiveAccess{}
	}
	var grant *authz.AgencyGrant
	if am.Role == authz.RoleAgent && am.AgencyActive {
		if rel, err := s.St.GetActiveAgencyRelation(am.TenantID, am.PrincipalRef); err == nil {
			g := authz.AgencyGrant{
				RelationRef:    rel.ID,
				AllowedActions: rel.AllowedActions,
				StoreScopeSet:  rel.StoreScopeSet,
				GrantRevision:  rel.GrantRevision,
				PayerGrantRef:  rel.PayerGrantRef,
			}
			if rel.ValidUntil != "" {
				if exp, perr := time.Parse(time.RFC3339, rel.ValidUntil); perr == nil {
					g.ExpiresAt = exp
				}
			}
			grant = &g
		}
	}
	nowT := time.Now()
	opsRows, err := s.St.ListValidOpsGrants(am.PrincipalRef, am.TenantID, nowT)
	if err != nil {
		opsRows = nil // fail closed:无 grant 可用
	}
	views := make([]authz.OpsGrantView, 0, len(opsRows))
	for _, g := range opsRows {
		v := authz.OpsGrantView{Ref: g.ID, PrincipalRef: g.PrincipalRef, TenantID: g.TenantID,
			StoreScopeSet: g.StoreScopeSet, Commands: g.Commands}
		if g.ExpiresAt != "" {
			if exp, perr := time.Parse(time.RFC3339, g.ExpiresAt); perr == nil {
				v.ExpiresAt = exp
			}
		}
		views = append(views, v)
	}
	return authz.ResolveAccess(am, grant, views, nowT)
}

// ---- GET /access/effective -----------------------------------------------------

func (s *Server) handleAccessEffective(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	c := callerFrom(r)
	am := s.authzMember(c)
	if am == nil || !am.Enabled {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not an enabled member of this tenant")
		return
	}
	a := s.resolveAccess(c)
	out := map[string]any{
		"principal_ref":     a.PrincipalRef,
		"tenant_id":         a.TenantID,
		"role":              string(a.Role),
		"allowed_store_ids": a.AllowedStoreIDs, // 空 = 全门店
		"permissions":       a.Permissions,
		"ops_permissions":   opsPermissionsJSON(a),
		"ops_grant_refs":    a.OpsGrantRefs,
	}
	if a.RelationRef != "" {
		out["relation"] = map[string]any{
			"ref":             a.RelationRef,
			"grant_revision":  a.GrantRevision,
			"expires_at":      authzTimeJSON(a.ExpiresAt),
			"payer_granted":   containsString(a.Permissions, string(authz.PermBillingManagePayer)),
			"allowed_actions": relationAllowedActions(a),
		}
	} else {
		out["relation"] = nil
	}
	writeJSON(w, http.StatusOK, out)
}

func opsPermissionsJSON(a authz.EffectiveAccess) []map[string]any {
	out := make([]map[string]any, 0, len(a.OpsPermissions))
	for _, op := range a.OpsPermissions {
		out = append(out, map[string]any{"command": op.Command, "store_scope_set": op.StoreScopeSet})
	}
	return out
}

func authzTimeJSON(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// relationAllowedActions reverse-maps held permissions onto the grant action
// whitelist (for display; the authoritative list lives on the relation row).
func relationAllowedActions(a authz.EffectiveAccess) []string {
	out := make([]string, 0)
	holds := func(p authz.Permission) bool { return containsString(a.Permissions, string(p)) }
	if holds(authz.PermStorefrontConfigure) {
		out = append(out, authz.AgencyActionStorefront)
	}
	if holds(authz.PermActionConfigure) {
		out = append(out, authz.AgencyActionAction)
	}
	if holds(authz.PermActionChannelTarget) {
		out = append(out, authz.AgencyActionChannelTarget)
	}
	if holds(authz.PermBillingView) {
		out = append(out, authz.AgencyActionRenewal)
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ---- GET /access/audit(org_owner 专属回查)---------------------------------------

func (s *Server) handleAccessAudit(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	rows, err := s.St.ListAuthzAudit(c.Member.TenantID,
		strings.TrimSpace(r.URL.Query().Get("target_type")),
		strings.TrimSpace(r.URL.Query().Get("target_id")), 100)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "audit list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

// ---- template-assignments(连锁模板分发引用)---------------------------------------

type templateAssignmentBody struct {
	TemplateRef           string   `json:"template_ref"`
	SourceVersion         string   `json:"source_version"`
	StoreIDs              []string `json:"store_ids"`
	AllowedOverrideFields []string `json:"allowed_override_fields"`
	RequestKey            string   `json:"request_key"`
}

// templateReservedOverrideFields 冻结:门店目标/账号/二维码/Wi-Fi 密码是门店
// 自有目标事实,模板下发永不触达(跨店盲目复制的通道在此关死)。
var templateReservedOverrideFields = map[string]bool{
	"store_target": true, "account": true, "qr_code": true, "wifi_password": true,
}

func (s *Server) handleTemplateAssignmentCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	c := callerFrom(r)
	// 门店级分发 = storefront:configure 且覆盖租户级("")→ 组织负责人专属
	// (manager 的本店权限不够跨店,agent 无 grant 一律不可)。
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermStorefrontConfigure), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "template distribution is an org-level action")
		return
	}
	var in templateAssignmentBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if strings.TrimSpace(in.TemplateRef) == "" || strings.TrimSpace(in.SourceVersion) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "template_ref and source_version are required")
		return
	}
	if len(in.StoreIDs) == 0 {
		fail(w, http.StatusBadRequest, "bad_request", "store_ids is required")
	}
	for _, f := range in.AllowedOverrideFields {
		if templateReservedOverrideFields[f] {
			fail(w, http.StatusBadRequest, "reserved_field", "store targets/accounts/QR/Wi-Fi secrets never cross stores via templates")
			return
		}
	}

	items := make([]map[string]any, 0, len(in.StoreIDs))
	okCount, replayedCount := 0, 0
	for _, storeID := range in.StoreIDs {
		ta, replayed, err := s.St.CreateTemplateAssignment(store.NewTemplateAssignment{
			TenantID: c.Member.TenantID, StoreID: storeID,
			TemplateRef: in.TemplateRef, SourceVersion: in.SourceVersion,
			AllowedOverrideFields: in.AllowedOverrideFields,
			RequestKey:            in.RequestKey, CreatedBy: c.Member.PrincipalRef,
		})
		switch {
		case err == nil:
			okCount++
			if replayed {
				replayedCount++
			}
			items = append(items, map[string]any{"store_id": storeID, "ok": true, "replayed": replayed, "id": ta.ID, "status": ta.Status})
		case errors.Is(err, store.ErrNotFound):
			items = append(items, map[string]any{"store_id": storeID, "ok": false, "error": "store_not_found"})
		case errors.Is(err, store.ErrTemplateReservedField):
			items = append(items, map[string]any{"store_id": storeID, "ok": false, "error": "reserved_field"})
		default:
			items = append(items, map[string]any{"store_id": storeID, "ok": false, "error": "internal"})
		}
	}
	// 审计(仅新授权事实落痕;重放行不重复记审计,失败不落授权事实)
	for _, it := range items {
		if it["ok"] != true || it["replayed"] == true {
			continue
		}
		_, _ = s.St.AppendAuthzAudit(store.AuthzAuditEntry{
			TenantID: c.Member.TenantID, ActorPrincipal: c.Member.PrincipalRef,
			ActorRole: c.Member.Role,
			Action:    "template_assign", TargetType: "template_assignment", TargetID: it["id"].(string),
			AfterJSON: marshalSnapshot(map[string]any{"template_ref": in.TemplateRef, "source_version": in.SourceVersion, "store_id": it["store_id"]}),
			Reason:    in.RequestKey,
		})
	}

	// 全部成功且全部命中既有行 = 幂等重放(零新增)→ 200 + replayed;
	// 有任何失败行 → 207 逐项报告(部分失败绝不标全部成功,重试亦然)。
	failCount := len(items) - okCount
	if failCount == 0 && okCount > 0 && okCount == replayedCount {
		writeJSON(w, http.StatusOK, map[string]any{"replayed": true, "items": items})
		return
	}
	status := http.StatusCreated
	if failCount > 0 || okCount == 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, map[string]any{"items": items})
}

func marshalSnapshot(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Server) handleTemplateAssignmentList(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	c := callerFrom(r)
	// 查询按既有业务角色:owner/staff(租户级)过;store_manager 收窄到本店
	// (过滤器是服务端作用域事实,query 参数永不放大);agent 需激活关系。
	rec := authz.RecordScope{TenantID: c.Member.TenantID}
	am := s.authzMember(c)
	if am != nil && am.Role == authz.RoleStoreManager {
		rec.StoreID = am.StoreScope
	}
	if !s.requireAction(c, authz.ActionReadList, rec, w) {
		return
	}
	storeFilter := strings.TrimSpace(r.URL.Query().Get("store_id"))
	if am != nil && am.Role == authz.RoleStoreManager {
		storeFilter = am.StoreScope // 本店事实覆盖客户端参数
	}
	rows, err := s.St.ListTemplateAssignments(c.Member.TenantID, storeFilter)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "template assignment list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) handleTemplateAssignmentGet(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	c := callerFrom(r)
	am := s.authzMember(c)
	rec := authz.RecordScope{TenantID: c.Member.TenantID}
	if am != nil && am.Role == authz.RoleStoreManager {
		rec.StoreID = am.StoreScope
	}
	if !s.requireAction(c, authz.ActionReadRecord, rec, w) {
		return
	}
	ta, err := s.St.GetTemplateAssignment(r.PathValue("id"), c.Member.TenantID)
	if err != nil {
		// 他租户/不存在同答 404(掩码)
		fail(w, http.StatusNotFound, "not_found", "template assignment not found in this tenant scope")
		return
	}
	if am != nil && am.Role == authz.RoleStoreManager && ta.StoreID != am.StoreScope {
		fail(w, http.StatusNotFound, "not_found", "template assignment not found in this tenant scope")
		return
	}
	writeJSON(w, http.StatusOK, ta)
}

func (s *Server) handleTemplateAssignmentPatch(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	c := callerFrom(r)
	a := s.resolveAccess(c)
	if d := authz.Can(a, string(authz.PermStorefrontConfigure), ""); !d.Allowed {
		fail(w, http.StatusForbidden, d.Reason, "template distribution is an org-level action")
		return
	}
	var in struct {
		Status          string `json:"status"`
		AppliedRevision string `json:"applied_revision"`
		Reason          string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "a reason is required for template assignment changes")
		return
	}
	if in.Status != "cancelled" && in.Status != "applied" {
		fail(w, http.StatusBadRequest, "bad_status", "status must be cancelled or applied")
		return
	}
	ta, err := s.St.UpdateTemplateAssignmentStatus(r.PathValue("id"), c.Member.TenantID, in.Status, in.AppliedRevision, c.Member.PrincipalRef)
	if err != nil {
		if errors.Is(err, store.ErrTemplateStatus) {
			fail(w, http.StatusConflict, "template_status", "this status transition is not allowed")
			return
		}
		fail(w, http.StatusNotFound, "not_found", "template assignment not found in this tenant scope")
		return
	}
	_, _ = s.St.AppendAuthzAudit(store.AuthzAuditEntry{
		TenantID: c.Member.TenantID, ActorPrincipal: c.Member.PrincipalRef,
		ActorRole: c.Member.Role,
		Action:    "template_assignment_update", TargetType: "template_assignment", TargetID: ta.ID,
		BeforeJSON: marshalSnapshot(map[string]any{"status": "distributed"}),
		AfterJSON:  marshalSnapshot(map[string]any{"status": ta.Status, "applied_revision": ta.AppliedRevision}),
		Reason:     in.Reason,
	})
	writeJSON(w, http.StatusOK, ta)
}

// ---- ops-grants(受信服务生产者专属;商家面永不自授)--------------------------------

const opsGrantManagerRole = "ops_grant_manager"

// requireOpsGrantProducer is the ops-grant twin of requireServiceProducer: the
// same per-service token seam (constant-time compare, fail-closed registry)
// but the ops_grant_manager role instead of queue_producer. Requests without a
// service identity skip the session path on purpose (no merchant surface here)
// and fall through to the handler's uniform 401 — 商家会话永不自授 ops grant。
func (s *Server) requireOpsGrantProducer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceID := strings.TrimSpace(r.Header.Get(serviceIDHeader))
		if serviceID == "" {
			next(w, r)
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
		if s.Cfg.ServiceProducerRole(serviceID) != opsGrantManagerRole {
			s.auditServiceProducer("rejected", serviceID, "role_not_ops_grant_manager")
			fail(w, http.StatusForbidden, "forbidden", "service identity lacks the ops_grant_manager role")
			return
		}
		s.auditServiceProducer("accepted", serviceID, "")
		ctx := context.WithValue(r.Context(), serviceProducerKey, &serviceProducer{ServiceID: serviceID})
		next(w, r.WithContext(ctx))
	})
}

// opsGrantProducer re-checks the verified service identity inside the handler.
func (s *Server) opsGrantProducer(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := serviceProducerFrom(r.Context())
	if p == nil || p.ServiceID == "" {
		fail(w, http.StatusUnauthorized, "unauthorized", "ops grants are issued through the trusted service seam only")
		return "", false
	}
	if s.Cfg.ServiceProducerRole(p.ServiceID) != opsGrantManagerRole {
		fail(w, http.StatusForbidden, "forbidden", "service identity lacks the ops_grant_manager role")
		return "", false
	}
	return p.ServiceID, true
}

func (s *Server) handleOpsGrantCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	serviceID, ok := s.opsGrantProducer(w, r)
	if !ok {
		return
	}
	var in struct {
		TenantID      string   `json:"tenant_id"`
		PrincipalRef  string   `json:"principal_ref"`
		StoreScopeSet []string `json:"store_scope_set"`
		Commands      []string `json:"commands"`
		ExpiresAt     string   `json:"expires_at"`
		Reason        string   `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if _, terr := s.St.GetTenant(in.TenantID); terr != nil {
		fail(w, http.StatusBadRequest, "bad_tenant", "tenant_id must reference an existing tenant")
		return
	}
	g, err := s.St.CreateOpsGrant(store.NewOpsGrant{
		TenantID: in.TenantID, PrincipalRef: in.PrincipalRef,
		StoreScopeSet: in.StoreScopeSet, Commands: in.Commands,
		IssuedByService: serviceID, ExpiresAt: in.ExpiresAt,
	})
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_grant", err.Error())
		return
	}
	_, _ = s.St.AppendAuthzAudit(store.AuthzAuditEntry{
		TenantID: g.TenantID, ActorPrincipal: serviceID, ActorRole: opsGrantManagerRole,
		Action: "ops_grant_issue", TargetType: "ops_grant", TargetID: g.ID,
		AfterJSON: marshalSnapshot(map[string]any{"principal_ref": g.PrincipalRef, "commands": g.Commands, "expires_at": g.ExpiresAt}),
		Reason:    in.Reason,
	})
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleOpsGrantList(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	serviceID, ok := s.opsGrantProducer(w, r)
	if !ok {
		return
	}
	rows, err := s.St.ListOpsGrantsByIssuer(serviceID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ops grant list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) handleOpsGrantRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.authzGate(w) {
		return
	}
	serviceID, ok := s.opsGrantProducer(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	g, err := s.St.GetOpsGrant(id)
	if err != nil || g.IssuedByService != serviceID {
		// 他服务签发的 grant = 不可见(掩码语义)
		fail(w, http.StatusNotFound, "not_found", "ops grant not found for this issuer")
		return
	}
	rev, err := s.St.RevokeOpsGrant(id, serviceID, time.Now())
	if err != nil {
		if errors.Is(err, store.ErrOpsGrantAlreadyRevoked) {
			fail(w, http.StatusConflict, "ops_grant_already_revoked", "this ops grant is already revoked")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "ops grant revoke failed")
		return
	}
	_, _ = s.St.AppendAuthzAudit(store.AuthzAuditEntry{
		TenantID: rev.TenantID, ActorPrincipal: serviceID, ActorRole: opsGrantManagerRole,
		Action: "ops_grant_revoke", TargetType: "ops_grant", TargetID: rev.ID,
		Reason: "revoked by issuing service",
	})
	writeJSON(w, http.StatusOK, rev)
}
