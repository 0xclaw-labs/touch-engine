# HUI-1665 实施计划｜门店稳定触点：/t→门店首页、旧卡兼容、独立停用与换绑

worktree: ../wt-touch-1665 ｜ 分支: touch/hui-1665-store-touchpoint ｜ 基线: 1460146（wave1 六分支合流后）
裁定书: briefs/hui-1665.md（预裁定为答基线）｜ 迁移号段: 0102-0104 ｜ 端口: go 18254 / web 18354（仅手动联调用，测试不依赖）

## 裁定基线（brainstorm 结论：裁定书可答全部阻塞问题，无 NEEDS_CONTEXT）

1. **target_type 取值** = `store`（store 根，guest → /s/{storeCode}，campaign_id 可空=主推活动注记）与
   `legacy_campaign`（旧行，guest → /c/{canonical 短码}，语义逐字节不动）。票面 `storefront|legacy_campaign`
   与裁定书 `store` 的命名差，按裁定书与派遣单统一用 `store`（legacy 值两处一致）。
2. **store_code 权威** = `store_storefronts.store_code`（4584 已合入基线，`sf_`+16 hex，UNIQUE）。
   provision/rebind 到 store 目标时 `EnsureStorefront` 确保行存在（商家写面，公共解析保持纯读 fail-closed）。
3. **公共解析载荷**（GET /api/v1/public/touchpoints/{code}，FEATURE_TOUCHPOINT_ENTRY 沿用，默认 off）：
   - store 根: `{schema_version:"touchpoint.v2", state:"available", target_type:"store", store_code, binding_revision}`
   - legacy: `{schema_version:"touchpoint.v2", state:<五态>, target_type:"legacy_campaign", code:<兼容保留>, campaign_code, binding_revision}`
   - 别名停用/未知/坏码同形 404 不变；解析不消费 campaign 五态（store 目标零活动门控——无活动/暂停/结束不关闭门店基础服务）。
4. **换绑**（rebind-preview / rebind 增量契约）：入参 `{target_type, store_id?, campaign_id?, expected_binding_revision}`；
   保留旧 `{campaign_id, expected_campaign_id}` 兼容路径（增量演进不砸旧调用方）；陈旧 revision → 409 rebind_conflict；
   成功 binding_revision+1，URL（别名码）不变；已有参与/权益快照零挪动（CountStartedParticipations 只读统计不变）。
5. **provision（新记录）**：target_type=store，store_id 由标签绑定服务端派生（权威必填，标签无店 → 400）；
   campaign_id 取标签 link 的活动作注记（可空）。
6. **迁移**：
   - 0102 重建 touchpoint_aliases：campaign_id 变可空 + 新增 `target_type`（default 'legacy_campaign', CHECK 白名单）与
     `binding_revision`（default 1, CHECK ≥1）+ 索引重建 + store_id 索引。Migrate 在事务内逐文件执行（PRAGMA foreign_keys
     事务内 no-op），DROP 被引用表需 `PRAGMA defer_foreign_keys=ON`（COMMIT 时按名解析到新表，测试含 events 行锁死此路径）。
   - 0103 反查补 store_id：`UPDATE touchpoint_aliases SET store_id=(campaigns.store_id) WHERE 空 AND c.store_id 非空`
     （同租户 join）；无店活动行保持空+legacy（管理异常并列展示，不猜门店）；幂等守卫=只填空行，重放零改动（真实 Migrate 重放测试，参照 0044 先例）。
   - 0104 不使用（号段预留，无第三项 schema 需求）。
7. **旧 alias 语义保留**：legacy 行解析/停用/共享 link 传播语义零改动；单卡停用绝不级联 campaign_links（既有测试继续锁死）。

## 写面（唯一写面纪律）

- server: `internal/db/migrations/0102_*.sql`、`0103_*.sql`；`internal/store/store_touchpoints.go` + 专属测试；
  `internal/httpapi/handlers_touchpoints.go` + `server_touchpoints_test.go`；`internal/db/db_touchpoint_migration_test.go`（新）。
