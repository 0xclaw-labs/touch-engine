package store

import "testing"

// 当前选择可变化；只有准确关联的冻结事实可以归入历史内容版本。
func TestDashboardOpsFrozenVersionAttribution(t *testing.T) {
	w := seedOpsWorld(t)
	w.rawParticipation(t, "version-p", w.campA1, w.store1, "nfc", "v2", "in_progress", "2026-10-14T08:00:00Z")
	w.rawAttempt(t, "version-a", "version-p", "douyin", "h5", "v1", "handed_off", "2026-10-14T08:10:00Z")
	w.rawCredential(t, "version-h", "version-p", "version-a", "2026-10-14T08:11:00Z")
	w.rawCredential(t, "version-h-replay", "version-p", "version-a", "2026-10-14T08:12:00Z")
	w.rawBenefit(t, "version-b", "version-p", "staff_verified_post", "redeemed", w.store1, 500, 300, "2026-10-14T09:00:00Z", "2026-10-14T10:00:00Z", "")
	start, end := opsWinBounds(t)
	for _, tc := range []struct {
		version            string
		selected, handoffs int64
	}{{"v1", 1, 1}, {"v2", 0, 0}, {"missing", 0, 0}} {
		t.Run(tc.version, func(t *testing.T) {
			facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{ContentPackVersion: tc.version})
			must(t, err)
			if facts.ContentSelectedTotal != tc.selected || facts.HandoffInitiatedTotal != tc.handoffs {
				t.Errorf("frozen %s: selected/handoffs=%d/%d, want %d/%d", tc.version, facts.ContentSelectedTotal, facts.HandoffInitiatedTotal, tc.selected, tc.handoffs)
			}
			if facts.StartedTotal != 1 || facts.EvidenceDistinctParticipations != 1 || facts.IssuedTotal != 1 || facts.RedeemedTotal != 1 || facts.Cost.RewardCostMinor != 300 {
				t.Errorf("facts without immutable version must remain unfiltered: %+v", facts)
			}
		})
	}
}

func TestDashboardOpsHandoffVersionRequiresExactAttempt(t *testing.T) {
	w := seedOpsWorld(t)
	w.rawParticipation(t, "p-one", w.campA1, w.store1, "nfc", "v2", "in_progress", "2026-10-14T08:00:00Z")
	w.rawParticipation(t, "p-other", w.campA1, w.store1, "nfc", "v1", "in_progress", "2026-10-14T08:00:00Z")
	w.rawAttempt(t, "a-other", "p-other", "douyin", "h5", "v1", "handed_off", "2026-10-14T08:10:00Z")
	w.rawCredential(t, "h-unbound", "p-one", "", "2026-10-14T08:11:00Z")
	w.rawCredential(t, "h-mismatched", "p-one", "a-other", "2026-10-14T08:12:00Z")
	start, end := opsWinBounds(t)
	for _, version := range []string{"v1", "v2"} {
		facts, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{ContentPackVersion: version})
		must(t, err)
		if facts.HandoffInitiatedTotal != 0 {
			t.Errorf("%s must not infer unrelated credential version: %d", version, facts.HandoffInitiatedTotal)
		}
	}
	plain, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	if plain.HandoffInitiatedTotal != 1 {
		t.Fatalf("unfiltered credential fact must remain visible: %d", plain.HandoffInitiatedTotal)
	}
}

