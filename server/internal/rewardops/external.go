package rewardops

import (
	"context"
)

// Issuance states for an external coupon service result.
const (
	StateIssued  = "issued"
	StateUnknown = "pending_unknown" // show 待确认, never a success
	StateFailed  = "failed"
)

// IssuanceRequest carries the caller's idempotency key. A retry or a requery
// after a timeout must reuse the same key so the remote cannot double-issue.
type IssuanceRequest struct {
	IdempotencyKey  string
	TenantID        string
	CampaignID      string
	ParticipationID string
	BenefitID       string
	InventoryID     string
	FaceMinor       int64
	CostMinor       int64
	Currency        string
}

// IssuanceResult is what the external service answered. Code is the coupon
// code and is sensitive: it never enters logs or the operations payload.
type IssuanceResult struct {
	State  string
	Code   string
	Detail string
}

// ExternalIssuer is the only seam to a real third-party coupon service. This
// ticket defines the interface and proves the requery discipline with stubs;
// no production wiring exists.
type ExternalIssuer interface {
	Issue(ctx context.Context, req IssuanceRequest) (IssuanceResult, error)
	// Lookup asks the remote about an earlier request by the SAME idempotency
	// key. It never creates a new coupon.
	Lookup(ctx context.Context, idempotencyKey string) (IssuanceResult, error)
}

// IssueWithRequery runs issue and, when the transport fails (timeout, reset,
// any error), queries once with the same key. The result is honest: a state
// the remote could not confirm stays pending_unknown — it is never upgraded
// to issued and never invented.
func IssueWithRequery(ctx context.Context, issuer ExternalIssuer, idempotencyKey string, issue func() (IssuanceResult, error), lookup ...func() (IssuanceResult, error)) (IssuanceResult, error) {
	if err := ctx.Err(); err != nil {
		return IssuanceResult{}, err
	}
	res, err := issue()
	if err == nil {
		return res, nil
	}
	if len(lookup) == 0 || lookup[0] == nil {
		return IssuanceResult{State: StateUnknown, Detail: "issue_failed_without_lookup"}, nil
	}
	queried, lerr := lookup[0]()
	if lerr != nil {
		return IssuanceResult{State: StateUnknown, Detail: "issue_and_lookup_failed"}, nil
	}
	if queried.State == StateIssued || queried.State == StateFailed {
		return queried, nil
	}
	return IssuanceResult{State: StateUnknown, Detail: "remote_result_unconfirmed"}, nil
}
