package publishhandoff

// service_test.go 的夹具助手(仅测试构建)。

import (
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// eventKinds returns (kinds, sources) of one handoff's evidence events in
// chronological order.
func (s *Service) eventKinds(t *testing.T, handoffID string) (kinds, sources []string) {
	t.Helper()
	rows, err := s.db.Query(`SELECT kind, evidence_source FROM publish_handoff_events
		WHERE handoff_id=? ORDER BY created_at, id`, handoffID)
	if err != nil {
		t.Fatalf("event kinds: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, src string
		if err := rows.Scan(&k, &src); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
		sources = append(sources, src)
	}
	return kinds, sources
}

// findByAttempt resolves the handoff id owning an attempt (negative-path
// helper).
func findByAttempt(t *testing.T, s *Service, attemptID string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM publish_handoffs WHERE attempt_id=?`, attemptID).Scan(&id); err != nil {
		t.Fatalf("find by attempt: %v", err)
	}
	return id
}

// startReadyHandoff seeds a frozen attempt and drives Start to handoff_ready
// with the given admission record. Visitor id is "visit_<suffix>".
func startReadyHandoff(t *testing.T, s *Service, suffix string, rec channeladmission.Record) Handoff {
	t.Helper()
	ptID, atID, visitor := seedFrozenAttempt(t, s, suffix)
	res, err := s.Start(t.Context(), startInput(ptID, atID, visitor), rec)
	if err != nil {
		t.Fatalf("startReadyHandoff: %v", err)
	}
	return res.Handoff
}

// fixtureReceiptFor builds the canonical admitted-app receipt for a handoff.
func fixtureReceiptFor(h Handoff) Receipt {
	return Receipt{
		ReceiptID: "rcp_" + h.ID, Platform: h.Platform, AppRef: "app_touch_prod",
		TenantID: h.TenantID, ParticipationID: h.ParticipationID, AttemptID: h.AttemptID,
		ContentVersion: h.ContentVersion, PostID: "post_" + h.ID, Outcome: ReceiptOutcomePublished,
	}
}

// receiptWith returns a fixture receipt with a mutated field (negative paths).
func receiptWith(h Handoff, mut func(*Receipt)) Receipt {
	r := fixtureReceiptFor(h)
	mut(&r)
	return r
}

// signWith signs a receipt with the given fixture secret.
func signWith(t *testing.T, secret string, r Receipt) string {
	t.Helper()
	sig, err := SignReceiptForTest(secret, r)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

var _ = strings.Contains // keep import honest when helpers shrink

// newServiceAtPath opens (or reopens) a database file at path with the same
// fixture wiring (restart acceptance). Safe to call twice on one path: the
// reference seed rows are INSERT OR IGNORE.
func newServiceAtPath(t *testing.T, path string) *Service {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	tenantID, storeID, campaignID := "tnt_seed", "sto_seed", "cmp_seed"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO tenants (id, name, created_at) VALUES (?,?,?)`, []any{tenantID, "seed-tenant", "2026-10-14T00:00:00Z"}},
		{`INSERT OR IGNORE INTO stores (id, tenant_id, name, created_at, updated_at) VALUES (?,?,?,?,?)`,
			[]any{storeID, tenantID, "seed-store", "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}},
		{`INSERT OR IGNORE INTO campaigns (id, tenant_id, title, status, store_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			[]any{campaignID, tenantID, "seed-campaign", "active", storeID, "2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z"}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return New(d, Options{
		Now:             func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
		MediaSecret:     testMediaSecret,
		MediaBaseURL:    "https://media.example.com/handoff",
		ReceiptVerifier: NewHMACVerifier("receipt-secret"),
		AppRef:          "app_touch_prod",
	})
}
