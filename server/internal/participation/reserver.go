package participation

import "context"

// BenefitReserver is the trusted port toward the reward domain (HUI-1671 / G3).
// The participation domain creates the participation FIRST, then reserves
// with a stable idempotent reference; the reward side can never reverse this
// request into "create a participation for me".
//
// 纪律:
//   - Production wiring lands with HUI-1671 (root-arranged). Until then a nil
//     Reserver is legal: participations still start, reservation stays
//     pending, and the customer is NEVER told 已保留.
//   - This port does not carry stock, prices, or coupon payloads: no second
//     inventory lives in this package.
//   - Reserve must be idempotent on ref.BenefitSlot: replays and re-handoffs
//     with the same slot must not occupy stock twice.
type BenefitReserver interface {
	Reserve(ctx context.Context, ref ParticipationRef) (ReserveResult, error)
}

// ParticipationRef is the immutable reference handed to the reward domain.
// BenefitSlot is stable for the whole participation (one slot per
// participation, never one per attempt).
type ParticipationRef struct {
	ParticipationID    string
	TenantID           string
	StoreID            string
	CampaignID         string
	BenefitSlot        string
	RuleVersion        int
	ContentPackVersion string
}

// Reservation outcome reasons when Reserved is false.
const (
	ReasonOutOfStock = "out_of_stock"
	ReasonTimeout    = "timeout"
	ReasonConflict   = "conflict"
)

// ReserveResult reports one reservation attempt. Reserved=true requires a
// non-empty ReservationRef; anything else leaves the participation's
// reservation state pending/failed — never confirmed.
type ReserveResult struct {
	Reserved       bool
	ReservationRef string
	Reason         string
}
