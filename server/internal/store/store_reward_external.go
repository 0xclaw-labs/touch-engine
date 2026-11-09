package store

// HUI-1671: 优惠供给目录(deal_offers)、外部券边界、发放幂等持久化。
//
// 边界纪律:
//   - deal_offers 用 kind 区分 external_product(美团/点评/抖音团购) 与
//     merchant_benefit(本地门店券),issuer 记发行方。
//   - 外部券的核销事实只能由授权适配器返回;本地账对绑定外部供给的权益
//     只能拒绝核销(ErrExternalRedeemForbidden),不能改状态冒充核销。
//   - 外部发放沿同一幂等键回查(reward_external_issuances 唯一约束);
//     结果未知保持 pending_unknown,永不升级为已发放;缺授权不发伪平台券。
//   - coupon_ref 是敏感外部券引用:不进日志、不进公共面。

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
)

const (
	// DealKindExternalProduct is an external platform group-buy item; its
	// redemption lives on the platform, never in the local ledger.
	DealKindExternalProduct = "external_product"
	// DealKindMerchantBenefit is a local merchant coupon/gift/discount.
	DealKindMerchantBenefit = "merchant_benefit"

	DealStatusActive  = "active"
	DealStatusPaused  = "paused"
	DealStatusRetired = "retired"
)

// DealOffer is one entry of the offer catalog (B09 前半).
type DealOffer struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	CampaignID       string `json:"campaign_id"`
	Kind             string `json:"kind"`
	Issuer           string `json:"issuer"`
	Provider         string `json:"provider,omitempty"`
	ProviderOfferID  string `json:"provider_offer_id,omitempty"`
	AuthorizationRef string `json:"authorization_ref,omitempty"`
	Title            string `json:"title"`
	FaceValueMinor   int64  `json:"face_value_minor"`
	Currency         string `json:"currency,omitempty"`
	ValidFrom        string `json:"valid_from,omitempty"`
	ValidUntil       string `json:"valid_until,omitempty"`
	Status           string `json:"status"`
	Note             string `json:"note,omitempty"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
}

// IsExternal reports whether offers of this kind are redeemed off-ledger.
func (o DealOffer) IsExternal() bool { return o.Kind == DealKindExternalProduct }

const dealOfferCols = `id,tenant_id,campaign_id,kind,issuer,provider,provider_offer_id,authorization_ref,title,face_value_minor,currency,valid_from,valid_until,status,note,created_by,created_at`

func scanDealOffer(sc interface{ Scan(...any) error }) (DealOffer, error) {
	var o DealOffer
	var provider, providerOfferID, authRef, currency, from, until, note sql.NullString
	err := sc.Scan(&o.ID, &o.TenantID, &o.CampaignID, &o.Kind, &o.Issuer, &provider,
		&providerOfferID, &authRef, &o.Title, &o.FaceValueMinor, &currency, &from, &until,
		&o.Status, &note, &o.CreatedBy, &o.CreatedAt)
	if err != nil {
		return DealOffer{}, err
	}
	o.Provider, o.ProviderOfferID, o.AuthorizationRef = provider.String, providerOfferID.String, authRef.String
	o.Currency, o.ValidFrom, o.ValidUntil, o.Note = currency.String, from.String, until.String, note.String
	return o, nil
}

// NewDealOffer is the creation input. External products should carry provider
// and provider_offer_id; the authorization ref is the adapter's grant and is
// required before any external issuance may be recorded against the offer.
type NewDealOffer struct {
	CampaignID       string
	Kind             string
	Issuer           string
	Provider         string
	ProviderOfferID  string
	AuthorizationRef string
	Title            string
	FaceValueMinor   int64
	Currency         string
	ValidFrom        string
	ValidUntil       string
	Note             string
	CreatedBy        string
}

// CreateDealOffer inserts one offer row. External rows without a provider are
// refused: an external product without a platform is an invented fact.
func (s *Store) CreateDealOffer(tenantID string, in NewDealOffer) (DealOffer, error) {
	o := DealOffer{}
	kind := strings.TrimSpace(in.Kind)
	title := strings.TrimSpace(in.Title)
	issuer := strings.TrimSpace(in.Issuer)
	if tenantID == "" || in.CampaignID == "" || title == "" || issuer == "" || in.CreatedBy == "" {
		return o, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	switch kind {
	case DealKindExternalProduct, DealKindMerchantBenefit:
	default:
		return o, fmt.Errorf("%w: unknown offer kind %q", ErrRewardInvalid, kind)
	}
	if kind == DealKindExternalProduct && strings.TrimSpace(in.Provider) == "" {
		return o, fmt.Errorf("%w: external offer needs a provider", ErrRewardInvalid)
	}
	if in.FaceValueMinor < 0 {
		return o, fmt.Errorf("%w: negative face value", ErrRewardInvalid)
	}
	if _, err := s.GetCampaign(in.CampaignID, tenantID); err != nil {
		return o, fmt.Errorf("%w: campaign not found in tenant", ErrNotFound)
	}
	o = DealOffer{
		ID: newID("offer_"), TenantID: tenantID, CampaignID: in.CampaignID,
		Kind: kind, Issuer: issuer, Provider: strings.TrimSpace(in.Provider),
		ProviderOfferID:  strings.TrimSpace(in.ProviderOfferID),
		AuthorizationRef: strings.TrimSpace(in.AuthorizationRef),
		Title:            title, FaceValueMinor: in.FaceValueMinor,
		Currency: strings.TrimSpace(in.Currency), ValidFrom: strings.TrimSpace(in.ValidFrom),
		ValidUntil: strings.TrimSpace(in.ValidUntil), Status: DealStatusActive,
		Note: strings.TrimSpace(in.Note), CreatedBy: in.CreatedBy, CreatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO deal_offers(`+dealOfferCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.TenantID, o.CampaignID, o.Kind, o.Issuer, o.Provider, o.ProviderOfferID,
		o.AuthorizationRef, o.Title, o.FaceValueMinor, o.Currency, o.ValidFrom, o.ValidUntil,
		o.Status, o.Note, o.CreatedBy, o.CreatedAt)
	if err != nil {
		return DealOffer{}, err
	}
	return o, nil
}

