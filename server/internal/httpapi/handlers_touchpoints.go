package httpapi

// HUI-1665 增量(2026-10-14): 触点管理与稳定入口(管理面 + 公共解析)。
// HUI-1665 Wave2(2026-11-10): B02 门店稳定触点 —— store 根目标/换绑 target 契约。
//
// 纪律:
//   - 管理面 /api/v1/touchpoints 不加 FEATURE 开关(仓内惯例);鉴权与 NFC 面同
//     一矩阵:org_owner 全租户、store_manager 本店(requireTagFace/记录级 store
//     scope),跨租户 404 掩码;
//   - 停用/恢复只动别名自身(SetTouchpointStatus 绝不级联 campaign_links)——
//     单卡停用不误停共享的其他卡;旧 /nfc/tags/{id}/status 传播语义零改动;
//   - 换绑 = 换别名目标(B02:target_type=store 根 / legacy_campaign):预览
//     (当前→新目标事实+已开始参与数只读统计+binding_revision)→ 确认
//     (expected_binding_revision 乐观并发,409 rebind_conflict;旧
//     expected_campaign_id 请求体保留兼容);store 目标经 storeTargetScoped
//     (存在/未停用/越店 404 掩码);URL(别名码)永不变化;
//   - 部署五态只经显式 /stage 单步推进;CSV/二维码/检查路径零推进;
//   - QR/CSV 载荷 = 纯不透明 /t/{code} URL(PUBLIC_BASE_URL 缺失 503 fail-closed),
//     复用 qrentry/tagcsv 既有纪律(白名单尺寸/注入转义/固定六列);
//   - 公共解析 /api/v1/public/touchpoints/{code} 走 FEATURE_TOUCHPOINT_ENTRY
//     (默认 off,handler gate 统一 404 双保险):redirect 载荷白名单
//     {schema_version,state,target_type,binding_revision,store_code?(store 根,
//     零活动门控) | code+campaign_code(legacy,五态交 /c/{code} 呈现)};
//     别名停用/未知/形状非法同形 404;跳转路径只由前端按白名单目标构造;
//   - uid_hint 是管理面提示:只出现在本面与导出列,公共面零泄漏。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/qrentry"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/tagcsv"
)

// touchpointAdminView is the management response shape: the store view plus
// the ready-to-copy stable URL (omitted when PUBLIC_BASE_URL is unconfigured
// — 不生成占位域名入口当已可用).
type touchpointAdminView struct {
	store.TouchpointView
	URL    string                  `json:"url,omitempty"`
	Events []store.TouchpointEvent `json:"events,omitempty"`
}

func (s *Server) touchpointURL(code string) string {
	if strings.TrimSpace(s.Cfg.PublicBaseURL) == "" {
		return ""
	}
	u, err := qrentry.PublicTouchEntryURL(s.Cfg.PublicBaseURL, code)
	if err != nil {
		return "" // code 由本系统生成,构造失败属内部错误;不猜测
	}
	return u
}

func (s *Server) touchpointAdminView(v store.TouchpointView, withEvents bool) touchpointAdminView {
	out := touchpointAdminView{TouchpointView: v, URL: s.touchpointURL(v.Code)}
	if withEvents {
		if events, err := s.St.ListTouchpointEvents(v.TenantID, v.ID); err == nil {
			out.Events = events
		}
	}
	return out
}

// touchpointScoped fetches the alias view in the caller's tenant and enforces
// the member's store scope on it. Missing and out-of-scope are both 404 (掩码).
func (s *Server) touchpointScoped(c *caller, id string, w http.ResponseWriter) (store.TouchpointView, bool) {
	v, err := s.St.GetTouchpointView(id, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "touchpoint not found")
		return store.TouchpointView{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
		return store.TouchpointView{}, false
	}
	if !s.requireScopedAction(c, authz.ActionManageTags, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: v.StoreID}, true, w) {
		return store.TouchpointView{}, false
	}
	return v, true
}

// ---- provision / list / detail ---------------------------------------------------

