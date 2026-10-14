package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// HUI-1671 B3 migration contracts (0023/0024/0025, additive SQLite).

func TestRewardMigrationsCreateTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "touch.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	names, err := db.TableNames(d)
	if err != nil {
		t.Fatalf("table names: %v", err)
	}
	want := map[string]bool{
		"reward_inventory": false, "reward_code_pool": false,
		"reward_benefits": false, "reward_operations": false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Fatalf("table %s missing after migrations", n)
		}
	}
}

// seedRewardWorld builds two tenants, two campaigns, and two active store
// coupon batches (inv-1 style ids are returned, not assumed).
func seedRewardWorld(t *testing.T) (*Store, string, string, string, string, string, string) {
	t.Helper()
	s := openStore(t)
	tenA, err := s.CreateTenant("奖励商家A")
	must(t, err)
	tenB, err := s.CreateTenant("奖励商家B")
	must(t, err)
	campA, err := s.CreateCampaign(NewCampaign{TenantID: tenA.ID, Title: "碰一碰活动A", CreatedBy: "seed"})
	must(t, err)
	campB, err := s.CreateCampaign(NewCampaign{TenantID: tenB.ID, Title: "碰一碰活动B", CreatedBy: "seed"})
	must(t, err)
	invA, err := s.CreateRewardInventory(tenA.ID, NewRewardInventory{
		CampaignID: campA.ID, Kind: RewardKindStoreCoupon, Name: "开业券",
		BenefitSlot: "first_publish", RuleVersion: 3,
		AllowedEvidence: []string{"staff_observed_submission", "staff_verified_post"},
		RuleRef:         "rules://campA/v3", TotalQty: 2,
		FaceValueMinor: 500, CostMinor: 300, Currency: "FEN",
		CreatedBy: "seed",
	})
	must(t, err)
	invB, err := s.CreateRewardInventory(tenB.ID, NewRewardInventory{
		CampaignID: campB.ID, Kind: RewardKindStoreCoupon, Name: "B店券",
		BenefitSlot: "first_publish", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"},
		TotalQty:        5, FaceValueMinor: 100, CostMinor: 50, Currency: "FEN",
		CreatedBy: "seed",
	})
	must(t, err)
	return s, tenA.ID, campA.ID, invA.ID, tenB.ID, campB.ID, invB.ID
}

func TestRewardBenefitsUniqueTriple(t *testing.T) {
	s, tenA, campA, invA, tenB, campB, invB := seedRewardWorld(t)
	ts := now()
	must(t, s.insertRawBenefit("ben-1", tenA, campA, invA, "part-1", "first_publish", "staff_observed_submission", "SC-abc", "reserved", ts))
	// Same tenant+participation+slot is refused by the database itself.
	if err := s.insertRawBenefit("ben-2", tenA, campA, invA, "part-1", "first_publish", "staff_verified_post", "SC-def", "reserved", ts); err == nil {
		t.Fatal("duplicate tenant+participation+slot must violate the unique constraint")
	}
	// A different slot for the same participation is a distinct reward.
	must(t, s.insertRawBenefit("ben-3", tenA, campA, invA, "part-1", "second_slot", "staff_observed_submission", "", "reserved", ts))
	// Same participation+slot in another tenant is a distinct reward.
	must(t, s.insertRawBenefit("ben-4", tenB, campB, invB, "part-1", "first_publish", "staff_observed_submission", "", "reserved", ts))
}

