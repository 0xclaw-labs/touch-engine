# G08 商家内容链 MX-OPS 增量实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 touch-engine / product-image-engine / leads-engine 三个仓分别做窄增量，让商家/获客侧消费 `mx-ops-c0/v1` 的执行偏好、权威队列等待、可空 ETA、费用承担与候选回流，顾客路径零新生成。

**Architecture:** 每仓一份同形 TS 消费内核（合同类型原样拷贝 + 严格读取/展示规则），各自在既有页面上挂最小 UI；touch 额外加一个 Go 队列投影同步接缝（严格校验合同后存公开投影）；PIE 额外加一个 I2V Adapter 接缝包，如实返回 UNKNOWN，不冒充 T2V。

**Tech Stack:** Next.js 15 + React 19 + TypeScript（vitest）、Go 1.26（net/http + modernc sqlite）；无新依赖。

**Spec:** `docs/superpowers/specs/2026-10-19-g08-merchant-chain-design.md`

## Global Constraints

- 合同 `mx-ops-c0/v1 @ 6ffc90ca`；只读消费，不改合同语义；不新增跨服务字段（无 additive 需求）。
- 未知一律 `null`/UNKNOWN；`0` 是有效测量值，不得当未知；`internal_debug` 公开投影整键删除。
- 不做真实模型/GPU/账务/生产调用；不新增钱包/调度器/引擎/仓库/端口/依赖；不改 lockfile。
- 不 merge、不 deploy、不切流；一仓一分支一 PR。
- 中文文档/commit；commit 前缀 `feat(mx-ops-<ticket>):` / `docs(...)` / `test(...)`。
- 每任务先失败测试→最小实现→通过→提交；受影响测试先跑，最后全量。
- 不碰他人 open PR 文件：`.github/workflows/*`、PIE `web/tests/eco-nav-registry.test.ts`。

## Review Focus

1. `job_queue_view` 里 `eta_seconds_min/max`、`waiting_jobs_ahead`、`running_jobs` 为 null 时，UI 是否误显示 0/“马上好”（应显示“暂不可估/未知”）。
2. 晚到旧稿、重复回流（同 key 同 digest）与同 key 异 digest 冲突时，已启用内容是否保持不变、是否误新增候选/重复扣费。
3. `free_idle`/`night_batch`/`paid_elastic` 选项在无权益、无时间窗、报价未确认时是否仍可选（应禁用并给原因），以及是否出现数字 priority 入参。
4. 访客/顾客路径是否因触碰、刷新、弱网重登触发任何新生成/新提交（应为 0）。
5. 真实上游缺失时页面是否显示 UNKNOWN 而非伪进度/伪成功（尤其 PIE 无 I2V adapter 时不得出静图/假视频）。

---

## Part A — touch-engine（HUI-3553，分支 `mx-ops/3553-night-content-return`，worktree `.worktrees/mx-ops-3553-touch-engine`）

### Task A1: 合同副本 + 队列读模型内核

**Files:**
- Create: `web/src/lib/mx-ops/contract.ts`（从 `git show 6ffc90ca:docs/contracts/mx-ops-c0/v1/mx-ops-c0.v1.ts` 原样拷贝）
- Create: `web/src/lib/mx-ops/queue.ts`
- Create: `docs/mx-ops/3553/contract-version.md`
- Create: `web/tests/fixtures/mx-ops/job-queue-view.waiting.json`、`job-queue-view.measured.json`、`execution-policy.night_batch.json`、`return-candidate.json`
- Test: `web/tests/mx-ops-queue.test.ts`

**Interfaces:**
- Produces: `readJobQueueView(raw: unknown): JobQueueView | null`、`publicQueueView(raw: unknown): JobQueueView | null`、`presentQueue(view: JobQueueView | null, nowUnix?: number): MerchantQueue | null`、`MerchantQueue`（字段见 spec §4.1）。
- Consumes: `contract.ts` 的 `JobQueueView` 类型。

- [ ] **Step 1: 写失败测试** `web/tests/mx-ops-queue.test.ts`：
  - 合法 waiting fixture → `presentQueue` 的 `phaseLabel/等待原因`，`etaText==="暂不可估"`，`waitingJobsAhead===null` 显示“未知”，`jobsAhead===2` 保留；
  - measured fixture（`eta 0/0`、`waiting_jobs_ahead:0`）→ `etaText` 含“0 分钟”，`waitingJobsAhead===0`（0≠null）；
  - 带 `internal_debug` → `publicQueueView` 结果无 `internal_debug` 键；
  - 错版本/错 object_kind/缺字段 → null；
  - `legacyQueue` 保留 `jobs_ahead` 含运行中语义（fixture 旁路断言）。
