# 碰一碰 Touch Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修通商家创建→内容→链接→发布与顾客参与→内容→发布→奖励的正常路径，并把 EcoTopNav/语义 token 接到真实权威或诚实降级。

**Architecture:** Next.js BFF（`web/src/app/api`）+ Go 单服务（`server/internal/httpapi`）；前端只打同源 `/api/*`。商家创建用分步页 + 服务端 draft + localStorage 草稿；公共页用服务端 capability 信号门控；生态导航用 `PUBLIC_AI_ECO_NAV_URL` 真实模型、blocked 时隐藏；应用切换经 web BFF → Go → public-ai shell launch，launch_url 按 vendored registry 校验。

**Tech Stack:** Next.js 15 / React 19 / TypeScript / vitest 2 / Go 1.27 / SQLite / Playwright（e2e）。

**Spec:** `docs/superpowers/specs/2026-10-21-hui-2628-touch-shell-design.md`

## Global Constraints

- 分支 `eco/hui-2628-touch-shell`，worktree `/Volumes/MacintoshHD/派诺生态/wt/touch-engine/mb-eco-touch`。
- 不 push、不部署、不改 Linear、不发券、不新增钱包、不改 public-ai 生产 manifest。
- 浏览器只访问同源 `/api/*`；页面源不出现 `style={{`、`JSON.stringify` 或裸 hex。
- 公共 `/c/*` 永不挂 `EcoTopNav/AdminShell`，永不请求商家接口。
- 每个逻辑提交带 HUI id；先 RED 后 GREEN。
- 本地真实栈端口：Go 18460、identity stub 18461、Next 18462；不硬编码生态域名。
- 生成 token 文件不可手改；只改手写 CSS 与 vendored 新文件。
- 任何失败/未知状态必须给真实原因与恢复动作；禁止用 fixture 假装成功。

## Review Focus

- 创建流第①步创建 draft 后刷新/返回：草稿必须在（localStorage + 服务端），不能重复建活动。
- 短码停用/活动结束：分享区不能给一个“看起来能用”的链接。
- 参与关闭时公共页：不能显示可点的主行动，也不能把关闭写成“已结束”。
- 商家换内容版后顾客页面：必须显示版本不一致，不能静默展示新版。
- EcoTopNav 上游缺配置/超时：登录后页面不得出现夹具品牌/租户；应用点击不得直跳 registry URL。

---

### Task 1: Server 公共能力信号、短码重试、内容版本钉死

**Files:**
- Modify: `server/internal/httpapi/handlers_business.go`（public link view + CreateLink 调用）
- Modify: `server/internal/store/store.go`（CreateLink 重试）
- Modify: `server/internal/httpapi/handlers_content_packs.go`（content-snapshot version_id）
- Test: `server/internal/httpapi/server_public_visitor_test.go`、`server/internal/httpapi/server_content_packs_test.go`、`server/internal/store/store_test.go`

**Interfaces:**
- Produces: public link JSON `{ ..., "capabilities": {"participation": bool, "content_packs": bool, "reward_ops": bool, "leads_capture": bool, "publish_reward": bool} }`
- Produces: `GET /public/links/{code}/content-snapshot?pack_id=&platform=&version_id=`；`version_id` 非空且 ≠ 当前采用版 → 409 `{error:"version_mismatch"}`。
- Produces: `store.CreateLink` 在唯一键冲突时最多重试 5 次。

- [ ] 写失败测试：public link 响应含 capabilities，且与 Config 开关一致。
- [ ] 写失败测试：content-snapshot 传旧 version_id → 409 version_mismatch；不传仍返回当前采用版。
- [ ] 写失败测试：CreateLink 碰撞（预置同 code 或注入短码生成器）→ 重试后成功/有界失败。
- [ ] 实现，跑 `GOWORK=off go test ./internal/httpapi ./internal/store -count=1`。
- [ ] Commit: `fix(hui-2628): public capability signal, pinned snapshot version, shortcode collision retry`

### Task 2: 商家创建向导 + 草稿 + 分享

**Files:**
- Create: `web/src/app/work/campaigns/new/page.tsx`
- Create: `web/src/components/work/campaign-create-wizard.tsx`
- Create: `web/src/lib/campaign-create.ts`（纯函数：草稿序列化/校验/本地时间/分享文案）
- Modify: `web/src/app/work/campaigns/page.tsx`（列表页删表单，加“新建活动”）
- Modify: `web/src/app/work/campaigns/[id]/page.tsx`（分享区、状态 stepper、单一主行动）
- Create: `web/src/components/work/campaign-share.tsx`
- Test: `web/tests/campaign-create.test.ts`、`web/tests/campaign-share.test.ts`、`web/tests/product-pages.test.ts`

