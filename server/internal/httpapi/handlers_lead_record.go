package httpapi

// handlers_lead_record.go: HUI-4594 trusted leads-receiver seam.
//
// 纪律(裁定书):
//   - GET /api/v1/internal/lead-records/{submissionId}: 联系方式离开本进程的
//     唯一通道。只给 TOUCH_SERVICE_TOKENS 受信方(lead_receiver 角色,常量时间
//     比较,registry 未配置 fail-closed 503);X-Leads-Tenant 声明租入租户并与
//     线索归属租户核对,不一致统一 404(不泄漏存在性);
//   - 返回最小必要字段: 身份三件套 + source_revision/consent_revision/purpose
//     授权元数据;内部簿记字段(sync_error/consent_ip/dedup_key/attempts)永不外发;
//   - 撤回只给 tombstone: 取数即重新核同意——已撤销(水位>0)的记录零联系字段,
//     authorization=revoked,source_revision=撤回水位(接收方据此单调收敛);
//   - POST …/acknowledgements: 持久化 ACK。五元组(receipt_id/source_event_id/
//     submission_id/source_revision/receiver_state)齐且通过校验才结束同步;
//     撤回水位拦晚到重放复活;同 receipt_id 同负载幂等、异负载 409。
//   - 旧 GET /internal/v1/lead-records(BFF 内部令牌取联系方式)已退役:
//     BFF 共享令牌不是受信接收方身份。

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// leadReceiverRole is the TOUCH_SERVICE_ROLES value that marks a registered
// service as a leads ingest receiver. A service without the entry defaults to
// queue_producer and is refused here (fail-closed).
const leadReceiverRole = "lead_receiver"

// requireLeadReceiver is the leads twin of requireServiceProducer: same
// per-service token seam but the lead_receiver role, and NO session fallback —
// the merchant surface can never read this seam (unlike the ops-grant twin,
// which falls through to its handler on a missing service identity).
func (s *Server) requireLeadReceiver(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceID := strings.TrimSpace(r.Header.Get(serviceIDHeader))
		if serviceID == "" {
			s.auditLeadReceiver("rejected", "", "missing_service_identity")
			fail(w, http.StatusUnauthorized, "unauthorized", "leads records are served to registered receivers only")
			return
		}
		if len(s.Cfg.ServiceTokens) == 0 {
			s.auditLeadReceiver("unavailable", serviceID, "registry_not_configured")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "leads_receiver_unavailable",
				"message": "trusted service registry is not configured; refusing to act (fail-closed)",
			})
			return
		}
		want, registered := s.Cfg.ServiceTokens[serviceID]
		got := strings.TrimSpace(r.Header.Get(serviceTokenHeader))
		if !registered || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			// Uniform refusal: never disclose whether the service id exists.
			s.auditLeadReceiver("rejected", serviceID, "bad_service_identity")
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong service identity")
			return
		}
		if s.Cfg.ServiceProducerRole(serviceID) != leadReceiverRole {
			s.auditLeadReceiver("rejected", serviceID, "role_not_lead_receiver")
			fail(w, http.StatusForbidden, "forbidden", "service identity lacks the lead_receiver role")
			return
		}
		s.auditLeadReceiver("accepted", serviceID, "")
		ctx := context.WithValue(r.Context(), serviceProducerKey, &serviceProducer{ServiceID: serviceID})
		next(w, r.WithContext(ctx))
	})
}

// auditLeadReceiver logs the auth decision. Never logs tokens.
func (s *Server) auditLeadReceiver(result, serviceID, reason string) {
	if s == nil || s.Log == nil {
		return
	}
	s.Log.Printf("leads_receiver_auth service=%s result=%s reason=%s", serviceID, result, reason)
}

// leadReceiver re-checks the verified service identity inside the handler and
// returns the declared ingesting tenant. The tenant is a header the receiver
// must present: it is verified against the lead row, never trusted blindly.
func (s *Server) leadReceiver(w http.ResponseWriter, r *http.Request) (serviceID, tenantID string, ok bool) {
	p := serviceProducerFrom(r.Context())
	if p == nil || p.ServiceID == "" {
		s.auditLeadReceiver("rejected", "", "missing_verified_identity")
		fail(w, http.StatusUnauthorized, "unauthorized", "leads records are served to registered receivers only")
		return "", "", false
	}
	tenantID = strings.TrimSpace(r.Header.Get("X-Leads-Tenant"))
	if tenantID == "" {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return "", "", false
	}
	return p.ServiceID, tenantID, true
}

