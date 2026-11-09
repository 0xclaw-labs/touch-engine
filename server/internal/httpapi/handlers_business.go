package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func authzScope(c *caller) authz.RecordScope {
	if c == nil || c.Member == nil {
		return authz.RecordScope{}
	}
	return authz.RecordScope{TenantID: c.Member.TenantID}
}

// ---- HUI-1674 作用域裁决助手 -------------------------------------------------
//
// 作用域事实在服务端:members.store_scope 决定门店经理能触达哪些记录。
// 客户端声明(头/查询参数/请求体)只影响"想做什么",永不影响"能做什么"。

// callerIsStoreManager reports whether the caller is store-scope-limited.
func callerIsStoreManager(c *caller) bool {
	return c != nil && c.Member != nil && authz.Role(c.Member.Role) == authz.RoleStoreManager
}

// callerStoreScope returns the member's store scope ("" = 总部/租户级).
func callerStoreScope(c *caller) string {
	if c == nil || c.Member == nil {
		return ""
	}
	return c.Member.StoreScope
}

// callerStoreLimited (HUI-4593 修复轮 4):成员行自带门店作用域、读列表必须按
// 本店过滤的角色 —— 门店经理,以及 0064 起显式带 store_scope 的店员(业务动作
// 仅本店语义)。租户级成员(含旧 staff)不是 store-limited。
func callerStoreLimited(c *caller) bool {
	if c == nil || c.Member == nil || c.Member.StoreScope == "" {
		return false
	}
	switch authz.Role(c.Member.Role) {
	case authz.RoleStoreManager, authz.RoleStaff:
		return true
	default:
		return false
	}
}

// requireScopedAction authorizes action on a record in rec. For denials on an
// EXISTING record (mask404), out_of_scope is answered 404 not_found so
// foreign-store records stay invisible (跨店 404 掩码);for target-parameter
// checks (create/rebind) it stays 403 out_of_scope.
func (s *Server) requireScopedAction(c *caller, action authz.Action, rec authz.RecordScope, mask404 bool, w http.ResponseWriter) bool {
	d := authz.Authorize(s.authzMember(c), action, rec)
	if d.Allowed {
		return true
	}
	if mask404 && d.Reason == authz.ReasonOutOfScope {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return false
	}
	fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
	return false
}

// campaignScoped fetches the campaign within the caller's tenant and enforces
// the member's store scope on it. Missing and out-of-scope are both 404.
func (s *Server) campaignScoped(c *caller, id string, action authz.Action, w http.ResponseWriter) (store.Campaign, bool) {
	camp, err := s.St.GetCampaign(id, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return store.Campaign{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return store.Campaign{}, false
	}
	if !s.requireScopedAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: camp.StoreID}, true, w) {
		return store.Campaign{}, false
	}
	return camp, true
}

// validateStoreBinding checks a store_id supplied as a campaign binding target:
// it must exist in the tenant, must NOT be disabled (停用门店禁止新建/改绑入),
// and must be inside the caller's store scope (store_manager).
func (s *Server) validateStoreBinding(c *caller, storeID string, w http.ResponseWriter) bool {
	sto, err := s.St.GetStore(storeID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_store", "store not found in this tenant")
		return false
	}
	if sto.Status == store.StoreStatusDisabled {
		fail(w, http.StatusBadRequest, "store_disabled", "store is disabled; new campaign binding is refused (existing campaigns are handled explicitly)")
		return false
	}
	if !s.requireScopedAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: sto.ID}, false, w) {
		return false
	}
	return true
}

// allowScopedList gates the list surfaces: org_owner/staff pass the tenant-level
// ReadList gate; a store_manager also passes but MUST continue into the
// server-side scope-filtered branch (their list is never tenant-wide).
func (s *Server) allowScopedList(c *caller, w http.ResponseWriter) bool {
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "no membership resolved")
		return false
	}
	d := authz.Authorize(s.authzMember(c), authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID})
	if d.Allowed {
		return true
	}
	if d.Reason == authz.ReasonOutOfScope && callerIsStoreManager(c) {
		return true
	}
	fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
	return false
}

