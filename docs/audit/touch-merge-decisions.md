# Touch `shadcn-touch` 语义合并决策表（Phase 0 v2）

- 基线：`main` / `origin/main` = `0933eec`（干净，tsc 0 错误，vitest 1258 全过）。
- 集成分支：`integrate/shadcn-hardening-20261122-v2`，从 `origin/main` 逐个 `cherry-pick -x` topic 9 个 commit。
- 裁决规则：
  1. 业务逻辑 / 状态机 / 请求 / 安全 / 恢复路径 → 取 main；topic 仅把旧实现换成 shadcn 调用时，保留 main 逻辑函数在原位，只换渲染层外壳。
  2. 纯展示 JSX / className / 布局 → 取 topic，但 main 独有的入口、按钮、链接、错误分支必须回填新 JSX。
  3. 共享原语（`ui/dialog.tsx`、`ui/sheet.tsx`、`ui/input.tsx` 等）→ 取 topic（加固成果），并确认 main 调用点可编译。
  4. 无法在自动化通道内安全判断的分叉（topic 外壳与被删 main 守卫交织、无后端可运行时核验）→ 该文件整体取 main，外壳化列入人工接手，不静默选边。

## 一、逐文件裁决

| # | 文件 | 裁决 | main 独有、已保全的符号 / 路径 | topic 侧采用内容 | 备注 |
|---|------|------|-------------------------------|------------------|------|
| 1 | `web/package.json` | 合并 | 保留 main `jsdom@26.1.0`（测试 DOM 环境） | 并入 topic 的 radix 系列、cva、clsx、lucide-react、tailwind-merge、tailwindcss-animate、tailwindcss/postcss/autoprefixer | 依赖合并后 `npm ci` 校验 lockfile 同步 |
| 2 | `web/package-lock.json` | 重生成 | — | 按合并后 package.json 由 npm 重生成 | `npm ci` 退出 0 |
| 3 | `web/src/app/c/[code]/customer-publish.tsx` | 混合（main 逻辑 + topic 外壳） | `legacyState` 状态机（loading/ready/original/error）与早期返回分支；`account=""` 不收集账号声明；`original_participation_required` 错误分支；`!attempt.content_version` 确认守卫；三态主按钮（预览/确认/导出）；“其他操作与核验”详情块；main 摘要文案 | Card/CardContent、Label、Textarea、Button outline sm、shadcn select 样式；`registered_adapters` 展示说明（字段 main lib 已存在） | 剔除 topic 的账号输入、账号修订按钮、`account_label` 守卫、未接线的“唤起编辑器/授权发布”死按钮（`onClick` 为空） |
| 4 | `web/src/app/t/[code]/touch-entry.tsx` | 混合（main 逻辑 + topic 外壳） | `reloadTick` 重试入口“重新读取入口”；两段门店归属/恢复码说明文案；`data-testid="touch-entry-recovery"`；effect 依赖 `[code, reloadTick]` | Card、Alert（destructive/default）、Skeleton、MapPin/TriangleAlert 图标、`blocked` 派生变体 | — |
| 5 | `web/src/components/customer/ActivityIntro.tsx` | 混合（main 逻辑 + topic 外壳） | `promise`（权益名/描述/门店/期限/去重口径/每单限领）；`existingParticipation` + `reservationReady` 早返回；`conditionsConfirmed` 门控动作清单/核验清单/领取条件；`benefitReservationState` 闸门 | Card、Button、Alert warning 外壳 | 第三动作项文案保留 main 口径 |
| 6 | `web/src/components/customer/ContentPublish.tsx` | 混合（main 逻辑 + topic 外壳） | `participationId` prop；空 choices 回退原版本；查询串 `participation_id`；`!mine && !participationId` 不匹配守卫；effect 依赖含 `participationId` | Card、Button、Alert warning 外壳；核验口径 Alert 列表 | — |
| 7 | `web/src/components/customer/RewardStatus.tsx` | 混合（main 逻辑 + topic 外壳） | `ready = canContinueReservedParticipation(...)` 按钮/说明闸门；`benefitsHref` 权益链接；未 ready 时第三档 `rewardLine` 文案；`verifyToken` 一次性凭据流 | Card、Button outline sm 外壳 | 测试锁定 `!redeemed && ready && (` 与 `const redeemed`，已保全 |
| 8 | `web/src/app/c/[code]/public-campaign.tsx` | **main 整体** | `handleReservationRecovery`、`reservationReady` 闸门、`retryParticipationReservation`、`rewardEntry`/`rewardEntryState` 奖励闸门、`handleCancel`/`handleSwitchVersion`、`CampaignPublishFlow` 交接流、访客体验/私域/留资流 | 不采用 | topic 建立在旧生命周期模型上（旧 `PublishHandoff`、无预留名额恢复、无奖励闸门），shadcn 外壳与被删恢复/闸门路径交织；自动化通道无后端可核验，外壳化延后人工接手 |
| 9 | `web/src/components/work/merchant-session.tsx` | **main 整体** | `observeSsoAccountChanges` SSO 监听、`readScopesWithRefresh` 作用域续期、`clearWorkspace`、`resetWorkspace`/`createScopeReader`、`AgencyWorkContext` 代理角色包裹、`useSession` 全部门控 | 不采用 | topic 缺少上述会话/安全/韧性路径；其 Alert/Button/Skeleton 外壳与被删守卫交织；`shell` prop 未纳入（main `MerchantGate` 仅接 `children`） |
| 10 | `web/src/components/work/today-desk.tsx` | **main 整体** | `OperationsSummary` + `todayObligations` 请求/结果段、`storeCount`、`data-action="retry"` 重试、`WorkFrame` 包裹、`serviceDesk` 入口 | 不采用 | topic 走旧 `planToday`/`pickHandoff` + campaigns/workbench 请求模型，main 已迁移到 OperationsSummary；两套请求模型不可在无后端情况下合并 |
| 11 | `web/src/app/work/campaigns/[id]/page.tsx` | **main 整体** | `configRes` 等 main 装配守卫 | 不采用 | 同 #8/#10：topic 外壳建立在旧模型上，延后人工接手 |
| 12 | `web/src/app/work/campaigns/page.tsx` | **main 整体** | main 列表装配 | 不采用 | 同上 |
| 13 | `web/src/app/work/materials/page.tsx` | **main 整体** | `campaignId/campaignResult/store/result/returnHref/params` 装配流 | 不采用 | 同上 |
| 14 | `web/src/app/work/rewards/page.tsx` | **main 整体** | 台账请求 `ledgerRequest`、团购动作 `runDealAction`/`setOfferStatus`、券工具 `CouponTool`/`DealOfferCreateForm`/`ExternalGroupBuyTab`、价格校验 `verifyPrice`、记录授权 `recordAuth` | 不采用 | 奖励/台账业务闸门属 main 独有，topic 外壳改写不可直接替代 |
| 15 | `web/src/app/work/stores/page.tsx` | **main 整体** | main 门店装配与 `router` 流 | 不采用 | 门店 Dialog 接线（topic 外壳）延后人工接手；冒烟阶段无会话无法进入列表 |
| 16 | `web/src/components/work/analytics/analytics-view.tsx` | **main 整体** | 租户/角色重挂载守卫（`requestKey`/`layer`/`layerByKey`/`ScopedAnalyticsView`/`serviceCount`/`filterLimit`） | 不采用 | 分析面按租户/角色隔离属安全相关，不取 topic 旧视图 |
| 17 | `web/src/components/work/campaign-config-panel.tsx` | **main 整体** | `expectedVersion` 乐观锁、`resume` 续作守卫 | 不采用 | 版本守卫属业务状态机，延后外壳化 |
| 18 | `web/src/components/work/campaign-create-wizard.tsx` | **main 整体** | `check` 校验、`queryStore` 门店查询 | 不采用 | 同上 |
| 19 | `web/src/components/work/campaign-share.tsx` | **main 整体** | main 分享装配 | 不采用 | topic 结构重写与 main 守卫交织 |
| 20 | `web/src/components/work/content-pack-panel.tsx` | **main 整体** | main 素材包装配 | 不采用 | 同上 |
| 21 | `web/src/components/work/service-desk/participation-card.tsx` | **main 整体** | main 参与卡片核验/恢复展示 | 不采用 | 与 #22 同服务台域，随主面板保全 main |
| 22 | `web/src/components/work/service-desk/service-desk.tsx` | **main 整体** | `stableServiceOperation`（4 处）、`serviceMutationNeedsRecovery`（4 处）：幂等键与“结果未知”恢复路径 | 不采用 | 服务台幂等/恢复属安全与状态机核心，topic 计数为 0，整体保全 main |
| 23 | `web/src/components/work/touchpoints/touchpoints-panel.tsx` | **main 整体** | 交付流 `openDeliveryDetail/loadDeliveries/recordDeliveryEvent/submitReplace/openReplace/orders/dlvStep/intentKey` 等 | 不采用 | 触点交付/替换流程属 main 独有；二维码 Sheet 外壳延后接手 |
| 24 | `web/src/app/admin/page.tsx` | **main 整体** | `AdminWorkspace`、`reinitialize` 账号切换/工作区重置回调 | 不采用 | main 账号切换回调优先；admin 分组侧栏/Command 接线延后 |
| 25 | `web/src/lib/workspace-tenant.ts` | 采用 topic（纯增量） | main 函数体不变 | `usable` 由私有改为 `export`（供新增 `OrgSwitchCommand` 使用） | 仅可见性放开，无行为变化 |
| 26 | `web/src/lib/page-census.ts` | **main 整体** | main 路由表普查口径 | 不采用 | topic 快照按 topic 路由集合写死；合入后以 main 真实路由表重扫（见下节） |
| 27 | `web/tests/page-census.test.ts` | **main 整体** | main 路由真值断言（账本 36 条） | 不采用 | topic 断言 15 条路由，与 main 路由表不符，不采用其快照数值 |
| 28 | `web/src/components/ui/dialog.tsx` | 采用 topic | — | topic 加固原语（390 弹层裁切） | 与 topic 版本逐字节一致；main 调用点 tsc/build 通过 |
| 29 | `web/src/components/ui/sheet.tsx` | 采用 topic | — | topic 加固原语 | 同上 |
| 30 | `web/src/components/ui/input.tsx` | 采用 topic | — | topic 加固原语 | 同上 |

