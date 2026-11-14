package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Customer publish surface (HUI-1670). Guests may preview, export, and
// explicitly confirm a manual publish. This handler never calls a platform
// and never reports publish success from a button, a confirmation, or a
// client-supplied post id.

func (s *Server) handlePublicPublishCapabilities(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availableLegacyPublishLink(w, r)
	if !ok {
		return
	}
	notes, err := s.St.ListPublishAdapterNotes(res.Campaign.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "capability lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, capabilityPayload(notes))
}

func (s *Server) handlePublicPublishPreview(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availableLegacyPublishLink(w, r)
	if !ok {
		return
	}
	var body struct {
		Platform     string `json:"platform"`
		Publisher    string `json:"publisher"`
		Copy         string `json:"copy"`
		AccountLabel string `json:"account_label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	attempt, err := custpublish.Preview(custpublish.PreviewInput{
		Platform: custpublish.Platform(strings.TrimSpace(body.Platform)), Publisher: body.Publisher,
		Copy: body.Copy, AccountLabel: body.AccountLabel,
	})
	if err != nil {
		writePublishGate(w, err)
		return
	}
	saved, err := s.St.CreateCustomerPublishPreview(res.Campaign.TenantID, res.Campaign.ID, res.Link.Code, attempt)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "preview could not be saved")
		return
	}
	writeJSON(w, http.StatusCreated, publishAttemptPayload(saved, nil))
}

func (s *Server) handlePublicPublishExport(w http.ResponseWriter, r *http.Request) {
	res, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	_, pkg, err := custpublish.Export(row.DomainAttempt())
	if err != nil {
		writePublishGate(w, err)
		return
	}
	saved, err := s.St.MarkCustomerPublishExported(row.ID, res.Campaign.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, publishAttemptPayload(saved, &pkg))
}

func (s *Server) handlePublicPublishConfirm(w http.ResponseWriter, r *http.Request) {
	res, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	var body struct {
		ContentVersion   string `json:"content_version"`
		AccountLabel     string `json:"account_label"`
		Publisher        string `json:"publisher"`
		Platform         string `json:"platform"`
		AssetUseAccepted bool   `json:"asset_use_accepted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	platform := body.Platform
	if platform == "" {
		platform = row.Platform
	}
	next, err := custpublish.Confirm(row.DomainAttempt(), custpublish.Confirmation{
		ContentVersion: body.ContentVersion, AccountLabel: body.AccountLabel,
		Platform: custpublish.Platform(platform), Publisher: body.Publisher, AssetUseAccepted: body.AssetUseAccepted,
	})
	if err != nil {
		writePublishGate(w, err)
		return
	}
	saved, err := s.St.SaveCustomerPublishConfirmation(row.ID, res.Campaign.TenantID, next.ContentVersion, next.AccountLabel)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "confirmation could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, publishAttemptPayload(saved, nil))
}

func (s *Server) handlePublicPublishSelfReport(w http.ResponseWriter, r *http.Request) {
	res, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	var body struct {
		PostID string `json:"post_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	_ = custpublish.SelfReport(row.DomainAttempt(), body.PostID)
	saved, err := s.St.MarkCustomerPublishSelfReport(row.ID, res.Campaign.TenantID, body.PostID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "self-report could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, publishAttemptPayload(saved, nil))
}

func (s *Server) handlePublicPublishRequest(w http.ResponseWriter, r *http.Request) {
	_, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	_, err := custpublish.RequestPublish(row.DomainAttempt())
	if err != nil {
		writePublishGate(w, err)
		return
	}
	fail(w, http.StatusConflict, custpublish.ReasonCapabilityClosed, "authorized publish is not available")
}

func (s *Server) handlePublicPublishOpenEditor(w http.ResponseWriter, r *http.Request) {
	_, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	_, err := custpublish.OpenEditor(row.DomainAttempt())
	if err != nil {
		writePublishGate(w, err)
		return
	}
	fail(w, http.StatusConflict, custpublish.ReasonCapabilityClosed, "opening the platform editor is not available")
}

func (s *Server) handlePublicPublishRevise(w http.ResponseWriter, r *http.Request) {
	res, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	var body struct {
		Copy         *string `json:"copy"`
		AccountLabel *string `json:"account_label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	if body.Copy == nil && body.AccountLabel == nil {
		fail(w, http.StatusBadRequest, "missing_revision", "send a new copy or account")
		return
	}
	next := row.DomainAttempt()
	if body.Copy != nil {
		revised, err := custpublish.ChangeCopy(next, *body.Copy)
		if err != nil {
			writePublishGate(w, err)
			return
		}
		next = revised
	}
	if body.AccountLabel != nil {
		next = custpublish.ChangeAccount(next, *body.AccountLabel)
	}
	saved, err := s.St.ReviseCustomerPublish(row.ID, res.Campaign.TenantID, next)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revision could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, publishAttemptPayload(saved, nil))
}

func (s *Server) handlePublishAdapterNote(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	decision := authz.Authorize(s.authzMember(c), authz.ActionManageCampaignRules, authz.RecordScope{TenantID: c.Member.TenantID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, "forbidden", "not allowed to note a publish adapter")
		return
	}
	var body struct {
		Platform string `json:"platform"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	if err := s.St.NotePublishAdapter(c.Member.TenantID, strings.TrimSpace(body.Platform), body.Name); err != nil {
		fail(w, http.StatusBadRequest, "bad_adapter", "platform and adapter name are required")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"noted": true, "enables_capabilities": false,
		"platform": strings.TrimSpace(body.Platform), "name": strings.TrimSpace(body.Name),
	})
}

func (s *Server) availablePublishLink(w http.ResponseWriter, r *http.Request) (store.ResolvedLink, bool) {
	code := strings.TrimSpace(r.PathValue("code"))
	res := s.St.ResolveLink(code, time.Now())
	if res.Outcome != store.OutcomeAvailable {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(res.Outcome)})
		return store.ResolvedLink{}, false
	}
	return res, true
}

func (s *Server) publishAttempt(w http.ResponseWriter, r *http.Request) (store.ResolvedLink, store.CustomerPublishAttempt, bool) {
	res, ok := s.availableLegacyPublishLink(w, r)
	if !ok {
		return store.ResolvedLink{}, store.CustomerPublishAttempt{}, false
	}
	row, err := s.St.GetCustomerPublish(r.PathValue("id"), res.Campaign.TenantID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "publish preparation not found")
		} else {
			fail(w, http.StatusInternalServerError, "internal", "publish preparation lookup failed")
		}
		return store.ResolvedLink{}, store.CustomerPublishAttempt{}, false
	}
	if row.CampaignID != res.Campaign.ID {
		fail(w, http.StatusNotFound, "not_found", "publish preparation not found")
		return store.ResolvedLink{}, store.CustomerPublishAttempt{}, false
	}
	return res, row, true
}

// This older text-only workflow cannot carry the original participation,
// reward hold or admitted media method. Configured campaigns use the normal
// participation/handoff authority; old rows remain historical facts.
func (s *Server) availableLegacyPublishLink(w http.ResponseWriter, r *http.Request) (store.ResolvedLink, bool) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return store.ResolvedLink{}, false
	}
	_, err := s.St.GetPublishConfig(res.Campaign.TenantID, res.Campaign.ID)
	if err == nil {
		fail(w, http.StatusConflict, "original_participation_required", "本活动请使用原参与进度中的发布交接；名额确认前不要先完成有奖励任务。参考内容仍可查看。")
		return store.ResolvedLink{}, false
	}
	if !errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusServiceUnavailable, "participation_contract_unavailable", "本次活动条件暂时读不到，请稍后刷新原进度。")
		return store.ResolvedLink{}, false
	}
	return res, true
}

func writePublishGate(w http.ResponseWriter, err error) {
	reason := custpublish.ReasonOf(err)
	status := http.StatusConflict
	if reason == custpublish.ReasonPublisherMustBeCustomer || reason == "unknown_platform" || reason == "missing_copy_or_account" || reason == "confirmation_mismatch" || reason == "asset_use_not_accepted" {
		status = http.StatusUnprocessableEntity
	}
	message := "this publish action is not available"
	if reason == custpublish.ReasonPublisherMustBeCustomer {
		message = "publish subject must be the customer who joined the activity, not a merchant account"
	}
	fail(w, status, reason, message)
}

func capabilityPayload(notes []custpublish.AdapterNote) map[string]any {
	rows := custpublish.Matrix(notes)
	platforms := make([]any, 0, len(rows))
	for _, row := range rows {
		caps := map[string]any{}
		for _, kind := range []custpublish.Capability{
			custpublish.CapPreview, custpublish.CapExport, custpublish.CapOpenEditor,
			custpublish.CapAuthorizedPublish, custpublish.CapConfirmPublish,
		} {
			cell := row.Capability(kind)
			item := map[string]any{"enabled": cell.Enabled, "reason": cell.Reason}
			if cell.EvidenceURL != "" {
				item["evidence_url"] = cell.EvidenceURL
			}
			caps[string(kind)] = item
		}
		platforms = append(platforms, map[string]any{
			"platform": row.Platform, "publisher": row.Publisher,
			"manual_guide": row.ManualGuide, "capabilities": caps,
		})
	}
	names := make([]string, 0, len(notes))
	for _, n := range notes {
		names = append(names, n.Name)
	}
	return map[string]any{
		"publisher": custpublish.PublisherActivityCustomer, "adapters_do_not_enable": true,
		"registered_adapters": names, "platforms": platforms,
	}
}

func publishAttemptPayload(row store.CustomerPublishAttempt, pkg *custpublish.Package) map[string]any {
	attempt := row.DomainAttempt()
	reward := custpublish.RewardDecision(attempt)
	unknown := func() map[string]any {
		m := custpublish.InterpretMetric(false, nil)
		return map[string]any{"available": m.Available, "value": m.Value, "reason": m.Reason}
	}
	out := map[string]any{
		"attempt_id": row.ID, "status": row.Status, "platform": row.Platform,
		"publisher": row.Publisher, "copy": row.Copy, "account_label": row.AccountLabel,
		"content_version": row.ContentVersion, "platform_post_id": row.PlatformPostID,
		"counts_as_published": attempt.CountsAsPublished(), "publish_success": attempt.CountsAsPublished(),
		"reward_triggered": reward.Trigger, "reward_reason": reward.Reason,
		"outbound_calls": row.OutboundCalls, "confirmation_current": attempt.ConfirmationCurrent(),
		"self_reported": row.SelfReported,
		"engagement": map[string]any{
			"completion": unknown(), "likes": unknown(), "plays": unknown(), "poi_exposure": unknown(),
		},
	}
	if pkg != nil {
		out["package"] = map[string]any{"post_id": pkg.PostID, "caption": pkg.Caption, "steps": pkg.Steps}
	}
	return out
}
