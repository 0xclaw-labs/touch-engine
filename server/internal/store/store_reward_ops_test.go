package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
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

// ---- ledger state machine (Tasks 5-7) --------------------------------------

func reserveInput(campA, invID string) RewardReserveInput {
	return RewardReserveInput{
		TenantID: "tenantA", CampaignID: campA, ParticipationID: "part-1",
		Slot: "first_publish", RuleVersion: 3, EventID: "res-evt-1",
		Actor: "participation-service", TTLSeconds: 3600,
	}
}

func TestReserveRewardBenefitBasic(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	in := reserveInput(campA, invA)
	in.TenantID = tenA
	ben, out, err := s.ReserveRewardBenefit(in, "2026-10-14T10:00:00Z")
	must(t, err)
	if ben.Status != RewardStatusReserved || out.Outcome != "reserved" {
		t.Fatalf("unexpected reserve: %+v %+v", ben, out)
	}
	if ben.Code == "" {
		t.Fatal("store coupon must bind a code at reserve time")
	}
	if ben.ReservedUntil == "" || ben.ReservedAt != "2026-10-14T10:00:00Z" {
		t.Fatalf("TTL not recorded: %+v", ben)
	}
	// Resume (a distinct engagement event) refreshes the same row and does
	// not occupy twice.
	resume := in
	resume.EventID = "res-evt-1-resume"
	ben2, out2, err := s.ReserveRewardBenefit(resume, "2026-10-14T10:30:00Z")
	must(t, err)
	if ben2.ID != ben.ID || out2.Outcome != "re_reserved" {
		t.Fatalf("resume must reuse the row: %+v %+v", ben2, out2)
	}
	if ben2.ReservedUntil == ben.ReservedUntil {
		t.Fatalf("resume must refresh the TTL: %s", ben2.ReservedUntil)
	}
	list, err := s.ListRewardBenefits(tenA, campA, "")
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("resume must not add rows: %d", len(list))
	}
	// Event replay answers from the ledger without change.
	ben3, out3, err := s.ReserveRewardBenefit(in, "2026-10-14T11:00:00Z")
	must(t, err)
	if !out3.Replay || out3.Applied || ben3.ID != ben.ID {
		t.Fatalf("replay must answer unchanged: %+v %+v", out3, ben3)
	}
}

func TestReserveLastStockRace(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	base := reserveInput(campA, invA)
	base.TenantID = tenA
	// invA has total 2: take both, third must be refused.
	base.ParticipationID = "p-1"
	_, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z")
	must(t, err)
	base.ParticipationID = "p-2"
	base.EventID = "res-evt-2"
	_, _, err = s.ReserveRewardBenefit(base, "2026-10-14T10:00:01Z")
	must(t, err)
	base.ParticipationID = "p-3"
	base.EventID = "res-evt-3"
	if _, _, err = s.ReserveRewardBenefit(base, "2026-10-14T10:00:02Z"); !errors.Is(err, ErrRewardStock) {
		t.Fatalf("third reservation must hit stock limit, got %v", err)
	}
}

func TestReserveExpiryReleasesStock(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	base := reserveInput(campA, invA)
	base.TenantID = tenA
	// invA total is 2. p-1 reserves at 10:00 for 1h.
	_, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z")
	must(t, err)
	base.ParticipationID, base.EventID = "p-2", "res-evt-2"
	ben2, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:01Z")
	must(t, err)
	// At 12:00 both reservations are due; the sweep frees them.
	n, err := s.ExpireDueRewardReservations("2026-10-14T12:00:00Z", tenA)
	must(t, err)
	if n != 2 {
		t.Fatalf("expected 2 expired, got %d", n)
	}
	// Stock is free again: p-3 can reserve.
	base.ParticipationID, base.EventID = "p-3", "res-evt-3"
	_, out, err := s.ReserveRewardBenefit(base, "2026-10-14T12:01:00Z")
	must(t, err)
	if out.Outcome != "reserved" {
		t.Fatalf("freed stock must be reservable: %+v", out)
	}
	// p-1 returns and re-reserves: same row comes back, still one benefit.
	base.ParticipationID, base.EventID = "part-1", "res-evt-1b"
	_, out, err = s.ReserveRewardBenefit(base, "2026-10-14T12:02:00Z")
	must(t, err)
	if out.Outcome != "re_reserved" {
		t.Fatalf("expired re-reserve outcome: %+v", out)
	}
	list, err := s.ListRewardBenefits(tenA, campA, "")
	must(t, err)
	reserved := 0
	for _, b := range list {
		if b.ParticipationID == "part-1" {
			reserved++
		}
	}
	if reserved != 1 || len(list) != 3 {
		t.Fatalf("re-reserve must reuse the same row: %+v", list)
	}
	_ = ben2
}

