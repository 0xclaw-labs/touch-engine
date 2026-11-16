# 付款与退款事件合同只读裁定

日期：2026-11-15（UTC）。范围：HUI-4666 统计复审提出的两个合同问题。仅新增本报告，没有修改账务、统计、数据库迁移或输入校验。

Touch 账务原实现核对基线：`9eff481d9ab4e5477c39c75f4d4baccda821de74`。本轮写报告时 canonical HEAD 为 `422ca4cacc3469fe192e8f78ad44e3fbccaa9aec`；本文重点核对原账务接收合同，并对照主工作树的对应函数，不用本轮统计消费者的新查询倒推生产者合同。public-ai 只读依据固定到 `a0c5b24c0e1e4024e58c33b918531b9a750e6a86`。

## 裁定

| 问题 | 现行合同能确认什么 | 本轮结论 |
| --- | --- | --- |
| 同订单、同 `refund_ref`，不同 `event_id` 是否应去重 | Touch 明确按 `(tenant_id, source, event_id)` 幂等；同订单允许余额内多次部分退款。未找到把 Touch `refund_ref` 定义为稳定唯一资金交易的映射合同 | **不能直接认定统计重复计数，也不能宣称同 ref 多事件已被批准为部分退款。保留明确合同缺口，不在统计消费者增加 refund_ref 去重** |
| `payment_paid` 的 `payload.payment_ref` 能否为空 | Touch 原输入、状态机及已有交付 HTTP 测试均接受为空；付款事实来自有服务身份和 `billing_authority` 角色的生产者 | **按现行投影合同接受。不新增 PaymentRef 必填，不在统计层以其为空排除已接受的付款事件** |

这里的“受信付款事实”是 Touch 对已认证权威生产者的投影信任边界。单独的 `source + event_id` 字符串不证明真实收款；它们是幂等身份，不是渠道验签结果，也不是新的付款授权。

## 一、退款：现行唯一域明确，refund_ref 的生产者映射仍缺

### Touch 证据

1. [HUI-4591 实施计划](../../superpowers/plans/hui-4591-plan.md)“铁律”第 4、7 条及迁移 0112 说明：重复回调按同 `source + event_id` 读回；退款不得超过原订单未退余额。该文件明确其来源为 `briefs/hui-4591.md` 裁定书，并说明当时未从 Linear 读取票面。本次没有把实施计划冒充更高层的已签署跨仓合同。
2. [0112_billing_hardware_refunds.sql](../../../server/internal/db/migrations/0112_billing_hardware_refunds.sql) 的 `billing_refunds`：唯一约束是 `UNIQUE (tenant_id, source, event_id)`；`refund_ref` 为 `TEXT NOT NULL`，没有非空字符串检查或唯一约束；另有 `(tenant_id, order_id)` 普通索引。
3. [store_billing.go](../../../server/internal/store/store_billing.go) `ApplyBillingRefund`：先按 tenant/source/event 查重并比较 canonical payload；新 event 则读取同订单已退总和，做剩余金额校验后新增一行。没有按 `refund_ref` 查重复。因此同 order/ref、不同行 event，只要合计不超订单金额，代码会记录多行；这项是代码行为，不是对其业务合法性的额外背书。
4. 同文件 `refundPayloadJSON` 把 `refund_ref` 放入同事件 payload 比较。它是 payload 的一个事实字段，不是现有数据库的唯一键。
5. [refund.go](../../../server/internal/billing/refund.go) `ValidateRefund` / `ValidateRefundAgainstOrder`：要求正金额、原因、审核人、合法发票状态，并守卫累计金额；未赋予 `RefundRef` 唯一性。
6. [fulfillment_test.go](../../../server/internal/billing/fulfillment_test.go) `TestRefundWithinRemainingBalance` 明确允许同订单剩余余额内再次退款；[store_billing_test.go](../../../server/internal/store/store_billing_test.go) `TestRefundIdempotentAndCapped` 与 [server_billing_test.go](../../../server/internal/httpapi/server_billing_test.go) `TestRefundEventsIdempotentAndCapped` 验证同 event 重放一次、超退拒绝、余额内第二笔入账。后两项的第二笔都更换了 refund_ref 和 event_id，**没有裁定同 ref 不同 event 的业务含义**。
7. [funnel.go](../../../server/internal/dashboard/funnel.go) `CostRecon` 公开退款去重口径仍是 `UNIQUE(tenant,source,event_id)`。统计读取这份投影，不具备另行重定义资金交易身份的依据。

