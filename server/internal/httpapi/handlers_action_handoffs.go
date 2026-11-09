package httpapi

// handlers_action_handoffs.go: HUI-1670 touch-handoff/v2 统一发布交接 HTTP 面。
//
// 面貌(全部登记在 FEATURE_PUBLISH_HANDOFF 之下,off = 不注册 + 统一 404):
//   GET  /api/v1/public/actions/{actionId}/handoffs/plan     计划预览(零落行)
//   POST /api/v1/public/actions/{actionId}/handoffs          开交接(gate-first)
//   GET  /api/v1/public/handoffs/{handoffId}                 查态(反枚举 404)
//   POST /api/v1/public/handoffs/{handoffId}/facts           弱证据层落事件
//   POST /api/v1/public/handoffs/{handoffId}/submit          经 adapter.Submit
//   POST /api/v1/public/handoffs/{handoffId}/query           经 adapter.Query
//   POST /api/v1/public/handoffs/{handoffId}/cancel          顾客取消(终态)
//   POST /api/v1/public/handoff-callbacks/{platform}        官方回调验签
//
// 纪律(裁定书/3535 合同 §6):
//   - 请求权威 = 3546 动作会话(visitor cookie 是唯一访问凭据;path actionId
//     必须等于会话动作,否则同形 404 反枚举);
//   - Gate 上下文服务端冻结:scenario 由动作 Kind 派生、RewardPlay 按记录
//     scenario 派生、MerchantAppRef 取部署配置——客户端 reward/绑定标记
//     永不参与裁决;
//   - payload_kind 分流:media=3553 已采用可分发版本+指纹确认;link_share=
//     服务端从已发布动作 TargetRef 生成合法 share_url,不伪建内容包;
//   - 媒体引用按 3553 media-access 机制铸造(短 TTL/purpose 白名单/落审计),
//     文件字节零过本服务;自动交接 purpose=platform_fetch,手动导出 purpose=
//     download;
//   - fixture 回执只进测试;回调未配 adapter/验签失败一律拒绝(NOT_VERIFIED)。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// scenarioForKind maps the action kind (4584) onto the 3535 scenario
// dimension — server-side derivation, the client never names the scenario.
func scenarioForKind(kind string) string {
	switch kind {
	case "review":
		return channeladmission.ActionScenarioReview
	case "follow":
		return channeladmission.ActionScenarioFollow
	case "service":
		return channeladmission.ActionScenarioService
	case "groupbuy":
		return channeladmission.ActionScenarioGroupBuy
	default:
		return channeladmission.ActionScenarioStorePromo
	}
}

// resolveActionCapability picks the governing 3535 record for the natural key
// (catalog × platform × method × scenario) — GetActionCapabilitiesByCode has
// already collapsed tenant-scoped rows over '*'. No row = synthesized unknown
// record (fail-closed → pending_external semantics; 不泄露库内存在性).
func (s *Server) resolveActionCapability(tenantID, catalogCode, platform, method, scenario string) channeladmission.ActionCapability {
	rows, err := s.St.GetActionCapabilitiesByCode(catalogCode, tenantID)
	if err != nil {
		return unknownActionCapability(catalogCode, platform, method, scenario)
	}
	for _, r := range rows {
		if r.Platform == platform && r.Method == method && r.Scenario == scenario {
			return r
		}
	}
	return unknownActionCapability(catalogCode, platform, method, scenario)
}

func unknownActionCapability(catalogCode, platform, method, scenario string) channeladmission.ActionCapability {
	return channeladmission.ActionCapability{
		CatalogCode: catalogCode, Platform: platform, Method: method, Scenario: scenario,
		AppBinding: "none", TenantScope: "*",
		Status:     channeladmission.StatusUnknown,
		ReasonText: "该动作/平台/方式的官方准入核验记录缺失",
		Missing:    []string{"官方对该路线的准入核验记录"},
	}
}

