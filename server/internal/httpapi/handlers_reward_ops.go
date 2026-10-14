// handlers_reward_ops.go: HUI-1671 B3 真实奖励交付的 HTTP 治理面。
//
// 纪律:
//   - /work 管理面（session + authz）不加 FEATURE 开关（仓内惯例：后台功能
//     不加 FEATURE）。店员确认/核销走 authz.ActionUpdate + 门店 scope
//     （store_manager 仅本店，agent 拒写，禁用成员拒绝）；批次/撤销走
//     authz.ActionManagePublishReward（org_owner 专属，店员不因此获得
//     预算权）。
//   - 受信预留端点 /internal/v1/reward-reservations 只收 internal token
//     （G2 参与服务受信调用；入参=受信参与引用，本票不反向调用 G2）。
//   - 公共访客可达端口（短码 Summary）走 FEATURE_REWARD_OPS 默认 off：
//     off = 路由不注册 + handler gate 统一 404（双保险，FEATURE_DASHBOARD
//     同一写法）。只发白名单计数，无券码、无面额成本、无内部 token。
//   - 服务端单点判定：adjudication 与账本事务都在 Go 侧完成，BFF 零业务
//     判断。敏感券码只在管理面（authenticated）出现在权益行内，不进错误
//     文案、不进公共端口。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// rewardOpsGate answers uniform 404 when FEATURE_REWARD_OPS is off: an
// invisible surface (rulesGate/dashGate 同一纪律).
func (s *Server) rewardOpsGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureRewardOps {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// registerRewardOpsRoutes is called once from Handler(). Keeping the block in
// one place makes the server.go patch a single line.
func (s *Server) registerRewardOpsRoutes(mux *http.ServeMux) {
	// /work admin surface (session + authz; no feature flag).
	mux.Handle("GET /api/v1/campaigns/{id}/reward-inventory", s.requireSession(s.handleRewardInventoryList))
	mux.Handle("POST /api/v1/campaigns/{id}/reward-inventory", s.requireSession(s.handleRewardInventoryCreate))
	mux.Handle("POST /api/v1/campaigns/{id}/reward-inventory/{invId}/status", s.requireSession(s.handleRewardInventoryStatus))
	mux.Handle("GET /api/v1/campaigns/{id}/reward-benefits", s.requireSession(s.handleRewardBenefitsList))
	mux.Handle("GET /api/v1/campaigns/{id}/reward-review-requests", s.requireSession(s.handleRewardReviewRequests))
	mux.Handle("POST /api/v1/campaigns/{id}/reward-confirm", s.requireSession(s.handleRewardConfirm))
	mux.Handle("POST /api/v1/campaigns/{id}/reward-benefits/{benefitId}/redeem", s.requireSession(s.handleRewardRedeem))
	mux.Handle("POST /api/v1/campaigns/{id}/reward-benefits/{benefitId}/reverse", s.requireSession(s.handleRewardReverse))
	mux.Handle("GET /api/v1/campaigns/{id}/reward-summary", s.requireSession(s.handleRewardSummary))

	// Trusted service call (internal token; G2 participation service).
	mux.Handle("POST /internal/v1/reward-reservations", s.requireInternal(s.handleRewardReserve))

	// Guest-reachable summary, FEATURE_REWARD_OPS gated (default off).
	if s.Cfg.FeatureRewardOps {
		mux.Handle("GET /api/v1/public/links/{code}/reward-summary", http.HandlerFunc(s.handlePublicRewardSummary))
	}
}

// rewardError maps ledger refusals onto honest HTTP answers. Conflict (a
// processed fact to read back is NOT an error; a state clash is) maps to 409,
// stock/unavailable/bad-input to 422/400, foreign rows to 404.
func rewardError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "reward record not found")
	case errors.Is(err, store.ErrRewardStock):
		fail(w, http.StatusUnprocessableEntity, "out_of_stock", "奖励库存已用完，停止新的承诺。")
	case errors.Is(err, store.ErrRewardUnavailable):
		fail(w, http.StatusUnprocessableEntity, "batch_unavailable", "该批次已暂停或不在有效期，停止新的承诺。")
	case errors.Is(err, store.ErrRewardConflict):
		fail(w, http.StatusConflict, rewardReason(err), rewardConflictText(err))
	case errors.Is(err, store.ErrRewardInvalid):
		fail(w, http.StatusUnprocessableEntity, rewardReason(err), "奖励请求没有被接受。")
	default:
		fail(w, http.StatusInternalServerError, "internal", "reward ledger write failed")
	}
}

// rewardReason extracts the machine reason from a wrapped ledger error.
func rewardReason(err error) string {
	msg := err.Error()
	for _, reason := range []string{
		"slot_reversed", "not_issued", "reservation_expired", "nothing_active_to_reverse",
		"slot_belongs_to_other_campaign", "official_fact_consumed", "fabricated_official_receipt",
		"official_evidence_required", "evidence_not_allowed", "unknown_evidence",
		"missing_field", "campaign_mismatch", "slot_unknown", "rule_version_mismatch", "store_mismatch",
	} {
		if strings.Contains(msg, reason) {
			return reason
		}
	}
	return "reward_conflict"
}

