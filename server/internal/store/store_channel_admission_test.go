package store

import (
	"errors"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// TestLoadAdmissionSeedRoundTrip: the v1 seed loads, reads back field-faithful
// through the store, and Gate over the stored rows refuses exactly like the
// pure layer — 库内记录与 JSON 种子一致性 (acceptance: the read model never
// drifts from the contract).
func TestLoadAdmissionSeedRoundTrip(t *testing.T) {
	s := openStore(t)
	seed, err := channeladmission.EmbeddedSeed()
	must(t, err)

	changed, err := s.LoadAdmissionSeed(seed)
	must(t, err)
	if changed != len(seed.Records) {
		t.Fatalf("first load changed %d records, want %d", changed, len(seed.Records))
	}

	list, err := s.ListChannelAdmissions("t_any")
	must(t, err)
	if len(list) != len(seed.Records) {
		t.Fatalf("tenant list has %d rows, want %d", len(list), len(seed.Records))
	}

	// field-faithful round trip for every record
	for _, want := range seed.Records {
		got, err := s.GetChannelAdmission(want.Platform, want.EntryMode, want.Scenario, "t_any")
		must(t, err)
		if got.Status != want.Status || got.ReasonCode != want.ReasonCode ||
			got.TenantScope != want.TenantScope || got.AppBinding != want.AppBinding ||
			got.LastCheckedAt != want.LastCheckedAt || got.ValidUntil != want.ValidUntil ||
			got.ReasonText != want.ReasonText || got.CloseReason != want.CloseReason ||
			got.RewardAdmitted != want.RewardAdmitted {
			t.Fatalf("%s round-trip mismatch:\n got %+v\nwant %+v", want.IdentityKey(), got, want)
		}
		if len(got.OfficialSources) != len(want.OfficialSources) {
			t.Fatalf("%s sources mismatch: %v vs %v", want.IdentityKey(), got.OfficialSources, want.OfficialSources)
		}
		for i := range want.OfficialSources {
			if got.OfficialSources[i] != want.OfficialSources[i] {
				t.Fatalf("%s source[%d] mismatch", want.IdentityKey(), i)
			}
		}
		if got.Capabilities != want.Capabilities {
			t.Fatalf("%s capabilities mismatch: %+v vs %+v", want.IdentityKey(), got.Capabilities, want.Capabilities)
		}
		if len(got.Missing) != len(want.Missing) {
			t.Fatalf("%s missing hints mismatch: %v vs %v", want.IdentityKey(), got.Missing, want.Missing)
		}

		// Gate over the stored row refuses identically to the pure layer
		now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
		dStore := channeladmission.Gate(channeladmission.GateInput{Now: now, TenantID: "t_any"}, got)
		dPure := channeladmission.Gate(channeladmission.GateInput{Now: now, TenantID: "t_any"}, want)
		if dStore.Allowed != dPure.Allowed || dStore.Reason != dPure.Reason {
			t.Fatalf("%s store/pure gate divergence: %+v vs %+v", want.IdentityKey(), dStore, dPure)
		}
		if dStore.Allowed {
			t.Fatalf("%s must not be allowed (no platform approval exists)", want.IdentityKey())
		}
	}
}

// TestLoadAdmissionSeedIdempotentRevisions: re-loading the identical seed
// appends nothing; a payload change appends exactly one update revision.
func TestLoadAdmissionSeedIdempotentRevisions(t *testing.T) {
	s := openStore(t)
	seed, err := channeladmission.EmbeddedSeed()
	must(t, err)
	if _, err := s.LoadAdmissionSeed(seed); err != nil {
		t.Fatalf("first load: %v", err)
	}
	revs := func(platform, mode, scenario string) int {
		n, err := s.CountChannelAdmissionRevisions(platform, mode, scenario, "*")
		must(t, err)
		return n
	}
	const key = "kuaishou"
	if n := revs(key, "native", "store_promo"); n != 1 {
		t.Fatalf("after first load revisions = %d, want 1 (create)", n)
	}

	// identical re-load: zero churn
	changed, err := s.LoadAdmissionSeed(seed)
	must(t, err)
	if changed != 0 {
		t.Fatalf("identical re-load changed %d records, want 0", changed)
	}
	if n := revs(key, "native", "store_promo"); n != 1 {
		t.Fatalf("identical re-load appended revisions, want still 1, got %d", n)
	}

	// payload change (simulate a future platform approval): exactly one update
	modified := *seed
	modified.Records = append([]channeladmission.Record(nil), seed.Records...)
	for i := range modified.Records {
		if modified.Records[i].IdentityKey() == "kuaishou|native|store_promo" {
			modified.Records[i].Status = channeladmission.StatusAdmitted
			modified.Records[i].ReasonCode = "ok"
			modified.Records[i].ReasonText = "平台批复已获（合成示例，测试修订追踪）"
			modified.Records[i].RewardAdmitted = true
		}
	}
	changed, err = s.LoadAdmissionSeed(&modified)
	must(t, err)
	if changed != 1 {
		t.Fatalf("modified load changed %d records, want 1", changed)
	}
	if n := revs(key, "native", "store_promo"); n != 2 {
		t.Fatalf("after payload change revisions = %d, want 2 (create+update)", n)
	}
	got, err := s.GetChannelAdmission("kuaishou", "native", "store_promo", "t1")
	must(t, err)
	if got.Status != channeladmission.StatusAdmitted || !got.RewardAdmitted {
		t.Fatalf("updated row not persisted: %+v", got)
	}
	// Gate now allows a reward play on the (synthetic) approved route
	d := channeladmission.Gate(channeladmission.GateInput{
		Now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), TenantID: "t1", RewardPlay: true}, got)
	if !d.Allowed {
		t.Fatalf("approved route must gate-allow the reward play, got %q", d.Reason)
	}
}

