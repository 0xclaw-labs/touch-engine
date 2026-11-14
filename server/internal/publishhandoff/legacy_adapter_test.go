package publishhandoff

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLegacyAdapterUsesSameFrozenHandoffAndUnknownQuery(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	p, a, visitor := seedFrozenAttempt(t, s, "legacy-adapter")
	input := startInput(p, a, visitor)
	original, err := s.Start(context.Background(), input, admittedXHSRecord(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := mediaAction(t)
	q := v2Query("ios_safari")
	q.TenantID = input.TenantID
	q.Now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	q.MerchantAppRef = "app"
	adapter := &prepareCaptureAdapter{stubAdapter: stubAdapter{platform: "xiaohongshu", prep: PrepareResult{ClientAction: ClientAction{Type: ClientActionOAuthConsent, Params: map[string]string{"phase": "confirm_submit", "account_ref": "trusted-original"}}}, submitErr: unknownSubmitError{}, query: QueryOutcome{State: "pending"}}}
	s.SetAdapter(adapter)
	h, err := s.AdaptLegacy(context.Background(), original.Handoff.ID, visitor, rec, q)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != original.Handoff.ID || h.ParticipationID != p || h.AttemptID != a || h.PayloadKind != "" || h.ContentVersion != input.ConfirmedContentVersion {
		t.Fatalf("changed original authority: %#v", h)
	}
	if len(adapter.requests) != 1 {
		t.Fatal("original adapter Prepare was not called")
	}
	if _, err = s.SubmitAction(context.Background(), h.ID, visitor); err == nil {
		t.Fatal("must return unknown")
	}
	got, err := s.GetActionForVisitor(context.Background(), h.ID, visitor)
	if err != nil || got.Status != StateUnknown {
		t.Fatalf("unknown persistence: %v %s", err, got.Status)
	}
	if _, err = s.SubmitAction(context.Background(), h.ID, visitor); !errors.Is(err, ErrInvalidState) {
		t.Fatal("legacy must not resend")
	}
	if _, err = s.QueryAction(context.Background(), h.ID, visitor); err != nil {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM publish_handoffs`).Scan(&count)
	if count != 1 {
		t.Fatalf("created second handoff: %d", count)
	}
	if _, err = s.FindLegacyAdapter(context.Background(), p, h.Platform, "other-visitor"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong visitor: %v", err)
	}
}
