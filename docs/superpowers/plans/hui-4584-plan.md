# 实施计划｜HUI-4584 30项动作目录与门店渠道配置

- worktree: /Users/jimfu/works/派诺生态/wt-touch-4584 ｜ 分支: touch/hui-4584-action-catalog ｜ 基线: 161fadc
- 依据: Linear HUI-4584 票面 ＞ SHARED-BRIEF.md ＞ briefs/hui-4584.md（裁定书）
- 迁移号段 0043-0049（本票预计只用 0043/0044）｜ 端口 web 18341 / go 18241
- 验收命令: `cd server && go test ./...`（基线红 7 个见 LEDGER，不属本域）；`cd web && npx vitest run`

## 范围（照裁定书冻结）

1. **G1 基石**：store_actions 从 `(campaign_id,kind)` 唯一改为稳定 `action_id` 实例模型。唯一下界=「同门店同 catalog_code+platform+variant 可并存多条」（五个点评、多个关注互不覆盖）。kind 不再是唯一键。
2. **catalog（A01-A30）落 Go 常量表**：`server/internal/actioncatalog` 包，handler 端校验 catalog_code 合法性；不建目录管理 UI。
3. **M4 渠道配置页** `/work/stores/[id]/channels`：新增页面（15 页基线保留）。普通链接目标与高级权限分开展示；绝不展示 AppSecret；平台专属表单归 4589，本页只做通用列表/启停/排序/缺项提示/目标摘要/测试入口。
4. **validate 按 action 粒度**：普通链接结构校验与营销奖励场景准入分开返回；后者只留 capability_ref 引用位 + `evaluated:false`（准入真值归 3535，本票不实现）。

## 契约（字段名照裁定书逐字）

- `GET /api/v1/public/storefronts/{storeCode}`：`schema_version/store_code/storefront_revision/store{name,address,opening_hours,state}/sections[]/actions[{action_id,action_revision,catalog_code,platform,kind,variant,label,presentation,capability_ref}]/activity_cards/policy_links`。禁止 tenant 密钥与内部 scope。storefront_revision 本票恒 0（发布版本归 1676）；sections 给默认单 section。登记制开关 `FEATURE_STOREFRONT`（默认 off，双保险同仓内惯例）。
- `GET/PUT /api/v1/stores/{id}/actions`：响应 `config_revision,items[]`；PUT 带 `expected_revision`，旧版本 409 `stale_version` 且不部分写入。items 字段逐字：`action_id,catalog_code,platform,kind,variant,label,target_ref,content_binding_ref,order,enabled,revision`（另附只读补充：catalog_label/source/legacy_campaign_id，宁多勿缺）。action_id = 门店范围内随机不透明 ID（`sa_<hex24>`），不用自增。
- `POST /api/v1/actions/{actionId}/validate`：返回 `action_id,action_revision,field_errors[],method_states[],reason_codes[],reward_admission{}`。不执行发布/关注/加群/收费，不做外呼。

## 数据模型

- `0043_store_actions.sql`：
  - `store_actions(id PK, tenant_id, store_id, catalog_code CHECK(''或A[0-9][0-9]), platform, kind, variant, label, target_ref, content_binding_ref, capability_ref, presentation DEFAULT 'link', sort_order, enabled, revision, revoked, expires_at, legacy_campaign_id, legacy_kind, created_at, updated_at)` — 无 UNIQUE(campaign,kind)；无任何 catalog+platform 唯一约束。
  - `store_action_configs(store_id PK, config_revision, updated_at)` — 门店级乐观锁计数。
  - `store_action_snapshots(store_id, config_revision, payload JSON, UNIQUE(store_id,config_revision))` — 每次成功保存形成候选不可变修订（1676 发布版本将来引用）。
  - `store_storefronts(store_id PK, tenant_id, store_code UNIQUE, opening_hours, storefront_revision DEFAULT 0, ...)` — 公共门店码与门店面字段，另表不动 stores 表。
- `0044_store_actions_legacy_backfill.sql`：旧 `extra_jump_actions` (campaign_id,kind) 行 → 每行生成 action_id、catalog_code 按 kind+href 域名映射（wifi→A17、wecom→A18 variant=wecom；review→A08-A12 按 href 域名；follow→A13-A16 按 href 域名；**映射不出→catalog_code='' 待商家在 M4 明确采用，不猜选**；navigate/return 不迁，保留旧路径）、campaign_id 落 `legacy_campaign_id`（快照兼容）。带 `NOT EXISTS(legacy_campaign_id,legacy_kind)` 幂等守卫（旧行唯一映射保留）。仅迁 campaign 有 store_id 的行；无店活动行留旧表，旧行为不变。

## 权限（复用既有 authz，不新建权限体系，不加 authz.go 常量——新权限动作需求归 4593/B11）

- GET `/stores/{id}/actions`：`requireScopedAction(ActionReadRecord, {tenant, store})`（同 handleStoreGet；跨店对 store_manager 掩码 404）。
- PUT `/stores/{id}/actions`：`requireScopedAction(ActionUpdate, {tenant, store})`（org_owner/staff/本店 store_manager；agent 写面天然关闭）。
- validate：先按 actionId 解析受信 tenant/store（actionId 不是授权），再 `requireScopedAction(ActionReadRecord, {action.tenant, action.store})` 掩码 404。
- 公共 storefront：无会话白名单 GET；tenant lifecycle 非 active → 404 `unauthorized`；门店 disabled → actions 空数组 + state=disabled。

## PUT 语义（批量原子 + 幂等 + 乐观并发）

