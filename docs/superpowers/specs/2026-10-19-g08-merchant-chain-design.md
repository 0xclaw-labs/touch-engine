# G08 商家内容链 — MX-OPS R2 增量设计（1702 / 2747 / 3553）

- 日期：2026-10-19
- 组/票：G08 / HUI-1702（product-image-engine）、HUI-2747（leads-engine）、HUI-3553（touch-engine）
- 合同：`mx-ops-c0/v1 @ 6ffc90ca`（旧 45b32f4b；errata 只影响 config-command-response 消费方，本组不消费该对象）
- 状态：已实现并交付 PR（设计/计划发出后 Lead 未显式回 PLAN APPROVED；按 DECISIONS D16「20 分钟默认继续」执行，Lead-delegated default）。真实模型/GPU/账务/生产调用不在本设计内。

## 1. 意图与边界

三票原功能均已合并且票仍 In Progress。本次只做 MX-OPS R2 的**消费增量**：

1. 商家/获客侧消费星模允许的**执行偏好**（立即/夜间/闲时/付费弹性）与**费用承担**；
2. 消费**权威队列读模型**：phase / wait_reason / jobs_ahead / running_jobs / 可空 ETA / next_eligible_at / policy_version；
3. **候选回流**：云端产物只进候选，商家/获客明确采用才生效；晚到旧稿、重复回执、重试都不覆盖已启用版本；
4. 顾客/访客路径**零新生成**（3553 硬约束）。

非目标（不做）：

- 不做老票剩余功能，不重做整票，不接管旧 G1/Motion/GoBoost 写面；
- 不新建引擎、调度器、钱包、H3/Compute、Runtime token、AutoDL 执行器；
- 不发明合同字段/枚举/状态；需要 additive 时只报 Lead，不在仓内改合同语义；
- 不调用真实付费模型/GPU；不部署、不 merge、不切流。

Anti-goals（做了就算失败）：

- 用 T2V 冒充商品图 I2V；把静图/占位 URL/内存夹具标成真实视频；
- 客户端自封优先级或把免费当 internal；把后台成本钱包当客户余额重复扣费；
- 队列完成/回流自动外发、自动触达、自动记录转化、自动替换已启用内容；
- 给访客伪进度、让顾客触碰新开 GPU/重新生成；
- 把 UNKNOWN 折叠成成功（ETA、队列、授权、能力都必须显式 null/UNKNOWN）。

## 2. 在飞保护核查结论（2026-10-19 00:40，三仓逐仓）

| 仓 | base origin/main | 脏树 | 本地 worktree | 远端未合并同票分支 | 同票 PR | 其他 open PR |
|---|---|---|---|---|---|---|
| product-image-engine | `57390e6` | 无 | 仅 main | 无（`ag05/hui-1702-i2v-honest`、`hui-1702-image-to-video`、`hui-1702-r3` 均已 MERGED #55/#16/#25） | 无 | #67 只改 `web/tests/eco-nav-registry.test.ts` |
| leads-engine | `912fcb7` | 无 | 仅 main | 无（`ext/hui-2747-campaign-motion` 已 MERGED #42） | 无 | #60 只改 `.github/workflows/*` |
| touch-engine | `a2a2ac5` | 无 | 仅 main | 无（`hui-3553-content-packs` 已 MERGED #50） | 无 | #49 只改 `.github/workflows/*` |

- `ccfilmsichao` 在三仓的 PR（all states）为空；In Progress 只是票状态与原作者保留，未发现未合并写面。
- 结论：**无同文件在飞冲突**，可做窄增量补丁。仍避开他人 open PR 的文件（`.github/workflows/*`、PIE 的 `web/tests/eco-nav-registry.test.ts`）。
- 原票承诺、In Progress 状态、旧 worktree/PR 归属全部保留；本次分支为 `mx-ops/*`，不接管旧 G1。

## 3. 合同采用

- `mx-ops-c0/v1 @ 6ffc90ca`。每仓 `docs/mx-ops/<ticket>/contract-version.md` 记录 sha + 采用对象/字段 + 未采用项。
- 采用对象：`execution_policy`（template/time_window/dispatch/cost_bearing/service_level）、`job_queue_view`（全部公开字段；`internal_debug` 仅内部）、`execution_grant`（只展示/验证，不建钱包）、`error_taxonomy`（只映射展示文案）。
- 不采用：`config_command` / `config_command_response`（G01/G02 管理面）、`compute_pool` 写面（3824）、`model_offering` 写面。
- 采用方式：把合同仓的 `mx-ops-c0.v1.ts` **原样拷贝**到每仓 `web/src/lib/mx-ops/contract.ts`，加头注释标注 sha，不改语义；Go 侧只在本票需要处窄解析（touch 的队列视图同步端点），不复制整份合同。
- 需要 additive 字段：**无**。本设计只用合同已有字段；UI 偏好是本地枚举到模板的映射，不扩合同。

