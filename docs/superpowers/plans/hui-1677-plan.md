# 计划｜HUI-1677 经营统计（TOUCH-ALL-20261109-v1 轮）：动作漏斗、证据分层、真实线索与费用对账

worktree: /Users/jimfu/works/派诺生态/wt-touch-1677 ｜ 分支: touch/hui-1677-analytics ｜ 基线: bafb03a（wave1+wave2 全合流）
迁移: 0116-0118（**裁定：零新增迁移**——六层/线索/费用/待办全部消费既有生产者表：action_session_events(0058)、store_service_events(0080)、publish_handoffs/events(0034/0035)、customer_publish_attempts(0010)、reward_benefits/official_facts(0024/0025)、lead_submissions.receiver_state(0088-0090)、billing_orders/ai_usage/refunds/subscriptions(0108-0112)、store_actions(0043)。纯只读聚合不落任何汇总表。）

## 现场结论（前任遗留）
worktree 唯一脏项 = untracked `web/node_modules`（npm 安装产物，非代码）。无可复用实现，弃；本票从零实现，不动该目录。

## 六层证据 → 真实事实映射（每层带 definition 披露，绝不相加成自然人数）
| 层 | key | 来源表 | 去重键 |
|---|---|---|---|
| 1 点击 | action_clicked | action_session_events kind='created'（3546 动作页会话，distinct session_id）+ store_service_events kind='open'（4589 卡片打开，逐事件行） | session / 事件行 |
| 2 唤起尝试 | invoke_attempt | store_service_events kind='jump_attempt'（4589）+ publish_handoff_events kind='editor_open_proved' distinct handoff_id（1670 可证实唤起，比尝试强，按名披露不冒充尝试） | 事件行 / handoff |
| 3 媒体交接 | handoff_requested/completed | publish_handoffs 创建行（idempotency_key UNIQUE）/ 当前状态 ∈ {handoff_ready,editor_opened,submitting,awaiting_review,unknown,platform_confirmed}（状态机只前进，六态蕴含 ready 已达） | handoff |
| 4 顾客自报 | user_reported | customer_publish_attempts self_reported=1（distinct attempt） | attempt |
| 5 店员确认 | staff_observed/staff_verified | publish_handoff_events kind='staff_saw_submit'/'staff_verified_work'（distinct handoff）+ reward_benefits evidence 同名类（distinct participation，既有三类面板保留） | handoff / participation 分列 |
| 6 平台证实 | platform_confirmed | publish_handoffs status='platform_confirmed'（distinct handoff）+ reward_official_facts（distinct attempt，既有）+ reward_benefits evidence='official_publish_confirmed' | 各自去重 |

## 漏斗下钻
- FilterSet 扩展 `action_id`（不透明 id，SQL 内 tenant/store 再收窄）与 `environment`（白名单=3535 六环境 desktop_browser|ios_safari|android_browser|ios_wechat|android_wechat|target_app）；未知值 400 不静默空集。
- 适用面照实披露（applied_filters）：action/environment 维度只存在于会话/服务事件层；campaign 漏斗各级不适用级照旧披露。campaign 为空的门店服务数据照常可见（不以 campaign 为根）。
- dims 扩展 Actions 引用行（scope 收窄），web 动作筛选下拉有源。

## 线索口径（T18）
- 分列：本地提交（窗口内 lead_submissions，按 capture_source 活动来源/门店留资两分）/ 待同步 pending_sync / 接收方确认 crm_received（receiver_state='stored'，ACK 五元组）/ 已撤销（窗口内提交、当前 revoked）。**待同步不计入已获客**——注记为响应常量必带；纯浏览与微信点击不在任何线索桶。

## 费用对账（T15，4591 投影只读）
- 订阅：billing_orders kind='subscription' status='active'（支付事实投影）SUM(amount_minor)+单数；failed 单数单列（失败不是支出）。
- AI 实际费用：billing_ai_usage 按 state 分列（confirmed/consumed/refunded/failed 各 SUM+计数；refunded 不计支出）。
- 硬件：billing_orders kind='hardware' status='active' SUM − billing_refunds SUM（退款单列不减写，分列披露）。
- 权益兑现成本：既有 reward cost（issued/redeemed 分列）；内容生成费与券面额注记沿用。全部带 definition（来源表/去重键/事件时间），来源引用（order_ref/quote_ref）计数可回查到 /work/billing。

## summary 扩展（B13+首页待办；老字段逐字保留）
新增 `todos` 块：unavailable_actions（停用/撤销/过期动作，当前态）/ missing_content（启用且未绑定已采用内容）/ authorization_expired（动作 expires_at 已过 + 渠道准入 admitted 且 valid_until 已过，分列）/ pending_confirm（既有口径）/ pending_redeem（issued 未核销当前态）/ billing_anomalies（failed 订单数）。每项带 definition。

## HTTP 面
- GET /api/v1/dashboard/operations：响应追加 evidence_layers / leads / cost_reconciliation 三块 + filter_echo 扩展；老字段零改动。
- GET /api/v1/dashboard/operations-summary：追加 todos 块；老字段零改动（2628 契约不破坏）。

## Web M10（/work/analytics）
- lib/analytics.ts：类型扩展 + action/environment 筛选 + 新块渲染数据；analytics-view.tsx：筛选区 +（动作下拉/环境下拉）、证据六层区、线索口径区、费用对账区；未知保持未知文案纪律不变。

## 任务序列（TDD 逐任务红→绿→commit）
1. dashboard 包：FilterSet 扩展校验（env 白名单/action id）。
2. dashboard 包：EvidenceLayersPanel/LeadsPanel/CostReconciliation/TodoSummary 类型 + BuildOperations/BuildOperationsSummary 纯函数扩展（含 idempotence 测试）。
3. store：DashboardOps 六层 SQL（含 scope/filters/窗口/跨租户 0 行）。
4. store：线索/费用对账 SQL + dims.Actions。
5. store：DashboardOpsSummary todos facts。
6. httpapi：两 handler 扩展（老消费者字段回归断言）。
7. httpapi e2e：走查夹具扩展（服务事件经真实 store 写路径、billing 经 store 投影方法、线索经真实 ACK 语义种子）+ 独立 SQL 复算对照。
8. web lib + page 扩展 + vitest。
9. 全量 go test ./... + vitest run + code-review 自审 + 报告 reports/hui-1677-report.md。

## 核心验收映射
- T03 分渠道互不覆盖：六层/环境维度各自独立计数，跨租户 0 行。
- T10 官方回执只补证据：platform_confirmed 层与 rewards 层分列，人工先发官方晚到不重复计数（official facts distinct attempt 已有测试语义沿用）。
- T15 费用口径：订阅实付/AI/硬件/权益兑现四分明细；券面额不是支出；退款分列不冲销。
- T18 线索口径：pending_sync ≠ 已获客；receiver ACK 才进已获客桶。
