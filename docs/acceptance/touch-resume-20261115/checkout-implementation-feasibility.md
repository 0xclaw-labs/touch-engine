# Touch 原单 checkout 可实施性核查

日期：2026-11-15（UTC）。本轮只读核查 HUI-4664、HUI-4591、Touch canonical 账单路径及 `0xclaw-labs/public-ai` 现有订单/checkout 合同；唯一新增文件是本报告。没有修改代码、配置或数据库，没有建立订单、调用付款接口、登记应用、发送 Linear/PR 更新或验证真实收付款。

## 结论

**在本次固定版本及所核查的现有入口中，没有可直接消费 Touch 原订单，同时完成付款组织与付款人约束、冻结价格、原单幂等和受控返回原任务的 checkout 接口。现状不能仅靠填写两个应用标识、换成已登记应用，或增加一个前端跳转完成。**

public-ai 已有可复用的真实通道预下单、验签付款事实、钱包充值、商品购买、原订单收款记账与幂等组件。这些是已有工程基础。当前缺口包含两类开发工作：Touch 的客户端/适配接线，以及公共侧尚未提供的 Touch 原单 producer 和资金合同。后者不是永久不可开发，也不能被笼统归为“等待外部”；需要在公共权威侧明确并实现合同。真实价格、付款组织授权、通道与正式部署验证又是独立前提。

当前 `checkout_unwired` 准确表达了已检查的实现边界。它不证明报价、原单恢复或账务投影代码缺失，也不代表原单支付闭环已经交付。

## 固定依据与检查范围

