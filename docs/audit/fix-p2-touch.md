# Phase 2 · Touch 批次（fix-p2-touch）加固报告

- 分支：`fix/p2-touch-20261122`（从 `origin/main` = `d2096866` 切出）
- 基线：main `d2096866`，tsc 0 / vitest 1258 全过 / build 0 / 对比度 0 failures。
- 范围：仅 touch-engine 仓库；按条目分 commit；不改路由、不改数据契约。
- 日期：2026-11-22。

## 一、三件状态总览

| 条目 | 状态 | 说明 |
|------|------|------|
| 1. `tk-*` 旧皮肤收敛 | ✅ 完成 | 单层 token 别名改走 shadcn/ui 语义层；类名全部保留，取值来源统一；138 对比度对 before/after 0 差异。 |
| 2. admin 壳接线 | ✅ 完成 | Sidebar / Command / 待办 Badge 接入 `admin/page.tsx`；恢复 `admin-shell-primitives.test.ts`，4 例全过。 |
| 3. 22 处原生 select | 🟡 partial | 迁移 5 处 / 4 文件；保留 69 处 / 17 文件，逐条见第四节清单与原因。 |

门禁最终数字：

| 门禁 | 结果 |
|------|------|
| `npx tsc --noEmit` | 0 错误 |
| `vitest run` | 129 文件 / **1262 测试全过**（基线 1258；+4 为恢复的 admin-shell 证据用例），新增失败 0 |
| `npm run build` | 退出 0（路由表全量编译通过） |
| `scripts/contrast-gate.mjs` | 138 对检查 / **0 failures** |
| 双视口截图 | `/admin/preview` 1280×900、390×844 各 1 张；`/t/[code]`、`/c/[code]` 无 fixture，记 SKIP（见第六节） |

## 二、`tk-*` 旧皮肤收敛

### 2.1 测量口径（本批重扫）

- `web/src/app/touch.css` 内 `.tk-*` 选择器：111 条；
- 代码（tsx/ts）中出现的不同 `.tk-*` 类：**109** 个；类名 token 合计 **2314** 处，分布 **76** 个文件；
- `--tk-*` 自定义属性：定义 15 个，经 `var()` 引用 14 个。

> 审计基线记录为 110 类 / 143 规则行 / 73 tsx / 2312 token；本批重扫口径含 admin 面与内联样式，数字略有出入，以本表实测为准。

### 2.2 映射表（调色板别名 → shadcn/ui 语义 token）

旧别名直连 `--pn-*` 叶子；新别名先经 shadcn/ui 语义层（`globals.css` 的 `body, [data-pn-surface]` 块）再落到同一 `--pn-*` 叶子。两套皮肤因此共享同一调色板来源。

| legacy `--tk-*` | 旧直连 `--pn-*` role | 新 shadcn 语义 token | 亮（admin.compact/light） | 暗（admin.compact/dark） | 取值差异 |
|-----------------|----------------------|----------------------|--------------------------|--------------------------|----------|
| `--tk-bg` | `bg.page` | `--background` | `#f7f8f5` | `#141b1d` | 无 |
| `--tk-surface` | `bg.surface` | `--card` | `#ffffff` | `#1b2427` | 无 |
| `--tk-border` | `border.default` | `--border` | `#dce2da` | `#313a45` | 无 |
| `--tk-ink-main` | `text.primary` | `--foreground` | `#192421` | `#e6eaef` | 无 |
| `--tk-ink-sub` | `text.muted` | `--muted-foreground` | `#52615b` | `#a4b0bc` | 无 |
| `--tk-brand` | `brand.accent` | `--primary` | `#6cebdc` | `#4f8cff` | 无 |
| `--tk-bg-subtle` | `bg.sunken` | `--muted` | `#edf1ee` | `#101618` | 无 |
| `--tk-warning` | `status.warning.fg` | `--warning` | `#854500` | `#fcd34d` | 无 |
| `--tk-danger` | `status.danger.fg` | `--destructive` | `#b42336` | `#fda4af` | 无 |
| `--tk-ok` | `status.success.fg` | `--success` | `#12613d` | `#6ee7b7` | 无 |

