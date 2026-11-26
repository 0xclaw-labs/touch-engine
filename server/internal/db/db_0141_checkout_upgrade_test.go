package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestUpgradeFrom0140PreservesAllBillingAndCheckoutRows 模拟真实线上已执行到
// 0140_douyin_share_bindings.sql 的数据库(已存在 0110 billing_orders 与 0139
// billing_checkout_refs 历史行),就地升级 0141 后验证:
//   - 零丢表、零清库、零改写历史原单/价格快照/授权快照/ck_* 续付引用/退款行;
//   - 0141 新列存在且带安全默认值;
//   - 重复执行 Migrate 幂等。
func TestUpgradeFrom0140PreservesAllBillingAndCheckoutRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-0140.db")
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	d.SetMaxOpenConns(1)
	defer d.Close()

	// 1. 只应用到 0140(跳过 0141 及之后迁移),构建真实升级前基线库。
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	sort.Strings(entries)
	appliedPre := 0
	for _, name := range entries {
		if strings.Compare(name, "migrations/0141_") >= 0 {
			continue
		}
		applied, err := alreadyApplied(d, name)
		if err != nil {
			t.Fatalf("alreadyApplied %s: %v", name, err)
		}
		if applied {
			continue
		}
		body, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		tx, err := d.Begin()
		if err != nil {
			t.Fatalf("begin %s: %v", name, err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			t.Fatalf("exec %s: %v", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(name) VALUES(?)`, name); err != nil {
			tx.Rollback()
			t.Fatalf("record %s: %v", name, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %s: %v", name, err)
		}
		appliedPre++
	}
	if appliedPre == 0 {
		t.Fatal("expected pre-0141 migrations to apply")
	}

	// 2. 在 0140 结构下写入真实历史业务数据(旧订单、价格快照、代理授权快照、
	//    0139 续付引用 ck_*、订单事件、退款行)。
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_legacy','老商家','2026-11-01T00:00:00Z')`)
	legacySnap := `{"price_ref":"price_m_v1","pricing_version":"v2026.01","price_minor":9900,"currency":"CNY","service_days":30}`
	mustExec(`INSERT INTO billing_orders
		(id,tenant_id,kind,action,plan_code,price_ref,price_snapshot,amount_minor,currency,status,
		 order_ref,payment_ref,idempotency_key,payer_grant_ref,agency_relation_ref,payer_principal,
		 actor_principal,actor_role,fail_reason,created_at,updated_at)
		VALUES('bo_legacy_1','tnt_legacy','subscription','renew','store.plan.month','price_m_v1',?,9900,'CNY','opening',
		       'ord_ext_1','pay_ext_1','idem_legacy_1','pg_legacy_1','agr_legacy_1','usr_agent_1',
		       'usr_agent_1','agent','','2026-11-10T01:00:00Z','2026-11-10T01:05:00Z')`, legacySnap)
	mustExec(`INSERT INTO billing_order_events
		(id,tenant_id,order_id,source,event_id,event_type,payload,occurred_at,created_at)
		VALUES('boe_legacy_1','tnt_legacy','bo_legacy_1','pai-billing','evt_legacy_1','payment_paid',
		       '{"payment_ref":"pay_ext_1"}','2026-11-10T01:05:00Z','2026-11-10T01:05:00Z')`)
	mustExec(`INSERT INTO billing_checkout_refs
		(id,tenant_id,handoff_id,quote_ref,checkout_ref,order_ref,state,amount_minor,currency,
		 payer_ref,payment_ref,fail_reason,created_at,updated_at)
		VALUES('bcr_legacy_1','tnt_legacy','cph_legacy_1','q_legacy_1','ck_legacy_001','cph_legacy_1',
		       'checkout_pending',123456,'CNY','acct_legacy','','','2026-11-15T10:00:00Z','2026-11-15T10:00:00Z')`)
	mustExec(`INSERT INTO billing_refunds
		(id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,invoice_title,invoice_status,
		 invoice_ref,auditor,source,event_id,payload,occurred_at,created_at)
		VALUES('brf_legacy_1','tnt_legacy','bo_legacy_1','ref_legacy_1',1000,'CNY','部分退差','老商家有限公司','pending',
		       '','ops_auditor','pai-billing','evt_ref_1','{}','2026-11-15T12:00:00Z','2026-11-15T12:00:00Z')`)

	// 3. 就地运行 Migrate(应用 0141)并再次重跑验证幂等。
	if err := Migrate(d); err != nil {
		t.Fatalf("upgrade to 0141: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("idempotent re-migrate: %v", err)
	}

	// 4. 逐行验证所有历史记录毫发无损,且新列带安全默认值。
	var (
		gotSnap, gotStatus, gotPayRef, gotGrant, gotPricingVer string
		gotAmt                                                 int64
		gotGrantRev                                            int64
	)
	if err := d.QueryRow(`SELECT price_snapshot,amount_minor,status,payment_ref,payer_grant_ref,pricing_version,payer_grant_revision
		FROM billing_orders WHERE id='bo_legacy_1'`).
		Scan(&gotSnap, &gotAmt, &gotStatus, &gotPayRef, &gotGrant, &gotPricingVer, &gotGrantRev); err != nil {
		t.Fatalf("query upgraded billing_orders: %v", err)
	}
	if gotSnap != legacySnap || gotAmt != 9900 || gotStatus != "opening" || gotPayRef != "pay_ext_1" ||
		gotGrant != "pg_legacy_1" || gotPricingVer != "" || gotGrantRev != 0 {
		t.Fatalf("legacy billing_order mutated after upgrade: snap=%q amt=%d status=%q pay=%q grant=%q pv=%q rev=%d",
			gotSnap, gotAmt, gotStatus, gotPayRef, gotGrant, gotPricingVer, gotGrantRev)
	}

	var (
		ckRef, ckOrder, ckState, ckKind, ckRemote, ckTradeNo, ckRemoteStatus string
		ckAmt                                                                int64
	)
	if err := d.QueryRow(`SELECT checkout_ref,order_ref,state,amount_minor,order_kind,remote_checkout_ref,out_trade_no,remote_status
		FROM billing_checkout_refs WHERE id='bcr_legacy_1'`).
		Scan(&ckRef, &ckOrder, &ckState, &ckAmt, &ckKind, &ckRemote, &ckTradeNo, &ckRemoteStatus); err != nil {
		t.Fatalf("query upgraded billing_checkout_refs: %v", err)
	}
	if ckRef != "ck_legacy_001" || ckOrder != "cph_legacy_1" || ckState != "checkout_pending" || ckAmt != 123456 ||
		ckKind != "content_production" || ckRemote != "" || ckTradeNo != "" || ckRemoteStatus != "" {
		t.Fatalf("legacy billing_checkout_refs mutated: ref=%q order=%q state=%q amt=%d kind=%q remote=%q trade=%q rstat=%q",
			ckRef, ckOrder, ckState, ckAmt, ckKind, ckRemote, ckTradeNo, ckRemoteStatus)
	}

	var refundCount, eventCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM billing_refunds WHERE id='brf_legacy_1'`).Scan(&refundCount); err != nil || refundCount != 1 {
		t.Fatalf("legacy refund lost: count=%d err=%v", refundCount, err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM billing_order_events WHERE id='boe_legacy_1'`).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("legacy order event lost: count=%d err=%v", eventCount, err)
	}

	// 5. 验证升级后可以正常回填远端 Mint 元数据(保留旧 ck_* 主键与订单不变)。
	mustExec(`UPDATE billing_checkout_refs
		SET remote_checkout_ref='tco_live_001', out_trade_no='OTN202611170001', provider='wechat',
		    remote_status='pending', return_target='ti-touch-engine-billing', price_digest='sha256:abc',
		    expires_at='2026-11-17T12:00:00Z', intent_key='idem_legacy_ck_1'
		WHERE id='bcr_legacy_1'`)
	if err := d.QueryRow(`SELECT remote_checkout_ref,out_trade_no,remote_status FROM billing_checkout_refs WHERE id='bcr_legacy_1'`).
		Scan(&ckRemote, &ckTradeNo, &ckRemoteStatus); err != nil {
		t.Fatalf("query updated checkout_ref: %v", err)
	}
	if ckRemote != "tco_live_001" || ckTradeNo != "OTN202611170001" || ckRemoteStatus != "pending" {
		t.Fatalf("updated checkout_ref mismatch: %q %q %q", ckRemote, ckTradeNo, ckRemoteStatus)
	}
}
