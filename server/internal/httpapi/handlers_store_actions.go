package httpapi

// HUI-4584 G1: 门店动作目录与渠道配置（商家面 GET/PUT + validate）。
//
// 纪律：
//   - 鉴权复用既有 authz：GET=ReadRecord(store scope)、PUT=Update(store scope)、
//     validate=先按 actionId 解析受信 tenant/store（actionId 不是授权）再按
//     ReadRecord(store scope) 掩码 404。不新建权限体系（新权限动作归 HUI-4593）。
//   - 公共面在 handlers_public storefront（本票另一端点），不在本文件。
//   - validate 只读：不改任何行、不外呼、不执行发布/关注/加群/收费。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/actioncatalog"
	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

type storeActionIn struct {
	ActionID          string `json:"action_id"`
	CatalogCode       string `json:"catalog_code"`
	Platform          string `json:"platform"`
	Kind              string `json:"kind"`
	Variant           string `json:"variant"`
	Label             string `json:"label"`
	TargetRef         string `json:"target_ref"`
	ContentBindingRef string `json:"content_binding_ref"`
	Order             int    `json:"order"`
	Enabled           bool   `json:"enabled"`
}

func (s *Server) handleStoreActionsGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	sf, items, rev, ok := s.loadStoreActions(w, c.Member.TenantID, rec.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, storeActionsDocument(rec, sf, items, rev))
}