- items[] 全量目标集；单事务：重复 action_id 在同一负载内 → 400 `duplicate_action_id`（明确拒绝，不最后一项覆盖）；
- 带 action_id 的项必须已存在于本店（否则 400 `unknown_action_id`），内容变化才 bump 该项 revision；不带 action_id 的项新建（revision=1）；负载中缺失的存量项删除；
- 内容集与现态完全一致 → 幂等重放：不 bump config_revision、不写快照，返回当前态 200；
- expected_revision ≠ 当前 → 409 `stale_version`，零写入；
- 每次真实变更：config_revision+1 + 插入一条不可变 JSON 快照；
- 校验（保存与 validate 共用规则）：catalog_code 必在 A01-A30；target_ref 非空时必须公网 https（复用 `extrajump.OfficialAddress` 纯函数）；label≤100；content_binding_ref 为不透明串不解析；capability_ref 本票只存引用位。

## 任务拆分（TDD 红→绿→commit）

- **T1 actioncatalog 包**（红：actioncatalog_test.go）— `All()` 30 项（A01-A30 连续、code 唯一、label/platform/kind/variant 齐）、`Lookup/ValidCode`、`LegacyMapping(kind,href)`（wifi→A17、wecom→A18+wecom、review/follow 域名映射、映射不出返回 none、navigate/return→none）。绿后 commit。
- **T2 迁移 0043/0044 + store 层 legacy backfill**（红：store_actions_migration_test.go）— 建表列断言；预置旧行（wifi/review(大众点评域)/review(未知域)/wecom/follow(抖音域)/navigate + 无店活动行）→ `BackfillLegacyExtraJumps()` → 断言：行数、映射、navigate 未迁、无店行未迁、action_id 各自独立非自增、legacy_campaign_id 保留、二次执行幂等零新增。绿后 commit。
- **T3 store 层 CRUD**（红：store_actions_store_test.go）— EnsureStorefront 稳定码、GetStorefrontByCode；ReplaceStoreActions：新建/更新/删除混合、9 项改 1 项其余八项（含 revision）逐字段不变、重复 ID 400、未知 action_id 400、stale 零写入（ErrStaleRevision）、相同内容重放 revision 不变、快照行数=真实变更次数。绿后 commit。
- **T4 httpapi GET/PUT**（红：server_store_actions_test.go 前半）— server.go 窄注册 2 行 + config.go 窄补丁（无新 flag 于本任务）；空态 `{config_revision:0,items:[],catalog:30}`；owner PUT 建 2 项→revision=1、ID 稳定；store_manager 本店可写/他店 404；staff 可写；未登录 401；跨租户 404；重复 ID 400；unknown catalog 400；http:// 目标 400 `bad_target_ref`；409 stale；幂等重放同 revision；改 1/9 其余不变。绿后 commit。
- **T5 validate**（红：同文件后半）— 合法项：field_errors 空 + method_states 三分证据（web configured / client_launch unverified / platform_action unknown）+ reason_codes 空 + reward_admission.evaluated=false；target 缺失（enabled）→ target_missing；legacy 非 https 行 → target_not_official；禁用行 target 缺失不算错；跨租户/未知 id → 404 掩码；无会话 401。绿后 commit。
- **T6 公共 storefront**（红：同文件公共段 + config FeatureStorefront 窄补丁）— flag on：按 store_code 返回冻结字段全集、actions 只含 enabled+已映射+未撤销未过期、catalog_code='' 的 legacy 行不出现在公共面、payload 原文不含 tenant_id/target_ref/"legacy"；store disabled → state=disabled 且 actions=[]；tenant suspended → 404 unauthorized；未知码 404；flag off → 404。绿后 commit。
- **T7 web M4**（红：web/tests/store-channels.test.ts）— `src/lib/store-channels.ts` 纯函数：catalog 缺项计算、reorder 交换、buildPutBody（order 规整、expected_revision 携带）、targetSummary（host+截断路径）、validate 摘要三分证据文案（含「原生唤起未验证」「未知」「准入未判定」，绝无「关注成功/点评成功」）、stale_version 恢复文案；页面 `app/work/stores/[id]/channels/page.tsx`：列表/启停/排序/新增/保存/测试入口/高级权限分区（凭据不落本页）/loading-error-empty-retry/stale 提示；stores 列表页每行加一条「渠道动作」链接（窄补丁， census 记账不漂移——链接非 raw button/input）。绿后 commit。

## 明确不做（边界）

- 不动 extrajump 既有端点/表/行为（旧 /admin、/c 兼容面原样）；不删 navigate/return。
- 不实现 3535 准入判断、3553 内容解析、4589 平台目标表单、1676 storefront 发布版本、4593 新权限动作。
- 不动 authz.go、page-map.ts、globals.css、AdminShell 导航；web/vendor 不碰。
- target_ref 只存目标；不接收顾客自带 href（公共面根本不下发 target_ref）。

## 正反验收对照（票面）

- 正例：同店五点评+四关注并存保存互不覆盖；改一项其余不变；第二个门店同平台配置完全隔离；无活动门店可保存 Wi-Fi/关注/团购。
- 反例：重复 ID 拒绝；跨租户/伪造 store_id 拒绝；未知 catalog_code 拒绝；旧 revision 覆盖 409 零写入；普通点击/校验不建奖励、Lead、官方发布事实（validate 无写路径，测试断言相关表计数不变）。
- 兼容：旧 (campaign_id,kind) 端点响应字节级不受影响（迁移只加表不加改）；旧 navigate/return 留旧路径。
