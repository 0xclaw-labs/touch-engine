package httpapi

// handlers_public_leads.go: HUI-1747 public lead-capture surface (guest, no
// session). Discipline carried over from the T0 public surface, extended for
// the one public WRITE this ticket adds:
//   - FEATURE_LEADS_CAPTURE off (the default) -> every route answers the same
//     uniform 404 as "campaign not found": the surface does not exist;
//   - campaign validity reuses the T0 five-state resolution; non-available is
//     404 with state text only;
//   - source_app and tenant are server-owned: the request schema is strict
//     (DisallowUnknownFields), so target_tenant/tenant_id/source_app in a body
//     are refused before any domain code runs;
//   - responses carry submission_ref and state only — never contact data;
//   - anonymous view events increment a pure aggregate counter (no identity).

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignrules"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// leadsGate answers uniform 404 when the feature is off: an invisible surface.
func (s *Server) leadsGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureLeadsCapture {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// resolvePublicCampaign resolves a short code for the public lead surface and
// writes the T0-style stateful 404 for every non-available outcome.
func (s *Server) resolvePublicCampaign(w http.ResponseWriter, r *http.Request) (store.ResolvedLink, bool) {
	code := strings.TrimSpace(r.PathValue("code"))
	res := s.St.ResolveLink(code, time.Now())
	if res.Outcome != store.OutcomeAvailable {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(res.Outcome)})
		return res, false
	}
	return res, true
}

// GET /api/v1/public/links/{code}/lead-form
func (s *Server) handlePublicLeadForm(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "lead form is read-only")
		return
	}
	res, ok := s.resolvePublicCampaign(w, r)
	if !ok {
		return
	}
	if s.blockPublicWrite(w, r, res) {
		return
	}
	notice := leads.CurrentNotice()
	form, err := s.St.GetLeadFormByCampaign(res.Campaign.ID)
	if errors.Is(err, store.ErrNotFound) || !form.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead form lookup failed")
		return
	}
	marketing := form.MarketingOptinEnabled
	// the shown notice version must match what we will require consent for
	if form.NoticeVersion != notice.Version {
		notice.Version = form.NoticeVersion // form pins the version it was configured with
	}
	merchant := ""
	if t, err := s.St.GetTenant(res.Campaign.TenantID); err == nil {
		merchant = t.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":                 true,
		"fields":                  []string{"name", "phone", "wechat(optional)"},
		"required_fields":         []string{"name", "phone"},
		"purpose":                 "本次活动的服务与咨询。不留下联系方式也可以继续看活动。",
		"recipient":               map[string]any{"name": merchant},
		"notice":                  notice,
		"marketing_optin_enabled": marketing,
		"marketing_blocks_browse": false,
	})
}