func (s *Server) handleStoreActionsPut(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, false, w) {
		return
	}
	var body struct {
		ExpectedRevision *int             `json:"expected_revision"`
		Items            *[]storeActionIn `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedRevision == nil || body.Items == nil {
		fail(w, http.StatusBadRequest, "bad_request", "expected_revision and items are required")
		return
	}
	inputs := make([]store.StoreActionInput, 0, len(*body.Items))
	for _, raw := range *body.Items {
		code := strings.TrimSpace(raw.CatalogCode)
		entry, known := actioncatalog.Lookup(code)
		if !known {
			fail(w, http.StatusBadRequest, "unknown_catalog_code", "catalog_code must be one of A01-A30")
			return
		}
		label := strings.TrimSpace(raw.Label)
		if len(label) > 200 {
			fail(w, http.StatusBadRequest, "bad_label", "label is too long")
			return
		}
		target := strings.TrimSpace(raw.TargetRef)
		if raw.Enabled && target == "" {
			fail(w, http.StatusBadRequest, "target_required", "an enabled action needs a public https target")
			return
		}
		if target != "" && !extrajump.OfficialAddress(target) {
			fail(w, http.StatusBadRequest, "bad_target_ref", "target_ref must be a public https URL")
			return
		}
		inputs = append(inputs, store.StoreActionInput{
			ID: strings.TrimSpace(raw.ActionID), CatalogCode: entry.Code,
			Label: label, TargetRef: target, ContentBindingRef: strings.TrimSpace(raw.ContentBindingRef),
			SortOrder: raw.Order, Enabled: raw.Enabled,
		})
	}
	rev, items, err := s.St.ReplaceStoreActions(c.Member.TenantID, rec.ID, *body.ExpectedRevision, inputs)
	switch {
	case errors.Is(err, store.ErrStaleRevision):
		fail(w, http.StatusConflict, "stale_version", "config was changed by someone else; reload and retry")
		return
	case errors.Is(err, store.ErrDuplicateActionID):
		fail(w, http.StatusBadRequest, "duplicate_action_id", "the same action_id appears twice in items")
		return
	case errors.Is(err, store.ErrUnknownActionID):
		fail(w, http.StatusBadRequest, "unknown_action_id", "an action_id does not exist in this store")
		return
	case errors.Is(err, store.ErrUnknownCatalogCode):
		fail(w, http.StatusBadRequest, "unknown_catalog_code", "catalog_code must be one of A01-A30")
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "store actions could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, storeActionsDocument(rec, storefrontOf(w, s, c.Member.TenantID, rec.ID), items, rev))
}

// storeForActions resolves the store within the caller's tenant (404 masking).
func (s *Server) storeForActions(w http.ResponseWriter, r *http.Request, c *caller) (store.StoreRecord, bool) {
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return store.StoreRecord{}, false
	}
	rec, err := s.St.GetStore(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return store.StoreRecord{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return store.StoreRecord{}, false
	}
	return rec, true
}

func (s *Server) loadStoreActions(w http.ResponseWriter, tenantID, storeID string) (store.Storefront, []store.StoreAction, int, bool) {
	sf, err := s.St.StorefrontForStore(tenantID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront could not be provisioned")
		return store.Storefront{}, nil, 0, false
	}
	items, rev, err := s.St.ListStoreActions(tenantID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store actions lookup failed")
		return store.Storefront{}, nil, 0, false
	}
	return sf, items, rev, true
}

func storefrontOf(w http.ResponseWriter, s *Server, tenantID, storeID string) store.Storefront {
	sf, err := s.St.StorefrontForStore(tenantID, storeID)
	if err != nil {
		return store.Storefront{}
	}
	return sf
}

func storeActionsDocument(rec store.StoreRecord, sf store.Storefront, items []store.StoreAction, rev int) map[string]any {
	return map[string]any{
		"store_id":        rec.ID,
		"store_name":      rec.Name,
		"store_code":      sf.StoreCode,
		"config_revision": rev,
		"items":           storeActionItems(items),
		"catalog":         catalogDocument(),
	}
}

func catalogDocument() []map[string]any {
	entries := actioncatalog.All()
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, map[string]any{
			"code": entry.Code, "label": entry.Label, "platform": entry.Platform,
			"kind": entry.Kind, "variant": entry.Variant,
		})
	}
	return out
}

func storeActionItems(items []store.StoreAction) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		source := "store"
		if item.LegacyCampaignID != "" {
			source = "legacy_campaign"
		}
		label := catalogLabelOf(item.CatalogCode)
		out = append(out, map[string]any{
			"action_id":           item.ID,
			"action_revision":     item.Revision,
			"catalog_code":        item.CatalogCode,
			"catalog_label":       label,
			"platform":            item.Platform,
			"kind":                item.Kind,
			"variant":             item.Variant,
			"label":               item.Label,
			"target_ref":          item.TargetRef,
			"content_binding_ref": item.ContentBindingRef,
			"capability_ref":      item.CapabilityRef,
			"order":               item.SortOrder,
			"enabled":             item.Enabled,
			"revision":            item.Revision,
			"source":              source,
			"legacy_campaign_id":  item.LegacyCampaignID,
		})
	}
	return out
}

func catalogLabelOf(code string) string {
	if entry, ok := actioncatalog.Lookup(code); ok {
		return entry.Label
	}
	return ""
}

// handleStoreActionValidate validates one action instance. Read-only: it
// never publishes, follows, joins, charges, or writes any row.
func (s *Server) handleStoreActionValidate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	actionID := strings.TrimSpace(r.PathValue("actionId"))
	tenantID, storeID, err := s.St.StoreActionScope(actionID)
	if errors.Is(err, store.ErrNotFound) || err == nil && (tenantID == "" && storeID == "") {
		// 统一 404：不泄漏 action id 的存在性。
		fail(w, http.StatusNotFound, "not_found", "action not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "action lookup failed")
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: tenantID, StoreID: storeID}, true, w) {
		return
	}
	item, err := s.St.GetStoreActionByID(tenantID, storeID, actionID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "action not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "action lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, validateDocument(item, time.Now().UTC()))
}

// validateDocument returns action_id/action_revision/field_errors/method_states/
// reason_codes (+ reward_admission 引用位). 三分证据与旧跳转面同口径：网页可达 ≠
// 原生唤起 ≠ 平台动作；营销奖励场景准入真值在 HUI-3535，本票只留 capability_ref。
func validateDocument(item store.StoreAction, now time.Time) map[string]any {
	type fieldError = map[string]any
	var fieldErrors []fieldError
	var reasonCodes []string
	addError := func(field, code string) {
		fieldErrors = append(fieldErrors, fieldError{"field": field, "code": code})
		reasonCodes = append(reasonCodes, code)
	}

	webState := "configured"
	webReason := ""
	switch {
	case item.CatalogCode == "":
		addError("catalog_code", "catalog_unassigned")
		webState, webReason = "closed", "not_configured"
	case item.Revoked:
		addError("revoked", "revoked")
		webState, webReason = "closed", "revoked"
	case actionExpiredAt(item.ExpiresAt, now):
		addError("expires_at", "expired")
		webState, webReason = "closed", "expired"
	case item.TargetRef == "":
		if item.Enabled {
			addError("target_ref", "target_missing")
		}
		webState, webReason = "closed", "not_configured"
	case !extrajump.OfficialAddress(item.TargetRef):
		addError("target_ref", "target_not_official")
		webState, webReason = "closed", "address_not_official"
	}
	if fieldErrors == nil {
		fieldErrors = []fieldError{}
	}
	if reasonCodes == nil {
		reasonCodes = []string{}
	}

	methodStates := []map[string]any{
		{"method": "web", "state": webState},
		{"method": "client_launch", "state": "unverified", "reason": "native_launch_unverified"},
		{"method": "platform_action", "state": "unknown"},
	}
	if webReason != "" {
		methodStates[0]["reason"] = webReason
	}

	return map[string]any{
		"action_id":       item.ID,
		"action_revision": item.Revision,
		"catalog_code":    item.CatalogCode,
		"enabled":         item.Enabled,
		"field_errors":    fieldErrors,
		"method_states":   methodStates,
		"reason_codes":    reasonCodes,
		"reward_admission": map[string]any{
			"evaluated":      false,
			"capability_ref": item.CapabilityRef,
			"reason":         "admission_truth_not_wired",
		},
		"creates_lead": false, "crm_imported": false, "reward_triggered": false, "publish_success": false,
		"auto_follow": false, "auto_join": false, "auto_pay": false,
	}
}

func actionExpiredAt(raw string, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return true
		}
	}
	return !t.After(now)
}
