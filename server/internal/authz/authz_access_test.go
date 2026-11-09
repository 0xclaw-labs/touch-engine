package authz

import (
	"testing"
	"time"
)

// HUI-4593 连锁、代理与权限:四类动作权限维度 + EffectiveAccess 裁决测试。
//
// 冻结矩阵(裁定书):
//   - storefront/action → org_owner 全门店、store_manager 本店、agent 仅 grant
//     内且限 store_scope_set;staff(新旧两种)一律无(fail-closed);
//   - billing → org_owner;agent 仅 grant 含 renewal 时 billing:view,代付需
//     payer_grant_ref;store_manager/staff 无;
//   - ops → 商家角色恒为零;只认受信服务端 grant(签发方非本包裁决,本包只
//     校验有效期/撤销/命令);
//   - 默认拒绝;org_owner ≠ 平台管理员。

var (
	nowT   = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ownerX = &Member{ID: "mem_o1", TenantID: "tnt_X", PrincipalRef: "usr_o1", Role: RoleOrgOwner, Enabled: true}
	mgrS1  = &Member{ID: "mem_m1", TenantID: "tnt_X", PrincipalRef: "usr_m1", Role: RoleStoreManager, StoreScope: "sto_S1", Enabled: true}
	staffX = &Member{ID: "mem_s1", TenantID: "tnt_X", PrincipalRef: "usr_s1", Role: RoleStaff, Enabled: true}
	// 新语义:店员显式携带门店 scope(0064 起应用层允许;存量行不受影响)
	staffS1 = &Member{ID: "mem_s2", TenantID: "tnt_X", PrincipalRef: "usr_s2", Role: RoleStaff, StoreScope: "sto_S1", Enabled: true}
	agentX  = &Member{ID: "mem_a1", TenantID: "tnt_X", PrincipalRef: "usr_a1", Role: RoleAgent, Enabled: true, AgencyActive: true}
)

func TestPermissionConstantsFrozen(t *testing.T) {
	// B11 契约冻结:权限串改名将破坏 HUI-4584/4591/4592/2222 消费方。
	frozen := map[Permission]string{
		PermStorefrontConfigure: "storefront:configure",
		PermActionConfigure:     "action:configure",
		PermActionChannelTarget: "action:channel_target",
		PermActionManageDeal:    "action:manage_deal",
		PermActionManageVoucher: "action:manage_voucher",
		PermBillingView:         "billing:view",
		PermBillingManagePayer:  "billing:manage_payer",
	}
	for p, want := range frozen {
		if string(p) != want {
			t.Fatalf("permission constant drifted: %q want %q", string(p), want)
		}
	}
}

func agencyGrant(actions []string, stores []string, revision int64, expires time.Time, payerRef string) *AgencyGrant {
	g := &AgencyGrant{
		RelationRef:    "agr_1",
		AllowedActions: actions,
		StoreScopeSet:  stores,
		GrantRevision:  revision,
		PayerGrantRef:  payerRef,
	}
	if !expires.IsZero() {
		g.ExpiresAt = expires
	}
	return g
}

func TestResolveAccessOwnerFullDimensions(t *testing.T) {
	a := ResolveAccess(ownerX, nil, nil, nowT)
	for _, p := range []Permission{PermStorefrontConfigure, PermActionConfigure, PermActionChannelTarget,
		PermActionManageDeal, PermActionManageVoucher, PermBillingView, PermBillingManagePayer} {
		if !Can(a, string(p), "sto_any").Allowed {
			t.Fatalf("owner must hold %s on any store", p)
		}
	}
	if Can(a, "ops:inspect", "").Allowed {
		t.Fatal("org_owner is NEVER a platform operator (ops comes from trusted grants only)")
	}
	if len(a.AllowedStoreIDs) != 0 {
		t.Fatalf("owner AllowedStoreIDs must be empty (= all stores), got %v", a.AllowedStoreIDs)
	}
}