func TestReserveRefusals(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	base := reserveInput(campA, invA)
	base.TenantID = tenA
	base.TTLSeconds = 0
	if _, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z"); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("TTL must be explicit, got %v", err)
	}
	base = reserveInput(campA, invA)
	base.TenantID = tenA
	base.RuleVersion = 9
	if _, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z"); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("unknown rule version must be invalid, got %v", err)
	}
	base = reserveInput(campA, invA)
	base.TenantID = "ten-other"
	if _, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant reserve must be not found, got %v", err)
	}
}

func confirmInput(tenA, campA, participation string, evidence string) RewardConfirmInput {
	return RewardConfirmInput{
		TenantID: tenA, CampaignID: campA, ParticipationID: participation,
		Slot: "first_publish", RuleVersion: 3, EventID: "cfm-" + participation,
		Evidence: evidence, Actor: "staff-9", StoreID: "store-1", Now: "2026-10-14T12:00:00Z",
	}
}

func TestConfirmIssuesRealBenefit(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	ben, out, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-1", "staff_observed_submission"))
	must(t, err)
	if ben.Status != RewardStatusIssued || out.Outcome != "issued" || ben.Code == "" {
		t.Fatalf("confirm should issue: %+v %+v", ben, out)
	}
	if ben.FaceValueMinor != 500 || ben.CostMinor != 300 {
		t.Fatalf("face and cost must be copied separately: %+v", ben)
	}
	// Confirm-and-fulfil posts two facts in one transaction.
	in := confirmInput(tenA, campA, "p-2", "staff_verified_post")
	in.FulfillNow = true
	in.RedeemEventID = "rdm-p-2"
	ben2, out2, err := s.ConfirmRewardBenefit(in)
	must(t, err)
	if ben2.Status != RewardStatusRedeemed || out2.Outcome != "issued_and_redeemed" {
		t.Fatalf("fulfil now should redeem in place: %+v %+v", ben2, out2)
	}
	ops, err := s.listRewardOperations(tenA, ben2.ID)
	must(t, err)
	kinds := map[string]int{}
	for _, op := range ops {
		kinds[op.Op]++
	}
	if kinds["issue"] != 1 || kinds["redeem"] != 1 {
		t.Fatalf("issue and redeem must both be posted: %+v", kinds)
	}
	// Replay of the same confirmation event is a no-op answer.
	_, out3, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-1", "staff_observed_submission"))
	must(t, err)
	if !out3.Replay || out3.Applied || out3.Outcome != "issued" {
		t.Fatalf("replay must answer the first result unchanged: %+v", out3)
	}
	// A different event on the same slot reads back the existing benefit.
	in2 := confirmInput(tenA, campA, "p-1", "staff_verified_post")
	in2.EventID = "cfm-other-staff"
	ben4, out4, err := s.ConfirmRewardBenefit(in2)
	must(t, err)
	if ben4.ID != ben.ID || out4.Outcome != "already_issued" || out4.Applied {
		t.Fatalf("double evidence must read back: %+v %+v", ben4, out4)
	}
	// The evidence type of the granted benefit never changes.
	if ben4.Evidence != "staff_observed_submission" {
		t.Fatalf("evidence must stay the first granted class: %+v", ben4)
	}
}

func TestConfirmWithoutReserveAlsoWorks(t *testing.T) {
	// 店员直接现场确认（顾客没有提前预约）也是一条合法路径。
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	ben, out, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "walkup-1", "staff_verified_post"))
	must(t, err)
	if ben.Status != RewardStatusIssued || out.Applied != true {
		t.Fatalf("walk-up confirm should issue: %+v %+v", ben, out)
	}
}