## 4. 共享消费内核（三仓同形、各自副本）

因为三个仓是独立部署、不同技术栈，内核按仓拷贝（同语义、不同文件），不建共享 npm 包（避免新仓库/新发布物）。

### 4.1 `web/src/lib/mx-ops/queue.ts`

接口：

```ts
export function readJobQueueView(raw: unknown): JobQueueView | null;      // 严格读；unknown→null；0 保留
export function publicQueueView(raw: unknown): JobQueueView | null;     // 删除 internal_debug（整键）
export function presentQueue(view: JobQueueView | null, nowUnix?: number): MerchantQueue;
export function legacyQueue(raw: unknown): LegacyQueue | null;          // 冻结语义：jobs_ahead 含运行中
```

`MerchantQueue`：`phaseLabel`、`waitLabel`、`waitKind`（cloud/capacity/window/eligibility/budget/authorization/material/none/unknown）、`stageLabel`、`jobsAhead`、`waitingJobsAhead`、`runningJobs`、`etaText`、`nextEligibleText`、`policySource`、`policyVersion`、`capacityLabel`、`borrowed`、`asOfText`。

规则：

- `contract_version !== "mx-ops-c0/v1"` 或 `object_kind !== "job_queue_view"` → null（不猜）。
- 必需字段缺失/类型不符 → null（整条不可信），禁止逐字段补 0。
- `eta_seconds_min/max`：任一是 null 或 min>max → "暂不可估"；`0` 是有效值。
- `waiting_jobs_ahead`/`running_jobs` 为 null → "未知"，不是 0。
- `next_eligible_at_unix` 只按原值展示；null → 无窗口。
- `internal_debug` 永不进入公开视图；公开投影整键删除。

### 4.2 `web/src/lib/mx-ops/execution.ts`

接口：

```ts
export type PreferenceId = "immediate" | "night_batch" | "free_idle" | "paid_elastic";
export function executionOptions(input: { allowedTemplates: PolicyTemplate[]; entitlements: string[]; payer: PayerKind; quoteConfirmed: boolean; nowUnix: number; hasNightWindow: boolean }): ExecutionOption[];
export function validatePreference(input: { preference: string; policy: ExecutionPolicy | null; entitlements: string[]; quoteConfirmed: boolean }): PreferenceResult;
export function costBearingLabel(payer: PayerKind): string;
export function assertNoPriority(value: unknown): void;  // 出现 priority/优先级数字即拒绝
```

规则（合同权限链交集的下沉展示，不做服务端政策判断）：

- 选项只能来自 `allowed_policy_templates` ∩ 权益 ∩ 时间窗；不可用给原因，不隐藏事实。
- `free_idle` 需要显式 `free_idle` 权益 + `platform_subsidy`/`campaign` 承担；**不等于 internal**。
- `night_batch` 需要 `time_window`；窗口结束只停新派单，不续命/不换模式。
- `paid_elastic` 需要用户承担 + 已确认报价；不默认抢占运行中。
- 永远不发数字 priority；客户端伪造 priority 由服务端 `mx_ops_c0_priority_forged` 拒绝（内核也拒绝构造）。
- 费用承担只展示，不建第二钱包；唯一 charge 归星模 + 公共 Billing。

### 4.3 `web/src/lib/mx-ops/return.ts`

接口：

```ts
export type ReturnReceipt = { idempotencyKey: string; creativeRef: string; candidateRef: string; contentDigest: string; receivedAtUnix: number; source: string };
export function acceptReturn(state: ReturnState, incoming: ReturnReceipt): ReturnResult; // { state, replayed, conflict, autoAdopted: false }
```

规则：

- 同 key 同 digest = 重放，不新增候选、不重生成、不重扣。
- 同 key 不同 digest = `idempotency_conflict`，拒绝。
- 新 key = 追加候选；**adoptedRef 永不被本函数改变**。
- 晚到旧稿 = 候选 + `late: true`；已启用内容不静默变化。
- 采用只经显式动作；本内核 `autoAdopted` 恒 false。

### 4.4 本组统一等待词汇

