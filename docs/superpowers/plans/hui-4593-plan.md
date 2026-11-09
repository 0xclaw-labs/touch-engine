# 计划｜HUI-4593 连锁、代理与权限（模板下发、成员权限、代理协作及白标授权）

- worktree: /Users/jimfu/works/派诺生态/wt-touch-4593 ｜ 分支: touch/hui-4593-authz ｜ 基线: 161fadc
- 需求权威: Linear HUI-4593 ＋ SHARED-BRIEF ＋ 裁定书 briefs/hui-4593.md（四类权限维度、代理纪律、审计要求已预裁，照办）
- 唯一写面: **authz/members 域**（server/internal/authz、agency scope、成员授权、模板分发引用、/work/settings M13）
- 迁移号段: 0064-0067 ｜ 端口: web 18345 / go 18245

## 冻结契约（本票对其他票的承诺）

### 1. 权限维度与权限串（B11 契约，冻结）

四类维度 → 权限串（`<dimension>:<command>`，authz 包常量，同包 exported 供 HUI-2222/4592 调）：

| 维度 | 权限串 | 说明 |
|---|---|---|
| storefront | `storefront:configure` | 首页/品牌展示配置（含白标授权开关的判定） |
| action | `action:configure` | 动作配置 |
| action | `action:channel_target` | 渠道目标（HUI-4584 target schema）配置 |
| action | `action:manage_deal` / `action:manage_voucher` | 商务动作（4584/COMMERCE 消费） |
| billing | `billing:view` | 订阅/账单只读 |
| billing | `billing:manage_payer` | 付款主体管理（公共资金权威独立裁决，本层只判资格） |
| ops | `ops:<command>` | 平台运营命令（命令集由 HUI-4592 定义；本层只做 grant 校验） |

角色→维度映射（冻结矩阵，默认拒绝）：

| 角色 | storefront | action | billing | ops |
|---|---|---|---|---|
| org_owner | 全门店 | 全门店 | view+manage_payer | 永不（org_owner ≠ 平台管理员） |
| store_manager | 本店 | 本店 | 无 | 永不 |
| staff（含 scope 或否） | 无（fail-closed） | 无 | 无 | 永不 |
| agent | store_scope_set 内（需 grant 含 `storefront`） | 同左（`action`） | 仅 `billing:view`（grant 含 `renewal`）；代付需 payer_grant_ref 非空 | 永不 |
| 平台管理员 | — | — | — | 仅受信服务端 ops grant（X-Service-ID 签发/校验/撤销/有效期） |

### 2. EffectiveAccess（拟新增，authz 包 exported）

```go
type EffectiveAccess struct {
    PrincipalRef   string
    TenantID       string
    AllowedStoreIDs []string   // "" 代表全门店（总部）
    Permissions     []string   // 冻结权限串全集的子集
    RelationRef     string     // 激活代管关系行 ref（非代理为空）
    GrantRevision   int64      // 代理 scope 版本（乐观并发）
    ExpiresAt       string     // 代理关系到期（RFC3339，空=不过期）
    OpsGrantRefs    []string   // 命中的受信 ops grant 引用
}
func ResolveAccess(m *Member, g *AgencyGrant, ops []OpsGrantView, now time.Time) EffectiveAccess // 纯函数
func Can(a EffectiveAccess, permission string, storeID string) Decision
```

- HTTP 面: `GET /api/v1/access/effective`（FEATURE_AUTHZ，默认 off）。
- scope 全部从可信 session + 服务端行解析；query/body 传 ID 永不参与。

### 3. 代理 additive 增量（0064 迁移列）

agency_relations 新列：`allowed_actions TEXT`（JSON 数组；NULL/空 = 旧语义：只读+开子账号）、
`store_scope_set TEXT`（JSON 数组；空 = 全门店）、`valid_until TEXT`、`grant_revision INTEGER NOT NULL DEFAULT 0`、
`payer_grant_ref TEXT NOT NULL DEFAULT ''`。

- allowed_actions 白名单值（冻结）: `storefront` / `action` / `renewal`。旧关系(NULL)不获得任何新增写权。
- 代付: `payer_grant_ref` 非空才可能过 billing 资格判定；最终扣费仍由公共 payer 权威裁决（HUI-4591）。
- 到期/撤销即失权: 回填判定改为 `revoked_at IS NULL AND (valid_until IS NULL OR valid_until > now)`，
  每请求实时回填（既有 authzMember 单点），撤销后旧 token 下一请求即 403（T17）。
