// handlers_public_action_content.go: HUI-3553 B05 公共内容读面 + 受控媒体。
//
// 纪律(票面/裁定书):
//   - 顾客可触达面走 FEATURE_CONTENT_PACKS(off = 路由不注册 + gate 统一 404);
//   - GET /api/v1/public/actions/{actionId}/content 读取已发布的具体版本及冻结候选池;
//     本人 action_session_id 可继续读取该会话的冻结选择;未发布 = 404;
//     blocked/授权过期 = 409;不暴露授权原文/内部凭据;
//   - tenant/store 从数据行解析(受信接缝),action_id 不是后台授权;
//   - POST /api/v1/public/content/{version}/media-access:purpose 白名单
//     preview|download|platform_fetch;预览须已发布或属合法冻结会话；导出另须
//     本人原已准入媒体交接及同冻结版本,用途不得超出该交接形态；商家沿原scoped授权。
//     签发短TTL凭据(版本+媒体+用途+来源+TTL)并落审计;
//     blocked 后拒绝新签发;续签只续同一合法版本;
//   - GET|HEAD /api/v1/public/content/media/{token}:验签+TTL→版本仍可分发
//     (fail-closed:缓存 token 也拒)→媒体属于版本→真实文件存在→
//     http.ServeContent(服务器实测 MIME、ETag=sha、单/多 Range、206/416、
//     HEAD 零体;download 用途 attachment);绝不以 HTML 冒充媒体;
//   - 匿名读取与平台抓取不依赖商家 cookie;浏览/换稿/下载零生成零收费;
//   - source_kind/source_ref由服务端在原交接或scoped商家授权后签入；非预览
//     兑换沿签名来源重新核当前会话/成员/准入/动作及内容状态,平台不带访客cookie。
//     旧无可证明来源的非预览票据410要求原入口重签；预览仍可匿名读取实际bytes。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// checkMediaAccessSession validates an action_session reference at issuance
// time: the session must exist, remain active, and authorize this exact version
// from its frozen publication candidate pool. A current pack binding never
// grants access to arbitrary versions. Everything else is rejected with the same code style as
// the session surface: unknown/mismatched = 404 not_found (anti-enumeration),
// ended = 409 session_ended, drift = 409 action_mismatch.
func (s *Server) checkMediaAccessSession(w http.ResponseWriter, sessionID, tenantID, packID, versionID string) bool {
	if err := s.validateMediaAccessSession(sessionID, tenantID, packID, versionID); err != nil {
		var denied *mediaExportError
		if errors.As(err, &denied) {
			fail(w, denied.status, denied.code, denied.message)
		} else {
			fail(w, http.StatusInternalServerError, "internal", "session check failed")
		}
		return false
	}
	return true
}

