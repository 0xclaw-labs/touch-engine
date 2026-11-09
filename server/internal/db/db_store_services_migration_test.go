package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 会让这个测试失败的生产改动：store_service_events 或 wecom_callback_log
// 引入任何「成功」语义列/枚举（followed/connected/added/reviewed）、
// 迁移不幂等（重复执行翻倍）、服务行放开 kind 枚举外的值。

func TestStoreServicesSchemaAndEvidenceHonesty(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	mustExec := func(q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','商家','now')`)
	mustExec(`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		VALUES('sto_1','tnt_1','一店','','u','now','now')`)

	// Wi-Fi 行：config JSON 由模型层校验，存储层只收 kind 枚举内的行。
	mustExec(`INSERT INTO store_services(id,tenant_id,store_id,catalog_code,kind,variant,label,config,
		sort_order,enabled,revision,created_at,updated_at)
		VALUES('sv_a','tnt_1','sto_1','A17','wifi','','门店 Wi-Fi','{"ssid":"Painuo-Guest","password":"passw0rd123","encryption":"wpa2"}',
		1,1,1,'now','now')`)
	// 同店同 kind 可并存多条（个人微信与企微、公众号与社群互不覆盖）。
	mustExec(`INSERT INTO store_services(id,tenant_id,store_id,catalog_code,kind,variant,label,config,
		sort_order,enabled,revision,created_at,updated_at)
		VALUES('sv_b','tnt_1','sto_1','A18','contact','wecom','加企业微信','{"url":"https://work.weixin.qq.com/ca/abc"}',
		2,1,1,'now','now')`)

	// kind CHECK：枚举外拒绝；成功语义 kind（followed 等）不存在且进不来。
	if _, err := d.Exec(`INSERT INTO store_services(id,tenant_id,store_id,catalog_code,kind,variant,label,config,
		sort_order,enabled,revision,created_at,updated_at)
		VALUES('sv_x','tnt_1','sto_1','','followed','','x','{}',3,1,1,'now','now')`); err == nil {
		t.Fatal("kind 'followed' must be refused by CHECK")
	}
	// catalog_code CHECK：'' 或 A[0-9][0-9]。
	if _, err := d.Exec(`INSERT INTO store_services(id,tenant_id,store_id,catalog_code,kind,variant,label,config,
		sort_order,enabled,revision,created_at,updated_at)
		VALUES('sv_y','tnt_1','sto_1','X99','wifi','','x','{}',3,1,1,'now','now')`); err == nil {
		t.Fatal("catalog_code 'X99' must be refused by CHECK")
	}

	// 事件表：只收 open/jump_attempt/copy 三种行为证据；「关注/连接/添加成功」
	// 语义的枚举值在 CHECK 之外，写不进去。
	mustExec(`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
		VALUES('ev_1','tnt_1','sto_1','sv_a','open','ios_safari','click','now')`)
	mustExec(`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
		VALUES('ev_2','tnt_1','sto_1','sv_a','jump_attempt','android_browser','jump_attempt','now')`)
	for _, bad := range []string{"followed", "connected", "added", "reviewed", "success"} {
		if _, err := d.Exec(`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
			VALUES('ev_bad','tnt_1','sto_1','sv_a','` + bad + `','ios_safari','click','now')`); err == nil {
			t.Fatalf("event_kind %q must be refused by CHECK", bad)
		}
	}

	// 企微回调日志：digest-only，状态枚举诚实；无「customer_added」类列可写。
	mustExec(`INSERT INTO wecom_callback_log(id,occurred_at,msg_signature,signature_valid,decrypt_state,payload_digest,note)
		VALUES('wc_1','now','da39a3ee5e6b4b0d3255bfef95601890afd80709',0,'pending_qualification','','待资质')`)
	if _, err := d.Exec(`INSERT INTO wecom_callback_log(id,occurred_at,msg_signature,signature_valid,decrypt_state,payload_digest,note)
		VALUES('wc_2','now','',1,'customer_added','','x')`); err == nil {
		t.Fatal("decrypt_state 'customer_added' must be refused by CHECK")
	}
}

func TestStoreServicesMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "touch.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tables := []string{"store_services", "store_service_configs", "store_service_snapshots",
		"store_service_events", "wecom_callback_log"}
	count := func(db interface{ Query(string, ...any) (*sql.Rows, error) }, table string) int {
		t.Helper()
		rs, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		defer rs.Close()
		n := 0
		for rs.Next() {
			n++
		}
		return n
	}
	for _, table := range tables {
		if count(d, table) != 1 {
			t.Fatalf("table %s missing after first open", table)
		}
	}
	d.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	for _, table := range tables {
		if count(d2, table) != 1 {
			t.Fatalf("table %s duplicated after reopen (migration not idempotent)", table)
		}
	}
}
