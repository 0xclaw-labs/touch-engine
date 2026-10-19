package store

// HUI-3553 W5：受信服务生产者按 handoff id 反查（tenant 从行取，不信任调用方租户头）。

import (
	"errors"
	"testing"
)

func TestGetContentHandoffByID(t *testing.T) {
	s := openPackStore(t)
	tenA, campaignA, _, _ := packFixture(t, s)
	h, _, err := s.CreateContentHandoff(tenA, campaignA, HandoffInput{
		Purpose: "周末活动推广", StoreBrief: "旗舰店", DesiredForm: "竖版短视频",
		IdempotencyKey: "idem-bi-1", ExecutionPreference: "night_batch",
		PolicyRef: "policy://night-batch", PolicyVersion: 7,
	}, "owner-1")
	must(t, err)

	got, err := s.GetContentHandoffByID(h.ID)
	must(t, err)
	if got.ID != h.ID || got.TenantID != tenA || got.CampaignID != campaignA {
		t.Fatalf("by-id row mismatch: %+v", got)
	}
	if got.JobRef != "" || len(got.JobQueueView) != 0 {
		t.Fatalf("new handoff must start without queue view: %+v", got)
	}

	if _, err := s.GetContentHandoffByID("cho_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown handoff must be ErrNotFound, got %v", err)
	}
}