- web: `src/lib/touchpoints.ts`、`src/app/t/[code]/touch-entry.tsx`、`src/components/work/touchpoints/touchpoints-panel.tsx`、
  `tests/touchpoints.test.ts`、`tests/touchpoints-panel.test.ts`、`tests/touch-entry.test.ts`。
- 只读他人域：storefronts/actions（4584）、participations（3546）、nfc/tags、campaigns/links。中央文件零改动（路由已注册、开关已存在）。

## 任务序列（TDD：每任务红→绿→commit）

### T1 迁移 0102 + db 测试（db_touchpoint_migration_test.go）
- 测试先行：真实 Migrate 后 schema 断言——target_type/binding_revision 列存在且 CHECK 生效（坏值拒插）、
  campaign_id 可空（NULL campaign_id + target_type=store 可插）、既有行 default legacy_campaign/1、
  touchpoint_events 行在 rebuild 后幸存（defer_foreign_keys 路径）、索引存在。
- 0102 SQL：defer_foreign_keys → CREATE new（18 列+3 新列，全部旧约束保留）→ INSERT SELECT → DROP old →
  RENAME → 重建 idx_touchpoint_aliases_tag/tenant/campaign + 新 idx_touchpoint_aliases_store。
- 红：schema 断言失败 → 绿：0102 落地。

### T2 迁移 0103 反查补 store_id + 幂等测试
- 测试先行：Open 后手工种 4 类 alias 行（campaign→storeA 可反查 / campaign→storeB 已有 store_id 不动 /
  无店 campaign 保持空 / campaign 缺失保持空）→ DELETE schema_migrations 0103 → Migrate 重放 → 断言；
  再次重放零改动（幂等守卫）。
- 0103 SQL：UPDATE ... FROM campaigns 同租户 join，只填空 store_id。

### T3 store 层 store 根触点（store_touchpoints.go + store_touchpoints_test.go）
- 常量 `TargetStore="store"` / `TargetLegacyCampaign="legacy_campaign"`；sentinel `ErrTagWithoutStore`。
- Alias/View 增 TargetType/BindingRevision/StoreCode；touchpointViewSelect 的 campaigns INNER JOIN → LEFT JOIN
  （store 行 campaign NULL 不丢行），title/status 可空扫描。
- TouchpointRows 增 HasStore/StoreCode；LoadTouchpointRows store 分支（storefront row JOIN stores/tenants，
  缺行 fail-closed）；DecideTouchpoint 分目标类型（store：HasStore→active→redirect(StoreCode)；legacy：原逻辑零改动）；
  TouchpointResolution 增 TargetType/StoreCode/BindingRevision。
- ProvisionTouchpoint：tag 无店 → ErrTagWithoutStore；EnsureStorefront 确保码存在；target_type=store、
  binding_revision=1、campaign 注记=link campaign。
- RebindTouchpointTarget：target 校验（store→本租户店+EnsureStorefront；legacy→本租户活动+有短码）；
  guard = expected_binding_revision>0 优先，否则 expected_campaign_id 兼容；同事务 bump revision+审计
  （detail 记 target 变化）；RebindTouchpoint 旧签名保留为兼容委托。
- attachEntry store 行：EntryCode=StoreCode、EntryState=available/disabled/not_found。
- 测试：provision 派生/无店 400 哨兵/重复 409 不变；rebind 升级 legacy→store 同码 revision+1；store→legacy；
  revision 陈旧 409；跨租户店/活动 404 掩码；DecideTouchpoint store 分支三分支 + legacy 回归；单卡停用不影响他卡与共享 link。

### T4 httpapi 契约（handlers_touchpoints.go + server_touchpoints_test.go）
- 公共载荷 v2（见裁定基线 3）：TpRedirect 分 target_type 写 store_code 或 code+campaign_code；
  schema_version/binding_revision 全量携带；404 形与 off 双保险零改动；FEATURE_PUBLIC_CACHE 下
  legacy→store 换绑后立即反映（epoch）。