- 多级代理: 子账号白名单不变（staff/store_manager），代理永不铸造 agent/org_owner。
- scope 变更: `PUT /api/v1/agency/relations/{id}/scope`（org_owner 专属；携带 grant_revision 乐观并发，STALE_VERSION 拒绝）。

### 4. 模板分发引用（0066 迁移）

`template_assignment(ref, tenant_id, store_id, template_ref, source_version, allowed_override_fields, request_key, status, applied_revision, created_by, created_at, updated_at)`
唯一索引 `(tenant_id, store_id, template_ref, source_version, request_key)` → 同请求重试幂等重放，不重复创建。
- status ∈ distributed/applied/cancelled；本票只记录分发与授权结果，**不写 storefront 版本表**（应用委托 1676 领域服务写草稿）。
- `allowed_override_fields` 拒绝保留字段（门店目标/账号/二维码/Wi-Fi 密码永不跨店复制）: `store_target`/`account`/`qr_code`/`wifi_password`。
- API: `POST /api/v1/template-assignments`（org_owner，多店逐项报告 per-store 成功/失败）、`GET /api/v1/template-assignments/{id}`、
  `GET /api/v1/template-assignments`（owner 全租户 / store_manager 本店）、`PATCH /api/v1/template-assignments/{id}`（owner：cancelled/applied_revision）。

### 5. 审计（0065 迁移）

`authz_audit_log(id, tenant_id, actor_principal, actor_member_id, actor_role, action, target_type, target_id, before_json, after_json, reason, created_at)`，append-only。
覆盖: 成员创建/改角色/停用、代理建立/撤销/scope 变更、模板下发/取消、ops grant 签发/撤销。
`reason` 在新特权写面必填（400 空 reason）；既有 /admin/members 写面 reason 可选但始终落审计行。

### 6. ops grant（0067 迁移）

`ops_grants(ref, principal_ref, tenant_id, store_scope_set, commands, issued_by_service, issued_at, expires_at, revoked_by, revoked_at)`。
签发/撤销仅走受信服务生产者缝（X-Service-ID + TOUCH_SERVICE_TOKENS，复用既有 requireServiceProducer，角色校验扩 ops_grant_manager）；
商家面（/admin/members、代理接口、用户输入）永不自授 ops grant。

### 7. 门店店员 scope 显式建模

- 0064 起，应用层校验允许 `role=staff` 的成员行显式携带 store_scope（新语义=仅本店）；
  存量 staff 行（scope=''）保持 T0 租户级语义，**绝不静默等同**。
- authz: `RoleStaff + StoreScope≠""` → 业务动作按 inOwnStore；`StoreScope==""` → 行为逐字节不变。

### 8. 白标授权

复用既有 brandctx/brandhost，本票只做授权开关与作用域：exported `authz.CanManageBrandSettings(access)`
（= org_owner 且具备 `storefront:configure`，Host Brand 永不授成员权限/选付款人——既有纪律不变）。
不做新白标渲染、不建域名/证书能力（归 2222/1663 公共权威）。

## 共享文件窄补丁清单（合流由指挥统一解）

| 文件 | 窄补丁 |
|---|---|
| server/internal/httpapi/server.go | 新增 FEATURE_AUTHZ 路由注册段（access/effective、template-assignments、ops-grants）＋ agency scope 路由 2 行 |
| server/internal/config/config.go | EnvFeatureAuthz + FeatureAuthz 字段 |
| web/src/lib/page-map.ts | 账本 +1 行：/work/settings（settings 模式） |
| web/src/lib/page-census.ts | markerBook +1 行：/work/settings（薄壳零标记） |
| web/tests/page-census.test.ts | 账本计数 15→16 |
| web/src/components/work/work-frame.tsx | LINKS +1 行：设置入口 |

## 任务拆解（TDD，逐任务红→绿→commit）

### T1 迁移 0064-0067（无行为变更，先落 schema）
- 0064_member_store_scope.sql: agency_relations 五列 additive（ALTER TABLE ADD COLUMN，SQLite 可行）；
  staff scope 无 schema 变更（列已存在），只动应用层校验 → 不在本迁移。
- 0065_authz_audit.sql: authz_audit_log 表 + 索引。
- 0066_template_assignment.sql: template_assignment 表 + 幂等唯一索引。
- 0067_ops_grants.sql: ops_grants 表 + 索引。
- 验收: db 迁移测试（表存在、旧库升级零丢失、幂等索引在位）；go test ./internal/db/...。