func TestRewardOperationsDedupAndAppendOnly(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	ts := now()
	must(t, s.insertRawBenefit("ben-1", tenA, campA, invA, "part-1", "first_publish", "staff_observed_submission", "SC-abc", "reserved", ts))
	op := `INSERT INTO reward_operations(id,tenant_id,benefit_id,op,dedup_key,actor,store_id,reason,payload,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`
	must(t, withExec(s, op, "op-1", tenA, "ben-1", "issue", "evt-1", "staff-9", "store-1", "", "{}", ts))
	// Same event key replayed is refused by the partial unique index.
	if err := withExec(s, op, "op-2", tenA, "ben-1", "issue", "evt-1", "staff-9", "store-1", "", "{}", ts); err == nil {
		t.Fatal("duplicate dedup_key must violate the unique index")
	}
	// Bookkeeping rows without a dedup key (fulfil-now second posting,
	// system expiry) never collide.
	must(t, withExec(s, op, "op-3", tenA, "ben-1", "redeem", "", "staff-9", "store-1", "", "{}", ts))
	must(t, withExec(s, op, "op-4", tenA, "ben-1", "redeem", "", "staff-8", "store-1", "", "{}", ts))
	// The same event key inside one tenant is consumed once — even when a
	// second benefit row exists (FK needs a benefit of its own).
	must(t, s.insertRawBenefit("ben-2", tenA, campA, invA, "part-2", "first_publish", "staff_observed_submission", "", "reserved", ts))
	if err := withExec(s, op, "op-5", tenA, "ben-2", "issue", "evt-1", "staff-9", "store-1", "", "{}", ts); err == nil {
		t.Fatal("same dedup key twice in one tenant must be refused regardless of benefit")
	}
}

func TestRewardCheckConstraints(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	ts := now()
	if err := s.insertRawBenefit("ben-bad", tenA, campA, invA, "p-9", "first_publish", "staff_observed_submission", "", "shipped", ts); err == nil {
		t.Fatal("benefit status shipped must be refused")
	}
	if err := s.insertRawBenefit("ben-bad2", tenA, campA, invA, "p-9", "first_publish", "clicked_a_button", "", "reserved", ts); err == nil {
		t.Fatal("evidence clicked_a_button must be refused")
	}
	must(t, s.insertRawBenefit("ben-1", tenA, campA, invA, "part-c", "first_publish", "staff_observed_submission", "", "reserved", ts))
	op := `INSERT INTO reward_operations(id,tenant_id,benefit_id,op,dedup_key,actor,store_id,reason,payload,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`
	if err := withExec(s, op, "op-bad", tenA, "ben-1", "refund", "", "x", "store-1", "", "{}", ts); err == nil {
		t.Fatal("operation refund must be refused")
	}
}

func TestRewardCodePoolUnique(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	inv, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindImportedCode, Name: "导入券池",
		BenefitSlot: "first_publish", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"},
		Codes:           []string{"CODE-001", "CODE-002"},
		FaceValueMinor:  100, CostMinor: 50, Currency: "FEN", CreatedBy: "seed",
	})
	must(t, err)
	pool := `INSERT INTO reward_code_pool(id,tenant_id,inventory_id,code,status,created_at) VALUES(?,?,?,?,?,?)`
	if err := withExec(s, pool, "pc-x", tenA, inv.ID, "CODE-001", "available", now()); err == nil {
		t.Fatal("duplicate code in one batch must be refused")
	}
}

func TestRewardTablesForeignKeys(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	ts := now()
	err := s.insertRawBenefit("ben-fk", tenA, campA, "inv-missing", "p-1", "first_publish", "staff_observed_submission", "", "reserved", ts)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("missing inventory must fail the FK, got %v", err)
	}
}

func withExec(s *Store, q string, args ...any) error {
	_, err := s.DB.Exec(q, args...)
	return err
}

