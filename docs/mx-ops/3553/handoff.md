# MX-OPS handoff — HUI-3553 碰一碰商家内容链增量

- Group / Ticket: G08 / HUI-3553
- Repo: 0xclaw-labs/touch-engine
- Branch: `mx-ops/3553-night-content-return`
- Base: `a2a2ac5`（origin/main）
- Head: 本分支最新 commit（PR 记录）
- Worktree: `.worktrees/mx-ops-3553-touch-engine`
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

- Standards: 待补（分支收尾前执行）。
- Spec: 待补。

## Blocked / UNKNOWN

1. 真实星模 `job_queue_view` 投影未接：当前由 `POST .../queue-view` 显式同步接缝接收（fixture/未来上游 push），未连通真实运行时 → `UNKNOWN（未授权实机窗口）`。
2. 同步接缝当前挂在 session + `manage_content_packs` 权限下（沿用仓内既有实现）；合同语义上队列投影应由受信运行时/服务身份推送。真实接线时应改为 internal 服务身份，并补「生产者身份」校验 → 列为已知偏差，不影响本票 fixture 验收，但真实集成前必须收口。
3. 真实夜间窗口执行/GPU 排队/回流：`NOT_RUN`；fixture 全链路通过不等于生产通过。
4. 真实费用（商家工具费/活动预算/模型成本）：未接账务，UI 显示 `未提供`；`NOT_RUN`。
5. 并发写者事件：实现期间同 worktree 出现非本代理写入的服务端文件（00:50–00:51），随后又出现/回滚未提交的 A6 文件（00:53–00:54）。当前 HEAD 内代码已由本代理复核、测试通过；若另有 G08-3553 执行者，请 Lead 协调，避免再次覆盖。
6. 真实跨应用交接（1745/GoBoost/AiCut/Motion）：`NOT_RUN`，归对应票。

## Real-integration status

未在真实 GPU/生产验证（UNKNOWN）。可执行验收：HUI-3836 窗口内由受信运行时推送 `job_queue_view` → 商家页显示 phase/ETA；夜间任务排队→离场→回原 job；回流候选→显式采用；访客多次触碰 provider submit 数不变（当前由源码/守卫证据证明，真实计数待窗口）。

## Linear receipt

Lead 统一回报；本代理不写 Linear。