func (s *Server) validateMediaAccessSession(sessionID, tenantID, packID, versionID string) error {
	if s.Participation == nil {
		return &mediaExportError{http.StatusServiceUnavailable, "action_directory_unavailable", "the session authority is not wired on this deployment"}
	}
	ref, err := s.Participation.LookupSessionRef(sessionID)
	switch {
	case errors.Is(err, participation.ErrNotFound):
		// 未知引用与跨租户引用同形 404:不泄漏存在性。
		return &mediaExportError{http.StatusNotFound, "not_found", "session not found"}
	case errors.Is(err, participation.ErrSessionEnded):
		return &mediaExportError{http.StatusConflict, "session_ended", "this session has ended"}
	case err != nil:
		return &mediaExportError{http.StatusInternalServerError, "internal", "session check failed"}
	}
	if ref.TenantID != tenantID {
		return &mediaExportError{http.StatusNotFound, "not_found", "session not found"}
	}
	// A frozen session authorizes exact publication candidates, never all versions of a pack.
	if ok, err := s.St.SessionActionAvailable(ref.TenantID, ref.StoreID, ref.ActionID, time.Now()); err != nil || !ok {
		return &mediaExportError{http.StatusNotFound, "not_found", "action not available"}
	}
	allowed := false
	if ref.ContentCandidates != nil {
		for _, id := range ref.ContentCandidates {
			if id == versionID {
				allowed = true
			}
		}
	} else if ref.ContentPackVersion == versionID {
		var err error
		allowed, err = s.St.VerifyHistoricalSessionContent(ref.TenantID, ref.StoreID, ref.ActionID, versionID, ref.ContentSelectedAt)
		if err != nil {
			return &mediaExportError{http.StatusInternalServerError, "internal", "session content check failed"}
		}
	}
	if allowed {
		if ref.StorefrontRevision > 0 {
			action, err := s.St.SessionPublishedAction(ref.TenantID, ref.StoreID, ref.ActionID, ref.StorefrontRevision, ref.ActionRevision, time.Now())
			if err != nil {
				return &mediaExportError{http.StatusNotFound, "not_found", "session publication not available"}
			}
			if action.Kind == "publish" {
				compatible := false
				for _, id := range action.ContentCandidates {
					compatible = compatible || id == versionID
				}
				if !compatible {
					return &mediaExportError{http.StatusConflict, "action_mismatch", "this content does not match the published action"}
				}
			}
		}
		return nil
	}

	return &mediaExportError{http.StatusConflict, "action_mismatch", "this session does not reference this content"}
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
	var tenantID, storeID, selected string
	var pool []string
	var revision int
	if sessionID := strings.TrimSpace(r.URL.Query().Get("action_session_id")); sessionID != "" {
		if s.Participation == nil {
			fail(w, http.StatusServiceUnavailable, "action_directory_unavailable", "session authority unavailable")
			return
		}
		visitor := requireVisitor404(w, r)
		if visitor == "" {
			return
		}
		view, err := s.Participation.ActionSessionStatus(r.Context(), sessionID, visitor)
		if err != nil {
			writeActionSessionError(w, err)
			return
		}
		sess := view.Session
		if sess.ActionID != actionID {
			fail(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		if sess.Status != participation.SessionActive {
			fail(w, http.StatusConflict, "session_ended", "this session has ended")
			return
		}
		tenantID, storeID, selected, pool, revision = sess.TenantID, sess.StoreID, sess.ContentPackVersion, sess.ContentCandidates, sess.ActionRevision
		if !s.checkMediaAccessSession(w, sessionID, tenantID, "", selected) {
			return
		}
	} else {
		ref, exists, err := s.ResolveAction(r.Context(), actionID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "action lookup failed")
			return
		}
		if !exists {
			fail(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		tenantID, storeID, selected, pool, revision = ref.TenantID, ref.StoreID, ref.ContentPackVersion, ref.ContentCandidates, ref.ActionRevision
	}

	v, err := s.St.GetStoreContentPackVersion(selected, tenantID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	packID := v.PackID
	pack, err := s.St.GetStoreContentPack(packID, tenantID)
	if err != nil || pack.StoreID != storeID {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if pool == nil {
		pool = []string{selected}
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
	now := time.Now()
	for _, candidateID := range pool {
		cv, err := s.St.GetStoreContentPackVersion(candidateID, tenantID)
		if err != nil {
			continue
		}
		cp, err := s.St.GetStoreContentPack(cv.PackID, tenantID)
		if err != nil || cp.StoreID != storeID {
			continue
		}
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
	cleanPayload, _ := matrixhandoff.StripMerchantFields(map[string]any{
		"action_id":                     actionID,
		"action_revision":               revision,
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
	writeJSON(w, http.StatusOK, cleanPayload)
}

// POST /api/v1/public/content/{version}/media-access
func (s *Server) handlePublicMediaAccess(w http.ResponseWriter, r *http.Request) {
	if !s.contentPackGate(w) {
		return
	}
	var in struct {
		ActionSessionID string   `json:"action_session_id"`
		HandoffID       string   `json:"handoff_id"`
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
	// 浏览预览保持已发布/本人冻结候选合同。非预览不能仅凭version或
	// 任意session升级：必须准确落到本人原media handoff及其当前准入。
	// 商家素材管理沿原scoped wrapper；兑换凭签名来源查权威,平台无需访客cookie。
	sourceKind, sourceRef := "", ""
	if c := callerFrom(r); c != nil && c.Member != nil {
		// Only the scoped merchant wrapper sets this trusted caller; public JSON
		// cannot assert a source. Renewals remain tied to the live member row.
		sourceKind, sourceRef = contentpack.MediaSourceMerchant, c.Member.ID
		in.ActionSessionID = ""
	} else {
		if in.Purpose != contentpack.MediaPurposePreview && strings.TrimSpace(in.ActionSessionID) == "" {
			fail(w, http.StatusUnprocessableEntity, "media_handoff_required", "请先通过原动作的已准入媒体交接取材")
			return
		}
		if sid := strings.TrimSpace(in.ActionSessionID); sid != "" {
			visitor := requireVisitor404(w, r)
			if visitor == "" {
				return
			}
			if _, err := s.Participation.ActionSessionStatus(r.Context(), sid, visitor); err != nil {
				writeActionSessionError(w, err)
				return
			}
			if !s.checkMediaAccessSession(w, sid, v.TenantID, v.PackID, v.ID) {
				return
			}
		} else {
			allowed, err := s.St.PublishedContentVersion(v.ID, time.Now())
			if err != nil {
				fail(w, http.StatusInternalServerError, "internal", "publication lookup failed")
				return
			}
			if !allowed {
				fail(w, http.StatusNotFound, "not_found", "resource not found")
				return
			}
		}
		if in.Purpose != contentpack.MediaPurposePreview {
			if s.PublishHandoff == nil {
				fail(w, http.StatusServiceUnavailable, "handoff_unavailable", "handoff authority unavailable")
				return
			}
			sourceRef = strings.TrimSpace(in.HandoffID)
			if sourceRef == "" {
				// Compatibility renewal: resolve an exact frozen version, never any
				// handoff or current selection in the session.
				_ = s.St.DB.QueryRow(`SELECT id FROM publish_handoffs WHERE tenant_id=? AND action_session_id=? AND content_pack_version_id=? AND payload_kind='media' ORDER BY created_at DESC,id DESC LIMIT 1`, v.TenantID, in.ActionSessionID, v.ID).Scan(&sourceRef)
			}
			if sourceRef == "" {
				fail(w, http.StatusUnprocessableEntity, "media_handoff_required", "请先通过原动作的已准入媒体交接取材")
				return
			}
			h, err := s.PublishHandoff.GetActionForVisitor(r.Context(), sourceRef, mustVisitor(r))
			if err != nil || h.SessionID != in.ActionSessionID {
				fail(w, http.StatusNotFound, "not_found", "media handoff not found")
				return
			}
			sourceKind = contentpack.MediaSourceActionHandoff
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
	if in.Purpose != contentpack.MediaPurposePreview {
		for _, m := range requested {
			claim := contentpack.MediaAccessClaim{VersionID: v.ID, MediaID: m.mediaID, Purpose: in.Purpose, SessionRef: in.ActionSessionID, SourceKind: sourceKind, SourceRef: sourceRef}
			if !s.checkMediaExportSource(w, r.Context(), v, claim) {
				return
			}
		}
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
		claim.SourceKind, claim.SourceRef = sourceKind, sourceRef
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
	if claim.Purpose != contentpack.MediaPurposePreview && !s.checkMediaExportSource(w, r.Context(), v, claim) {
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

// checkMediaExportSource rechecks server-owned provenance without invoking an
// adapter or requiring a platform fetcher to carry a visitor/merchant cookie.
// A signed token is a transport credential; live membership, session, exact
// frozen handoff, admission and purpose remain independent vetoes.
type mediaExportError struct {
	status        int
	code, message string
}

func (e *mediaExportError) Error() string { return e.message }

func (s *Server) checkMediaExportSource(w http.ResponseWriter, ctx context.Context, v store.StoreContentPackVersion, claim contentpack.MediaAccessClaim) bool {
	if err := s.validateMediaExportSource(ctx, v, claim); err != nil {
		var denied *mediaExportError
		if errors.As(err, &denied) {
			fail(w, denied.status, denied.code, denied.message)
		} else {
			s.writeActionHandoffError(w, err)
		}
		return false
	}
	return true
}

func (s *Server) validateMediaExportSource(ctx context.Context, v store.StoreContentPackVersion, claim contentpack.MediaAccessClaim) error {
	if claim.SourceKind == "" || claim.SourceRef == "" {
		return &mediaExportError{http.StatusGone, "media_source_renewal_required", "旧取材链接无法证明签发来源，请从原合法交接或商家素材管理重新取材"}
	}
	if claim.SourceKind == contentpack.MediaSourceMerchant {
		member, err := s.St.GetMember(claim.SourceRef)
		pack, perr := s.St.GetStoreContentPack(v.PackID, v.TenantID)
		if err != nil || perr != nil || member.TenantID != v.TenantID || claim.SessionRef != "" {
			return &mediaExportError{http.StatusNotFound, "not_found", "media source not found"}
		}
		d := authz.Authorize(s.authzMember(&caller{Member: &member}), authz.ActionReadList, authz.RecordScope{TenantID: v.TenantID, StoreID: pack.StoreID})
		if !d.Allowed {
			return &mediaExportError{http.StatusForbidden, d.Reason, "media source membership no longer permits this store"}
		}
		return nil
	}
	if s.PublishHandoff == nil {
		return &mediaExportError{http.StatusServiceUnavailable, "handoff_unavailable", "handoff authority unavailable"}
	}
	handoffID := claim.SourceRef
	if claim.SourceKind == contentpack.MediaSourceActionStart {
		// Adapter prepare receives URLs before StartAction returns; the immutable
		// row is already inserted before Prepare can pull these URLs.
		if err := s.St.DB.QueryRow(`SELECT id FROM publish_handoffs WHERE tenant_id=? AND action_session_id=? AND idempotency_key=? AND content_pack_version_id=? AND payload_kind='media'`, v.TenantID, claim.SessionRef, claim.SourceRef, v.ID).Scan(&handoffID); err != nil {
			return &mediaExportError{http.StatusNotFound, "not_found", "media handoff not found"}
		}
	} else if claim.SourceKind != contentpack.MediaSourceActionHandoff && claim.SourceKind != contentpack.MediaSourceCampaignHandoff {
		return &mediaExportError{http.StatusGone, "media_source_renewal_required", "unrecognized media source; renew from the original authority"}
	}
	h, err := s.PublishHandoff.LoadMediaSource(handoffID)
	if err != nil || h.TenantID != v.TenantID || h.ContentPackVersionID != v.ID {
		return &mediaExportError{http.StatusNotFound, "not_found", "media handoff not found"}
	}
	if h.Status == publishhandoff.StateCanceled || h.Status == publishhandoff.StateFailed {
		return &mediaExportError{http.StatusConflict, "invalid_state", "this handoff no longer allows media access"}
	}
	text, media, err := s.PublishHandoff.SnapshotOf(ctx, h.ID, h.TenantID)
	matched := false
	for _, m := range media {
		if m.AssetID == claim.MediaID && m.SHA256 == collectSHA(v, claim.MediaID) {
			matched = true
		}
	}
	if err != nil || !matched {
		return &mediaExportError{http.StatusNotFound, "not_found", "media not in frozen handoff"}
	}
	if claim.SourceKind == contentpack.MediaSourceCampaignHandoff {
		if h.PayloadKind != "" || claim.SessionRef != "" || claim.Purpose != contentpack.MediaPurposePlatformFetch {
			return &mediaExportError{http.StatusUnprocessableEntity, "media_purpose_not_allowed", "purpose does not match the original campaign handoff"}
		}
		if s.Participation == nil {
			return &mediaExportError{http.StatusServiceUnavailable, "participation_unavailable", "participation authority unavailable"}
		}
		visitor := mustLegacyVisitor(s, h.ID)
		p, err := s.Participation.GetForVisitor(ctx, h.ParticipationID, visitor)
		resolved := s.St.ResolveLink(p.LinkCode, time.Now())
		var frozen string
		ferr := s.St.DB.QueryRow(`SELECT content_pack_version FROM participation_attempts WHERE id=? AND tenant_id=? AND participation_id=? AND status='handed_off' AND platform=?`, h.AttemptID, h.TenantID, h.ParticipationID, h.Platform).Scan(&frozen)
		if err != nil || ferr != nil || frozen != v.ID || p.TenantID != h.TenantID || p.StoreID != h.StoreID || resolved.Outcome != store.OutcomeAvailable || resolved.Campaign.ID != p.CampaignID || s.legacyPublishScenario(h.TenantID, p.CampaignID) != h.Scenario {
			return &mediaExportError{http.StatusNotFound, "not_found", "campaign handoff no longer available"}
		}
		if h.Method == "" {
			rec := s.publishRecordOrUnknown(h.Platform, h.EntryMode, h.Scenario, h.TenantID)
			plan := publishhandoff.PlanRoute(publishhandoff.PlanInput{TenantID: h.TenantID, Platform: h.Platform, EntryMode: h.EntryMode, Scenario: h.Scenario, Now: time.Now().UTC(), Text: text, DeepLinkBase: s.Cfg.PublishHandoffH5EntryBase, MiniappBase: s.Cfg.PublishHandoffMiniappEntryBase, RewardPlay: rec.Scenario == channeladmission.ActionScenarioRewardTask, MerchantAppRef: s.Cfg.PublishHandoffMerchantAppRef}, rec)
			if plan.Kind == publishhandoff.PlanRefused || plan.Kind == publishhandoff.PlanPendingExternal {
				return &publishhandoff.HandoffRefusedError{Reason: plan.Reason, ReasonText: plan.ReasonText}
			}
			return nil
		}
	} else {
		if s.Participation == nil || claim.SessionRef == "" || h.SessionID != claim.SessionRef || h.PayloadKind != publishhandoff.PayloadKindMedia {
			return &mediaExportError{http.StatusNotFound, "not_found", "media handoff not found"}
		}
		ref, err := s.Participation.LookupSessionRef(claim.SessionRef)
		if err != nil || ref.TenantID != h.TenantID || ref.StoreID != h.StoreID || ref.ActionID != h.ActionID || ref.Platform != h.Platform || ref.ActionRevision != h.ActionRevision {
			return &mediaExportError{http.StatusNotFound, "not_found", "media session no longer available"}
		}
		if err := s.validateMediaAccessSession(claim.SessionRef, v.TenantID, v.PackID, v.ID); err != nil {
			return err
		}
		if claim.Purpose != actionMediaPurpose(h.PlanKind) {
			return &mediaExportError{http.StatusUnprocessableEntity, "media_purpose_not_allowed", "purpose does not match the original handoff route"}
		}
	}
	rec := s.resolveActionCapability(h.TenantID, h.CatalogCode, h.Platform, h.Method, h.Scenario)
	plan := publishhandoff.JudgeActionPlan(s.actionGateQuery(rec, h.Environment), rec, publishhandoff.ActionPlanInput{Text: text, Media: media})
	if !plan.Allowed {
		return &publishhandoff.HandoffRefusedError{Reason: plan.Reason, ReasonText: plan.ReasonText, PendingExternal: plan.PendingExternal}
	}
	if h.PayloadKind != "" && claim.Purpose != actionMediaPurpose(plan.Kind) {
		return &mediaExportError{http.StatusUnprocessableEntity, "media_purpose_not_allowed", "current admission does not authorize this purpose"}
	}
	return nil
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

// handleMerchantMediaAccess keeps candidate preview inside the existing scoped content authority.
func (s *Server) handleMerchantMediaAccess(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.storeContentPackRead(c, r.PathValue("id"), w) {
		return
	}
	if !s.storePackInTenantStore(w, c.Member.TenantID, r.PathValue("id"), r.PathValue("packId")) {
		return
	}
	v, err := s.St.GetStoreContentPackVersion(r.PathValue("versionId"), c.Member.TenantID)
	if err != nil || v.PackID != r.PathValue("packId") {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	r.SetPathValue("version", v.ID)
	s.handlePublicMediaAccess(w, r)
}
