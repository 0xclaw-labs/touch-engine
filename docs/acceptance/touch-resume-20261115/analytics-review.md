# HUI-4665｜2026-11-15 统计续验与费用窄修复

状态：本轮费用缺陷已由红测定位并修复，定向工程回归通过；等待主代理集成后的独立正常浏览器复验。不是本票全部验收，也不是正式支付验收。

- 起点：Touch `9eff481d9ab4e5477c39c75f4d4baccda821de74`。
- 分支：`codex/touch-analytics-20261115`；工作树 `.worktrees/resume-analytics`。具体交付 SHA 见本次提交及交接记录。
- 已核对 [HUI-4665](https://linear.app/huigoo/issue/HUI-4665) 最新正文与 2026-11-14 最终评论，以及 `docs/acceptance/touch-user-jobs-20261114/reports/analytics.md`、`reviews/analytics-review.md`。票面仍为 In Review；迟到、更正、撤回、实际费用的完整组合验收未完成。
- 本轮使用新建的 `t.TempDir` 测试数据库。上一轮临时数据库已经丢失，以下结果不声称是原数据库、原记录复验。

## 一、确认的根因与红测

### 1. 已支付订单在开通中或开通失败时漏计

原 `DashboardOps` 只汇总 `billing_orders.status='active'`。原账单状态机接受 `payment_paid` 后状态为 `opening`，之后可变成 `failed`，支付事实仍然存在。统计因此把已支付金额错误变成未知，并把单数降为 0。

新增 `TestDashboardPaidOrdersSurviveActivationStateChanges` 使用原 `CreateBillingOrder`、`ApplyBillingOrderEvent` 写路径：创建订单 → 付款事件 → 相同事件重放 → 开通失败。修前订阅/硬件各在 opening、failed 失败，共四条：

```text
subscription paid order in opening disappeared: amount=<nil> orders=0; want 1200 and 1
subscription paid order in failed disappeared: amount=<nil> orders=0; want 1200 and 1
hardware paid order in opening disappeared: amount=<nil> orders=0; want 1200 and 1
hardware paid order in failed disappeared: amount=<nil> orders=0; want 1200 and 1
```

### 2. 付款晚到时落在订单创建日，且仅 active 投影会被猜成支付

新增 `TestDashboardPaymentFactsUseAuthorityWindowAndDeduplicateOrder`：订单在 10 月 13 日创建，权威付款发生于 14 日，15 日收到；16 日收到另一标识的相同付款回调。另一个订单 14 日创建、15 日付款；历史 active 行没有付款事件。修前 14/15 日断言均失败。

修后按同租户、同订单最早 `payment_paid.occurred_at` 计一次，分别落到 14 日的 1200/1 单、15 日的 800/1 单；13/16 日没有可用付款金额。没有原付款事件的 active 行不再证明已支付。

### 3. 订阅退款混入硬件退款，迟到退款落错日

原查询把所有 `billing_refunds` 都称为硬件退款，且按退款投影 `created_at` 落窗。

新增 `TestDashboardHardwareRefundsUseOrderKindAndAuthorityWindow` 沿原退款写路径，产生订阅退款 600、硬件部分退款 300+200，逐条重放。修前：

```text
同日到达：hardware refund amount/count=1100/3, want 500/2
次日到达：hardware refund amount/count=0/0, want 500/2
```

修后订阅/硬件退款按原订单类型分别展示，均按退款 `occurred_at` 落窗。再退硬件剩余 500 后，退款合计 1000/3 笔，原硬件付款仍为 1000/1 单；部分退款和全额退款都不改写原付款金额。

### 4. 不同币种与缺币种仍被合成完整费用

`TestDashboardPaymentAmountsRequireOneKnownCurrency` 修前证明 CNY+USD、CNY+空币种均返回非空合计。修后保留可证明的付款/退款笔数，金额仅在每条金额有效且币种一致、非空时可用；否则保持未知，不换汇、不用部分 SUM 冒充完整金额。已知的 CNY 零金额仍返回真实 0。

React 静态渲染修前也有两项失败：订阅退款没有单独呈现；缺币种的裸金额仍显示为完整费用。修后有币种、退款分列及开通失败说明，缺币种不输出裸金额。

## 二、修改边界与事实口径

- 新增 `store_dashboard_payments.go`，只读取已有 `billing_order_events`、`billing_orders`、`billing_refunds`；不创建收费/余额/退款权威，不更改生产者写路径。
- 付款以权威 `payment_paid` 事件确认，以原订单冻结金额和币种取值；按订单去重、按原付款时间落窗。当前开通状态不代替支付事实。
- 退款仍来自原退款权威投影，订阅和硬件分列；部分退款、全额退款均不冲销原付款。
- API 保留原字段名，新增付款/退款币种和订阅退款字段。`hardware_refund_minor` 在金额不可证时省略，客户端按已有缺值规则显示未知；真实本地零条退款仍返回 0/0。
- 更新来源、去重、时间窗口和开通失败说明。原 AI 用量状态与时间口径不改；本轮币种完整性修复范围是订阅/硬件付款及退款。
- 原固定费用夹具补入显式 `payment_paid` 事件，不能再仅用 active 行证明付款。这些输入明确是工程事实，未被称为真实交易。
- 没有修改身份/SSO、账单生产者、发布生产者、权益生产者、门店/动作目录、工作台首页或今日摘要。A01–A30、28 业务视图、门店为根/活动可选的边界不变。

## 三、API／独立复算／页面同一事实

`TestDashboardPaymentFactsMatchIndependentLedgerAndPageFixture` 先通过原订单及事件写路径构造新工程数据库，再经真实统计 HTTP 读取；另外直接逐行读取付款/退款账，独立在测试里去重和复算，不调用统计聚合器。HTTP 和 React 渲染共用手工复算期望：`server/internal/httpapi/testdata/analytics-payment-facts.json`。

| 事实 | 单位为最小货币单位的 API/复算值 | React 显示 | 条件 |
|---|---:|---|---|
| 订阅付款 | 1200 / 1 单 / CNY | 12.00，1 单，CNY | 已付款，后来开通失败；原付款保留 |
| 订阅退款 | 300 / 1 笔 / CNY | 3.00，1 笔，CNY | 单独呈现 |
| 硬件付款 | 2000 / 1 单 / CNY | 20.00，1 单，CNY | 仍在 opening；不等待交付 |
| 硬件退款 | 500 / 2 笔 / CNY | 5.00，2 笔，CNY | 两次部分退款；重复回调不增加 |

付款/退款均在 10 月 14 日发生、15 日收到，统计窗口使用原发生日。该证据是“真实应用 HTTP + 独立复算 + React 静态渲染”，不是正常浏览器操作，也不是正式付款。

## 四、已执行验证

先加载环境：

```bash
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
```

定向 Go 回归（最后相关逻辑版本）：

```bash
cd server
go test ./internal/dashboard ./internal/store ./internal/httpapi -run 'TestDashboard(PaidOrders|Payment|HardwareRefunds|Ops|ScopedStaff|ContentCost)|TestBuildOperations|TestCostReconciliation|TestOperationsSummary|TestLeadReceipt.*(Revoc|Replay)' -count=1
```

结果：dashboard 0.006s、store 7.146s、httpapi 2.781s，全部通过。其中最后的 `TestLeadReceipt...` 模式没有命中原用例名称，故撤回/重放单独按准确名字重跑如下，不把无匹配当作通过证据：

```bash
go test ./internal/store ./internal/publishhandoff -run 'TestAck(LeadReceiptReplayIdempotent|StoredOnRevokedLeadIsWaterlineBlocked|ReceiverClaimedRevokeRecordsOnly)|TestCancelThenLateReceiptDoesNotResurrect|TestReceiptReplayAndOutOfOrder|TestAppendFactDedupAndConflict|TestPlatformCallbackVerifiesAndConfirms|TestStaffConfirmOnV2Row' -count=1 -v
```

结果：store 3 项通过（0.789s）；publishhandoff 5 项通过（1.330s）。覆盖旧 ACK 重放幂等、撤回水位、接收方撤回申报不倒推本地状态、取消后晚到官方回执不复活、官方回执乱序/重放、v2 同事件冲突及店员证据不升级为官方。

Web：

```bash
cd web
npm test -- --run tests/analytics.test.ts tests/analytics-render.test.ts tests/analytics-page.test.ts
npx tsc --noEmit
```

结果：3 文件 / 60 项通过；TypeScript 通过。npm 的已有环境 `http-proxy` 配置提示与 Vite CJS 弃用提示仍出现，不影响退出状态。

共享期望移入 Go `testdata` 后，单独再跑 `go test ./internal/httpapi -run '^TestDashboardPaymentFactsMatchIndependentLedgerAndPageFixture$' -count=1`（0.188s，通过），`npm test -- --run tests/analytics-render.test.ts`（12 项通过，49ms）和 `npx tsc --noEmit`（退出 0），避免服务端测试依赖 Web 测试目录。`git diff --check` 通过。

本组按协调要求没有另跑完整 Go/Web 全套或启动第二套浏览器服务；统一候选的全套测试、构建和服务生命周期由主代理处理。

## 五、原验收项的复用证据与独立浏览器待办

| 项目 | 本轮工程依据 | 正常浏览器待验 |
|---|---|---|
| 空组织、只有门店、确认零/缺字段/无权 | 原空库 HTTP、解析器与真实 React 渲染继续通过 | Terra 用新环境从首页正常进入统计；390/430px/桌面检查 |
| 重复、晚到、更正、同参与多证据 | 原 `TestDashboardOpsRealWalkthrough`、固定事实集、六层事实、冻结 v1/v2、跨店归因继续通过 | 同一业务参与，从原业务记录进入统计，逐项记录来源和范围 |
| 撤回旧事件不得复活 | 上述 8 项原写路径回归通过；统计撤回当前态/线索分层回归通过 | 正常撤回与回到统计的页面观察仍待独立复验 |
| 切店/撤权旧响应 | 原 scoped staff、越店拒绝、撤权摘要、账单权限 HTTP 回归通过；未确认新的客户端根因 | 延迟旧请求后切组织；已加载 A 店后应用 B 店；另一账号撤权后真实窗口回焦及刷新。旧数据立即隐藏且迟到响应不得覆盖 |
| 费用 unknown | 本轮付款/退款红→绿、原内容金额缺失和权益冻结成本完整性通过 | 本候选同一份费用事实在正常页面查看与独立复算；工程费用输入必须明确标识 |

建议正常路径为：`/` 首页 → “统计” → `/work/analytics` → 门店筛选/来源口径 → 刷新 → 返回首页。不要使用不存在的 `/work` 首页路径；不通过 SQL 改库制造成功。若使用网络延迟/503/撤权故障注入，只作为读边界工程演练，不能当正式业务成功。

仍未验：新候选的正常浏览器全过程、所有宽度与真实回焦撤权组合；真实 Identity、真实账单权威回调、正式支付/退款/AI 实付、真实外部平台发布和实卡/NFC。旧截图及已丢失临时库均不作为本轮新证据。HUI-4665 不据此标 Done。
