package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

// seedResolveScenario builds one campaign with one link in a configurable
// state, plus an optional store binding, and returns the short code.
func seedResolveScenario(t *testing.T, s *Store, status, startsAt, endsAt string, bindStore *StoreRecord, linkEnabled bool) string {
	t.Helper()
	ten, err := s.CreateTenant("商家")
	must(t, err)
	if bindStore != nil {
		st, err := s.CreateStore(ten.ID, "门店", "地址", "seed")
		must(t, err)
		*bindStore = st
	}
	c, err := s.CreateCampaign(NewCampaign{TenantID: ten.ID, Title: "活动", PublicContent: "内容",
		StartsAt: startsAt, EndsAt: endsAt, CreatedBy: "seed"})
	must(t, err)
	if campaign.Status(status) != campaign.StatusDraft {
		if bindStore != nil {
			sid := bindStore.ID
			c, err = s.UpdateCampaign(c.ID, ten.ID, CampaignPatch{StoreID: &sid})
			must(t, err)
		}
		if campaign.Status(status) == campaign.StatusPaused {
			c, err = s.TransitionCampaign(c.ID, ten.ID, campaign.StatusActive) // draft→paused is illegal; route via active
			must(t, err)
		}
		c, err = s.TransitionCampaign(c.ID, ten.ID, campaign.Status(status))
		must(t, err)
	} else if bindStore != nil {
		sid := bindStore.ID
		_, err = s.UpdateCampaign(c.ID, ten.ID, CampaignPatch{StoreID: &sid})
		must(t, err)
	}
	l, err := s.CreateLink(ten.ID, c.ID, "seed")
	must(t, err)
	if !linkEnabled {
		l, err = s.SetLinkEnabled(l.ID, ten.ID, false)
		must(t, err)
	}
	return l.Code
}

// TestDecideLinkEquivalenceWithResolveLink pins the cached-path pure function
// to the flag-off ResolveLink behavior across the full scenario matrix: for
// every combination, DecideLink(LoadResolvedRows(code), at) must equal
// ResolveLink(code, at) in outcome and attached rows. This is the anti-drift
// guarantee that lets ResolveLink stay untouched.
func TestDecideLinkEquivalenceWithResolveLink(t *testing.T) {
	now := time.Now().UTC()
	window := map[string][2]string{
		"active":      {now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)},
		"expired":     {now.Add(-2 * time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339)},
		"not_started": {now.Add(time.Hour).Format(time.RFC3339), now.Add(2 * time.Hour).Format(time.RFC3339)},
	}
	storeStates := map[string]func(t *testing.T, s *Store, id, tenant string){
		"unbound": nil,
		"active":  nil,
		"disabled": func(t *testing.T, s *Store, id, tenant string) {
			_, err := s.SetStoreStatus(id, tenant, StoreStatusDisabled)
			must(t, err)
		},
	}
	statuses := []string{"active", "paused", "draft", "ended"}
	linkStates := []bool{true, false}

	for wname, w := range window {
		for sname, mutate := range storeStates {
			for _, status := range statuses {
				for _, enabled := range linkStates {
					name := fmt.Sprintf("%s/%s/%s/link=%v", wname, sname, status, enabled)
					t.Run(name, func(t *testing.T) {
						s := openStore(t)
						var st StoreRecord
						var bind *StoreRecord
						if sname != "unbound" {
							bind = &st
						}
						code := seedResolveScenario(t, s, status, w[0], w[1], bind, enabled)
						if mutate != nil && bind != nil {
							mutate(t, s, st.ID, st.TenantID)
						}
						at := now
						want := s.ResolveLink(code, at)
						got := DecideLink(s.LoadResolvedRows(code), at)
						if got.Outcome != want.Outcome {
							t.Fatalf("outcome = %q, want %q", got.Outcome, want.Outcome)
						}
						if got.StoreUnavailable != want.StoreUnavailable {
							t.Fatalf("store_unavailable = %v, want %v", got.StoreUnavailable, want.StoreUnavailable)
						}
						if got.Campaign != want.Campaign || got.Link != want.Link {
							t.Fatalf("rows differ:\n got %+v\nwant %+v", got, want)
						}
					})
				}
			}
		}
	}
}

// TestDecideLinkUnknownAndMalformed: both NotFound, indistinguishable.
func TestDecideLinkUnknownAndMalformed(t *testing.T) {
	s := openStore(t)
	if got := s.ResolveLink("ZZZZZZZZZZZZ", time.Now()); got.Outcome != OutcomeNotFound {
		t.Fatalf("malformed = %q", got.Outcome)
	}
	if got := DecideLink(s.LoadResolvedRows("ZZZZZZZZZZZZ"), time.Now()); got.Outcome != OutcomeNotFound {
		t.Fatalf("malformed cached path = %q", got.Outcome)
	}
	if got := DecideLink(ResolvedRows{}, time.Now()); got.Outcome != OutcomeNotFound {
		t.Fatalf("empty rows = %q", got.Outcome)
	}
}

// TestDecideLinkJudgesWindowAtCallTime: the same rows decide differently as
// the clock crosses the window boundary (available is never cached as
// forever-valid; HUI-2981 invariant 4).
func TestDecideLinkJudgesWindowAtCallTime(t *testing.T) {
	s := openStore(t)
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	code := seedResolveScenario(t, s, "active", start.Format(time.RFC3339), end.Format(time.RFC3339), nil, true)
	rows := s.LoadResolvedRows(code)

	before := DecideLink(rows, start.Add(-time.Second))
	if before.Outcome != OutcomeNotStarted {
		t.Fatalf("before window = %q", before.Outcome)
	}
	inside := DecideLink(rows, start.Add(time.Second))
	if inside.Outcome != OutcomeAvailable {
		t.Fatalf("inside window = %q", inside.Outcome)
	}
	after := DecideLink(rows, end.Add(time.Second))
	if after.Outcome != OutcomeExpired {
		t.Fatalf("after window = %q", after.Outcome)
	}
}

