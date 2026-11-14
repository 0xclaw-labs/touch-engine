# 公共身份既有合同预检（只读，2026-11-14）

从 0xclaw-labs/public-ai/main 读取，不是生产部署可用性证明。

## internal/identity/http.go

Source: https://github.com/0xclaw-labs/public-ai/blob/main/internal/identity/http.go
Blob SHA: 75427b8c58cedad62715ce383f0fbf7db8011771

```go
		case r.URL.Path == "/internal/v1/identity/revocations":
			h.serveRevocations(w, r)
		case r.URL.Path == "/internal/v1/identity/api-keys/import":
			h.serveAPIKeyImport(w, r)
		case r.URL.Path == "/internal/v1/identity/api-keys":
			h.serveAPIKeys(w, r)
		case r.URL.Path == "/internal/v1/identity/api-keys/revoke":
			h.serveAPIKeyRevoke(w, r)
		case r.URL.Path == "/internal/v1/identity/api-keys/verify":
			h.serveAPIKeyVerify(w, r)
		case r.URL.Path == "/internal/v1/identity/users/exists":
			h.serveUserExists(w, r)
		case r.URL.Path == "/internal/v1/identity/users":
			h.serveUsers(w, r)
		case r.URL.Path == "/internal/v1/identity/session/resolve":
			h.serveSessionResolve(w, r)
		case r.URL.Path == "/internal/v1/identity/password/change":
```
```go

// serveUserExists is a read-only existence/status probe for product BFFs.
// It deliberately returns no password hash/salt/email.
func (h identityHTTPHandler) serveUserExists(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeIdentityError(w, http.StatusMethodNotAllowed, "identity_method_not_allowed")
		return
	}
	email := NormalizeEmail(r.URL.Query().Get("email"))
	if email == "" {
		writeIdentityError(w, http.StatusBadRequest, "identity_invalid_user_request")
		return
	}
	rec, ok, err := h.svc.store.UserStatusByEmail(r.Context(), email)
	if err != nil {
		writeIdentityError(w, http.StatusServiceUnavailable, "identity_user_unavailable")
		return
	}
	resp := map[string]any{"exists": ok}
	if ok {
		resp["status"] = rec.Status
		resp["user_id"] = rec.UserID
		resp["account_id"] = rec.AccountID
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// serveUsers is the gated credential dual-write endpoint: the frontend's
```

## internal/identity/member_http.go

Source: https://github.com/0xclaw-labs/public-ai/blob/main/internal/identity/member_http.go
Blob SHA: cbd3a8fa9283e3c8608ccca2d3e94c3f4f6a516d

```go
// --- invite -----------------------------------------------------------------

type memberInviteHTTPRequest struct {
	AppID          string `json:"app_id"`
	TargetTenantID string `json:"target_tenant_id"`
	// TenantID is the pre-HUI-2078 spelling of target_tenant_id, kept for
	// existing callers; sending both with different values is rejected.
	TenantID         string `json:"tenant_id"`
	Email            string `json:"email"`
	Role             string `json:"role"`
	BillingAccountID string `json:"billing_account_id"`
}

type memberInviteHTTPResponse struct {
	InvitationID string `json:"invitation_id"`
	TenantID     string `json:"tenant_id"`
	AppID        string `json:"app_id"`
	Role         string `json:"role"`
	ExpiresAt    string `json:"expires_at"`
}

func (h *PublicAuthHandler) serveMemberInvite(w http.ResponseWriter, r *http.Request) {
	rec, app, ok := h.memberGate(w, r)
	if !ok {
		return
	}
	if h.rejectForeignOrigin(w, r) {
		return
	}
	body, ok := decodePublicBody[memberInviteHTTPRequest](w, r)
	if !ok {
		writePublicError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	ip := h.clientIP(r)
	if !h.allowBurst(w, "member_invite", rec.UserID+"|"+ip, ip, 20, 40, 10*time.Minute) {
		return
	}
	target, ok := targetTenant(body.TargetTenantID, body.TenantID)
	if !ok {
		writePublicError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	inv, err := h.members.Invite(r.Context(), InviteRequest{
		Actor: rec.UserID, TargetTenantID: target,
		AppID: firstNonEmptyMember(strings.TrimSpace(body.AppID), app.ID),
		Email: body.Email, Role: strings.TrimSpace(body.Role),
	})
	if err != nil {
		writeMemberError(w, err)
		return
	}
	writePublicJSON(w, http.StatusOK, memberInviteHTTPResponse{
		InvitationID: inv.InvitationID, TenantID: inv.TenantID, AppID: inv.AppID,
		Role: inv.Role, ExpiresAt: inv.ExpiresAt.UTC().Format(time.RFC3339),
```
```go
// --- directory & own audit --------------------------------------------------

type memberView struct {
	PrincipalID string `json:"principal_id"`
	AppID       string `json:"app_id"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at,omitempty"`
	Revoked     bool   `json:"revoked"`
}

