package httpapi

// handlers_admin_leads.go: merchant-side lead visibility (authenticated admin
// surface). The merchant is the authorized source domain, so contact data is
// returned here — but only within the tenant scope, through the standard
// membership middleware, and only for their own campaigns.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// POST /api/v1/campaigns/{id}/lead-form — enable/configure the fixed light form.
func (s *Server) handleLeadFormUpsert(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	// campaign scope first: another tenant's campaign must be invisible here,
	// and a store manager only reaches own-store campaigns (HUI-1674, 404 mask)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionCreate, w); !ok {
		return
	}
	// feature off -> the whole lead surface is invisible, admin side included:
	// configuring a form whose collection pipeline (forwarder) is not running
	// would silently dead-end, so the flag gates this surface too.
	if !s.leadsGate(w) {
		return
	}
	var in struct {
		NoticeVersion         string `json:"notice_version"`
		MarketingOptinEnabled *bool  `json:"marketing_optin_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	version := in.NoticeVersion
	if version == "" {
		version = "v1"
	}
	marketing := true
	if in.MarketingOptinEnabled != nil {
		marketing = *in.MarketingOptinEnabled
	}
	rec, err := s.St.UpsertLeadForm(c.Member.TenantID, r.PathValue("id"), version, marketing, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead form upsert failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// GET /api/v1/campaigns/{id}/leads — tenant-scoped submission list.
func (s *Server) handleLeadList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w); !ok {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	items, err := s.St.ListLeadSubmissions(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/campaigns/{id}/lead-stats — submissions vs anonymous views,
// deliberately two separate counters.
func (s *Server) handleLeadStats(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w); !ok {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	subs, views, err := s.St.LeadStats(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead stats failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": subs, "anonymous_views": views})
}

// GET /api/v1/campaigns/{id}/leads/{ref}/audit — minimal audit trail.
func (s *Server) handleLeadAudit(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w); !ok {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	// ref must belong to this tenant+campaign
	lead, err := s.St.GetLeadSubmissionByRef(r.PathValue("ref"))
	if err != nil || lead.TenantID != c.Member.TenantID || lead.CampaignID != r.PathValue("id") {
		fail(w, http.StatusNotFound, "not_found", "lead not found")
		return
	}
	rows, err := s.St.ListLeadAudit(c.Member.TenantID, lead.SubmissionRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "audit list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

// POST /api/v1/stores/{id}/lead-form — HUI-4594: enable/configure the store
// level lead form (无活动留资的商家开关). Store-scoped mirror of the campaign
// form upsert: the caller needs ActionCreate within this store's scope.
func (s *Server) handleStoreLeadFormUpsert(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.storeScoped(c, r.PathValue("id"), authz.ActionCreate, w); !ok {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	var in struct {
		NoticeVersion         string `json:"notice_version"`
		MarketingOptinEnabled *bool  `json:"marketing_optin_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	version := in.NoticeVersion
	if version == "" {
		version = "v1"
	}
	marketing := true
	if in.MarketingOptinEnabled != nil {
		marketing = *in.MarketingOptinEnabled
	}
	rec, err := s.St.UpsertLeadFormForStore(c.Member.TenantID, r.PathValue("id"), version, marketing, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead form upsert failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// storeScoped fetches the store within the caller's tenant and enforces the
// member's scope on it (same masking contract as campaignScoped).
func (s *Server) storeScoped(c *caller, id string, action authz.Action, w http.ResponseWriter) (store.StoreRecord, bool) {
	rec, err := s.St.GetStore(id, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return store.StoreRecord{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return store.StoreRecord{}, false
	}
	if !s.requireScopedAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, false, w) {
		return store.StoreRecord{}, false
	}
	return rec, true
}

// GET /api/v1/work/leads — HUI-4594 M11: the tenant-wide authorized-lead view
// across both capture sources (campaign + store), with honest stats. The
// merchant is the authorized source domain, so contact fields are in scope
// here (same discipline as the campaign lead list), but only inside the
// standard membership middleware and the caller's store scope:
//   - org_owner / tenant-level staff / active agent: whole tenant;
//   - store_manager / store-scoped staff: rows of their own store only.
//
// 口径(裁定书): 已获客只数 receiver 确认(持久化 ACK 的 crm_received);
// 待同步(pending_sync) 是独立桶,永不算已获客;撤回单列。
func (s *Server) handleWorkLeads(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	c := callerFrom(r)
	scopeStore := callerStoreScope(c) // '' = tenant level
	if !s.requireScopedAction(c, authz.ActionReadList, authz.RecordScope{
		TenantID: c.Member.TenantID, StoreID: scopeStore,
	}, false, w) {
		return
	}
	rows, err := s.St.ListLeadsByTenant(c.Member.TenantID, 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead list failed")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, lead := range rows {
		if scopeStore != "" && lead.StoreID != scopeStore {
			continue // store-scope members only see their own store's rows
		}
		items = append(items, workLeadView(lead))
	}
	counts, err := s.St.CountTenantLeadStates(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead stats failed")
		return
	}
	stats := map[string]int{"authorized_total": 0, "pending_sync": 0, "receiver_received": 0, "revoked": 0}
	for _, row := range counts {
		stats["authorized_total"] += row.Count
		switch {
		case row.SyncState == "revoked":
			stats["revoked"] += row.Count
		case row.SyncState == "crm_received" && row.ReceiverState != "":
			stats["receiver_received"] += row.Count // 持久化 ACK 落库=已获客
		case row.SyncState == "pending_sync":
			stats["pending_sync"] += row.Count // 待同步≠已获客
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "stats": stats})
}

// workLeadView is the whitelisted item shape: provenance + sync/receiver
// projection. Internal bookkeeping (dedup_key, consent_ip_fp, attempts,
// sync_error text) never leaves.
func workLeadView(lead store.LeadSubmission) map[string]any {
	revoked := lead.SyncState == "revoked" || lead.RevokedAt != ""
	return map[string]any{
		"submission_ref":         lead.SubmissionRef,
		"capture_source":         lead.CaptureSource,
		"campaign_id":            lead.CampaignID,
		"store_id":               lead.StoreID,
		"name":                   lead.Name,
		"phone":                  lead.Phone,
		"marketing_optin":        lead.MarketingOptin,
		"consent_notice_version": lead.ConsentNoticeVersion,
		"consent_at":             lead.ConsentAt,
		"sync_state":             lead.SyncState,
		"receiver_state":         lead.ReceiverState,
		"has_receipt":            lead.ReceiptID != "",
		"revoked":                revoked,
		"created_at":             lead.CreatedAt,
	}
}