**映射 10 个 token。** 取值等价性：shadcn 语义块在 `globals.css` 中本身就映射到与旧桥相同的 `--pn-*` 叶子（如 `--primary: var(--pn-brand-accent)`），因此别名换层后解析结果逐色一致；脚本比对 138 个对比度对的 fg/bg/ratio，**before/after 差异 0**。

### 2.3 保留清单（不硬映射）

| legacy `--tk-*` | 保留直连 | 保留原因 |
|-----------------|----------|----------|
| `--tk-ink-2` | `--pn-text-secondary` | shadcn/ui 语义集无「次级正文」槽位；映射到 `--secondary-foreground` 会错取 text-primary，禁止硬套。 |
| `--tk-brand-text` | `--pn-brand-text` | 品牌链接文字色，与 `--primary`（品牌底色）不是同一角色；无对应槽位。 |
| `--tk-warning-bg` | `--pn-status-warning-weak` | 警告浅底；shadcn 无 weak-fill 槽位。 |
| `--tk-display` | `--pn-type-display-family` | 字体族，非颜色角色。 |
| `--tk-sans` | `--pn-type-body-family` | 字体族，非颜色角色。 |

**保留 5 个 token。**

### 2.4 类名与命名债

- 所有 `.tk-*` 类名在 DOM 中保持不变，既有 DOM 断言（如 `tk-button`/`tk-quiet`/`tk-unknown`/`tk-danger`/`tk-ok` 等源码与样式断言）不受影响。
- 布局/私有语义类（`tk-page`、`tk-card`、`tk-row`、`tk-gap`、`tk-grid-*`、`tk-mt-*`、`tk-fs-*`、`tk-check`、`tk-form` 等）不承载颜色角色，未做 token 映射，保留为结构类。
- 结论：**命名债仍在，调色债已清**。后续可在外壳化批次中逐步把结构类替换为 Tailwind/shadcn 工具类。

### 2.5 对比度 before/after

- 门禁脚本：`web/scripts/contrast-gate.mjs`（读 `resolved.json` 自算 WCAG 比值，文本 ≥4.5 / 非文本 ≥3）。
- before：138 对，0 failures；after：138 对，0 failures；逐对 fg/bg/ratio 差异 0。
- 抽样（admin.compact/light）：`text.primary.on.bg.page` 14.973、`text.secondary.on.bg.page` 7.264、`text.muted.on.bg.surface` 6.522，前后一致。
- 原始记录：`/tmp/painuo-hardening2/touch/phase2/contrast-before.json`、`contrast-after.json`、`contrast-final.json`。

## 三、admin 壳接线

### 3.1 接线前后

`AdminShell` 组件本就预留 `scopes` / `tenantId` / `todoCount` props 并在有 `scopes` 时渲染 `AdminSidebar`；`MerchantWorkbench` 本就预留 `onTodoCount` 回调（上抛 `/api/workbench` 返回的 `my_tasks.length`）。缺口在页面未回传。

| 位置 | 接线前 | 接线后 |
|------|--------|--------|
| `app/admin/page.tsx` → `AdminShell` | 仅传 `nickname` / `sessionRole` / `onLogout` / `onTenantChange` | 补传 `tenantId={tenantId}`、`scopes={scopes}`、`todoCount={todoCount}` |
| `app/admin/page.tsx` → `MerchantWorkbench` | 未接待办回调 | 补 `onTodoCount={setTodoCount}` |
| 页面状态 | 无待办计数 | 新增 `const [todoCount, setTodoCount] = useState(0)` |

- 侧栏分组导航（概览 / 经营 / 分析与服务）、`OrgSwitchCommand` 组织切换、待办 `Badge` 均由既有组件渲染；本批不改权限判断、不改路由、不改数据契约。

### 3.2 证据用例恢复

- 恢复 `web/tests/admin-shell-primitives.test.ts`（原 commit `8c87b37`，Phase 0 因页面未接线被剔除）。
- 覆盖：Command 原语导出（Input/List/Group/Item/Empty、listbox/option role）；组织切换只在受控成员列表内搜索并回传选中租户；侧栏分组渲染与待办 Badge；AdminShell 透传成员列表与待办计数。
- 结果：4 例全过。

