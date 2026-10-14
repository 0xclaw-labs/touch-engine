package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ErrRewardConflict    = errors.New("store: reward state conflict")
	ErrRewardStock       = errors.New("store: reward stock exhausted")
	ErrRewardUnavailable = errors.New("store: reward batch unavailable")
	ErrRewardInvalid     = errors.New("store: reward input invalid")
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
	if errors.Is(err, sql.ErrNoRows) {
		return RewardInventory{}, ErrNotFound
	}
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
	sc    interface{ Scan(...any) error }
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

// ---- benefit rows -----------------------------------------------------------

// RewardBenefit is one reward instance. Exactly one row exists per
// (tenant, participation, benefit_slot) forever.
type RewardBenefit struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	CampaignID      string `json:"campaign_id"`
	InventoryID     string `json:"inventory_id"`
	ParticipationID string `json:"participation_id"`
	BenefitSlot     string `json:"benefit_slot"`
	RuleVersion     int    `json:"rule_version"`
	Evidence        string `json:"evidence"`
	Code            string `json:"code,omitempty"`
	Status          string `json:"status"`
	StoreID         string `json:"store_id"`
	FaceValueMinor  int64  `json:"face_value_minor"`
	CostMinor       int64  `json:"cost_minor"`
	Currency        string `json:"currency"`
	ReservedAt      string `json:"reserved_at"`
	ReservedUntil   string `json:"reserved_until"`
	IssuedAt        string `json:"issued_at"`
	IssuedBy        string `json:"issued_by"`
	RedeemedAt      string `json:"redeemed_at"`
	RedeemedBy      string `json:"redeemed_by"`
	ReversedAt      string `json:"reversed_at"`
	ReversedBy      string `json:"reversed_by"`
	ReverseReason   string `json:"reverse_reason,omitempty"`
	Note            string `json:"note,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

const rewardBenefitCols = `id,tenant_id,campaign_id,inventory_id,participation_id,benefit_slot,rule_version,evidence,code,status,store_id,face_value_minor,cost_minor,currency,reserved_at,reserved_until,issued_at,issued_by,redeemed_at,redeemed_by,reversed_at,reversed_by,reverse_reason,note,created_at,updated_at`

func scanRewardBenefit(sc interface{ Scan(...any) error }) (RewardBenefit, error) {
	var b RewardBenefit
	err := sc.Scan(&b.ID, &b.TenantID, &b.CampaignID, &b.InventoryID, &b.ParticipationID,
		&b.BenefitSlot, &b.RuleVersion, &b.Evidence, &b.Code, &b.Status, &b.StoreID,
		&b.FaceValueMinor, &b.CostMinor, &b.Currency, &b.ReservedAt, &b.ReservedUntil,
		&b.IssuedAt, &b.IssuedBy, &b.RedeemedAt, &b.RedeemedBy, &b.ReversedAt, &b.ReversedBy,
		&b.ReverseReason, &b.Note, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

// RewardOutcome answers what THIS call did. Applied is false and Replay is
// true when the event had been processed before; the row fields still carry
// the current truth so the caller can read back the processed result.
type RewardOutcome struct {
	Outcome string // reserved / re_reserved / issued / issued_and_redeemed / already_issued / already_redeemed / already_reversed / redeemed / reversed
	Applied bool
	Replay  bool
}

// ListRewardBenefits returns the tenant's benefits for one campaign, newest
// first. storeID filters to one store (empty = all stores).
func (s *Store) ListRewardBenefits(tenantID, campaignID, storeID string) ([]RewardBenefit, error) {
	q := `SELECT ` + rewardBenefitCols + ` FROM reward_benefits
		WHERE tenant_id=? AND campaign_id=?`
	args := []any{tenantID, campaignID}
	if storeID != "" {
		q += ` AND store_id=?`
		args = append(args, storeID)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RewardBenefit{}
	for rows.Next() {
		b, err := scanRewardBenefit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetRewardBenefit loads one benefit inside the tenant (and campaign) scope.
func (s *Store) GetRewardBenefit(id, tenantID, campaignID string) (RewardBenefit, error) {
	row := s.DB.QueryRow(
		`SELECT `+rewardBenefitCols+` FROM reward_benefits WHERE id=? AND tenant_id=? AND campaign_id=?`,
		id, tenantID, campaignID)
	b, err := scanRewardBenefit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RewardBenefit{}, ErrNotFound
	}
	return b, err
}

type rewardOperationRow struct {
	ID      string
	Op      string
	Actor   string
	StoreID string
	Reason  string
	Created string
}

func (s *Store) listRewardOperations(tenantID, benefitID string) ([]rewardOperationRow, error) {
	rows, err := s.DB.Query(
		`SELECT id, op, actor, store_id, reason, created_at FROM reward_operations
		 WHERE tenant_id=? AND benefit_id=? ORDER BY created_at, id`, tenantID, benefitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rewardOperationRow{}
	for rows.Next() {
		var r rewardOperationRow
		if err := rows.Scan(&r.ID, &r.Op, &r.Actor, &r.StoreID, &r.Reason, &r.Created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- shared transaction helpers ---------------------------------------------

// beginRewardTx starts the ledger transaction. modernc/sqlite serializes
// writers on the single connection; the state machine below keeps its
// guarantees inside one transaction each.
func (s *Store) beginRewardTx() (*sql.Tx, error) {
	return s.DB.Begin()
}

// sweepDueInTx expires due reservations and posts one system trail row per
// benefit. It runs inside every mutating entry point so stock frees lazily
// and deterministically.
func sweepDueInTx(tx *sql.Tx, tenantID, nowTS string) (int, error) {
	rows, err := tx.Query(
		`SELECT id, tenant_id FROM reward_benefits
		 WHERE status='reserved' AND reserved_until <> '' AND reserved_until < ?`+tenantFilter(tenantID),
		nowTS)
	if err != nil {
		return 0, err
	}
	type due struct{ id, tenant string }
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.tenant); err != nil {
			rows.Close()
			return 0, err
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range dues {
		res, err := tx.Exec(
			`UPDATE reward_benefits SET status='expired', updated_at=? WHERE id=? AND status='reserved'`,
			nowTS, d.id)
		if err != nil {
			return 0, err
		}
		if cnt, _ := res.RowsAffected(); cnt == 0 {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO reward_operations(id,tenant_id,benefit_id,op,dedup_key,actor,store_id,reason,payload,created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			newID("rop_"), d.tenant, d.id, "expire", "", "system", "", "reservation_ttl", "", nowTS); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

func tenantFilter(tenantID string) string {
	if tenantID == "" {
		return ""
	}
	return ` AND tenant_id='` + strings.ReplaceAll(tenantID, "'", "''") + `'`
}