func (s *Server) handleTouchpointProvision(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	var in struct {
		TagID    string `json:"tag_id"`
		Label    string `json:"label"`
		Position string `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.TagID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "tag_id is required")
		return
	}
	// 门店经理只能为本店标签启用入口(服务端强制,客户端输入不放宽)。
	if scope != "" {
		tv, err := s.St.GetTagView(in.TagID, c.Member.TenantID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && tv.StoreID != scope) {
			fail(w, http.StatusNotFound, "not_found", "tag not found")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "tag lookup failed")
			return
		}
	}
	a, err := s.St.ProvisionTouchpoint(c.Member.TenantID, in.TagID, in.Label, in.Position, c.Member.PrincipalRef)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrTagAlreadyProvisioned):
		fail(w, http.StatusConflict, "already_provisioned", "this tag already has a stable entry")
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "tag not found")
		return
	default:
		fail(w, http.StatusInternalServerError, "internal", "provision failed")
		return
	}
	v, err := s.St.GetTouchpointView(a.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
		return
	}
	writeJSON(w, http.StatusCreated, s.touchpointAdminView(v, false))
}

// parseTouchpointFilter reads the shared list/export filters. Unknown values
// are refused (bad_filter) instead of silently ignored.
func parseTouchpointFilter(r *http.Request) (store.TouchpointFilter, bool) {
	f := store.TouchpointFilter{
		CampaignID: strings.TrimSpace(r.URL.Query().Get("campaign_id")),
		StoreID:    strings.TrimSpace(r.URL.Query().Get("store_id")),
		GroupID:    strings.TrimSpace(r.URL.Query().Get("group_id")),
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
	}
	switch f.Status {
	case "", store.TagStatusActive, store.TagStatusDisabled:
	default:
		return f, false
	}
	return f, true
}

func (s *Server) handleTouchpointList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	f, ok := parseTouchpointFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_filter", "status must be active or disabled")
		return
	}
	// 服务端强制作用域过滤——客户端传来的 store_id 永不放宽经理视野。
	if scope != "" {
		f.StoreID = scope
	}
	items, err := s.St.ListTouchpoints(c.Member.TenantID, f)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint list failed")
		return
	}
	out := make([]touchpointAdminView, 0, len(items))
	for _, v := range items {
		out = append(out, s.touchpointAdminView(v, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func (s *Server) handleTouchpointGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.touchpointScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.touchpointAdminView(v, true))
}

// ---- rebind ----------------------------------------------------------------------

// touchpointTargetFacts is the preview projection of one binding target: the
// fields the confirm dialog shows, keyed by target type. Legacy targets carry
// campaign facts; store targets carry store facts (public code included).
func (s *Server) touchpointTargetFacts(tenantID string, t store.TouchpointTarget) map[string]any {
	out := map[string]any{"target_type": t.Type, "id": ""}
	switch t.Type {
	case store.TargetStore:
		out["id"] = t.StoreID
		if sto, err := s.St.GetStore(t.StoreID, tenantID); err == nil {
			out["title"] = sto.Name
			out["status"] = sto.Status
		}
		if sf, err := s.St.StorefrontForStore(tenantID, t.StoreID); err == nil {
			out["store_code"] = sf.StoreCode
		}
	default:
		out["id"] = t.CampaignID
		if camp, err := s.St.GetCampaign(t.CampaignID, tenantID); err == nil {
			out["title"] = camp.Title
			out["status"] = camp.Status
			out["campaign_id"] = camp.ID
		}
	}
	return out
}

// parseTouchpointTarget reads a preview/rebind request body into a target plus
// the concurrency guard. New contract: {target_type, store_id|campaign_id?,
// expected_binding_revision}. Legacy bodies ({campaign_id,
// expected_campaign_id}) keep working unchanged (增量演进不砸旧调用方).
func parseTouchpointTarget(body io.Reader) (store.TouchpointTarget, store.TouchpointRebindExpectation, bool, error) {
	var in struct {
		TargetType             string `json:"target_type"`
		StoreID                string `json:"store_id"`
		CampaignID             string `json:"campaign_id"`
		ExpectedBindingRevision int64 `json:"expected_binding_revision"`
		ExpectedCampaignID     string `json:"expected_campaign_id"`
	}
	if err := json.NewDecoder(body).Decode(&in); err != nil {
		return store.TouchpointTarget{}, store.TouchpointRebindExpectation{}, false, err
	}
	guard := store.TouchpointRebindExpectation{Revision: in.ExpectedBindingRevision, CampaignID: in.ExpectedCampaignID}
	switch strings.TrimSpace(in.TargetType) {
	case store.TargetStore:
		if strings.TrimSpace(in.StoreID) == "" {
			return store.TouchpointTarget{}, store.TouchpointRebindExpectation{}, false, fmt.Errorf("store_id is required for a store target")
		}
		return store.TouchpointTarget{Type: store.TargetStore, StoreID: strings.TrimSpace(in.StoreID)}, guard, true, nil
	case store.TargetLegacyCampaign:
		if strings.TrimSpace(in.CampaignID) == "" {
			return store.TouchpointTarget{}, store.TouchpointRebindExpectation{}, false, fmt.Errorf("campaign_id is required for a campaign target")
		}
		return store.TouchpointTarget{Type: store.TargetLegacyCampaign, CampaignID: strings.TrimSpace(in.CampaignID)}, guard, true, nil
	case "":
		// 兼容路径:只有 campaign_id + expected_campaign_id 的旧请求体。
		if strings.TrimSpace(in.CampaignID) == "" {
			return store.TouchpointTarget{}, store.TouchpointRebindExpectation{}, false, fmt.Errorf("target_type or campaign_id is required")
		}
		return store.TouchpointTarget{Type: store.TargetLegacyCampaign, CampaignID: strings.TrimSpace(in.CampaignID)}, guard, false, nil
	default:
		return store.TouchpointTarget{}, store.TouchpointRebindExpectation{}, false, fmt.Errorf("target_type must be store or legacy_campaign")
	}
}

// storeTargetScoped validates a store rebind target: exists in tenant, not
// disabled, inside the caller's store scope. Out-of-scope masks as 404
// (与 campaignScoped 同形;目标输入永不放宽经理视野).
func (s *Server) storeTargetScoped(c *caller, storeID string, w http.ResponseWriter) (store.StoreRecord, bool) {
	sto, err := s.St.GetStore(storeID, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return store.StoreRecord{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return store.StoreRecord{}, false
	}
	if sto.Status == store.StoreStatusDisabled {
		fail(w, http.StatusBadRequest, "store_disabled", "store is disabled; rebinding into it is refused")
		return store.StoreRecord{}, false
	}
	if !s.requireScopedAction(c, authz.ActionManageTags, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: sto.ID}, true, w) {
		return store.StoreRecord{}, false
	}
	return sto, true
}

// touchpointPreviewImpact counts participations bound to the CURRENT target's
// campaign (read-only, 3546 domain): 换绑不能挪动已承诺权益,统计只描述影响面。
func (s *Server) touchpointPreviewImpact(tenantID, campaignID string) int64 {
	if strings.TrimSpace(campaignID) == "" {
		return 0
	}
	started, err := s.St.CountStartedParticipations(tenantID, campaignID)
	if err != nil {
		return 0 // 影响面统计失败不阻断预览;确认侧不受影响
	}
	return started
}

// handleTouchpointRebindPreview: 当前→新目标预览 + 影响范围(已开始参与数只读
// 统计——这些参与继续绑定原活动,换绑不能挪动已承诺权益)+ binding_revision
// (确认时回传作 expected)。
func (s *Server) handleTouchpointRebindPreview(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.touchpointScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	target, _, _, err := parseTouchpointTarget(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	switch target.Type {
	case store.TargetStore:
		if _, ok := s.storeTargetScoped(c, target.StoreID, w); !ok {
			return
		}
	default:
		if _, ok := s.campaignScoped(c, target.CampaignID, authz.ActionManageTags, w); !ok {
			return
		}
		// 无短码目标无法落地访客,预览与确认同拒。
		if ok, err := s.St.CampaignHasEntryLink(c.Member.TenantID, target.CampaignID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "entry lookup failed")
			return
		} else if !ok {
			fail(w, http.StatusBadRequest, "campaign_without_entry", "target campaign has no short code entry")
			return
		}
	}
	current := store.TouchpointTarget{Type: v.TargetType, StoreID: v.StoreID, CampaignID: v.CampaignID}
	writeJSON(w, http.StatusOK, map[string]any{
		"current":                s.touchpointTargetFacts(c.Member.TenantID, current),
		"next":                   s.touchpointTargetFacts(c.Member.TenantID, target),
		"started_participations": s.touchpointPreviewImpact(c.Member.TenantID, v.CampaignID),
		"binding_revision":       v.BindingRevision,
	})
}

func (s *Server) handleTouchpointRebind(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	if _, ok := s.touchpointScoped(c, r.PathValue("id"), w); !ok {
		return
	}
	target, guard, _, err := parseTouchpointTarget(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	switch target.Type {
	case store.TargetStore:
		if _, ok := s.storeTargetScoped(c, target.StoreID, w); !ok {
			return
		}
	default:
		if _, ok := s.campaignScoped(c, target.CampaignID, authz.ActionManageTags, w); !ok {
			return
		}
		// 无短码目标无法落地访客,预览与确认同拒。
		if ok, err := s.St.CampaignHasEntryLink(c.Member.TenantID, target.CampaignID); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "entry lookup failed")
			return
		} else if !ok {
			fail(w, http.StatusBadRequest, "campaign_without_entry", "target campaign has no short code entry")
			return
		}
	}
	a, err := s.St.RebindTouchpointTarget(r.PathValue("id"), c.Member.TenantID, target, guard, c.Member.PrincipalRef)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrRebindConflict):
		fail(w, http.StatusConflict, "rebind_conflict", "the current target moved; re-read the preview and retry")
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "touchpoint or campaign not found")
		return
	default:
		fail(w, http.StatusInternalServerError, "internal", "rebind failed")
		return
	}
	v, err := s.St.GetTouchpointView(a.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, s.touchpointAdminView(v, false))
}

// ---- status / stage ----------------------------------------------------------------

func (s *Server) handleTouchpointStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	if _, ok := s.touchpointScoped(c, r.PathValue("id"), w); !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Status == "" {
		fail(w, http.StatusBadRequest, "bad_request", "status (active|disabled) is required")
		return
	}
	a, err := s.St.SetTouchpointStatus(r.PathValue("id"), c.Member.TenantID, in.Status, c.Member.PrincipalRef)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "touchpoint not found")
		return
	default:
		fail(w, http.StatusBadRequest, "bad_status", "status must be active or disabled")
		return
	}
	v, err := s.St.GetTouchpointView(a.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, s.touchpointAdminView(v, false))
}

// handleTouchpointStage: 部署五态显式单步推进。CSV/二维码/检查路径绝不调用。
func (s *Server) handleTouchpointStage(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	if _, ok := s.touchpointScoped(c, r.PathValue("id"), w); !ok {
		return
	}
	var in struct {
		Stage string `json:"stage"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Stage) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "stage is required")
		return
	}
	a, err := s.St.AdvanceTouchpointStage(r.PathValue("id"), c.Member.TenantID, in.Stage, in.Note, c.Member.PrincipalRef)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrStageTransition):
		fail(w, http.StatusConflict, "stage_transition", "stages advance one explicit step at a time")
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "touchpoint not found")
		return
	default:
		fail(w, http.StatusInternalServerError, "internal", "stage advance failed")
		return
	}
	v, err := s.St.GetTouchpointView(a.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, s.touchpointAdminView(v, false))
}