**Interfaces:**
- `emptyDraft(): CampaignDraft`
- `draftErrors(draft): Record<string,string>`
- `toRFC3339Local(value: string): string`（空串→""；非法→null）
- `draftStorageKey(tenantId): string`
- `parseDraft(raw: string | null): CampaignDraft | null`
- `sharePath(origin: string, code: string): string`
- `shareText(input: {title, storeName, startsAt, endsAt, url}): string`

- [ ] 纯函数测试 RED（RFC3339、草稿往返、分享文案、无效日期）。
- [ ] 实现 `campaign-create.ts` → GREEN。
- [ ] 向导组件：三步、服务端 draft、localStorage、错误/加载/空态、一个主行动。
- [ ] 列表页改为列表 + 唯一“新建活动”；`data-pattern="list"`。
- [ ] 详情页分享区：只对 enabled link 提供复制/二维码；停用/活动结束文案；状态中文；主行动唯一。
- [ ] Commit: `feat(hui-2628): merchant campaign creation wizard, safe share panel, publish state`

### Task 3: 顾客页门控、时间、版本一致、AI 标识、奖励提示

**Files:**
- Modify: `web/src/app/c/[code]/public-campaign.tsx`
- Modify: `web/src/components/customer/ContentPublish.tsx`
- Modify: `web/src/components/customer/RewardStatus.tsx`
- Modify: `web/src/app/c/[code]/customer-publish.tsx`（能力矩阵折叠）
- Create: `web/src/lib/public-time.ts`（`formatCampaignWindow`）
- Modify: `web/src/lib/customer-journey.ts`（`codeIssueNotice`）
- Test: `web/tests/public-time.test.ts`、`web/tests/customer-journey.test.ts`、`web/tests/customer-content-publish.test.ts`、`web/tests/customer-three-screens.test.ts`

**Interfaces:**
- `formatCampaignWindow(startsAt?: string, endsAt?: string, opts?: {locale?, timeZone?, now?}): string`
- `codeIssueNotice(payload: string): string | null`（null = 可出示）
- `ContentSnapshotView.marking?: {ai_generated?: boolean; ad_required?: boolean; incentive_disclosed?: boolean}`
- `PublicView.capabilities?: {participation?: boolean; content_packs?: boolean; ...}`

- [ ] `public-time` + `codeIssueNotice` 纯函数测试 RED → 实现 GREEN。
- [ ] 公共页能力门：participation=false 不显示 start；content_packs=false 不挂 ContentPublish；显示真实原因。
- [ ] ContentPublish：请求带 `version_id`；409 mismatch 显示明确恢复；AI/广告标识；请求竞态守卫。
- [ ] 公共页时间改 `formatCampaignWindow`。
- [ ] RewardStatus：busy 双保险、codeIssueNotice。
- [ ] CustomerPublish：能力矩阵收进 `<details>`，保留可用/不可用与安全语义。
- [ ] Commit: `fix(hui-2222): customer capability gate, pinned content version, honest AI/time/reward copy`

### Task 4: EcoTopNav 真实模型 + app switch BFF + Go launch

**Files:**
- Modify: `web/src/lib/eco-nav/load.ts`（blocked unavailable，不 fixture）
- Modify: `web/src/app/api/eco-nav/route.ts`（转发会话头）
- Create: `web/src/app/api/eco-nav/switch/route.ts`
- Modify: `web/src/components/eco-nav/EcoTopNav.tsx`（按钮启动、错误、无 href）
- Modify: `web/src/components/eco-nav/use-launch-resolver.ts` → `use-launch-targets.ts`
- Modify: `web/src/components/admin/AdminShell.tsx`（失败不 fixture）
- Create: `server/internal/httpapi/handlers_eco_nav.go`
- Modify: `server/internal/identity/client.go`（LaunchShell）
- Modify: `server/internal/httpapi/server.go`（注册 POST /api/v1/eco-nav/switch）
- Test: `web/tests/eco-nav/load.test.ts`、`web/tests/eco-nav/switch.test.ts`、`server/internal/httpapi/server_eco_nav_test.go`、`server/internal/identity/client_test.go`