func rewardConflictText(err error) string {
	switch rewardReason(err) {
	case "slot_reversed":
		return "该权益已被更正撤销，不能再发放。"
	case "not_issued":
		return "权益还没有发放，不能直接核销。"
	case "reservation_expired":
		return "预留已过期，请重新确认。"
	default:
		return "奖励状态不允许这个操作。"
	}
}

// readRewardBody decodes a bounded JSON body into in, rejecting unknown
// fields (DisallowUnknownFields 同 store-motion 纪律).
func readRewardBody(w http.ResponseWriter, r *http.Request, in any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "request body must be JSON")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(in); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			fail(w, http.StatusBadRequest, "forbidden_field", "请求包含不允许的字段。")
			return false
		}
		fail(w, http.StatusBadRequest, "bad_json", "request body must be valid JSON")
		return false
	}
	return true
}

// ---- inventory (批次/预算, org_owner) ---------------------------------------

type rewardInventoryBody struct {
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	BenefitSlot     string   `json:"benefit_slot"`
	RuleVersion     int      `json:"rule_version"`
	AllowedEvidence []string `json:"allowed_evidence"`
	RuleRef         string   `json:"rule_ref"`
	TotalQty        int      `json:"total_qty"`
	Codes           []string `json:"codes"`
	FaceValueMinor  int64    `json:"face_value_minor"`
	CostMinor       int64    `json:"cost_minor"`
	Currency        string   `json:"currency"`
	StoreID         string   `json:"store_id"`
	ValidFrom       string   `json:"valid_from"`
	ValidUntil      string   `json:"valid_until"`
	Note            string   `json:"note"`
}

func (s *Server) handleRewardInventoryList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w)
	if !ok {
		return
	}
	items, err := s.St.ListRewardInventory(camp.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reward inventory lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRewardInventoryCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManagePublishReward, w)
	if !ok {
		return
	}
	var body rewardInventoryBody
	if !readRewardBody(w, r, &body) {
		return
	}
	inv, err := s.St.CreateRewardInventory(camp.TenantID, store.NewRewardInventory{
		CampaignID: camp.ID, StoreID: body.StoreID, Kind: body.Kind, Name: body.Name,
		BenefitSlot: body.BenefitSlot, RuleVersion: body.RuleVersion,
		AllowedEvidence: body.AllowedEvidence, RuleRef: body.RuleRef,
		TotalQty: body.TotalQty, Codes: body.Codes,
		FaceValueMinor: body.FaceValueMinor, CostMinor: body.CostMinor,
		Currency: body.Currency, ValidFrom: body.ValidFrom, ValidUntil: body.ValidUntil,
		Note: body.Note, CreatedBy: c.Member.PrincipalRef,
	})
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) handleRewardInventoryStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManagePublishReward, w)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !readRewardBody(w, r, &body) {
		return
	}
	inv, err := s.St.SetRewardInventoryStatus(r.PathValue("invId"), camp.TenantID, body.Status)
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// ---- lists (店员可见) --------------------------------------------------------

func (s *Server) handleRewardBenefitsList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w)
	if !ok {
		return
	}
	storeID := strings.TrimSpace(r.URL.Query().Get("store_id"))
	items, err := s.St.ListRewardBenefits(camp.TenantID, camp.ID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reward benefits lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRewardReviewRequests(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w)
	if !ok {
		return
	}
	items, err := s.St.ListRewardReviewRequests(camp.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "review queue lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- confirm / redeem / reverse ---------------------------------------------

type rewardConfirmBody struct {
	ParticipationID string `json:"participation_id"`
	BenefitSlot     string `json:"benefit_slot"`
	RuleVersion     int    `json:"rule_version"`
	EventID         string `json:"event_id"`
	Evidence        string `json:"evidence"`
	Official        struct {
		FactID         string `json:"fact_id"`
		PlatformPostID string `json:"platform_post_id"`
		ReceiptSource  string `json:"receipt_source"`
	} `json:"official"`
	StoreID       string `json:"store_id"`
	Note          string `json:"note"`
	FulfillNow    bool   `json:"fulfill_now"`
	RedeemEventID string `json:"redeem_event_id"`
}

func (s *Server) handleRewardConfirm(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	var body rewardConfirmBody
	if !readRewardBody(w, r, &body) {
		return
	}
	// Store/action permission rides the existing matrix: org_owner/staff
	// tenant-wide, store_manager only in own store, agents refused.
	decision := authz.Authorize(s.authzMember(c), authz.ActionUpdate,
		authz.RecordScope{TenantID: camp.TenantID, StoreID: body.StoreID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to confirm rewards")
		return
	}
	ben, outcome, err := s.St.ConfirmRewardBenefit(store.RewardConfirmInput{
		TenantID: camp.TenantID, CampaignID: camp.ID,
		ParticipationID: body.ParticipationID, Slot: body.BenefitSlot,
		RuleVersion: body.RuleVersion, EventID: body.EventID, Evidence: body.Evidence,
		Official: rewardops.OfficialRef{
			FactID: body.Official.FactID, PlatformPostID: body.Official.PlatformPostID,
			ReceiptSource: body.Official.ReceiptSource,
		},
		Actor: c.Member.PrincipalRef, StoreID: body.StoreID, Note: body.Note,
		FulfillNow: body.FulfillNow, RedeemEventID: body.RedeemEventID,
		Now: nowRFC3339(),
	})
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"benefit": ben, "outcome": outcome.Outcome,
		"applied": outcome.Applied, "replay": outcome.Replay})
}

type rewardRedeemBody struct {
	EventID string `json:"event_id"`
	StoreID string `json:"store_id"`
}

func (s *Server) handleRewardRedeem(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w)
	if !ok {
		return
	}
	var body rewardRedeemBody
	if !readRewardBody(w, r, &body) {
		return
	}
	ben, err := s.St.GetRewardBenefit(r.PathValue("benefitId"), camp.TenantID, camp.ID)
	if err != nil {
		rewardError(w, err)
		return
	}
	// The benefit's own store scopes the redemption (错店店员答 403).
	decision := authz.Authorize(s.authzMember(c), authz.ActionUpdate,
		authz.RecordScope{TenantID: camp.TenantID, StoreID: ben.StoreID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to redeem this benefit")
		return
	}
	out, outcome, err := s.St.RedeemRewardBenefit(camp.TenantID, camp.ID, ben.ID,
		c.Member.PrincipalRef, body.StoreID, body.EventID, nowRFC3339())
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"benefit": out, "outcome": outcome.Outcome,
		"applied": outcome.Applied, "replay": outcome.Replay})
}