- [ ] **Step 2: 跑测试确认失败**：`cd web && npx vitest run tests/mx-ops-queue.test.ts`（模块不存在）。
- [ ] **Step 3: 实现** `contract.ts`（拷贝）+ `queue.ts`（严格读、null 保留、内部键删除、中文展示；`etaText` 规则：任一 null 或 min>max → 暂不可估）。
- [ ] **Step 4: 跑测试通过**。
- [ ] **Step 5: 写** `docs/mx-ops/3553/contract-version.md`：`mx-ops-c0/v1 @ 6ffc90ca`、采用对象/字段、未采用项、errata 说明、无 additive。
- [ ] **Step 6: Commit** `feat(mx-ops-3553): 消费 C0 队列读模型（严格 null/0 语义）`。

### Task A2: 执行偏好与费用承担内核

**Files:**
- Create: `web/src/lib/mx-ops/execution.ts`
- Test: `web/tests/mx-ops-execution.test.ts`

**Interfaces:**
- Produces: `executionOptions(input): ExecutionOption[]`、`validatePreference(input): PreferenceResult`、`costBearingLabel(payer): string`、`assertNoPriority(value): void`。
- Consumes: `contract.ts` 的 `ExecutionPolicy`/`PolicyTemplate`。

- [ ] **Step 1: 写失败测试** `web/tests/mx-ops-execution.test.ts`：
  - night_batch 策略 + 有夜间窗 → 夜间选项可用、显示时间窗；
  - 无 `free_idle` 权益 → 免费闲时禁用且 reason 非空；有权益 + `platform_subsidy` → 可用；
  - `paid_elastic` 报价未确认 → 禁用；确认 → 可用且显示用户承担；
  - 传入数字 `priority` 字段 → `assertNoPriority` 抛错；`validatePreference` 对策略外模板返回 `unsupported`；
  - 费用文案含“只展示/发起，唯一 charge 归星模 + 公共 Billing”，不出现“钱包余额”。
- [ ] **Step 2: 跑失败**。
- [ ] **Step 3: 实现** 规则：选项 = allowed_templates ∩ 权益 ∩ 窗口；夜窗缺失不可选；免费≠internal；不发 priority。
- [ ] **Step 4: 跑通过**。
- [ ] **Step 5: Commit** `feat(mx-ops-3553): 执行偏好只从允许模板与权益取交集`。

### Task A3: 回流候选内核

**Files:**
- Create: `web/src/lib/mx-ops/return.ts`
- Test: `web/tests/mx-ops-return.test.ts`

**Interfaces:**
- Produces: `acceptReturn(state: ReturnState, incoming: ReturnReceipt): ReturnResult`，`ReturnState = { candidates: ReturnReceipt[]; adoptedRef: string | null }`，`ReturnResult = { state, replayed, conflict, late, autoAdopted: false }`。
- Consumes: 无。

- [ ] **Step 1: 写失败测试**：首回执→候选+`autoAdopted:false`；同 key 同 digest→`replayed:true`、候选数不变；同 key 异 digest→`conflict:true`、状态不变；已 adopted 后新回执→`state.adoptedRef` 不变、新候选 `late:true`。
- [ ] **Step 2: 跑失败**。
- [ ] **Step 3: 实现**（digest 用小写十六进制/`sha256:` 前缀都接受，内部规范化；不改 adoptedRef）。
- [ ] **Step 4: 跑通过**。
- [ ] **Step 5: Commit** `feat(mx-ops-3553): 回流只进候选，显式采用才生效`。

### Task A4: 商家制作视图（夜间/等待/费用/恢复）

**Files:**
- Create: `web/src/lib/work/production.ts`
- Test: `web/tests/mx-ops-production.test.ts`

**Interfaces:**
- Produces: `presentProduction(job: ProductionJob, ctx: ProductionContext): ProductionView`、`visitorContentGuard(): { generationAllowed: false; reason: string }`。
- Consumes: `presentQueue`、`executionOptions`、`costBearingLabel`。