// POST /api/v1/public/links/{code}/lead-submissions
func (s *Server) handlePublicLeadSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	res, ok := s.resolvePublicCampaign(w, r)
	if !ok {
		return
	}
	if s.blockPublicWrite(w, r, res) {
		return
	}

	// strict schema: unknown fields (target_tenant, tenant_id, source_app, ...)
	// are refused outright — the delivery tenant is the campaign's owner and
	// can never be steered by request input.
	var in struct {
		Name           string `json:"name"`
		Phone          string `json:"phone"`
		Wechat         string `json:"wechat"`
		ConsentVersion string `json:"consent_version"`
		Consent        bool   `json:"consent"`
		MarketingOptin bool   `json:"marketing_optin"`
		Channel        string `json:"channel"`
	}
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if !in.Consent {
		fail(w, http.StatusBadRequest, "consent_required", "explicit consent is required to store a lead")
		return
	}
	notice := leads.CurrentNotice()
	if in.ConsentVersion != notice.Version {
		fail(w, http.StatusBadRequest, "notice_version_mismatch", "consent must reference the notice version shown to you")
		return
	}
	phone, err := leads.NormalizePhone(in.Phone)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_phone", "phone must be a valid mainland mobile number")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	form, err := s.St.GetLeadFormByCampaign(res.Campaign.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !form.Enabled) {
		fail(w, http.StatusConflict, "lead_capture_not_open", "this campaign does not accept leads")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead form lookup failed")
		return
	}
	if in.MarketingOptin && !form.MarketingOptinEnabled {
		in.MarketingOptin = false // 营销勾选未开启时提交无效,而非报错
	}

	// per-IP simple rate limit (single process, in-memory)
	ip := clientIP(r)
	if !s.LeadsLimiter.Allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "message": "too many submissions, try later"})
		return
	}

	now := time.Now().UTC()
	phoneFP := leads.PhoneFingerprint(s.Cfg.LeadsPhonePepper, phone)
	dk := leads.DedupKey(s.Cfg.LeadsPhonePepper, res.Campaign.ID, phoneFP, now)

	// HUI-1676 FEAT-0177: 确定性活动规则——每联系人每日提交频控(登记制开关,
	// FEATURE_CAMPAIGN_RULES off 时此块整体不存在,现行为逐字节不变)。
	//   - 幂等重放先短路:同 (活动,手机号,UTC 日) 的重放直接走既有幂等路径,
	//     不评估也不计数;首次提交才评估频控;
	//   - 计数口径:租户内该联系人当日全部首次提交(与按活动的同日同号幂等
	//     正交),按 UTC 日窗;规则值取本活动的规则集;
	//   - 只拒绝不罚款:拒绝响应按仓内错误惯例带机器原因码 submission_cap_reached。
	if s.Cfg.FeatureCampaignRules {
		if _, rerr := s.St.GetLeadSubmissionByDedupKey(dk); errors.Is(rerr, store.ErrNotFound) {
			rules, gerr := s.St.GetCampaignRulesByCampaign(res.Campaign.ID)
			if errors.Is(gerr, store.ErrNotFound) {
				rules.Ruleset = campaignrules.Ruleset{} // 未配置 = 不限
			} else if gerr != nil {
				fail(w, http.StatusInternalServerError, "internal", "campaign rules lookup failed")
				return
			}
			if rules.Ruleset.PerContactDailySubmissionCap != nil {
				n, cerr := s.St.CountContactSubmissionsOnDay(res.Campaign.TenantID, phone, now.Format("2006-01-02"))
				if cerr != nil {
					fail(w, http.StatusInternalServerError, "internal", "submission count failed")
					return
				}
				// 评估点只应用本阶段规则(提交入口只看提交频控;发布/奖励规则
				// 属其各自链路,待 HUI-1670/1671 落地接线)。
				subRS := campaignrules.Ruleset{PerContactDailySubmissionCap: rules.Ruleset.PerContactDailySubmissionCap}
				if d := campaignrules.Evaluate(subRS, campaignrules.Facts{Now: now, ContactSubmissionsToday: n}); !d.Allowed {
					writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": d.Reason, "message": "this contact has reached today's submission limit"})
					return
				}
			}
		} else if rerr != nil {
			fail(w, http.StatusInternalServerError, "internal", "submission lookup failed")
			return
		}
	}

	ref, err := leads.NewSubmissionRef()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reference mint failed")
		return
	}
	eventID := "ev-" + ref
	envelope := leads.BuildSubmitEnvelope(
		s.Cfg.AppID, s.Cfg.LeadsTargetApp, res.Campaign.TenantID,
		res.Campaign.ID, res.Campaign.StoreID, in.Channel, "", "", // tag/asset left to future surface wiring
		ref, form.NoticeVersion, now.Format(time.RFC3339), in.MarketingOptin,
		in.Name, phone, in.Wechat, 1)
	if s.Cfg.FeatureBrand {
		if d := s.decidePublicBrand(r, res); d.Shell != nil {
			envelope.ApplyBrandTemplate(d.Shell.DisplayName, "")
		}
	}
	envelope.AttachSourceTrace(s.campaignLeadTrace(res.Campaign, res.Link.Code, ref))
	payload, err := leads.MarshalEnvelope(envelope)
	if errors.Is(err, leads.ErrPIIDetected) {
		// structurally impossible today; kept as a hard stop if the payload grows
		fail(w, http.StatusInternalServerError, "internal", "outbound fact refused by PII scan")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "envelope build failed")
		return
	}

	lead, dup, err := s.St.SubmitLead(store.NewLeadSubmission{
		TenantID: res.Campaign.TenantID, CampaignID: res.Campaign.ID,
		StoreID: res.Campaign.StoreID, LinkID: res.Link.ID, FormID: form.ID,
		SubmissionRef: ref, DedupKey: dk,
		Name: in.Name, Phone: phone, Wechat: in.Wechat,
		MarketingOptin: in.MarketingOptin,
		NoticeVersion:  form.NoticeVersion,
		ConsentAt:      now.Format(time.RFC3339),
		ConsentIPFP:    leads.PhoneFingerprint(s.Cfg.LeadsPhonePepper, ip), // IP fingerprint, not raw IP
		OutboxEventID:  eventID, OutboxPayload: string(payload),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead store failed")
		return
	}
	if !dup {
		_ = s.St.AppendLeadAudit(res.Campaign.TenantID, lead.SubmissionRef, "accept",
			`{"notice":"`+form.NoticeVersion+`","marketing":`+boolJSON(in.MarketingOptin)+`}`, "consumer")
	}
	// 幂等:同键(活动+手机号+单位时间)返回原 submission_ref;状态语义一致。
	// 本地接受、CRM 接收、负责人跟进是三件不同的事。这里只报告本地事实。
	body := publicLeadProgress(lead)
	body["duplicate"] = dup
	body["notice_version"] = form.NoticeVersion
	body["revoke"] = "POST /api/v1/public/links/" + res.Link.Code + "/lead-revocations"
	if t, err := s.St.GetTenant(res.Campaign.TenantID); err == nil {
		body["submitted_to"] = map[string]any{"kind": "merchant_tenant", "name": t.Name}
	}
	writeJSON(w, mapStatus(dup), body)
}