区分：`等待云端产物`（排 GPU/队列/夜间窗口）、`等待资源`（capacity）、`等待授权/预算`、`等待商家/销售人工`（人工审核、跟进）。GPU 忙不得阻塞 CRM 日常工作；ETA 未知显示"暂不可估"，不给伪进度。

## 5. 各仓增量

### 5.1 touch-engine（HUI-3553）

新增/修改：

- `web/src/lib/mx-ops/{contract,queue,execution,return}.ts`：共享内核。
- `web/src/lib/work/production.ts`：商家制作视图 `presentProduction(job, ctx)`：
  - 偏好标签、云端 phase/queue/等待原因/ETA/下一窗口；
  - 夜间窗口 vs 活动发布日期分开比较：窗口晚于发布日期 → 商家语言提示，不自动升级付费/改活动；
  - 三层费用分开：商家工具费 / 活动优惠预算 / 模型实际成本（只展示，不建钱包）；
  - 恢复入口只回同一 `handoff_id/job_ref`，不新建制作工程；
  - 顾客守卫 `visitorContentGuard()`：`generationAllowed:false`，访客只拿已采用版本。
- `web/src/components/work/production-panel.tsx`：活动详情新增"云端制作"区（挂在 ContentPackPanel 旁）；列出最近制作交接、等待、ETA、候选；"安排夜间制作"仅在有工具能力且有夜间窗口时可用。
- 服务端窄增量：
  - migration `0042_content_handoff_production.sql`：`content_handoffs` 增 `execution_preference / policy_ref / policy_version / job_ref / job_queue_view`（全部 NOT NULL DEFAULT，旧行兼容）。
  - `internal/mxopsview`：严格解析/规范化 `job_queue_view`（校验 contract_version/object_kind/必需字段；删 `internal_debug`；0≠null；unknown→null）。
  - store：`HandoffInput` 增 `ExecutionPreference/PolicyRef/PolicyVersion`；新增 `UpdateContentHandoffQueueView`、`GetContentHandoff`。
  - handlers：`GET /api/v1/campaigns/{id}/content-handoffs`（恢复入口）；`POST /api/v1/content-handoffs/{handoffId}/queue-view`（adapter 同步接缝，校验合同后存公开投影）。客户端不得直接写入内部字段。
  - 创建交接的 HTTP 层：`execution_preference` 枚举校验；`priority` 等未知字段继续 400 拒绝（现有 strict decode 已拒绝，补测试）。
- 顾客路径：不改 `custpublish`；新增测试证明顾客发布读已采用版本且不调用制作/生成入口。

### 5.2 product-image-engine（HUI-1702）

- **I2V 能力结论 = UNKNOWN**。证据：`server/internal/videofixture` 是内存夹具（`provider_calls=0`、`generation_triggered=false`、无视频字节）；`server/internal/showcasevideo` 明确"本包没有图生视频客户端"；`server/internal/engwalk` 只有 `text_image`；仓内无任何 image-conditioned video provider/adapter。**没有可用 I2V，禁止用 T2V 冒充。**
- 新增 `server/internal/i2v/capability.go`：定义缺的 Adapter 接缝（`Adapter.Submit/Poll/Fetch`、image-conditioned 校验、时长/比例/主体保持检查、成本行、真实产物 hash），`Resolve(modelID)` 返回 `{status:"UNKNOWN", missing:[...]}`；请求缺 image 条件 → `ErrNotImageConditioned`，绝不回退 T2V；无 adapter 时不拨号、不生成。
- `docs/mx-ops/1702/i2v-capability.md`：证据、缺口、验收方式、负责人接缝；真实联调 `UNKNOWN（未授权实机窗口）`。
- `web/src/lib/mx-ops/*` 内核 + `web/src/lib/showcase-video-production.ts`：消费可空 `job_queue_view`、报价/费用承担（`计费待确认` 保留）、候选素材回流（不改已采用）。
- `web/src/components/showcase-video/Panel.tsx`：任务卡显示 phase/queue/等待原因/可空 ETA/费用承担/候选状态；能力 UNKNOWN 时明确显示"图生视频能力未验证"，不出现静图/假播放；生成中离场/刷新回原 job（复用现有 record/previousId 逻辑）。
- 不改 Motion/GoBoost/公共服务；跨应用交接仍归 1745。

### 5.3 leads-engine（HUI-2747）