- [ ] **Step 1: 写失败测试**：
  - 夜间 job（window 20:00–08:00，活动发布 2026-10-20）→ 显示“夜间批量/下一窗口”，`windowAfterPublish===false`；
  - 下一窗口晚于发布日 → `windowAfterPublish===true` 且提示“预计赶不上发布”，`autoUpgrade:false`；
  - 无 `job_queue_view` → `queue===null`、等待文案“云端排队状态未知”；
  - 三层费用字段齐全且互不合并；含“不自动收费”；
  - `visitorContentGuard().generationAllowed===false`。
- [ ] **Step 2: 跑失败**。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 跑通过**。
- [ ] **Step 5: Commit** `feat(mx-ops-3553): 云端制作等待与费用分层展示`。

### Task A5: 服务端队列投影同步接缝（Go）

**Files:**
- Create: `server/internal/db/migrations/0042_content_handoff_production.sql`
- Create: `server/internal/mxopsview/mxopsview.go`、`mxopsview_test.go`、`testdata/job-queue-view.waiting.json`
- Modify: `server/internal/store/store_content_packs.go`（HandoffInput/ContentHandoff/scanHandoff/CreateContentHandoff/handoffCols；新增 `UpdateContentHandoffQueueView`、`GetContentHandoff`）
- Modify: `server/internal/httpapi/handlers_content_packs.go`（create 增 `execution_preference/policy_ref/policy_version`；新增 list 与 queue-view 同步 handler）
- Modify: `server/internal/httpapi/server.go`（两条路由）
- Test: `server/internal/httpapi/server_content_handoffs_test.go`

**Interfaces:**
- Produces: `mxopsview.Parse(raw []byte) (View, error)`、`(View) PublicJSON() ([]byte, error)`；HTTP `GET /api/v1/campaigns/{id}/content-handoffs`、`POST /api/v1/content-handoffs/{handoffId}/queue-view`。
- Consumes: `mx-ops-c0/v1` `job_queue_view` JSON。

- [ ] **Step 1: 写失败测试**：`mxopsview_test.go`（合法 fixture 通过；错版本/缺字段/非对象失败；`internal_debug` 在 `PublicJSON` 中缺失；null ETA 保留 null、0 保留 0）；`server_content_handoffs_test.go`（创建带 `execution_preference`、`priority` 字段 400、同 key 重放同 id、list 返回、queue-view 同步后 GET 可见、跨租户 404）。
- [ ] **Step 2: 跑失败**：`cd server && go test ./internal/mxopsview/ ./internal/httpapi/ -run 'Handoff|Mxops' -count=1`。
- [ ] **Step 3: 实现** migration/store/handlers/package（严格 JSON：拒绝未知内部键？接受但剥离 `internal_debug`；enum 只收 `immediate|night_batch|free_idle|paid_elastic`；未知字段继续由 strict decode 拒绝）。
- [ ] **Step 4: 跑通过**：`go test ./internal/mxopsview/ ./internal/store/ ./internal/httpapi/ -count=1`。
- [ ] **Step 5: Commit** `feat(mx-ops-3553): 队列投影同步接缝与制作恢复读面`。

### Task A6: 云端制作面板 + 挂载

**Files:**
- Create: `web/src/components/work/production-panel.tsx`
- Modify: `web/src/app/work/campaigns/[id]/page.tsx`（ContentPackPanel 旁挂载）
- Test: `web/tests/mx-ops-production-panel.test.ts`

**Interfaces:**
- Consumes: `presentProduction`；`session.api("GET", `campaigns/${campaignId}/content-handoffs`)`；`session.api("POST", `campaigns/${campaignId}/content-handoffs`, { execution_preference, ... })`。

- [ ] **Step 1: 写失败测试**（源码契约断言）：面板含 phase/等待原因/ETA/下一窗口/费用三层/候选/恢复入口；无队列显示“未知”；不渲染 `internal_debug`；无自动采用按钮；“安排夜间制作”仅 `disabled=false` 当能力与窗口可用；访客提示“顾客触碰不会新开生成”。
- [ ] **Step 2: 跑失败**。
- [ ] **Step 3: 实现面板 + 挂载**（复用 tk-* 样式；失败给商家语言与重试；不弹内部码）。
- [ ] **Step 4: 跑通过**：`npx vitest run tests/mx-ops-production-panel.test.ts`。
- [ ] **Step 5: Commit** `feat(mx-ops-3553): 活动页云端制作区（等待/窗口/候选/恢复）`。

### Task A7: 顾客零新生成证据 + 全量回归 + handoff