func TestResolveAccessStoreManagerOwnStoreOnly(t *testing.T) {
	a := ResolveAccess(mgrS1, nil, nil, nowT)
	if !Can(a, "storefront:configure", "sto_S1").Allowed {
		t.Fatal("manager configures own store")
	}
	d := Can(a, "storefront:configure", "sto_S2")
	if d.Allowed || d.Reason != ReasonOutOfScope {
		t.Fatalf("manager cross-store = out_of_scope, got %+v", d)
	}
	// tenant-level record ("") is out of scope for a scoped manager
	if d := Can(a, "action:configure", ""); d.Allowed {
		t.Fatal("scoped manager must not act on tenant-level records")
	}
	if Can(a, "billing:view", "").Allowed || Can(a, "ops:anything", "").Allowed {
		t.Fatal("manager has no billing/ops dimensions")
	}
}

func TestResolveAccessStaffFailClosed(t *testing.T) {
	// legacy tenant-level staff: business semantics untouched, zero NEW dimensions
	a := ResolveAccess(staffX, nil, nil, nowT)
	for _, p := range []string{"storefront:configure", "action:configure", "billing:view", "ops:inspect"} {
		if Can(a, p, "").Allowed {
			t.Fatalf("legacy staff must not hold %s", p)
		}
	}
	// explicit store-scoped staff: same — new dimensions stay fail-closed
	a2 := ResolveAccess(staffS1, nil, nil, nowT)
	if Can(a2, "storefront:configure", "sto_S1").Allowed {
		t.Fatal("store-scoped staff still has no configuration dimensions (explicit modeling ≠ implicit grant)")
	}
}

func TestResolveAccessAgentGrantMatrix(t *testing.T) {
	// no grant (legacy relation): only legacy whitelist outside this layer; zero new dims
	a := ResolveAccess(agentX, nil, nil, nowT)
	for _, p := range []string{"storefront:configure", "action:configure", "billing:view"} {
		if Can(a, p, "sto_S1").Allowed {
			t.Fatalf("agent without grant must not hold %s", p)
		}
	}

	// grant with storefront+action, scoped to S1+S2
	g := agencyGrant([]string{"storefront", "action"}, []string{"sto_S1", "sto_S2"}, 2, time.Time{}, "")
	a = ResolveAccess(agentX, g, nil, nowT)
	if !Can(a, "storefront:configure", "sto_S1").Allowed || !Can(a, "action:configure", "sto_S2").Allowed {
		t.Fatal("agent configures within granted stores")
	}
	if d := Can(a, "storefront:configure", "sto_S9"); d.Allowed || d.Reason != ReasonOutOfScope {
		t.Fatalf("agent outside store_scope_set = out_of_scope, got %+v", d)
	}
	if Can(a, "action:channel_target", "sto_S1").Allowed {
		t.Fatal("channel_target not in grant actions → denied even inside scope")
	}
	if Can(a, "billing:view", "sto_S1").Allowed {
		t.Fatal("renewal not granted → no billing dimension")
	}
	if a.GrantRevision != 2 || a.RelationRef != "agr_1" {
		t.Fatalf("grant revision/relation must be carried, got %+v", a)
	}

	// renewal grant → billing:view only; pay needs payer_grant_ref
	g = agencyGrant([]string{"renewal"}, nil, 3, time.Time{}, "")
	a = ResolveAccess(agentX, g, nil, nowT)
	if !Can(a, "billing:view", "").Allowed {
		t.Fatal("renewal grant implies billing:view")
	}
	if Can(a, "billing:manage_payer", "").Allowed {
		t.Fatal("payer management needs payer_grant_ref")
	}
	g = agencyGrant([]string{"renewal"}, nil, 4, time.Time{}, "pgr_9")
	a = ResolveAccess(agentX, g, nil, nowT)
	if !Can(a, "billing:manage_payer", "").Allowed {
		t.Fatal("payer_grant_ref present → billing eligibility passes (final charge decided by public payer authority)")
	}
}

func TestResolveAccessAgentExpiry(t *testing.T) {
	// expired grant: extended capabilities fail closed (same as revoked)
	expired := nowT.Add(-time.Minute)
	g := agencyGrant([]string{"storefront"}, []string{"sto_S1"}, 1, expired, "")
	a := ResolveAccess(agentX, g, nil, nowT)
	if Can(a, "storefront:configure", "sto_S1").Allowed {
		t.Fatal("expired agency grant must fail closed")
	}
	future := nowT.Add(time.Minute)
	g = agencyGrant([]string{"storefront"}, []string{"sto_S1"}, 1, future, "")
	a = ResolveAccess(agentX, g, nil, nowT)
	if !Can(a, "storefront:configure", "sto_S1").Allowed {
		t.Fatal("unexpired grant works")
	}
}

