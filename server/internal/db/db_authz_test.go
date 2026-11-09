package db

import (
	"path/filepath"
	"testing"
)

// HUI-4593 连锁、代理与权限:migration 0064-0067 的 additive 语义。
//
// 纪律(裁定书):
//   - 0064 代理关系 additive 增量列:allowed_actions/store_scope_set/valid_until/
//     grant_revision/payer_grant_ref;旧行零迁移、旧语义(NULL/缺省=只读+开子账号);
//   - 0065 authz_audit_log:授权域 append-only 审计(操作者/时间/前后值/原因);
//   - 0066 template_assignment:连锁模板分发引用(幂等重放唯一索引只约束
//     带 request_key 的请求;无 key 的重复分发合法);
//   - 0067 ops_grants:受信服务端 ops grant(平台管理员来源),商家面永不自授。

func TestOpenCreatesAuthzSchema(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	got, err := TableNames(d)
	if err != nil {
		t.Fatalf("table names: %v", err)
	}
	want := map[string]bool{
		"authz_audit_log":     false,
		"template_assignment": false,
		"ops_grants":          false,
	}
	for _, name := range got {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("table %s missing after migrations; got %v", name, got)
		}
	}
}

// TestAgencyScopeColumnsAdditive: 0008 时代的旧行(旧列集 INSERT)在 0064 之后
// 仍可原样写入,scope 列落缺省(旧语义);新列也可显式写入。
func TestAgencyScopeColumnsAdditive(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// legacy-shape INSERT (0008 column list) must keep working
	legacy := `INSERT INTO agency_relations(id,tenant_id,agent_principal,note,established_by,established_at,revoked_by,revoked_at)
		 VALUES('agr_old','tnt_x','usr_ag1','','owner','2026-01-01T00:00:00Z','',NULL)`
	if _, err := d.Exec(legacy); err != nil {
		t.Fatalf("legacy insert must keep working after 0064: %v", err)
	}
	var allowedActions, scopeSet, validUntil, payerRef string
	var revision int
	if err := d.QueryRow(`SELECT allowed_actions,store_scope_set,valid_until,payer_grant_ref,grant_revision
		 FROM agency_relations WHERE id='agr_old'`).
		Scan(&allowedActions, &scopeSet, &validUntil, &payerRef, &revision); err != nil {
		t.Fatalf("scope columns must exist with defaults: %v", err)
	}
	if allowedActions != "" || scopeSet != "" || validUntil != "" || payerRef != "" || revision != 0 {
		t.Fatalf("legacy row defaults wrong: %q %q %q %q %d", allowedActions, scopeSet, validUntil, payerRef, revision)
	}

	// extended insert carries the scope columns
	ext := `INSERT INTO agency_relations(id,tenant_id,agent_principal,note,established_by,established_at,revoked_by,revoked_at,
		 allowed_actions,store_scope_set,valid_until,grant_revision,payer_grant_ref)
		 VALUES('agr_new','tnt_x','usr_ag2','','owner','2026-01-01T00:00:00Z','',NULL,'["storefront","action"]','["sto_1"]','2027-01-01T00:00:00Z',3,'pgr_1')`
	if _, err := d.Exec(ext); err != nil {
		t.Fatalf("extended insert: %v", err)
	}
}