// GET /api/v1/internal/lead-records/{submissionId}
func (s *Server) handleLeadRecord(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	_, tenantID, ok := s.leadReceiver(w, r)
	if !ok {
		return
	}
	ref := strings.TrimSpace(r.PathValue("submissionId"))
	lead, err := s.St.GetLeadSubmissionByRef(ref)
	// tenant mismatch and unknown ref are the same uniform 404: the declared
	// tenant must own the record for it to exist at all.
	if err != nil || lead.TenantID != tenantID {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	writeJSON(w, http.StatusOK, s.leadRecordView(r, lead))
}

// leadRecordView is the minimal authorized field set. Contact fields are
// present only while the consent is live; a revoked record is a tombstone —
// fetching re-checks the consent, so a late replay cannot re-serve contact data.
func (s *Server) leadRecordView(r *http.Request, lead store.LeadSubmission) map[string]any {
	revoked := lead.RevokeSourceRevision > 0 || lead.SyncState == "revoked" || lead.RevokedAt != ""
	revision := lead.SourceVersion
	if revoked && lead.RevokeSourceRevision > 0 {
		revision = lead.RevokeSourceRevision // the waterline the target converges to
	}
	rec := map[string]any{
		"submission_ref":   lead.SubmissionRef,
		"tenant_id":        lead.TenantID,
		"email":            "",
		"authorization":    "granted",
		"purpose":          "lead_submission",
		"source_revision":  revision,
		"consent_revision": lead.ConsentNoticeVersion,
		"campaign_ref":     lead.CampaignID,
		"store_ref":        lead.StoreID,
		"capture_source":   lead.CaptureSource,
		"consents": map[string]any{
			"form_authorized": !revoked,
			"channel_reply":   false,
			"marketing_phone": lead.MarketingOptin && !revoked,
			"marketing_sms":   lead.MarketingOptin && !revoked,
			"notice_version":  lead.ConsentNoticeVersion,
		},
	}
	// action provenance (ticket contract: minimal fields include action_id):
	// resolvable only while the 3546 session reference is live and in scope.
	if lead.SessionID != "" && s.Participation != nil && s.Participation.SessionsReady() {
		if sref, err := s.Participation.LookupSessionRef(lead.SessionID); err == nil {
			rec["action_ref"] = sref.ActionID
		}
	}
	if revoked {
		rec["authorization"] = "revoked"
		rec["tombstone"] = true
		rec["marketing_optin_recorded"] = lead.MarketingOptin // provenance only, not a live grant
		if lead.RevokedAt != "" {
			rec["revoked_at"] = lead.RevokedAt
		}
		return rec
	}
	rec["name"] = lead.Name
	rec["phone"] = lead.Phone
	rec["wechat"] = lead.Wechat
	rec["channel_subject_ref"] = ""
	return rec
}

// POST /api/v1/internal/lead-records/{submissionId}/acknowledgements
//
// The persisted ACK. Schema is strict; all five members must be present or the
// sync is left untouched (400). Store-level invariants (unknown source event,
// revocation waterline, receipt id conflicts) are the same transaction that
// writes the receipt.
func (s *Server) handleLeadAck(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	serviceID, tenantID, ok := s.leadReceiver(w, r)
	if !ok {
		return
	}
	ref := strings.TrimSpace(r.PathValue("submissionId"))
	var in struct {
		ReceiptID      string `json:"receipt_id"`
		SourceEventID  string `json:"source_event_id"`
		SubmissionID   string `json:"submission_id"`
		SourceRevision int    `json:"source_revision"`
		ReceiverState  string `json:"receiver_state"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if strings.TrimSpace(in.SubmissionID) != ref {
		fail(w, http.StatusBadRequest, "ack_mismatch", "submission_id must match the record path")
		return
	}
	rec, dup, err := s.St.AckLeadReceipt(tenantID, ref, serviceID, store.LeadAck{
		ReceiptID:      in.ReceiptID,
		SourceEventID:  in.SourceEventID,
		SubmissionID:   in.SubmissionID,
		SourceRevision: in.SourceRevision,
		ReceiverState:  in.ReceiverState,
	})
	switch {
	case err == nil:
		status := http.StatusCreated
		if dup {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{
			"submission_ref": rec.SubmissionRef,
			"receiver_state": rec.ReceiverState,
			"duplicate":      dup,
		})
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	case errors.Is(err, store.ErrAckIncomplete):
		// a partial ACK is not an ACK: the sync state was left untouched
		fail(w, http.StatusBadRequest, "ack_incomplete", "all five ack members are required")
		return
	case errors.Is(err, store.ErrAckConflict):
		fail(w, http.StatusConflict, "idempotency_conflict", "same receipt id with a different payload")
		return
	case errors.Is(err, store.ErrUnknownSourceEvent):
		s.auditAckRefused(tenantID, ref, serviceID, in.ReceiptID, "unknown_source_event")
		fail(w, http.StatusConflict, "unknown_source_event", "ack references an event this side never sent")
		return
	case errors.Is(err, store.ErrRevocationWaterline):
		s.auditAckRefused(tenantID, ref, serviceID, in.ReceiptID, "revocation_waterline")
		fail(w, http.StatusConflict, "revocation_waterline", "late replay of a delivered record cannot resurrect a revoked contact")
		return
	default:
		fail(w, http.StatusInternalServerError, "internal", "ack persistence failed")
		return
	}
}

// auditAckRefused records refusals that matter for audit (fabricated events,
// resurrection attempts). It never carries contact data.
func (s *Server) auditAckRefused(tenantID, ref, serviceID, receiptID, reason string) {
	_ = s.St.AppendLeadAudit(tenantID, ref, "ack_refused",
		`{"reason":"`+reason+`","receipt_id":"`+receiptID+`"}`, "receiver:"+serviceID)
}