// actionSessionFor resolves the caller's own action session (visitor cookie
// binding) and requires the path actionId to match. sessionID empty reads the
// {sessionId} path value (start 路由);计划预览经 query 参数传入。Any mismatch
// answers the same 404 shape (anti-enumeration).
func (s *Server) actionSessionFor(w http.ResponseWriter, r *http.Request, actionID, sessionID string) (participation.ActionSession, bool) {
	if !s.requireActionDirectory(w) {
		return participation.ActionSession{}, false
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return participation.ActionSession{}, false
	}
	if sessionID == "" {
		sessionID = r.PathValue("sessionId")
	}
	view, err := s.Participation.ActionSessionStatus(r.Context(), sessionID, visitor)
	if err != nil {
		writeActionSessionError(w, err)
		return participation.ActionSession{}, false
	}
	if actionID != "" && view.Session.ActionID != actionID {
		// 会话不属于这个动作:同形 404,不泄漏会话存在性。
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return participation.ActionSession{}, false
	}
	return view.Session, true
}

// storeVersionSnapshot converts an adopted STORE content pack version into the
// v2 handoff snapshot (真实 MIME/filename;文件字节零过本服务).
func storeVersionSnapshot(v store.StoreContentPackVersion) (publishhandoff.TextInput, []publishhandoff.MediaItem) {
	text := publishhandoff.TextInput{Title: v.Title, Body: v.Body, Topics: v.Topics, StoreName: v.StoreName}
	var media []publishhandoff.MediaItem
	if v.ContentKind == contentpack.ContentKindVideo && !v.Video.IsEmpty() {
		media = append(media, publishhandoff.MediaItem{
			Kind: publishhandoff.MediaVideo, AssetID: v.Video.MediaID, SHA256: v.Video.SHA256,
			ContentType: v.Video.MimeType, Filename: "video-" + v.Video.MediaID + controlledMediaExt(v.Video.MimeType),
		})
	}
	if v.ContentKind == contentpack.ContentKindVideo && !v.Cover.IsEmpty() {
		media = append(media, publishhandoff.MediaItem{
			Kind: publishhandoff.MediaCover, AssetID: v.Cover.MediaID, SHA256: v.Cover.SHA256,
			ContentType: v.Cover.MimeType, Filename: "cover-" + v.Cover.MediaID + controlledMediaExt(v.Cover.MimeType),
		})
	}
	for _, img := range v.ImageText {
		if img.IsEmpty() {
			continue
		}
		media = append(media, publishhandoff.MediaItem{
			Kind: publishhandoff.MediaImage, AssetID: img.MediaID, SHA256: img.SHA256,
			ContentType: img.MimeType, Filename: "image-" + img.MediaID + controlledMediaExt(img.MimeType),
		})
	}
	return text, media
}

// resolveMediaPayload resolves the session's selected content into the frozen
// snapshot (3553 已采用版本校验;blocked/授权过期 = 409 拒新分发).
func (s *Server) resolveMediaPayload(w http.ResponseWriter, sess participation.ActionSession) (store.StoreContentPackVersion, publishhandoff.TextInput, []publishhandoff.MediaItem, bool) {
	if strings.TrimSpace(sess.ContentPackVersion) == "" {
		fail(w, http.StatusConflict, "content_required",
			"该动作没有已选内容；请先在内容面选择已采用版本，或改用纯链接交接")
		return store.StoreContentPackVersion{}, publishhandoff.TextInput{}, nil, false
	}
	v, err := s.St.GetStoreContentPackVersion(sess.ContentPackVersion, sess.TenantID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "selected content not found")
		return store.StoreContentPackVersion{}, publishhandoff.TextInput{}, nil, false
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		if !invalidPack409(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
		}
		return store.StoreContentPackVersion{}, publishhandoff.TextInput{}, nil, false
	}
	text, media := storeVersionSnapshot(v)
	return v, text, media, true
}

// resolveShareURL builds the link_share target from the PUBLISHED action's
// TargetRef (4584 已发布动作;公网 https 已在其可见性门校验)。无目标 = 422
// target_missing(绝不伪建链接)。
func (s *Server) resolveShareURL(w http.ResponseWriter, actionID string) (string, bool) {
	tenantID, storeID, err := s.St.StoreActionScope(actionID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return "", false
	}
	a, err := s.St.GetStoreActionByID(tenantID, storeID, actionID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return "", false
	}
	target := strings.TrimSpace(a.TargetRef)
	if !strings.HasPrefix(target, "https://") {
		fail(w, http.StatusUnprocessableEntity, "target_missing",
			"该动作没有可分享的已发布目标；纯链接交接不伪造链接")
		return "", false
	}
	return target, true
}