### public-ai 已有强唯一合同，但并非 Touch 字段的自动映射

[HUI-716 原支付退款冻结设计](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/docs/superpowers/specs/2026-09-12-hui-716-refund-reservations-design.md) 的“选择与边界”明确允许同一原支付多次部分退款；“唯一域与错误”同时规定业务引用唯一域为 `(owner, app, RefundRequestRef)`，换 key 或跨 order 复用该引用均冲突。其 [order_refund_reservation_store.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/order_refund_reservation_store.go) 在 `migrateRefundReservations` / `reserveRefund` 实际实施该唯一约束。

这份合同针对内部 Go `OrderRefundReservationCommand`、原 `ReceiptID`、完整 owner/app/order/currency 范围和退款冻结。它明确不新增 HTTP/main 装配、不执行真实渠道退款。它没有声明：

- Touch 的 `refund_ref` 就是这个 `RefundRequestRef`、`ReservationID` 或最终退款事实 ID；
- Touch 的 `refund_registered` 每行是独立成功退款的增量，还是同一退款对象的多次状态事件；
- 同一资金事实重发时，Touch `event_id` 必须保持稳定或可更换。

[product-billing-v1.md](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/docs/contracts/product-billing-v1.md) 里的 `refund_ref` 又属于 goboost 产品算力包的 sandbox 撤销，合同明确不执行现金退款，也不能拿来定义 Touch 的现金退款事件。

### 缺少的最小合同

需要真正向 Touch 生产 `refund_registered` 的适配合同，明确：稳定原退款事实 ID 及范围、到 `refund_ref` 的映射、到 source/event_id 的确定性映射、部分退款每笔是否独立 ID、amount 是本次增量还是累计、同一退款状态更新是否允许另发计金额事件。若上游规定一个稳定交易 ID 只能投影一次，应在生产者及账务接收边界建立相应身份约束和冲突规则，再由消费者照实读取；不能只改统计查询去重掩盖账务投影重复。

本次仅在固定 public-ai 版本检查 contracts 目录、refund_ref / payment_ref 相关源码与合同，并检索 `refund_registered`、`payment_paid`；未找到这份 Touch 事件适配合同。此为限定范围的缺失结论，不宣称整个生态永远不存在其他约定。

## 二、付款：PaymentRef 可空是现有受信投影行为

### 当前入口实际验证什么

[handlers_billing_authority.go](../../../server/internal/httpapi/handlers_billing_authority.go) 的 `requireBillingAuthority` / `authorityProducer` / `handleBillingAuthorityEvent` 构成当前入口：

1. 要求 `X-Service-ID`；无服务身份 401，服务 token registry 未配置 503。
2. 查服务 token，并用 `subtle.ConstantTimeCompare` 比较 `X-Service-Token`；错误身份 401，非 `billing_authority` 角色 403。handler 再核对 context 中已确认的服务身份和角色。商家会话本身不能写付款事实。
3. 要求真实存在的 tenant、非空 source 和 event_id；订单事件要求 order_id。`payload` 按 `billing.EventPayload` 解码，没有 `payment_ref` 非空校验。
4. `ApplyBillingOrderEvent` 按 tenant/order 查原订单、按 tenant/source/event 查重、调用状态机，并在同事务更新投影与追加事件。

