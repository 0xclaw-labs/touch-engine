package httpapi

// Action session surface (HUI-3546 增量轮 2026-11-09, B08): 匿名动作会话与跨端恢复。
//
// 面貌:顾客面(http.HandlerFunc,无商家会话概念)——首方 visitor cookie 是
// 唯一访问凭据;租户/门店从已发布动作(目录端口)解析,action_id、NFC 短码、
// 恢复码都不是后台授权。恢复凭据只走 POST 体,永不进 URL 查询参数。
//
// 纪律:
//   - 与参与面同一开关 FEATURE_PARTICIPATION:off = 路由不注册且每个 handler
//     再答统一 404(双保险);
//   - 动作目录端口未接线 = 503 action_directory_unavailable(fail-closed;
//     生产接线归 HUI-4584 动作目录,本票不建第二份目录);
//   - 载荷白名单:action_session_id/status/冻结版本/实时位/权益引用;
//     visitor 键、幂等键、规则快照原文、预留引用一律不出响应;
//   - 凭据明文只在签发响应出现一次,永不进日志(日志面是 method/path/status)。

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/participation"
)

// actionSessionsReady: 双保险中的第二道(路由未注册是第一道)。off = 统一 404。
func (s *Server) actionSessionsReady(w http.ResponseWriter) bool {
	return s.participationReady(w)
}

// requireActionDirectory: 目录未接线 → 503 fail-closed(不建行、不伪报)。
func (s *Server) requireActionDirectory(w http.ResponseWriter) bool {
	if s.Participation == nil || !s.Participation.SessionsReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "action_directory_unavailable",
			"message": "the action catalog is not wired on this deployment; refusing to act (fail-closed)",
		})
		return false
	}
	return true
}

// ---- payload views (whitelist) ------------------------------------------------

func actionSessionView(sess participation.ActionSession, resumeAvailable bool) map[string]any {
	out := map[string]any{
		"action_session_id":   sess.ID,
		"status":              string(sess.Status),
		"storefront_revision": sess.StorefrontRevision,
		"action_revision":     sess.ActionRevision,
		"rule_version":        sess.RuleVersion,
		"resume_available":    resumeAvailable,
		"created_at":          sess.CreatedAt,
	}
	if sess.ContentPackVersion != "" {
		out["selected_content_pack_version"] = sess.ContentPackVersion
	}
	if sess.ParticipationID != "" {
		out["participation_id"] = sess.ParticipationID
	}
	return out
}

// writeActionSessionError maps session-domain sentinels onto status codes.
// Unknown and not-found collapse into 404 so probing learns nothing.
func writeActionSessionError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, participation.ErrDirectoryUnavailable):
		s := http.StatusServiceUnavailable
		writeJSON(w, s, map[string]any{"error": "action_directory_unavailable", "message": "the action catalog is not wired; refusing to act (fail-closed)"})
	case errors.Is(err, participation.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "session not found")
	case errors.Is(err, participation.ErrActionUnavailable):
		fail(w, http.StatusConflict, "action_unavailable", "this action is not currently available")
	case errors.Is(err, participation.ErrStaleVersion):
		fail(w, http.StatusConflict, "stale_version", "your expected revision is out of date; reload and retry")
	case errors.Is(err, participation.ErrSessionEnded):
		fail(w, http.StatusConflict, "session_ended", "this session has ended")
	case errors.Is(err, participation.ErrCampaignRequired):
		fail(w, http.StatusUnprocessableEntity, "campaign_required", "this action has no activity to join")
	case errors.Is(err, participation.ErrCredentialExpired):
		fail(w, http.StatusGone, "resume_expired", "this resume credential has expired")
	case errors.Is(err, participation.ErrCredentialUsed):
		fail(w, http.StatusConflict, "resume_used", "this resume credential was already used")
	case errors.Is(err, participation.ErrCredentialRevoked):
		fail(w, http.StatusConflict, "resume_revoked", "this resume credential was revoked")
	case errors.Is(err, participation.ErrTargetMismatch):
		fail(w, http.StatusForbidden, "target_mismatch", "this credential does not apply here")
	case errors.Is(err, participation.ErrNotEntitled):
		fail(w, http.StatusForbidden, "forbidden", "this session is not yours")
	case errors.Is(err, participation.ErrFrozen):
		fail(w, http.StatusConflict, "frozen", "this activity window is closed")
	case errors.Is(err, participation.ErrInvalidSource), errors.Is(err, participation.ErrInvalidTransition):
		fail(w, http.StatusUnprocessableEntity, "invalid_state", "this action is not available")
	case errors.Is(err, participation.ErrIdempotencyConflict):
		fail(w, http.StatusConflict, "idempotency_conflict", "this idempotency key belongs to another session; start fresh")
	default:
		fail(w, http.StatusInternalServerError, "internal", "session action failed")
	}
}

// ---- handlers -----------------------------------------------------------------