**Files:**
- Test: `web/tests/mx-ops-visitor-zero-generation.test.ts`
- Create: `docs/mx-ops/3553/handoff.md`
- Create: `docs/mx-ops/3553/contract-version.md`（A1 已建，补采用清单）

- [ ] **Step 1: 写失败测试**：断言顾客页/发布路径源码不含 `content-handoffs`/`queue-view`/生成调用；`visitorContentGuard` 恒 false；同一已采用版本快照函数稳定（同 pack+campaign → 同 version id）。
- [ ] **Step 2: 跑测试**。
- [ ] **Step 3: 全量 `npx vitest run` + `go test ./internal/...`**，记录通过数/失败数。
- [ ] **Step 4: 写 handoff.md**（PLAYBOOK §5 模板 + UNKNOWN 清单）。
- [ ] **Step 5: Commit** `docs(mx-ops-3553): handoff 与顾客零生成证据`。

---

## Part B — product-image-engine（HUI-1702，分支 `mx-ops/1702-i2v-policy-candidate`，worktree `.worktrees/mx-ops-1702-product-image-engine`）

### Task B1: 合同副本 + 队列/执行偏好/回流内核

**Files:** 同 A1–A3 的 `web/src/lib/mx-ops/*`、`docs/mx-ops/1702/contract-version.md`、fixtures、测试 `web/tests/mx-ops-queue.test.ts` 等。
**Interfaces:** 同 A1–A3。
- [ ] 复制并改造 A1–A3（同语义；测试同形）→ `npx vitest run tests/mx-ops-*.test.ts` 通过 → commit `feat(mx-ops-1702): 消费 C0 队列/偏好/回流内核`。

### Task B2: I2V Adapter 接缝（Go，UNKNOWN）

**Files:**
- Create: `server/internal/i2v/capability.go`、`capability_test.go`
- Create: `docs/mx-ops/1702/i2v-capability.md`
- Test: `server/internal/i2v/capability_test.go`

**Interfaces:**
- Produces: `i2v.Resolve(modelID string) Capability`，`Capability{Status "UNKNOWN", Missing []string, ImageConditioned bool, AdapterRef string}`；`i2v.ValidateRequest(Request) error`；`Adapter` 接口 `Submit/Poll/Fetch`。
- Consumes: 无。

- [ ] **Step 1: 写失败测试**：`Resolve("h3-fast")` → `Status=="UNKNOWN"`、`Missing` 含 `image_conditioned_video_provider`/`submit`/`poll`/`fetch`/`artifact_hash`/`subject_consistency_gate`；`ValidateRequest` 无 image_ref → `ErrNotImageConditioned`；有 image_ref 但无 adapter → `ErrAdapterUnavailable`；任何路径 `ProviderCalls==0`。
- [ ] **Step 2: 跑失败**：`cd server && go test ./internal/i2v/ -count=1`。
- [ ] **Step 3: 实现**（不拨号、不生成；注释写明为什么 T2V 不能替代）。
- [ ] **Step 4: 跑通过**。
- [ ] **Step 5: 写** `docs/mx-ops/1702/i2v-capability.md`：证据（videofixture/showcasevideo/engwalk）、缺口、验收脚本、负责人接缝、`UNKNOWN（未授权实机窗口）`。
- [ ] **Step 6: Commit** `feat(mx-ops-1702): 明确 I2V Adapter 接缝与 UNKNOWN 能力`。

### Task B3: 展示视频生产视图 + 面板

**Files:**
- Create: `web/src/lib/showcase-video-production.ts`
- Modify: `web/src/components/showcase-video/Panel.tsx`、`web/src/lib/showcase-video.ts`
- Test: `web/tests/mx-ops-showcase-production.test.ts`

**Interfaces:**
- Produces: `presentShowcaseProduction(job, nowUnix)` → `{ queue: MerchantQueue|null; costBearing; candidate: {ref, adopted:false}; capability: "UNKNOWN"; notice }`。
- Consumes: `presentQueue`、`costBearingLabel`。