## 四、原生 select 清单（migrated / retained）

全仓实测：`<select>` 共 **74** 处，分布 **22** 个文件。本批迁移 **5** 处 / 4 文件；保留 **69** 处 / 18 文件。

### 4.1 Migrated（已换 `components/ui` Select）

| # | 文件:行 | 受控状态 | 说明 |
|---|---------|----------|------|
| 1 | `src/app/c/[code]/customer-publish.tsx:226` | value/onChange → value/onValueChange | 发布平台下拉；`Label` 包裹保留。 |
| 2 | `src/components/admin/ExtraJumpPanel.tsx:133` | 同上；`disabled={!owner}` 保留 | 活动跳转候选；空值选项改为 `SelectValue placeholder`。 |
| 3 | `src/components/admin/MerchantWorkbench.tsx:318` | intent 枚举下拉 | 内容任务意图；`onValueChange` 内做类型断言。 |
| 4 | `src/components/admin/MerchantWorkbench.tsx:522` | 活动选择联动 draft | 选中活动后回填标题/简介。 |
| 5 | `src/components/publish/CampaignPublishFlow.tsx:10` | 平台下拉 | 三屏流平台选择；受控契约不变。 |

迁移约束：未使用 `name` 属性的原生表单提交依赖；未新增隐藏 input（现有调用点均为受控状态，无 name 提交契约）。

### 4.2 Retained（保留原生 select）

#### A. 目录自足纪律测试锁定 —— 1 处

| 文件:行 | 锁定依据 |
|---------|----------|
| `src/components/publish/PublishHandoff.tsx:304` | `tests/publish-handoff-mount.test.ts:71-75` 断言该组件 import 仅允许 `react` 与 `./state`；引入 `@/components/ui/select` 会破坏自足目录纪律。已回退迁移。 |

#### B. Phase 0 延后工作面板（业务守卫/状态机与外壳交织，需后端可运行时对齐）—— 47 处

| 文件 | 处数 | 行号 |
|------|------|------|
| `src/app/work/materials/page.tsx` | 1 | 50 |
| `src/app/work/rewards/page.tsx` | 5 | 294, 563, 690, 710, 770 |
| `src/app/work/stores/[id]/services/page.tsx` | 4 | 300, 312, 340, 441 |
| `src/app/work/stores/[id]/channels/page.tsx` | 2 | 396, 439 |
| `src/components/work/touchpoints/touchpoints-panel.tsx` | 12 | 607, 616, 625, 640, 654, 659, 712, 720, 730, 833, 843, 867 |
| `src/components/work/store-content-drawer.tsx` | 6 | 446, 483, 490, 501, 519, 528 |
| `src/components/work/analytics/analytics-view.tsx` | 6 | 155, 169, 194, 219, 233, 250 |
| `src/components/work/campaign-config-panel.tsx` | 1 | 448（带 `name="benefit_kind"`） |
| `src/components/work/campaign-create-wizard.tsx` | 1 | 277 |
| `src/components/work/campaign-share.tsx` | 1 | 127 |
| `src/components/work/service-desk/service-desk.tsx` | 2 | 588, 647 |
| `src/components/work/service-desk/participation-card.tsx` | 2 | 152, 229 |
| `src/components/work/settings/settings-desk.tsx` | 2 | 409, 561 |
| `src/components/work/storefront/storefront-page-desk.tsx` | 2 | 423, 435 |

原因：与 Phase 0 裁决 #10–#23 同批延后；这些面板的守卫/恢复路径/请求模型尚不能在无后端情况下对齐，外壳（含 select 原语）化随工作面板整体改造进行。

#### C. ops 控制台 —— 4 处

| 文件:行 | 说明 |
|---------|------|
| `src/components/ops/operations-desk.tsx:311` | 工单严重程度；`data-field="ticket-severity"`。 |
| `src/components/ops/operations-desk.tsx:353` | 演练类型；`data-field="drill-kind"`。 |
| `src/components/ops/operations-desk.tsx:359` | 演练结果；`data-field="drill-result"`。 |
| `src/components/ops/tenants-desk.tsx:129` | 租户台筛选项。 |