// ---- QR / CSV -----------------------------------------------------------------------

func (s *Server) handleTouchpointQRCode(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.touchpointScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	// fail-closed:没有对外基地址就无法构造 canonical 载荷,拒绝猜测。
	if strings.TrimSpace(s.Cfg.PublicBaseURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "qr_not_configured",
			"message": "PUBLIC_BASE_URL is not configured; the QR payload cannot be built (fail-closed)",
		})
		return
	}
	size, err := qrentry.ParseSize(r.URL.Query().Get("size"))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_size", "size must be one of 128/256/512")
		return
	}
	payload, err := qrentry.PublicTouchEntryURL(s.Cfg.PublicBaseURL, v.Code)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "qr payload build failed")
		return
	}
	// json 载荷:面板展示 + 可复制稳定 URL。载荷本身只有不透明 /t/ URL。
	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, map[string]any{"url": payload, "code": v.Code, "size": size})
		return
	}
	pngBytes, err := qrentry.PNG(payload, size)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "qr render failed")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// code is Crockford base32: header-safe by construction
	w.Header().Set("Content-Disposition", `attachment; filename="touchpoint-`+v.Code+`-`+strconv.Itoa(size)+`.png"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pngBytes)
}

// handleTouchpointExportCSV: 写卡资料导出。列契约与 PR#4 写卡工具一致
// (label,short_code,url,store,group,uid_hint),url 列承载 /t/{别名码}。
func (s *Server) handleTouchpointExportCSV(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	// fail-closed:没有对外基地址就无法构造 canonical URL 列,拒绝猜测。
	if strings.TrimSpace(s.Cfg.PublicBaseURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "public_base_url_missing",
			"message": "PUBLIC_BASE_URL is not configured; the url column cannot be built (fail-closed)",
		})
		return
	}
	f, ok := parseTouchpointFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_filter", "status must be active or disabled")
		return
	}
	if scope != "" {
		f.StoreID = scope
	}
	items, err := s.St.ListTouchpoints(c.Member.TenantID, f)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "touchpoint export failed")
		return
	}
	rows := make([]tagcsv.Row, 0, len(items))
	for _, v := range items {
		url, err := qrentry.PublicTouchEntryURL(s.Cfg.PublicBaseURL, v.Code)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "url build failed")
			return
		}
		label := v.Label
		if label == "" {
			label = v.TagLabel
		}
		rows = append(rows, tagcsv.Row{
			Label: label, ShortCode: v.Code, URL: url,
			Store: v.StoreName, Group: v.GroupName, UIDHint: v.UIDHint,
		})
	}
	out, err := tagcsv.Build(rows)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "csv build failed")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="touchpoints-`+strconv.FormatInt(time.Now().UTC().Unix(), 10)+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}

