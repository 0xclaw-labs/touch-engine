// authz_access.go: HUI-4593 连锁、代理与权限 —— 四类动作权限维度与
// EffectiveAccess 裁决(B11 公共权限契约)。
//
// 分层纪律(承接 authz.go):
//   - 本包是纯裁决核:不触库、不看 HTTP。调用方(HTTP 层)负责把可信来源的
//     行(成员行/激活代管关系/受信 ops grant)解析成本包输入;请求头/参数/体
//     永不成为权威。
//   - 导出给 HUI-2222(登录/回跳)与 HUI-4592(运营操作)消费的稳定面:
//     ResolveAccess + Can + CanManageBrandSettings + Permission 常量。
//     业务票调用统一裁决,不各自写 role==owner 放行。
//
// 冻结矩阵(裁定书):
//   - storefront(首页配置)/action(动作配置): org_owner 全门店、store_manager
//     本店、agent 仅 grant 内动作且限 store_scope_set;staff(新旧两种)一律无;
//   - billing(订阅账单): org_owner;agent 仅 grant 含 renewal 时 billing:view,
//     代付资格需 payer_grant_ref 非空(最终扣费仍由公共 payer 权威裁决);
//   - ops(平台运营): 商家角色恒为零 —— org_owner ≠ 平台管理员;ops 命令只认
//     受信服务端 grant 行(签发在 HTTP 层受信服务生产者缝,本包只校验
//     有效期/撤销/主体/门店范围/命令)。
package authz

import "time"

// Permission is a frozen B11 permission string ("<dimension>:<command>").
// 改名即破坏消费方(HUI-4584/4591/4592/2222)——契约表冻结,只增不改。
type Permission string

const (
	PermStorefrontConfigure Permission = "storefront:configure"  // 首页/品牌展示配置(含白标授权开关判定)
	PermActionConfigure     Permission = "action:configure"      // 动作配置(HUI-4584 action 目录)
	PermActionChannelTarget Permission = "action:channel_target" // 渠道目标(target schema)配置
	PermActionManageDeal    Permission = "action:manage_deal"    // 商务动作:deal
	PermActionManageVoucher Permission = "action:manage_voucher" // 商务动作:voucher
	PermBillingView         Permission = "billing:view"          // 订阅/账单只读
	PermBillingManagePayer  Permission = "billing:manage_payer"  // 付款主体管理资格(资金裁决归公共 payer 权威)
)

// AgencyAllowedAction is a value inside agency_relations.allowed_actions.
// 旧关系(空)永不隐含任何新增写权;白名单外值在 store 层写入时即被拒绝。
const (
	AgencyActionStorefront    = "storefront"     // 代理代配置首页(限 store_scope_set)
	AgencyActionAction        = "action"         // 代理代配置动作(限 store_scope_set)
	AgencyActionChannelTarget = "channel_target" // 代理代配置渠道目标(限 store_scope_set)
	AgencyActionRenewal       = "renewal"        // 代理协助续费/代发起开通申请(billing:view)
)

// AgencyGrant is the caller-resolved ACTIVE agency relation scope. The HTTP
// layer builds it from agency_relations after the revoked check —— 到期判定在
// ResolveAccess 内兜底(防调用方漏检,双保险)。
type AgencyGrant struct {
	RelationRef    string
	AllowedActions []string
	StoreScopeSet  []string // 空 = 全门店
	GrantRevision  int64
	ExpiresAt      time.Time // 零值 = 不过期
	PayerGrantRef  string    // 空 = 不可代付(独立公共权威引用,本层不解释)
}

// OpsGrantView is one caller-resolved trusted ops grant row (already tombstone-
// filtered by the store layer; 本包对 RevokedAt 再兜一层防御)。
type OpsGrantView struct {
	Ref           string
	PrincipalRef  string
	TenantID      string
	StoreScopeSet []string // 空 = 全门店
	Commands      []string
	ExpiresAt     time.Time // 零值 = 不过期
	RevokedAt     time.Time // 零值 = 未撤销
}

// OpsPermission is one live ops command with ITS OWN store scope (ops grant
// 门店范围跟着 grant 走,不与角色/代理 scope 混用)。
type OpsPermission struct {
	Command       string
	StoreScopeSet []string
}

// EffectiveAccess is the resolved verdict input for one principal in one
// tenant: what this member may do, where, until when.
type EffectiveAccess struct {
	PrincipalRef string
	TenantID     string
	// Role: the underlying member role (consumers branch on it instead of
	// re-deriving from IDs; org_owner ≠ 平台管理员 stays visible here).
	Role Role
	// AllowedStoreIDs: 空 = 全门店(总部/租户级);非空 = 仅这些门店
	// (store_manager=本店;agent=grant 白名单)。
	AllowedStoreIDs []string
	// Permissions: 冻结权限串子集(商家四维度;ops 命令在 OpsPermissions)。
	Permissions []string
	// OpsPermissions: 受信 ops grant 命中的命令与各自门店范围。
	OpsPermissions []OpsPermission
	// RelationRef/GrantRevision/ExpiresAt: 激活代管关系快照(非代理为零值)。
	RelationRef   string
	GrantRevision int64
	ExpiresAt     time.Time
	// OpsGrantRefs: 命中的受信 ops grant 引用(可回查)。
	OpsGrantRefs []string
}

