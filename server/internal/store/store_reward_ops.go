package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
)

// HUI-1671 B3 real-reward ledger. Inventory batches, benefit instances, and
// the append-only operations trail. Every query is tenant-scoped and
// parameterized; the one-reward-per-slot guarantee is a database UNIQUE
// constraint, not an application check. Coupon codes are sensitive: they live
// in reward_benefits.code / reward_code_pool only, never in operations
// payloads, never in logs, never on the public surface.

const (
	RewardKindStoreCoupon  = "store_coupon"
	RewardKindImportedCode = "imported_code"
	RewardKindGiftDiscount = "gift_discount"

	RewardInventoryActive  = "active"
	RewardInventoryPaused  = "paused"
	RewardInventoryRetired = "retired"

	RewardStatusReserved = "reserved"
	RewardStatusIssued   = "issued"
	RewardStatusRedeemed = "redeemed"
	RewardStatusReversed = "reversed"
	RewardStatusExpired  = "expired"
)

// ErrRewardConflict marks a state conflict the caller must read back (already
// issued, already redeemed, slot reversed). ErrRewardStock marks an empty
// batch. Both are domain refusals, not transport errors.
var (
	ErrRewardConflict = errors.New("store: reward state conflict")
	ErrRewardStock    = errors.New("store: reward stock exhausted")
	ErrRewardInvalid  = errors.New("store: reward input invalid")
)

// rewardOccupiedStatuses are the benefit statuses that hold stock. Reversed
// and expired releases are deliberately excluded: corrections and lapsed
// reservations free the allocation again.
const rewardOccupiedStatuses = `('reserved','issued','redeemed')`

