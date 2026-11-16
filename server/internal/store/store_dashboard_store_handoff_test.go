package store

import "testing"

// Store-root handoffs are real facts even when there is no campaign. Their
// frozen store owns the fact; events cannot borrow a row from another tenant.
func TestDashboardOpsStoreRootHandoffsAndEvents(t *testing.T) {
	w := seedOpsWorld(t)
	stamp := "2026-10-14T09:40:00Z"
	for _, row := range []struct{ id, store string }{{"root-one", w.store1}, {"root-two", w.store2}, {"legacy", w.store1}} {
		var campaign any
		platform, method, payload, version := "wechat", "manual_material", "media", "version-1"
		if row.id == "root-one" {
			platform, method, payload, version = "qq", "link_share", "link_share", ""
		}
		if row.id == "legacy" {
			campaign = w.campA1
			version = ""
		}
		must(t, w.exec(`INSERT INTO publish_handoffs(id,tenant_id,store_id,campaign_id,platform,entry_mode,scenario,idempotency_key,status,evidence_source,media_json,text_json,visitor_id,action_id,action_session_id,action_revision,method,environment,payload_kind,content_pack_version_id,created_at,updated_at)
			VALUES(?,?,?,?,?,'h5','store_promo',?,'handoff_ready','customer_action','[]','{}',?,?,?,1,?,'desktop_browser',?,?,?,?)`,
			row.id, w.tenA, row.store, campaign, platform, "idem-"+row.id, "vis-"+row.id, "sa-"+row.id, "sess-"+row.id, method, payload, version, stamp, stamp))
		for _, kind := range []string{"customer_click", "wake_attempt", "media_handoff"} {
			w.rawHandoffEvent(t, row.id+"-"+kind, row.id, kind, stamp)
		}
	}
	// Corrupt cross-tenant event linkage must never turn into this tenant's click.
	w.rawHandoffEvent(t, "foreign-click", "root-one", "customer_click", stamp)
	must(t, w.exec(`UPDATE publish_handoff_events SET tenant_id=? WHERE id='foreign-click'`, w.tenB))
	start, end := opsWinBounds(t)
	for _, tc := range []struct {
		name, scope string
		filter      OpsFilter
		want        int64
	}{
		{"store-root-one", w.store1, OpsFilter{ActionID: "sa-root-one"}, 1},
		{"store-root-two", w.store2, OpsFilter{}, 1},
		{"store-one-includes-legacy", w.store1, OpsFilter{}, 2},
		{"campaign-excludes-root", w.store1, OpsFilter{CampaignID: w.campA1}, 1},
		{"link-has-no-media-version", w.store1, OpsFilter{ContentPackVersion: "version-1"}, 0},
		{"exact-frozen-version", w.store2, OpsFilter{ContentPackVersion: "version-1"}, 1},
		{"other-action", w.store2, OpsFilter{ActionID: "sa-root-one"}, 0},
		{"scope-precedes-filter", w.store2, OpsFilter{StoreID: w.store1}, 1},
		{"tenant-overview", "", OpsFilter{}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, _, err := w.s.DashboardOps(w.tenA, tc.scope, start, end, tc.filter)
			must(t, err)
			for name, got := range map[string]int64{"requested": facts.LayerHandoffRequested, "ready": facts.LayerHandoffCompleted, "media": facts.LayerMediaHandoff, "click": facts.LayerCustomerClick, "wake": facts.LayerWakeAttempt} {
				if got != tc.want {
					t.Errorf("%s=%d, want %d", name, got, tc.want)
				}
			}
		})
	}
	// A later campaign move cannot reattribute a frozen handoff to another store.
	must(t, w.exec(`UPDATE campaigns SET store_id=? WHERE id=?`, w.store2, w.campA1))
	facts, _, err := w.s.DashboardOps(w.tenA, w.store1, start, end, OpsFilter{ActionID: "sa-legacy"})
	must(t, err)
	if facts.LayerHandoffRequested != 1 || facts.LayerMediaHandoff != 1 {
		t.Fatalf("campaign move rewrote frozen store facts: requested/media=%d/%d", facts.LayerHandoffRequested, facts.LayerMediaHandoff)
	}
}