type invitationView struct {
	InvitationID string `json:"invitation_id"`
	AppID        string `json:"app_id"`
	Role         string `json:"role"`
	MaskedEmail  string `json:"masked_email"`
	CreatedAt    string `json:"created_at"`
	ExpiresAt    string `json:"expires_at"`
	Accepted     bool   `json:"accepted"`
	Revoked      bool   `json:"revoked"`
}

type memberDirectoryResponse struct {
	Ok          bool             `json:"ok"`
	TenantID    string           `json:"tenant_id"`
	Members     []memberView     `json:"members"`
	Invitations []invitationView `json:"invitations"`
}

func (h *PublicAuthHandler) serveMemberDirectory(w http.ResponseWriter, r *http.Request) {
	rec, _, ok := h.memberGate(w, r)
	if !ok {
		return
	}
	// 读路由同样限流（写路由已有）：会话门挡匿名，这里挡被盗会话的批量目录拖取。
	ip := h.clientIP(r)
	if !h.allowBurst(w, "member_read", rec.UserID+"|"+ip, ip, 60, 120, 10*time.Minute) {
		return
	}
	q := r.URL.Query()
	dir, err := h.members.ListMembers(r.Context(), ListMembersRequest{
		Actor:          rec.UserID,
		TargetTenantID: strings.TrimSpace(q.Get("target_tenant_id")),
		AppID:          strings.TrimSpace(q.Get("app_id")),
	})
	if err != nil {
		writeMemberError(w, err)
		return
	}
	resp := memberDirectoryResponse{Ok: true, TenantID: dir.TenantID,
		Members: []memberView{}, Invitations: []invitationView{}}
	for _, m := range dir.Members {
		resp.Members = append(resp.Members, memberView{
			PrincipalID: m.PrincipalID, AppID: m.AppID, Role: m.Role,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339), Revoked: m.RevokedAt != nil,
		})
	}
	for _, inv := range dir.Invitations {
		resp.Invitations = append(resp.Invitations, invitationView{
			InvitationID: inv.InvitationID, AppID: inv.AppID, Role: inv.Role,
			MaskedEmail: MaskEmail(inv.Email),
			CreatedAt:   inv.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:   inv.ExpiresAt.UTC().Format(time.RFC3339),
			Accepted:    inv.AcceptedAt != nil, Revoked: inv.RevokedAt != nil,
		})
	}
	writePublicJSON(w, http.StatusOK, resp)
}

```

注意：users/exists 是受信内部BFF按完整邮箱的只读存在/状态查询，返回原Identity user_id/account_id，不能在浏览器猜usr_派生值。公共member目录/邀请另有成员会话、app/tenant范围及origin门，需沿真实合同核接。不能把本地tenant ID未经映射当公共tenant。优先原目录/邀请，精确邮箱受控选择若采用须服务端做本产品授权/限频/最小披露，不提供全局人员枚举。
