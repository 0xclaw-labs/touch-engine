package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/brandhost"
	"github.com/bianjiefilm/touch-engine/server/internal/redact"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// ---- auth handlers (identity is the only path; no stub mode) ----------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Email == "" || in.Password == "" {
		fail(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}
	if s.Cfg.FeatureBrand {
		host, herr := brandhost.FromRequest(r)
		if herr != nil {
			fail(w, http.StatusBadRequest, "invalid_host", "public host is not a brand identity")
			return
		}
		res, rerr := s.Brand.Read(r.Context(), host, false)
		if !brandAdmitsLogin(w, res, rerr) {
			return
		}
	}
	pair, err := s.ID.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		// Never distinguish user-facing reasons beyond rejection; never log tokens.
		fail(w, http.StatusUnauthorized, "login_rejected", "login rejected by platform identity")
		return
	}
	s.setSessionCookies(w, pair)
	// Login does not create a tenant membership and does not select a payer.
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

// brandAdmitsLogin maps registry kinds to distinct login failures.
// A registry outage is 503, not a fake rejection, and no cookie is set.
func brandAdmitsLogin(w http.ResponseWriter, res brandctx.Result, err error) bool {
	if err != nil || res.Kind == brandctx.KindUnavailable || res.Kind == brandctx.KindInvalid {
		fail(w, http.StatusServiceUnavailable, "brand_unavailable", "brand registry could not be read; no session was created")
		return false
	}
	switch res.Kind {
	case brandctx.KindReady:
		if res.Manifest.AdmitLogin {
			return true
		}
		fail(w, http.StatusForbidden, "brand_not_admitted", "brand is not admitting login; no session was created")
	case brandctx.KindSuspended:
		fail(w, http.StatusForbidden, "brand_suspended", "brand is suspended; no session was created")
	case brandctx.KindRetired:
		fail(w, http.StatusForbidden, "brand_retiring", "brand is retiring; no session was created")
	case brandctx.KindUnknown:
		fail(w, http.StatusNotFound, "unknown_brand", "host is not a registered brand; no session was created")
	case brandctx.KindDomain:
		fail(w, http.StatusForbidden, "domain_error", "brand domain is not admitted; no session was created")
	default:
		fail(w, http.StatusServiceUnavailable, "brand_unavailable", "brand registry could not be read; no session was created")
	}
	return false
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	refresh := ""
	if ck, err := r.Cookie(s.refreshCookieName()); err == nil {
		refresh = ck.Value
	}
	if refresh == "" {
		fail(w, http.StatusUnauthorized, "unauthenticated", "no refresh token")
		return
	}
	pair, err := s.ID.Refresh(r.Context(), refresh)
	if err != nil {
		fail(w, http.StatusUnauthorized, "refresh_rejected", "refresh rejected by platform identity")
		return
	}
	s.setSessionCookies(w, pair)
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	refresh := ""
	if ck, err := r.Cookie(s.refreshCookieName()); err == nil {
		refresh = ck.Value
	}
	// Best-effort server-side revocation; local cookies are always cleared.
	if refresh != "" {
		userID := ""
		if c := callerFrom(r); c != nil {
			userID = c.Principal.ID
		}
		_ = s.ID.Logout(r.Context(), refresh, userID)
	}
	s.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "principal is not a member of this tenant")
		return
	}
	body := map[string]any{
		"principal_ref": c.Principal.ID,
		"email":         redact.MaskEmail(c.Principal.Email),
		"tenant_id":     c.Member.TenantID,
		"role":          c.Member.Role,
		"store_scope":   c.Member.StoreScope, // HUI-1674: "" = 总部/全门店;非空 = 仅该门店
		"enabled":       c.Member.Enabled,
		// HUI-2222: feature 开关下发 web 的单缝。恒存在,web 靠布尔值区分
		// off 与缺省;service_desk(/work/service 页面本体归 HUI-3562)与
		// authz(HUI-4593 /work/settings 成员与权限页)按开关下发。
		"features": map[string]any{
			"service_desk": s.Cfg.FeatureServiceDesk,
			"authz":        s.Cfg.FeatureAuthz,
		},
	}
	if s.Cfg.FeatureBrand {
		t, err := s.St.GetTenant(c.Member.TenantID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
			return
		}
		body["tenant_name"] = t.Name
		body["lifecycle"] = t.Lifecycle
		body["charge_hold"] = t.ChargeHold
		if g := c.Brand; g != nil {
			body["brand"] = s.brandShellJSON(g)
			body["workbar"] = g.Result.Manifest.DisplayName + " / " + t.Name
			// Only touch itself. Foreign registry apps are not recommended.
			if g.TouchOn {
				body["capabilities"] = []string{s.Cfg.AppID}
			} else {
				body["capabilities"] = []string{}
			}
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// ---- member admin (org_owner only) ----------------------------------------------
//
// HUI-1674 角色模型:
//   - org_owner  总部/全门店(存量 owner 的语义等价物;输入仍接受 "owner" 别名)
//   - store_manager  门店经理,必须带 store_scope(本租户内真实门店)
//   - staff  租户级职员(T0 语义)
//   - 非 store_manager 成员不得携带 store_scope;store_manager 不得清空作用域。

// parseMemberRole maps the input role to its canonical stored value.
// Returns "" with ok=false when the input is not a known role.
//
// FEAT-0176: "agent" IS a canonical stored role, but it is never issued
// through the admin members surface: agent memberships are created/re-enabled
// only by the agency establish flow (代管关系行 + 留痕一体化,单一创建路径)。
// This surface therefore refuses it (owner 可经既有 PATCH 把 agent 行改回
// 其他角色,但不经 agency 面铸造 agent 行)。
func parseMemberRole(in string) (string, bool) {
	if authz.Role(strings.TrimSpace(in)) == authz.RoleAgent {
		return "", false
	}
	canonical, ok := authz.CanonicalRole(authz.Role(in))
	if !ok {
		return "", false
	}
	return string(canonical), true
}

// validateMemberScope enforces the role↔scope invariant against the tenant's
// stores. Returns the validated scope ("" for non-manager roles).
//
// HUI-4593 门店店员 scope 显式建模:staff 行也允许显式携带 store_scope
// (新语义 = 仅本店;authz 侧业务动作按本店收窄)。存量 staff 行(scope="")
// 保持 T0 租户级语义,绝不静默等同「仅本店」。org_owner 仍永不携带 scope。
func (s *Server) validateMemberScope(c *caller, role, storeScope string, w http.ResponseWriter) (string, bool) {
	storeScope = strings.TrimSpace(storeScope)
	if role == "store_manager" || role == "staff" {
		if storeScope == "" {
			if role == "store_manager" {
				fail(w, http.StatusBadRequest, "bad_scope", "store_manager requires store_scope (a store of this tenant)")
				return "", false
			}
			return "", true // legacy staff: tenant-level semantics
		}
		if _, err := s.St.GetStore(storeScope, c.Member.TenantID); err != nil {
			fail(w, http.StatusBadRequest, "bad_scope", "store_scope must reference an existing store of this tenant")
			return "", false
		}
		return storeScope, true
	}
	if storeScope != "" {
		fail(w, http.StatusBadRequest, "bad_scope", "store_scope is only allowed for store_manager or staff")
		return "", false
	}
	return "", true
}

// appendMemberAudit writes the authorization-domain audit row for a member
// write (操作者/时间/前后值/原因). Best-effort persistence of the trail must
// never mask the business result: audit errors are logged, not surfaced.
func (s *Server) appendMemberAudit(c *caller, action, targetID, reason string, before, after map[string]any) {
	_, err := s.St.AppendAuthzAudit(store.AuthzAuditEntry{
		TenantID: c.Member.TenantID, ActorPrincipal: c.Member.PrincipalRef,
		ActorMemberID: c.Member.ID, ActorRole: c.Member.Role,
		Action: action, TargetType: "member", TargetID: targetID,
		BeforeJSON: memberSnapshotJSON(before), AfterJSON: memberSnapshotJSON(after),
		Reason: reason,
	})
	if err != nil && s.Log != nil {
		s.Log.Printf("authz_audit_write_failed action=%s target=%s err=%v", action, targetID, err)
	}
}

func memberSnapshotJSON(m map[string]any) string {
	if m == nil {
		return "null" // 修复轮 2(F3):新建无前值,显式 null 语义
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func memberSnapshot(m store.Member) map[string]any {
	return map[string]any{"role": m.Role, "store_scope": m.StoreScope, "enabled": m.Enabled, "display_name": m.DisplayName}
}

func (s *Server) handleMemberList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	members, err := s.St.ListMembers(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "list members failed")
		return
	}
	// 修复轮 2(F2):回填店员动作级授权现行态(列表一次读取,页面免逐个查询)。
	grants := s.St.ListTenantPermissionGrants(c.Member.TenantID)
	items := make([]map[string]any, 0, len(members))
	for _, m := range members {
		item := map[string]any{
			"id": m.ID, "tenant_id": m.TenantID, "principal_ref": m.PrincipalRef,
			"role": m.Role, "store_scope": m.StoreScope, "enabled": m.Enabled,
			"display_name": m.DisplayName, "created_by": m.CreatedBy,
			"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
		}
		if perms := grants[m.ID]; len(perms) > 0 {
			item["granted_permissions"] = perms
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleMemberCreate attaches an ALREADY EXISTING platform principal to this
// tenant. 身份纪律:principal_ref must be an identity-derived usr_* id; emails
// and phone numbers are refused here (they never create principals).
func (s *Server) handleMemberCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	var in struct {
		PrincipalRef string `json:"principal_ref"`
		Role         string `json:"role"`
		DisplayName  string `json:"display_name"`
		StoreScope   string `json:"store_scope"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	ref := strings.TrimSpace(in.PrincipalRef)
	if !strings.HasPrefix(ref, "usr_") || strings.ContainsAny(ref, "@ :/+") {
		fail(w, http.StatusBadRequest, "bad_principal_ref", "principal_ref must be an identity principal id (usr_*); emails/phones never create members")
		return
	}
	role, ok := parseMemberRole(in.Role)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_role", "role must be org_owner (or legacy owner), store_manager or staff")
		return
	}
	scope, ok := s.validateMemberScope(c, role, in.StoreScope, w)
	if !ok {
		return
	}
	m, err := s.St.CreateMemberScoped(c.Member.TenantID, ref, role, in.DisplayName, c.Member.PrincipalRef, true, scope)
	if err != nil {
		fail(w, http.StatusConflict, "member_exists", "principal already a member of this tenant (or storage error)")
		return
	}
	// HUI-4593: 授权留痕(操作者/时间/前值空对象/后值/原因)。
	s.appendMemberAudit(c, "member_create", m.ID, strings.TrimSpace(in.Reason), nil, memberSnapshot(m))
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleMemberPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	var in struct {
		Role        *string `json:"role"`
		Enabled     *bool   `json:"enabled"`
		DisplayName *string `json:"display_name"`
		StoreScope  *string `json:"store_scope"`
		Reason      string  `json:"reason"`
		// PrincipalRef is accepted only to be refused: it is immutable.
		PrincipalRef *string `json:"principal_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.PrincipalRef != nil {
		fail(w, http.StatusBadRequest, "principal_immutable", "principal_ref is immutable; it can only come from identity")
		return
	}
	m, err := s.St.GetMember(r.PathValue("id"))
	if err != nil || m.TenantID != c.Member.TenantID {
		// member must belong to the caller's tenant (掩码:他租户成员不可见)
		fail(w, http.StatusNotFound, "not_found", "member not found in this tenant scope")
		return
	}

	// resolve the effective target role (current role when not being changed)
	role := m.Role
	if in.Role != nil {
		canonical, ok := parseMemberRole(*in.Role)
		if !ok {
			fail(w, http.StatusBadRequest, "bad_role", "role must be org_owner (or legacy owner), store_manager or staff")
			return
		}
		role = canonical
	}
	// resolve the effective target scope: explicit pointer wins, otherwise the
	// current scope (and demotion away from store_manager clears it).
	scope := m.StoreScope
	if in.StoreScope != nil {
		scope = strings.TrimSpace(*in.StoreScope)
	} else if role != "store_manager" {
		scope = ""
	}
	// role changes wholesale: re-validate the role↔scope invariant whenever the
	// pair is touched.
	if in.Role != nil || in.StoreScope != nil {
		validated, ok := s.validateMemberScope(c, role, scope, w)
		if !ok {
			return
		}
		scope = validated
	}

	updated, err := s.St.UpdateMemberScoped(r.PathValue("id"), strPtrOrNil(role, in.Role != nil), in.Enabled, in.DisplayName, &scope)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "member not found in this tenant scope")
		return
	}
	// HUI-4593: 授权变更留痕(动作按事实命名:停用/角色调整都落 member_update,
	// 前后值快照可查;agent 停用走代理撤销面,另有 agency_revoke 审计行)。
	action := "member_update"
	if in.Enabled != nil && !*in.Enabled {
		action = "member_disable"
	}
	s.appendMemberAudit(c, action, m.ID, strings.TrimSpace(in.Reason), memberSnapshot(m), memberSnapshot(updated))
	writeJSON(w, http.StatusOK, updated)
}

// strPtrOrNil returns a pointer when set is true (role is canonical already).
func strPtrOrNil(v string, set bool) *string {
	if !set {
		return nil
	}
	return &v
}
