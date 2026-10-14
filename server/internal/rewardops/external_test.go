package rewardops

import (
	"context"
	"errors"
	"testing"
	"time"
)

// HUI-1671 B3: an external coupon service is integrated only through this
// interface. A timeout is answered by querying again with the SAME idempotency
// key; an unknown outcome shows "pending confirmation" and is never presented
// as a successful issuance. This ticket ships the interface and stub tests
// only; no real third party is called.

type fakeIssuer struct {
	issueCalls   int
	lookupCalls  int
	issueKeys    []string
	lookupKeys   []string
	issueErr     error
	issueResult  IssuanceResult
	lookupResult IssuanceResult
}

func (f *fakeIssuer) Issue(ctx context.Context, req IssuanceRequest) (IssuanceResult, error) {
	f.issueCalls++
	f.issueKeys = append(f.issueKeys, req.IdempotencyKey)
	if f.issueErr != nil {
		return IssuanceResult{}, f.issueErr
	}
	return f.issueResult, nil
}

func (f *fakeIssuer) Lookup(ctx context.Context, idempotencyKey string) (IssuanceResult, error) {
	f.lookupCalls++
	f.lookupKeys = append(f.lookupKeys, idempotencyKey)
	return f.lookupResult, nil
}

func TestIssueWithRequerySuccessFirstTry(t *testing.T) {
	fake := &fakeIssuer{issueResult: IssuanceResult{State: StateIssued, Code: "X-1"}}
	res, err := IssueWithRequery(context.Background(), fake, "idem-1", func() (IssuanceResult, error) {
		return fake.Issue(context.Background(), IssuanceRequest{IdempotencyKey: "idem-1"})
	})
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if res.State != StateIssued || fake.issueCalls != 1 || fake.lookupCalls != 0 {
		t.Fatalf("success first try: %+v issue=%d lookup=%d", res, fake.issueCalls, fake.lookupCalls)
	}
}

func TestIssueWithRequeryTimeoutUsesSameKey(t *testing.T) {
	fake := &fakeIssuer{
		issueErr:     context.DeadlineExceeded,
		lookupResult: IssuanceResult{State: StateIssued, Code: "X-2"},
	}
	issue := func() (IssuanceResult, error) {
		return fake.Issue(context.Background(), IssuanceRequest{IdempotencyKey: "idem-2"})
	}
	lookup := func() (IssuanceResult, error) {
		return fake.Lookup(context.Background(), "idem-2")
	}
	res, err := IssueWithRequery(context.Background(), fake, "idem-2", issue, lookup)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if res.State != StateIssued {
		t.Fatalf("requery should recover the issuance: %+v", res)
	}
	if fake.issueCalls != 1 || fake.lookupCalls != 1 {
		t.Fatalf("issue=%d lookup=%d", fake.issueCalls, fake.lookupCalls)
	}
	if len(fake.issueKeys) != 1 || len(fake.lookupKeys) != 1 || fake.lookupKeys[0] != "idem-2" {
		t.Fatalf("lookup must reuse the same idempotency key: %v %v", fake.issueKeys, fake.lookupKeys)
	}
}

func TestIssueWithRequeryUnknownStaysPending(t *testing.T) {
	fake := &fakeIssuer{
		issueErr:     errors.New("connection reset"),
		lookupResult: IssuanceResult{State: StateUnknown},
	}
	issue := func() (IssuanceResult, error) {
		return fake.Issue(context.Background(), IssuanceRequest{IdempotencyKey: "idem-3"})
	}
	lookup := func() (IssuanceResult, error) {
		return fake.Lookup(context.Background(), "idem-3")
	}
	res, err := IssueWithRequery(context.Background(), fake, "idem-3", issue, lookup)
	if err != nil {
		t.Fatalf("unknown outcome is not a transport error: %v", err)
	}
	if res.State != StateUnknown {
		t.Fatalf("unknown must stay unknown: %+v", res)
	}
	if res.State == StateIssued {
		t.Fatal("an unknown result must never be shown as issued")
	}
}

func TestIssueWithRequeryFailedIsHonest(t *testing.T) {
	fake := &fakeIssuer{issueResult: IssuanceResult{State: StateFailed, Detail: "declined"}}
	issue := func() (IssuanceResult, error) {
		return fake.Issue(context.Background(), IssuanceRequest{IdempotencyKey: "idem-4"})
	}
	res, err := IssueWithRequery(context.Background(), fake, "idem-4", issue)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if res.State != StateFailed {
		t.Fatalf("declined must stay failed: %+v", res)
	}
}

func TestIssuanceStates(t *testing.T) {
	if StateIssued != "issued" || StateUnknown != "pending_unknown" || StateFailed != "failed" {
		t.Fatalf("state constants drifted: %s %s %s", StateIssued, StateUnknown, StateFailed)
	}
}

// The context deadline variant: IssueWithRequery itself must bound its work.
func TestIssueWithRequeryRespectsDeadline(t *testing.T) {
	fake := &fakeIssuer{issueErr: context.DeadlineExceeded, lookupResult: IssuanceResult{State: StateUnknown}}
	issue := func() (IssuanceResult, error) {
		return fake.Issue(context.Background(), IssuanceRequest{IdempotencyKey: "idem-5"})
	}
	lookup := func() (IssuanceResult, error) {
		return fake.Lookup(context.Background(), "idem-5")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := IssueWithRequery(ctx, fake, "idem-5", issue, lookup)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if res.State != StateUnknown {
		t.Fatalf("expected pending confirmation: %+v", res)
	}
}