func TestDashboardOpsInventoryStoreAttribution(t *testing.T) {
	w := seedOpsWorld(t)
	// 标记未知成本，验证计数使用与库存面板相同的可见范围。
	must(t, w.exec(`UPDATE reward_inventory SET cost_recorded=0 WHERE id=?`, w.invA1))
	makeInventory := func(id, campaign, store string) {
		t.Helper()
		must(t, w.exec(`INSERT INTO reward_inventory(id,tenant_id,campaign_id,store_id,kind,name,benefit_slot,rule_version,allowed_evidence,total_qty,face_value_minor,cost_minor,currency,status,created_at)
		 VALUES(?,?,?,?, 'store_coupon',?, ?,3,'[]',100,500,0,'FEN','active','2026-10-14T08:00:00Z')`, id, w.tenA, campaign, store, id, id))
	}
	makeInventory("explicit-own", w.campA1, w.store1)
	makeInventory("conflicting", w.campA1, w.store2)
	makeInventory("unbound", w.campA2, "")
	makeInventory("explicit-unbound", w.campA2, w.store2)
	start, end := opsWinBounds(t)
	for _, tc := range []struct {
		name, scope, filter string
		ids                 []string
	}{
		{"store-one", w.store1, "", []string{w.invA1, "explicit-own"}},
		{"store-two", w.store2, "", []string{"explicit-unbound"}},
		{"filter-two", "", w.store2, []string{"explicit-unbound"}},
		{"scope-wins", w.store2, w.store1, []string{"explicit-unbound"}},
		{"tenant-admin", "", "", []string{w.invA1, "explicit-own", "conflicting", "unbound", "explicit-unbound"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, _, err := w.s.DashboardOps(w.tenA, tc.scope, start, end, OpsFilter{StoreID: tc.filter})
			must(t, err)
			seen := map[string]bool{}
			for _, row := range facts.Inventory {
				seen[row.InventoryID] = true
			}
			if len(seen) != len(tc.ids) {
				t.Errorf("inventory ids=%v, want %v", seen, tc.ids)
			}
			for _, id := range tc.ids {
				if !seen[id] {
					t.Errorf("missing visible batch %s", id)
				}
			}
			if facts.Cost.CostUnrecordedBatches != int64(len(tc.ids)) {
				t.Errorf("unrecorded=%d, want %d", facts.Cost.CostUnrecordedBatches, len(tc.ids))
			}
		})
	}
}

func TestDashboardOpsInventoryBenefitsKeepStoreAndTenantScope(t *testing.T) {
	w := seedOpsWorld(t)
	w.rawParticipation(t, "inv-p", w.campA1, w.store1, "nfc", "v1", "in_progress", "2026-10-14T08:00:00Z")
	w.rawBenefit(t, "inv-own", "inv-p", "staff_verified_post", "issued", w.store1, 500, 300, "2026-10-14T09:00:00Z", "", "")
	w.rawBenefit(t, "inv-other", "inv-p", "staff_verified_post", "redeemed", w.store2, 500, 300, "2026-10-14T09:00:00Z", "2026-10-14T10:00:00Z", "")
	w.rawBenefit(t, "inv-unknown", "inv-p", "", "reserved", "", 500, 300, "2026-10-14T09:00:00Z", "", "")
	w.rawBenefit(t, "inv-foreign", "inv-p", "", "reserved", w.store1, 500, 300, "2026-10-14T09:00:00Z", "", "")
	must(t, w.exec(`UPDATE reward_benefits SET tenant_id=? WHERE id='inv-foreign'`, w.tenB))
	start, end := opsWinBounds(t)
	facts, _, err := w.s.DashboardOps(w.tenA, w.store1, start, end, OpsFilter{})
	must(t, err)
	if len(facts.Inventory) != 1 {
		t.Fatalf("own campaign's implicit batch missing: %+v", facts.Inventory)
	}
	row := facts.Inventory[0]
	if !row.AvailableUnknown {
		t.Error("scoped reader cannot infer complete batch availability")
	}
	if row.Issued != 1 || row.Redeemed != 0 || row.Reserved != 0 {
		t.Errorf("scoped benefit counts leaked: %+v", row)
	}
	admin, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
	must(t, err)
	if admin.Inventory[0].Issued != 1 || admin.Inventory[0].Redeemed != 1 || admin.Inventory[0].Reserved != 1 {
		t.Fatalf("admin must retain tenant ledger: %+v", admin.Inventory)
	}
	if admin.Inventory[0].AvailableUnknown {
		t.Error("tenant admin retains complete tenant occupancy")
	}
}

