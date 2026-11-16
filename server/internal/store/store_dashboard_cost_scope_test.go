package store

import "testing"

// These are aggregation fixtures, not evidence of a real production charge.
func TestDashboardContentCostHonorsTrustedStoreAndSelectedStore(t *testing.T) {
	w := seedOpsWorld(t)
	c2, err := w.s.CreateCampaign(NewCampaign{TenantID: w.tenA, StoreID: w.store2, Title: "另一门店", CreatedBy: "test"})
	must(t, err)
	w.rawCopyJob(t, "cost-a", w.campA1, `{"trace":{"billed":true,"amount_minor":100}}`, "2026-10-14T10:00:00Z")
	w.rawCopyJob(t, "cost-b", c2.ID, `{"trace":{"billed":true,"amount_minor":900}}`, "2026-10-14T10:00:00Z")
	w.rawCopyJob(t, "cost-unassigned", w.campA2, `{"trace":{"billed":true,"amount_minor":700}}`, "2026-10-14T10:00:00Z")
	start, end := opsWinBounds(t)
	for _, tc := range []struct {
		name, scope, selected string
		amount, runs          int64
	}{
		{"trusted store", w.store1, "", 100, 1},
		{"selected store", "", w.store2, 900, 1},
		{"scope cannot be widened", w.store1, w.store2, 100, 1},
		{"tenant", "", "", 1700, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, _, err := w.s.DashboardOps(w.tenA, tc.scope, start, end, OpsFilter{StoreID: tc.selected})
			must(t, err)
			if facts.Cost.ContentGenerationAmountMinor == nil || *facts.Cost.ContentGenerationAmountMinor != tc.amount || facts.Cost.ContentGenerationBilledRuns != tc.runs {
				t.Fatalf("content cost escaped scope: %+v, want amount=%d runs=%d", facts.Cost, tc.amount, tc.runs)
			}
		})
	}
}

func TestDashboardContentCostDoesNotPresentPartialAmountsAsComplete(t *testing.T) {
	for _, tc := range []struct{ name, field string }{
		{"missing", ""}, {"null", `,"amount_minor":null`}, {"invalid string", `,"amount_minor":"missing"`}, {"negative", `,"amount_minor":-1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := seedOpsWorld(t)
			w.rawCopyJob(t, "known", w.campA1, `{"trace":{"billed":true,"amount_minor":100}}`, "2026-10-14T10:00:00Z")
			w.rawCopyJob(t, "unknown", w.campA1, `{"trace":{"billed":true`+tc.field+`}}`, "2026-10-14T10:00:00Z")
			start, end := opsWinBounds(t)
			facts, _, err := w.s.DashboardOps(w.tenA, w.store1, start, end, OpsFilter{})
			must(t, err)
			if facts.Cost.ContentGenerationAmountMinor != nil || facts.Cost.ContentGenerationBilledRuns != 2 {
				t.Fatalf("partial or invalid amount must remain unknown, got %+v", facts.Cost)
			}
		})
	}
}
