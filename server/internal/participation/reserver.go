package participation

import "context"

// BenefitReserver is the trusted port toward the reward domain (HUI-1671 / G3).
// The participation domain creates the participation FIRST, then reserves
// with a stable idempotent reference; the reward side can never reverse this
// request into "create a participation for me".
//
// 纪律:
//   - The production server connects the original reward ledger. A nil port
//     is retained for legacy contracts/tests and never reports a reservation.
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
	CreatedAt          string
	ExpiresAt          string
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
	// NotRequested applies only to legacy participations without a configured reward promise.
	NotRequested bool

	Reserved       bool
	ReservationRef string
	Reason         string
}

// StartResolver is an optional trusted preparation stage. It resolves the
// published reward promise before a new participation is stored. It runs only
// after the caller's original idempotent participation has been checked.
type StartResolver interface {
	ResolveStart(context.Context, StartInput) (StartInput, error)
}

// ReservationReader projects the current original reward fact without writing
// or renewing it. Stored participation state is not proof of an unexpired hold.
type ReservationReader interface {
	ReadReservation(context.Context, ParticipationRef) (ReserveResult, error)
}

// StartRefusedError carries a public-safe refusal from the published authority.
type StartRefusedError struct {
	Reason, Field, Message string
	Status                 int
}

func (e *StartRefusedError) Error() string { return "participation: " + e.Reason }