func TestConfirmNegativeCases(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	// Unknown rule version.
	in := confirmInput(tenA, campA, "p-x", "staff_observed_submission")
	in.RuleVersion = 9
	if _, _, err := s.ConfirmRewardBenefit(in); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("unknown version: %v", err)
	}
	// Evidence the snapshot does not allow (invA allows observed+verified).
	in = confirmInput(tenA, campA, "p-y", "official_publish_confirmed")
	in.Official = rewardops.OfficialRefForAttempt("a-1", "post-1")
	if _, _, err := s.ConfirmRewardBenefit(in); err == nil || !strings.Contains(err.Error(), "evidence_not_allowed") {
		t.Fatalf("official evidence on staff rule: %v", err)
	}
	// Missing note/store on confirm input.
	in = confirmInput(tenA, campA, "p-z", "staff_observed_submission")
	in.StoreID = ""
	if _, _, err := s.ConfirmRewardBenefit(in); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("missing store must be invalid: %v", err)
	}
}

func TestConfirmOfficialOnlyAndForgedReceipts(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	officialOnly, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindGiftDiscount, Name: "官方礼",
		BenefitSlot: "official_gift", RuleVersion: 3,
		AllowedEvidence: []string{"official_publish_confirmed"},
		TotalQty:        5, FaceValueMinor: 0, CostMinor: 200, Currency: "FEN", CreatedBy: "seed",
	})
	must(t, err)
	// Staff evidence cannot satisfy an official-only rule.
	in := confirmInput(tenA, campA, "p-1", "staff_observed_submission")
	in.Slot = "official_gift"
	if _, _, err := s.ConfirmRewardBenefit(in); err == nil || !strings.Contains(err.Error(), "official_evidence_required") {
		t.Fatalf("official-only must refuse staff evidence: %v", err)
	}
	// A self-typed receipt without a server-side confirmed attempt is forged.
	in = confirmInput(tenA, campA, "p-1", "official_publish_confirmed")
	in.Slot = "official_gift"
	in.Official = rewardops.OfficialRef{FactID: "attempt-nope", PlatformPostID: "999", ReceiptSource: "official_query"}
	if _, _, err := s.ConfirmRewardBenefit(in); err == nil || !strings.Contains(err.Error(), "fabricated_official_receipt") {
		t.Fatalf("forged receipt must be refused: %v", err)
	}
	// Self-reporting evidence class is unknown to the adjudicator.
	in2 := confirmInput(tenA, campA, "p-2", "self_report")
	if _, _, err := s.ConfirmRewardBenefit(in2); err == nil || !strings.Contains(err.Error(), "unknown_evidence") {
		t.Fatalf("self report class: %v", err)
	}
	_ = officialOnly
}

