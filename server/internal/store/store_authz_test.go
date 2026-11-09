package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// HUI-4593 连锁、代理与权限:存储层增量(代理 scope/授权审计/模板分发/ops grant)。
// 全部走真实 sqlite(embedded migrations,含 0064-0067)。
//
// 纪律:
//   - 代理 scope additive:旧关系行为逐字节不变(空 scope = 旧只读+开子账号);
//     scope 显式变更 = grant_revision+1(乐观并发,STALE 拒绝)+审计同事务;
//   - 到期 = 关系整体失效(fail-closed,HasValidAgencyRelation 单点);
//   - 审计 append-only(操作者/时间/前后值/原因);
//   - 模板分发只记录分发/授权结果,幂等重放(带 request_key),保留字段拒绝;
//   - ops grant 只由受信服务端缝落库;过期/撤销不进 ListValidOpsGrants。

func TestAgencyRelationScopeRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	rel, err := s.CreateAgencyRelation(ctx.tenA.ID, "usr_ag1", ctx.ownA.ID, "")
	must(t, err)
	// legacy row reads back with zero-valued scope (旧语义)
	got, err := s.GetAgencyRelation(rel.ID)
	must(t, err)
	if len(got.AllowedActions) != 0 || len(got.StoreScopeSet) != 0 || got.ValidUntil != "" ||
		got.GrantRevision != 0 || got.PayerGrantRef != "" {
		t.Fatalf("legacy scope must be empty, got %+v", got)
	}

	stoA, err := s.CreateStore(ctx.tenA.ID, "一号店", "地址A", "seed")
	must(t, err)
	updated, err := s.SetAgencyRelationScope(rel.ID, ctx.tenA.ID, SetAgencyScopeInput{
		AllowedActions: []string{"storefront", "action", "renewal"},
		StoreScopeSet:  []string{stoA.ID},
		ValidUntil:     "2027-01-01T00:00:00Z",
		PayerGrantRef:  "pgr_9",
		ExpectRevision: 0,
		ActorPrincipal: ctx.ownA.PrincipalRef,
		Reason:         "年度代运营授权",
	})
	must(t, err)
	if updated.GrantRevision != 1 {
		t.Fatalf("revision must bump to 1, got %d", updated.GrantRevision)
	}
	if strings.Join(updated.AllowedActions, ",") != "storefront,action,renewal" ||
		len(updated.StoreScopeSet) != 1 || updated.StoreScopeSet[0] != stoA.ID ||
		updated.ValidUntil != "2027-01-01T00:00:00Z" || updated.PayerGrantRef != "pgr_9" {
		t.Fatalf("scope round trip broken: %+v", updated)
	}

	// optimistic concurrency: stale revision refused
	if _, err := s.SetAgencyRelationScope(rel.ID, ctx.tenA.ID, SetAgencyScopeInput{
		AllowedActions: []string{"storefront"}, ExpectRevision: 0,
		ActorPrincipal: ctx.ownA.PrincipalRef, Reason: "r",
	}); !errors.Is(err, ErrStaleGrantRevision) {
		t.Fatalf("stale revision = %v, want ErrStaleGrantRevision", err)
	}

	// forbidden action value refused
	if _, err := s.SetAgencyRelationScope(rel.ID, ctx.tenA.ID, SetAgencyScopeInput{
		AllowedActions: []string{"pay_everything"}, ExpectRevision: 1,
		ActorPrincipal: ctx.ownA.PrincipalRef, Reason: "r",
	}); !errors.Is(err, ErrAgencyGrantInvalid) {
		t.Fatalf("unknown allowed_action = %v, want ErrAgencyGrantInvalid", err)
	}

	// foreign store refused
	otherStore, err := s.CreateStore(ctx.tenB.ID, "B店", "", "seed")
	must(t, err)
	if _, err := s.SetAgencyRelationScope(rel.ID, ctx.tenA.ID, SetAgencyScopeInput{
		AllowedActions: []string{"storefront"}, StoreScopeSet: []string{otherStore.ID},
		ExpectRevision: 1, ActorPrincipal: ctx.ownA.PrincipalRef, Reason: "r",
	}); !errors.Is(err, ErrAgencyGrantInvalid) {
		t.Fatalf("foreign store in scope set = %v, want ErrAgencyGrantInvalid", err)
	}

	// revoked relation cannot be re-scoped
	if _, err := s.RevokeAgencyRelation(rel.ID, ctx.tenA.ID, ctx.ownA.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.SetAgencyRelationScope(rel.ID, ctx.tenA.ID, SetAgencyScopeInput{
		AllowedActions: []string{"storefront"}, ExpectRevision: 1,
		ActorPrincipal: ctx.ownA.PrincipalRef, Reason: "r",
	}); !errors.Is(err, ErrAgencyAlreadyRevoked) {
		t.Fatalf("scope on revoked relation = %v, want ErrAgencyAlreadyRevoked", err)
	}
}

