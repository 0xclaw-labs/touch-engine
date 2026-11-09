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
		// HUI-3546 增量轮(0056-0058): 匿名动作会话/恢复凭据/审计事件。
		"action_session_credentials",
		"action_session_events",
		"action_sessions",
		"activity_benefit_facts",
		"agency_relations",
		"agency_subaccount_trail",
		"authorized_return_clicks",
		"authorized_returns",
		// HUI-4593 (0065) 授权域审计
		"authz_audit_log",
		"campaign_assets",
		"campaign_links",
		"campaign_publish_config_revisions",
		"campaign_publish_configs",
		"campaign_rule_revisions",
		"campaign_rules",
		"campaign_saved_copy",
		"campaigns",
		"channel_action_capabilities",
		"channel_admission_control_log",
		"channel_admission_revisions",
		"channel_admissions",
		"content_handoffs",
		"content_media",
		"content_pack_adoptions",
		"content_pack_version_events",
		"content_pack_versions",
		"content_packs",
		"copy_drafts",
		"copy_jobs",
		"copy_versions",
		"customer_publish_attempts",
		// HUI-1671 增量(0068-0072)
		"deal_offers",
		// HUI-4590 (0087) 外部券核销尝试
		"external_voucher_attempts",
		"extra_jump_actions",
		"extra_jump_clicks",
		"handoff_credentials",
		"lead_audit",
		"lead_forms",
		"lead_receipts",
		"lead_submissions",
		"leads_outbox",
		"lib_assets",
		"lib_pool_items",
		"lib_pools",
		"lib_selections",
		"media_access_grants",
		// HUI-4593 修复轮 2 (0065) 店员动作级授权
		"member_permission_grant",
		"members",
		"nfc_tag_groups",
		"nfc_tags",
		// HUI-4593 (0067) 受信服务端 ops grant
		"ops_grants",
		"participation_attempts",
		"participations",
		"private_domain_clicks",
		"private_domain_entries",
		"public_view_stats",
		"publish_adapter_notes",
		"publish_handoff_events",
		"publish_handoffs",
		"publish_reward_ledger",
		"publish_reward_proofs",
		"publish_reward_rules",
		"publish_reward_subjects",
		"reward_benefits",
		"reward_code_pool",
		// HUI-1671 增量(0068-0072)
		"reward_display_codes",
		"reward_expiry_facts",
		"reward_external_issuances",
		"reward_inventory",
		"reward_official_facts",
		"reward_operations",
		"schema_migrations",
		// HUI-4584 (0043): 门店动作实例模型 + 配置修订 + 公共门店码。
		"store_action_configs",
		// HUI-3553 增量(0050-0055)
		"store_action_content_bindings",
		"store_action_snapshots",
		"store_actions",
		"store_content_pack_adoptions",
		"store_content_pack_version_events",
		"store_content_pack_versions",
		"store_content_packs",
		// HUI-4590 增量(0084-0087): 团购套餐目录/适用门店/点击归因/外部券核销事实。
		"store_deal_click_stats",
		"store_deal_offer_stores",
		"store_deal_offers",
		"store_motion_requests",
		// HUI-4589 (0079-0081): 门店服务配置/行为事件/企微回调日志。
		"store_service_configs",
		"store_service_events",
		"store_service_snapshots",
		"store_services",
		"store_storefronts",
		"stores",
		// HUI-4593 (0066) 连锁模板分发引用
		"template_assignment",
		"tenant_exports",
		"tenants",
		// HUI-1665 增量(0040/0041)
		"touchpoint_aliases",
		"touchpoint_events",
		"video_template_assignments",
		"video_template_versions",
		"video_templates",
		// HUI-4589 (0081) 企微回调日志（digest-only）
		"wecom_callback_log",
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