准确地说，这里是**服务凭据校验**，不是请求体 HMAC 或支付渠道签名校验。`source` 来自已认证生产者提交的正文，未在此处强制等于 `X-Service-ID`；渠道收款证据由权威生产者承担。不能把 source/event_id 本身称作验签或付款证明，也不能因该入口可信便声称已经验证真实生产收款。

### 状态机与已有测试

[activation.go](../../../server/internal/billing/activation.go) 顶部纪律直接说明：受信服务生产者事件是本投影接收真实服务端支付状态的通道，浏览器回跳只做读面回查。`ApplyOrderEvent` 的 `EventPaymentPaid` 分支使 processing → opening，并保存 `in.Payload.PaymentRef`；未限制它非空。opening 状态的同引用回调保持幂等，不同引用冲突，active / failed / cancelled 后迟到付款不能追溯改状态。

[0110_billing_orders.sql](../../../server/internal/db/migrations/0110_billing_orders.sql) 的 `payment_ref` 默认空；订单事件按 tenant/source/event 唯一。`payment_ref` 不充当该投影的付款存在性开关。

已有测试证据：

- [activation_test.go](../../../server/internal/billing/activation_test.go) `TestPaymentPaidOpensOrder` / `TestPaymentTwiceDifferentRefConflicts`：支付事件推进 opening；已知引用不能漂移。这些使用有值样例，并未规定所有事件必须有值。
- [server_billing_test.go](../../../server/internal/httpapi/server_billing_test.go) `TestAuthoritySeamFailClosedWithoutCredentials` / `TestFourStateActivationLoopViaAuthority`：会话不能自授、registry 空 fail-closed、服务事件驱动开通、浏览器 GET 不推进。
- [server_touchpoint_deliveries_test.go](../../../server/internal/httpapi/server_touchpoint_deliveries_test.go) `dlvPayOrder` 与 `dlvPayOrderB` **省略整个 payload**，向真实 HTTP authority 路由发送 payment_paid 并断言 201/200；`TestTouchpointDeliveryHTTPFullJourney` 继续据此创建有付款前置门的交付。这是既有受控 HTTP 测试的明确兼容行为，不能靠统计层补必填悄然取消。

以上为本次源码与既有测试内容核对，不宣称在本次裁定里重新执行这些 Go 测试。

### 与 public-ai 收款命令严格区分

[HUI-715 收款设计](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/docs/superpowers/specs/2026-09-12-hui-715-order-receipts-design.md) 的“唯一域、隔离与错误”确实规定 `payment_ref` 在 owner/app/order 内唯一，另有业务事件唯一与外部 provider/merchant/transaction 全局唯一；[order_receipt_command.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/order_receipt_command.go) 的 `OrderReceiptCommand.Identity()` 明确将非空合法 PaymentRef 纳入 shape 校验。

但这份命令用于 `OrderReceiptRecorder.RecordReceipt` 写原始收款限额与不可变资金事实，需要独立授权及渠道成功证据；设计明确为内部 Go 接口、未提供 HTTP/main 装配，不是 Touch `payment_paid` 的 schema。没有看到从该 receipt 到 Touch 订单事件的正式映射。因此不能用公共收款命令的强约束，反向宣称 Touch 当前已有“PaymentRef 必填”合同。

未来若正式 producer 合同要求 Touch 支付事件携带稳定 `ReceiptID` / payment_ref，应在该正式映射合同中同时给出订单、付款事实和 event_id 的关系，并迁移生产者、接收校验及历史兼容规则。本轮统计只读受信已接受事件；不另建付款权，不改变报价、建单和支付分离。

## 本轮处理边界

- 不修改 ApplyBillingRefund、ApplyBillingOrderEvent、schema 或统计查询的既定身份规则。
- 不将“同 refund_ref、不同 event_id”直接定性成已证实的重复退款；保留生产者映射合同缺口。
- 不将空 PaymentRef 的已接受 payment_paid 当作无付款，也不新增输入必填规则。
- 不调用真实支付，不读取生产 secret，不更新生产配置或 Linear/PR；本报告不代表真实收退款或正式部署验收。
