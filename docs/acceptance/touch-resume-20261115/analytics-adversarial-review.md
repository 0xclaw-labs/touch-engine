# 2026-11-15 统计费用独立对抗复审

受审：Touch analytics `5d63c98a3a712f3e7c8e636b0ba0ceaa8b92c95f`；基线 `9eff481d9ab4e5477c39c75f4d4baccda821de74`。结论：**暂不建议以本提交宣称费用对账边界完成**。发现一项可复现的坏时间问题，另记录两项经现有合同解释的身份语义。受测库均为 Go 临时数据库；没有浏览器登录、正式支付或外部回执。

## 已复现问题

1. **无法解析发生时间的退款被当作已知零。** 使用 `CreateBillingOrder` 建 1000 CNY 订阅单，原 `ApplyBillingRefund` 接收 300 CNY 退款，`OccurredAt="bad-time"`、接收时间为 2026-10-14。`opsRefunds` 用 `datetime(r.occurred_at)` 与窗口比较，SQLite 将坏时间转换成 NULL，该行不匹配；然后无匹配行分支返回 `Count=0, Amount=0`。无法确定发生时间的本地事实不足以断言窗口内无退款；页面又仅在笔数大于零时显示退款。当前 HTTP 权威入口对非空 `occurred_at` 未解析验证，这不是仅靠手工改数据库才能制造的状态。参见 `server/internal/store/store_dashboard_payments.go` 的 `opsRefunds` 和 `server/internal/httpapi/handlers_billing_authority.go`。
2. **整秒窗口口径校正。** 付款发生 `23:59:59.900Z`、窗口终点 `23:59:59Z` 时，应计入最后一个整秒桶。`windowNoteSecond` 明确规定事件按 UTC 整秒粒度比较、边界含；本审查先前将其按纳秒精度排除的断言错误，已撤回并修正 overlay，不能据此修改业务窗口为纳秒粒度。下一整秒才在窗口外。

以上由 `go test -overlay /tmp/analytics-overlay.json ./internal/store -run '^TestTerraMalformedRefundAndFraction$' -count=1 -v` 验证，退出 1，输出 `accepted refund with invalid occurred_at excluded and reported known zero: count=0 amount=0`；整秒末桶断言已校正。overlay 的源文件为 `/tmp/analytics-adversarial-test.go`，仅供此环境接续；没有修改受审代码。

## 身份语义与范围

- **同一退款引用的新事件**：原 `ApplyBillingRefund` 对同订单、同 `refund_ref`、同 300 CNY 但不同 `event_id` 接收两行，统计输出 2 笔/600。`TestTerraSameRefundRefNewEvent` 实测红。原库唯一约束为 `(tenant_id,source,event_id)`，现行合同以 `(tenant_id, source, event_id)` 为幂等键，不能单凭相同引用断言一定是一笔退款，也不能在统计层静默去重；此测试的单笔预期没有合同依据，不记为已确认缺陷。
- **空付款引用**：原 `ApplyBillingOrderEvent` 接收 `payment_paid`、有效 source/event_id 和时间，但空 `PaymentRef`；统计仍输出 1 单/1000 CNY。`TestTerraBlankPaymentReference` 实测红。现行合同并不要求 `payment_ref` 必填，因而空引用不构成已确认缺陷；此测试的未知金额预期没有合同依据，不应补造引用。

## 复算、范围与回归证据

- 手工 JSON 的 1200/1、300/1、2000/1、500/2 与输入事实逐项一致。HTTP 测试确实直接读取原订单事件和退款表后另行去重/相加，未调用被测聚合器；但 SQL 日期条件、join 形式与实现相近，样本同日，不能独立证明跨日最早事件、异常时间、边界、币种及冲突退款的处理。手工固定期望提供第二个可核算锚点，不等于覆盖这些对抗输入。
- 新付款和退款查询均以 tenant 绑定，订单关联同时核 `tenant_id`，没有发现跨租户 join 漏洞。组织账单不按门店过滤，HTTP 对缺少 `billing:view` 权限者屏蔽整块，界面说明组织范围及业务筛选不适用；该设计不等于每个门店费用分摊。未对所有角色和并发撤权作浏览器实测。
- 混币、缺币、非法或负数金额的匹配行由 `opsMoneyTotal` 保持金额未知，单币有效零保留为零；现有定向测试覆盖主要正常输入。无法解析的时间会在金额完整性判断之前被过滤，这是上述假零缺口。
- 退款原硬件字段从必有整数改为在未知时可省略，新增订阅退款/币种字段；现有 Web 解析和静态渲染覆盖了相应缺值，其他 API 消费者是否依赖旧字段必有仍待契约核验。
- `source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh` 后，定向 `go test ./internal/dashboard ./internal/store ./internal/httpapi -run 'TestDashboard(PaidOrders|Payment|HardwareRefunds|Ops|ScopedStaff|ContentCost)|TestBuildOperations|TestCostReconciliation|TestOperationsSummary' -count=1` 全部通过。Web 复用 canonical `node_modules` 临时软链，`npm test -- --run tests/analytics.test.ts tests/analytics-render.test.ts tests/analytics-page.test.ts` 3 文件/60 项通过；软链已删除。独立 overlay 的上述红测是额外边界，不能被既有绿测抵消。

不把上述工程测试、静态渲染或测试夹具视为正常浏览器、正式身份或真实交易验证。修复后应保留原红测并在同一受测修复候选复跑，另覆盖跨时区、整秒桶及下一秒边界、坏时间与正常事件混合、付款/退款按租户隔离、缺/负数/混币、无权限屏蔽和旧 API 消费者。

## 31fb20d 修复后的独立复验

受测 canonical 提交 `31fb20db87b513446fed19dbdcb923d79c149787`。原独立 overlay 重定向到 canonical `server/internal/store` 后，`TestTerraMalformedRefundAndFraction` 通过：无法解析时间的退款不再显示已知零，末秒小数仍按既有整秒含边界口径计入。该测试只以本地临时数据库运行。

另按完整名称运行 `TestDashboardUnknownRefundTimeNeverBecomesKnownTotal`（订阅/硬件 × 只有未知/已知混未知四子例）、`TestDashboardUnknownPaymentTimeNeverBecomesKnownPartialTotal`、`TestDashboardPaymentWindowUsesInclusiveUTCSeconds` 和 `TestDashboardPaymentFactsMatchIndependentLedgerAndPageFixture`，store 与 HTTP 均通过。未知退款时间按租户和订单类型隔离；同一付款单只要有任何付款事件时间不可解析，就不能用其他回调的 `MIN` 推定最早日。无法归窗的事实使该类金额和币种未知；确定落窗的笔数与无法归窗的数量分列。四个 `*_time_unknown` 字段经 store、HTTP 和页面投影，静态渲染提示未知合计及已确认笔数，不显示部分合计。

此前同一套 Go 定向三包回归重新运行通过；另运行 `npm test -- --run tests/analytics.test.ts tests/analytics-render.test.ts tests/analytics-page.test.ts`，3 文件、62 项通过。原宽泛 Go 测试名称正则没有覆盖 `TestDashboardUnknown...`，故本次额外用上述完整名称逐项执行，不以无匹配代替通过。以上关闭本报告针对坏时间退款假零的工程红例；不扩展为浏览器、正式支付、外部账完整性或整票验收结论。
