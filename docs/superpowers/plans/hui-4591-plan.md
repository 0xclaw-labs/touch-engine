# HUI-4591 实施计划｜订购、收费与账单（Wave2）

- worktree: /Users/jimfu/works/派诺生态/wt-touch-4591 ｜ 分支: touch/hui-4591-billing ｜ 基线: ce47dde（wave1 已合流）
- 迁移号段: 0108-0112 ｜ 端口: web 18356 / go 18256 ｜ 报告: touch-all-20261109/reports/hui-4591-report.md
- 权威: briefs/hui-4591.md 裁定书（Linear 票面 MCP 当前不可用，裁定书+派遣单为需求权威；报告如实声明）

## 铁律（裁定书逐条）

1. 本仓只存**可重建投影**：subscription_ref / entitlement_projection / order_ref + 幂等键。**不自增余额**，任何表没有 balance 列；公共订单权威（派诺生态统一账单）语义诚实投影。
2. 价格目录**配置不写死**：价格/币种/服务期/门店档位/加店单价/阶梯全部来自目录行（经受信权威缝写入），Go 代码零价格常量；订单引用价格快照，目录变更不改历史订单。
3. 四态开通（F16）：处理中 processing → 开通中 opening → 已开通 active ｜ 失败 failed。只有**受信服务缝送达的真实服务端支付事实**才推进状态；浏览器返回（收银台跳回）只是读面回查，永不当支付证明。
4. 幂等开通：重复回调（同 source+event_id）只读回不重复收费/开通；同键不同负载 409 IDEMPOTENCY_CONFLICT。
5. 续费扩店（F17）：未到期续费从 paid_until 起算（不丢剩余天数）；已到期从付款确认时刻起算；扩店差价按价格快照的加店单价/阶梯；降档生效日=当前服务期届满；已承诺权益不追溯削减。
6. 到期与暂停（F18）：订阅到期只限新增收费能力；有效旧券继续查验/核销（1671 已落，本票只把判定源换真）；明确维护保留期（paid_until + grace_days 展示，不删除任何事实）。
7. 退款发票对账（F19）：原订单/退款金额/原因/开票主体/发票状态/审核人逐字段落库；重复回调不重复退款；退款金额不超原订单未退余额；核销（活动账）与现金账分开（activityspend 纪律不动）。
8. AI 用量（F13 投影）：投影自 4595 的 quote/charge 引用；浏览/复制不计费由调用侧约束；投影行幂等、失败/退款状态可查。
9. 硬件订单（F14）：一次性商品/服务明细；付款/出库/写卡/回读/现场验收**五状态独立记录**（操作者/时间/设备标识），不可跳跃。
10. 4593 接线：agent 发起订阅订单时，付款授权**服务端解析**激活代管关系（renewal 动作 + payer_grant_ref 非空），快照落单——payer_grant_ref 由占位字符串变成可解析真值；account-separation/charges 的 PayerAuthorized 从硬编码 false 换成服务端裁决。
11. 1671 接线：订阅到期判定从进程级 Cfg 换成**按租户事实**（billing_subscriptions.paid_until）；无投影行的租户回退 Cfg（旧行为逐字节不变）。
12. 真实支付网关无凭据 = 工程面完整 + 诚实未验证：受信权威缝无 token 注册时 503 fail-closed；/admin/preview 保持演示定位不动。

## 交付物

### A. 迁移 0108-0112（server/internal/db/migrations/）

- `0108_billing_price_catalog.sql`：billing_price_catalog——scope_tenant(''=全局默认)/plan_code/pricing_version 唯一；price_ref/currency/price_minor/service_days/included_store_count/extra_store_price_minor/tier_json/effective_from/effective_to。
- `0109_billing_subscription_projection.sql`：billing_subscriptions——tenant_id 唯一；subscription_ref/plan_code/pricing_version/status(unconfirmed|processing|opening|active|expired)/paid_until/grace_days/store_limit/entitlement_projection(JSON)/last_order_id。
- `0110_billing_orders.sql`：billing_orders（id/tenant/kind(subscription|hardware)/action(new|renew|expand|downgrade|purchase)/plan_code/price_ref/price_snapshot JSON/amount_minor/currency/status(processing|opening|active|failed|cancelled)/order_ref/payment_ref/idempotency_key UNIQUE(tenant,key)/payer_grant_ref/agency_relation_ref/payer_principal/actor_principal/actor_role/fail_reason/created_at/updated_at）+ billing_order_events（UNIQUE(tenant_id,source,event_id)，payload JSON）。
- `0111_billing_ai_usage.sql`：billing_ai_usage——UNIQUE(tenant_id,quote_ref)；capability/quantity/amount_minor/currency/pricing_version/state(confirmed|consumed|refunded|failed)/business_ref/consumed_by_ref。
- `0112_billing_hardware_refunds.sql`：billing_hardware_items（订单明细：sku/name/kind(goods|service)/qty/unit_amount_minor）+ billing_hardware_fulfillment（order_id 唯一；五状态各 (state,operator,occurred_at,device_ref) 列组）+ billing_refunds（UNIQUE(tenant_id,source,event_id)；refund_ref/amount_minor/reason/invoice_title/invoice_status/invoice_ref/auditor）。

