# MX-OPS handoff — HUI-3553 碰一碰商家内容链增量

- Group / Ticket: G08 / HUI-3553
- Repo: 0xclaw-labs/touch-engine
- Branch: `mx-ops/3553-night-content-return`
- Base: `a2a2ac5`（origin/main）
- Head: 本分支最新 commit（PR 记录）
- Worktree: `.worktrees/mx-ops-3553-touch-engine`
- PR: [#65](https://github.com/0xclaw-labs/touch-engine/pull/65)（OPEN，不合并/不部署）
- Contract: `mx-ops-c0/v1 @ 6ffc90ca`（记录见 `docs/mx-ops/3553/contract-version.md`；无 additive 需求）

## Scope done（逐条对工单 2026-10-18 MX-OPS 增量）

- [x] 商家执行偏好消费：`web/src/lib/mx-ops/execution.ts` 只从 `allowed_policy_templates` ∩ 权益 ∩ 时间窗 ∩ 报价状态取交集；无数字 priority（`assertNoPriority` 拒绝）；免费闲时需显式权益 + 平台补贴，不等于 internal；费用承担只展示，唯一 charge 归星模 + 公共 Billing。
- [x] 权威队列读模型：`web/src/lib/mx-ops/queue.ts` 严格读 `job_queue_view`；未知=null、0 保留；ETA 半未知/倒置按合同拒绝；`waiting_jobs_ahead` 不含运行中；`internal_debug` 公开投影整键删除；旧 `queue` 冻结语义保留。
- [x] 商家制作视图：`web/src/lib/work/production.ts` + `web/src/components/work/production-panel.tsx`，活动详情展示云端 phase/等待原因/可空 ETA/下一窗口/策略版本/恢复同一 job；夜间窗口与活动结束日分开，赶不上只提示、不自动升级/改活动；三层费用分开；访客守卫 `generationAllowed:false`。
- [x] 候选回流：`web/src/lib/mx-ops/return.ts` 同 key 同 digest 重放、同 key 异 digest 冲突、晚到旧稿只进候选、adopted 指针永不被回流改写；UI 明确「显式采用」。
- [x] 服务端窄增量：migration `0042` + `content_handoffs` 5 列；`GET /api/v1/campaigns/{id}/content-handoffs` 恢复列表；`POST /api/v1/content-handoffs/{handoffId}/queue-view` 合同严格校验后只存公开投影；strict decode 拒绝 `priority` 等未知字段；跨租户 404。
- [x] 顾客零新生成：顾客页/发布域不引用制作/队列/生成入口；公共快照只读 `GetAdoptedVersion`；`custpublish` 无模型/队列调用。
- [x] 保留 In Progress 与原 Touch G1 归属；未改 materials 独占、未重做旧票、未接管顾客/奖励组。

## Tests

- `cd web && npx vitest run` => **65 文件 / 673 用例全过**（基线 59/625；新增 6 个 mx-ops 测试文件）。
- `cd server && go build ./...` => OK
- `cd server && go test ./internal/mxopsview/ ./internal/store/ ./internal/httpapi/ -count=1` => 全绿（mxopsview 新增；store/httpapi 含新增用例）
- `go vet ./internal/mxopsview/ ./internal/httpapi/ ./internal/store/` => OK
- 新增测试：web 6 文件（queue/execution/return/production/panel/visitor）+ Go 2 文件。

## Code review

- Standards: PASS（gofmt 干净；沿用仓内 `tk-*`/source-contract 测试惯例；合同类型为原样副本，无双份语义）。
  Judgement notes：①三仓同形内核是有意的独立部署副本，不抽共享包（避免新增发布物）；②queue-view 同步接缝当前走 session+manage 权限，真实接线应改为受信服务身份（已列 UNKNOWN/偏差）；③旧 `queue` 仅保留读取器，不改变冻结语义。
- Spec: PASS（对照工单 2026-10-18 增量逐条：偏好/队列/ETA/夜间窗口/费用/候选/顾客零生成均有实现与测试）；缺口如实列 UNKNOWN：真实投影未接、夜间执行/账务未跑、touch 创建侧偏好 UI 仍受制作引擎未接通限制。

## Blocked / UNKNOWN

1. 真实星模 `job_queue_view` 投影未接：当前由 `POST .../queue-view` 显式同步接缝接收（fixture/未来上游 push），未连通真实运行时 → `UNKNOWN（未授权实机窗口）`。
2. 同步接缝受信服务身份：**已收口（W5，2026-10-19）**。新增 `requireServiceProducer` 双 auth：
   受信服务用 `X-Service-ID`+per-service `X-Service-Token`（`TOUCH_SERVICE_TOKENS` registry）写 queue-view，
   无需 session/共享 internal token/`X-Tenant-ID`；tenant 从 handoff 行反查；旧 session+`manage_content_packs`
   路径保留且字节/状态码不变（双 auth 过渡）。真实 Runtime 推送仍 UNKNOWN（3836 窗口）。
3. 真实夜间窗口执行/GPU 排队/回流：`NOT_RUN`；fixture 全链路通过不等于生产通过。
4. 真实费用（商家工具费/活动预算/模型成本）：未接账务，UI 显示 `未提供`；`NOT_RUN`。
5. 并发写者事件：实现期间同 worktree 出现非本代理写入的服务端文件（00:50–00:51），随后又出现/回滚未提交的 A6 文件（00:53–00:54）。当前 HEAD 内代码已由本代理复核、测试通过；若另有 G08-3553 执行者，请 Lead 协调，避免再次覆盖。
6. 真实跨应用交接（1745/GoBoost/AiCut/Motion）：`NOT_RUN`，归对应票。

## Real-integration status

未在真实 GPU/生产验证（UNKNOWN）。可执行验收：HUI-3836 窗口内由受信运行时推送 `job_queue_view` → 商家页显示 phase/ETA；夜间任务排队→离场→回原 job；回流候选→显式采用；访客多次触碰 provider submit 数不变（当前由源码/守卫证据证明，真实计数待窗口）。

## Linear receipt

Lead 统一回报；本代理不写 Linear。

---

## W5 收口：queue-view 受信服务生产身份（2026-10-19）

- 分支：`mx-ops/3553-trusted-identity`（base `origin/main ea11bd3`，含 #65）；worktree `.worktrees/mx-ops-touch-identity`
- 合同：`mx-ops-c0/v1 @ 6ffc90ca`；**无 DB 迁移/无新表/无新服务端口**；`order-receipt/v1`/公共 API 零改动
- 改动：
  - `server/internal/config`：`TOUCH_SERVICE_TOKENS`/`TOUCH_SERVICE_ROLES` per-service registry（默认空=nil）；
    坏项/重复 id/未登记服务角色 → `Gate()` problem，registry 整体 fail-closed 不部分生效；token 只存内存。
  - `server/internal/httpapi/server.go`：`requireServiceProducer` 中间件（`X-Service-ID`+`X-Service-Token`，
    `subtle.ConstantTimeCompare`，`queue_producer` 角色；无服务头精确委托旧 `requireSession`；
    服务路径优先；服务 token 不进日志/错误体）；queue-view 路由切换到该中间件。
  - `handlers_content_packs.go`：handler 双路径分叉——服务生产身份用 store 按 id 反查行拿 tenant（忽略 `X-Tenant-ID`），
    写入仍走 `mxopsview.Parse` + `PublicJSON()` 严格校验/64KiB；旧 session 路径原样；结构化审计
    `mx_ops_queue_view_producer service/handoff/tenant/job_ref/result`（无 token/body）。
  - `server/internal/store`：新增 `GetContentHandoffByID`（无租户过滤，调用方必须用返回行 tenant；未知=ErrNotFound）。
  - 运维：`docs/mx-ops/3553/README.md`（env/headers/状态码/轮换/回滚/0600 纪律）。
- 测试实际输出（2026-10-19，同一 head 内容）：
```
$ cd server && go build ./...                                                    => BUILD=0
$ cd server && go vet ./...                                                      => VET=0
$ cd server && go test -count=1 ./internal/config/ ./internal/store/ ./internal/httpapi/ ./internal/mxopsview/
  ok  internal/config      0.294s
  ok  internal/store       75.163s
  ok  internal/httpapi     98.767s
  ok  internal/mxopsview   0.713s
  => TEST=0
```
  新增测试：config 3（registry 解析/默认关闭/坏项 fail-closed）、store 1（ByID found/404）、
  httpapi 11（中间件：服务路径/优先级/拒绝矩阵×5/旧路径精确委托；端到端：写入/租户头忽略/
  未知 404/坏合同+超限 parity/无 registry 503/审计）。
- 旧路径回归：既有 `TestContentHandoffMXOpsSeam`/`TestContentHandoffQueueViewRejectsBadContract`
  等全部保持；显式断言旧响应键集合与 `job_queue_view` 投影与服务路径逐键相等。
- UNKNOWN：①真实星模 Runtime 推送 = HUI-3836 授权窗口（未接）；②真实 ETA/队列语义未联调；
  ③真实跨应用交接/账务不在本项；④生产部署/轮换演练未执行（runbook 已备）。
- 并发/过程注记：本项为 W5 独立收口，工作树无其他写者；PR-only 不 merge/deploy。
