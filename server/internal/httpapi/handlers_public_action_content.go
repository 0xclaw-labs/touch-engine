// handlers_public_action_content.go: HUI-3553 B05 公共内容读面 + 受控媒体。
//
// 纪律(票面/裁定书):
//   - 顾客可触达面走 FEATURE_CONTENT_PACKS(off = 路由不注册 + gate 统一 404);
//   - GET /api/v1/public/actions/{actionId}/content 按服务器已采用引用返回:
//     绑定 → 包 → 当前采用指针;无采用/无绑定 = 404 语义(不是空数组伪装
//     成功);blocked/授权过期 = 409 拒新分发;候选池只含已采用且可分发的
//     真实候选(「换一个」绝不随机);不暴露授权原文/内部凭据;
//   - tenant/store 从数据行解析(受信接缝),action_id 不是后台授权;
//   - POST /api/v1/public/content/{version}/media-access:purpose 白名单
//     preview|download|platform_fetch;版本必须已采用且可分发;媒体必须属于
//     该版本;签发短 TTL 凭据(非一次性,绑定版本+媒体+用途+TTL)并落审计;
//     blocked 后拒绝新签发;续签只续同一合法版本;
//   - GET|HEAD /api/v1/public/content/media/{token}:验签+TTL→版本仍可分发
//     (fail-closed:缓存 token 也拒)→媒体属于版本→真实文件存在→
//     http.ServeContent(服务器实测 MIME、ETag=sha、单/多 Range、206/416、
//     HEAD 零体;download 用途 attachment);绝不以 HTML 冒充媒体;
//   - 匿名读取与平台抓取不依赖商家 cookie;浏览/换稿/下载零生成零收费;
//   - 会话引用(action_session_id):中央集成 2026-11-10 起在签发时刻校验
//     (存在/active/tenant+动作匹配,见 checkMediaAccessSession);兑换时刻不
//     校验(平台适配器凭已签 claim 取材,不持访客 cookie);引用不是授权源,
//     会话权威归 HUI-3546,空引用保持可签发。
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// checkMediaAccessSession validates an action_session reference at issuance
// time (中央集成 2026-11-10): the session must exist, still be active, belong
// to the version's tenant, and reference this content — either its action is
// currently bound to the version's pack (the authoritative binding index) or
// the session's selected content version IS this version (preview-time 换选
// to a candidate). Everything else is rejected with the same code style as
// the session surface: unknown/mismatched = 404 not_found (anti-enumeration),
// ended = 409 session_ended, drift = 409 action_mismatch.
func (s *Server) checkMediaAccessSession(w http.ResponseWriter, sessionID, tenantID, packID, versionID string) bool {
	if s.Participation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "action_directory_unavailable",
			"message": "the session authority is not wired on this deployment; refusing to issue (fail-closed)",
		})
		return false
	}
	ref, err := s.Participation.LookupSessionRef(sessionID)
	switch {
	case errors.Is(err, participation.ErrNotFound):
		// 未知引用与跨租户引用同形 404:不泄漏存在性。
		fail(w, http.StatusNotFound, "not_found", "session not found")
		return false
	case errors.Is(err, participation.ErrSessionEnded):
		fail(w, http.StatusConflict, "session_ended", "this session has ended")
		return false
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "session check failed")
		return false
	}
	if ref.TenantID != tenantID {
		fail(w, http.StatusNotFound, "not_found", "session not found")
		return false
	}
	// 动作匹配:会话的动作当前绑定到本版本所在包;或会话的换选内容正是本版本
	// (预览期换到候选池另一包时,凭所选版本取材)。两者皆非 = 跨动作引用,
	// 绝不为这条内容背书。
	if b, berr := s.St.GetActionContentBinding(ref.TenantID, ref.ActionID); berr == nil && b.PackID == packID {
		return true
	}
	if ref.ContentPackVersion == versionID {
		return true
	}
	fail(w, http.StatusConflict, "action_mismatch", "this session does not reference this content")
	return false
}