### 自动合并、函数级复核无 main 符号丢失的文件

`AdminShell.tsx`、`AdminSidebar.tsx`（新增）、`OrgSwitchCommand.tsx`（新增）、`ExtraJumpPanel.tsx`、`MatrixDraftButton.tsx`、`MerchantWorkbench.tsx`、`PublicEntry.tsx`（新增）、`PublishHandoff.tsx`、`AdminDataTable.tsx`、`load-error-state.tsx`、`motion-handoff-brief.tsx`、`production-panel.tsx`、`redemption-card.tsx`、`store-motion-note.tsx`、`preview-shell.tsx`、`command.tsx`（新增），以及其余 19 个 `ui/*` 原语、`tailwind.config.ts`、`tailwindcss-animate` 配置、`globals.css`/`touch.css`/`eco-top-nav.module.css` 增量样式、`lib/utils.ts`。

复核方式：对每个自动合并文件做 main vs HEAD 的顶层 `const/function` 符号差集，main 侧无符号在 HEAD 侧缺失；`npx tsc --noEmit` 0 错误、`npx vitest run` 1258 全过、`npm run build` 退出 0、对比度门禁 `scripts/contrast-gate.mjs` 无 failures。

## 二、page-census 路由真值重扫（不采用 topic 快照）

- main `page-map` 账本实际路由：**36** 条（`page-census.test.ts` 断言 `ledger).toHaveLength(36)`）。
- topic 快照：**15** 条（备注“HUI-2628 创建页 + HUI-3562 店员台 + HUI-1665 两页”），按 topic 旧路由集合写死。
- 裁决：普查库与用例整体取 main，重扫真值以 main `pageLedger()` 为准；topic 的 15 条快照数值未采用。
- 重扫结果：`tests/page-census.test.ts` 10 个用例全过；普查路由与账本一一对应、无重复、不指向组件文件、不含 `/login` 与 `/api`。

## 三、延后人工接手项（本通道未落地）

1. 工作 9 页及其面板的 shadcn 外壳化（#11–#23、#15 门店 Dialog、#23 二维码 Sheet、#17 配置 Tabs、#14 奖励 Tabs、#10 今日台 Card 列表）：需在有后端/可运行时环境下，把 main 守卫与 topic 外壳逐层对齐后再接线。
2. admin 分组侧栏 / Command 组织切换 / 待办 Badge 接入 `admin/page.tsx`：新增组件已在库内并通过编译，但 main 页面尚未接线。
3. 两份 topic 证据用例随外壳接线一并恢复：
   - `tests/work-primitives.test.ts`（Dialog/Sheet/Tabs 接线断言）；
   - `tests/admin-shell-primitives.test.ts`（Command/分组侧栏/待办 Badge 断言）。
   剔除原因：断言锁定未接线的外壳实现细节（原语使用、标签文本、接线属性），非业务契约；对应页面外壳延后期间保留会造成必然失败。