// TestEpochBumpsOnStructuralWrites: every management write to a cached
// read-model table must advance the epoch (deterministic 0-stale invalidation);
// factual writes must not.
func TestEpochBumpsOnStructuralWrites(t *testing.T) {
	s := openStore(t)
	ten, err := s.CreateTenant("商家")
	must(t, err)
	if s.Epoch() == 0 {
		t.Fatal("CreateTenant must bump epoch")
	}
	e := s.Epoch()

	st, err := s.CreateStore(ten.ID, "门店", "地址", "seed")
	must(t, err)
	if s.Epoch() != e+1 {
		t.Fatal("CreateStore must bump")
	}
	e = s.Epoch()
	if _, err := s.UpdateStore(st.ID, ten.ID, strPtr("新名"), nil); err != nil || s.Epoch() != e+1 {
		t.Fatal("UpdateStore must bump")
	}
	e = s.Epoch()
	if _, err := s.SetStoreStatus(st.ID, ten.ID, StoreStatusDisabled); err != nil || s.Epoch() != e+1 {
		t.Fatal("SetStoreStatus must bump")
	}
	e = s.Epoch()

	c, err := s.CreateCampaign(NewCampaign{TenantID: ten.ID, Title: "活动", CreatedBy: "seed"})
	must(t, err)
	if s.Epoch() != e+1 {
		t.Fatal("CreateCampaign must bump")
	}
	e = s.Epoch()
	title := "新标题"
	if _, err := s.UpdateCampaign(c.ID, ten.ID, CampaignPatch{Title: &title}); err != nil || s.Epoch() != e+1 {
		t.Fatal("UpdateCampaign must bump")
	}
	e = s.Epoch()
	if _, err := s.TransitionCampaign(c.ID, ten.ID, campaign.StatusActive); err != nil || s.Epoch() != e+1 {
		t.Fatal("TransitionCampaign must bump")
	}
	e = s.Epoch()

	l, err := s.CreateLink(ten.ID, c.ID, "seed")
	must(t, err)
	if s.Epoch() != e+1 {
		t.Fatal("CreateLink must bump")
	}
	e = s.Epoch()
	if _, err := s.SetLinkEnabled(l.ID, ten.ID, false); err != nil || s.Epoch() != e+1 {
		t.Fatal("SetLinkEnabled must bump")
	}
	e = s.Epoch()

	if _, err := s.UpsertLeadForm(ten.ID, c.ID, "v1", false, "seed"); err != nil || s.Epoch() != e+1 {
		t.Fatal("UpsertLeadForm must bump")
	}
	e = s.Epoch()

	if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: c.ID, LinkIDs: []string{l.ID}, Count: 1, BindMode: "shared", LabelPrefix: "T"}); err != nil || s.Epoch() != e+1 {
		t.Fatal("CreateTagsBatch must bump")
	}
	e = s.Epoch()
	tags, err := s.ListTags(ten.ID, TagFilter{})
	must(t, err)
	if len(tags) != 1 {
		t.Fatalf("tags = %d", len(tags))
	}
	if _, err := s.SetTagStatus(tags[0].ID, ten.ID, TagStatusDisabled); err != nil || s.Epoch() != e+1 {
		t.Fatal("SetTagStatus must bump (it also flips the bound link)")
	}
	e = s.Epoch()
	label := "新标签"
	if _, err := s.PatchTag(tags[0].ID, ten.ID, TagPatch{Label: &label}); err != nil || s.Epoch() != e+1 {
		t.Fatal("PatchTag must bump")
	}
	e = s.Epoch()
	if err := s.DeleteTag(tags[0].ID, ten.ID); err != nil || s.Epoch() != e+1 {
		t.Fatal("DeleteTag must bump")
	}
	e = s.Epoch()

	if err := s.SetTenantLifecycle(ten.ID, LifecycleSuspended); err != nil || s.Epoch() != e+1 {
		t.Fatal("SetTenantLifecycle must bump")
	}
	e = s.Epoch()
	if err := s.BindTenantBrand(ten.ID, "brd_x"); err != nil || s.Epoch() != e+1 {
		t.Fatal("BindTenantBrand must bump")
	}
	e = s.Epoch()
	if err := s.SetChargeHold(ten.ID, true); err != nil || s.Epoch() != e+1 {
		t.Fatal("SetChargeHold must bump")
	}
	e = s.Epoch()
	if err := s.StampLinkBrand(l.ID, ten.ID, "brd_x", "h.example.com"); err != nil || s.Epoch() != e+1 {
		t.Fatal("StampLinkBrand must bump")
	}

	// negative control: an unknown-id update that writes nothing must not bump
	e = s.Epoch()
	_, _ = s.UpdateCampaign("cmp_missing", ten.ID, CampaignPatch{Title: &title})
	if s.Epoch() != e {
		t.Fatal("failed/no-op write must not bump")
	}
}

// TestLoadResolvedRowsNegativeCacheable: a missing code yields Found=false
// rows (cacheable as a negative entry under the same epoch).
func TestLoadResolvedRowsNegativeCacheable(t *testing.T) {
	s := openStore(t)
	rows := s.LoadResolvedRows("0123456789AB")
	if rows.Found {
		t.Fatal("unknown code must not be Found")
	}
	if got := DecideLink(rows, time.Now()); got.Outcome != OutcomeNotFound {
		t.Fatalf("negative rows decide = %q", got.Outcome)
	}
}