| 依据 | 本次读取版本/范围 |
| --- | --- |
| [HUI-4664](https://linear.app/huigoo/issue/HUI-4664) | 当前 In Progress；更新时间 `2026-11-15T05:45:23.773Z`。要求原单受控支付、原任务返回、付款主体/价格版本/生效日一致；本票只拥有 A1/M12 呈现与受控跳转 |
| [HUI-4591](https://linear.app/huigoo/issue/HUI-4591) | 当前 Done；更新时间 `2026-11-14T04:27:16.278Z`。票面明确订单/支付/退款/订阅由公共服务持有；列出的 Touch BFF 接口是拟契约，必须映射真实公共 API，不得编造上游端点 |
| Touch canonical | `47ecebb9a4dab875556f0b3a6bac7e53fa3cb463`；核查账单 handlers、订单 store、付款权限、报价预览、authority 接收入口、账单页面及 eco-nav 跳转 |
| public-ai main | GitHub branches/main 本次返回 `a0c5b24c0e1e4024e58c33b918531b9a750e6a86`；后续文件读取均固定此 SHA |
| public-ai 范围 | `cmd/platform-billing/main.go`、Billing HTTP 路由分发、legacy checkout、Billing Center、product checkout/catalog/purchase 分发、unified 用量路由、订单 receipt/settlement 内部组件、生产应用注册表 |

对 `checkout_ref`、`return_context_ref`、`AdjudicateQuoteRef` 和 `NewOrderReceiptRecorder` 做了仓内限定检索，结合实际路由与类型核对。缺失判断只适用于以上固定仓库版本和合同范围，不推断所有未检查系统或部署环境均不存在其它实现。未读取生产凭据或探测生产支付接口。

## 一、Touch 当前已有的原单能力

### 保存的是可恢复的本地订单意图

`handleBillingSubscriptionOrder` 接收 `action/plan_code/price_ref/additional_stores/reason/idempotency_key`，从会话及实时授权解析 tenant、actor 和 payer，调用本地 `CreateBillingOrder`。订单保存价格快照、金额币种、原付款 principal、grant/relation、actor、幂等键；初始 `processing`，公共 `order_ref/payment_ref` 为空。`sameBillingOrderIntent` 校验原意图，数据库以 `(tenant_id, idempotency_key)` 合并并发重试。恢复旧单保留原快照，不用当前目录给旧单重新计价。

证据：[handlers_billing.go](../../../server/internal/httpapi/handlers_billing.go) 的 `handleBillingSubscriptionOrder`、`subscriptionReplayInput`；[store_billing.go](../../../server/internal/store/store_billing.go) 的 `BillingOrderRow`、`NewBillingOrder`、`sameBillingOrderIntent`、`createBillingOrderTx`。

### 继续支付已有权限检查，但没有原单 checkout 调用

`originalOrderCheckout` 要求 `processing` 且 `PaymentRef` 为空，再比较实时 `PayerPrincipal/PayerGrantRef/AgencyRelationRef` 和原单。`cashierCheckout` 当前固定返回 `available=false`、`reason=checkout_unwired`。

页面的 `continuePayment(id)` 先查询原单，不重新建单；`openCheckout` 却只有 `switchApp(app_id, launch_target_id)`。`ecoNavSwitchBody` 严格只有两个字段，`LaunchShell` 仅收到 principal、tenant、app、target。因此通用 SSO 的登录和租户传递没有替原单签发 checkout。

证据：[handlers_billing.go](../../../server/internal/httpapi/handlers_billing.go) 的 `cashierCheckout`、`originalOrderCheckout`；[billing-desk.tsx](../../../web/src/components/work/billing/billing-desk.tsx) 的 `openCheckout`、`continuePayment`；[billing.ts](../../../web/src/lib/billing.ts) 的 `CheckoutRef`；[handlers_eco_nav.go](../../../server/internal/httpapi/handlers_eco_nav.go) 的 `ecoNavSwitchBody`、`handleEcoNavSwitch`。

### 报价预览及权威接收入口不能代替 producer

`handleBillingSubscriptionQuote` 只从已生效的本地目录投影预览价格、服务天数和店数，没有建立公共支付报价或返回公共 `quote_ref`。`AdjudicateQuoteRef` 是 AI 用量 `billing_ai_usage` 行的纯校验函数；它不是订阅付款报价调用。已有 `PLATFORM_BILLING_BASE_URL` 的 AI 用量/内容制作接线也不能自动成为续费或硬件 checkout。

Touch 已有受服务身份和 `billing_authority` 角色保护的目录/事件接收面，可消费 `order_registered/payment_paid/activation_active` 等事件；这是 receiver/projection。当前没有从这条建单路径发往公共原单 producer 的请求。`payment_paid` 当前 payload 的兼容边界详见本轮[付款与退款事件合同裁定](payment-contract-review.md)，不能由消费者自行补造渠道证明。

证据：[handlers_subscription_quote.go](../../../server/internal/httpapi/handlers_subscription_quote.go)、[quote.go](../../../server/internal/billing/quote.go)、[handlers_billing_authority.go](../../../server/internal/httpapi/handlers_billing_authority.go)、[activation.go](../../../server/internal/billing/activation.go)。

## 二、public-ai 现有能力逐项核对

### 1. Legacy checkout：充值订单

已有 `POST /internal/v1/billing/checkout`，由 `serveCheckout → Service.CreateCheckout` 处理。参数是 `provider/account_id/project_id/amount_cny/app_id`，返回 `out_trade_no/code_url`；创建 `billing_topups`，验签后由 `SettlePaidTopup` 进入充值结算。

它没有 Touch 原订单、订阅商品、冻结价格版本、店数/服务期或原任务返回合同。把 Touch 总价填进 `amount_cny` 只会发起相应充值，不会把 Touch 订单支付并开通。

证据：[http.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/http.go) 的 `checkoutHTTPRequest`、`serveCheckout`；[service.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/service.go) 的 `CreateCheckout`、`SettlePaidTopup`。

### 2. Billing Center：具备付款权限、幂等与返回白名单的统一钱包充值

已有 `POST /billing-center/topup-intents` 及 GET 恢复查询。请求为 `account_id/provider/amount_minor/currency/return_target/quote_ref/idempotency_key`，通过 `X-Session-Token` 解析终端用户；`CreateTopupIntent` 重验 owner/payer 资格、同 app/付款账户、返回目标白名单，保存 intent 和 channel order，具有实质可复用能力。

但 `quote_ref` 明确定义为 quoted/held **usage_id**，只绑定 AI 用量事实；`amount_minor` 是充值额，不是 Touch 商品订单的冻结总价。`SettleBillingCenterIntent` 最终调用 `grantUnifiedCashConn`，将款项进入 intent 绑定的钱包。返回目标是已登记 app target 的准确 URL，没有原 Touch 订单/门店/内容任务上下文。该接口可以承担其本来的充值任务，不能当作套餐续费、扩店或硬件原单付款。

证据：[billingcenter_http.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/billingcenter_http.go) 的路由和 `billingCenterIntentRequest`；[billingcenter.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/billingcenter.go) 的 `TopupIntentRequest`、`CreateTopupIntent`；[billingcenter_store.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/billingcenter_store.go) 的 `SettleBillingCenterIntent`。

### 3. Product checkout：真实微信实现存在，但现合同购买的是产品目录 SKU

已有 `POST /internal/v1/billing/product/checkout`，并非只有 sandbox：

| 核查点 | 当前函数、参数和行为 | 对 Touch 原单的含义 |
| --- | --- | --- |
| 入参 | `productCheckoutBody` / `ProductCheckoutRequest` 仅 `account_id/kind/sku_id/idempotency_key` | 没有消费 Touch 原单或其报价、店数、期限的参数 |
| 调用权限 | `serveProductCheckout` 要求调用 app 等于 `product.Catalog().AppID`；`CreateCheckout` 检查账户存在 | 当前接口没有 Touch tenant→付款组织映射、原 payer grant/revision 校验合同 |
| 价格 | `Quote` 从当前目录查询 plan/pack/experience；pack 可按当前会员折扣定价 | 不是接收 Touch 已冻结的 `price_ref/price_snapshot`；不能把本地价格当作已被此 producer 承认 |
| 幂等 | `CreateCheckout` 的参数 hash 包含 kind/account/app/SKU/金额，旧 key 相同参数复用 intent | 本地订单到 key 的映射可由 Touch 编写；但函数先查当前价再查旧 key，改价/旧 SKU 移除后的恢复还需要明确的原价合同 |
| 创建结果 | 返回 `out_trade_no/code_url/kind/sku_id/amount_minor/currency/status/replayed` | 没有 `checkout_ref/launch_url/return_context_ref` 或原任务返回消费面 |
| 付款后动作 | `SettleWechatPayment` 写 verified payment fact，再调用 `PurchasePlan/PurchasePack/PurchaseExperience`；`business_ref` 固定为 `checkout:<out_trade_no>` | 当前结算对象是该目录的产品订单、点数与权益；没有 Touch 店数/订阅生效/硬件行或 Touch authority 事件映射 |
| 启用条件 | `NewProductCheckoutService` 需要 published 目录和完整微信商户配置，缺失则 503 | 不能以启用 sandbox 或修改显示标识替代真实通道配置 |

当前嵌入目录 `app_id=goboost`、`pricing_status=placeholder`，包含 GoBoost 月包、算力包、体验等；部署支持 `-product-catalog` 覆盖，但**本次没有读取部署值**，不能把仓内默认值当成现网配置。即使装配另一份已批准目录，也仍需落实 Touch 商品语义、原单付款组织、价格锁定及结果回写；不是把已有 GoBoost SKU 直接映射为 Touch 商品。

以上判断以当前源码为准。较早 `product-billing-v1.md` 的 sandbox-only 描述落后于现行真实微信分支，不能拿它证明公共服务“没有真实付款代码”。

证据：[product_checkout_http.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/product_checkout_http.go)、[product_checkout.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/product_checkout.go)、[product_http.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/product_http.go)、[product-catalog-v1.json](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/productcatalog/product-catalog-v1.json)、[platform-billing/main.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/cmd/platform-billing/main.go)。

### 4. OrderReceiptRecorder：原单收款记账基础，不是现成收银 producer

`OrderReceiptScope` 确有 `LedgerOwner/AppID/OrderRef/Currency`。`OrderReceiptCommand`、`OrderReceiptRecorder.RecordReceipt` 提供原订单收款参数身份、幂等和不可变事实基础，但要求注入 `OrderReceiptAuthority.AuthorizeOrder/VerifyReceipt`，由其提供完整授权与经过渠道核验的成功证据。构造器在 authority 缺失时拒绝。

源码明确说明 **No production authority adapter or HTTP/main wiring is provided here**。本次构造调用检索只得到定义、测试和恢复验证脚本；Billing main/HTTP 分发也未提供对应原单收银入口。因此可以复用这一组件开发公共 producer，但 Touch 不能直接调用它创建渠道 checkout，更不能自己实现一个总是通过的 authority 来补付款事实。

`OrderSettlementCommand` 另处理已有收款的验收、费用政策和收益分配，也不是创建付款意图的接口。

证据：[order_receipt_command.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/order_receipt_command.go)、[order_receipt_recorder.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/order_receipt_recorder.go)、[order_settlement_command.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/order_settlement_command.go)、[unified_http.go](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/internal/billing/unified_http.go)。

### 5. 应用登记：能批准导航目标，不能建立上述资金合同

固定生产 manifest 包含 `goboost/aicut/dhuman/nextmodel-web/agentbuddy/guanlan-order/leads-engine`，没有 Touch 或独立 cashier 目标。补真实目标是未来返回接线的一项部署工作，但登记本身不增加原单消费、付款组织、价格或结果映射。

证据：[production-manifest.json](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/docs/contracts/app-registry/v1/production-manifest.json)。这是仓内 manifest 核查，未声称读取了生产加载的注册表。

## 三、可自行编码的 Touch 实现范围

以下都是工程接线工作；不应将它们写成“必须等待正式支付环境才可开始”。实现时依照已确认的公共合同，并保持收银不可用，直到服务能力及前提真实满足。

| 工作 | 可实施位置 | 必须保留的行为 |
| --- | --- | --- |
| 服务端公共 checkout adapter | 新增专用客户端，由 `handleBillingSubscriptionOrder` 或独立原单操作调用；通过服务配置注入 | 服务端从原订单装配输入；不得从浏览器信任金额、tenant、payer 或任意 URL |
| 引用映射与恢复 | `BillingOrderRow/NewBillingOrder` 及相应持久化/查询 | 保存公共 order/checkout 引用、状态、期限、原请求身份；同原单同 key 查回；响应丢失不重新购买 |
| 实时付款人校验 | `originalOrderCheckout`、`payerDecisionFor` | 保留原 payer/grant/relation 与实时授权比较；如合同要求则补 grant revision；不能把 actor principal 自动当作付款组织 |
| 受控返回上下文 | 原单/原门店/原任务关联存储，受控 callback/recovery 读面 | 绑定原 tenant、任务和允许目标；返回只触发权威回查，不因 success query 推进 paid/active |
| 前端原单接续 | `CheckoutRef`、`openCheckout`、`continuePayment` | 接受真实 checkout 结果；处理已付款、过期、拒绝、未知和响应丢失；不用第二笔新单“恢复”旧单 |
| 结果投影 adapter | 现有受信 billing authority 接收面或服务器查询同步 | 将公共 order/payment/provisioning/refund 的稳定身份映射到 Touch；冻结价格与付款组织要能对上；开通和付款分开 |

Touch 当前缺少独立付款组织字段、checkout/return context 字段和 outbound producer 调用，这些均可编码。其实际字段语义、签发者和查询入口必须来自下节公共合同，不能把新写的字段名称当成已经存在的上游 API。

## 四、尚缺的公共 producer/资金合同及最小落地条件

下列是需要在公共权威侧完成的明确开发和合同范围，不是要求另造钱包，也不是泛称“外部不可做”。

1. **接受 Touch 原订单的权威入口。** 定义如何消费/创建对应公共订单，稳定绑定 Touch tenant+原单、付款组织、有效 payer 及代理授权；以原业务意图和幂等身份确定同一支付意图。不能只拿 app 服务凭据和任意 account_id 代替 Touch 原单付款组织判定。
2. **认可原商品和原价格。** 由已批准目录或有效报价确认订阅/续费/扩店/硬件商品、版本、数量、目标门店范围、币种金额及服务生效规则；明确报价过期、价格变化、旧 SKU 下架时原单如何恢复。现 product checkout 的动态重新报价不能自动满足 Touch 原价冻结要求。
3. **checkout 与原单的读写合同。** 返回可恢复的支付引用或受控启动结果，明确查单、到期、关闭、已付款未开通和并发重试语义；与真实渠道预下单及 verified payment fact 绑定。可复用现有 payino、幂等和 receipt 组件，仍需实际接线。
4. **结果到 Touch 的确定映射。** 给出公共原单、付款/退款事实、金额币种、发生时间、服务生效事实到 `order_ref/payment_ref/source/event_id` 等投影字段的关系；明确重复投递和退款状态更新的稳定身份。按已有受信 receiver 接受事件，不等于这份跨仓 producer 映射已经存在。
5. **原任务受控返回。** 确定签发/保存/消费返回上下文的责任，绑定已核实的目标与原组织任务；通用 launch-target 白名单可以复用，但它本身只解决可去哪个页面。

这些合同和 producer 实现完成后，才进入部署与真实验证：已批准 Touch 价格、正确测试组织/付款人/代理授权、服务凭据、商户渠道、回调验签、实际返回目标及对应域的生效回执。当前仓库代码只证明有相关基础设施，本次没有确认现网启用状态或发生过真实付款。

## 五、交付建议与本次验证边界

建议将后续执行拆为可明确验收的两个代码范围：公共侧补齐 Touch 原单 producer 合同与实现；Touch 侧依合同接入 adapter、持久化和原任务恢复。前者可复用既有通道和账本能力，后者可保留现有原单、付款权限及权威事件投影。合同未建立之前，不将充值、GoBoost 商品购买或登记成功标成 Touch 收银已接通。

工程验收至少应覆盖：同原单重试只生成一个支付意图；请求已成但响应丢失后查回；旧单在目录改价/下架后按既定合同恢复；错误 tenant/付款组织/撤回授权拒绝；付款成功但开通失败不二次收费；回调重复/乱序不多开；跨组织返回不能渲染或操作旧单。之后用同一真实测试订单核对公共付款事实、Touch 投影、页面和实际服务生效，分别保留工程与正式资金证据。

本轮没有代码改动，因此没有新增或运行测试，也没有用样例事件、固定数据库或源码推演冒充实际业务闭环。已完成的是固定版本源码、两张 Linear 票面和现有合同的只读可实施性核查。