// TestTemplateAssignmentReplayUnique: 带 request_key 的同请求重试被唯一索引
// 拒绝(幂等重放的存储兜底);空 request_key 的重复分发合法。
func TestTemplateAssignmentReplayUnique(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO stores(id,tenant_id,name,address,status,created_at,updated_at)
		 VALUES('sto_1','tnt_x','一号店','', 'active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	ins := func(id, key string) string {
		return `INSERT INTO template_assignment(id,tenant_id,store_id,template_ref,source_version,allowed_override_fields,request_key,status,applied_revision,created_by,created_at,updated_at)
			 VALUES('` + id + `','tnt_x','sto_1','tpl_hero','v3','[]','` + key + `','distributed','','owner','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`
	}
	if _, err := d.Exec(ins("ta_1", "req_1")); err != nil {
		t.Fatalf("first keyed assignment: %v", err)
	}
	if _, err := d.Exec(ins("ta_2", "req_1")); err == nil {
		t.Fatal("same request_key retry must be refused by the replay unique index")
	}
	if _, err := d.Exec(ins("ta_3", "")); err != nil {
		t.Fatalf("unkeyed assignment: %v", err)
	}
	if _, err := d.Exec(ins("ta_4", "")); err != nil {
		t.Fatalf("unkeyed repeat distribution must stay legal: %v", err)
	}
	// same key on another store is a different request
	if _, err := d.Exec(`INSERT INTO stores(id,tenant_id,name,address,status,created_at,updated_at)
		 VALUES('sto_2','tnt_x','二号店','', 'active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed store 2: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO template_assignment(id,tenant_id,store_id,template_ref,source_version,allowed_override_fields,request_key,status,applied_revision,created_by,created_at,updated_at)
		 VALUES('ta_5','tnt_x','sto_2','tpl_hero','v3','[]','req_1','distributed','','owner','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("same key on another store must be allowed: %v", err)
	}
}

func TestTemplateAssignmentStatusCheck(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO stores(id,tenant_id,name,address,status,created_at,updated_at)
		 VALUES('sto_1','tnt_x','一号店','', 'active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO template_assignment(id,tenant_id,store_id,template_ref,source_version,allowed_override_fields,request_key,status,applied_revision,created_by,created_at,updated_at)
		 VALUES('ta_bad','tnt_x','sto_1','tpl','v1','[]','','done','','owner','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("status outside distributed/applied/cancelled must be refused by CHECK")
	}
}

func TestOpsGrantsSchema(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO ops_grants(id,tenant_id,principal_ref,store_scope_set,commands,issued_by_service,issued_at,expires_at,revoked_by,revoked_at)
		 VALUES('ops_1','tnt_x','usr_ops','sto_1','["ops:inspect"]','svc_ops','2026-01-01T00:00:00Z','2026-01-02T00:00:00Z','',NULL)`); err != nil {
		t.Fatalf("ops grant insert: %v", err)
	}
}

func TestMemberPermissionGrantActiveUnique(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		 VALUES('mem_s1','tnt_x','usr_s1','staff',NULL,1,'店员','seed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO member_permission_grant(id,tenant_id,member_id,permission,granted_by,granted_at,revoked_by,revoked_at,reason)
		 VALUES('g_1','tnt_x','mem_s1','storefront:configure','usr_owner','2026-01-01T00:00:00Z','',NULL,'门店帮工')`); err != nil {
		t.Fatalf("first active grant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO member_permission_grant(id,tenant_id,member_id,permission,granted_by,granted_at,revoked_by,revoked_at,reason)
		 VALUES('g_2','tnt_x','mem_s1','storefront:configure','usr_owner','2026-01-02T00:00:00Z','',NULL,'dup')`); err == nil {
		t.Fatal("second ACTIVE grant for the same (member, permission) must be refused by the partial unique index")
	}
	// revoke the first: re-grant becomes legal (新行,可回查)
	if _, err := d.Exec(`UPDATE member_permission_grant SET revoked_by='usr_owner', revoked_at='2026-01-03T00:00:00Z' WHERE id='g_1'`); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO member_permission_grant(id,tenant_id,member_id,permission,granted_by,granted_at,revoked_by,revoked_at,reason)
		 VALUES('g_3','tnt_x','mem_s1','storefront:configure','usr_owner','2026-01-04T00:00:00Z','',NULL,'重新授权')`); err != nil {
		t.Fatalf("re-grant after revoke must be allowed: %v", err)
	}
}

func TestAuthzAuditAppendOnlyShape(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	row := `INSERT INTO authz_audit_log(id,tenant_id,actor_principal,actor_member_id,actor_role,action,target_type,target_id,before_json,after_json,reason,created_at)
		 VALUES('aud_1','tnt_x','usr_owner','mem_1','org_owner','member_scope_change','member','mem_2','{}','{"store_scope":"sto_1"}','门店交接','2026-01-01T00:00:00Z')`
	if _, err := d.Exec(row); err != nil {
		t.Fatalf("audit insert: %v", err)
	}
}
