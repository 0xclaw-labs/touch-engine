package httpapi

// HUI-1665 增量(2026-10-14): 触点管理与稳定入口(管理面 + 公共解析)。
//
// 纪律:
//   - 管理面 /api/v1/touchpoints 不加 FEATURE 开关(仓内惯例);鉴权与 NFC 面同
//     一矩阵:org_owner 全租户、store_manager 本店(requireTagFace/记录级 store
//     scope),跨租户 404 掩码;
//   - 停用/恢复只动别名自身(SetTouchpointStatus 绝不级联 campaign_links)——
//     单卡停用不误停共享 link 的其他卡;旧 /nfc/tags/{id}/status 传播语义零改动;
//   - 换绑 = 换别名目标活动:预览(当前→新+已开始参与数只读统计)→ 确认
//     (expected 乐观并发,409 rebind_conflict);URL(别名码)永不变化;
//   - 部署五态只经显式 /stage 单步推进;CSV/二维码/检查路径零推进;
//   - QR/CSV 载荷 = 纯不透明 /t/{code} URL(PUBLIC_BASE_URL 缺失 503 fail-closed),
//     复用 qrentry/tagcsv 既有纪律(白名单尺寸/注入转义/固定六列);
//   - 公共解析 /api/v1/public/touchpoints/{code} 走 FEATURE_TOUCHPOINT_ENTRY
//     (默认 off,handler gate 统一 404 双保险):available 交既有 /c/{code} 呈现,
//     别名停用/未知/形状非法同形 404;载荷白名单 {state}/{state,code};
//   - uid_hint 是管理面提示:只出现在本面与导出列,公共面零泄漏。

import (
	"encoding/json"
	"errors"
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

// handleTouchpointRebindPreview: 当前→新活动预览 + 影响范围(已开始参与数,
// 3546 只读统计——这些参与继续绑定原活动,换绑不能挪动已承诺权益)。
func (s *Server) handleTouchpointRebindPreview(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.touchpointScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	var in struct {
		CampaignID string `json:"campaign_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.CampaignID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "campaign_id is required")
		return
	}
	next, ok := s.campaignScoped(c, in.CampaignID, authz.ActionManageTags, w)
	if !ok {
		return
	}
	current, err := s.St.GetCampaign(v.CampaignID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "current campaign not found")
		return
	}
	started, err := s.St.CountStartedParticipations(c.Member.TenantID, v.CampaignID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "impact lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current":                map[string]any{"id": current.ID, "title": current.Title, "status": current.Status},
		"next":                   map[string]any{"id": next.ID, "title": next.Title, "status": next.Status},
		"started_participations": started,
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
	var in struct {
		CampaignID         string `json:"campaign_id"`
		ExpectedCampaignID string `json:"expected_campaign_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil ||
		strings.TrimSpace(in.CampaignID) == "" || strings.TrimSpace(in.ExpectedCampaignID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "campaign_id and expected_campaign_id are required")
		return
	}
	// 目标必须本租户 + 本店 scope。
	if _, ok := s.campaignScoped(c, in.CampaignID, authz.ActionManageTags, w); !ok {
		return
	}
	// 无短码目标无法落地访客,预览与确认同拒。
	if ok, err := s.St.CampaignHasEntryLink(c.Member.TenantID, in.CampaignID); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "entry lookup failed")
		return
	} else if !ok {
		fail(w, http.StatusBadRequest, "campaign_without_entry", "target campaign has no short code entry")
		return
	}
	a, err := s.St.RebindTouchpoint(r.PathValue("id"), c.Member.TenantID, in.CampaignID, in.ExpectedCampaignID, c.Member.PrincipalRef)
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
	a, err := s.St.SetTouchpointStatus(r.PathValue("id"), c.Member.TenantID, in.Status)
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

// handlePublicTouchpoint: 无会话 GET-only 白名单解析。别名停用/未知/形状非法
// 同形 404;活动级状态(暂停/过期等)带码 200,由既有 /c/{code} 页呈现五态文案
// ——本端点零呈现职责,公共载荷不出现 uid_hint/角色/租户/门店内部字段。
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
		writeJSON(w, http.StatusOK, map[string]any{"state": string(res.State), "code": res.Code})
	case store.TpDisabled:
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(store.TpDisabled)})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(store.TpNotFound)})
	}
}
