package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Public visitors may record an activity benefit. This path does not create a
// platform user, does not read a wallet, and does not accept a cash disguise.
func (s *Server) handlePublicBenefitClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "benefit claims are posted")
		return
	}
	code := strings.TrimSpace(r.PathValue("code"))
	res := s.St.ResolveLink(code, time.Now())
	if res.Outcome != store.OutcomeAvailable {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": string(res.Outcome)})
		return
	}
	var body struct {
		Kind           string `json:"kind"`
		FaceMinor      int64  `json:"face_minor"`
		Op             string `json:"op"`
		AsPlatformCash bool   `json:"as_platform_cash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "benefit claim is not valid JSON")
		return
	}
	var book activityspend.Ledger
	fact, err := activityspend.PostBenefit(&book, activityspend.BenefitInput{
		CampaignID:     res.Campaign.ID,
		Kind:           body.Kind,
		FaceMinor:      body.FaceMinor,
		Op:             body.Op,
		VisitorRef:     newGuestRef(),
		AsPlatformCash: body.AsPlatformCash,
	})
	if err != nil {
		if errors.Is(err, activityspend.ErrFaceIsNotCash) {
			fail(w, http.StatusUnprocessableEntity, "benefit_face_is_not_platform_cash", "券面额不能写成平台现金支出")
			return
		}
		fail(w, http.StatusBadRequest, "bad_request", "benefit claim is not an activity benefit")
		return
	}
	tenantID := res.Campaign.TenantID
	if tenantID == "" {
		tenantID = res.Link.TenantID
	}
	if err := s.St.InsertActivityBenefit(tenantID, fact); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "benefit was not recorded")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ledger":                fact.Ledger,
		"kind":                  fact.Kind,
		"face_minor":            fact.FaceMinor,
		"platform_user_created": false,
		"wallet_debited_minor":  0,
		"org_balance_visible":   false,
	})
}

func newGuestRef() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "guest_unavailable"
	}
	return "guest_" + hex.EncodeToString(buf[:])
}

// Client payer_authorized is ignored. The payer authorization is resolved
// server-side (HUI-4591 接线 4593 payer_grant_ref 真值): org_owner 恒持有
// billing:manage_payer;agent 仅当激活代管关系含 renewal 且 payer_grant_ref
// 非空。订阅资格按租户事实(HUI-4591 投影),无投影行回退进程级 Cfg。
func (s *Server) handleAccountCharge(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "membership required")
		return
	}
	var body struct {
		Capability       string `json:"capability"`
		Invoked          bool   `json:"invoked"`
		PayerAccountID   string `json:"payer_account_id"`
		PersonalWalletID string `json:"personal_wallet_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "charge request is not valid JSON")
		return
	}
	_, effective := s.billingSubscriptionFact(c.Member.TenantID)
	decision := activityspend.DecideCharge(activityspend.ChargeInput{
		Capability:       body.Capability,
		Invoked:          body.Invoked,
		ActorRole:        c.Member.Role,
		PayerAuthorized:  s.callerHasPayerGrant(c),
		PayerAccountID:   body.PayerAccountID,
		PersonalWalletID: body.PersonalWalletID,
		Subscription:     billingEntitlementStatus(effective),
	})
	writeJSON(w, http.StatusOK, decision)
}

func (s *Server) handleAccountSeparation(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.allowScopedList(c, w) {
		return
	}
	view, err := s.separationFor(c)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "account separation failed")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleAccountRecharge(w http.ResponseWriter, r *http.Request) {
	_ = callerFrom(r)
	fail(w, http.StatusForbidden, "production_not_authorized", "没有生产充值授权，已有活动不会被锁住")
}

func (s *Server) separationFor(c *caller) (activityspend.SeparationView, error) {
	benefits, err := s.St.ListActivityBenefits(c.Member.TenantID)
	if err != nil {
		return activityspend.SeparationView{}, err
	}
	var campaigns []store.Campaign
	if callerIsStoreManager(c) {
		campaigns, err = s.St.ListCampaignsByStore(c.Member.TenantID, callerStoreScope(c))
	} else {
		campaigns, err = s.St.ListCampaigns(c.Member.TenantID)
	}
	if err != nil {
		return activityspend.SeparationView{}, err
	}
	allowed := map[string]bool{}
	book := activityspend.Ledger{}
	for _, campaign := range campaigns {
		allowed[campaign.ID] = true
		book.CampaignIDs = append(book.CampaignIDs, campaign.ID)
	}
	for _, fact := range benefits {
		if allowed[fact.CampaignID] {
			book.Benefits = append(book.Benefits, fact)
		}
	}
	// HUI-4591 接线:订阅三栏状态按租户事实(有投影行用投影;无行回退 Cfg)。
	_, effective := s.billingSubscriptionFact(c.Member.TenantID)
	return activityspend.Separation(book, activityspend.Subscription{Status: billingEntitlementStatus(effective)}, activityspend.ChargeDecision{}), nil
}