### B. 纯领域包 server/internal/billing/（TDD：billing 包测试先行）

| 文件 | 内容 |
|---|---|
| catalog.go | CatalogEntry/ResolveCatalogPrice（租户目录>全局，生效窗口，plan_code 匹配）/BuildOrderSnapshot（快照=引用不改写）；零价格常量 |
| activation.go | 四态机 ApplyOrderEvent：payment_paid→opening（真实服务端支付事实才放行）；activation_active→active+订阅投影载荷；activation_failed→failed；同键重放只读回；同键异负载 ErrIdempotencyConflict；非法跃迁 ErrInvalidTransition |
| renewal.go | RenewalBase（未到期= paid_until 起算；已到期=付款确认时刻）；ExpansionQuote（差价=加店单价×新增数，阶梯 tier_json 可解析）；DowngradeEffective（=当前 paid_until）；引用价格快照 |
| expiry_projection.go | SubscriptionFact{Status,PaidUntil,GraceDays,...}；EffectiveStatus(now)（active 且过 paid_until → expired）；GrantDeadlinesFor→rewardops.GrantDeadlines{SubscriptionUntil=paid_until}；无行回退 Cfg 的规则由调用侧实现（本包只判事实） |
| refund.go | ApplyRefundEvent：幂等（同 source+event_id 重放）；金额>未退余额拒绝；必填 reason/invoice_title/auditor；重复回调不重复退款 |
| hardware.go | 五状态机 hardwareStepCanTransition（payment→shipment→write_card→readback→acceptance，不可跳跃/不可倒退）；StepFact{Operator,OccurredAt,DeviceRef} |
| quote.go | **4595 seam（exported 契约）**：QuoteProjection/QuoteRefQuery/QuoteRefVerdict/AdjudicateQuoteRef（纯函数）；拒绝码 quote_not_found/quote_cross_tenant/quote_not_confirmed/quote_already_consumed/quote_capability_mismatch |
| payer.go | PayerGrantSnapshot{PayerGrantRef,RelationRef,GrantRevision,PayerPrincipal}；DecidePayerForOrder（org_owner=本人付款；agent=激活关系含 renewal 且 payer_grant_ref 非空→快照，否则 payment_authorization_required） |

### C. store 层 server/internal/store/store_billing.go

- 目录：UpsertBillingCatalogEntry/ListBillingCatalog(tenant,at,plan)
- 订单：CreateBillingOrder（幂等键重放）/GetBillingOrder/ListBillingOrders/ApplyBillingOrderEvent（事务内：事件去重→领域机判定→状态推进→订阅投影 upsert）
- AI 用量：RecordBillingAIUsage（幂等）/GetQuoteProjection/ConsumeQuoteProjection/ListBillingAIUsage
- 硬件：CreateHardwareOrder（明细落库）/ApplyHardwareStep/GetHardwareOrder/ListHardwareOrders
- 退款：ApplyBillingRefund/ListBillingRefunds
- 订阅投影：GetBillingSubscription/StoreUsage(tenant)=limit vs 活跃门店数
- 窄补丁：db_test.go 表清单加 8 表（加行）

### D. HTTP 面 server/internal/httpapi/handlers_billing.go（+ authority seam）

商家面（requireSession；reads=authz.Can billing:view；订阅下单=org_owner 或 agent 服务端 payer 解析）：
- GET /api/v1/billing/subscription ｜ GET /api/v1/billing/price-catalog
- POST /api/v1/billing/subscription-orders（kind=subscription；action/new|renew|expand|downgrade；idempotency_key 必填；agent 走 payer.go 服务端解析 payer_grant_ref——客户端传值一律忽略）
- POST /api/v1/billing/hardware-orders（一次性明细）
- GET /api/v1/billing/orders（kind 过滤）｜ GET /api/v1/billing/orders/{id}（状态回查=诚实投影读，含事件史）
- GET /api/v1/billing/ai-usage ｜ GET /api/v1/billing/hardware-orders ｜ GET /api/v1/billing/refunds

