package channeladmission

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// contractSeedPath locates the versioned contract truth relative to this
// package directory (server/internal/channeladmission -> repo root).
const contractSeedPath = "../../../docs/contracts/channel-admission/v1/seed.json"

// TestSeedV1FiveTicketTruths locks the 2026-10-14 official research conclusions
// recorded on the ticket: each identity key with its admission status, official
// source URL and check date. unknown must never silently become allowed — that
// invariant is enforced by Gate (Task 2) and asserted again in store tests.
func TestSeedV1FiveTicketTruths(t *testing.T) {
	seed, err := EmbeddedSeed()
	if err != nil {
		t.Fatalf("parse embedded seed: %v", err)
	}
	if seed.Version != 1 {
		t.Fatalf("seed version = %d, want 1", seed.Version)
	}
	want := []struct {
		key    string // platform|entry_mode|scenario
		status string
		source string
	}{
		{"douyin|h5|store_promo", StatusNotAdmitted,
			"https://developer.open-douyin.com/docs/resource/zh-CN/dop/operation-standard/platform-capabilities/useclue"},
		{"douyin|miniapp|reward_task", StatusUnknown,
			"https://developer.open-douyin.com/docs/resource/zh-CN/mini-app/open-capacity/operation/douyin-video/douyin-task"},
		{"xiaohongshu|miniapp|store_promo", StatusUnknown,
			"https://miniapp.xiaohongshu.com/doc/DC530301"},
		{"bilibili|h5|store_promo", StatusUnknown,
			"https://open.bilibili.com/"},
		{"kuaishou|native|store_promo", StatusUnknown,
			"https://open.kuaishou.com/platformDocs/develop/mobile-app/android.html"},
	}
	if len(seed.Records) != len(want) {
		t.Fatalf("seed has %d records, want %d", len(seed.Records), len(want))
	}
	byKey := map[string]Record{}
	for _, r := range seed.Records {
		byKey[r.IdentityKey()] = r
	}
	for _, w := range want {
		r, ok := byKey[w.key]
		if !ok {
			t.Fatalf("seed missing record %s", w.key)
		}
		if r.Status != w.status {
			t.Errorf("%s status = %q, want %q", w.key, r.Status, w.status)
		}
		if len(r.OfficialSources) == 0 || r.OfficialSources[0] != w.source {
			t.Errorf("%s official source = %v, want first %q", w.key, r.OfficialSources, w.source)
		}
		if r.LastCheckedAt != "2026-10-14" {
			t.Errorf("%s last_checked_at = %q, want 2026-10-14 (ticket research date)", w.key, r.LastCheckedAt)
		}
		if r.Status != StatusAdmitted && r.RewardAdmitted {
			t.Errorf("%s: reward_admitted must be false unless status=admitted (unknown never enables reward play)", w.key)
		}
		for _, capName := range []string{"media", "title", "body", "topic", "poi", "receipt", "callback"} {
			if s := r.Capabilities.Field(capName); s != CapUnknown && s != CapSupported && s != CapUnsupported {
				t.Errorf("%s capability %s = %q, want one of supported/unsupported/unknown", w.key, capName, s)
			}
		}
		if r.TenantScope == "" {
			t.Errorf("%s tenant_scope must be set (* for platform-level truth)", w.key)
		}
	}
}

// TestSeedVendoredCopyMatchesContract pins the vendored copy to the versioned
// contract file byte for byte (HUI-2227 vendored-truth pattern: the contract
// file in docs/ is the truth, the package copy must never drift).
func TestSeedVendoredCopyMatchesContract(t *testing.T) {
	vendored, err := seedFS.ReadFile(seedEmbedPath)
	if err != nil {
		t.Fatalf("read embedded seed copy: %v", err)
	}
	contract, err := os.ReadFile(contractSeedPath)
	if err != nil {
		t.Fatalf("read contract seed: %v", err)
	}
	if !bytes.Equal(vendored, contract) {
		t.Fatal("vendored seed copy drifted from docs/contracts/channel-admission/v1/seed.json; re-sync the copy")
	}
}

// TestSeedDatesParseable keeps the audit dates machine-checkable: every record
// carries a parseable check date and a valid_until in the future of the check
// date (the ticket demands 登记实际读取日期 + 有效期).
func TestSeedDatesParseable(t *testing.T) {
	seed, err := EmbeddedSeed()
	if err != nil {
		t.Fatalf("parse embedded seed: %v", err)
	}
	for _, r := range seed.Records {
		if _, err := time.Parse("2006-01-02", r.LastCheckedAt); err != nil {
			t.Errorf("%s last_checked_at %q not parseable: %v", r.IdentityKey(), r.LastCheckedAt, err)
		}
		if r.ValidUntil == "" {
			t.Errorf("%s valid_until empty: an admission fact without a re-check horizon is not a record", r.IdentityKey())
			continue
		}
		vu, err := time.Parse("2006-01-02", r.ValidUntil)
		if err != nil {
			t.Errorf("%s valid_until %q not parseable: %v", r.IdentityKey(), r.ValidUntil, err)
			continue
		}
		checked, _ := time.Parse("2006-01-02", r.LastCheckedAt)
		if !vu.After(checked) {
			t.Errorf("%s valid_until %s must be after last_checked_at %s", r.IdentityKey(), r.ValidUntil, r.LastCheckedAt)
		}
	}
}

// TestRecordValidateRejectsJunk keeps the type layer honest: only the three
// admission statuses and three capability states parse; unknown status never
// smuggles in as allowed anywhere in the pipeline.
func TestRecordValidateRejectsJunk(t *testing.T) {
	var raw struct {
		Records []Record `json:"records"`
	}
	bad := `{"records":[{"platform":"douyin","entry_mode":"h5","scenario":"store_promo",
		"app_binding":"none","tenant_scope":"*","status":"allowed","last_checked_at":"2026-10-14","valid_until":"2027-01-12"}]}`
	if err := json.Unmarshal([]byte(bad), &raw); err == nil {
		t.Fatal("status 'allowed' must be rejected by the type layer: it does not exist; unknown never equals allowed")
	}
}