func hasString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// grantCoversStore: 空 scope 集 = 全门店;非空 = 白名单命中。
func grantCoversStore(scopeSet []string, storeID string) bool {
	if len(scopeSet) == 0 {
		return true
	}
	return hasString(scopeSet, storeID)
}

// ResolveAccess computes the effective access from trusted server-side rows.
// now 由调用方注入(可测);代理 grant 过期与 ops grant 过期/撤销在此 fail-closed。
func ResolveAccess(m *Member, g *AgencyGrant, ops []OpsGrantView, now time.Time) EffectiveAccess {
	a := EffectiveAccess{Permissions: []string{}, OpsPermissions: []OpsPermission{}}
	if m == nil || !m.Enabled || !m.Role.Valid() {
		return a
	}
	a.PrincipalRef = m.PrincipalRef
	a.TenantID = m.TenantID
	a.Role = m.Role

	switch m.Role {
	case RoleOrgOwner:
		a.Permissions = append(a.Permissions,
			string(PermStorefrontConfigure), string(PermActionConfigure), string(PermActionChannelTarget),
			string(PermActionManageDeal), string(PermActionManageVoucher),
			string(PermBillingView), string(PermBillingManagePayer))
	case RoleStoreManager:
		a.AllowedStoreIDs = []string{m.StoreScope}
		a.Permissions = append(a.Permissions,
			string(PermStorefrontConfigure), string(PermActionConfigure), string(PermActionChannelTarget))
	case RoleAgent:
		// agent 维度只来自显式 grant;无 grant(旧关系)= 零新增维度(fail-closed)。
		if g != nil && grantLive(g.ExpiresAt, now) {
			a.RelationRef = g.RelationRef
			a.GrantRevision = g.GrantRevision
			a.ExpiresAt = g.ExpiresAt
			a.AllowedStoreIDs = append([]string{}, g.StoreScopeSet...) // 空 = 全门店
			for _, act := range g.AllowedActions {
				switch act {
				case AgencyActionStorefront:
					a.Permissions = append(a.Permissions, string(PermStorefrontConfigure))
				case AgencyActionAction:
					a.Permissions = append(a.Permissions, string(PermActionConfigure))
				case AgencyActionChannelTarget:
					a.Permissions = append(a.Permissions, string(PermActionChannelTarget))
				case AgencyActionRenewal:
					a.Permissions = append(a.Permissions, string(PermBillingView))
					if g.PayerGrantRef != "" {
						a.Permissions = append(a.Permissions, string(PermBillingManagePayer))
					}
				}
			}
		}
	case RoleStaff:
		// 新维度一律 fail-closed:store_scope 显式建模 ≠ 隐式授权。
	}

	// ops 维度:只来自受信服务端 grant 行;主体/租户必须就是本成员。
	for _, og := range ops {
		if og.PrincipalRef != m.PrincipalRef || og.TenantID != m.TenantID {
			continue
		}
		if !opsGrantLive(og, now) {
			continue
		}
		for _, cmd := range og.Commands {
			if cmd == "" {
				continue
			}
			a.OpsPermissions = append(a.OpsPermissions, OpsPermission{Command: cmd, StoreScopeSet: og.StoreScopeSet})
		}
		a.OpsGrantRefs = append(a.OpsGrantRefs, og.Ref)
	}
	return a
}

func grantLive(expires, now time.Time) bool {
	return expires.IsZero() || now.Before(expires)
}

func opsGrantLive(og OpsGrantView, now time.Time) bool {
	if !og.RevokedAt.IsZero() && !now.Before(og.RevokedAt) {
		return false
	}
	return grantLive(og.ExpiresAt, now)
}

// IsOpsCommand reports whether the permission string is an ops command
// ("<dimension>:<command>" with dimension=ops). Ops commands only ever come
// from trusted server-side grants.
func IsOpsCommand(permission string) bool {
	return len(permission) > 4 && permission[:4] == "ops:"
}

// Can decides one permission on one store. storeID "" = 租户级记录。
//   - ops 命令:只由其 grant 的门店范围裁决(空 = 全门店);
//   - 商家维度:租户级记录只对全门店持有者真;门店记录需命中 AllowedStoreIDs。
func Can(a EffectiveAccess, permission string, storeID string) Decision {
	if IsOpsCommand(permission) {
		held := false
		for _, op := range a.OpsPermissions {
			if op.Command == permission {
				held = true
				if grantCoversStore(op.StoreScopeSet, storeID) {
					return allow()
				}
			}
		}
		if held {
			return deny(ReasonOutOfScope)
		}
		return deny(ReasonForbidden)
	}
	if !hasString(a.Permissions, permission) {
		return deny(ReasonForbidden)
	}
	if len(a.AllowedStoreIDs) == 0 {
		return allow() // 全门店(含租户级记录)
	}
	if storeID != "" && hasString(a.AllowedStoreIDs, storeID) {
		return allow()
	}
	return deny(ReasonOutOfScope)
}

// CanManageBrandSettings gates white-label/brand display settings authoring
// (白标授权开关与作用域). Brand settings are tenant-level; only the org owner
// holding storefront:configure qualifies — a host brand never grants member
// permissions, an agent never manages brand settings (HUI-2053 纪律延续).
func CanManageBrandSettings(a EffectiveAccess) bool {
	if a.Role != RoleOrgOwner {
		return false
	}
	if !hasString(a.Permissions, string(PermStorefrontConfigure)) {
		return false
	}
	// tenant-level coverage required
	return len(a.AllowedStoreIDs) == 0
}
