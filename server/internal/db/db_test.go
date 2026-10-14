package db

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	want := []string{
		"activity_benefit_facts",
		"agency_relations",
		"agency_subaccount_trail",
		"authorized_return_clicks",
		"authorized_returns",
		"campaign_assets",
		"campaign_links",
		"campaign_rule_revisions",
		"campaign_rules",
		"campaign_saved_copy",
		"campaigns",
		"content_handoffs",
		"content_pack_adoptions",
		"content_pack_version_events",
		"content_pack_versions",
		"content_packs",
		"copy_drafts",
		"copy_jobs",
		"copy_versions",
		"customer_publish_attempts",
		"extra_jump_actions",
		"extra_jump_clicks",
		"lead_audit",
		"lead_forms",
		"lead_submissions",
		"leads_outbox",
		"lib_assets",
		"lib_pool_items",
		"lib_pools",
		"lib_selections",
		"members",
		"nfc_tag_groups",
		"nfc_tags",
		"private_domain_clicks",
		"private_domain_entries",
		"public_view_stats",
		"publish_adapter_notes",
		"publish_reward_ledger",
		"publish_reward_proofs",
		"publish_reward_rules",
		"publish_reward_subjects",
		"reward_benefits",
		"reward_code_pool",
		"reward_inventory",
		"reward_official_facts",
		"reward_operations",
		"schema_migrations",
		"store_motion_requests",
		"stores",
		"tenant_exports",
		"tenants",
		"video_template_assignments",
		"video_template_versions",
		"video_templates",
	}
	got, err := TableNames(d)
	if err != nil {
		t.Fatalf("table names: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables = %v, want %v", got, want)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "touch.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	d.Close()

	// reopen: migrations must not re-apply or fail
	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
}

func TestForeignKeysEnforced(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	// campaign row without tenant must be refused by FK
	if _, err := d.Exec(`INSERT INTO campaigns(id,tenant_id,title,public_content,status,starts_at,ends_at,created_by,created_at,updated_at)
		VALUES('cmp_x','tnt_missing','t','', 'draft','','','u','now','now')`); err == nil {
		t.Fatal("expected FK violation for missing tenant, got nil")
	}
}