### T2 authz 权限核心（authz_access.go，纯函数）
- 红测先行 authz_access_test.go:
  - 冻结矩阵逐格断言（owner/manager/staff(新旧两种)/agent(五种 grant 组合)/ops grant）；
  - 默认拒绝；跨租户；代理无 grant/撤销/到期全拒（ReasonAgencyInactive）；到期边界；
  - `action:manage_deal` 等命令串不在 grant 白名单即拒；ops grant 过期/撤销即拒；
  - grant_revision 透传；EffectiveAccess.AllowedStoreIDs 语义（""=全门店）。
- 实现: Permission 常量、AgencyGrant、OpsGrantView、ResolveAccess、Can、CanManageBrandSettings。
- 验收: go test ./internal/authz/...。

### T3 store 层扩展（store_agency.go 增量 + store_authz_audit.go + store_template_assignment.go + store_ops_grants.go）
- 红测先行:
  - HasValidAgencyRelation（含 valid_until 边界；旧 NULL 行为不变）；
  - SetAgencyRelationScope（乐观并发 grant_revision；STALE 拒绝；scope 审计同事务）；
  - AppendAuthzAudit/ListAuthzAudit（append-only，前后值 JSON）；
  - CreateTemplateAssignment 幂等重放、保留字段拒绝、按店逐行；ListByTenant/ListByStore/PatchStatus；
  - CreateOpsGrant/ListValidOpsGrants（过期/撤销不返回）/RevokeOpsGrant。
- 验收: go test ./internal/store/...。

### T4 httpapi 面（handlers_access.go + handlers_agency.go 增量 + handlers_auth.go 审计化）
- FEATURE_AUTHZ 注册段（access/effective、template-assignments、ops-grants）；
  agency scope 路由挂 FEATURE_AGENCY 既有段旁（窄补丁）。
- 红测先行（server_authz_access_test.go 等，复用现有 httpapi 测试装配风格）:
  - GET /access/effective: owner/manager/店员(新旧)/agent(含 grant 与撤销后即时失权 T17)/伪造 X-Tenant；
  - 成员写面: reason 落审计、代理不能建 owner（既有 403）、agent 撤销后同会话立即 403；
  - PUT agency scope: owner 专属、STALE_VERSION、reason 必填、旧关系升级需显式；
  - template-assignments: owner 多店下发逐项报告、幂等重放不重复、保留字段 400、manager 本店只读、
    部分失败不标全部成功、PATCH cancelled；
  - ops-grants: 服务生产者签发/撤销、商家自授 401/404、过期 grant 不进 access/effective。
- 验收: go test ./internal/httpapi/...（基线红 7 个不计）。

### T5 web /work/settings（M13）
- src/app/work/settings/page.tsx（薄壳）＋ src/components/work/settings/*.tsx（成员与权限/代理协作/模板下发/白标授权四区，
  agent 角色时显示「正在为 X 工作＋权限＋付款主体」横幅）。
- 窄补丁: page-map.ts、page-census.ts、page-census.test.ts、work-frame.tsx。
- 红测先行 tests/settings-page.test.ts（源码级断言，同 service-desk-page 风格）:
  census 同步、四区结构 data-section、授权/撤回/代管操作闭环、reason 必填校验、
  权限不足 UX（403 文案不崩）、代理横幅、移动优先（无固定宽）。
- 验收: npx vitest run。

### T6 全量验证 + 自查收口
- cd server && go test ./...（记录基线红 7 个不属本域）；cd web && npx vitest run。
- 本地起服（go 18245 / web 18345）冒烟主路径（登录→设置→授权→代理→撤销→403）。
- 自我 code-review（spec 符合性＋质量）→ 修复 → finishing-branch（不 push 不 merge）。

## 正反验收对照（票面 T17 及负例）

| 场景 | 预期 |
|---|---|
| owner A 读 B（伪造 X-Tenant-ID） | 403 not_member / cross_tenant，零泄露 |
| store_manager 跨店 | 403/404 掩码 |
| staff 旧行 | T0 租户级语义不变；新维度全拒 |
| staff 带 scope 新行 | 业务动作仅本店 |
| agent 无关系/撤销/到期 | 全拒 agency_inactive；撤销后既有会话下一请求 403（T17） |
| 伪造 ops 声明（头/体） | 拒绝；ops 只认受信服务端 grant 行 |
| 代理无 payer 授权做收费动作 | billing 拒；不误扣双钱包 |
| 同模板重试下发 | 幂等重放，不重复建草稿 |
| allowed_override_fields 含保留字段 | 400 reserved_field |
| 部分门店失败 | 逐项报告，不标全部成功 |
| 撤回支持/代理授权后 | 下一请求即失败（实时回填） |