原因：ops 面壳层未纳入本批范围；`tests/ops-session.test.ts` 按 `data-field`/`data-state` 断言 DOM，未按 `select` tagName 锁定，但随 ops 面整体改造统一迁移更稳妥。

#### D. admin 高密度表单 —— 17 处

`src/app/admin/page.tsx`：941, 973, 988, 995, 1002, 1008, 1014, 1087, 1128, 1134, 1139, 1146, 1178, 1185, 1192, 1242, 1248。

原因：本批仅完成 admin 壳接线（第三节）；17 处表单下拉随下一轮 admin 外壳化统一迁移。已核实：无 vitest 用例按 `<select>` tagName 或 `option` role 断言本页；e2e `selectOption` 锁定的门店/活动创建流（`10-state-scenarios.spec.ts:52`、`12-campaign-creation.spec.ts:17`、`15-commercial-jobs.spec.ts:35`）均落在 B 类延后面板，不在本批迁移面内。

### 4.3 测试锁定核查结论

- vitest/jsdom：未发现按 `container.querySelector("select")`、`tagName === "SELECT"` 或 `role="option"` 锁定下拉控件的用例；既有源码断言均针对组件 import 边界、文案与状态机。
- e2e：3 处 `selectOption`（见上）对应延后工作流，保留原生不影响本批门禁。

## 五、commit 清单（分支 `fix/p2-touch-20261122`）

1. `refactor(touch): P2 tk-* 调色板别名收敛至 shadcn/ui 语义层（取值不变）`
2. `feat(touch): admin 壳接线——分组侧栏/Command/待办 Badge 接入 admin 页面`
3. `refactor(touch): 5 处受控 select 迁移至 ui/Select`

## 六、双视口核查记录

| 路由 | 1280×900 | 390×844 | 备注 |
|------|----------|---------|------|
| `/admin/preview`（`TOUCH_ECO_NAV_PREVIEW=1`） | ✅ 截图 `admin-preview-1280x900.png` | ✅ 截图 `admin-preview-390x844.png` | 页面自含 fixture（AccountSeparation 预览数据）；浅色主题、卡片/Badge/按钮渲染正常，未见调色跳变。 |
| `/t/[code]` | ⏭ SKIP | ⏭ SKIP | 无参与记录 fixture：`/t/demo123` 返回 404；未伪造数据。 |
| `/c/[code]` | ⏭ SKIP | ⏭ SKIP | 无参与记录 fixture：`/c/demo123` 仅停留在 loading/error 态；未伪造数据。 |

截图目录：`/tmp/painuo-hardening2/touch/phase2/`。

## 七、blocked 项与解锁条件

| 项 | 状态 | 解锁条件 |
|----|------|----------|
| 工作 9 页/面板的 shadcn 外壳接线（Phase 0 延后 #11–#23、#15 门店 Dialog、#23 二维码 Sheet、#17 配置 Tabs、#14 奖励 Tabs、#10 今日台 Card 列表） | 仍 blocked | 需有后端/可运行时环境，逐层对齐 main 守卫与 topic 外壳后接线。 |
| `tests/work-primitives.test.ts`（Dialog/Sheet/Tabs 接线断言） | 仍剔除 | 随上述工作面板外壳接线一并恢复。 |
| admin 17 处表单 select 与 B/C 类 retained select | 本批 partial | 随对应页面外壳化批次迁移；迁移时保留受控值/onChange 契约，空值选项用 `SelectValue placeholder`。 |
| 工作面板内 select 的 Radix 迁移 | 未启动 | 同上 B 类解锁条件。 |

## 八、产物路径

- 本报告：`docs/audit/fix-p2-touch.md`
- 机器可读结果：`/tmp/painuo-hardening2/touch/phase2-fix.json`
- 截图：`/tmp/painuo-hardening2/touch/phase2/admin-preview-1280x900.png`、`admin-preview-390x844.png`
- 对比度原始记录：`/tmp/painuo-hardening2/touch/phase2/contrast-before.json`、`contrast-after.json`、`contrast-final.json`