func TestDashboardOpsInventoryMissingOrForeignCampaignFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "foreign-tenant"} {
		t.Run(mode, func(t *testing.T) {
			w := seedOpsWorld(t)
			// 仅隔离测试库模拟历史损坏/删除残留；正常业务外键不允许制造这种状态。
			must(t, w.exec(`PRAGMA foreign_keys=OFF`))
			if mode == "missing" {
				must(t, w.exec(`DELETE FROM campaigns WHERE id=?`, w.campA1))
			} else {
				must(t, w.exec(`UPDATE campaigns SET tenant_id=? WHERE id=?`, w.tenB, w.campA1))
			}
			must(t, w.exec(`PRAGMA foreign_keys=ON`))
			must(t, w.exec(`UPDATE reward_inventory SET store_id=?,cost_recorded=0 WHERE id=?`, w.store1, w.invA1))
			start, end := opsWinBounds(t)
			facts, _, err := w.s.DashboardOps(w.tenA, w.store1, start, end, OpsFilter{})
			must(t, err)
			if len(facts.Inventory) != 0 || facts.Cost.CostUnrecordedBatches != 0 {
				t.Fatalf("unprovable campaign ownership leaked: %+v", facts.Inventory)
			}
			admin, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{})
			must(t, err)
			if len(admin.Inventory) != 1 {
				t.Fatalf("admin should retain tenant batch record: %+v", admin.Inventory)
			}
		})
	}
}

func TestDashboardOpsMultipleFrozenAttemptsAndPublishSnapshots(t *testing.T) {
	w := seedOpsWorld(t)
	w.rawParticipation(t, "multi-p", w.campA1, w.store1, "nfc", "current", "in_progress", "2026-10-14T08:00:00Z")
	w.rawParticipation(t, "other-store-p", w.campA1, w.store2, "nfc", "current", "in_progress", "2026-10-14T08:00:00Z")
	for _, a := range []struct{ id, p, version, at string }{
		{"old-a", "multi-p", "old", "2026-10-13T08:00:00Z"},
		{"old-a-retry", "multi-p", "old", "2026-10-14T08:10:00Z"},
		{"new-a", "multi-p", "new", "2026-10-14T08:20:00Z"},
		{"new-a-retry", "multi-p", "new", "2026-10-14T08:30:00Z"},
		{"other-store-a", "other-store-p", "new", "2026-10-14T08:20:00Z"},
	} {
		w.rawAttempt(t, a.id, a.p, "douyin", "h5", a.version, "handed_off", a.at)
		w.rawCredential(t, "h-"+a.id, a.p, a.id, a.at)
	}
	w.rawHandoff(t, "snapshot-old", w.store1, "multi-p", "old-a", "handoff_ready", "2026-10-14T09:00:00Z")
	w.rawHandoff(t, "snapshot-new", w.store1, "multi-p", "new-a", "handoff_ready", "2026-10-14T09:00:00Z")
	w.rawHandoffEvent(t, "snapshot-event-old", "snapshot-old", "media_handoff", "2026-10-14T09:01:00Z")
	w.rawHandoffEvent(t, "snapshot-event-new", "snapshot-new", "media_handoff", "2026-10-14T09:01:00Z")
	must(t, w.exec(`UPDATE publish_handoffs SET content_pack_version_id='old' WHERE id='snapshot-old'`))
	must(t, w.exec(`UPDATE publish_handoffs SET content_pack_version_id='new' WHERE id='snapshot-new'`))
	start, end := opsWinBounds(t)
	for _, tc := range []struct {
		version                    string
		selected, handoff, publish int64
	}{{"old", 0, 0, 1}, {"new", 1, 1, 1}, {"current", 0, 0, 0}} {
		facts, _, err := w.s.DashboardOps(w.tenA, w.store1, start, end, OpsFilter{ContentPackVersion: tc.version})
		must(t, err)
		if facts.ContentSelectedTotal != tc.selected || facts.HandoffInitiatedTotal != tc.handoff || facts.LayerHandoffRequested != tc.publish || facts.LayerMediaHandoff != tc.publish {
			t.Errorf("%s: selected/credential/publish/event=%d/%d/%d/%d, want %d/%d/%d/%d", tc.version, facts.ContentSelectedTotal, facts.HandoffInitiatedTotal, facts.LayerHandoffRequested, facts.LayerMediaHandoff, tc.selected, tc.handoff, tc.publish, tc.publish)
		}
	}
	admin, _, err := w.s.DashboardOps(w.tenA, "", start, end, OpsFilter{ContentPackVersion: "new"})
	must(t, err)
	if admin.ContentSelectedTotal != 2 || admin.HandoffInitiatedTotal != 2 {
		t.Fatalf("tenant scope must count two distinct participations, not repeated attempts: %d/%d", admin.ContentSelectedTotal, admin.HandoffInitiatedTotal)
	}
}