- `web/src/lib/mx-ops/*` 内核 + `web/src/lib/campaignMotionQueue.ts`：
  - `presentCampaignProduction(input)`：制作任务/活动卡回流真实 phase/queue/等待原因/夜间窗口/可空 ETA/策略版本；
  - 区分"等待云端产物"与"等待销售跟进/人工审核"；GPU 忙不阻塞 CRM；
  - 费用归因：`campaign_id / creative_id / variant / model cost`，渠道数不线性生成；只展示/发起，不建钱包；
  - 队列完成不自动触达/发布/记录转化（`autoOutreach:false`）。
- `web/src/lib/campaignMotion.ts` 增：最小合法事实交接客户端校验（campaign/brand/product/CTA/payer/budget/return），CRM 私密键（chat/contact/phone/lead…）拒绝；与既有服务端 `campaignmotion` 白名单一致（不复制政策判断，只做入库前提示）。
- `web/src/components/CampaignMotionPanel.tsx`：增加偏好选择（来自 `allowed_templates`）与队列/等待/费用/候选回流展示；无 `job_queue_view` 时如实 UNKNOWN。
- 不改 Go 服务端（现有 `campaignmotion` 已实现事实白名单/幂等/落库）；真实 RefVerifier/StudioPort/账务观测仍 `UNKNOWN`。

## 6. 数据流与 fixture 全链路

```
(管理员/星模策略) --mx-ops-c0/v1--> [policy fixture] --消费--> executionOptions/validatePreference
(权威队列)        --job_queue_view--> [queue fixture]  --严格读--> presentQueue --UI--> 商家/获客
(云端产物)        --return receipt--> [return fixture] --acceptReturn--> 候选 --显式采用--> 已启用
```

每仓测试内固定 fixture（从 `6ffc90ca` 正例拷贝 + 本仓回流例）：

- `web/tests/fixtures/mx-ops/execution-policy.night_batch.json`
- `web/tests/fixtures/mx-ops/job-queue-view.waiting.json`（null ETA）与 `job-queue-view.measured.json`（0 与已知值）
- `web/tests/fixtures/mx-ops/return-candidate.json`

全链路测试（fixture，不联网）：

1. 夜间偏好 → 生成/提交交接（同 idempotency key 重放）→ 队列 waiting/night window → 可空 ETA 展示 → 刷新/离场恢复同 job → 回流候选 → 显式采用同版本 → 晚到旧稿/重复回执不覆盖。
2. 无合同/未知字段 → 显式 UNKNOWN，不猜、不折叠成功。
3. 访客/顾客路径：多次触碰 provider submit 数不变（源码/调用点断言 + 纯函数守卫）。

## 7. 测试策略

- 每仓先失败测试：`web/tests/mx-ops-*.test.ts`（纯函数 + fixture + 源码契约断言）；touch/PIE 另有 Go 测试。
- 命令：`cd web && npx vitest run`（受影响文件先跑，最后全量）；Go `cd server && go test ./internal/...`（PIE 因既有依赖 tag 缺失，Go 侧只跑 `go test ./internal/i2v/`，其余记基线失败）。
- 不跑真实供应商/GPU；不新增依赖；不改 lockfile（除已必要的既有）。
- 证据：测试输出、fixture、`contract-version.md`、`docs/mx-ops/<ticket>/handoff.md`。

## 8. 已定默认（Lead-delegated default，按 DECISIONS）

- D12：三仓三支三 PR，串行 order = touch → PIE → leads；每仓完成闭环再下一仓。
- D02：PR-only，不 merge/deploy；分支 `mx-ops/<ticket>-<slug>`，worktree `.worktrees/mx-ops-<ticket>-<repo>`。
- D05：基线失败记录后继续；PIE Go 基线失败（`public-ai/sdk/go v0.4.0` tag 已不存在）不修，web 测试全绿。
- D06：真实模型/GPU/账务 `UNKNOWN`，fixture 全链路必须跑通。
- D11：消费侧只展示/发起，不改唯一收费链。
- 合同 additive：无需求。

## 9. UNKNOWN / 未验证清单（设计时点）

1. 真实 I2V 可用性：**无可用 adapter**，UNKNOWN；需 2295/Catalog/Adapter owner 补 image-conditioned video provider。
2. 真实星模 `job_queue_view` 投影：未接；touch 用显式同步端点 + fixture；PIE/leads 读可选字段。
3. 真实 `execution_policy` 下发/权益：未接；用合同 fixture 验证 UI 规则。
4. 真实计费/费用归因：NOT_RUN；只展示合同金额/承担方。
5. 真实夜间窗口执行/回流：NOT_RUN；fixture 全链路通过不等于生产通过。
6. 跨应用真实交接（1745/Motion/GoBoost）：NOT_RUN，仍归对应票。
