# 收银可用性与原订单接续复审

标识：`TOUCH-EXEC-20261115`。工单：[HUI-4664](https://linear.app/huigoo/issue/HUI-4664)。复审基线为 Touch `9eff481d9ab4e5477c39c75f4d4baccda821de74`。

## 已确认的问题

原 `cashierCheckout()` 只校验 `TOUCH_CASHIER_APP_ID` 与 `TOUCH_CASHIER_TARGET_ID` 的格式。配置未登记的 `cashier / ti-cashier-web` 时，订阅读面也返回 `available=true`；正常账单页据此先建单，再调用通用应用跳转。该跳转后来被拒绝，不能弥补先前的就绪误报。

进一步核对表明，换成真实已登记的 `goboost / ti-goboost-web` 也不能解决：`openCheckout → switchApp → shell/launch` 只绑定主体、租户、应用和目标，不带原订单消费上下文。现有 `CheckoutRef` 没有原单 checkout 引用，接收端也没有已证实的原单消费合同。普通应用的 SSO 成功不证明原订单支付接续成功。

这是有条件可复现的代码缺陷，不是已观察到的生产事故。两个环境变量为空时原页面已阻止确认下单，但原订单详情还缺少接入未完成的具体说明。

## 本轮修复

- 收银读取统一返回 `available=false / reason=checkout_unwired`，不发出伪造的支付目标。仅填写标识、注册普通应用或通过通用 SSO 都不能打开付款承诺。
- 保留原报价、原订单列表与详情、权限裁决、付款人快照、幂等及权威事件入口。API 有权调用方仍可登记原业务订单；本轮没有禁止该原合同，不能写成所有 API 均无法建单。
- 正常页面的确认下单继续被不可用状态阻止；在原待支付订单详情明确说明订单保留、支付接续未开通、无需重新下单及可以回查。
- 原订单详情先判断 `not_awaiting_payment` 和 `original_payer_required`，只有满足原付款条件的未付订单才得到 `checkout_unwired`。已支付不再付款，失去授权不被通道说明掩盖。
- 配置注释已纠正：两个目标标识只为兼容保留，不是原单支付能力的证明。

这是纠正就绪误报，**不是完成正式支付链**。未通过隐藏缺失能力、临时假登记、向 URL 塞 `order_id` 或使用其他应用充值页来宣布成功。

## 红绿证据

新回归 `TestCashierAvailabilityRequiresAnOrderBoundCheckout` 从真实 HTTP 订阅接口读取：

1. 未登记 pair 在旧实现返回 `available=true`，失败。
2. 已登记但只支持普通导航的 GoBoost pair 在旧实现也返回 `available=true`，失败。
3. 空配置缺少 `checkout_unwired` 原因，补充反例先失败。
4. 修复后上述读取均明确不可用，仍能取得原权威报价 `price_m_v2026`，只读与报价不会创建订单。

原付款人和代理回归保留，并纠正了原测试中“配置两个字符串就可继续支付”的错误正例。原单重复回查保持同一记录，跨租户拒绝、代理付款权撤回拒绝、已支付不可再次付款均仍验证。

执行：`go test -p 1 ./internal/httpapi -run 'TestCashier|TestOriginalUnpaid|TestAgentRenewalOrder|TestSubscriptionOrder|TestBilling|TestRefund|TestHardware' -count=1`，通过。Web 账单与商业定向回归 2 文件、12 项通过。完整候选验证由本轮主验收记录承接。

## 上游合同与剩余工作

[public-ai 正式生产登记](https://github.com/0xclaw-labs/public-ai/blob/a0c5b24c0e1e4024e58c33b918531b9a750e6a86/docs/contracts/app-registry/v1/production-manifest.json) 当前没有 Touch 返回目标或 cashier。已有充值 checkout、GoBoost 商品 checkout 和其它产品自有收银页，均不能据此推定能够消费 Touch 原订单。

HUI-4664 还需真实公共结算消费端提供受控 checkout 引用、原订单与付款组织绑定、原金额和价格版本校验、幂等支付、可信结果回查及原任务返回。通用 shell 四字段及禁止业务参数出现在 URL 的合同保持。正式 Touch 域名、登记证据、服务身份和实际支付仍待同候选联验。

Terra 已对上述修复边界做独立静态审查。正常浏览器登录后的账单页面、本次真实支付与退款均尚未验收，本票不能 Done。