// POST /api/v1/public/links/{code}/lead-status
// Verified read of the real sync projection. Phone mismatch is a uniform 404.
func (s *Server) handlePublicLeadStatus(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "lead status is a verified read")
		return
	}
	res, ok := s.resolvePublicCampaign(w, r)
	if !ok {
		return
	}
	if s.blockPublicWrite(w, r, res) {
		return
	}
	var in struct {
		SubmissionRef string `json:"submission_ref"`
		Phone         string `json:"phone"`
	}
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	phone, err := leads.NormalizePhone(in.Phone)
	if err != nil {
		writeJSON(w, http.StatusNotFound, revocationNotFound())
		return
	}
	lead, err := s.St.GetLeadSubmissionByRef(strings.TrimSpace(in.SubmissionRef))
	if err != nil || lead.CampaignID != res.Campaign.ID || lead.Phone != phone {
		writeJSON(w, http.StatusNotFound, revocationNotFound())
		return
	}
	writeJSON(w, http.StatusOK, publicLeadProgress(lead))
}

func publicLeadProgress(lead store.LeadSubmission) map[string]any {
	state := lead.SyncState
	if lead.SyncState == leads.StateRejected && strings.Contains(lead.SyncError, "subscription_disabled") {
		state = "crm_paused"
	}
	// 销售是否收到、负责人是否跟进，这个公共面观察不到。写成 false 会变成否定结论。
	return map[string]any{
		"submission_ref":           lead.SubmissionRef,
		"state":                    state,
		"crm_received":             lead.SyncState == leads.StateCRMReceived,
		"sales_received":           "unknown",
		"owner_followed_up":        "unknown",
		"creates_platform_account": false,
	}
}

// POST /api/v1/public/links/{code}/lead-revocations
func (s *Server) handlePublicLeadRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	res, ok := s.resolvePublicCampaign(w, r)
	if !ok {
		return
	}
	if s.blockPublicWrite(w, r, res) {
		return
	}
	var in struct {
		SubmissionRef string `json:"submission_ref"`
		Phone         string `json:"phone"`
	}
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	phone, err := leads.NormalizePhone(in.Phone)
	if err != nil {
		writeJSON(w, http.StatusNotFound, revocationNotFound()) // no signal about ref existence
		return
	}
	if !s.LeadsLimiter.Allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "message": "too many attempts, try later"})
		return
	}
	lead, err := s.St.GetLeadSubmissionByRef(strings.TrimSpace(in.SubmissionRef))
	// 最小验证:ref 必须属于该短码的活动,且手机号一致;任一失败统一 404 防枚举。
	if err != nil || lead.CampaignID != res.Campaign.ID || lead.Phone != phone {
		writeJSON(w, http.StatusNotFound, revocationNotFound())
		return
	}

	stored, err := s.St.LeadOutboxPayload(lead.SubmissionRef, "submit")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "submit fact lookup failed")
		return
	}
	trace, err := traceFromSubmitFact(stored)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "submit fact unreadable")
		return
	}
	revokeVersion := lead.SourceVersion + 1
	revokeEnv := leads.BuildRevokeEnvelope(
		s.Cfg.AppID, s.Cfg.LeadsTargetApp, lead.TenantID, lead.CampaignID,
		lead.SubmissionRef, lead.ConsentAt, revokeVersion,
		lead.Name, lead.Phone, lead.Wechat)
	revokeEnv.AttachSourceTrace(trace)
	revokePayload, err := leads.MarshalEnvelope(revokeEnv)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revoke fact refused by PII scan")
		return
	}
	revokeEventID := "ev-" + lead.SubmissionRef + "-revoke"
	updated, err := s.St.RevokeLeadSubmission(lead.SubmissionRef, revokeEventID, string(revokePayload))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, revocationNotFound())
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation failed")
		return
	}
	_ = s.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "revoke",
		`{"prior_state":"`+lead.SyncState+`","revoke_event":`+boolJSON(updated.SyncState == leads.StateRevoked)+`}`, "consumer")
	writeJSON(w, http.StatusOK, map[string]any{
		"submission_ref": updated.SubmissionRef,
		"state":          updated.SyncState,
	})
}

// POST /api/v1/public/links/{code}/view-events — anonymous browse beacon.
func (s *Server) handlePublicViewEvent(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	res, ok := s.resolvePublicCampaign(w, r)
	if !ok {
		return
	}
	var in struct {
		Channel string `json:"channel"`
	}
	// body optional; unknown fields (tenant, principal, ...) are refused and
	// do not count as a view. An explicit qr/nfc tag is only a stat bucket.
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	channel := in.Channel
	switch channel {
	case "web", "wecom", "qr", "nfc":
	default:
		channel = "web"
	}
	day := time.Now().UTC().Format("2006-01-02")
	if err := s.St.IncrementViewStat(res.Link.Code, day, channel); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "view stat failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- small helpers -------------------------------------------------------------

func revocationNotFound() map[string]any {
	return map[string]any{"error": "not_found", "message": "submission not found or verification failed"}
}

func mapStatus(dup bool) int {
	if dup {
		return http.StatusOK
	}
	return http.StatusCreated
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func ioLimit(r *http.Request) io.Reader { return io.LimitReader(r.Body, 1<<20) }

// clientIP extracts the host part of RemoteAddr (loopback BFF in production).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