// pickInventoryInTx chooses the active batch for (tenant, campaign, slot,
// version). With requireFree it skips batches without availability. Batches
// are consumed oldest first so refills come later.
func pickInventoryInTx(tx *sql.Tx, tenantID, campaignID, slot string, ruleVersion int, requireFree bool) (RewardInventory, error) {
	rows, err := tx.Query(
		`SELECT `+rewardInventoryCols+`,
			(SELECT COUNT(*) FROM reward_benefits b
			  WHERE b.inventory_id = reward_inventory.id
			    AND b.status IN `+rewardOccupiedStatuses+`)
		FROM reward_inventory
		WHERE tenant_id=? AND campaign_id=? AND benefit_slot=? AND rule_version=? AND status='active'
		ORDER BY created_at, id`, tenantID, campaignID, slot, ruleVersion)
	if err != nil {
		return RewardInventory{}, err
	}
	defer rows.Close()
	for rows.Next() {
		occupied := 0
		inv, err := scanRewardInventory("", compositeScan{rows, &occupied})
		if err != nil {
			return RewardInventory{}, err
		}
		if !requireFree || occupied < inv.TotalQty {
			return inv, nil
		}
	}
	if err := rows.Err(); err != nil {
		return RewardInventory{}, err
	}
	return RewardInventory{}, sql.ErrNoRows
}

func inventoryUsable(inv RewardInventory, nowTS string) error {
	if inv.Status != RewardInventoryActive {
		return ErrRewardUnavailable
	}
	if inv.ValidFrom != "" && nowTS < inv.ValidFrom {
		return ErrRewardUnavailable
	}
	if inv.ValidUntil != "" && nowTS > inv.ValidUntil {
		return ErrRewardUnavailable
	}
	return nil
}

// occupiedInTx counts live occupancies of one batch.
func occupiedInTx(tx *sql.Tx, inventoryID string) (int, error) {
	var n int
	err := tx.QueryRow(
		`SELECT COUNT(*) FROM reward_benefits WHERE inventory_id=? AND status IN `+rewardOccupiedStatuses,
		inventoryID).Scan(&n)
	return n, err
}

// allocateCodeInTx binds the sensitive code to the benefit at first
// occupation: generated for store coupons, popped from the pool for imported
// batches, none for gifts.
func allocateCodeInTx(tx *sql.Tx, inv RewardInventory, benefitID string) (string, error) {
	switch inv.Kind {
	case RewardKindStoreCoupon:
		var b [10]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		return "SC" + hex.EncodeToString(b[:]), nil
	case RewardKindImportedCode:
		var id, code string
		err := tx.QueryRow(
			`SELECT id, code FROM reward_code_pool
			 WHERE inventory_id=? AND status='available' ORDER BY rowid LIMIT 1`,
			inv.ID).Scan(&id, &code)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrRewardStock
		}
		if err != nil {
			return "", err
		}
		res, err := tx.Exec(
			`UPDATE reward_code_pool SET status='allocated', allocated_benefit=? WHERE id=? AND status='available'`,
			benefitID, id)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "", ErrRewardStock
		}
		return code, nil
	default: // gift_discount: nothing to hand over materially
		return "", nil
	}
}