// actionContentBindingRef renders the拟契约 binding reference: "pack:<id>".
// The actions PUT (content_binding_ref write) belongs to HUI-4584; the merge
// reconciles this ref with the actions table.
func actionContentBindingRef(packID string) string { return "pack:" + packID }

func controlledMediaExt(mime string) string {
	switch mime {
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

// versionMedia describes one media entry of a version for the public face.
type versionMedia struct {
	mediaID string
	role    string
	order   int
	mime    string
	sha256  string
	size    int64
	durMS   int64
	width   int
	height  int
	fileOK  bool
}

// collectVersionMedia lists the version's media in a stable order:
// video (role=video), cover (role=cover), then image_text in frozen order.
func collectVersionMedia(v store.StoreContentPackVersion) []versionMedia {
	out := make([]versionMedia, 0, 2+len(v.ImageText))
	if !v.Video.IsEmpty() {
		out = append(out, versionMedia{
			mediaID: v.Video.MediaID, role: "video", order: 0,
			mime: v.Video.MimeType, sha256: v.Video.SHA256, size: v.Video.ByteSize,
			durMS: v.Video.DurationMS, width: v.Video.Width, height: v.Video.Height,
			fileOK: true,
		})
	}
	if !v.Cover.IsEmpty() {
		out = append(out, versionMedia{
			mediaID: v.Cover.MediaID, role: "cover", order: 0,
			mime: v.Cover.MimeType, sha256: v.Cover.SHA256, size: v.Cover.ByteSize,
			width: v.Cover.Width, height: v.Cover.Height,
			fileOK: true,
		})
	}
	for _, img := range v.ImageText {
		if img.IsEmpty() {
			continue
		}
		out = append(out, versionMedia{
			mediaID: img.MediaID, role: "image", order: img.Order,
			mime: img.MimeType, sha256: img.SHA256, size: img.ByteSize,
			width: img.Width, height: img.Height,
			fileOK: true,
		})
	}
	return out
}

// GET /api/v1/public/actions/{actionId}/content
func (s *Server) handlePublicActionContent(w http.ResponseWriter, r *http.Request) {
	if !s.contentPackGate(w) {
		return
	}
	actionID := r.PathValue("actionId")
	if strings.TrimSpace(actionID) == "" {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	// 绑定行是 tenant/store 权威:action_id 只是查找键(不是授权)。
	binding, err := s.St.GetActionContentBindingAnyTenant(actionID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	tenantID, storeID, packID := binding.TenantID, binding.StoreID, binding.PackID
	pack, err := s.St.GetStoreContentPack(packID, tenantID)
	if err != nil || pack.StoreID != storeID {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	// 服务器已采用引用:包的当前采用指针(不是绑定行的滞后引用)。
	v, err := s.St.GetStoreAdoptedPackVersion(tenantID, packID)
	if err != nil {
		// 无采用 = 404 语义(不是空数组伪装成功)。
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		if !invalidPack409(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
		}
		return
	}
	media := make([]map[string]any, 0)
	for _, m := range collectVersionMedia(v) {
		// availability 按真实文件存在性作答(媒体缺失不伪装可用)。
		availability := "ready"
		if _, err := s.Media.Open(m.sha256, controlledMediaExt(m.mime)); err != nil {
			availability = "missing"
		}
		media = append(media, map[string]any{
			"media_id": m.mediaID, "role": m.role, "order": m.order,
			"mime_type": m.mime, "byte_size": m.size, "sha256": m.sha256,
			"duration_ms": m.durMS, "width": m.width, "height": m.height,
			"availability": availability,
		})
	}
	candidates := make([]map[string]any, 0)
	adopted, err := s.St.ListStoreAdoptedVersions(tenantID, storeID)
	if err == nil {
		now := time.Now()
		for _, cv := range adopted {
			if cv.ID == v.ID {
				continue
			}
			if err := contentpack.Distributable(cv.Status, cv.GrantUntil, now); err != nil {
				continue // blocked/expired 永不进候选池
			}
			candidate := map[string]any{
				"pack_id": cv.PackID, "version_id": cv.ID,
				"title": cv.Title, "content_kind": cv.ContentKind,
			}
			cover := cv.Cover
			if cover.IsEmpty() && len(cv.ImageText) > 0 {
				cover = cv.ImageText[0]
			}
			if !cover.IsEmpty() {
				candidate["cover"] = map[string]any{
					"media_id": cover.MediaID, "sha256": cover.SHA256,
					"mime_type": cover.MimeType,
				}
			}
			candidates = append(candidates, candidate)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action_id":                     actionID,
		"action_revision":               binding.ActionRevision,
		"content_binding_ref":           actionContentBindingRef(packID),
		"selected_content_pack_version": v.ID,
		"pack_id":                       packID,
		"store_id":                      storeID,
		"content_kind":                  v.ContentKind,
		"title":                         v.Title,
		"body":                          v.Body,
		"topics":                        v.Topics,
		"store_name":                    v.StoreName,
		"price_note":                    v.PriceNote,
		"valid_note":                    v.ValidNote,
		"marking":                       v.Marking,
		"media":                         media,
		"candidates":                    candidates,
	})
}

// POST /api/v1/public/content/{version}/media-access
func (s *Server) handlePublicMediaAccess(w http.ResponseWriter, r *http.Request) {
	if !s.contentPackGate(w) {
		return
	}
	var in struct {
		ActionSessionID string   `json:"action_session_id"`
		MediaIDs        []string `json:"media_ids"`
		Purpose         string   `json:"purpose"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if !contentpack.ValidMediaPurpose(in.Purpose) {
		fail(w, http.StatusBadRequest, contentpack.ReasonBadMediaPurpose, "purpose must be preview, download or platform_fetch")
		return
	}
	// tenant 从数据行解析(受信接缝);未知版本统一 404。
	v, err := s.St.GetStoreContentPackVersionByID(r.PathValue("version"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		// blocked/授权过期:拒新签发(续签也必须重新合法)。
		if !invalidPack409(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
		}
		return
	}
	// 中央集成接线(2026-11-10, HUI-3553←HUI-3546): action_session_id 非空时在
	// 签发时刻校验会话(存在/active/tenant 匹配/动作与内容匹配);空引用保持
	// 现状可签发(平台适配器凭已签 claim 取材不持访客 cookie——校验只在签发
	// 时刻,兑换时刻不校验会话)。会话权威是 3546 的行,引用不是授权源。
	if sid := strings.TrimSpace(in.ActionSessionID); sid != "" {
		if !s.checkMediaAccessSession(w, sid, v.TenantID, v.PackID, v.ID) {
			return
		}
	}
	known := map[string]versionMedia{}
	for _, m := range collectVersionMedia(v) {
		known[m.mediaID] = m
	}
	// 先整体校验,再签发:避免部分媒体失败时留下半批凭据审计。
	requested := make([]versionMedia, 0, len(in.MediaIDs))
	for _, mediaID := range in.MediaIDs {
		m, okKnown := known[mediaID]
		if !okKnown {
			fail(w, http.StatusUnprocessableEntity, contentpack.ReasonMediaMissing, "the requested media is not part of this version")
			return
		}
		requested = append(requested, m)
	}
	now := time.Now()
	items := make([]map[string]any, 0, len(requested))
	for _, m := range requested {
		claim, err := contentpack.NewMediaAccessClaim(v.ID, m.mediaID, in.Purpose, in.ActionSessionID, now, 0)
		if err != nil {
			if !invalidPack422(w, err) {
				fail(w, http.StatusInternalServerError, "internal", "media claim failed")
			}
			return
		}
		token, err := contentpack.MintMediaAccess(s.Cfg.InternalToken, claim)
		if err != nil {
			if !invalidPack422(w, err) {
				fail(w, http.StatusInternalServerError, "internal", "media mint failed")
			}
			return
		}
		if err := s.St.RecordMediaAccessGrant(v.TenantID, v.ID, m.mediaID, in.Purpose, in.ActionSessionID, claim.ExpiresAt); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "grant ledger failed")
			return
		}
		filename := m.mediaID
		if row, err := s.St.GetContentMedia(m.mediaID, v.TenantID); err == nil && strings.TrimSpace(row.Filename) != "" {
			filename = row.Filename
		}
		items = append(items, map[string]any{
			"media_id":   m.mediaID,
			"url":        "/api/v1/public/content/media/" + token,
			"expires_at": claim.ExpiresAt.UTC().Format(time.RFC3339),
			"mime_type":  m.mime,
			"byte_size":  m.size,
			"sha256":     m.sha256,
			"filename":   filename,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version_id": v.ID,
		"purpose":    in.Purpose,
		"expires_at": now.Add(contentpack.HandoffTTLDefault).UTC().Format(time.RFC3339),
		"items":      items,
	})
}

// GET|HEAD /api/v1/public/content/media/{token} — controlled real media.
func (s *Server) handlePublicControlledMedia(w http.ResponseWriter, r *http.Request) {
	if !s.contentPackGate(w) {
		return
	}
	claim, err := contentpack.VerifyMediaAccess(s.Cfg.InternalToken, r.PathValue("token"), time.Now())
	if err != nil {
		var ve *contentpack.ValidationError
		if errors.As(err, &ve) {
			switch ve.Reason {
			case contentpack.ReasonHandoffExpired:
				// 过期重试不能触发再制作/再扣费:重试只走 media-access 续签。
				fail(w, http.StatusGone, ve.Reason, "this media link has expired; renew via media-access")
				return
			case contentpack.ReasonHandoffInvalid:
				fail(w, http.StatusBadRequest, ve.Reason, "bad media token")
				return
			}
		}
		fail(w, http.StatusBadRequest, "media_token_invalid", "bad media token")
		return
	}
	// tenant 从数据行解析;版本/媒体/分发状态逐层 fail-closed。
	v, err := s.St.GetStoreContentPackVersionByID(claim.VersionID)
	if err != nil {
		fail(w, http.StatusGone, contentpack.ReasonMediaMissing, "resource not found")
		return
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		// 撤权后即使缓存命中也不能继续取材。
		if !invalidPack409(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
		}
		return
	}
	var snap store.StoreMediaSnapshot
	matched := false
	for _, m := range collectVersionMedia(v) {
		if m.mediaID == claim.MediaID {
			matched = true
			switch m.role {
			case "video":
				snap = v.Video
			case "cover":
				snap = v.Cover
			default:
				for _, img := range v.ImageText {
					if img.MediaID == m.mediaID {
						snap = img
					}
				}
			}
			break
		}
	}
	if !matched || snap.MediaID != claim.MediaID || snap.SHA256 != collectSHA(v, claim.MediaID) {
		fail(w, http.StatusGone, contentpack.ReasonMediaMissing, "the requested media is not part of this version")
		return
	}
	file, err := s.Media.Open(snap.SHA256, controlledMediaExt(snap.MimeType))
	if err != nil {
		// 媒体缺失:明确 410,不伪造可用。
		fail(w, http.StatusGone, contentpack.ReasonMediaMissing, "the media file is missing")
		return
	}
	defer file.Close()
	filename := snap.MediaID
	if row, err := s.St.GetContentMedia(snap.MediaID, v.TenantID); err == nil && strings.TrimSpace(row.Filename) != "" {
		filename = row.Filename
	}
	// 服务器以真实资产元数据决定类型;ETag = 内容哈希(重试续传可校验)。
	w.Header().Set("Content-Type", snap.MimeType)
	w.Header().Set("ETag", `"`+snap.SHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentpack.MediaAccessDisposition(claim.Purpose, filename))
	// ServeContent 提供 HEAD/Range(单段+多段)/206/416/Accept-Ranges。
	http.ServeContent(w, r, filename, time.Time{}, file)
}

// collectSHA resolves the frozen sha for one media id (snapshot truth).
func collectSHA(v store.StoreContentPackVersion, mediaID string) string {
	for _, m := range collectVersionMedia(v) {
		if m.mediaID == mediaID {
			return m.sha256
		}
	}
	return ""
}