func (s *Server) handleRewardReverse(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManagePublishReward, w)
	if !ok {
		return
	}
	var body struct {
		Reason  string `json:"reason"`
		EventID string `json:"event_id"`
	}
	if !readRewardBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "更正撤销必须注明原因。")
		return
	}
	ben, outcome, err := s.St.ReverseRewardBenefit(camp.TenantID, camp.ID, r.PathValue("benefitId"),
		c.Member.PrincipalRef, strings.TrimSpace(body.Reason), body.EventID, nowRFC3339())
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"benefit": ben, "outcome": outcome.Outcome,
		"applied": outcome.Applied, "replay": outcome.Replay})
}

// ---- summary (管理面 + 公共面) ----------------------------------------------

func (s *Server) handleRewardSummary(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadList, w)
	if !ok {
		return
	}
	sum, err := s.St.RewardSummary(camp.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reward summary failed")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// handlePublicRewardSummary is the guest-reachable, whitelisted counts port
// for the campaign homepage card (G1) and the participation service (G2). No
// codes, no face value, no cost, no internal tokens.
func (s *Server) handlePublicRewardSummary(w http.ResponseWriter, r *http.Request) {
	if !s.rewardOpsGate(w) {
		return
	}
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	sum, err := s.St.RewardSummary(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reward summary failed")
		return
	}
	slots := []map[string]any{}
	inventory, err := s.St.ListRewardInventory(res.Campaign.TenantID, res.Campaign.ID)
	if err == nil {
		for _, inv := range inventory {
			slots = append(slots, map[string]any{
				"benefit_slot": inv.BenefitSlot, "rule_version": inv.RuleVersion,
				"available_qty": inv.AvailableQty,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"campaign_id": res.Campaign.ID, "as_of": sum.AsOf,
		"counts": sum.Counts, "slots": slots,
	})
}

// ---- trusted reservation (G2) ------------------------------------------------

type rewardReserveBody struct {
	TenantID        string `json:"tenant_id"`
	CampaignID      string `json:"campaign_id"`
	ParticipationID string `json:"participation_id"`
	BenefitSlot     string `json:"benefit_slot"`
	RuleVersion     int    `json:"rule_version"`
	EventID         string `json:"event_id"`
	TTLSeconds      int    `json:"ttl_seconds"`
	Actor           string `json:"actor"`
}

// handleRewardReserve is the trusted participation-service entry (internal
// token only). The participation reference is opaque: this ticket never calls
// back into G2 (no G2→G3→G2 wait cycle), and pure browsing reaches nothing
// here.
func (s *Server) handleRewardReserve(w http.ResponseWriter, r *http.Request) {
	var body rewardReserveBody
	if !readRewardBody(w, r, &body) {
		return
	}
	ben, outcome, err := s.St.ReserveRewardBenefit(store.RewardReserveInput{
		TenantID: body.TenantID, CampaignID: body.CampaignID,
		ParticipationID: body.ParticipationID, Slot: body.BenefitSlot,
		RuleVersion: body.RuleVersion, EventID: body.EventID,
		Actor: body.Actor, TTLSeconds: body.TTLSeconds,
	}, nowRFC3339())
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"benefit": ben, "outcome": outcome.Outcome,
		"applied": outcome.Applied, "replay": outcome.Replay,
		"reserved_until": ben.ReservedUntil})
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
