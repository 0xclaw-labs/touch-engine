// handlers_publish_handoff.go: HUI-1670 顾客发布交接的 HTTP 面。
//
// 纪律:
//   - 开关:FEATURE_PUBLISH_HANDOFF(默认 off)= 路由不注册(server.go 条件
//     注册)且本 gate 再答统一 404 —— 双保险,与 FEATURE_CHANNEL_ADMISSION
//     同款写法;
//   - 顾客面(/api/v1/public/links/{code}/publish-handoff/**)无会话概念:
//     visitor cookie 只做同浏览器续接;交接必须先过 publishhandoff 内的
//     channeladmission.Gate(服务端单点,前端隐藏按钮不算数);换稿/换平台
//     =新 attempt+重新确认,不静默换内容;
//   - 店员事实面(/api/v1/publish-handoffs/{id}/staff-events)走 requireSession
//     + authz 门店作用域:只记录人工事实(见提交/核对作品),永不转官方;
//   - 官方回执回调(/api/v1/public/publish-receipts/{id})服务端验签+五绑定
//     +重放去重;生产密钥未配置时验签一律拒绝(NOT_VERIFIED 残差);
//   - 白名单响应:内部 token/密钥/幂等键原文不出现在任何响应;凭据明文只在
//     创建响应出现一次。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// publishHandoffGate answers uniform 404 when the surface is off.
func (s *Server) publishHandoffGate(w http.ResponseWriter) bool {
	if s.Cfg.FeaturePublishHandoff && s.PublishHandoff != nil {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// publishRecordOrUnknown resolves one admission record for the handoff's
// route; unregistered routes synthesize the unknown rule (fail-closed,
// 与 handlePublicChannelAdmission 同语义,不泄露库内存在性).
func (s *Server) publishRecordOrUnknown(platform, entryMode, scenario, tenantID string) channeladmission.Record {
	rec, err := s.St.GetChannelAdmission(platform, entryMode, scenario, tenantID)
	if err == nil {
		return rec
	}
	return channeladmission.Record{
		Platform: platform, EntryMode: entryMode, Scenario: scenario,
		Status:   channeladmission.StatusUnknown,
		Missing:  []string{"该平台/入口/场景的官方准入核验记录"},
	}
}

func handoffView(h publishhandoff.Handoff) map[string]any {
	return map[string]any{
		"id": h.ID, "status": string(h.Status), "evidence_source": string(h.EvidenceSource),
		"platform": h.Platform, "entry_mode": h.EntryMode, "scenario": h.Scenario,
		"participation_id": h.ParticipationID, "attempt_id": h.AttemptID,
		"content_version": h.ContentVersion, "query_count": h.QueryCount,
		"timed_out": h.TimedOut, "created_at": h.CreatedAt, "updated_at": h.UpdatedAt,
	}
}

func planView(p publishhandoff.Plan) map[string]any {
	out := map[string]any{
		"kind": p.Kind, "allowed": p.Allowed, "reason": p.Reason,
		"reason_text": p.ReasonText, "notice": p.Notice, "missing": p.Missing,
	}
	if p.Entry != nil {
		out["entry"] = map[string]any{
			"method": p.Entry.Method, "url": p.Entry.URL,
			"miniapp_path": p.Entry.MiniappPath, "params": p.Entry.Params,
			"unsupported": p.Entry.Unsupported,
		}
	}
	if p.Manual != nil {
		out["manual"] = map[string]any{
			"steps": p.Manual.Steps, "export_caption": p.Manual.ExportCaption,
			"media_note": p.Manual.MediaNote,
		}
	}
	return out
}

func mediaView(refs []publishhandoff.MediaRef) []map[string]any {
	out := make([]map[string]any, 0, len(refs))
	for _, m := range refs {
		out = append(out, map[string]any{
			"kind": m.Kind, "asset_id": m.AssetID, "url": m.URL, "sha256": m.SHA256,
			"content_type": m.ContentType, "filename": m.Filename, "expires_at": m.ExpiresAt,
		})
	}
	return out
}

// versionSnapshot converts an adopted contentpack version into the handoff
// input snapshot (媒体+文案;文件字节零过本服务).
func versionSnapshot(v store.ContentPackVersion) (publishhandoff.TextInput, []publishhandoff.MediaItem) {
	text := publishhandoff.TextInput{
		Title: v.Title, Body: v.Body, Topics: v.Topics, StoreName: v.StoreName,
	}
	var media []publishhandoff.MediaItem
	shaOf := func(a contentpack.AssetSnapshot) string { return a.SHA256 }
	if v.ContentKind == contentpack.ContentKindVideo {
		if !v.Video.IsEmpty() {
			media = append(media, publishhandoff.MediaItem{
				Kind: publishhandoff.MediaVideo, AssetID: v.Video.AssetID, SHA256: shaOf(v.Video),
				ContentType: "video/mp4", Filename: "video-" + v.Video.AssetID + ".mp4",
			})
		}
		if !v.Cover.IsEmpty() {
			media = append(media, publishhandoff.MediaItem{
				Kind: publishhandoff.MediaCover, AssetID: v.Cover.AssetID, SHA256: shaOf(v.Cover),
				ContentType: "image/jpeg", Filename: "cover-" + v.Cover.AssetID + ".jpg",
			})
		}
	}
	for i, img := range v.ImageText {
		media = append(media, publishhandoff.MediaItem{
			Kind: publishhandoff.MediaImage, AssetID: img.AssetID, SHA256: shaOf(img),
			ContentType: "image/jpeg", Filename: "image-" + img.AssetID + "-" + strconv.Itoa(i) + ".jpg",
		})
	}
	return text, media
}

// ---- GET routes: per-platform honest plan surface (preview/确认前的能力解释) ----

// GET /api/v1/public/links/{code}/publish-handoff/routes?platform=&scenario=
// 可选 pack_id:附带内容快照指纹(顾客确认面),不附带时只给路线裁决。
func (s *Server) handlePublicPublishHandoffRoutes(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	scenario := strings.TrimSpace(r.URL.Query().Get("scenario"))
	if scenario == "" {
		scenario = "store_promo"
	}
	if platform == "" {
		fail(w, http.StatusBadRequest, "param_required", "platform query parameter is required")
		return
	}
	tenantID := res.Campaign.TenantID
	records, err := s.St.ListChannelAdmissions(tenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel admission lookup failed")
		return
	}
	plans := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		if rec.Platform != platform || rec.Scenario != scenario {
			continue
		}
		p := publishhandoff.PlanRoute(publishhandoff.PlanInput{
			TenantID: tenantID, Platform: platform, EntryMode: rec.EntryMode, Scenario: scenario,
			Now:             time.Now().UTC(),
			DeepLinkBase:    s.Cfg.PublishHandoffH5EntryBase,
			MiniappBase:     s.Cfg.PublishHandoffMiniappEntryBase,
		}, rec)
		plans = append(plans, map[string]any{
			"entry_mode": rec.EntryMode, "scenario": rec.Scenario, "plan": planView(p),
		})
	}
	out := map[string]any{"platform": platform, "scenario": scenario, "routes": plans}
	if len(plans) == 0 {
		out["notice"] = "该平台暂无准入记录，按未知规则处理：不默认允许，可使用合规导出+手动发布指引。"
	}
	// 可选内容指纹(顾客确认面):与交接时同一指纹函数,换稿=指纹变化。
	if packID := strings.TrimSpace(r.URL.Query().Get("pack_id")); packID != "" {
		v, err := s.St.GetAdoptedVersion(tenantID, res.Campaign.ID, platform)
		if err != nil || v.PackID != packID {
			fail(w, http.StatusNotFound, "not_found", "no adopted content for this pack/platform")
			return
		}
		if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
			if !invalidPack409(w, err) {
				fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
			}
			return
		}
		text, media := versionSnapshot(v)
		out["snapshot"] = map[string]any{
			"pack_id": v.PackID, "version_id": v.ID, "content_kind": string(v.ContentKind),
			"title": text.Title, "body": text.Body, "topics": text.Topics,
			"store_name": text.StoreName,
			"content_version": publishhandoff.ContentFingerprint(text, media),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- POST start -------------------------------------------------------------------

// POST /api/v1/public/links/{code}/publish-handoff
func (s *Server) handlePublicPublishHandoffStart(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	var body struct {
		ParticipationID         string `json:"participation_id"`
		AttemptID               string `json:"attempt_id"`
		Platform                string `json:"platform"`
		EntryMode               string `json:"entry_mode"`
		Scenario                string `json:"scenario"`
		PackID                  string `json:"pack_id"`
		IdempotencyKey          string `json:"idempotency_key"`
		ConfirmedContentVersion string `json:"confirmed_content_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	body.Platform = strings.TrimSpace(body.Platform)
	body.Scenario = strings.TrimSpace(body.Scenario)
	if body.Scenario == "" {
		body.Scenario = "store_promo"
	}
	if body.Platform == "" || strings.TrimSpace(body.ParticipationID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "platform and participation_id are required")
		return
	}
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	visitor := s.ensureVisitor(w, r)
	if visitor == "" {
		fail(w, http.StatusInternalServerError, "internal", "visitor key unavailable")
		return
	}
	p, err := s.Participation.GetForVisitor(r.Context(), strings.TrimSpace(body.ParticipationID), visitor)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	tenantID := p.TenantID

	// 1) 渠道裁决先行(拒绝路径零内容探测:不给未准入路线任何信息面)。
	entryMode := strings.TrimSpace(body.EntryMode)
	if entryMode == "" {
		entryMode = s.pickEntryMode(tenantID, body.Platform, body.Scenario)
	}
	record := s.publishRecordOrUnknown(body.Platform, entryMode, body.Scenario, tenantID)
	if plan := publishhandoff.PlanRoute(publishhandoff.PlanInput{
		TenantID: tenantID, Platform: body.Platform, EntryMode: entryMode, Scenario: body.Scenario,
		Now:         time.Now().UTC(),
		DeepLinkBase: s.Cfg.PublishHandoffH5EntryBase, MiniappBase: s.Cfg.PublishHandoffMiniappEntryBase,
	}, record); plan.Kind == publishhandoff.PlanRefused {
		s.writePublishHandoffError(w, &publishhandoff.HandoffRefusedError{
			Reason: plan.Reason, ReasonText: plan.ReasonText, Notice: plan.Notice, Missing: plan.Missing,
		})
		return
	}

	// 2) 已采用内容快照(只读;blocked/授权过期=409 拒新分发)。
	v, err := s.St.GetAdoptedVersion(tenantID, res.Campaign.ID, body.Platform)
	if err != nil || (strings.TrimSpace(body.PackID) != "" && v.PackID != strings.TrimSpace(body.PackID)) {
		fail(w, http.StatusNotFound, "not_found", "no adopted content for this platform")
		return
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		if !invalidPack409(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
		}
		return
	}
	text, media := versionSnapshot(v)

	// 3) 顾客确认:确认指纹必须就是本次要交出的快照(换稿需重新确认)。
	fingerprint := publishhandoff.ContentFingerprint(text, media)
	if strings.TrimSpace(body.ConfirmedContentVersion) == "" {
		fail(w, http.StatusUnprocessableEntity, "confirmation_required",
			"顾客必须先确认本次内容(confirmed_content_version 缺失)")
		return
	}
	if body.ConfirmedContentVersion != fingerprint {
		fail(w, http.StatusUnprocessableEntity, "confirmation_mismatch",
			"确认的内容版本与当前快照不符；换稿后需要重新确认，不会静默更新你要发出的内容")
		return
	}

	// 4) 参与尝试:给定 attempt 必须可交接;未给定则同平台补建(3546 语义:
	//    交接时冻结内容与规则版本,并铸造短时单用途恢复凭据)。
	attemptID, recovery, herr := s.prepareAttempt(r, p, visitor, body.Platform, entryMode, strings.TrimSpace(body.AttemptID))
	if herr != "" {
		fail(w, http.StatusUnprocessableEntity, "invalid_state", herr)
		return
	}

	// 5) 幂等键:显式优先;缺省按 参与+平台 派生(同参与换平台=新交接)。
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = "ph:" + p.ID + ":" + body.Platform
	}

	out, err := s.PublishHandoff.Start(r.Context(), publishhandoff.StartInput{
		TenantID: tenantID, StoreID: p.StoreID, CampaignID: p.CampaignID, LinkCode: p.LinkCode,
		ParticipationID: p.ID, AttemptID: attemptID, VisitorID: visitor,
		Platform: body.Platform, EntryMode: entryMode, Scenario: body.Scenario,
		IdempotencyKey:          idem,
		ConfirmedContentVersion: fingerprint,
		ContentPackVersionID:    v.ID,
		H5Base:                  s.Cfg.PublishHandoffH5EntryBase,
		MiniappBase:             s.Cfg.PublishHandoffMiniappEntryBase,
		Text:                    text, Media: media,
	}, record)
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	resp := map[string]any{
		"handoff": handoffView(out.Handoff),
		"plan":    planView(out.Plan),
		"media":   mediaView(out.Media),
	}
	if recovery != nil {
		// 凭据明文只在此响应出现一次(离开前的恢复方式;码≠发奖授权)。
		resp["recovery"] = recovery
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, resp)
}

// pickEntryMode picks the least-refused registered entry mode for the route
// (admitted wins; anything else keeps its honest order).
func (s *Server) pickEntryMode(tenantID, platform, scenario string) string {
	records, err := s.St.ListChannelAdmissions(tenantID)
	if err != nil {
		return channeladmission.EntryManual
	}
	fallback := ""
	for _, rec := range records {
		if rec.Platform != platform || rec.Scenario != scenario {
			continue
		}
		if rec.Status == channeladmission.StatusAdmitted {
			return rec.EntryMode
		}
		if fallback == "" {
			fallback = rec.EntryMode
		}
	}
	if fallback == "" {
		fallback = channeladmission.EntryManual
	}
	return fallback
}

// prepareAttempt resolves-or-creates the 3546 attempt and freezes it at
// handoff (BeginHandoff: 内容+规则版本冻结 + 短时单用途恢复凭据).
func (s *Server) prepareAttempt(r *http.Request, p participation.Participation, visitor, platform, entryMode, attemptID string) (string, map[string]any, string) {
	method := participation.MethodH5
	if entryMode == channeladmission.EntryMiniapp {
		method = participation.MethodPlatformMini
	}
	attempts, err := s.Participation.ListAttempts(r.Context(), p.ID, visitor)
	if err != nil {
		return "", nil, "participation lookup failed"
	}
	var chosen *participation.Attempt
	for i := range attempts {
		a := attempts[i]
		if attemptID != "" {
			if a.ID == attemptID {
				chosen = &attempts[i]
				break
			}
			continue
		}
		// 未指定:同平台可交接的尝试优先复用(prepared 补冻结,handed_off 直用)。
		if a.Platform == platform && (a.Status == participation.AttemptPrepared || a.Status == participation.AttemptHandedOff) {
			chosen = &attempts[i]
			break
		}
	}
	if chosen == nil {
		if attemptID != "" {
			return "", nil, "this attempt is not available for handoff"
		}
		a, err := s.Participation.CreateAttempt(r.Context(), participation.AttemptInput{
			ParticipationID: p.ID, VisitorID: visitor, Platform: platform, Method: method,
		})
		if err != nil {
			return "", nil, "attempt creation failed"
		}
		chosen = &a
	}
	switch chosen.Status {
	case participation.AttemptPrepared:
		tok, err := s.Participation.BeginHandoff(r.Context(), participation.HandoffInput{
			ParticipationID: p.ID, VisitorID: visitor, AttemptID: chosen.ID,
			Purpose: participation.PurposeCustomerResume, PublicBaseURL: s.Cfg.PublicBaseURL,
		})
		if err != nil {
			return "", nil, "attempt freeze failed"
		}
		rec := map[string]any{
			"credential_id": tok.Credential.ID, "token": tok.Token,
			"expires_at": tok.Credential.ExpiresAt,
		}
		if tok.QRPayload != "" {
			rec["qr_payload"] = tok.QRPayload
		}
		return chosen.ID, rec, ""
	case participation.AttemptHandedOff:
		return chosen.ID, nil, "" // 已冻结(重放/复用):恢复方式走 3546 lookup/redeem
	default:
		return "", nil, "this attempt is not available for handoff"
	}
}

// writePublishHandoffError maps domain errors onto honest HTTP codes.
func (s *Server) writePublishHandoffError(w http.ResponseWriter, err error) {
	var refused *publishhandoff.HandoffRefusedError
	switch {
	case errors.As(err, &refused):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "handoff_refused", "reason": refused.Reason,
			"reason_text": refused.ReasonText, "notice": refused.Notice, "missing": refused.Missing,
		})
	case errors.Is(err, publishhandoff.ErrAttemptNotFrozen):
		fail(w, http.StatusUnprocessableEntity, "attempt_not_frozen", "the publish attempt is not frozen for handoff")
	case errors.Is(err, publishhandoff.ErrConfirmationMismatch):
		fail(w, http.StatusUnprocessableEntity, "confirmation_mismatch", "confirmed content version does not match the frozen snapshot")
	case errors.Is(err, publishhandoff.ErrMediaPreparation):
		fail(w, http.StatusUnprocessableEntity, "media_preparation_failed", "media preparation failed; no paid generation was triggered")
	case errors.Is(err, publishhandoff.ErrBadInput):
		fail(w, http.StatusBadRequest, "bad_request", "invalid handoff input")
	case errors.Is(err, publishhandoff.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "publish handoff not found")
	case errors.Is(err, publishhandoff.ErrEvidenceRequired):
		fail(w, http.StatusUnprocessableEntity, "evidence_required",
			"窗口失焦、点击或自报不能证明平台已打开；需要可验证证据")
	case errors.Is(err, publishhandoff.ErrQueryRequired):
		fail(w, http.StatusConflict, "query_required", "timeout: query the same handoff first; no resend")
	default:
		fail(w, http.StatusInternalServerError, "internal", "publish handoff action failed")
	}
}

// ---- GET / cancel / done / editor-opened (顾客自有) ----------------------------

func (s *Server) ownHandoff(w http.ResponseWriter, r *http.Request) (publishhandoff.Handoff, bool) {
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return publishhandoff.Handoff{}, false
	}
	h, err := s.PublishHandoff.GetForVisitor(r.Context(), r.PathValue("id"), visitor)
	if err != nil {
		s.writePublishHandoffError(w, err)
		return publishhandoff.Handoff{}, false
	}
	return h, true
}

// GET /api/v1/public/links/{code}/publish-handoff/{id}
func (s *Server) handlePublicPublishHandoffGet(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	if _, ok := s.availablePublishLink(w, r); !ok {
		return
	}
	h, ok := s.ownHandoff(w, r)
	if !ok {
		return
	}
	record := s.publishRecordOrUnknown(h.Platform, h.EntryMode, h.Scenario, h.TenantID)
	text, _, err := s.PublishHandoff.SnapshotOf(r.Context(), h.ID, h.TenantID)
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	plan := publishhandoff.PlanRoute(publishhandoff.PlanInput{
		TenantID: h.TenantID, Platform: h.Platform, EntryMode: h.EntryMode, Scenario: h.Scenario,
		Now: time.Now().UTC(), Text: text,
		DeepLinkBase: s.Cfg.PublishHandoffH5EntryBase, MiniappBase: s.Cfg.PublishHandoffMiniappEntryBase,
	}, record)
	refs, err := s.PublishHandoff.MediaForVisitor(r.Context(), h.ID, mustVisitor(r))
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff": handoffView(h), "plan": planView(plan), "media": mediaView(refs),
	})
}

func mustVisitor(r *http.Request) string { return visitorKey(r) }

// POST .../publish-handoff/{id}/cancel
func (s *Server) handlePublicPublishHandoffCancel(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownHandoff(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	next, err := s.PublishHandoff.Cancel(r.Context(), h.ID, mustVisitor(r), body.Reason)
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": handoffView(next)})
}

// POST .../publish-handoff/{id}/done — 顾客"我已完成"=核验请求事件,不是事实。
func (s *Server) handlePublicPublishHandoffDone(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownHandoff(w, r)
	if !ok {
		return
	}
	next, err := s.PublishHandoff.CustomerDone(r.Context(), h.ID, mustVisitor(r))
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff": handoffView(next),
		"notice":  "已记录你的完成申报，等待店员核验或官方确认；申报本身不算发布成功，也不触发奖励。",
	})
}

// POST .../publish-handoff/{id}/editor-opened — 无可验证证据一律 422。
func (s *Server) handlePublicPublishHandoffEditorOpened(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	h, ok := s.ownHandoff(w, r)
	if !ok {
		return
	}
	var body struct {
		Proof string `json:"proof"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	next, err := s.PublishHandoff.MarkEditorOpened(r.Context(), h.ID, mustVisitor(r), body.Proof)
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": handoffView(next)})
}

// ---- official receipt callback (server-to-server, no session) --------------------

// POST /api/v1/public/publish-receipts/{id}
func (s *Server) handlePublicPublishReceipt(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	var wire publishhandoff.ReceiptWire
	if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be a receipt")
		return
	}
	next, err := s.PublishHandoff.ApplyReceiptByID(r.Context(), r.PathValue("id"), wire)
	if errors.Is(err, publishhandoff.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "publish handoff not found")
		return
	}
	if err != nil {
		var refused *publishhandoff.HandoffRefusedError
		if errors.As(err, &refused) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "handoff_refused", "reason": refused.Reason})
			return
		}
		// 已验真但被拒(绑定/乱序/结果不符):照录回执拒绝事件,200 停止重试。
		writeJSON(w, http.StatusOK, map[string]any{
			"accepted": false, "status": string(next.Status), "reason": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": true, "status": string(next.Status),
	})
}

// ---- staff facts -----------------------------------------------------------------

// POST /api/v1/publish-handoffs/{id}/staff-events — 人工事实两分落库。
func (s *Server) handlePublishHandoffStaffEvent(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	fact := strings.TrimSpace(body.Kind)
	if fact != publishhandoff.FactStaffSawSubmit && fact != publishhandoff.FactStaffVerifiedWork {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be staff_saw_submit or staff_verified_work")
		return
	}
	h, err := s.PublishHandoff.GetScoped(r.Context(), r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, publishhandoff.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "publish handoff not found")
		return
	}
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: h.StoreID}, true, w) {
		return
	}
	next, err := s.PublishHandoff.RecordStaffEvent(r.Context(), c.Member.TenantID, h.ID, publishhandoff.StaffActor{
		ID: c.Member.ID, Name: c.Member.PrincipalRef,
	}, fact, body.Note)
	if err != nil {
		s.writePublishHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoff": handoffView(next)})
}
