// Package authz is the pure server-side authorization core for touch-engine.
//
// 分层纪律(ADR-0001 D5):登录身份(identity principal)≠ 租户成员资格
// (members 行)≠ 资源授权。本包只裁决后两层;principal 的真伪由 identity
// 解析保证。公共活动页(游客)不属于本包:它是无会话只读白名单面,写操作
// 在 HTTP 层直接 401。
//
// HUI-1674 FEAT-0175 连锁多门店:租户内新增组织角色 org_owner(总部,全门店)
// 与门店作用域成员 store_manager(仅本门店)。作用域是服务端裁决事实
// (members.store_scope),客户端声明(头/参数/体)永不参与授权。
//
// HUI-1675 FEAT-0176 代理与子账号(单级代理 v1):新增代理角色 agent。代理的
// 权威不在角色本身,而在激活的代管关系行(agency_relations,HTTP 层单点回填
// Member.AgencyActive);能力 = 白名单(名下商家数据只读 + 开子账号),其余
// fail-closed。子账号 = 商家租户内的既有成员角色,不设第二套身份/权限体系。
package authz

// Role of a tenant member.
type Role string

const (
	RoleOrgOwner     Role = "org_owner"     // 连锁总部:全门店管控
	RoleStoreManager Role = "store_manager" // 门店经理:仅本门店(store_scope)
	RoleStaff        Role = "staff"         // 租户级职员(T0 语义保留)
	// RoleAgent (HUI-1675 FEAT-0176 代理与子账号): 服务商代理。成员行只是既有
	// 成员体系内的挂载点;权威来自激活的代管关系行(HTTP 层回填 AgencyActive)。
	// 能力 = 白名单:名下商家数据只读 + 开子账号;其余一律拒绝。
	RoleAgent Role = "agent"
	// LegacyOwner is the T0 role name. After migration 0004 no row stores it;
	// it survives only as an INPUT alias for RoleOrgOwner (provision/API),
	// resolved by CanonicalRole. It is NOT a valid stored role (fails closed).
	LegacyOwner Role = "owner"
)

// Valid reports whether the role is one of the canonical stored values.
func (r Role) Valid() bool {
	return r == RoleOrgOwner || r == RoleStoreManager || r == RoleStaff || r == RoleAgent
}

// CanonicalRole maps an input role to the canonical stored role. The T0 alias
// "owner" (full tenant control) maps to org_owner; unknown roles are refused.
func CanonicalRole(r Role) (Role, bool) {
	switch r {
	case LegacyOwner:
		return RoleOrgOwner, true
	default:
		if r.Valid() {
			return r, true
		}
		return "", false
	}
}

// Member is a tenant membership resolved from the members table.
type Member struct {
	ID           string // members.id
	TenantID     string
	PrincipalRef string
	Role         Role
	// StoreScope: "" = 总部/全门店;非空 = 仅该门店(store id)。只有
	// store_manager 行允许非空(写路径强制);缺 scope 的 manager 行全拒(fail-closed)。
	StoreScope string
	Enabled    bool
	// AgencyActive (FEAT-0176): 结构体字段,非库列。仅 role=agent 有意义 ——
	// 由 HTTP 层按 agency_relations 激活行单点回填;false(缺省)= 无激活代管
	// 关系,agent 能力集整体 fail-closed 关闭。
	AgencyActive bool
}

// inOwnStore reports whether the member's scope covers the record's store.
func (m Member) inOwnStore(storeID string) bool {
	return m.StoreScope != "" && storeID == m.StoreScope
}

// Action is an operation the API can authorize.
type Action string

const (
	ActionCreate              Action = "create"                // stores/campaigns/assets/links
	ActionReadList            Action = "read_list"             // own-tenant lists
	ActionReadRecord          Action = "read_record"           // own-tenant record
	ActionUpdate              Action = "update"                // campaigns incl. pause/resume/end
	ActionManageMembers       Action = "manage_members"        // org_owner only
	ActionManageStores        Action = "manage_stores"         // HUI-1674 store create/edit/enable/disable: org_owner only
	ActionExportQR            Action = "export_qr"             // HUI-1664 QR download: org_owner only
	ActionManageTags          Action = "manage_tags"           // HUI-1665 NFC tags CRUD/batch/export; HUI-1674 store_manager on own-store tags
	ActionManageCampaignRules Action = "manage_campaign_rules" // HUI-1676 FEAT-0177 活动规则 CRUD: org_owner only
	// HUI-1671: 发布奖励规则与人工证明复核。默认不放行；staff 不能复核。
	ActionManagePublishReward Action = "manage_publish_reward"
	ActionReviewPublishReward Action = "review_publish_reward"
	// HUI-1672: 活动页附加跳转（WiFi/导航/点评/企微/关注）只由总部配置。
	ActionManageExtraJumps Action = "manage_extra_jumps"
	// ActionManageAssetLib (HUI-1666 FEAT-0167 商家素材库): 素材登记/导入、池
	// 增删改、池内引用增删、候选标记 —— 管理动作限 org_owner。选择/调取不走本
	// 动作(按既有业务角色:ActionCreate/ActionReadList)。
	ActionManageAssetLib Action = "manage_asset_lib"
	// ActionManageVideoTemplates (HUI-1669 FEAT-0170 视频模板管理): 模板
	// CRUD/发布/版本/分配 —— 管理动作限 org_owner。查询不走本动作(按既有
	// 业务角色:ActionReadList/ActionReadRecord;门店经理仅本店分配查询)。
	ActionManageVideoTemplates Action = "manage_video_templates"
	// ActionManageAgency (HUI-1675 FEAT-0176 代理与子账号): 建立/解除代管
	// 关系 —— 平台/owner 类角色(org_owner)专属治理面。代理自身无此权。
	ActionManageAgency Action = "manage_agency"
	// ActionManageAgencySubaccounts (HUI-1675 FEAT-0176): 开子账号(代管理
	// 动作)与代开留痕查询。org_owner 恒可(owner 类保留最高治理权,直开的
	// 留痕不冒充代理身份);agent 仅当激活代管关系(AgencyActive)。子账号
	// 角色白名单(staff/store_manager)在处理层与存储层双重把关。
	ActionManageAgencySubaccounts Action = "manage_agency_subaccounts"
	// ActionManageContentPacks (HUI-3553 活动内容包): 建包/建版本/明确采用/
	// 阻止新分发/回流候选/制作交接/受控媒体交接铸造 —— 管理动作限 org_owner。
	// 查询不走本动作(按既有业务角色:ActionReadList/ActionReadRecord)。
	ActionManageContentPacks Action = "manage_content_packs"
)