func TestResolveAccessOpsGrants(t *testing.T) {
	valid := OpsGrantView{Ref: "ops_1", PrincipalRef: "usr_o1", TenantID: "tnt_X",
		StoreScopeSet: []string{"sto_S1"}, Commands: []string{"ops:inspect"},
		ExpiresAt: nowT.Add(time.Hour)}
	a := ResolveAccess(ownerX, nil, []OpsGrantView{valid}, nowT)
	if !Can(a, "ops:inspect", "sto_S1").Allowed {
		t.Fatal("valid ops grant authorizes its command within scope")
	}
	if d := Can(a, "ops:inspect", "sto_S9"); d.Allowed || d.Reason != ReasonOutOfScope {
		t.Fatalf("ops command outside grant store scope = out_of_scope, got %+v", d)
	}
	if d := Can(a, "ops:refund", "sto_S1"); d.Allowed {
		t.Fatal("ops command not in grant commands is denied")
	}
	// expired
	expired := valid
	expired.ExpiresAt = nowT.Add(-time.Second)
	a = ResolveAccess(ownerX, nil, []OpsGrantView{expired}, nowT)
	if Can(a, "ops:inspect", "sto_S1").Allowed {
		t.Fatal("expired ops grant is dead")
	}
	// revoked grants never reach ResolveAccess (HTTP layer filters); defense in depth:
	revoked := valid
	revoked.RevokedAt = nowT.Add(-time.Second)
	a = ResolveAccess(ownerX, nil, []OpsGrantView{revoked}, nowT)
	if Can(a, "ops:inspect", "sto_S1").Allowed {
		t.Fatal("revoked ops grant is dead")
	}
	// wrong principal: grant belongs to another principal → not applied
	other := valid
	other.PrincipalRef = "usr_else"
	a = ResolveAccess(ownerX, nil, []OpsGrantView{other}, nowT)
	if Can(a, "ops:inspect", "sto_S1").Allowed {
		t.Fatal("ops grant of another principal must not apply")
	}
}

func TestResolveAccessDisabledAndUnknownRole(t *testing.T) {
	disabled := *ownerX
	disabled.Enabled = false
	a := ResolveAccess(&disabled, nil, nil, nowT)
	if len(a.Permissions) != 0 {
		t.Fatal("disabled member holds nothing")
	}
	ghost := &Member{ID: "mem_g", TenantID: "tnt_X", PrincipalRef: "usr_g", Role: Role("ghost"), Enabled: true}
	a = ResolveAccess(ghost, nil, nil, nowT)
	if len(a.Permissions) != 0 {
		t.Fatal("unknown role holds nothing")
	}
}

func TestCanUnknownPermissionDenied(t *testing.T) {
	a := ResolveAccess(ownerX, nil, nil, nowT)
	if d := Can(a, "made:up", ""); d.Allowed {
		t.Fatal("unknown permission strings are always denied")
	}
}

func TestCanManageBrandSettings(t *testing.T) {
	owner := ResolveAccess(ownerX, nil, nil, nowT)
	if !CanManageBrandSettings(owner) {
		t.Fatal("org_owner with storefront:configure manages brand settings")
	}
	mgr := ResolveAccess(mgrS1, nil, nil, nowT)
	if CanManageBrandSettings(mgr) {
		t.Fatal("store manager never manages brand settings")
	}
	// agent with storefront grant still is not brand-settings manager (brand is tenant-level)
	g := agencyGrant([]string{"storefront"}, nil, 1, time.Time{}, "")
	ag := ResolveAccess(agentX, g, nil, nowT)
	if CanManageBrandSettings(ag) {
		t.Fatal("agent never manages brand settings")
	}
}

func TestEffectiveAccessIsCopySafe(t *testing.T) {
	a := ResolveAccess(ownerX, nil, nil, nowT)
	a.Permissions[0] = "tampered"
	b := ResolveAccess(ownerX, nil, nil, nowT)
	if Can(b, "storefront:configure", "").Allowed != true {
		t.Fatal("ResolveAccess must return fresh state each call")
	}
}