// handleActionSessionStart: POST /api/v1/public/actions/{actionId}/sessions。
// 显式动作创建或同浏览器恢复;浏览 GET 不自动创建(没有 GET 入口)。
func (s *Server) handleActionSessionStart(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	if !s.requireActionDirectory(w) {
		return
	}
	var body struct {
		Source                     string `json:"touchpoint_source"`
		ContentPackVersion         string `json:"content_pack_version"`
		IdempotencyKey             string `json:"idempotency_key"`
		ExpectedActionRevision     int    `json:"expected_action_revision"`
		ExpectedStorefrontRevision int    `json:"expected_storefront_revision"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	visitor := s.ensureVisitor(w, r)
	if visitor == "" {
		fail(w, http.StatusInternalServerError, "internal", "visitor key unavailable")
		return
	}
	started, err := s.Participation.StartActionSession(r.Context(), participation.SessionStartInput{
		ActionID:                   r.PathValue("actionId"),
		Source:                     participation.Source(strings.TrimSpace(body.Source)),
		ContentPackVersion:         strings.TrimSpace(body.ContentPackVersion),
		IdempotencyKey:             strings.TrimSpace(body.IdempotencyKey),
		VisitorID:                  visitor,
		ExpectedActionRevision:     body.ExpectedActionRevision,
		ExpectedStorefrontRevision: body.ExpectedStorefrontRevision,
	})
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	status := http.StatusOK
	if started.Created {
		status = http.StatusCreated
	}
	// resume_available = 当前实时可用(请求刚过 liveAction)且会话未终态;
	// 同键重放可能命中已结束会话,这里如实回 false。
	writeJSON(w, status, actionSessionView(started.Session, started.Session.Status == participation.SessionActive))
}

// handleActionSessionStatus: GET /api/v1/public/action-sessions/{sessionId}/status。
// 只读:进度/待处理/权益引用 + 实时位;动作停用不拦读(旧权益保护)。
func (s *Server) handleActionSessionStatus(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	view, err := s.Participation.ActionSessionStatus(r.Context(), r.PathValue("sessionId"), visitor)
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	out := actionSessionView(view.Session, view.ResumeAvailable)
	out["action_available"] = view.ActionAvailable
	if view.Participation != nil {
		out["participation"] = map[string]any{
			"id":                        view.Participation.ID,
			"status":                    view.Participation.Status,
			"benefit_reservation_state": view.Participation.BenefitReservationState,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleActionSessionContent: POST /api/v1/public/action-sessions/{sessionId}/content。
// 预览期换选内容引用;交接后的冻结与确认上下文归发布交接域(HUI-1670)。
func (s *Server) handleActionSessionContent(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	var body struct {
		ContentPackVersion string `json:"content_pack_version"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	sess, err := s.Participation.UpdateActionSessionContent(r.Context(),
		r.PathValue("sessionId"), visitor, strings.TrimSpace(body.ContentPackVersion))
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, actionSessionView(sess, true))
}

// handleActionSessionAttach: POST /api/v1/public/action-sessions/{sessionId}/participations。
// 显式办理权益才升级建 participation(既有参与语义);无活动拒绝,不伪造活动 id。
func (s *Server) handleActionSessionAttach(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	var body struct {
		Source string `json:"touchpoint_source"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	res, err := s.Participation.AttachParticipation(r.Context(), participation.AttachInput{
		SessionID: r.PathValue("sessionId"), VisitorID: visitor,
		Source: participation.Source(strings.TrimSpace(body.Source)),
	})
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	out := actionSessionView(res.Session, true)
	out["participation"] = newParticipationView(res.Participation)
	writeJSON(w, status, out)
}

// handleActionSessionResumeCredentialIssue:
// POST /api/v1/public/action-sessions/{sessionId}/resume-credentials。
// 只向当前合法访客签发 purpose=customer_resume 的短时单用途凭据;
// 明文只在此响应出现一次。
func (s *Server) handleActionSessionResumeCredentialIssue(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	tok, err := s.Participation.IssueResumeCredential(r.Context(), participation.ResumeCredentialInput{
		SessionID: r.PathValue("sessionId"), VisitorID: visitor,
	})
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"credential_id": tok.Credential.ID,
		"purpose":       string(participation.PurposeCustomerResume),
		"token":         tok.Token, // 唯一一次明文;消费/日志都不再出现
		"expires_at":    tok.Credential.ExpiresAt,
	})
}

// handleActionSessionResumeCredentialRevoke:
// DELETE /api/v1/public/action-sessions/{sessionId}/resume-credentials/{credentialId}。
func (s *Server) handleActionSessionResumeCredentialRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	if err := s.Participation.RevokeResumeCredential(r.Context(),
		r.PathValue("sessionId"), visitor, r.PathValue("credentialId")); err != nil {
		writeActionSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// handleActionSessionCancel: POST /api/v1/public/action-sessions/{sessionId}/cancel。
// 取消即终态 + 散落凭据全废。
func (s *Server) handleActionSessionCancel(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	sess, err := s.Participation.CancelActionSession(r.Context(), r.PathValue("sessionId"), visitor)
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, actionSessionView(sess, false))
}

// handleResumeExchange: POST /api/v1/public/resume/exchange。
// 校验作用域后绑定新浏览器并消费该凭据(单用途);凭据走 POST 体,
// 永不进 URL 查询参数。店员码/参与凭据查无此记录,统一 404。
func (s *Server) handleResumeExchange(w http.ResponseWriter, r *http.Request) {
	if !s.actionSessionsReady(w) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	visitor := s.ensureVisitor(w, r)
	if visitor == "" {
		fail(w, http.StatusInternalServerError, "internal", "visitor key unavailable")
		return
	}
	res, err := s.Participation.ExchangeResumeCredential(r.Context(), participation.SessionExchangeInput{
		Token: body.Token, NewVisitorID: visitor,
	})
	if err != nil {
		writeActionSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, actionSessionView(res.Session, true))
}