// Deny reason codes.
const (
	ReasonNotMember   = "not_member"
	ReasonDisabled    = "member_disabled"
	ReasonCrossTenant = "cross_tenant"
	ReasonForbidden   = "forbidden"
	// ReasonOutOfScope: the record lives in the caller's tenant but outside the
	// member's store scope (another store, or tenant-level/unassigned). The
	// HTTP layer masks reads as 404 so foreign records stay invisible.
	ReasonOutOfScope = "out_of_scope"
	// ReasonAgencyInactive (FEAT-0176): the caller is an agent member whose
	// 代管关系 is not active (never established or revoked). Fail-closed:
	// the whole agent capability set is off.
	ReasonAgencyInactive = "agency_inactive"
)

// Decision is the outcome of an authorization check.
type Decision struct {
	Allowed bool
	// Reason is a stable machine-readable code for tests/logs.
	Reason string
}

func allow() Decision { return Decision{Allowed: true} }

func deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

// RecordScope describes the target record's tenancy AND its owning store.
type RecordScope struct {
	TenantID string
	// StoreID is the store the record belongs to; "" = 租户级/未绑定
	// (总部可见,门店经理不可见)。
	StoreID string
}

// Authorize decides whether member may perform action on the tenant/record.
// member must be non-nil (a resolved membership for the caller's principal).
// Guests never reach this function: the HTTP layer answers 401 before it.
func Authorize(member *Member, action Action, rec RecordScope) Decision {
	if member == nil {
		return deny(ReasonNotMember)
	}
	if !member.Enabled {
		return deny(ReasonDisabled)
	}
	if !member.Role.Valid() {
		return deny(ReasonForbidden)
	}

	// Membership is per-tenant: acting on another tenant's anything requires a
	// membership row in THAT tenant. Without it: cross-tenant refusal. This
	// check precedes store scope (a foreign tenant's row is not even scoped).
	if rec.TenantID != "" && rec.TenantID != member.TenantID {
		return deny(ReasonCrossTenant)
	}

	switch action {
	case ActionManageMembers, ActionManageStores, ActionExportQR, ActionManageCampaignRules,
		ActionManagePublishReward, ActionReviewPublishReward, ActionManageExtraJumps,
		ActionManageAssetLib, ActionManageVideoTemplates, ActionManageAgency,
		ActionManageContentPacks:
		// 总部专属治理面。store_manager 对「本店」也无此权(不可建删/改门店);
		// agent 无建立/解除代管权(建立/解除是 owner 类治理动作)。
		if member.Role != RoleOrgOwner {
			return deny(ReasonForbidden)
		}
		return allow()
	case ActionManageAgencySubaccounts:
		// FEAT-0176:开子账号。org_owner 恒可;agent 仅当激活代管关系;
		// 其余角色(含 store_manager/staff 的子账号)一律拒绝。
		switch member.Role {
		case RoleOrgOwner:
			return allow()
		case RoleAgent:
			if !member.AgencyActive {
				return deny(ReasonAgencyInactive)
			}
			return allow()
		default:
			return deny(ReasonForbidden)
		}
	case ActionManageTags:
		switch member.Role {
		case RoleOrgOwner:
			return allow()
		case RoleStoreManager:
			if member.inOwnStore(rec.StoreID) {
				return allow()
			}
			return deny(ReasonOutOfScope)
		default:
			return deny(ReasonForbidden)
		}
	case ActionCreate, ActionReadList, ActionReadRecord, ActionUpdate:
		switch member.Role {
		case RoleOrgOwner, RoleStaff:
			// org_owner 看全门店;staff 保留 T0 租户级语义(存量兼容)。
			return allow()
		case RoleAgent:
			// FEAT-0176 白名单:名下商家数据只读(read_list/read_record,租户级
			// 全量;可见性由成员行+激活关系双门槛限定)。写动作不在白名单,
			// 无激活关系时连只读都关闭(fail-closed)。
			if !member.AgencyActive {
				return deny(ReasonAgencyInactive)
			}
			if action == ActionReadList || action == ActionReadRecord {
				return allow()
			}
			return deny(ReasonForbidden)
		case RoleStoreManager:
			if member.inOwnStore(rec.StoreID) {
				return allow()
			}
			return deny(ReasonOutOfScope)
		default:
			return deny(ReasonForbidden)
		}
	default:
		return deny(ReasonForbidden)
	}
}