// insertRawBenefit writes a benefit row with raw SQL for schema-contract
// tests. Production writes go through the ledger methods in Task 5/6.
func (s *Store) insertRawBenefit(id, tenantID, campaignID, inventoryID, participationID, slot, evidence, code, status, ts string) error {
	_, err := s.DB.Exec(`INSERT INTO reward_benefits(id,tenant_id,campaign_id,inventory_id,participation_id,benefit_slot,rule_version,evidence,code,status,store_id,face_value_minor,cost_minor,currency,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, tenantID, campaignID, inventoryID, participationID, slot, 1, evidence, code, status, "store-1", 100, 50, "FEN", ts, ts)
	return err
}

func TestCreateRewardInventoryValidation(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	if _, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: "cash", Name: "x", BenefitSlot: "s", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"}, TotalQty: 1, CreatedBy: "a",
	}); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("unknown kind must be invalid, got %v", err)
	}
	if _, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindStoreCoupon, Name: "x", BenefitSlot: "s", RuleVersion: 1,
		AllowedEvidence: []string{"click"}, TotalQty: 1, CreatedBy: "a",
	}); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("unknown evidence must be invalid, got %v", err)
	}
	if _, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: "camp-other", Kind: RewardKindStoreCoupon, Name: "x", BenefitSlot: "s", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"}, TotalQty: 1, CreatedBy: "a",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant campaign must be not found, got %v", err)
	}
	if _, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindStoreCoupon, Name: "x", BenefitSlot: "s", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"}, TotalQty: 1, StoreID: "sto-other", CreatedBy: "a",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign store must be not found, got %v", err)
	}
	if _, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindImportedCode, Name: "x", BenefitSlot: "s", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"}, Codes: []string{"A", "A"}, CreatedBy: "a",
	}); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("duplicate codes must be invalid, got %v", err)
	}
}

func TestListRewardInventoryAvailability(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	ts := now()
	list, err := s.ListRewardInventory(tenA, campA)
	must(t, err)
	if len(list) != 1 || list[0].AvailableQty != 2 {
		t.Fatalf("fresh batch should be fully available: %+v", list)
	}
	must(t, s.insertRawBenefit("ben-occ", tenA, campA, invA, "p-1", "first_publish", "staff_observed_submission", "SC-1", "reserved", ts))
	must(t, s.insertRawBenefit("ben-occ2", tenA, campA, invA, "p-2", "first_publish", "staff_verified_post", "SC-2", "issued", ts))
	must(t, s.insertRawBenefit("ben-free", tenA, campA, invA, "p-3", "first_publish", "staff_observed_submission", "SC-3", "expired", ts))
	must(t, s.insertRawBenefit("ben-free2", tenA, campA, invA, "p-4", "first_publish", "staff_observed_submission", "SC-4", "reversed", ts))
	list, err = s.ListRewardInventory(tenA, campA)
	must(t, err)
	// Two occupy; expired and reversed free their allocation.
	if list[0].AvailableQty != 0 {
		t.Fatalf("availability should count reserved+issued+redeemed only: %+v", list[0])
	}
}

func TestSetRewardInventoryStatus(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	got, err := s.SetRewardInventoryStatus(invA, tenA, RewardInventoryPaused)
	must(t, err)
	if got.Status != RewardInventoryPaused {
		t.Fatalf("pause failed: %+v", got)
	}
	if _, err := s.SetRewardInventoryStatus(invA, tenA, "gone"); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("unknown status must be invalid, got %v", err)
	}
	if _, err := s.SetRewardInventoryStatus(invA, "ten-other", RewardInventoryActive); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant status change must be not found, got %v", err)
	}
	must(t, func() error { _, err := s.SetRewardInventoryStatus(invA, tenA, RewardInventoryRetired); return err }())
	retired, err := s.SetRewardInventoryStatus(invA, tenA, RewardInventoryActive)
	must(t, err)
	if retired.Status != RewardInventoryRetired {
		t.Fatal("retired is terminal")
	}
	list, err := s.ListRewardInventory(tenA, campA)
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("retired batches leave the list: %+v", list)
	}
}

func TestRewardCodePoolAllocation(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	inv, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindImportedCode, Name: "导入券池",
		BenefitSlot: "first_publish", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"},
		Codes:           []string{"C-1", "C-2", "C-3"},
		FaceValueMinor:  100, CostMinor: 40, Currency: "FEN", CreatedBy: "seed",
	})
	must(t, err)
	if inv.TotalQty != 3 || inv.AvailableQty != 3 {
		t.Fatalf("pool batch quantity wrong: %+v", inv)
	}
	// Pop codes in order; pool drains.
	for i, want := range []string{"C-1", "C-2", "C-3"} {
		got, err := s.popRewardCode(inv.ID)
		must(t, err)
		if got != want {
			t.Fatalf("pop %d = %q want %q", i, got, want)
		}
	}
	if _, err := s.popRewardCode(inv.ID); !errors.Is(err, ErrRewardStock) {
		t.Fatalf("drained pool must answer out of stock, got %v", err)
	}
}