// GetDealOffer loads one offer inside the tenant scope.
func (s *Store) GetDealOffer(id, tenantID string) (DealOffer, error) {
	row := s.DB.QueryRow(`SELECT `+dealOfferCols+` FROM deal_offers WHERE id=? AND tenant_id=?`, id, tenantID)
	o, err := scanDealOffer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DealOffer{}, ErrNotFound
	}
	return o, err
}

// ListDealOffers returns the tenant's offers for one campaign (live first).
func (s *Store) ListDealOffers(tenantID, campaignID string) ([]DealOffer, error) {
	rows, err := s.DB.Query(
		`SELECT `+dealOfferCols+` FROM deal_offers WHERE tenant_id=? AND campaign_id=? AND status <> ? ORDER BY created_at, id`,
		tenantID, campaignID, DealStatusRetired)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DealOffer{}
	for rows.Next() {
		o, err := scanDealOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// benefitOffer loads the offer a benefit's batch is bound to (” when none).
func (s *Store) benefitOffer(tx *sql.Tx, tenantID, inventoryID string) (DealOffer, error) {
	var offerID string
	q := `SELECT offer_id FROM reward_inventory WHERE id=? AND tenant_id=?`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRow(q, inventoryID, tenantID)
	} else {
		row = s.DB.QueryRow(q, inventoryID, tenantID)
	}
	if err := row.Scan(&offerID); err != nil {
		return DealOffer{}, err
	}
	if strings.TrimSpace(offerID) == "" {
		return DealOffer{}, sql.ErrNoRows
	}
	q2 := `SELECT ` + dealOfferCols + ` FROM deal_offers WHERE id=? AND tenant_id=?`
	var row2 *sql.Row
	if tx != nil {
		row2 = tx.QueryRow(q2, offerID, tenantID)
	} else {
		row2 = s.DB.QueryRow(q2, offerID, tenantID)
	}
	return scanDealOffer(row2)
}

// BenefitIsExternal reports whether the benefit's batch is bound to an
// external platform offer: such benefits are displayed and awaited, never
// redeemed by the local ledger.
func (s *Store) BenefitIsExternal(tenantID string, benefit RewardBenefit) bool {
	offer, err := s.benefitOffer(nil, tenantID, benefit.InventoryID)
	if err != nil {
		return false
	}
	return offer.IsExternal()
}

// ---- external issuance (幂等持久化) ------------------------------------------

// ExternalIssuance is one persisted issuance attempt keyed by the caller's
// idempotency key. State issued|pending_unknown|failed; pending_unknown is a
// real state shown as 待确认, never upgraded without a confirming lookup.
type ExternalIssuance struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	CampaignID      string `json:"campaign_id"`
	ParticipationID string `json:"participation_id"`
	BenefitID       string `json:"benefit_id,omitempty"`
	OfferID         string `json:"offer_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	State           string `json:"state"`
	Provider        string `json:"provider,omitempty"`
	CouponRef       string `json:"coupon_ref,omitempty"` // sensitive
	Detail          string `json:"detail,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

const (
	ExternalStateIssued  = "issued"
	ExternalStateUnknown = "pending_unknown"
	ExternalStateFailed  = "failed"
)

// ErrExternalRedeemForbidden: 本地账无权核销外部券(明确拒绝码)。
var ErrExternalRedeemForbidden = errors.New("store: external coupons cannot be redeemed by the local ledger")

// ErrExternalAuthorizationRequired: 无授权不允许生成任何"平台已发"事实。
var ErrExternalAuthorizationRequired = errors.New("store: external issuance needs an authorization ref")

const externalIssuanceCols = `id,tenant_id,campaign_id,participation_id,benefit_id,offer_id,idempotency_key,state,provider,coupon_ref,detail,created_at,updated_at`

func scanExternalIssuance(sc interface{ Scan(...any) error }) (ExternalIssuance, error) {
	var e ExternalIssuance
	var benefitID, provider, couponRef, detail sql.NullString
	err := sc.Scan(&e.ID, &e.TenantID, &e.CampaignID, &e.ParticipationID, &benefitID, &e.OfferID,
		&e.IdempotencyKey, &e.State, &provider, &couponRef, &detail, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return ExternalIssuance{}, err
	}
	e.BenefitID, e.Provider, e.CouponRef, e.Detail = benefitID.String, provider.String, couponRef.String, detail.String
	return e, nil
}

// ExternalGrantInput is one grant request toward an external platform. The
// AuthorizationRef must come from the offer's adapter grant (HUI-4590); the
// Issue/Lookup closures wrap the real adapter and may be nil only in tests
// that never reach the remote.
type ExternalGrantInput struct {
	TenantID         string
	CampaignID       string
	ParticipationID  string
	BenefitID        string
	OfferID          string
	IdempotencyKey   string
	AuthorizationRef string
	Provider         string
}

// GrantExternalCoupon runs the honest external issuance discipline:
//   - no authorization ref: refuse, nothing is recorded, no fake coupon;
//   - existing issued/failed row: replay (read back, never re-issue);
//   - existing pending_unknown row: requery by the SAME key via lookup;
//   - new key: issue once; on transport failure lookup once with the same key;
//     what the remote cannot confirm stays pending_unknown.
func (s *Store) GrantExternalCoupon(in ExternalGrantInput, issue func() (rewardops.IssuanceResult, error), lookup func() (rewardops.IssuanceResult, error), nowTS string) (ExternalIssuance, bool, error) {
	if strings.TrimSpace(in.TenantID) == "" || strings.TrimSpace(in.CampaignID) == "" ||
		strings.TrimSpace(in.ParticipationID) == "" || strings.TrimSpace(in.OfferID) == "" ||
		strings.TrimSpace(in.IdempotencyKey) == "" {
		return ExternalIssuance{}, false, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	offer, err := s.GetDealOffer(in.OfferID, in.TenantID)
	if err != nil {
		return ExternalIssuance{}, false, err
	}
	if !offer.IsExternal() {
		return ExternalIssuance{}, false, fmt.Errorf("%w: offer is not an external product", ErrRewardInvalid)
	}
	if strings.TrimSpace(in.AuthorizationRef) == "" && strings.TrimSpace(offer.AuthorizationRef) == "" {
		return ExternalIssuance{}, false, ErrExternalAuthorizationRequired
	}
	if _, err := time.Parse(time.RFC3339Nano, nowTS); err != nil {
		return ExternalIssuance{}, false, fmt.Errorf("%w: bad now", ErrRewardInvalid)
	}
	if prev, err := s.GetExternalIssuance(in.TenantID, in.IdempotencyKey); err == nil {
		// Replay/requery by the SAME key. Terminal states read back; an
		// unresolved row is requeried once, never re-issued.
		switch prev.State {
		case ExternalStateIssued, ExternalStateFailed:
			return prev, true, nil
		default:
			updated, err := s.requeryExternalIssuance(prev, lookup, nowTS)
			return updated, true, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return ExternalIssuance{}, false, err
	}
	// New key: issue exactly once; on transport failure lookup once with the
	// SAME key; what the remote cannot confirm stays pending_unknown. A nil
	// adapter is an unavailable interface: honestly unknown, never a fake
	// success (接口不可用时不伪造第三方券).
	var res rewardops.IssuanceResult
	if issue == nil {
		res = rewardops.IssuanceResult{State: rewardops.StateUnknown, Detail: "issuer_unavailable"}
	} else {
		res = issueWithOneRequery(issue, lookup)
	}
	state := ExternalStateUnknown
	couponRef := ""
	switch res.State {
	case rewardops.StateIssued:
		state, couponRef = ExternalStateIssued, strings.TrimSpace(res.Code)
	case rewardops.StateFailed:
		state = ExternalStateFailed
	}
	e := ExternalIssuance{
		ID: newID("reis_"), TenantID: in.TenantID, CampaignID: in.CampaignID,
		ParticipationID: in.ParticipationID, BenefitID: strings.TrimSpace(in.BenefitID),
		OfferID: in.OfferID, IdempotencyKey: in.IdempotencyKey, State: state,
		Provider:  firstNonEmpty(strings.TrimSpace(in.Provider), offer.Provider),
		CouponRef: couponRef, Detail: strings.TrimSpace(res.Detail),
		CreatedAt: nowTS, UpdatedAt: nowTS,
	}
	if _, err := s.DB.Exec(
		`INSERT INTO reward_external_issuances(`+externalIssuanceCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.TenantID, e.CampaignID, e.ParticipationID, e.BenefitID, e.OfferID,
		e.IdempotencyKey, e.State, e.Provider, e.CouponRef, e.Detail, e.CreatedAt, e.UpdatedAt); err != nil {
		return ExternalIssuance{}, false, err
	}
	return e, false, nil
}

// issueWithOneRequery runs the transport discipline without any network
// assumption: issue once; when it errs, lookup once with the same key; an
// unconfirmed result is unknown — never invented, never upgraded.
func issueWithOneRequery(issue, lookup func() (rewardops.IssuanceResult, error)) rewardops.IssuanceResult {
	res, err := issue()
	if err == nil {
		return res
	}
	if lookup == nil {
		return rewardops.IssuanceResult{State: rewardops.StateUnknown, Detail: "issue_failed_without_lookup"}
	}
	queried, lerr := lookup()
	if lerr != nil {
		return rewardops.IssuanceResult{State: rewardops.StateUnknown, Detail: "issue_and_lookup_failed"}
	}
	if queried.State == rewardops.StateIssued || queried.State == rewardops.StateFailed {
		return queried
	}
	return rewardops.IssuanceResult{State: rewardops.StateUnknown, Detail: "remote_result_unconfirmed"}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// requeryExternalIssuance asks the remote about a pending_unknown row via the
// same idempotency key. Only a confirming answer (issued/failed) upgrades the
// row; anything else stays 待确认.
func (s *Store) requeryExternalIssuance(prev ExternalIssuance, lookup func() (rewardops.IssuanceResult, error), nowTS string) (ExternalIssuance, error) {
	if lookup == nil {
		return prev, nil
	}
	res, err := lookup()
	if err != nil {
		return prev, nil // still unknown, honestly
	}
	switch res.State {
	case rewardops.StateIssued, rewardops.StateFailed:
		return s.updateExternalIssuanceState(prev, res, nowTS)
	default:
		return prev, nil
	}
}

func (s *Store) updateExternalIssuanceState(prev ExternalIssuance, res rewardops.IssuanceResult, nowTS string) (ExternalIssuance, error) {
	state := ExternalStateUnknown
	switch res.State {
	case rewardops.StateIssued:
		state = ExternalStateIssued
	case rewardops.StateFailed:
		state = ExternalStateFailed
	}
	couponRef := strings.TrimSpace(res.Code)
	if _, err := s.DB.Exec(
		`UPDATE reward_external_issuances SET state=?, coupon_ref=?, detail=?, updated_at=? WHERE id=? AND tenant_id=?`,
		state, couponRef, strings.TrimSpace(res.Detail), nowTS, prev.ID, prev.TenantID); err != nil {
		return ExternalIssuance{}, err
	}
	return s.GetExternalIssuance(prev.TenantID, prev.IdempotencyKey)
}

// GetExternalIssuance loads one issuance row by its idempotency key.
func (s *Store) GetExternalIssuance(tenantID, idempotencyKey string) (ExternalIssuance, error) {
	row := s.DB.QueryRow(
		`SELECT `+externalIssuanceCols+` FROM reward_external_issuances WHERE tenant_id=? AND idempotency_key=?`,
		tenantID, idempotencyKey)
	e, err := scanExternalIssuance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalIssuance{}, ErrNotFound
	}
	return e, err
}

// ListExternalIssuances returns the campaign's external issuance rows (newest
// first). coupon_ref is included: the caller is the authenticated admin
// surface and must never log or forward it.
func (s *Store) ListExternalIssuances(tenantID, campaignID string) ([]ExternalIssuance, error) {
	rows, err := s.DB.Query(
		`SELECT `+externalIssuanceCols+` FROM reward_external_issuances
		 WHERE tenant_id=? AND campaign_id=? ORDER BY created_at DESC, id DESC`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExternalIssuance{}
	for rows.Next() {
		e, err := scanExternalIssuance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