受信权威缝（requireBillingAuthority：X-Service-ID + 独立 token + billing_authority 角色；同 requireOpsGrantProducer 模式，registry 空=503 fail-closed）：
- POST /api/v1/billing/authority/events——统一事件口（source/event_id/occurred_at/tenant_id/type/payload）；type：order_registered/payment_paid/activation_opening/activation_active/activation_failed/refund_registered/ai_usage_recorded/hardware_step；幂等/冲突语义同铁律 4。

server.go 窄补丁：一个注册块（12 行）。config.go 窄补丁：TOUCH_CASHIER_APP_ID/TOUCH_CASHIER_TARGET_ID 两键（收银台跳转参数；空=页面诚实显「生态收银台未接入」）。handlers_auth.go whoami features 窄补丁 1 行（billing_cashier 下发 app_id/target，空=无）。

### E. 两接线（窄补丁）

1. handlers_reward_ops.go `rewardPromiseDeadlines`：订阅期限改读按租户事实（billing.SubscriptionFact→paid_until；无行回退 Cfg 现逻辑，逐字节保留）。
2. handlers_activity_spend.go `separationFor`/`handleAccountCharge` + handlers_workbench.go：订阅状态与 PayerAuthorized 换服务端解析（fact-first，Cfg 回退；PayerAuthorized=Can(billing:manage_payer)）。

### F. M12 页面（web）

- web/src/lib/billing.ts：视图类型+文案收敛（四态/五状态/退款发票状态中文）。
- web/src/components/work/billing/billing-desk.tsx：四页签（套餐与门店额度｜订单与支付｜AI 用量｜发票与退款）；续费/扩店→下单→收银台跳转（session.api eco-nav/switch，whoami features.billing_cashier 空=诚实未接入）+ 订单状态回查（GET orders/{id}）；ai_usage 只读投影（浏览/复制不计费文案）；负例错误码展示。
- web/src/app/work/billing/page.tsx：薄壳（零 census 标记）。
- work-frame LINKS + 账单；page-map.ts + /work/billing(billing 模式)；page-census.ts markerBook + 空标记行；page-census.test.ts 两处 18→19 + want map 加行；web/tests/billing-page.test.ts。
- /admin/preview：不动（保持演示定位）。

## TDD 任务序列（每任务红→绿→commit）

1. 迁移 0108-0112 + db_test 表清单（红=表缺失）→绿
2. billing/catalog.go + 测试（解析/窗口/租户优先/零硬编码）
3. billing/activation.go + 测试（四态/幂等重放/异负载冲突/浏览器返回不推进）
4. billing/renewal.go + 测试（续期起算两分支/扩店差价/阶梯/降档生效日）
5. billing/expiry_projection.go + 测试（EffectiveStatus/六到期 paid_until 对齐）
6. billing/refund.go + 测试（幂等/超退拒绝/必填字段/不重复退款）
7. billing/hardware.go + 测试（五状态顺序/跳跃拒绝/事实记录）
8. billing/quote.go + 测试（4595 契约五拒绝码/已消费/跨租户）
9. billing/payer.go + 测试（owner/agent 两分支/payer_grant_ref 缺失拒绝）
10. store_billing.go + 测试（建单重放/事件事务/订阅投影 upsert/消费 quote）
11. handlers_billing.go 商家面 + HTTP 测试（授权矩阵/负例/幂等键）
12. authority seam + HTTP 测试（registry 空 503/事件幂等/冲突 409/完整闭环 processing→opening→active）
13. 接线 1+2 + 现有测试回归（T16/activityspend/agency 全绿证明零破坏）
14. web billing.ts/billing-desk/page + vitest
15. code-review 自审 + go test ./... + vitest run + next build + gofmt/vet

## 验收（本票核心）

- T01 完整商家开通：下单→受信支付事实→四态→active→订阅投影→M12 展示；agent renewal 通道含 payer 真值。
- T15 支付与账务：幂等回调不重复开通/退款；浏览器返回不当证明；无凭据 503 诚实；订单引用价格快照。
- T16 多种到期组合：订阅到期只拦新承诺，旧券查验/核销不拦（接线后按租户事实判定）。