// actionMediaPurpose picks the 3553 purpose by plan kind: automated handoffs
// serve platform fetch; manual export serves customer download.
func actionMediaPurpose(planKind string) string {
	if planKind == publishhandoff.PlanManualOnly {
		return contentpack.MediaPurposeDownload
	}
	return contentpack.MediaPurposePlatformFetch
}

// mintActionMediaRefs mints fresh short-TTL media-access URLs from the frozen
// snapshot (3553 claim 机制;每次铸造落审计)。
func (s *Server) mintActionMediaRefs(r *http.Request, h publishhandoff.ActionHandoff) []map[string]any {
	if h.PayloadKind != publishhandoff.PayloadKindMedia || h.ContentPackVersionID == "" {
		return []map[string]any{}
	}
	_, media, err := s.PublishHandoff.SnapshotOf(r.Context(), h.ID, h.TenantID)
	if err != nil {
		return []map[string]any{}
	}
	purpose := actionMediaPurpose(h.PlanKind)
	now := time.Now()
	base := strings.TrimRight(s.Cfg.PublicBaseURL, "/")
	out := make([]map[string]any, 0, len(media))
	for _, m := range media {
		claim, cerr := contentpack.NewMediaAccessClaim(h.ContentPackVersionID, m.AssetID, purpose, h.SessionID, now, 0)
		if cerr != nil {
			continue
		}
		token, terr := contentpack.MintMediaAccess(s.Cfg.InternalToken, claim)
		if terr != nil {
			continue
		}
		_ = s.St.RecordMediaAccessGrant(h.TenantID, h.ContentPackVersionID, m.AssetID, purpose, h.SessionID, claim.ExpiresAt)
		out = append(out, map[string]any{
			"kind": m.Kind, "media_id": m.AssetID, "sha256": m.SHA256,
			"content_type": m.ContentType, "filename": m.Filename,
			"url":        base + "/api/v1/public/content/media/" + token,
			"purpose":    purpose,
			"expires_at": claim.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// ---- views -------------------------------------------------------------------

func actionHandoffView(h publishhandoff.ActionHandoff) map[string]any {
	out := map[string]any{
		"handoff_id":      h.ID,
		"status":          string(h.Status),
		"evidence_source": string(h.EvidenceSource),
		"platform":        h.Platform,
		"payload_kind":    h.PayloadKind,
		"method":          h.Method,
		"environment":     h.Environment,
		"plan_kind":       h.PlanKind,
		"evidence_level":  h.EvidenceLevel,
		"query_count":     h.QueryCount,
		"timed_out":       h.TimedOut,
		"created_at":      h.CreatedAt,
		"updated_at":      h.UpdatedAt,
		"content_version": h.ContentVersion,
		"action_revision": h.ActionRevision,
		"catalog_code":    h.CatalogCode,
	}
	if h.ParticipationID != "" {
		out["participation_id"] = h.ParticipationID
	}
	if h.ShareURL != "" {
		out["share_url"] = h.ShareURL
	}
	if h.ClientAction != nil {
		out["client_action"] = clientActionView(*h.ClientAction)
	}
	out["unsupported_fields"] = orEmptySlice(h.UnsupportedFields)
	return out
}

func clientActionView(c publishhandoff.ClientAction) map[string]any {
	out := map[string]any{"type": c.Type}
	if c.URL != "" {
		out["url"] = c.URL
	}
	if c.MiniappPath != "" {
		out["miniapp_path"] = c.MiniappPath
	}
	if len(c.Params) > 0 {
		out["params"] = c.Params
	}
	if c.Caption != "" {
		out["caption"] = c.Caption
	}
	if len(c.Steps) > 0 {
		out["steps"] = c.Steps
	}
	if c.MediaNote != "" {
		out["media_note"] = c.MediaNote
	}
	if c.Note != "" {
		out["note"] = c.Note
	}
	return out
}

func actionPlanView(p publishhandoff.ActionPlan) map[string]any {
	out := map[string]any{
		"kind":             p.Kind,
		"allowed":          p.Allowed,
		"pending_external": p.PendingExternal,
		"evidence_level":   p.EvidenceLevel,
		"reason":           p.Reason,
		"reason_text":      p.ReasonText,
		"missing":          orEmptySlice(p.Missing),
		"notice":           p.Notice,
	}
	if p.ClientAction != nil {
		out["client_action"] = clientActionView(*p.ClientAction)
	}
	out["unsupported_fields"] = orEmptySlice(p.UnsupportedFields)
	return out
}

// writeActionHandoffError maps v2 domain errors onto honest HTTP codes.
func (s *Server) writeActionHandoffError(w http.ResponseWriter, err error) {
	var refused *publishhandoff.HandoffRefusedError
	switch {
	case errors.As(err, &refused):
		payload := map[string]any{
			"error": "handoff_refused", "reason": refused.Reason,
			"reason_text": refused.ReasonText, "notice": refused.Notice,
			"missing":          orEmptySlice(refused.Missing),
			"pending_external": refused.PendingExternal,
		}
		writeJSON(w, http.StatusUnprocessableEntity, payload)
	case errors.Is(err, publishhandoff.ErrIdempotencyConflict):
		fail(w, http.StatusConflict, "idempotency_conflict",
			"this idempotency key was already used with a different payload")
	case errors.Is(err, publishhandoff.ErrPayloadMismatch):
		fail(w, http.StatusUnprocessableEntity, "payload_mismatch",
			"payload_kind does not match this route's required inputs")
	case errors.Is(err, publishhandoff.ErrConfirmationMismatch):
		fail(w, http.StatusUnprocessableEntity, "confirmation_mismatch",
			"确认的内容版本与当前快照不符；换内容后需要重新确认")
	case errors.Is(err, publishhandoff.ErrAttemptNotFrozen):
		fail(w, http.StatusUnprocessableEntity, "attempt_not_frozen", "the publish attempt is not frozen for handoff")
	case errors.Is(err, publishhandoff.ErrEventConflict):
		fail(w, http.StatusConflict, "idempotency_conflict",
			"this source+event_id was already recorded with a different kind")
	case errors.Is(err, publishhandoff.ErrUnsupported):
		fail(w, http.StatusUnprocessableEntity, "unsupported_by_adapter",
			"该平台适配器未提供此能力(诚实缺位,不伪造成功)")
	case errors.Is(err, publishhandoff.ErrReceiptUnverified):
		fail(w, http.StatusUnprocessableEntity, "receipt_unverified",
			"callback signature could not be verified; nothing was recorded as confirmed")
	case errors.Is(err, publishhandoff.ErrInvalidState):
		fail(w, http.StatusConflict, "invalid_state", "this handoff state does not allow the action")
	case errors.Is(err, publishhandoff.ErrBadInput):
		fail(w, http.StatusBadRequest, "bad_request", "invalid handoff input")
	case errors.Is(err, publishhandoff.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "publish handoff not found")
	default:
		fail(w, http.StatusInternalServerError, "internal", "publish handoff action failed")
	}
}

// ---- 计划预览 ---------------------------------------------------------------

// GET /api/v1/public/actions/{id}/handoffs/plan?method=&environment=&payload_kind=
// ({id} 与 capabilities 面同一通配名——ServeMux 同段通配名必须一致)
// 零落行;确认令牌(confirmation_required)与 start 同一函数派生。
func (s *Server) handlePublicActionHandoffPlan(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	actionID := r.PathValue("id")
	sess, ok := s.actionSessionFor(w, r, actionID, strings.TrimSpace(r.URL.Query().Get("action_session_id")))
	if !ok {
		return
	}
	method := strings.TrimSpace(r.URL.Query().Get("method"))
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	payloadKind := strings.TrimSpace(r.URL.Query().Get("payload_kind"))
	if method == "" || environment == "" {
		fail(w, http.StatusBadRequest, "param_required", "method and environment query parameters are required")
		return
	}
	if !isAdmissionEnvironment(environment) {
		fail(w, http.StatusBadRequest, "param_invalid",
			"environment must be one of desktop_browser|ios_safari|android_browser|ios_wechat|android_wechat|target_app")
		return
	}
	if payloadKind != "" && payloadKind != publishhandoff.PayloadKindMedia && payloadKind != publishhandoff.PayloadKindLinkShare {
		fail(w, http.StatusBadRequest, "param_invalid", "payload_kind must be media|link_share")
		return
	}
	scenario := scenarioForKind(sess.Kind)
	rec := s.resolveActionCapability(sess.TenantID, sess.CatalogCode, sess.Platform, method, scenario)
	q := s.actionGateQuery(rec, environment)
	payload, pok := s.actionPlanPayload(w, sess, payloadKind, actionID)
	if !pok {
		return
	}
	plan := publishhandoff.JudgeActionPlan(q, rec, payload)
	out := map[string]any{
		"action_id":   actionID,
		"method":      method,
		"environment": environment,
		"plan":        actionPlanView(plan),
	}
	if payloadKind != "" && plan.Allowed {
		if token, terr := publishhandoff.ConfirmationFor(payloadKind, payload); terr == nil {
			out["confirmation_required"] = token
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// actionGateQuery assembles the SERVER-DERIVED gate facts (3535 合同 §6 指令①):
// RewardPlay from the record scenario; MerchantAppRef from deployment config.
// 客户端 reward/binding 标记永不参与。
func (s *Server) actionGateQuery(rec channeladmission.ActionCapability, environment string) channeladmission.ActionQuery {
	return channeladmission.ActionQuery{
		Now:            time.Now().UTC(),
		TenantID:       rec.TenantScope,
		Environment:    environment,
		RewardPlay:     rec.Scenario == channeladmission.ActionScenarioRewardTask,
		MerchantAppRef: s.Cfg.PublishHandoffMerchantAppRef,
	}
}

// actionPlanPayload resolves the plan payload for the requested payload_kind:
// media = the session's adopted snapshot; link_share = the published target.
func (s *Server) actionPlanPayload(w http.ResponseWriter, sess participation.ActionSession, payloadKind, actionID string) (publishhandoff.ActionPlanInput, bool) {
	switch payloadKind {
	case "":
		return publishhandoff.ActionPlanInput{}, true
	case publishhandoff.PayloadKindMedia:
		_, text, media, ok := s.resolveMediaPayload(w, sess)
		if !ok {
			return publishhandoff.ActionPlanInput{}, false
		}
		return publishhandoff.ActionPlanInput{Text: text, Media: media}, true
	case publishhandoff.PayloadKindLinkShare:
		share, ok := s.resolveShareURL(w, actionID)
		if !ok {
			return publishhandoff.ActionPlanInput{}, false
		}
		return publishhandoff.ActionPlanInput{ShareURL: share}, true
	default:
		return publishhandoff.ActionPlanInput{}, true
	}
}

// ---- 开交接 ------------------------------------------------------------------

// POST /api/v1/public/actions/{actionId}/handoffs
func (s *Server) handlePublicActionHandoffStart(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	actionID := r.PathValue("actionId")
	var body struct {
		ActionSessionID string `json:"action_session_id"`
		ActionRevision  *int   `json:"action_revision"`
		PayloadKind     string `json:"payload_kind"`
		Method          string `json:"method"`
		Environment     string `json:"environment"`
		Confirmation    string `json:"confirmation"`
		IdempotencyKey  string `json:"idempotency_key"`
		ParticipationID string `json:"participation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	body.ActionSessionID = strings.TrimSpace(body.ActionSessionID)
	body.PayloadKind = strings.TrimSpace(body.PayloadKind)
	body.Method = strings.TrimSpace(body.Method)
	body.Environment = strings.TrimSpace(body.Environment)
	body.Confirmation = strings.TrimSpace(body.Confirmation)
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	body.ParticipationID = strings.TrimSpace(body.ParticipationID)
	if body.ActionSessionID == "" || body.ActionRevision == nil || body.PayloadKind == "" ||
		body.Method == "" || body.Environment == "" || body.Confirmation == "" || body.IdempotencyKey == "" {
		fail(w, http.StatusBadRequest, "param_required",
			"action_session_id, action_revision, payload_kind, method, environment, confirmation and idempotency_key are required")
		return
	}
	if body.PayloadKind != publishhandoff.PayloadKindMedia && body.PayloadKind != publishhandoff.PayloadKindLinkShare {
		fail(w, http.StatusBadRequest, "param_invalid", "payload_kind must be media|link_share")
		return
	}
	if !isAdmissionEnvironment(body.Environment) {
		fail(w, http.StatusBadRequest, "param_invalid",
			"environment must be one of desktop_browser|ios_safari|android_browser|ios_wechat|android_wechat|target_app")
		return
	}
	// 会话权威:path 动作必须等于 body 会话,且会话属于本 visitor。
	r.SetPathValue("sessionId", body.ActionSessionID)
	sess, ok := s.actionSessionFor(w, r, actionID, body.ActionSessionID)
	if !ok {
		return
	}
	if body.ActionRevision != nil && *body.ActionRevision != sess.ActionRevision {
		// 3546 同码:期望修订与冻结修订不符 → 刷新后重试。
		fail(w, http.StatusConflict, "stale_version", "your expected action revision is out of date; reload and retry")
		return
	}
	scenario := scenarioForKind(sess.Kind)
	rec := s.resolveActionCapability(sess.TenantID, sess.CatalogCode, sess.Platform, body.Method, scenario)
	q := s.actionGateQuery(rec, body.Environment)

	in := publishhandoff.ActionStartInput{
		TenantID: sess.TenantID, StoreID: sess.StoreID,
		ActionID: sess.ActionID, CatalogCode: sess.CatalogCode, Platform: sess.Platform,
		SessionID: sess.ID, VisitorID: mustVisitor(r),
		ActionRevision: *body.ActionRevision,
		PayloadKind:    body.PayloadKind, Method: body.Method, Environment: body.Environment,
		Scenario:        scenario,
		ParticipationID: body.ParticipationID,
		IdempotencyKey:  body.IdempotencyKey,
		Confirmation:    body.Confirmation,
		Record:          rec,
		Query:           q,
	}
	switch body.PayloadKind {
	case publishhandoff.PayloadKindMedia:
		v, text, media, mok := s.resolveMediaPayload(w, sess)
		if !mok {
			return
		}
		in.ContentPackVersionID = v.ID
		in.PlanPayload = publishhandoff.ActionPlanInput{Text: text, Media: media}
	case publishhandoff.PayloadKindLinkShare:
		share, sok := s.resolveShareURL(w, actionID)
		if !sok {
			return
		}
		in.PlanPayload = publishhandoff.ActionPlanInput{ShareURL: share}
	}
	res, err := s.PublishHandoff.StartAction(r.Context(), in)
	if err != nil {
		s.writeActionHandoffError(w, err)
		return
	}
	resp := map[string]any{
		"handoff": actionHandoffView(res.Handoff),
		"plan":    actionPlanView(res.Plan),
		"media":   s.mintActionMediaRefs(r, res.Handoff),
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, resp)
}

// ---- 查态 / facts / submit / query / cancel -----------------------------------

// GET /api/v1/public/handoffs/{handoffId}
func (s *Server) handlePublicActionHandoffGet(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownActionHandoff(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff": actionHandoffView(h),
		"plan":    actionPlanView(s.PublishHandoff.FrozenPlanOf(h)),
		"media":   s.mintActionMediaRefs(r, h),
	})
}

func (s *Server) ownActionHandoff(w http.ResponseWriter, r *http.Request) (publishhandoff.ActionHandoff, bool) {
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return publishhandoff.ActionHandoff{}, false
	}
	h, err := s.PublishHandoff.GetActionForVisitor(r.Context(), r.PathValue("id"), visitor)
	if err != nil {
		s.writeActionHandoffError(w, err)
		return publishhandoff.ActionHandoff{}, false
	}
	return h, true
}

// POST /api/v1/public/handoffs/{handoffId}/facts — 弱证据层落事件(点击/唤起/
// 媒体交接/自报)。事件不改状态、不触发奖励;同 source+event_id 幂等。
func (s *Server) handlePublicActionHandoffFact(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	var body struct {
		Kind    string         `json:"kind"`
		Source  string         `json:"source"`
		EventID string         `json:"event_id"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	body.Kind = strings.TrimSpace(body.Kind)
	body.Source = strings.TrimSpace(body.Source)
	body.EventID = strings.TrimSpace(body.EventID)
	if body.Kind == "" || body.Source == "" || body.EventID == "" {
		fail(w, http.StatusBadRequest, "param_required", "kind, source and event_id are required")
		return
	}
	h, ok := s.ownActionHandoff(w, r)
	if !ok {
		return
	}
	next, deduped, err := s.PublishHandoff.RecordActionFact(r.Context(), h.ID, mustVisitor(r), body.Kind, body.Source, body.EventID, body.Payload)
	if err != nil {
		s.writeActionHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff": actionHandoffView(next),
		"deduped": deduped,
		"notice":  "已记录；点击/唤起/素材交接/自报都不是发布成功，也不触发奖励。",
	})
}

// POST /api/v1/public/handoffs/{handoffId}/submit — 经 adapter.Submit
// (unsupported = 422 诚实缺位;成功 → submitting)。
func (s *Server) handlePublicActionHandoffSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownActionHandoff(w, r)
	if !ok {
		return
	}
	next, err := s.PublishHandoff.SubmitAction(r.Context(), h.ID, mustVisitor(r))
	if err != nil {
		s.writeActionHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": actionHandoffView(next)})
}

// POST /api/v1/public/handoffs/{handoffId}/query — 先回查,不重投
// (unsupported = 422;结果照录不确认)。
func (s *Server) handlePublicActionHandoffQuery(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownActionHandoff(w, r)
	if !ok {
		return
	}
	next, err := s.PublishHandoff.QueryAction(r.Context(), h.ID, mustVisitor(r))
	if err != nil {
		s.writeActionHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": actionHandoffView(next)})
}

// POST /api/v1/public/handoffs/{handoffId}/cancel — 顾客取消(终态)。
func (s *Server) handlePublicActionHandoffCancel(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownActionHandoff(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	next, err := s.PublishHandoff.CancelAction(r.Context(), h.ID, mustVisitor(r), body.Reason)
	if err != nil {
		s.writeActionHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": actionHandoffView(next)})
}

// ---- 官方回调 -----------------------------------------------------------------

// POST /api/v1/public/handoff-callbacks/{platform} — 服务端到服务端,无会话。
// 验签完全在 adapter.VerifyCallback(平台各协议);管线只消费已验签回执。
// 未注册平台/验签失败一律拒绝(NOT_VERIFIED 残差,绝不入库为确认)。
func (s *Server) handlePublicHandoffCallback(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	platform := strings.TrimSpace(r.PathValue("platform"))
	if platform == "" {
		fail(w, http.StatusBadRequest, "param_required", "platform path parameter is required")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "callback body must be readable")
		return
	}
	headers := map[string]string{}
	for k := range r.Header {
		headers[strings.ToLower(k)] = r.Header.Get(k)
	}
	h, err := s.PublishHandoff.ApplyPlatformCallback(r.Context(), platform, publishhandoff.CallbackPayload{
		Platform: platform, Headers: headers, Body: raw,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"accepted": true, "status": string(h.Status),
		})
	case errors.Is(err, publishhandoff.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "publish handoff not found")
	case errors.Is(err, publishhandoff.ErrReceiptUnverified):
		// 验签失败:422 停止重试,什么都不确认为真。
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "receipt_unverified",
			"message": "callback signature could not be verified; nothing was recorded as confirmed",
		})
	default:
		// 已验真但被拒(绑定/乱序/结果不符):照录拒绝事件,200 停止重试。
		writeJSON(w, http.StatusOK, map[string]any{
			"accepted": false, "reason": err.Error(),
		})
	}
}
