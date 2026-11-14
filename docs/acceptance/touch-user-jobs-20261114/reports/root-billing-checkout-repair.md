# 原订单收银接续

root提交 `e0716cf`，基于原订单幂等/硬件原子修复 `6f3fb98`。

## 发现与改动

- 原账单页向 shell mint 后直接读取 launch_url；原 BFF 合同实际只返回 jump_token。改用既有 `useAppSwitch` 两步 mint/redeem，不新造跳转域名、支付事实或另一套票据。
- 下单之后丢失/中断跳转只能回查，无法继续前往收银台。原 GET order 现附带当前可否接续的投影：同原付款人、原 grant/relation、当前付款权限、未有支付事实且仍 processing 才提供配置目标；回查不建新单、不重报价。前端从原订单详情继续，点击时再次读取原记录。
- app/target 配置不完整或格式无效不宣称通道可用；已付款订单不再提供继续支付；原付款授权变化不转移付款人。

## 验证

- `logs/root-billing-checkout-red.log`：原实现对缺 target 仍 available=true；原 unpaid order GET 无接续事实。
- `logs/root-billing-checkout-final.log`：真实 HTTP 原单重复回查仅1单、无支付事实；跨租户404；受信支付事件之后不可再次接续；代理原授权可继续，换付款人/撤权不可继续。
- `logs/root-billing-checkout-web.log`：现有账单/原单语义/BFF mint-redeem21项通过；`root-billing-checkout-tsc.log` 类型检查通过。
- 未为接线修复新增源码字符串断言；移除旧测试对组件直接调用 mint 字符串的实现绑定，继续复用既有 BFF 行为合同测试。

## 正式边界

本部署尚无正式 cashier app/target，原工程 Identity stub 也没有 cashier。已只读核对 public-ai 当前生产注册表：未找到 cashier 登记；未修改注册表或借其他应用冒充收银。以上不证明真实收银、扣费、开通成功；正常浏览器原单正向收银还需正确登记及实际目标，工单不能据此 Done。
