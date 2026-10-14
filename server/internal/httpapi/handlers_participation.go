package httpapi

// Participation surface (HUI-3546 碰一碰门店交付 A2): 参与记录与跨端恢复。
//
// 两个互不相交的面:
//   - 顾客面(http.HandlerFunc,无商家会话概念):凭首方 visitor cookie 定位
//     「自身」参与(租户边界来自参与行本身,访客面没有可伪造的租户入参);
//     跨端续接只走短时/单用途/可撤销的交接凭据。payload 是白名单:
//     visitor_id、幂等键、预留引用一律不出现在响应里;
//   - 店员面(requireSession + 既有 authz 门店作用域):读参与/兑换顾客出示
//     的核验凭据,必须真实商家成员且门店作用域相符。
//
// 纪律:
//   - FEATURE_PARTICIPATION 默认 off:off = 路由不注册(404 不可见)且每个
//     handler 再答统一 404(双保险,与 FEATURE_DASHBOARD 同写法);
//   - benefit_reservation_state 只有 confirmed 才代表 G3 确认;pending/failed
//     一律不得表述为已保留(文案守卫在 web 层,这里只出机器可读状态);
//   - 凭据明文只在创建响应中出现一次,永不进日志;日志面是 method/path/status。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/redact"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// participationVisitorCookie is the first-party continuation key. It is a
// random id: not a phone number, not a platform open id, not an account.
const participationVisitorCookie = "touch_participation_visitor"

// participationReady is the double gate: off answers uniform 404 regardless
// of which handler the request reached.
func (s *Server) participationReady(w http.ResponseWriter) bool {
	if !s.Cfg.FeatureParticipation || s.Participation == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": "not_found"})
		return false
	}
	return true
}

// visitorKey returns the request's visitor id from the cookie ("" if absent).
func visitorKey(r *http.Request) string {
	c, err := r.Cookie(participationVisitorCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

// ensureVisitor returns the existing visitor id or mints one and sets the
// cookie. The cookie only ever rides this app's origin (no explicit Domain),
// HttpOnly, SameSite=Lax — same-browser continuation only.
func (s *Server) ensureVisitor(w http.ResponseWriter, r *http.Request) string {
	if v := visitorKey(r); v != "" {
		return v
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	v := hex.EncodeToString(b[:])
	http.SetCookie(w, &http.Cookie{
		Name: participationVisitorCookie, Value: v, Path: "/",
		MaxAge: 365 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	return v
}

// resolveAvailableLink answers the public five-state 404 shape for any
// non-available outcome (the machine-readable state is already public on the
// guest page; nothing else leaks).
func (s *Server) resolveAvailableLink(w http.ResponseWriter, code string) (store.ResolvedLink, bool) {
	res := s.St.ResolveLink(strings.TrimSpace(code), time.Now())
	if res.Outcome != store.OutcomeAvailable {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(res.Outcome)})
		return store.ResolvedLink{}, false
	}
	return res, true
}

// ---- payloads (whitelist) ------------------------------------------------------

type participationView struct {
	ID                      string        `json:"id"`
	Status                  string        `json:"status"`
	LinkCode                string        `json:"link_code"`
	ContentPackVersion      string        `json:"content_pack_version,omitempty"`
	RuleVersion             int           `json:"rule_version"`
	BenefitReservationState string        `json:"benefit_reservation_state"`
	StartsAt                string        `json:"starts_at,omitempty"`
	ExpiresAt               string        `json:"expires_at,omitempty"`
	CreatedAt               string        `json:"created_at"`
	Attempts                []attemptView `json:"attempts,omitempty"`
}

type attemptView struct {
	ID                 string `json:"id"`
	Platform           string `json:"platform,omitempty"`
	HandoffMethod      string `json:"handoff_method"`
	ContentPackVersion string `json:"content_pack_version,omitempty"`
	RuleVersion        int    `json:"rule_version"`
	Status             string `json:"status"`
	CreatedAt          string `json:"created_at"`
}

func newParticipationView(p participation.Participation) participationView {
	return participationView{
		ID: p.ID, Status: string(p.Status), LinkCode: p.LinkCode,
		ContentPackVersion: p.ContentPackVersion, RuleVersion: p.RuleVersion,
		BenefitReservationState: string(p.ReservationState),
		StartsAt:                p.Window.StartsAt, ExpiresAt: p.Window.ExpiresAt,
		CreatedAt: p.CreatedAt,
	}
}

func newAttemptView(a participation.Attempt) attemptView {
	return attemptView{
		ID: a.ID, Platform: a.Platform, HandoffMethod: string(a.HandoffMethod),
		ContentPackVersion: a.ContentPackVersion, RuleVersion: a.RuleVersion,
		Status: string(a.Status), CreatedAt: a.CreatedAt,
	}
}

// writeParticipationError maps domain sentinels onto status codes. Unknown
// and not-found collapse into 404 so probing learns nothing.
func writeParticipationError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, participation.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "participation not found")
	case errors.Is(err, participation.ErrNotEntitled):
		fail(w, http.StatusForbidden, "forbidden", "this participation is not yours to read")
	case errors.Is(err, participation.ErrCredentialExpired):
		fail(w, http.StatusGone, "handoff_expired", "this handoff credential has expired")
	case errors.Is(err, participation.ErrCredentialUsed):
		fail(w, http.StatusConflict, "handoff_used", "this handoff credential was already used")
	case errors.Is(err, participation.ErrCredentialRevoked):
		fail(w, http.StatusConflict, "handoff_revoked", "this handoff credential was revoked")
	case errors.Is(err, participation.ErrTargetMismatch):
		fail(w, http.StatusForbidden, "target_mismatch", "this handoff credential does not apply here")
	case errors.Is(err, participation.ErrFrozen):
		fail(w, http.StatusConflict, "frozen", "this participation is closed to changes")
	case errors.Is(err, participation.ErrInvalidTransition), errors.Is(err, participation.ErrInvalidSource):
		fail(w, http.StatusUnprocessableEntity, "invalid_state", "this participation action is not available")
	case errors.Is(err, participation.ErrIdempotencyConflict):
		fail(w, http.StatusConflict, "idempotency_conflict", "this idempotency key belongs to another session; start fresh")
	default:
		fail(w, http.StatusInternalServerError, "internal", "participation action failed")
	}
}

func decodeParticipationBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return false
	}
	return true
}

// ---- customer surface ------------------------------------------------------------

// handleParticipationStart: 浏览绝不建参与;只有显式开始才落行并申请预留。
func (s *Server) handleParticipationStart(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	var body struct {
		LinkCode           string `json:"link_code"`
		Source             string `json:"touchpoint_source"`
		ContentPackVersion string `json:"content_pack_version"`
		IdempotencyKey     string `json:"idempotency_key"`
		RuleVersion        int    `json:"rule_version"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	res, ok := s.resolveAvailableLink(w, body.LinkCode)
	if !ok {
		return
	}
	visitor := s.ensureVisitor(w, r)
	if visitor == "" {
		fail(w, http.StatusInternalServerError, "internal", "visitor key unavailable")
		return
	}
	ruleVersion := body.RuleVersion
	if ruleVersion == 0 {
		ruleVersion = 1
	}
	if ruleVersion < 0 {
		fail(w, http.StatusUnprocessableEntity, "invalid_state", "rule_version must not be negative")
		return
	}
	started, err := s.Participation.Start(r.Context(), participation.StartInput{
		TenantID: res.Campaign.TenantID, StoreID: res.Campaign.StoreID, CampaignID: res.Campaign.ID,
		LinkCode: res.Link.Code, Source: participation.Source(strings.TrimSpace(body.Source)),
		RuleVersion: ruleVersion, ContentPackVersion: strings.TrimSpace(body.ContentPackVersion),
		VisitorID: visitor, IdempotencyKey: strings.TrimSpace(body.IdempotencyKey),
		Window: participation.Window{StartsAt: res.Campaign.StartsAt, ExpiresAt: res.Campaign.EndsAt},
	})
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	status := http.StatusOK
	if started.Started {
		status = http.StatusCreated
	}
	writeJSON(w, status, newParticipationView(started.Participation))
}

// handleParticipationLookup: 同设备重复碰触优先恢复未完参与(不开新记录)。
func (s *Server) handleParticipationLookup(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	var body struct {
		LinkCode string `json:"link_code"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	res, ok := s.resolveAvailableLink(w, body.LinkCode)
	if !ok {
		return
	}
	visitor := s.ensureVisitor(w, r)
	p, found, err := s.Participation.LookupByVisitor(r.Context(), res.Campaign.TenantID, res.Link.Code, visitor)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, newParticipationView(p))
}

// requireVisitor404: 无 cookie 就没有任何「自身参与」可言;统一 404
// (与 not_found 同形,不给探测面)。
func requireVisitor404(w http.ResponseWriter, r *http.Request) string {
	visitor := visitorKey(r)
	if visitor == "" {
		fail(w, http.StatusNotFound, "not_found", "participation not found")
		return ""
	}
	return visitor
}