**Interfaces:**
- `blockedUnavailableEcoNav(): EcoNavModel`
- `POST /api/eco-nav/switch` request `{app_id: string, launch_target_id: string}` → 200 `{launch_url: string}`；400/403/429/502/503 安全错误。
- Go `identity.Client.LaunchShell(ctx, principalID, tenantID, appID, targetID string) (string, error)`
- Go route `POST /api/v1/eco-nav/switch` → `{launch_url}`.
- `validateLaunchUrl(targetId: string, raw: string): string | null`（同源 + path 前缀 + https）。

- [ ] Go identity client launch 测试 RED → 实现。
- [ ] Go handler 测试（成功/缺配置/上游 403/非法 target/current app）RED → 实现。
- [ ] Web load 测试：无配置/网络失败/契约坏 → blocked，不 fixture。
- [ ] Web switch 测试：缺字段 400、target 未注册 403、上游 URL 不匹配 502、成功 200。
- [ ] EcoTopNav 改按钮 + POST + location.assign；失败显示顶栏错误；测试源码/纯函数。
- [ ] Commit: `feat(hui-2227): real eco-nav loading, fail-closed app switch via shell launch`

### Task 5: 语义 token 落地 + 对比度门 + page-pattern

**Files:**
- Modify: `web/src/app/globals.css`（只留 reset + `--tk-*` 别名）
- Modify: `web/src/app/touch.css`（颜色/焦点/圆角走 `--pn-*`）
- Modify: `web/src/components/eco-nav/eco-top-nav.module.css`
- Create: `web/scripts/contrast-gate.mjs`
- Create: `web/tests/contrast-gate.test.ts`
- Create: `web/src/contracts/page-patterns/v1/catalog.json` + `SOURCE.txt`
- Create: `web/src/lib/page-patterns.ts`
- Create: `web/tests/page-patterns.test.ts`
- Modify: `web/package.json`（`test:contrast`）

**Interfaces:**
- `evaluateContrast(resolved, checks): {rows, failures}`
- `routePattern(route): PatternId | null`；`patternRegions(id): string[]`
- npm script `test:contrast` → `node scripts/contrast-gate.mjs`

- [ ] 对比度测试先写一个故意失败用例（红）→ 实现计算 → 真实门 GREEN。
- [ ] CSS 去裸值、补全别名、焦点环 token 化。
- [ ] vendor page-pattern catalog + 指纹/结构测试。
- [ ] Commit: `feat(hui-2628): semantic token adoption, deterministic contrast gate, page-pattern map`

### Task 6: 矩阵草稿交接修复

**Files:**
- Modify: `web/src/components/admin/TaskHandoffActions.tsx`
- Modify: `server/internal/httpapi/handlers_matrix_draft.go`（响应带 activity_version）
- Test: `web/tests/task-handoff.test.ts`、`server/internal/httpapi/server_ag07_test.go`

- [ ] 测试 RED：主行动调用 matrix-draft，不带 campaign-image；响应展示版本/草稿/未外发。
- [ ] 服务端响应加 `activity_version`。
- [ ] 实现 UI；删除死调用。
- [ ] Commit: `fix(hui-2628): matrix draft handoff replaces broken campaign-image action`

### Task 7: e2e 真实栈 + 证据

**Files:**
- Modify: `web/e2e/scripts/env.mjs`（FEATURE_CONTENT_PACKS/PARTICIPATION/REWARD_OPS，seed content pack）
- Modify: `web/e2e/scripts/identity-stub.mjs`（shell/launch 合同）
- Create: `web/e2e/specs/12-campaign-creation.spec.ts`
- Create: `web/e2e/specs/13-public-content-version.spec.ts`
- Create: `web/e2e/specs/14-eco-nav-switch.spec.ts`
- Create: `docs/superpowers/evidence/2026-10-21-hui-2628-touch-shell-evidence.md`

- [ ] build + env up + 新 spec 跑通；截图创建三步、详情分享、公共页、导航 blocked。
- [ ] 记录真实命令/输出/失败边界（public-ai 端点、平台 upload）。
- [ ] Commit: `test(hui-2628): real-stack e2e for creation, content version, eco-nav switch`

### Task 8: 两轴 review + 收口

- [ ] Standards + Spec 自审清单，修问题。
- [ ] 全量 vitest / go test / build / contrast / 目标 e2e。
- [ ] 合并 `mb/eco-20261021`（锁文件），复跑门，回 main，报告。