func TestHasValidAgencyRelationExpiry(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	rel, err := s.CreateAgencyRelation(ctx.tenA.ID, "usr_ag1", ctx.ownA.ID, "")
	must(t, err)
	stoA, err := s.CreateStore(ctx.tenA.ID, "一号店", "", "seed")
	must(t, err)
	_, err = s.SetAgencyRelationScope(rel.ID, ctx.tenA.ID, SetAgencyScopeInput{
		AllowedActions: []string{"storefront"}, StoreScopeSet: []string{stoA.ID},
		ValidUntil: "2026-11-10T12:00:00Z", ExpectRevision: 0,
		ActorPrincipal: ctx.ownA.PrincipalRef, Reason: "r",
	})
	must(t, err)

	nowT := time.Date(2026, 11, 10, 11, 59, 0, 0, time.UTC)
	if !s.HasValidAgencyRelation(ctx.tenA.ID, "usr_ag1", nowT) {
		t.Fatal("before expiry the relation is valid")
	}
	afterT := nowT.Add(2 * time.Minute)
	if s.HasValidAgencyRelation(ctx.tenA.ID, "usr_ag1", afterT) {
		t.Fatal("after expiry the relation is dead (fail-closed)")
	}
	// unknown agent
	if s.HasValidAgencyRelation(ctx.tenA.ID, "usr_nobody", nowT) {
		t.Fatal("unknown agent is never valid")
	}
}

func TestAuthzAuditAppendAndList(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	e, err := s.AppendAuthzAudit(AuthzAuditEntry{
		TenantID: ctx.tenA.ID, ActorPrincipal: ctx.ownA.PrincipalRef, ActorRole: "org_owner",
		Action: "member_scope_change", TargetType: "member", TargetID: "mem_2",
		BeforeJSON: `{"store_scope":""}`, AfterJSON: `{"store_scope":"sto_1"}`,
		Reason: "门店交接",
	})
	must(t, err)
	if e.ID == "" || e.CreatedAt == "" {
		t.Fatalf("audit entry must be stamped: %+v", e)
	}
	// second entry, other tenant: must not leak into A's list
	_, err = s.AppendAuthzAudit(AuthzAuditEntry{
		TenantID: ctx.tenB.ID, ActorPrincipal: ctx.ownB.PrincipalRef,
		Action: "member_disable", TargetType: "member", TargetID: "mem_3",
	})
	must(t, err)

	rows, err := s.ListAuthzAudit(ctx.tenA.ID, "", "", 50)
	must(t, err)
	if len(rows) != 1 || rows[0].TargetID != "mem_2" || rows[0].Reason != "门店交接" {
		t.Fatalf("audit list broken: %+v", rows)
	}
	byTarget, err := s.ListAuthzAudit(ctx.tenA.ID, "member", "mem_2", 50)
	must(t, err)
	if len(byTarget) != 1 {
		t.Fatalf("target-filtered audit = %d, want 1", len(byTarget))
	}
}

func TestTemplateAssignmentLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	stoA1, err := s.CreateStore(ctx.tenA.ID, "一号店", "", "seed")
	must(t, err)
	stoA2, err := s.CreateStore(ctx.tenA.ID, "二号店", "", "seed")
	must(t, err)

	in := NewTemplateAssignment{
		TenantID: ctx.tenA.ID, TemplateRef: "tpl_hero", SourceVersion: "v3",
		AllowedOverrideFields: []string{"hero_title", "theme_color"},
		RequestKey:            "req_1", CreatedBy: ctx.ownA.PrincipalRef,
	}
	ta1, _, err := s.CreateTemplateAssignment(withStore(in, stoA1.ID))
	must(t, err)
	if ta1.ID == "" || ta1.Status != "distributed" || ta1.AllowedOverrideFields[0] != "hero_title" {
		t.Fatalf("assignment row broken: %+v", ta1)
	}

	// same request_key retry = replay (no duplicate row)
	replay, replayed, err := s.CreateTemplateAssignment(withStore(in, stoA1.ID))
	must(t, err)
	if !replayed || replay.ID != ta1.ID {
		t.Fatalf("idempotent replay broken: replayed=%v id=%s want %s", replayed, replay.ID, ta1.ID)
	}

	// same key different store = new row
	ta2, _, err := s.CreateTemplateAssignment(withStore(in, stoA2.ID))
	must(t, err)
	if ta2.ID == ta1.ID {
		t.Fatal("another store is another distribution")
	}

	// reserved fields refused (门店目标/账号/二维码/Wi-Fi 密码永不跨店复制)
	for _, bad := range []string{"store_target", "account", "qr_code", "wifi_password"} {
		if _, _, err := s.CreateTemplateAssignment(withStore(in, stoA2.ID, bad)); !errors.Is(err, ErrTemplateReservedField) {
			t.Fatalf("reserved field %s = %v, want ErrTemplateReservedField", bad, err)
		}
	}

	// foreign tenant store refused
	stoB, err := s.CreateStore(ctx.tenB.ID, "B店", "", "seed")
	must(t, err)
	if _, _, err := s.CreateTemplateAssignment(withStore(in, stoB.ID)); err == nil {
		t.Fatal("foreign store distribution must be refused")
	}

	// status transitions: distributed -> applied (with revision), then terminal
	up, err := s.UpdateTemplateAssignmentStatus(ta1.ID, ctx.tenA.ID, "applied", "rev_7", ctx.ownA.PrincipalRef)
	must(t, err)
	if up.Status != "applied" || up.AppliedRevision != "rev_7" {
		t.Fatalf("apply broken: %+v", up)
	}
	if _, err := s.UpdateTemplateAssignmentStatus(ta1.ID, ctx.tenA.ID, "cancelled", "", ctx.ownA.PrincipalRef); !errors.Is(err, ErrTemplateStatus) {
		t.Fatalf("apply is terminal = %v, want ErrTemplateStatus", err)
	}
	up2, err := s.UpdateTemplateAssignmentStatus(ta2.ID, ctx.tenA.ID, "cancelled", "", ctx.ownA.PrincipalRef)
	must(t, err)
	if up2.Status != "cancelled" {
		t.Fatalf("cancel broken: %+v", up2)
	}

	// listing: tenant-scoped + optional store filter
	all, err := s.ListTemplateAssignments(ctx.tenA.ID, "")
	must(t, err)
	if len(all) != 2 {
		t.Fatalf("tenant list = %d, want 2", len(all))
	}
	one, err := s.ListTemplateAssignments(ctx.tenA.ID, stoA1.ID)
	must(t, err)
	if len(one) != 1 || one[0].ID != ta1.ID {
		t.Fatalf("store filter broken: %+v", one)
	}
	// tenant B sees nothing of A
	none, err := s.ListTemplateAssignments(ctx.tenB.ID, "")
	must(t, err)
	if len(none) != 0 {
		t.Fatalf("cross-tenant leak: %+v", none)
	}
	// masked read for foreign tenant
	if _, err := s.GetTemplateAssignment(ta1.ID, ctx.tenB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign read = %v, want ErrNotFound", err)
	}
}

func TestOpsGrantLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	g, err := s.CreateOpsGrant(NewOpsGrant{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_ops1",
		StoreScopeSet: []string{}, Commands: []string{"ops:inspect"},
		IssuedByService: "svc_ops_console", ExpiresAt: "2027-01-01T00:00:00Z",
	})
	must(t, err)
	if g.ID == "" || g.RevokedAt != "" {
		t.Fatalf("ops grant row broken: %+v", g)
	}

	nowT := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	valid, err := s.ListValidOpsGrants("usr_ops1", ctx.tenA.ID, nowT)
	must(t, err)
	if len(valid) != 1 || valid[0].ID != g.ID {
		t.Fatalf("valid grants = %+v", valid)
	}

	// other principal / other tenant: nothing
	if n, _ := s.ListValidOpsGrants("usr_else", ctx.tenA.ID, nowT); len(n) != 0 {
		t.Fatalf("cross-principal leak: %+v", n)
	}
	if n, _ := s.ListValidOpsGrants("usr_ops1", ctx.tenB.ID, nowT); len(n) != 0 {
		t.Fatalf("cross-tenant leak: %+v", n)
	}

	// bad command refused
	if _, err := s.CreateOpsGrant(NewOpsGrant{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_ops1",
		Commands: []string{"billing:manage_payer"}, IssuedByService: "svc",
	}); err == nil {
		t.Fatal("non-ops command must be refused (ops dimension only carries ops:*)")
	}
	if _, err := s.CreateOpsGrant(NewOpsGrant{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_ops1",
		Commands: []string{}, IssuedByService: "svc",
	}); err == nil {
		t.Fatal("empty commands must be refused")
	}

	// revoke = tombstone, immediately invisible, double revoke conflict
	rev, err := s.RevokeOpsGrant(g.ID, "svc_ops_console", nowT)
	must(t, err)
	if rev.RevokedAt == "" || rev.RevokedBy != "svc_ops_console" {
		t.Fatalf("revoke trail missing: %+v", rev)
	}
	if n, _ := s.ListValidOpsGrants("usr_ops1", ctx.tenA.ID, nowT.Add(time.Second)); len(n) != 0 {
		t.Fatal("revoked grant must be invisible immediately")
	}
	if _, err := s.RevokeOpsGrant(g.ID, "svc_ops_console", nowT); !errors.Is(err, ErrOpsGrantAlreadyRevoked) {
		t.Fatalf("double revoke = %v, want ErrOpsGrantAlreadyRevoked", err)
	}
}

func TestMemberPermissionGrantSetLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	staff, err := s.CreateMember(ctx.tenA.ID, "usr_staff_x", "staff", "店员", "test", true)
	must(t, err)

	// grant: 空集 → [storefront, action]
	set, err := s.SetMemberPermissionGrants(ctx.tenA.ID, staff.ID, []string{"storefront:configure", "action:configure"}, ctx.ownA.PrincipalRef, "门店帮工需要改首页")
	must(t, err)
	if len(set) != 2 {
		t.Fatalf("grant set = %v, want 2 perms", set)
	}
	if got := s.ListMemberPermissionGrants(ctx.tenA.ID, staff.ID); len(got) != 2 {
		t.Fatalf("active grants = %v, want 2", got)
	}

	// full replace: 撤一个留一个 → 撤销墓碑 + 新授权并存,可回查
	set, err = s.SetMemberPermissionGrants(ctx.tenA.ID, staff.ID, []string{"action:configure", "action:manage_deal"}, ctx.ownA.PrincipalRef, "改为动作配置")
	must(t, err)
	if len(set) != 2 || set[0] != "action:configure" {
		t.Fatalf("replaced set = %v", set)
	}
	// audit row with before/after sets
	rows, err := s.ListAuthzAudit(ctx.tenA.ID, "member", staff.ID, 50)
	must(t, err)
	sawSet := false
	for _, e := range rows {
		if e.Action == "member_permissions_set" && e.Reason == "改为动作配置" {
			sawSet = true
			if e.BeforeJSON == "" || e.AfterJSON == "" || e.BeforeJSON == "null" {
				t.Fatalf("permission set audit must carry before/after sets: %q %q", e.BeforeJSON, e.AfterJSON)
			}
		}
	}
	if !sawSet {
		t.Fatal("member_permissions_set audit missing")
	}

	// non-staff target refused
	owner2, err := s.CreateMember(ctx.tenA.ID, "usr_owner2", "org_owner", "老板2", "test", true)
	must(t, err)
	if _, err := s.SetMemberPermissionGrants(ctx.tenA.ID, owner2.ID, []string{"storefront:configure"}, ctx.ownA.PrincipalRef, "r"); !errors.Is(err, ErrMemberGrantInvalid) {
		t.Fatalf("non-staff grant = %v, want ErrMemberGrantInvalid", err)
	}

	// non-whitelisted permission refused (billing/ops never via this surface)
	if _, err := s.SetMemberPermissionGrants(ctx.tenA.ID, staff.ID, []string{"billing:view"}, ctx.ownA.PrincipalRef, "r"); !errors.Is(err, ErrMemberGrantInvalid) {
		t.Fatalf("billing grant = %v, want ErrMemberGrantInvalid", err)
	}
	if _, err := s.SetMemberPermissionGrants(ctx.tenA.ID, staff.ID, []string{"ops:inspect"}, ctx.ownA.PrincipalRef, "r"); !errors.Is(err, ErrMemberGrantInvalid) {
		t.Fatalf("ops grant = %v, want ErrMemberGrantInvalid", err)
	}

	// foreign tenant member masked
	staffB, err := s.CreateMember(ctx.tenB.ID, "usr_staff_b", "staff", "B店员", "test", true)
	must(t, err)
	if _, err := s.SetMemberPermissionGrants(ctx.tenA.ID, staffB.ID, []string{"storefront:configure"}, ctx.ownA.PrincipalRef, "r"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant grant = %v, want ErrNotFound", err)
	}

	// tenant-wide grant map for member list join
	m := s.ListTenantPermissionGrants(ctx.tenA.ID)
	if len(m[staff.ID]) != 2 {
		t.Fatalf("tenant grant map = %v", m)
	}
}

func withStore(in NewTemplateAssignment, storeID string, extraFields ...string) NewTemplateAssignment {
	out := in
	out.StoreID = storeID
	out.AllowedOverrideFields = append(append([]string{}, in.AllowedOverrideFields...), extraFields...)
	return out
}