// replayInTx answers whether this dedup key was already posted. It returns
// the benefit the event acted on and the recorded operation.
func replayInTx(tx *sql.Tx, tenantID, dedupKey string) (RewardBenefit, string, bool, error) {
	var benefitID, op string
	err := tx.QueryRow(
		`SELECT benefit_id, op FROM reward_operations WHERE tenant_id=? AND dedup_key=?`,
		tenantID, dedupKey).Scan(&benefitID, &op)
	if errors.Is(err, sql.ErrNoRows) {
		return RewardBenefit{}, "", false, nil
	}
	if err != nil {
		return RewardBenefit{}, "", false, err
	}
	var b RewardBenefit
	row := tx.QueryRow(
		`SELECT `+rewardBenefitCols+` FROM reward_benefits WHERE id=? AND tenant_id=?`,
		benefitID, tenantID)
	b, err = scanRewardBenefit(row)
	if err != nil {
		return RewardBenefit{}, "", false, err
	}
	return b, op, true, nil
}

func postOpInTx(tx *sql.Tx, tenantID, benefitID, op, dedupKey, actor, storeID, reason, payload, nowTS string) error {
	_, err := tx.Exec(
		`INSERT INTO reward_operations(id,tenant_id,benefit_id,op,dedup_key,actor,store_id,reason,payload,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		newID("rop_"), tenantID, benefitID, op, dedupKey, actor, storeID, reason, payload, nowTS)
	return err
}

// insertBenefitInTx inserts the one-per-slot row. A unique violation means a
// concurrent writer won the slot; the caller reads back and answers the
// processed result instead of failing.
func insertBenefitInTx(tx *sql.Tx, b RewardBenefit) (RewardBenefit, error) {
	_, err := tx.Exec(
		`INSERT INTO reward_benefits(`+rewardBenefitCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.TenantID, b.CampaignID, b.InventoryID, b.ParticipationID, b.BenefitSlot,
		b.RuleVersion, b.Evidence, b.Code, b.Status, b.StoreID, b.FaceValueMinor, b.CostMinor,
		b.Currency, b.ReservedAt, b.ReservedUntil, b.IssuedAt, b.IssuedBy, b.RedeemedAt,
		b.RedeemedBy, b.ReversedAt, b.ReversedBy, b.ReverseReason, b.Note, b.CreatedAt, b.UpdatedAt)
	if err != nil {
		return RewardBenefit{}, err
	}
	return b, nil
}

// ---- entry points -----------------------------------------------------------

const (
	rewardMinTTLSec = 1
	rewardMaxTTLSec = 30 * 24 * 3600
)

// RewardReserveInput is a trusted participation-service call: the user
// explicitly started participating and the campaign promised this slot at
// this rule version. TTLSeconds is mandatory and shown to the user before
// the operation.
type RewardReserveInput struct {
	TenantID        string
	CampaignID      string
	ParticipationID string
	Slot            string
	RuleVersion     int
	EventID         string
	Actor           string
	TTLSeconds      int
	Note            string
}

func addSeconds(ts string, sec int) (string, error) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return "", fmt.Errorf("%w: bad timestamp %q", ErrRewardInvalid, ts)
	}
	return t.Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339Nano), nil
}

// ReserveRewardBenefit atomically reserves one allocation: it either inserts
// the single per-slot row (binding the coupon code immediately) or reuses the
// existing row. Resuming refreshes the TTL; expired reservations re-reserve
// the same row after the stock check; active facts read back unchanged; a
// reversed slot is burnt.
func (s *Store) ReserveRewardBenefit(in RewardReserveInput, nowTS string) (RewardBenefit, RewardOutcome, error) {
	ben := RewardBenefit{}
	if strings.TrimSpace(in.ParticipationID) == "" || strings.TrimSpace(in.Slot) == "" ||
		strings.TrimSpace(in.EventID) == "" || strings.TrimSpace(in.Actor) == "" ||
		strings.TrimSpace(in.TenantID) == "" || in.CampaignID == "" || in.RuleVersion < 1 {
		return ben, RewardOutcome{}, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	if in.TTLSeconds < rewardMinTTLSec || in.TTLSeconds > rewardMaxTTLSec {
		return ben, RewardOutcome{}, fmt.Errorf("%w: ttl out of range", ErrRewardInvalid)
	}
	if _, err := time.Parse(time.RFC3339Nano, nowTS); err != nil {
		return ben, RewardOutcome{}, fmt.Errorf("%w: bad now", ErrRewardInvalid)
	}
	if _, err := s.GetCampaign(in.CampaignID, in.TenantID); err != nil {
		return ben, RewardOutcome{}, ErrNotFound
	}
	tx, err := s.beginRewardTx()
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	defer tx.Rollback()
	if _, err := sweepDueInTx(tx, in.TenantID, nowTS); err != nil {
		return ben, RewardOutcome{}, err
	}
	if prev, _, replay, err := replayInTx(tx, in.TenantID, in.EventID); err != nil {
		return ben, RewardOutcome{}, err
	} else if replay {
		return prev, RewardOutcome{Outcome: "replayed", Replay: true}, nil
	}
	// Inventory for this slot+version must exist and be usable.
	inv, err := pickInventoryInTx(tx, in.TenantID, in.CampaignID, in.Slot, in.RuleVersion, false)
	if errors.Is(err, sql.ErrNoRows) {
		return ben, RewardOutcome{}, fmt.Errorf("%w: no batch for slot/version", ErrRewardInvalid)
	}
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	if err := inventoryUsable(inv, nowTS); err != nil {
		return ben, RewardOutcome{}, err
	}
	existing, err := loadBenefitByParticipationInTx(tx, in.TenantID, in.CampaignID, in.ParticipationID, in.Slot)
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	until, err := addSeconds(nowTS, in.TTLSeconds)
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	switch {
	case existing == nil:
		if occupied, _ := occupiedInTx(tx, inv.ID); occupied >= inv.TotalQty {
			return ben, RewardOutcome{}, ErrRewardStock
		}
		ben = newReservedBenefit(inv, in, nowTS, until)
		ben, err = insertBenefitInTx(tx, ben)
		if err != nil {
			return ben, RewardOutcome{}, err
		}
		if ben.Code, err = allocateCodeInTx(tx, inv, ben.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
		if _, err := tx.Exec(`UPDATE reward_benefits SET code=? WHERE id=?`, ben.Code, ben.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := postOpInTx(tx, in.TenantID, ben.ID, "reserve", in.EventID, in.Actor, "", "", "{}", nowTS); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := tx.Commit(); err != nil {
			return ben, RewardOutcome{}, err
		}
		return ben, RewardOutcome{Outcome: "reserved", Applied: true}, nil
	case existing.Status == RewardStatusReserved || existing.Status == RewardStatusExpired:
		if existing.Status == RewardStatusExpired {
			if occupied, _ := occupiedInTx(tx, inv.ID); occupied >= inv.TotalQty {
				return ben, RewardOutcome{}, ErrRewardStock
			}
		}
		if _, err := tx.Exec(
			`UPDATE reward_benefits SET status='reserved', reserved_at=?, reserved_until=?, updated_at=? WHERE id=?`,
			nowTS, until, nowTS, existing.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := postOpInTx(tx, in.TenantID, existing.ID, "reserve", in.EventID, in.Actor, "", "resume", "{}", nowTS); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := tx.Commit(); err != nil {
			return ben, RewardOutcome{}, err
		}
		out, err := s.GetRewardBenefit(existing.ID, in.TenantID, in.CampaignID)
		return out, RewardOutcome{Outcome: "re_reserved", Applied: true}, err
	case existing.Status == RewardStatusReversed:
		return *existing, RewardOutcome{}, fmt.Errorf("%w: slot_reversed", ErrRewardConflict)
	default: // issued / redeemed: the promise is already live
		return *existing, RewardOutcome{Outcome: "already_active"}, nil
	}
}

func loadBenefitByParticipationInTx(tx *sql.Tx, tenantID, campaignID, participationID, slot string) (*RewardBenefit, error) {
	row := tx.QueryRow(
		`SELECT `+rewardBenefitCols+` FROM reward_benefits
		 WHERE tenant_id=? AND participation_id=? AND benefit_slot=?`,
		tenantID, participationID, slot)
	b, err := scanRewardBenefit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if b.CampaignID != campaignID {
		// The slot is bound to another campaign: the participation already
		// used this slot there. Treat as a conflict, not a silent second row.
		return &b, nil
	}
	return &b, nil
}

func newReservedBenefit(inv RewardInventory, in RewardReserveInput, nowTS, until string) RewardBenefit {
	return RewardBenefit{
		ID: newID("rben_"), TenantID: in.TenantID, CampaignID: in.CampaignID,
		InventoryID: inv.ID, ParticipationID: in.ParticipationID, BenefitSlot: in.Slot,
		RuleVersion: inv.RuleVersion, Evidence: "", Status: RewardStatusReserved,
		StoreID: inv.StoreID, FaceValueMinor: inv.FaceValueMinor, CostMinor: inv.CostMinor,
		Currency: inv.Currency, ReservedAt: nowTS, ReservedUntil: until,
		Note: in.Note, CreatedAt: nowTS, UpdatedAt: nowTS,
	}
}

// RewardConfirmInput is one staff confirmation event. Evidence must be one of
// the three classes and is adjudicated against the batch's rule snapshot
// inside the transaction; an official claim is anchored to a server-side
// publish attempt. FulfillNow posts issue and redeem as two facts in one
// transaction (确认并现场兑现).
type RewardConfirmInput struct {
	TenantID        string
	CampaignID      string
	ParticipationID string
	Slot            string
	RuleVersion     int
	EventID         string
	Evidence        string
	Official        rewardops.OfficialRef
	Actor           string
	StoreID         string
	Note            string
	FulfillNow      bool
	RedeemEventID   string
	Now             string
}

// ConfirmRewardBenefit runs adjudication, dedup, and the state machine in one
// transaction and posts the append-only trail.
func (s *Store) ConfirmRewardBenefit(in RewardConfirmInput) (RewardBenefit, RewardOutcome, error) {
	ben := RewardBenefit{}
	if strings.TrimSpace(in.ParticipationID) == "" || strings.TrimSpace(in.Slot) == "" ||
		strings.TrimSpace(in.EventID) == "" || strings.TrimSpace(in.Actor) == "" ||
		strings.TrimSpace(in.StoreID) == "" || strings.TrimSpace(in.TenantID) == "" ||
		in.CampaignID == "" || in.RuleVersion < 1 || strings.TrimSpace(in.Now) == "" {
		return ben, RewardOutcome{}, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	if in.FulfillNow && strings.TrimSpace(in.RedeemEventID) == "" {
		return ben, RewardOutcome{}, fmt.Errorf("%w: missing redeem event id", ErrRewardInvalid)
	}
	if _, err := s.GetCampaign(in.CampaignID, in.TenantID); err != nil {
		return ben, RewardOutcome{}, ErrNotFound
	}
	tx, err := s.beginRewardTx()
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	defer tx.Rollback()
	if _, err := sweepDueInTx(tx, in.TenantID, in.Now); err != nil {
		return ben, RewardOutcome{}, err
	}
	if prev, _, replay, err := replayInTx(tx, in.TenantID, in.EventID); err != nil {
		return ben, RewardOutcome{}, err
	} else if replay {
		return prev, RewardOutcome{Outcome: "issued", Replay: true}, nil
	}
	inv, err := pickInventoryInTx(tx, in.TenantID, in.CampaignID, in.Slot, in.RuleVersion, false)
	if errors.Is(err, sql.ErrNoRows) {
		return ben, RewardOutcome{}, fmt.Errorf("%w: no batch for slot/version", ErrRewardInvalid)
	}
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	if err := inventoryUsable(inv, in.Now); err != nil {
		return ben, RewardOutcome{}, err
	}
	// A store-scoped batch only materializes benefits at its own store: a
	// confirmation recorded at another store is a wrong-store refusal.
	if inv.StoreID != "" && strings.TrimSpace(in.StoreID) != inv.StoreID {
		return ben, RewardOutcome{}, fmt.Errorf("%w: store_mismatch", ErrRewardInvalid)
	}
	// Adjudicate against this batch's rule snapshot (in-flight participations
	// keep their original version: the caller names the version it promised).
	if v := rewardops.Adjudicate(inv.RewardSnapshot(), rewardops.Input{
		TenantID: in.TenantID, CampaignID: in.CampaignID, ParticipationID: in.ParticipationID,
		Slot: in.Slot, RuleVersion: in.RuleVersion, EventID: in.EventID, Evidence: in.Evidence,
		Official: in.Official, Actor: in.Actor, StoreID: in.StoreID, Note: in.Note,
	}); !v.Proceed {
		if v.PendingReview {
			return ben, RewardOutcome{}, fmt.Errorf("%w: %s", ErrRewardConflict, v.Reason)
		}
		return ben, RewardOutcome{}, fmt.Errorf("%w: %s", ErrRewardInvalid, v.Reason)
	}
	if in.Evidence == rewardops.EvidenceOfficial {
		if err := anchorOfficialInTx(tx, in); err != nil {
			return ben, RewardOutcome{}, err
		}
	}
	existing, err := loadBenefitByParticipationInTx(tx, in.TenantID, in.CampaignID, in.ParticipationID, in.Slot)
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	if existing != nil && existing.CampaignID != in.CampaignID {
		return *existing, RewardOutcome{}, fmt.Errorf("%w: slot_belongs_to_other_campaign", ErrRewardConflict)
	}
	if existing != nil {
		switch existing.Status {
		case RewardStatusReversed:
			return *existing, RewardOutcome{}, fmt.Errorf("%w: slot_reversed", ErrRewardConflict)
		case RewardStatusIssued:
			return *existing, RewardOutcome{Outcome: "already_issued"}, nil
		case RewardStatusRedeemed:
			return *existing, RewardOutcome{Outcome: "already_redeemed"}, nil
		case RewardStatusExpired:
			if occupied, _ := occupiedInTx(tx, inv.ID); occupied >= inv.TotalQty {
				return ben, RewardOutcome{}, ErrRewardStock
			}
		}
	} else if occupied, _ := occupiedInTx(tx, inv.ID); occupied >= inv.TotalQty {
		return ben, RewardOutcome{}, ErrRewardStock
	}
	code := ""
	targetID := ""
	if existing != nil {
		code = existing.Code
		targetID = existing.ID
	} else {
		staging := newReservedBenefit(inv, RewardReserveInput{
			TenantID: in.TenantID, CampaignID: in.CampaignID, ParticipationID: in.ParticipationID,
			Slot: in.Slot, Note: in.Note,
		}, in.Now, "")
		targetID = staging.ID
		if code, err = allocateCodeInTx(tx, inv, staging.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
	}
	// An official fact funds exactly one benefit; claim it before mutating.
	if in.Evidence == rewardops.EvidenceOfficial {
		if err := claimOfficialFactInTx(tx, in.TenantID, in.CampaignID, strings.TrimSpace(in.Official.FactID), targetID); err != nil {
			return ben, RewardOutcome{}, err
		}
	}
	// Promote to issued (and optionally redeemed) with one state transition.
	nextStatus := RewardStatusIssued
	if in.FulfillNow {
		nextStatus = RewardStatusRedeemed
	}
	if existing == nil {
		ben = newReservedBenefit(inv, RewardReserveInput{
			TenantID: in.TenantID, CampaignID: in.CampaignID, ParticipationID: in.ParticipationID,
			Slot: in.Slot, Note: in.Note,
		}, in.Now, "")
		ben.Evidence = in.Evidence
		ben.Code = code
		ben.Status = nextStatus
		ben.IssuedAt, ben.IssuedBy = in.Now, in.Actor
		if in.FulfillNow {
			ben.RedeemedAt, ben.RedeemedBy = in.Now, in.Actor
		}
		if _, err := insertBenefitInTx(tx, ben); err != nil {
			return ben, RewardOutcome{}, err
		}
		if _, err := tx.Exec(`UPDATE reward_benefits SET code=? WHERE id=?`, code, ben.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
	} else {
		ben = *existing
		if _, err := tx.Exec(
			`UPDATE reward_benefits SET status=?, evidence=?, issued_at=?, issued_by=?, updated_at=?, redeemed_at=CASE WHEN ? THEN ? ELSE redeemed_at END, redeemed_by=CASE WHEN ? THEN ? ELSE redeemed_by END WHERE id=?`,
			nextStatus, in.Evidence, in.Now, in.Actor, in.Now, in.FulfillNow, in.Now, in.FulfillNow, in.Actor, ben.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
	}
	if err := postOpInTx(tx, in.TenantID, ben.ID, "issue", in.EventID, in.Actor, in.StoreID, "", "{}", in.Now); err != nil {
		return ben, RewardOutcome{}, err
	}
	if in.FulfillNow {
		if err := postOpInTx(tx, in.TenantID, ben.ID, "redeem", "", in.Actor, in.StoreID, "fulfilled_on_confirmation", "{}", in.Now); err != nil {
			return ben, RewardOutcome{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ben, RewardOutcome{}, err
	}
	out := RewardOutcome{Outcome: "issued", Applied: true}
	if in.FulfillNow {
		out.Outcome = "issued_and_redeemed"
	}
	final, err := s.GetRewardBenefit(ben.ID, in.TenantID, in.CampaignID)
	return final, out, err
}

// anchorOfficialInTx verifies the official claim against the reward domain's
// own official-fact ledger (reward_official_facts). The attempts table is
// schema-locked to empty receipts by 0010, so a real official fact can only
// enter through the trusted official-query integration writing that table.
// A fact id absent from the ledger, a post id mismatch, or a foreign campaign
// is a fabricated receipt and refuses.
func anchorOfficialInTx(tx *sql.Tx, in RewardConfirmInput) error {
	var postID, receiptSource, attemptID string
	err := tx.QueryRow(
		`SELECT platform_post_id, receipt_source, attempt_id
		 FROM reward_official_facts WHERE tenant_id=? AND campaign_id=? AND attempt_id=?`,
		in.TenantID, in.CampaignID, strings.TrimSpace(in.Official.FactID)).
		Scan(&postID, &receiptSource, &attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: fabricated_official_receipt", ErrRewardInvalid)
	}
	if err != nil {
		return err
	}
	if receiptSource != rewardops.ReceiptOfficialQuery ||
		strings.TrimSpace(postID) == "" ||
		postID != strings.TrimSpace(in.Official.PlatformPostID) {
		return fmt.Errorf("%w: fabricated_official_receipt", ErrRewardInvalid)
	}
	return nil
}

// claimOfficialFactInTx binds the fact to one benefit. A fact consumed by an
// earlier grant cannot fund a second one; the caller answers the first
// result instead of double-granting.
func claimOfficialFactInTx(tx *sql.Tx, tenantID, campaignID, attemptID, benefitID string) error {
	res, err := tx.Exec(
		`UPDATE reward_official_facts SET consumed_by=? WHERE tenant_id=? AND campaign_id=? AND attempt_id=? AND consumed_by=''`,
		benefitID, tenantID, campaignID, attemptID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: official_fact_consumed", ErrRewardConflict)
	}
	return nil
}

// RedeemRewardBenefit is one atomic UPDATE: exactly one concurrent staff
// member wins; the other reads back the processed result.
func (s *Store) RedeemRewardBenefit(tenantID, campaignID, benefitID, actor, storeID, eventID, nowTS string) (RewardBenefit, RewardOutcome, error) {
	ben := RewardBenefit{}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(storeID) == "" ||
		strings.TrimSpace(eventID) == "" || strings.TrimSpace(nowTS) == "" {
		return ben, RewardOutcome{}, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	tx, err := s.beginRewardTx()
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	defer tx.Rollback()
	if _, err := sweepDueInTx(tx, tenantID, nowTS); err != nil {
		return ben, RewardOutcome{}, err
	}
	if prev, _, replay, err := replayInTx(tx, tenantID, eventID); err != nil {
		return ben, RewardOutcome{}, err
	} else if replay {
		return prev, RewardOutcome{Outcome: "redeemed", Replay: true}, nil
	}
	row := tx.QueryRow(
		`SELECT `+rewardBenefitCols+` FROM reward_benefits WHERE id=? AND tenant_id=? AND campaign_id=?`,
		benefitID, tenantID, campaignID)
	cur, err := scanRewardBenefit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ben, RewardOutcome{}, ErrNotFound
	}
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	switch cur.Status {
	case RewardStatusRedeemed:
		return cur, RewardOutcome{Outcome: "already_redeemed"}, nil
	case RewardStatusIssued:
		if _, err := tx.Exec(
			`UPDATE reward_benefits SET status='redeemed', redeemed_at=?, redeemed_by=?, updated_at=? WHERE id=? AND status='issued'`,
			nowTS, actor, nowTS, cur.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := postOpInTx(tx, tenantID, cur.ID, "redeem", eventID, actor, storeID, "", "{}", nowTS); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := tx.Commit(); err != nil {
			return ben, RewardOutcome{}, err
		}
		out, err := s.GetRewardBenefit(cur.ID, tenantID, campaignID)
		return out, RewardOutcome{Outcome: "redeemed", Applied: true}, err
	case RewardStatusReserved:
		return cur, RewardOutcome{}, fmt.Errorf("%w: not_issued", ErrRewardConflict)
	case RewardStatusExpired:
		return cur, RewardOutcome{}, fmt.Errorf("%w: reservation_expired", ErrRewardConflict)
	default: // reversed
		return cur, RewardOutcome{}, fmt.Errorf("%w: slot_reversed", ErrRewardConflict)
	}
}

// ReverseRewardBenefit is the admin correction: append-only, reason required,
// the original facts survive. Reversed slots never re-issue.
func (s *Store) ReverseRewardBenefit(tenantID, campaignID, benefitID, actor, reason, eventID, nowTS string) (RewardBenefit, RewardOutcome, error) {
	ben := RewardBenefit{}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" ||
		strings.TrimSpace(eventID) == "" || strings.TrimSpace(nowTS) == "" {
		return ben, RewardOutcome{}, fmt.Errorf("%w: reason and actor required", ErrRewardInvalid)
	}
	tx, err := s.beginRewardTx()
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	defer tx.Rollback()
	if prev, _, replay, err := replayInTx(tx, tenantID, eventID); err != nil {
		return ben, RewardOutcome{}, err
	} else if replay {
		return prev, RewardOutcome{Outcome: "reversed", Replay: true}, nil
	}
	row := tx.QueryRow(
		`SELECT `+rewardBenefitCols+` FROM reward_benefits WHERE id=? AND tenant_id=? AND campaign_id=?`,
		benefitID, tenantID, campaignID)
	cur, err := scanRewardBenefit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ben, RewardOutcome{}, ErrNotFound
	}
	if err != nil {
		return ben, RewardOutcome{}, err
	}
	switch cur.Status {
	case RewardStatusReversed:
		return cur, RewardOutcome{Outcome: "already_reversed"}, nil
	case RewardStatusExpired:
		return cur, RewardOutcome{}, fmt.Errorf("%w: nothing_active_to_reverse", ErrRewardConflict)
	case RewardStatusReserved, RewardStatusIssued, RewardStatusRedeemed:
		if _, err := tx.Exec(
			`UPDATE reward_benefits SET status='reversed', reversed_at=?, reversed_by=?, reverse_reason=?, updated_at=? WHERE id=?`,
			nowTS, actor, reason, nowTS, cur.ID); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := postOpInTx(tx, tenantID, cur.ID, "reverse", eventID, actor, "", reason, "{}", nowTS); err != nil {
			return ben, RewardOutcome{}, err
		}
		if err := tx.Commit(); err != nil {
			return ben, RewardOutcome{}, err
		}
		out, err := s.GetRewardBenefit(cur.ID, tenantID, campaignID)
		return out, RewardOutcome{Outcome: "reversed", Applied: true}, err
	default:
		return cur, RewardOutcome{}, fmt.Errorf("%w: unknown state", ErrRewardConflict)
	}
}

// ExpireDueRewardReservations runs the lazy TTL sweep explicitly (also run
// inside every mutating entry point). Tenant-scoped when tenantID is given.
func (s *Store) ExpireDueRewardReservations(nowTS, tenantID string) (int, error) {
	if _, err := time.Parse(time.RFC3339Nano, nowTS); err != nil {
		return 0, fmt.Errorf("%w: bad now", ErrRewardInvalid)
	}
	tx, err := s.beginRewardTx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := sweepDueInTx(tx, tenantID, nowTS)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// ---- summary and review queue -----------------------------------------------

// RewardSummary is the recomputed state of one campaign's reward ledger.
// Counts read the benefit rows; EvidenceGranted counts the three evidence
// classes over granted rewards (issued+redeemed); PendingReview is the
// number of self-reported publish attempts without a benefit. Face and cost
// stay separate numbers: face is marketing face value, never actual cost.
type RewardSummary struct {
	CampaignID         string         `json:"campaign_id"`
	AsOf               string         `json:"as_of"`
	Counts             map[string]int `json:"counts"`
	EvidenceGranted    map[string]int `json:"evidence_granted"`
	PendingReview      int            `json:"pending_review"`
	CommittedFaceMinor int64          `json:"committed_face_minor"`
	CommittedCostMinor int64          `json:"committed_cost_minor"`
	PendingFaceMinor   int64          `json:"pending_face_minor"`
	PendingCostMinor   int64          `json:"pending_cost_minor"`
}

// RewardSummary recomputes everything from the tables. It is a read, not a
// write; after a restart the same numbers fall out of the same rows.
func (s *Store) RewardSummary(tenantID, campaignID string) (RewardSummary, error) {
	sum := RewardSummary{
		CampaignID: campaignID, AsOf: now(),
		Counts: map[string]int{
			RewardStatusReserved: 0, RewardStatusIssued: 0, RewardStatusRedeemed: 0,
			RewardStatusReversed: 0, RewardStatusExpired: 0,
		},
		EvidenceGranted: map[string]int{
			rewardops.EvidenceStaffObserved: 0, rewardops.EvidenceStaffVerified: 0,
			rewardops.EvidenceOfficial: 0,
		},
	}
	rows, err := s.DB.Query(
		`SELECT status, evidence, face_value_minor, cost_minor FROM reward_benefits
		 WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	if err != nil {
		return RewardSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, evidence string
		var face, cost int64
		if err := rows.Scan(&status, &evidence, &face, &cost); err != nil {
			return RewardSummary{}, err
		}
		sum.Counts[status]++
		switch status {
		case RewardStatusIssued, RewardStatusRedeemed:
			if evidence != "" {
				sum.EvidenceGranted[evidence]++
			}
			sum.CommittedFaceMinor += face
			sum.CommittedCostMinor += cost
		case RewardStatusReserved:
			sum.PendingFaceMinor += face
			sum.PendingCostMinor += cost
		}
	}
	if err := rows.Err(); err != nil {
		return RewardSummary{}, err
	}
	// Pending review: self-reported attempts for this campaign without a
	// benefit. The slot reference is the participation id.
	err = s.DB.QueryRow(
		`SELECT COUNT(*) FROM customer_publish_attempts a
		 WHERE a.tenant_id=? AND a.campaign_id=? AND a.self_reported=1
		   AND NOT EXISTS (
		     SELECT 1 FROM reward_benefits b
		      WHERE b.tenant_id=a.tenant_id AND b.participation_id=a.id
		   )`, tenantID, campaignID).Scan(&sum.PendingReview)
	if err != nil {
		return RewardSummary{}, err
	}
	return sum, nil
}

// RewardReviewRequest is one self-reported publish attempt waiting for a
// staff decision. Read-only projection; the decision itself is a confirm.
type RewardReviewRequest struct {
	AttemptID        string `json:"attempt_id"`
	ParticipationRef string `json:"participation_ref"`
	Platform         string `json:"platform"`
	Note             string `json:"note"`
	CreatedAt        string `json:"created_at"`
}

// ListRewardReviewRequests returns self-reported, unconfirmed attempts for
// the campaign (oldest first). Empty note: the attempt table does not store
// customer notes, only the self-report flag.
func (s *Store) ListRewardReviewRequests(tenantID, campaignID string) ([]RewardReviewRequest, error) {
	rows, err := s.DB.Query(
		`SELECT a.id, a.platform, a.created_at FROM customer_publish_attempts a
		 WHERE a.tenant_id=? AND a.campaign_id=? AND a.self_reported=1
		   AND a.status <> 'publish_confirmed'
		   AND NOT EXISTS (
		     SELECT 1 FROM reward_benefits b
		      WHERE b.tenant_id=a.tenant_id AND b.participation_id=a.id
		   )
		 ORDER BY a.created_at, a.id`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RewardReviewRequest{}
	for rows.Next() {
		var r RewardReviewRequest
		if err := rows.Scan(&r.AttemptID, &r.Platform, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ParticipationRef = r.AttemptID
		out = append(out, r)
	}
	return out, rows.Err()
}