// allowScopedCreate gates creation surfaces: org_owner/staff pass the
// tenant-level Create gate; a store_manager also passes but the real decision
// is made afterwards by requireScopedAction against the TARGET store of the
// request (own store only). Disabled/unknown roles fail as usual.
func (s *Server) allowScopedCreate(c *caller, w http.ResponseWriter) bool {
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "no membership resolved")
		return false
	}
	d := authz.Authorize(s.authzMember(c), authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID})
	if d.Allowed {
		return true
	}
	if d.Reason == authz.ReasonOutOfScope && callerIsStoreManager(c) {
		return true
	}
	fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
	return false
}

// ---- stores -----------------------------------------------------------------
// HUI-1674:org_owner 建店/改店/停用店;store_manager 仅读本店;
// 停用门店不级联——存量活动逐个显式处理。

func (s *Server) handleStoreCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageStores, authzScope(c), w) {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "store name is required")
		return
	}
	rec, err := s.St.CreateStore(c.Member.TenantID, in.Name, in.Address, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store create failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleStoreList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	// 修复轮 4:store-limited 成员(经理/带 scope 店员)对租户级列表的 out_of_scope
	// 放行为「读后按本店过滤」,其余拒绝照旧;列表响应用服务端过滤收口,绝不外泄他店。
	if d := authz.Authorize(s.authzMember(c), authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}); !d.Allowed {
		if !(d.Reason == authz.ReasonOutOfScope && callerStoreLimited(c)) {
			fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
			return
		}
	}
	items, err := s.St.ListStores(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store list failed")
		return
	}
	// store-limited:仅本店(即便已停用——经理/店员仍需看到自己门店的状态)
	if callerStoreLimited(c) {
		scope := callerStoreScope(c)
		filtered := make([]store.StoreRecord, 0, 1)
		for _, it := range items {
			if it.ID == scope {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStoreGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, err := s.St.GetStore(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleStorePatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageStores, authzScope(c), w) {
		return
	}
	var in struct {
		Name    *string `json:"name"`
		Address *string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "store name must not be empty")
		return
	}
	rec, err := s.St.UpdateStore(r.PathValue("id"), c.Member.TenantID, in.Name, in.Address)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store update failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleStoreStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageStores, authzScope(c), w) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Status == "" {
		fail(w, http.StatusBadRequest, "bad_request", "status (active|disabled) is required")
		return
	}
	rec, err := s.St.SetStoreStatus(r.PathValue("id"), c.Member.TenantID, in.Status)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_status", "status must be active or disabled")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---- campaigns ---------------------------------------------------------------

type campaignIn struct {
	Title         string `json:"title"`
	PublicContent string `json:"public_content"`
	StartsAt      string `json:"starts_at"`
	EndsAt        string `json:"ends_at"`
	StoreID       string `json:"store_id"`
	OrderRef      string `json:"order_ref"` // optional opaque reference; empty = 无订单活动
}

func (s *Server) handleCampaignCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.allowScopedCreate(c, w) {
		return
	}
	var in campaignIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Title) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "campaign title is required")
		return
	}
	if in.StartsAt != "" || in.EndsAt != "" {
		if err := validateWindow(in.StartsAt, in.EndsAt); err != "" {
			fail(w, http.StatusBadRequest, "bad_window", err)
			return
		}
	}
	if in.StoreID != "" {
		if !s.validateStoreBinding(c, in.StoreID, w) {
			return
		}
	}
	// HUI-1674 作用域:门店经理只能把活动建在自己门店(in.StoreID 为空=总部级
	// 活动,经理不可建);org_owner/staff 不受限(存量语义)。
	if !s.requireScopedAction(c, authz.ActionCreate, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: in.StoreID}, false, w) {
		return
	}
	if s.Cfg.FeatureBrand && c.Brand != nil && !c.Brand.TouchOn {
		fail(w, http.StatusForbidden, "brand_app_disabled", "this brand has not enabled touch; existing records stay readable")
		return
	}
	rec, err := s.St.CreateCampaign(store.NewCampaign{
		TenantID: c.Member.TenantID, Title: in.Title, PublicContent: in.PublicContent,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, StoreID: in.StoreID,
		OrderRef: in.OrderRef, CreatedBy: c.Member.PrincipalRef,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign create failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func validateWindow(startsAt, endsAt string) string {
	if startsAt != "" {
		if _, err := parseRFC3339(startsAt); err != nil {
			return "starts_at must be RFC3339"
		}
	}
	if endsAt != "" {
		if _, err := parseRFC3339(endsAt); err != nil {
			return "ends_at must be RFC3339"
		}
	}
	if startsAt != "" && endsAt != "" {
		st, _ := parseRFC3339(startsAt)
		en, _ := parseRFC3339(endsAt)
		if en.Before(st) {
			return "ends_at must not be before starts_at"
		}
	}
	return ""
}

func (s *Server) handleCampaignList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.allowScopedList(c, w) {
		return
	}
	// HUI-1674:门店经理只见本店活动(服务端强制过滤;未绑定门店的活动=总部级,
	// 对经理不可见)。
	var items []store.Campaign
	var err error
	if callerIsStoreManager(c) {
		items, err = s.St.ListCampaignsByStore(c.Member.TenantID, callerStoreScope(c))
	} else {
		items, err = s.St.ListCampaigns(c.Member.TenantID)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCampaignGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleCampaignPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	cur, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	var in struct {
		Title         *string `json:"title"`
		PublicContent *string `json:"public_content"`
		StartsAt      *string `json:"starts_at"`
		EndsAt        *string `json:"ends_at"`
		StoreID       *string `json:"store_id"`
		Status        *string `json:"status"` // accepted only to be refused: use /status
		OrderRef      *string `json:"order_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.Status != nil {
		fail(w, http.StatusBadRequest, "use_status_endpoint", "campaign status changes must use POST /campaigns/{id}/status")
		return
	}
	if in.OrderRef != nil {
		fail(w, http.StatusBadRequest, "order_ref_immutable", "order_ref is fixed at creation")
		return
	}
	if (in.StartsAt != nil && *in.StartsAt != "") || (in.EndsAt != nil && *in.EndsAt != "") {
		st := cur.StartsAt
		en := cur.EndsAt
		if in.StartsAt != nil {
			st = *in.StartsAt
		}
		if in.EndsAt != nil {
			en = *in.EndsAt
		}
		if msg := validateWindow(st, en); msg != "" {
			fail(w, http.StatusBadRequest, "bad_window", msg)
			return
		}
	}
	if in.StoreID != nil {
		// 改绑目标:必须存在、未停用;门店经理只能绑回本店(解绑=总部级,拒绝)
		if *in.StoreID != "" {
			if !s.validateStoreBinding(c, *in.StoreID, w) {
				return
			}
		} else if callerIsStoreManager(c) {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager cannot unbind a campaign from its store")
			return
		}
	}
	rec, err := s.St.UpdateCampaign(r.PathValue("id"), c.Member.TenantID, store.CampaignPatch{
		Title: in.Title, PublicContent: in.PublicContent, StartsAt: in.StartsAt, EndsAt: in.EndsAt, StoreID: in.StoreID,
	})
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign update failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleCampaignStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w); !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	to := campaign.Status(in.Status)
	if !to.Valid() {
		fail(w, http.StatusBadRequest, "bad_status", "status must be one of draft/active/paused/ended")
		return
	}
	rec, err := s.St.TransitionCampaign(r.PathValue("id"), c.Member.TenantID, to)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if errors.Is(err, campaign.ErrIllegalTransition) {
		fail(w, http.StatusConflict, "illegal_transition", err.Error())
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "status change failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---- campaign assets (素材引用,不复制文件) ---------------------------------------

func (s *Server) handleAssetAdd(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	// campaign scope first: another tenant's campaign must be invisible here,
	// and a store manager only reaches own-store campaigns (跨店 404 掩码).
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionCreate, w); !ok {
		return
	}
	var in struct {
		AssetID string `json:"asset_id"`
		Version string `json:"version"` // optional; must equal platform sha256 when given
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.AssetID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "asset_id is required")
		return
	}

	// 平台接入纪律:素材引用必须经平台 upload 校验。开关关 = 显式拒绝,不伪造授权。
	if !s.Cfg.FeatureUpload {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_disabled",
			"message": "FEATURE_UPLOAD is off; material references cannot be validated (fail-closed)",
		})
		return
	}

	meta, err := s.Upload.ValidateRef(r.Context(), in.AssetID, in.Version)
	switch {
	case err == nil:
		// continue below
	case errors.Is(err, upload.ErrNotFound):
		fail(w, http.StatusNotFound, "upload_not_found", "asset does not exist (or deleted/quarantined) at platform upload")
		return
	case errors.Is(err, upload.ErrUnauthorized):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_unavailable",
			"message": "platform upload rejected this app's credentials; check PLATFORM_UPLOAD_TOKEN",
		})
		return
	case errors.Is(err, upload.ErrForbidden):
		fail(w, http.StatusForbidden, "upload_forbidden", "asset exists but this app may not reference it")
		return
	case errors.Is(err, upload.ErrNotReady):
		fail(w, http.StatusConflict, "upload_not_ready", "asset is not in ready state")
		return
	case errors.Is(err, upload.ErrVersionMismatch):
		fail(w, http.StatusConflict, "version_mismatch", "supplied version does not match platform sha256")
		return
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_unavailable",
			"message": "platform upload could not be reached; refusing to record a reference (fail-closed)",
		})
		return
	}

	rec, err := s.St.AddCampaignAsset(c.Member.TenantID, r.PathValue("id"), meta.AssetID, meta.SHA256, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, "asset_ref_conflict", "this asset version is already referenced (or storage error)")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleAssetList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	// campaign scope first: another tenant's campaign must be invisible here
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w); !ok {
		return
	}
	items, err := s.St.ListCampaignAssets(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAssetRemove(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w); !ok {
		return
	}
	if err := s.St.RemoveCampaignAsset(r.PathValue("assetId"), c.Member.TenantID, r.PathValue("id")); err != nil {
		fail(w, http.StatusNotFound, "not_found", "asset reference not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// ---- campaign links (受控定位短码) ------------------------------------------------

func (s *Server) handleLinkCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionCreate, w); !ok {
		return
	}
	if s.Cfg.FeatureBrand && c.Brand != nil && !c.Brand.TouchOn {
		fail(w, http.StatusForbidden, "brand_app_disabled", "this brand has not enabled touch; existing records stay readable")
		return
	}
	rec, err := s.St.CreateLink(c.Member.TenantID, r.PathValue("id"), c.Member.PrincipalRef)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link create failed")
		return
	}
	if s.Cfg.FeatureBrand && c.Brand != nil && c.Brand.Result.Manifest.Status == "active" {
		_ = s.St.StampLinkBrand(rec.ID, c.Member.TenantID, c.Brand.Result.Manifest.BrandID, c.Brand.Host)
		rec, _ = s.St.GetLink(rec.ID, c.Member.TenantID)
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleLinkList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	// campaign scope first: another tenant's campaign must be invisible here
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w); !ok {
		return
	}
	items, err := s.St.ListLinksByCampaign(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleLinkEnabled(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, http.StatusBadRequest, "bad_request", "enabled (boolean) is required")
		return
	}
	// scope via the link's campaign (HUI-1674): missing and out-of-scope are 404
	link, err := s.St.GetLink(r.PathValue("linkId"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "link not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link lookup failed")
		return
	}
	if _, ok := s.campaignScoped(c, link.CampaignID, authz.ActionUpdate, w); !ok {
		return
	}
	rec, err := s.St.SetLinkEnabled(r.PathValue("linkId"), c.Member.TenantID, *in.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "link not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link update failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
