package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"strings"
)

// PrepareRewardInventory contains only the merchant's inventory transaction.
// All promise bindings are read from the single publish-config authority.
type PrepareRewardInventory struct {
	RequestKey      string   `json:"request_key"`
	ExpectedVersion int      `json:"expected_version,omitempty"`
	Name            string   `json:"name"`
	Quantity        int      `json:"quantity"`
	Codes           []string `json:"codes,omitempty"`
	FaceValueMinor  int64    `json:"face_value_minor"`
	CostMinor       *int64   `json:"cost_minor,omitempty"`
}

// PreparedRewardInventory is a read-only recovery of the original request.
// It does not depend on today's promise version or on a browser's edited draft.
func (s *Store) PreparedRewardInventory(tenantID, campaignID, requestKey string) (RewardInventory, error) {
	if strings.TrimSpace(requestKey) == "" || len(requestKey) > 100 {
		return RewardInventory{}, fmt.Errorf("%w: request_key_required", ErrRewardInvalid)
	}
	digest := sha256.Sum256([]byte(tenantID + "/" + campaignID + "/" + requestKey))
	inv, err := s.GetRewardInventory("rinv_"+hex.EncodeToString(digest[:16]), tenantID)
	if err != nil {
		return RewardInventory{}, err
	}
	if inv.CampaignID != campaignID {
		return RewardInventory{}, ErrNotFound
	}
	return inv, nil
}

func (s *Store) PrepareRewardInventory(tenantID, campaignID string, in PrepareRewardInventory, actor string) (RewardInventory, error) {
	if strings.TrimSpace(in.RequestKey) == "" || len(in.RequestKey) > 100 {
		return RewardInventory{}, fmt.Errorf("%w: request_key_required", ErrRewardInvalid)
	}
	cfg, err := s.GetPublishConfig(tenantID, campaignID)
	if err != nil {
		return RewardInventory{}, err
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != cfg.Version {
		return RewardInventory{}, fmt.Errorf("%w: rule_version_mismatch", ErrRewardConflict)
	}
	if err = campaignenable.ValidateSpec(cfg.Spec); err != nil {
		return RewardInventory{}, fmt.Errorf("%w: invalid_promise", ErrRewardInvalid)
	}
	r := cfg.Spec.Reward
	if len(r.StoreIDs) != 1 || r.RedeemFrom == "" || r.RedeemUntil == "" || r.Slot == "" {
		return RewardInventory{}, fmt.Errorf("%w: save_complete_single_store_promise_first", ErrRewardInvalid)
	}
	kind := r.Kind
	if kind == "onsite_gift" || kind == "onsite_discount" {
		kind = RewardKindGiftDiscount
	}
	cost := int64(0)
	if in.CostMinor != nil {
		cost = *in.CostMinor
	}
	payload, _ := json.Marshal(in)
	digest := sha256.Sum256(payload)
	return s.CreateRewardInventory(tenantID, NewRewardInventory{
		CampaignID: campaignID, StoreID: r.StoreIDs[0], Kind: kind, Name: in.Name,
		BenefitSlot: r.Slot, RuleVersion: cfg.Version, AllowedEvidence: []string{cfg.Spec.VerificationMode},
		RuleRef:    fmt.Sprintf("publish-config:%s/v%d/%s", cfg.ID, cfg.Version, hex.EncodeToString(digest[:])),
		RequestKey: in.RequestKey, TotalQty: in.Quantity, Codes: in.Codes, FaceValueMinor: in.FaceValueMinor,
		CostMinor: cost, CostRecorded: in.CostMinor != nil, Currency: r.Currency,
		ValidFrom: r.RedeemFrom, ValidUntil: r.RedeemUntil, CreatedBy: actor,
	})
}
