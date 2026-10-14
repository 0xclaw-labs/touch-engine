package participation

// T2: 状态机语义。禁单一 success 布尔;fulfilled 是终态且只能由
// MarkFulfilled(G3 接线)到达;晚到失败不静默改写已兑现。

import (
	"errors"
	"testing"
	"time"
)

func TestStatusTransitions(t *testing.T) {
	legal := map[Status][]Status{
		StatusPending:        {StatusInProgress, StatusAwaitingReview, StatusCanceled},
		StatusInProgress:     {StatusAwaitingReview, StatusCanceled},
		StatusAwaitingReview: {StatusFulfilled, StatusCanceled},
		StatusFulfilled:      {},
		StatusCanceled:       {},
	}
	for from, tos := range legal {
		for _, to := range tos {
			if !CanTransition(from, to) {
				t.Errorf("%s -> %s should be legal", from, to)
			}
		}
	}
	illegal := [][2]Status{
		{StatusPending, StatusFulfilled},   // 预留未确认不算兑现
		{StatusFulfilled, StatusPending},   // 不降级:晚到失败不改写已兑现
		{StatusFulfilled, StatusCanceled},  // 终态
		{StatusCanceled, StatusInProgress}, // 终态
		{StatusCanceled, StatusFulfilled},  // 终态:取消后不再兑现
	}
	for _, p := range illegal {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s should be illegal", p[0], p[1])
		}
	}
	// 未知状态一律拒绝(fail-closed)。
	if CanTransition(Status("weird"), StatusPending) || CanTransition(StatusPending, Status("weird")) {
		t.Error("unknown statuses must fail closed")
	}
}

func TestTouchpointSourceValid(t *testing.T) {
	for _, s := range []string{"nfc", "qr", "shorturl", "manual"} {
		if !ValidSource(Source(s)) {
			t.Errorf("source %q should be valid", s)
		}
	}
	if ValidSource("fingerprint") || ValidSource("") {
		t.Error("unknown/empty source must be invalid")
	}
}

func TestWindowExpiry(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	w := Window{StartsAt: "2026-10-14T00:00:00Z", ExpiresAt: "2026-10-14T06:00:00Z"}
	if !w.Expired(now) {
		t.Fatal("window expired at 06:00 must read expired at 12:00")
	}
	open := Window{StartsAt: "2026-10-14T00:00:00Z", ExpiresAt: "2026-10-15T00:00:00Z"}
	if open.Expired(now) {
		t.Fatal("open window must not read expired")
	}
}

func TestSentinelErrorsAreDistinct(t *testing.T) {
	errs := []error{ErrNotFound, ErrIdempotencyConflict, ErrFrozen, ErrInvalidSource,
		ErrInvalidTransition, ErrCredentialExpired, ErrCredentialUsed,
		ErrCredentialRevoked, ErrTargetMismatch, ErrNotEntitled}
	seen := map[error]bool{}
	for _, e := range errs {
		if e == nil || seen[e] {
			t.Fatalf("sentinel %v missing or duplicated", e)
		}
		seen[e] = true
	}
	if !errors.Is(ErrCredentialExpired, ErrCredentialExpired) {
		t.Fatal("errors.Is self-check")
	}
}