// ---- public stable-entry resolution ---------------------------------------------------

// touchpointSchemaVersion freezes the public payload shape (票面兼容扩展契约:
// schema_version/state/target_type/store_code?/campaign_code?/binding_revision;
// 旧响应 code 按兼容约定保留在 legacy 分支).
const touchpointSchemaVersion = "touchpoint.v2"

// handlePublicTouchpoint: 无会话 GET-only 白名单解析。别名停用/未知/形状非法
// 同形 404;redirect 分目标类型产出白名单载荷:store 根带门店公共码(零活动
// 门控——无活动/暂停/结束不关闭门店基础服务,门店面自身状态由 /s 页经
// GET /api/v1/public/storefronts/{storeCode} 呈现),legacy 带规范短码与其五态
// (由既有 /c/{code} 页呈现)——本端点零呈现职责,公共载荷不出现 uid_hint/
// 角色/租户/门店内部字段;跳转路径只由前端按白名单目标构造,不接受外链。
func (s *Server) handlePublicTouchpoint(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.FeatureTouchpointEntry {
		// handler gate:与路由不注册双保险,off 永远统一 404。
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(store.TpNotFound)})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "stable entry resolution is read-only")
		return
	}
	code := strings.TrimSpace(r.PathValue("code"))

	// 缓存沿 FEATURE_PUBLIC_CACHE 既有策略:rows 缓存 + 命中时刻现判状态,
	// epoch 结构失效让别名建/停/换绑对下一个请求可见(0 陈旧)。
	var res store.TouchpointResolution
	var rows *store.TouchpointRows
	if s.Cfg.FeaturePublicCache && s.TpCache != nil && !s.TpCache.Disabled() && campaign.ValidShortcode(code) {
		if loaded, _, err := s.TpCache.GetOrLoad("tp:"+code, func() (store.TouchpointRows, error) {
			return s.St.LoadTouchpointRows(code), nil
		}); err == nil {
			rows = &loaded
			res = store.DecideTouchpoint(loaded, time.Now())
		}
	}
	if rows == nil {
		res = s.St.ResolveTouchpoint(code, time.Now())
	}

	switch res.Outcome {
	case store.TpRedirect:
		payload := map[string]any{
			"schema_version":   touchpointSchemaVersion,
			"state":            string(res.State),
			"target_type":      res.TargetType,
			"binding_revision": res.BindingRevision,
		}
		if res.TargetType == store.TargetStore {
			payload["store_code"] = res.StoreCode
		} else {
			payload["code"] = res.Code           // 兼容约定保留(旧 /t 前端只认 state+code)
			payload["campaign_code"] = res.Code  // 票面契约:显式活动短码字段
		}
		writeJSON(w, http.StatusOK, payload)
	case store.TpDisabled:
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(store.TpDisabled)})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(store.TpNotFound)})
	}
}
