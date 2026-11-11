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
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/actioncatalog"
	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/deals"
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
	if !s.requireStoreConfig(c, authz.PermActionConfigure, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, false, w) {
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
		// HUI-3553 对接契约（中央集成 2026-11-10 已切换）：content_binding_ref
		// 只接受 '' 或 pack:<id> 形态。写入侧已切 BindActionContent（绑定表是
		// 服务器权威索引，见 bindActionContentRefs）；行值仍按现契约存
		// pack:<id>——过渡读取副本，消费方（B05 公共内容读面）读权威绑定表
		// 取真。未知形态一律拒绝，不做猜测性引用。
		binding := strings.TrimSpace(raw.ContentBindingRef)
		if binding != "" && !strings.HasPrefix(binding, "pack:") {
			fail(w, http.StatusBadRequest, "bad_content_binding_ref", "content_binding_ref must reference an adopted pack as pack:<id>")
			return
		}
		if binding != "" && strings.TrimSpace(strings.TrimPrefix(binding, "pack:")) == "" {
			fail(w, http.StatusBadRequest, "bad_content_binding_ref", "content_binding_ref must reference an adopted pack as pack:<id>")
			return
		}
		inputs = append(inputs, store.StoreActionInput{
			ID: strings.TrimSpace(raw.ActionID), CatalogCode: entry.Code,
			Label: label, TargetRef: target, ContentBindingRef: binding,
			SortOrder: raw.Order, Enabled: raw.Enabled,
		})
	}
	// 回滚基准先行（中央集成 2026-11-10）：Bind 失败时行上 content_binding_ref
	// 要还原为本轮前的值，所以旧列表必须在 Replace 之前读。
	oldItems, _, err := s.St.ListStoreActions(c.Member.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store actions lookup failed")
		return
	}
	oldRefs := make(map[string]string, len(oldItems))
	for _, it := range oldItems {
		oldRefs[it.ID] = it.ContentBindingRef
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
	// 写入侧权威绑定（HUI-4584←HUI-3553）：任一绑定失败 = 整个 PUT 400，
	// 行上的过渡副本先还原为本轮前的值。inputs 与 items 同序同长
	// （ReplaceStoreActions 按输入序构建），按下标对齐取行修订。
	if err := s.bindActionContentRefs(c.Member.TenantID, rec.ID, *body.Items, items, oldRefs, c.Member.PrincipalRef); err != nil {
		var ve *contentpack.ValidationError
		switch {
		case errors.As(err, &ve):
			fail(w, http.StatusBadRequest, ve.Reason, "content binding rejected: "+ve.Reason)
		case errors.Is(err, store.ErrNotFound):
			fail(w, http.StatusBadRequest, "bad_content_binding_ref", "content_binding_ref must reference an adopted pack of this store as pack:<id>")
		default:
			fail(w, http.StatusInternalServerError, "internal", "content binding failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, storeActionsDocument(rec, storefrontOf(w, s, c.Member.TenantID, rec.ID), items, rev))
}

// bindActionContentRefs is the write-side authority switch (中央集成
// 2026-11-10): every pack:<id> reference resolves the pack's CURRENT adopted
// version and lands in the authoritative binding index via BindActionContent;
// an empty ref on a previously-bound action (and the removal of a bound
// action row) clears the authoritative row via ClearActionContentBinding.
// All pack validation runs BEFORE any authoritative mutation, so the
// realistic failure modes never half-apply; a residual Bind failure (e.g. a
// concurrent adoption race) rolls the transitional row values back to the
// pre-round refs before returning the error.
func (s *Server) bindActionContentRefs(tenantID, storeID string, inputs []storeActionIn, saved []store.StoreAction, oldRefs map[string]string, actor string) error {
	// 回滚：任一失败路径都先把行上过渡副本还原为本轮前的值。
	rollback := func(err error) error {
		if rberr := s.St.RestoreActionBindingRefs(tenantID, storeID, oldRefs); rberr != nil {
			return fmt.Errorf("binding rollback after failure: %v (original: %w)", rberr, err)
		}
		return err
	}
	// 1) 预检：逐包解析当前采用版本（纯读，先于一切权威变更）。
	type pendingBind struct {
		actionID string
		packID   string
		version  store.StoreContentPackVersion
		revision int
	}
	pending := make([]pendingBind, 0, len(inputs))
	for i, raw := range inputs {
		binding := strings.TrimSpace(raw.ContentBindingRef)
		if binding == "" {
			continue
		}
		packID := strings.TrimSpace(strings.TrimPrefix(binding, "pack:"))
		pack, err := s.St.GetStoreContentPack(packID, tenantID)
		if err != nil {
			return rollback(fmt.Errorf("pack %s: %w", packID, store.ErrNotFound)) // 未知/跨租户包 → 400 bad_content_binding_ref
		}
		if pack.StoreID != storeID {
			return rollback(fmt.Errorf("pack %s belongs to another store: %w", packID, store.ErrNotFound))
		}
		v, err := s.St.GetStoreAdoptedPackVersion(tenantID, packID)
		if err != nil {
			// 未采用（或指针不在本包）：与 BindActionContent 同一拒绝语义。
			return rollback(contentpack.NewValidationError(contentpack.ReasonNotDistributable, "version"))
		}
		if i >= len(saved) {
			return rollback(fmt.Errorf("bind alignment lost: %w", store.ErrUnknownActionID))
		}
		pending = append(pending, pendingBind{
			actionID: saved[i].ID, packID: pack.ID, version: v, revision: saved[i].Revision,
		})
	}
	// 2) 解绑与清理：本轮清空引用的原绑定行；整行被删除的动作其权威索引跟随。
	for _, id := range sortedKeys(oldRefs) {
		if _, still := savedByID(saved, id); !still {
			if err := s.St.ClearActionContentBinding(tenantID, id); err != nil {
				return rollback(err)
			}
		}
	}
	for i, raw := range inputs {
		if strings.TrimSpace(raw.ContentBindingRef) != "" || i >= len(saved) {
			continue
		}
		if old, had := oldRefs[saved[i].ID]; had && old != "" {
			if err := s.St.ClearActionContentBinding(tenantID, saved[i].ID); err != nil {
				return rollback(err)
			}
		}
	}
	// 3) 建绑/重绑：权威索引落地（行修订 = 本轮 Replace 后的行值）。
	for _, p := range pending {
		if _, err := s.St.BindActionContent(tenantID, storeID, p.actionID, p.packID, p.version.ID, p.revision, actor, time.Now()); err != nil {
			return rollback(err)
		}
	}
	return nil
}

// savedByID reports whether the id survived this round's replace.
func savedByID(saved []store.StoreAction, id string) (store.StoreAction, bool) {
	for _, it := range saved {
		if it.ID == id {
			return it, true
		}
	}
	return store.StoreAction{}, false
}

// sortedKeys returns the map keys in stable order (deterministic clears).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
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
	case dealTargetRefInvalid(item.CatalogCode, item.TargetRef):
		// HUI-4590 A19-A21 target 验证器接线(wave2 集成):团购类目的目标
		// 除公网 https 外还必须是该类目自己平台的官方商品页域;串挂/伪装域
		// 如实报进三分证据(web 态关闭+原因),不冒充可达。
		addError("target_ref", "target_not_platform_official")
		webState, webReason = "closed", "address_not_platform_official"
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

// dealTargetRefInvalid reports whether this catalog code is one of the
// group-buy codes (HUI-4590 A19 美团团购 / A20 大众点评团购 / A21 抖音团购)
// whose target_ref must be a public https page on the code's own platform
// domain (deals.ProductURLValid: 官方地址门 ∧ 平台域名门,子域允许、后缀伪装
// 拒)。非团购类目不在本门内(通用公网 https 门已在上一个 case)。
func dealTargetRefInvalid(catalogCode, targetRef string) bool {
	entry, ok := actioncatalog.Lookup(catalogCode)
	if !ok || entry.Kind != actioncatalog.KindGroupBuy {
		return false
	}
	return !deals.ProductURLValid(entry.Platform, targetRef)
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
