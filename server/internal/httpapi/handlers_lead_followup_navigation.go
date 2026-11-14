package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

var followUpHandoff = regexp.MustCompile(`^hnd_[a-f0-9]{32}$`)

type followUpNavigationInput struct {
	SourceTenant string `json:"source_tenant_id"`
	SourceRef    string `json:"source_ref"`
	Revision     int    `json:"source_revision"`
	Principal    string `json:"principal_id"`
	Handoff      string `json:"handoff_id,omitempty"`
	StoreID      string `json:"origin_store_id,omitempty"`
}

func (s *Server) followUpNavigationCall(ctx context.Context, path string, in followUpNavigationInput) (int, map[string]any, error) {
	if s.Cfg.LeadsFollowUpBaseURL == "" || s.Cfg.LeadsFollowUpToken == "" {
		return 503, nil, errors.New("follow-up not configured")
	}
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(s.Cfg.LeadsFollowUpBaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return 503, nil, err
	}
	mac := hmac.New(sha256.New, []byte(s.Cfg.LeadsFollowUpToken))
	mac.Write([]byte("touch-follow-up/v1\n" + path + "\n"))
	mac.Write(raw)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", s.Cfg.LeadsFollowUpToken)
	req.Header.Set("X-Touch-Follow-Up-Signature", hex.EncodeToString(mac.Sum(nil)))
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return 503, nil, err
	}
	defer res.Body.Close()
	var out map[string]any
	err = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&out)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return res.StatusCode, out, err
}
func (s *Server) originalFollowUpSubmission(w http.ResponseWriter, r *http.Request, c *caller, ref string) (store.LeadSubmission, bool) {
	lead, err := s.St.GetLeadSubmissionByRef(ref)
	if err != nil || lead.TenantID != c.Member.TenantID {
		fail(w, 404, "not_found", "lead not found")
		return lead, false
	}
	if lead.StoreID == "" {
		fail(w, 409, "source_unknown", "线索没有可核验的原门店")
		return lead, false
	}
	if !s.followUpStore(c, lead.TenantID, lead.StoreID, w) {
		return lead, false
	}
	if lead.SyncState != "crm_received" || lead.ReceiverState != "stored" || lead.RevokedAt != "" || lead.RevokeSourceRevision > 0 {
		fail(w, 409, "follow_up_unavailable", "该线索尚未确认入库或许可已撤回，不能进入跟进")
		return lead, false
	}
	return lead, true
}
func (s *Server) handleWorkLeadFollowUp(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	c := callerFrom(r)
	lead, ok := s.originalFollowUpSubmission(w, r, c, r.PathValue("ref"))
	if !ok {
		return
	}
	in := followUpNavigationInput{SourceTenant: lead.TenantID, SourceRef: lead.SubmissionRef, Revision: lead.SourceVersion, Principal: c.Principal.ID}
	status, out, err := s.followUpNavigationCall(r.Context(), "/internal/v1/touch-follow-ups/resolve", in)
	if err != nil || status != 200 {
		if status < 400 || status > 599 {
			status = 503
		}
		fail(w, status, "follow_up_unavailable", "无法核验当前获客成员、原线索映射或顾客许可，请稍后重试")
		return
	}
	tenant, _ := out["crm_tenant_id"].(string)
	leadID, _ := out["lead_id"].(string)
	if tenant == "" || leadID == "" {
		fail(w, 502, "invalid_mapping", "没有唯一的获客线索映射")
		return
	}
	launch, err := s.ID.LaunchShell(r.Context(), c.Principal.ID, tenant, "leads-engine", "ti-leads-engine-web")
	if err != nil || !safeLaunchURL(launch) {
		fail(w, 503, "follow_up_target_unavailable", "获客详情入口当前不可用或尚未获平台登记，请稍后重试")
		return
	}
	u, _ := url.Parse(launch)
	h := u.Query().Get("h")
	if !followUpHandoff.MatchString(h) {
		fail(w, 502, "invalid_shell_handoff", "shell returned no usable handoff")
		return
	}
	n := store.FollowUpNavigation{HandoffID: h, Kind: "origin", PrincipalID: c.Principal.ID, SourceTenantID: lead.TenantID, SourceRef: lead.SubmissionRef, SourceRevision: lead.SourceVersion, StoreID: lead.StoreID, CRMTenantID: tenant, LeadID: leadID, ExpiresAt: time.Now().Add(30 * time.Minute).Unix()}
	if s.St.InsertFollowUpNavigation(n) != nil {
		fail(w, 409, "handoff_conflict", "handoff already registered")
		return
	}
	in.Handoff = h
	in.StoreID = lead.StoreID
	status, _, err = s.followUpNavigationCall(r.Context(), "/internal/v1/touch-follow-ups/register", in)
	if err != nil || status != 201 {
		if status < 400 || status > 599 {
			status = 503
		}
		fail(w, status, "follow_up_unavailable", "详情接续未完成登记，请重试")
		return
	}
	if s.Jumps == nil {
		fail(w, 503, "jump_unavailable", "jump service unavailable")
		return
	}
	minted, err := s.Jumps.Mint(c.Member.TenantID, c.Principal.ID, "leads-engine", "ti-leads-engine-web", launch)
	if err != nil {
		fail(w, 503, "jump_unavailable", "jump service unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"jump_token": minted.Token, "grant_id": minted.Grant.ID, "expires_at": minted.Grant.ExpiresAt})
}

// A trusted receiver can request a return only from the original server-stored
// source context. Neither a store id nor a return URL is accepted from it.
func (s *Server) handleLeadFollowUpReturnRegister(w http.ResponseWriter, r *http.Request) {
	_, tenant, ok := s.leadReceiver(w, r)
	if !ok {
		return
	}
	var in struct {
		Handoff   string `json:"handoff_id"`
		Principal string `json:"principal_id"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || !followUpHandoff.MatchString(in.Handoff) {
		fail(w, 400, "bad_request", "shell handoff required")
		return
	}
	n, err := s.St.GetFollowUpNavigation(r.PathValue("origin"))
	if err != nil || n.Kind != "origin" || n.SourceTenantID != tenant || n.PrincipalID != in.Principal {
		fail(w, 404, "not_found", "origin context not found")
		return
	}
	if n.ExpiresAt <= time.Now().Unix() {
		fail(w, 410, "return_expired", "origin context expired")
		return
	}
	member, err := s.St.GetMemberByPrincipal(tenant, in.Principal)
	if err != nil {
		fail(w, 403, "not_member", "source membership is no longer active")
		return
	}
	c := &caller{Member: &member}
	if !s.followUpStore(c, n.SourceTenantID, n.StoreID, w) {
		return
	}
	n.OriginHandoffID = n.HandoffID
	n.HandoffID = in.Handoff
	n.Kind = "return"
	n.VisitRef = ""
	n.UsedAt = 0
	n.ExpiresAt = time.Now().Add(time.Minute).Unix()
	if s.St.InsertFollowUpNavigation(n) != nil {
		fail(w, 409, "handoff_conflict", "return handoff already registered")
		return
	}
	writeJSON(w, 201, map[string]bool{"registered": true})
}
func (s *Server) handleLeadFollowUpReturnResume(w http.ResponseWriter, r *http.Request) {
	n, err := s.St.GetFollowUpNavigation(r.PathValue("h"))
	if err != nil || n.Kind != "return" {
		writeJSON(w, 200, map[string]bool{"known": false})
		return
	}
	r.Header.Set(tenantHeader, n.SourceTenantID)
	s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		c := callerFrom(r)
		if c.Principal.ID != n.PrincipalID {
			fail(w, 404, "not_found", "return context not found")
			return
		}
		if n.UsedAt != 0 || n.ExpiresAt <= time.Now().Unix() {
			fail(w, 410, "return_expired", "返回链接已使用或过期，请回获客详情重新发起")
			return
		}
		if !s.followUpStore(c, n.SourceTenantID, n.StoreID, w) {
			return
		}
		if s.St.ConsumeFollowUpNavigation(n.HandoffID, c.Principal.ID, time.Now()) != nil {
			fail(w, 410, "return_expired", "return handoff already used")
			return
		}
		writeJSON(w, 200, map[string]any{"known": true, "tenant_id": n.SourceTenantID, "path": "/work/stores/" + url.PathEscape(n.StoreID)})
	}).ServeHTTP(w, r)
}

// Follow-up navigation uses the same read action and current store scope,
// with the existing-record 404 mask on both forward and return paths.
func (s *Server) followUpStore(c *caller, tenant, id string, w http.ResponseWriter) bool {
	if _, err := s.St.GetStore(id, tenant); err != nil {
		fail(w, 404, "not_found", "store not found")
		return false
	}
	return s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: tenant, StoreID: id}, true, w)
}