// TestChannelAdmissionTenantScopeBestMatch: a tenant-scoped row wins over the
// platform "*" row inside its own tenant; other tenants only ever see the
// platform row — 错 app/tenant 拒绝的存储侧。
func TestChannelAdmissionTenantScopeBestMatch(t *testing.T) {
	s := openStore(t)
	seed, err := channeladmission.EmbeddedSeed()
	must(t, err)
	if _, err := s.LoadAdmissionSeed(seed); err != nil {
		t.Fatalf("load: %v", err)
	}

	// tenant t_mer gets its own douyin h5 row (synthetic admitted variant)
	tenantSeed := &channeladmission.Seed{Version: 1, GeneratedAt: "2026-10-14"}
	row := seed.Records[0] // douyin|h5|store_promo (not_admitted, scope *)
	row.TenantScope = "t_mer"
	row.Status = channeladmission.StatusAdmitted
	row.ReasonCode = "ok"
	row.ReasonText = "定向获批（合成，测试 best-match）"
	row.ValidUntil = "2027-01-12"
	tenantSeed.Records = []channeladmission.Record{row}
	if _, err := s.LoadAdmissionSeed(tenantSeed); err != nil {
		t.Fatalf("load tenant seed: %v", err)
	}

	got, err := s.GetChannelAdmission("douyin", "h5", "store_promo", "t_mer")
	must(t, err)
	if got.TenantScope != "t_mer" || got.Status != channeladmission.StatusAdmitted {
		t.Fatalf("tenant row must win inside its tenant, got scope=%s status=%s", got.TenantScope, got.Status)
	}
	other, err := s.GetChannelAdmission("douyin", "h5", "store_promo", "t_other")
	must(t, err)
	if other.TenantScope != "*" || other.Status != channeladmission.StatusNotAdmitted {
		t.Fatalf("other tenant must see only the platform row, got scope=%s status=%s", other.TenantScope, other.Status)
	}

	// the merchant list never duplicates nor leaks: t_mer sees 5 platform rows
	// + exactly 1 scoped row; t_other sees exactly the 5 platform rows.
	lm, err := s.ListChannelAdmissions("t_mer")
	must(t, err)
	if len(lm) != len(seed.Records)+1 {
		t.Fatalf("t_mer list = %d rows, want %d", len(lm), len(seed.Records)+1)
	}
	lo, err := s.ListChannelAdmissions("t_other")
	must(t, err)
	if len(lo) != len(seed.Records) {
		t.Fatalf("t_other list = %d rows, want %d", len(lo), len(seed.Records))
	}
}

// TestGetChannelAdmissionUnknownRoute: a route that no record covers is
// ErrNotFound — callers must treat it as unknown rules, never as allowed.
func TestGetChannelAdmissionUnknownRoute(t *testing.T) {
	s := openStore(t)
	seed, err := channeladmission.EmbeddedSeed()
	must(t, err)
	if _, err := s.LoadAdmissionSeed(seed); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := s.GetChannelAdmission("channels", "h5", "store_promo", "t1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown route must be ErrNotFound, got %v", err)
	}
}