func (s *Server) handleParticipationGet(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	p, err := s.Participation.GetForVisitor(r.Context(), r.PathValue("id"), visitor)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	view := newParticipationView(p)
	if view.Attempts, err = s.visitorAttempts(r, p.ID, visitor); err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) visitorAttempts(r *http.Request, id, visitor string) ([]attemptView, error) {
	attempts, err := s.Participation.ListAttempts(r.Context(), id, visitor)
	if err != nil {
		return nil, err
	}
	out := make([]attemptView, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, newAttemptView(a))
	}
	return out, nil
}

func (s *Server) handleParticipationContent(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
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
	updated, err := s.Participation.UpdateContent(r.Context(), r.PathValue("id"), visitor, strings.TrimSpace(body.ContentPackVersion))
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newParticipationView(updated))
}

func (s *Server) handleParticipationCancel(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	updated, err := s.Participation.Cancel(r.Context(), r.PathValue("id"), visitor)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newParticipationView(updated))
}

func (s *Server) handleParticipationAttempts(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	out, err := s.visitorAttempts(r, r.PathValue("id"), visitor)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attempts": out})
}

func (s *Server) handleParticipationAttemptCreate(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	var body struct {
		Platform string `json:"platform"`
		Method   string `json:"handoff_method"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Method) == "" {
		body.Method = string(participation.MethodH5)
	}
	a, err := s.Participation.CreateAttempt(r.Context(), participation.AttemptInput{
		ParticipationID: r.PathValue("id"), VisitorID: visitor,
		Platform: strings.TrimSpace(body.Platform), Method: participation.HandoffMethod(body.Method),
	})
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newAttemptView(a))
}

// handleParticipationHandoff: 冻结 attempt 并签发短时/单用途凭据。
// 凭据明文只在此响应出现一次。
func (s *Server) handleParticipationHandoff(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	var body struct {
		AttemptID string `json:"attempt_id"`
		Purpose   string `json:"purpose"`
	}
	if !decodeParticipationBody(w, r, &body) {
		return
	}
	tok, err := s.Participation.BeginHandoff(r.Context(), participation.HandoffInput{
		ParticipationID: r.PathValue("id"), VisitorID: visitor,
		AttemptID:     strings.TrimSpace(body.AttemptID),
		Purpose:       participation.Purpose(strings.TrimSpace(body.Purpose)),
		PublicBaseURL: s.Cfg.PublicBaseURL,
	})
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	resp := map[string]any{
		"credential_id": tok.Credential.ID,
		"purpose":       string(tok.Credential.Purpose),
		"target_scope":  redact.MaskCode(tok.Credential.TargetScope),
		"expires_at":    tok.Credential.ExpiresAt,
		"token":         tok.Token, // 唯一一次明文;消费/日志都不再出现
		"participation": newParticipationView(tok.Participation),
	}
	if tok.QRPayload != "" {
		resp["qr_payload"] = tok.QRPayload
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleParticipationHandoffRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	visitor := requireVisitor404(w, r)
	if visitor == "" {
		return
	}
	if err := s.Participation.Revoke(r.Context(), r.PathValue("id"), visitor, r.PathValue("credentialId")); err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// handleParticipationRedeem: 顾客面兑换只认 customer_resume;店员核验
// 必须走 /staff/redeem(商家会话),这里收到 staff 目标一律拒绝。
func (s *Server) handleParticipationRedeem(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
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
	res, err := s.Participation.Redeem(r.Context(), participation.RedeemInput{
		Token: body.Token, Purpose: participation.PurposeCustomerResume,
		TargetScope: "h5", NewVisitorID: visitor,
	})
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newParticipationView(res.Participation))
}

// ---- staff surface (requireSession + authz store scope) ---------------------------

// handleParticipationStaffView: 店员读取。域层租户/门店作用域先审,
// authz.ActionReadRecord 再审,双 fail-closed。
func (s *Server) handleParticipationStaffView(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	p, err := s.Participation.GetForStaff(r.Context(), r.PathValue("id"), participation.StaffView{
		TenantID: c.Member.TenantID, Role: string(c.Member.Role), StoreScope: c.Member.StoreScope,
		Revoked: !c.Member.Enabled, // 撤权域层即拒,authz 再审(双 fail-closed)
	})
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: p.StoreID}, true, w) {
		return
	}
	attempts, err := s.Participation.ListAttemptsForStaff(r.Context(), p.ID, p.TenantID)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	view := newParticipationView(p)
	view.Attempts = make([]attemptView, 0, len(attempts))
	for _, a := range attempts {
		view.Attempts = append(view.Attempts, newAttemptView(a))
	}
	writeJSON(w, http.StatusOK, view)
}

// handleParticipationStaffRedeem: 店员扫顾客出示的核验凭据。先 Peek 定位
// 参与做租户/门店作用域鉴权,再消费凭据(单用途),参与推进 awaiting_review。
func (s *Server) handleParticipationStaffRedeem(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	if !s.allowStaffTokenAttempt(c, w) {
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
	p, _, err := s.Participation.PeekCredential(r.Context(), body.Token)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	// 目标租户/门店作用域先审;不符不消费凭据。
	if p.TenantID != c.Member.TenantID {
		fail(w, http.StatusForbidden, "target_mismatch", "this handoff credential does not apply here")
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: p.StoreID}, true, w) {
		return
	}
	if _, err := s.Participation.GetForStaff(r.Context(), p.ID, participation.StaffView{
		TenantID: c.Member.TenantID, Role: string(c.Member.Role), StoreScope: c.Member.StoreScope,
		Revoked: !c.Member.Enabled,
	}); err != nil {
		writeParticipationError(w, err)
		return
	}
	res, err := s.Participation.Redeem(r.Context(), participation.RedeemInput{
		Token: body.Token, Purpose: participation.PurposeStaffVerify,
		TargetScope: c.Member.TenantID, StaffTenantID: c.Member.TenantID,
	})
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newParticipationView(res.Participation))
}

// handleParticipationStaffPeek (HUI-3562 店员台): 扫/手输核验凭据打开参与卡,
// **不消费**凭据——重复扫码幂等回同一张卡,两位店员可同时开卡;消费仍由
// staff/redeem 单独负责。鉴权与 staff/redeem 同序:域层租户/门店作用域先审,
// authz read_record 再审,双 fail-closed;载荷是店员面白名单(参与视图 +
// campaign_id/store_id 供卡面取奖励上下文),访客键/幂等键/预留引用不出。
func (s *Server) handleParticipationStaffPeek(w http.ResponseWriter, r *http.Request) {
	if !s.participationReady(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	if !s.allowStaffTokenAttempt(c, w) {
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
	p, _, err := s.Participation.PeekCredential(r.Context(), body.Token)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	if p.TenantID != c.Member.TenantID {
		fail(w, http.StatusForbidden, "target_mismatch", "this handoff credential does not apply here")
		return
	}
	if _, err := s.Participation.GetForStaff(r.Context(), p.ID, participation.StaffView{
		TenantID: c.Member.TenantID, Role: string(c.Member.Role), StoreScope: c.Member.StoreScope,
		Revoked: !c.Member.Enabled,
	}); err != nil {
		writeParticipationError(w, err)
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: p.StoreID}, true, w) {
		return
	}
	attempts, err := s.Participation.ListAttemptsForStaff(r.Context(), p.ID, p.TenantID)
	if err != nil {
		writeParticipationError(w, err)
		return
	}
	view := newParticipationView(p)
	view.Attempts = make([]attemptView, 0, len(attempts))
	for _, a := range attempts {
		view.Attempts = append(view.Attempts, newAttemptView(a))
	}
	// 卡面载荷:参与视图平铺 + 奖励上下文(campaign/store),仍无访客键/
	// 幂等键/预留引用/租户 id。
	card := map[string]any{
		"campaign_id": p.CampaignID,
		"store_id":    p.StoreID,
		"attempts":    view.Attempts,
	}
	if raw, err := json.Marshal(view); err == nil {
		_ = json.Unmarshal(raw, &card)
	}
	writeJSON(w, http.StatusOK, card)
}

// allowStaffTokenAttempt charges one token-entry attempt against the member's
// budget (staff/peek + staff/redeem share one window: manual entry must not
// become a probing channel). A nil limiter (never in production) stays open.
func (s *Server) allowStaffTokenAttempt(c *caller, w http.ResponseWriter) bool {
	if s.StaffTokenLimiter == nil {
		return true
	}
	if s.StaffTokenLimiter.Allow(c.Member.PrincipalRef) {
		return true
	}
	fail(w, http.StatusTooManyRequests, "too_many_attempts", "尝试太频繁，请稍后再试。")
	return false
}