func TestConfirmOfficialAnchoredToServerFact(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	officialOnly, err := s.CreateRewardInventory(tenA, NewRewardInventory{
		CampaignID: campA, Kind: RewardKindGiftDiscount, Name: "官方礼",
		BenefitSlot: "official_gift", RuleVersion: 3,
		AllowedEvidence: []string{"official_publish_confirmed"},
		TotalQty:        5, CostMinor: 200, Currency: "FEN", CreatedBy: "seed",
	})
	must(t, err)
	// A real publish attempt row (0010 keeps its receipt fields empty).
	must(t, withExec(s, `INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,asset_use_accepted,confirmed_content_version,confirmed_account_label,self_reported,platform_post_id,receipt_source,outbound_calls,query_count,reward_triggered,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"att-real", tenA, campA, "code-1", "douyin", "activity_customer", "copy", "v1", "label",
		"unknown", 0, "", "", 0, "", "", 0, 1, 0, now(), now()))
	// Without an official fact the claim is fabricated.
	in := confirmInput(tenA, campA, "p-official", "official_publish_confirmed")
	in.Slot = "official_gift"
	in.Official = rewardops.OfficialRefForAttempt("att-real", "post-77")
	if _, _, err := s.ConfirmRewardBenefit(in); err == nil || !strings.Contains(err.Error(), "fabricated_official_receipt") {
		t.Fatalf("attempt without official fact must refuse: %v", err)
	}
	// Engineering fixture: the trusted official-query integration posts the
	// fact (today no such integration exists, so this stays test-only).
	must(t, withExec(s, `INSERT INTO reward_official_facts(id,tenant_id,campaign_id,attempt_id,platform,platform_post_id,receipt_source,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		"rof-1", tenA, campA, "att-real", "douyin", "post-77", "official_query", "official-query-job", now()))
	in = confirmInput(tenA, campA, "p-official", "official_publish_confirmed")
	in.Slot = "official_gift"
	in.Official = rewardops.OfficialRefForAttempt("att-real", "post-77")
	ben, out, err := s.ConfirmRewardBenefit(in)
	must(t, err)
	if ben.Status != RewardStatusIssued || ben.Evidence != "official_publish_confirmed" {
		t.Fatalf("anchored official should grant: %+v %+v", ben, out)
	}
	// The same attempt cannot fund a second participation.
	in2 := confirmInput(tenA, campA, "p-official-2", "official_publish_confirmed")
	in2.Slot = "official_gift"
	in2.EventID = "cfm-second"
	in2.Official = rewardops.OfficialRefForAttempt("att-real", "post-77")
	if _, _, err := s.ConfirmRewardBenefit(in2); err == nil || !strings.Contains(err.Error(), "official_fact_consumed") {
		t.Fatalf("one official fact one grant: %v", err)
	}
	// A post id mismatch is a fabricated receipt.
	must(t, withExec(s, `INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,asset_use_accepted,confirmed_content_version,confirmed_account_label,self_reported,platform_post_id,receipt_source,outbound_calls,query_count,reward_triggered,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"att-real-2", tenA, campA, "code-2", "douyin", "activity_customer", "copy", "v1", "label",
		"unknown", 0, "", "", 0, "", "", 0, 1, 0, now(), now()))
	must(t, withExec(s, `INSERT INTO reward_official_facts(id,tenant_id,campaign_id,attempt_id,platform,platform_post_id,receipt_source,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		"rof-2", tenA, campA, "att-real-2", "douyin", "post-88", "official_query", "official-query-job", now()))
	in3 := confirmInput(tenA, campA, "p-official-3", "official_publish_confirmed")
	in3.Slot = "official_gift"
	in3.EventID = "cfm-third"
	in3.Official = rewardops.OfficialRefForAttempt("att-real-2", "post-WRONG")
	if _, _, err := s.ConfirmRewardBenefit(in3); err == nil || !strings.Contains(err.Error(), "fabricated_official_receipt") {
		t.Fatalf("post id mismatch must refuse: %v", err)
	}
	// An attempt of another campaign (real second campaign of the same
	// tenant) cannot anchor this campaign.
	campB, err := s.CreateCampaign(NewCampaign{TenantID: tenA, Title: "另一活动", CreatedBy: "seed"})
	must(t, err)
	must(t, withExec(s, `INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,asset_use_accepted,confirmed_content_version,confirmed_account_label,self_reported,platform_post_id,receipt_source,outbound_calls,query_count,reward_triggered,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"att-other-camp", tenA, campB.ID, "code-3", "douyin", "activity_customer", "copy", "v1", "label",
		"unknown", 0, "", "", 0, "", "", 0, 1, 0, now(), now()))
	if _, err := s.DB.Exec(`INSERT INTO reward_official_facts(id,tenant_id,campaign_id,attempt_id,platform,platform_post_id,receipt_source,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		"rof-3", tenA, campB.ID, "att-other-camp", "douyin", "post-99", "official_query", "official-query-job", now()); err != nil {
		t.Fatalf("other campaign fixture: %v", err)
	}
	in4 := confirmInput(tenA, campA, "p-official-4", "official_publish_confirmed")
	in4.Slot = "official_gift"
	in4.EventID = "cfm-fourth"
	in4.Official = rewardops.OfficialRefForAttempt("att-other-camp", "post-99")
	if _, _, err := s.ConfirmRewardBenefit(in4); err == nil || !strings.Contains(err.Error(), "fabricated_official_receipt") {
		t.Fatalf("cross-campaign attempt must not anchor: %v", err)
	}
	_ = officialOnly
}

func TestConfirmOnReservedAndExpiredBenefits(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	base := reserveInput(campA, invA)
	base.TenantID = tenA
	must(t, func() error { _, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z"); return err }())
	// Confirming a live reservation promotes it (same row, same code).
	ben, out, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "part-1", "staff_observed_submission"))
	must(t, err)
	if out.Outcome != "issued" || ben.Status != RewardStatusIssued {
		t.Fatalf("reserved should promote: %+v %+v", ben, out)
	}
	// An expired reservation re-confirms after a stock re-check.
	base2 := reserveInput(campA, invA)
	base2.TenantID = tenA
	base2.ParticipationID, base2.EventID = "p-late", "res-late"
	must(t, func() error { _, _, err := s.ReserveRewardBenefit(base2, "2026-10-14T10:00:00Z"); return err }())
	must(t, func() error { _, err := s.ExpireDueRewardReservations("2026-10-14T12:00:00Z", tenA); return err }())
	in := confirmInput(tenA, campA, "p-late", "staff_verified_post")
	ben2, out2, err := s.ConfirmRewardBenefit(in)
	must(t, err)
	if out2.Outcome != "issued" {
		t.Fatalf("expired reservation re-issues: %+v %+v", ben2, out2)
	}
	// A reversed benefit can never issue again.
	must(t, func() error {
		_, _, err := s.ReverseRewardBenefit(tenA, campA, ben2.ID, "admin-1", "误审", "rvs-1", "2026-10-14T13:00:00Z")
		return err
	}())
	in3 := confirmInput(tenA, campA, "p-late", "staff_verified_post")
	in3.EventID = "cfm-after-reverse"
	if _, _, err := s.ConfirmRewardBenefit(in3); err == nil || !strings.Contains(err.Error(), "slot_reversed") {
		t.Fatalf("reversed slot must refuse: %v", err)
	}
}

func TestRedeemTwoStaffRace(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	must(t, func() error {
		_, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-1", "staff_observed_submission"))
		return err
	}())
	_, err := s.ListRewardBenefits(tenA, campA, "")
	must(t, err)
	list, _ := s.ListRewardBenefits(tenA, campA, "")
	benID := list[0].ID
	// Staff A redeems.
	gotA, outA, err := s.RedeemRewardBenefit(tenA, campA, benID, "staff-a", "store-1", "rdm-a", "2026-10-14T12:05:00Z")
	must(t, err)
	if outA.Outcome != "redeemed" || gotA.Status != RewardStatusRedeemed {
		t.Fatalf("first redeem must win: %+v %+v", gotA, outA)
	}
	// Staff B, a moment later, reads back the processed result.
	gotB, outB, err := s.RedeemRewardBenefit(tenA, campA, benID, "staff-b", "store-1", "rdm-b", "2026-10-14T12:05:01Z")
	must(t, err)
	if outB.Outcome != "already_redeemed" || outB.Applied {
		t.Fatalf("second redeem must read back: %+v %+v", gotB, outB)
	}
	// Same event replay is answered without a new posting.
	_, outC, err := s.RedeemRewardBenefit(tenA, campA, benID, "staff-a", "store-1", "rdm-a", "2026-10-14T12:05:02Z")
	must(t, err)
	if !outC.Replay || outC.Applied {
		t.Fatalf("replay must be marked: %+v", outC)
	}
	ops, err := s.listRewardOperations(tenA, benID)
	must(t, err)
	redeems := 0
	for _, op := range ops {
		if op.Op == "redeem" {
			redeems++
		}
	}
	if redeems != 1 {
		t.Fatalf("exactly one redeem posting expected, got %d", redeems)
	}
	// Reserved cannot be redeemed directly.
	must(t, func() error {
		_, _, err := s.ReserveRewardBenefit(RewardReserveInput{
			TenantID: tenA, CampaignID: campA, ParticipationID: "p-r", Slot: "first_publish",
			RuleVersion: 3, EventID: "res-r", Actor: "x", TTLSeconds: 600,
		}, "2026-10-14T12:06:00Z")
		return err
	}())
	list2, _ := s.ListRewardBenefits(tenA, campA, "")
	for _, b := range list2 {
		if b.ParticipationID == "p-r" {
			if _, _, err := s.RedeemRewardBenefit(tenA, campA, b.ID, "staff-a", "store-1", "rdm-r", "2026-10-14T12:07:00Z"); err == nil || !strings.Contains(err.Error(), "not_issued") {
				t.Fatalf("reserved redeem must refuse: %v", err)
			}
		}
	}
}

func TestReverseRewardBenefit(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	must(t, func() error {
		_, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-1", "staff_observed_submission"))
		return err
	}())
	list, _ := s.ListRewardBenefits(tenA, campA, "")
	benID := list[0].ID
	if _, _, err := s.ReverseRewardBenefit(tenA, campA, benID, "admin-1", "", "rvs-x", "2026-10-14T13:00:00Z"); !errors.Is(err, ErrRewardInvalid) {
		t.Fatalf("reverse requires a reason, got %v", err)
	}
	rev, out, err := s.ReverseRewardBenefit(tenA, campA, benID, "admin-1", "误审", "rvs-x", "2026-10-14T13:00:00Z")
	must(t, err)
	if rev.Status != RewardStatusReversed || out.Outcome != "reversed" {
		t.Fatalf("reverse failed: %+v %+v", rev, out)
	}
	// The original issuance fact survives (append-only trail).
	ops, err := s.listRewardOperations(tenA, benID)
	must(t, err)
	if len(ops) < 2 {
		t.Fatalf("issuance and correction must both survive: %d", len(ops))
	}
	_, out2, err := s.ReverseRewardBenefit(tenA, campA, benID, "admin-1", "again", "rvs-y", "2026-10-14T13:01:00Z")
	must(t, err)
	if out2.Outcome != "already_reversed" || out2.Applied {
		t.Fatalf("second reverse reads back: %+v", out2)
	}
	// Reversed stock is freed for others; the reversed slot itself is burnt.
	fresh, _, err := s.ConfirmRewardBenefit(confirmInput(tenA, campA, "p-new", "staff_verified_post"))
	must(t, err)
	if fresh.Status != RewardStatusIssued {
		t.Fatalf("freed stock must issue: %+v", fresh)
	}
}

func TestRewardLedgerRestartConsistency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "touch.db")
	build := func() (*Store, error) {
		d, err := db.Open(path)
		if err != nil {
			return nil, err
		}
		return New(d), nil
	}
	s, err := build()
	must(t, err)
	tenA, err := s.CreateTenant("重启商家")
	must(t, err)
	campA, err := s.CreateCampaign(NewCampaign{TenantID: tenA.ID, Title: "重启活动", CreatedBy: "seed"})
	must(t, err)
	invA, err := s.CreateRewardInventory(tenA.ID, NewRewardInventory{
		CampaignID: campA.ID, Kind: RewardKindStoreCoupon, Name: "重启券",
		BenefitSlot: "first_publish", RuleVersion: 1,
		AllowedEvidence: []string{"staff_observed_submission"}, TotalQty: 10,
		FaceValueMinor: 100, CostMinor: 30, Currency: "FEN", CreatedBy: "seed",
	})
	must(t, err)
	must(t, func() error {
		_, _, err := s.ReserveRewardBenefit(RewardReserveInput{
			TenantID: tenA.ID, CampaignID: campA.ID, ParticipationID: "p-1", Slot: "first_publish",
			RuleVersion: 1, EventID: "r1", Actor: "svc", TTLSeconds: 600,
		}, "2026-10-14T10:00:00Z")
		return err
	}())
	must(t, func() error {
		_, _, err := s.ConfirmRewardBenefit(RewardConfirmInput{
			TenantID: tenA.ID, CampaignID: campA.ID, ParticipationID: "p-2", Slot: "first_publish",
			RuleVersion: 1, EventID: "c1", Evidence: "staff_observed_submission",
			Actor: "staff-1", StoreID: "s1", Now: "2026-10-14T10:05:00Z",
		})
		return err
	}())
	s2, err := build()
	must(t, err)
	// Recompute from the append-only trail and compare with the row states.
	bens, err := s2.ListRewardBenefits(tenA.ID, campA.ID, "")
	must(t, err)
	issued := 0
	for _, b := range bens {
		ops, err := s2.listRewardOperations(tenA.ID, b.ID)
		must(t, err)
		lastIssue := false
		for _, op := range ops {
			if op.Op == "issue" {
				lastIssue = true
			}
			if op.Op == "reverse" || op.Op == "expire" {
				lastIssue = false
			}
		}
		if lastIssue {
			issued++
			if b.Status != RewardStatusIssued && b.Status != RewardStatusRedeemed {
				t.Fatalf("trail says active but row says %s", b.Status)
			}
		}
	}
	if issued != 1 {
		t.Fatalf("expected one live issuance after restart, got %d", issued)
	}
	inv, err := s2.GetRewardInventory(invA.ID, tenA.ID)
	must(t, err)
	if inv.AvailableQty != 8 {
		t.Fatalf("availability after restart: %+v", inv)
	}
}

func TestRewardSummary(t *testing.T) {
	s, tenA, campA, invA, _, _, _ := seedRewardWorld(t)
	base := reserveInput(campA, invA)
	base.TenantID = tenA
	must(t, func() error { _, _, err := s.ReserveRewardBenefit(base, "2026-10-14T10:00:00Z"); return err }())
	sum, err := s.RewardSummary(tenA, campA)
	must(t, err)
	if sum.Counts[RewardStatusReserved] != 1 {
		t.Fatalf("reserved count: %+v", sum.Counts)
	}
	if sum.Counts[RewardStatusIssued] != 0 || sum.Counts[RewardStatusRedeemed] != 0 {
		t.Fatalf("no issuance yet: %+v", sum.Counts)
	}
	if sum.PendingFaceMinor != 500 || sum.PendingCostMinor != 300 {
		t.Fatalf("pending amounts: %+v", sum)
	}
	// A grant moves committed face and cost separately.
	in := confirmInput(tenA, campA, "p-sum", "staff_verified_post")
	in.EventID = "cfm-sum"
	in.Now = "2026-10-14T10:30:00Z"
	must(t, func() error { _, _, err := s.ConfirmRewardBenefit(in); return err }())
	sum, err = s.RewardSummary(tenA, campA)
	must(t, err)
	if sum.Counts[RewardStatusIssued] != 1 || sum.Counts[RewardStatusReserved] != 1 {
		t.Fatalf("counts after grant: %+v", sum.Counts)
	}
	if sum.EvidenceGranted["staff_verified_post"] != 1 {
		t.Fatalf("evidence granted: %+v", sum.EvidenceGranted)
	}
	if sum.CommittedFaceMinor != 500 || sum.CommittedCostMinor != 300 {
		t.Fatalf("committed amounts: %+v", sum)
	}
	if sum.PendingFaceMinor != 500 || sum.PendingCostMinor != 300 {
		t.Fatalf("pending amounts after grant: %+v", sum)
	}
}

func TestRewardReviewRequests(t *testing.T) {
	s, tenA, campA, _, _, _, _ := seedRewardWorld(t)
	ts := now()
	// Two self-reported attempts, no benefits yet.
	must(t, withExec(s, `INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,asset_use_accepted,confirmed_content_version,confirmed_account_label,self_reported,platform_post_id,receipt_source,outbound_calls,query_count,reward_triggered,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"att-sr-1", tenA, campA, "code-1", "douyin", "activity_customer", "copy", "v1", "label",
		"unknown", 0, "", "", 1, "", "", 0, 0, 0, ts, ts))
	must(t, withExec(s, `INSERT INTO customer_publish_attempts(id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,asset_use_accepted,confirmed_content_version,confirmed_account_label,self_reported,platform_post_id,receipt_source,outbound_calls,query_count,reward_triggered,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"att-sr-2", tenA, campA, "code-2", "xiaohongshu", "activity_customer", "copy", "v1", "label",
		"unknown", 0, "", "", 1, "", "", 0, 0, 0, ts, ts))
	reqs, err := s.ListRewardReviewRequests(tenA, campA)
	must(t, err)
	if len(reqs) != 2 {
		t.Fatalf("two pending self reports expected: %+v", reqs)
	}
	sum, err := s.RewardSummary(tenA, campA)
	must(t, err)
	if sum.PendingReview != 2 {
		t.Fatalf("pending review count: %+v", sum)
	}
	// Confirming one drains the queue entry for that participation.
	in := confirmInput(tenA, campA, "att-sr-1", "staff_verified_post")
	in.EventID = "cfm-sr-1"
	must(t, func() error { _, _, err := s.ConfirmRewardBenefit(in); return err }())
	reqs, err = s.ListRewardReviewRequests(tenA, campA)
	must(t, err)
	if len(reqs) != 1 || reqs[0].AttemptID != "att-sr-2" {
		t.Fatalf("review queue after confirm: %+v", reqs)
	}
}