// RewardInventory is one batch (券批次): system store coupons, a legally
// imported code pool, or on-site gifts/discounts.
type RewardInventory struct {
	ID              string   `json:"id"`
	TenantID        string   `json:"tenant_id"`
	CampaignID      string   `json:"campaign_id"`
	StoreID         string   `json:"store_id"`
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	BenefitSlot     string   `json:"benefit_slot"`
	RuleVersion     int      `json:"rule_version"`
	AllowedEvidence []string `json:"allowed_evidence"`
	RuleRef         string   `json:"rule_ref"`
	TotalQty        int      `json:"total_qty"`
	AvailableQty    int      `json:"available_qty"`
	FaceValueMinor  int64    `json:"face_value_minor"`
	CostMinor       int64    `json:"cost_minor"`
	Currency        string   `json:"currency"`
	ValidFrom       string   `json:"valid_from"`
	ValidUntil      string   `json:"valid_until"`
	Status          string   `json:"status"`
	Note            string   `json:"note"`
	CreatedBy       string   `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
}

// NewRewardInventory is the creation input. For imported code batches Codes
// is required (1..500) and the batch quantity equals the pool size; the
// codes are never logged.
type NewRewardInventory struct {
	CampaignID      string
	StoreID         string
	Kind            string
	Name            string
	BenefitSlot     string
	RuleVersion     int
	AllowedEvidence []string
	RuleRef         string
	TotalQty        int
	Codes           []string
	FaceValueMinor  int64
	CostMinor       int64
	Currency        string
	ValidFrom       string
	ValidUntil      string
	Note            string
	CreatedBy       string
}

const rewardInventoryCols = `id,tenant_id,campaign_id,store_id,kind,name,benefit_slot,rule_version,allowed_evidence,rule_ref,total_qty,face_value_minor,cost_minor,currency,valid_from,valid_until,status,note,created_by,created_at`

func scanRewardInventory(allowed string, sc interface{ Scan(...any) error }) (RewardInventory, error) {
	var r RewardInventory
	var storeID, ruleRef, currency, from, until, note sql.NullString
	err := sc.Scan(&r.ID, &r.TenantID, &r.CampaignID, &storeID, &r.Kind, &r.Name, &r.BenefitSlot,
		&r.RuleVersion, &allowed, &ruleRef, &r.TotalQty, &r.FaceValueMinor, &r.CostMinor,
		&currency, &from, &until, &r.Status, &note, &r.CreatedBy, &r.CreatedAt)
	if err != nil {
		return RewardInventory{}, err
	}
	r.StoreID, r.RuleRef, r.Currency, r.ValidFrom, r.ValidUntil, r.Note =
		storeID.String, ruleRef.String, currency.String, from.String, until.String, note.String
	r.AllowedEvidence = strings.Split(allowed, ",")
	return r, nil
}

// CreateRewardInventory validates and inserts one batch plus, for imported
// code batches, its pool rows in the same transaction.
func (s *Store) CreateRewardInventory(tenantID string, in NewRewardInventory) (RewardInventory, error) {
	inv := RewardInventory{}
	name := strings.TrimSpace(in.Name)
	slot := strings.TrimSpace(in.BenefitSlot)
	kind := strings.TrimSpace(in.Kind)
	if name == "" || slot == "" || tenantID == "" || in.CampaignID == "" || in.RuleVersion < 1 || in.CreatedBy == "" {
		return inv, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	switch kind {
	case RewardKindStoreCoupon, RewardKindImportedCode, RewardKindGiftDiscount:
	default:
		return inv, fmt.Errorf("%w: unknown kind %q", ErrRewardInvalid, kind)
	}
	if in.FaceValueMinor < 0 || in.CostMinor < 0 || in.TotalQty < 0 {
		return inv, fmt.Errorf("%w: negative amount or quantity", ErrRewardInvalid)
	}
	ev := dedupeEvidence(in.AllowedEvidence)
	if len(ev) == 0 {
		return inv, fmt.Errorf("%w: no allowed evidence", ErrRewardInvalid)
	}
	if err := rewardops.ValidateSnapshot(rewardops.Snapshot{
		CampaignID: in.CampaignID, Version: in.RuleVersion, AllowedEvidence: ev, Slots: []string{slot},
	}); err != nil {
		return inv, fmt.Errorf("%w: %v", ErrRewardInvalid, err)
	}
	codes := make([]string, 0, len(in.Codes))
	seen := map[string]bool{}
	for _, c := range in.Codes {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			return inv, fmt.Errorf("%w: empty or duplicate code", ErrRewardInvalid)
		}
		seen[c] = true
		codes = append(codes, c)
	}
	total := in.TotalQty
	if kind == RewardKindImportedCode {
		if len(codes) == 0 || len(codes) > 500 {
			return inv, fmt.Errorf("%w: imported pool needs 1..500 codes", ErrRewardInvalid)
		}
		if in.TotalQty != 0 && in.TotalQty != len(codes) {
			return inv, fmt.Errorf("%w: total_qty must equal pool size", ErrRewardInvalid)
		}
		total = len(codes)
	} else if total < 1 {
		return inv, fmt.Errorf("%w: quantity must be at least 1", ErrRewardInvalid)
	}
	// The campaign must belong to this tenant (tenant mask), and a scoped
	// store must exist in the tenant.
	if _, err := s.GetCampaign(in.CampaignID, tenantID); err != nil {
		return inv, fmt.Errorf("%w: campaign not found in tenant", ErrNotFound)
	}
	if storeID := strings.TrimSpace(in.StoreID); storeID != "" {
		if _, err := s.GetStore(storeID, tenantID); err != nil {
			return inv, fmt.Errorf("%w: store not found in tenant", ErrNotFound)
		}
	}
	inv = RewardInventory{
		ID: newID("rinv_"), TenantID: tenantID, CampaignID: in.CampaignID,
		StoreID: strings.TrimSpace(in.StoreID), Kind: kind, Name: name, BenefitSlot: slot,
		RuleVersion: in.RuleVersion, AllowedEvidence: ev, RuleRef: strings.TrimSpace(in.RuleRef),
		TotalQty: total, FaceValueMinor: in.FaceValueMinor, CostMinor: in.CostMinor,
		Currency: strings.TrimSpace(in.Currency), ValidFrom: strings.TrimSpace(in.ValidFrom),
		ValidUntil: strings.TrimSpace(in.ValidUntil), Status: RewardInventoryActive,
		Note: strings.TrimSpace(in.Note), CreatedBy: in.CreatedBy, CreatedAt: now(),
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return RewardInventory{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO reward_inventory(`+rewardInventoryCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		inv.ID, inv.TenantID, inv.CampaignID, inv.StoreID, inv.Kind, inv.Name, inv.BenefitSlot,
		inv.RuleVersion, strings.Join(ev, ","), inv.RuleRef, inv.TotalQty, inv.FaceValueMinor,
		inv.CostMinor, inv.Currency, inv.ValidFrom, inv.ValidUntil, inv.Status, inv.Note,
		inv.CreatedBy, inv.CreatedAt); err != nil {
		return RewardInventory{}, err
	}
	for _, c := range codes {
		if _, err := tx.Exec(
			`INSERT INTO reward_code_pool(id,tenant_id,inventory_id,code,status,created_at) VALUES(?,?,?,?,?,?)`,
			newID("rcode_"), tenantID, inv.ID, c, "available", inv.CreatedAt); err != nil {
			return RewardInventory{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RewardInventory{}, err
	}
	inv.AvailableQty = total
	return inv, nil
}

func dedupeEvidence(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// ListRewardInventory returns the tenant's live batches for one campaign,
// oldest first, each with availability (reserved+issued+redeemed occupy).
func (s *Store) ListRewardInventory(tenantID, campaignID string) ([]RewardInventory, error) {
	rows, err := s.DB.Query(
		`SELECT `+rewardInventoryCols+`,
			(SELECT COUNT(*) FROM reward_benefits b
			  WHERE b.inventory_id = reward_inventory.id
			    AND b.status IN `+rewardOccupiedStatuses+`)
		FROM reward_inventory
		WHERE tenant_id=? AND campaign_id=? AND status <> ?
		ORDER BY created_at, id`, tenantID, campaignID, RewardInventoryRetired)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RewardInventory{}
	for rows.Next() {
		occupied := 0
		r, err := func() (RewardInventory, error) {
			inv, scErr := scanRewardInventory("", compositeScan{rows, &occupied})
			return inv, scErr
		}()
		if err != nil {
			return nil, err
		}
		r.AvailableQty = r.TotalQty - occupied
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRewardInventory loads one batch inside the tenant scope.
func (s *Store) GetRewardInventory(id, tenantID string) (RewardInventory, error) {
	row := s.DB.QueryRow(
		`SELECT `+rewardInventoryCols+`,
			(SELECT COUNT(*) FROM reward_benefits b
			  WHERE b.inventory_id = reward_inventory.id
			    AND b.status IN `+rewardOccupiedStatuses+`)
		FROM reward_inventory WHERE id=? AND tenant_id=?`, id, tenantID)
	occupied := 0
	inv, err := scanRewardInventory("", compositeScan{row, &occupied})
	if err != nil {
		return RewardInventory{}, err
	}
	inv.AvailableQty = inv.TotalQty - occupied
	return inv, nil
}

// SetRewardInventoryStatus pauses, resumes, or retires a batch. Retired is
// terminal; existing benefits keep their facts.
func (s *Store) SetRewardInventoryStatus(id, tenantID, status string) (RewardInventory, error) {
	switch status {
	case RewardInventoryActive, RewardInventoryPaused, RewardInventoryRetired:
	default:
		return RewardInventory{}, fmt.Errorf("%w: unknown inventory status", ErrRewardInvalid)
	}
	cur, err := s.GetRewardInventory(id, tenantID)
	if err != nil {
		return RewardInventory{}, err
	}
	if cur.Status == RewardInventoryRetired {
		return cur, nil
	}
	if _, err := s.DB.Exec(`UPDATE reward_inventory SET status=? WHERE id=? AND tenant_id=?`, status, id, tenantID); err != nil {
		return RewardInventory{}, err
	}
	return s.GetRewardInventory(id, tenantID)
}

// RewardSnapshot is the rule binding the batch carries: which evidence this
// campaign version accepts for this slot. G3 records the version it was told
// and never edits campaign rules.
func (r RewardInventory) RewardSnapshot() rewardops.Snapshot {
	return rewardops.Snapshot{
		CampaignID:      r.CampaignID,
		Version:         r.RuleVersion,
		AllowedEvidence: r.AllowedEvidence,
		Slots:           []string{r.BenefitSlot},
	}
}

// compositeScan lets one extra aggregate column ride along a fixed column
// list without duplicating the scan order.
type compositeScan struct {
	sc   interface{ Scan(...any) error }
	extra *int
}

func (c compositeScan) Scan(dest ...any) error {
	return c.sc.Scan(append(dest, c.extra)...)
}

// popRewardCode atomically moves one available pool code to allocated.
// The code stays in the pool table with its benefit binding; it is never
// logged. An empty pool answers ErrRewardStock.
func (s *Store) popRewardCode(inventoryID string) (string, error) {
	var id, code string
	err := s.DB.QueryRow(
		`SELECT id, code FROM reward_code_pool
		 WHERE inventory_id=? AND status='available' ORDER BY rowid LIMIT 1`,
		inventoryID).Scan(&id, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRewardStock
	}
	if err != nil {
		return "", err
	}
	res, err := s.DB.Exec(
		`UPDATE reward_code_pool SET status='allocated' WHERE id=? AND status='available'`, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrRewardStock
	}
	return code, nil
}