- [ ] **Step 1: 写失败测试**：无 `job_queue_view` → `queue===null` 且文案“排队状态未知”；有 waiting fixture → phase/ETA/next window；`showVideo/playStill/videoUrl` 恒 false/""（沿用既有诚实语义）；能力 UNKNOWN 文案出现；候选 `adopted:false`。
- [ ] **Step 2: 跑失败**。
- [ ] **Step 3: 实现并接入 Panel**（不改变已有 `presentShowcaseJob` 输出；仅新增区块）。
- [ ] **Step 4: 跑通过**：`npx vitest run tests/mx-ops-showcase-production.test.ts tests/showcase-video*.test.ts`。
- [ ] **Step 5: Commit** `feat(mx-ops-1702): 展示视频队列等待与候选回流展示`。

### Task B4: 全量回归 + handoff

- [ ] 跑 `npx vitest run`；记录 Go 基线失败（`public-ai/sdk/go v0.4.0` tag 缺失）为既有问题，不在本票修。
- [ ] 写 `docs/mx-ops/1702/handoff.md`（含 I2V UNKNOWN、无 T2V 冒充、真实窗口未跑）。
- [ ] Commit `docs(mx-ops-1702): handoff 与 I2V 能力证据`。

---

## Part C — leads-engine（HUI-2747，分支 `mx-ops/2747-campaign-handoff-queue`，worktree `.worktrees/mx-ops-2747-leads-engine`）

### Task C1: 合同副本 + 队列/执行偏好/回流内核

**Files:** 同 A1–A3 的 `web/src/lib/mx-ops/*`、`docs/mx-ops/2747/contract-version.md`、fixtures、`web/tests/mx-ops-*.test.ts`。
- [ ] 同形复制并跑通 → commit `feat(mx-ops-2747): 消费 C0 队列/偏好/回流内核`。

### Task C2: Campaign 制作等待/费用归因视图

**Files:**
- Create: `web/src/lib/campaignMotionQueue.ts`
- Modify: `web/src/components/CampaignMotionPanel.tsx`
- Modify: `web/src/lib/campaignMotion.ts`
- Test: `web/tests/mx-ops-campaign-wait.test.ts`

**Interfaces:**
- Produces: `presentCampaignProduction(input)` → `{ waitKind, waitLabel, etaText, nextWindowText, policyVersion, costs: { campaignId, creativeId, variant, modelCostLabel }, autoOutreach: false, autoConversion: false, notice }`；`validateHandoffFacts(facts)`（最小合法事实 + CRM 私密键拒绝）。
- Consumes: `presentQueue`、`executionOptions`。

- [ ] **Step 1: 写失败测试**：
  - 云等待 vs 人工等待区分（GPU 忙不阻塞 CRM，文案含“销售跟进不受影响”）；
  - 无队列 → ETA 未知；有 queue fixture → phase/ETA；
  - 渠道 3 个但模型调用 1 次（参数化 variant 不线性增长）→ 费用只 1 条归因；
  - 队列完成 → `autoOutreach:false`/`autoConversion:false`；
  - CRM 私密字段（chat/contact/phone/lead）→ `validateHandoffFacts` 拒绝；合法 facts 通过；
  - 夜窗晚于活动日期 → 提示但不自动升级付费。
- [ ] **Step 2: 跑失败**。
- [ ] **Step 3: 实现** + 面板区块（偏好来自 capability 的 `allowed_templates`，无则 UNKNOWN 不猜测）。
- [ ] **Step 4: 跑通过**：`npx vitest run tests/mx-ops-campaign-wait.test.ts tests/campaignMotion.test.ts`。
- [ ] **Step 5: Commit** `feat(mx-ops-2747): 制作等待/费用归因与候选回流展示`。

### Task C3: 全量回归 + handoff

- [ ] `npx vitest run`；`cd server && go test ./internal/campaignmotion/ ./internal/httpapi/ -count=1`。
- [ ] 写 `docs/mx-ops/2747/handoff.md`（真实 RefVerifier/StudioPort/账务 UNKNOWN）。
- [ ] Commit `docs(mx-ops-2747): handoff 与真实上游 UNKNOWN`。

---

## 收尾（Lead 审批后）

1. 三仓各自 code-review（Standards + Spec 两轴，可内联执行；无子代理深度）。
2. `git push -u origin <branch>` + `gh pr create`（标题 `[MX-OPS <ticket>] ...`，body 用 PLAYBOOK §5 模板）；不 merge/deploy。
3. 每仓 `send_message` Lead 一条 ≤20 行汇报：`STAGE=pr TICKET=... REPO=... STATUS=ok SHA=... TESTS=... NEXT=...`。
4. 最终完整报告：分支/SHA/PR/合同版本/测试/UNKNOWN 清单。