- provision handler：ErrTagWithoutStore→400 tag_not_store_bound；响应含 target_type/store_code/binding_revision。
- rebind-preview/rebind：入参 target_type/store_id/campaign_id?/expected_binding_revision + 旧兼容；
  preview 响应 {current,next（target 型事实）, started_participations, binding_revision}；
  越店经理 store 目标出 scope → 404 掩码；无短码 legacy 目标 400 不变。
- 测试：正例（无活动门店经 provision→公共解析 available+store_code；同卡换绑同 URL；换绑后老活动参与数不变）
  与负例（off 404、坏码 404、停用 404、跨租户 404、越店 404、陈旧 revision 409、兼容旧入参路径）。

### T5 web lib + /t 入口（touchpoints.ts / touch-entry.tsx + tests）
- `isValidStoreCode`（^sf_[0-9a-f]{16}$，镜像 Go randHex）；`touchEntryRedirect` 增 store 分支：
  target_type=store + state=available + store_code 形合法 → `/s/{store_code}`（location.replace 临时跳转不变）；
  target_type 缺失/未知但 code 合法 → 原 `/c/{code}` 兼容（旧载荷零破坏）；store_code 形非法一律不跳。
- `entryStateFace` 增 target 感知包装 `entryFace(targetType,state)`；`targetFace(targetType)` 显示文案。
- tests：store 分支白名单/注入形拒绝/legacy 兼容/文案。

### T6 M8 面改造（touchpoints-panel.tsx + tests 源契约）
- 创建：先选有权门店（stores 列表）→ 选卡（按店过滤 tag.store_id）→ 活动可选（按店过滤 campaigns）；
  服务端仍是派生权威（门店选择只作表单过滤，不作为提权输入）。
- 列表：显示目标类型/当前门店/store_code/可选活动；门店筛选下拉（服务端 store_id filter）+ 状态/活动筛选保留。
- 换绑：目标类型选择（门店/活动）→ preview（binding_revision 回显）→ 确认带 expected_binding_revision；
  409 文案保留；legacy 卡「升级为门店入口」走同一通道。
- 导出/QR/复制链接/五态/停用恢复/旧共享停用确认全保留（tagcsv 复用零改动）。
- tests：源契约（门店优先创建、expected_binding_revision、store_code 展示、target_type 展示、导出零推进不变）。

### T7 自审 + 全量验证 + 报告
- code-review：diff 自查（spec 符合性/白名单/fail-closed/不越写面）。
- `cd server && go test ./...`；`cd web && npx vitest run`；如实记录（基线 7 红已被 wave1 集成清零，应全绿）。
- 报告写 /Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-1665-report.md。不 push 不 merge。

## 验收对照（票面正反例 → 本计划覆盖）

| 票面 | 覆盖 |
|---|---|
| 无活动门店建卡→/t→C1（/s 契约） | T3/T4 公共解析 store 分支 + T5 白名单跳转 |
| 同卡换首页版本仍同 URL | T3 revision+1、码不变；T4/T6 断言 |
| 活动结束后同卡 Wi-Fi/关注/团购可用 | store 解析零 campaign 门控（T3 DecideTouchpoint） |
| 旧 /c 卡照旧进活动；code 兼容保留 | legacy 分支零改动 + T4 code/campaign_code 兼容 |
| 停用一张不影响另一张；恢复不清历史 | T3/T4 既有+新增回归 |
| 换绑不挪已有参与/权益 | 参与只读统计恒挂原活动（T4 断言） |
| 跨租户/越店/陈旧 revision/坏码/off | 404 掩码、409、404、404 双保险（T4） |
| 迁移前后旧 /t、旧/c、共享链接、坏码逐一比对 | T1/T2 真实 Migrate 重放 + legacy 回归 |

## 明确不做（他人写面）

/s 页与 C1 布局（1896）、storefront published 版本（1676）、动作 CRUD（4584）、shared 导航（2628）、
真实 NFC 写卡/回读/真机验证（1663）、authz 映射（4593）。T19 真机项如实挂账不在 API 层代签。
