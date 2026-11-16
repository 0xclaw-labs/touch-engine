# Touch cashier 部署登记 runbook（touch-order-checkout/v1.1）

> 部署顺序义务（合同 §6 半环防御三层）：**先登记 → 再发版（开关关）→ 翻开关 → 联测**；
> 回滚 = 清开关重启，intent 行保留幂等可重放。

## 1. Touch 侧（touch-engine）服务身份登记

生产 env（/etc 或部署 secret 面）追加：

```
# producer 外发事实（payment_paid/refund_registered/opening）入站身份：
TOUCH_SERVICE_TOKENS=pai-billing:<GENERATED_TOKEN>   # 与 §2 的 TOUCH_AUTHORITY_TOKEN 同值
TOUCH_SERVICE_ROLES=pai-billing:billing_authority
# mint 出站身份（收银台 app 族）：
PLATFORM_BILLING_BASE_URL=https://billing.modelxing.com   # 或内网权威地址
PLATFORM_BILLING_TOKEN=<GENERATED_TOKEN>                  # §2 的 BILLING_APP_TOKENS touch-engine 同值
TOUCH_CASHIER_PROVIDER=wechat                             # 缺省 wechat
TOUCH_CASHIER_TARGET_ID=ti-touch-engine-billing           # 冻结 return target
```

禁例不变：不得用 `PLATFORM_INTERNAL_TOKEN`/`eco-fixture-*`/legacy shared token。

## 2. public-ai 侧（platform-billing）

```
BILLING_APP_TOKENS=...追加 touch-engine:<GENERATED_TOKEN>
PLATFORM_BILLING_TOUCH_ORDERS=on                         # 收银面总开关（缺省 off fail-closed）
PLATFORM_BILLING_TOUCH_AUTHORITY_URL=<touch-engine 内网地址>
PLATFORM_BILLING_TOUCH_AUTHORITY_TOKEN=<GENERATED_TOKEN> # 与 §1 同值
PLATFORM_BILLING_TOUCH_AUTHORITY_SERVICE_ID=pai-billing   # 缺省即此值
```

## 3. manifest 登记（生产翻转是上线动作，须业主拍板）

仓内占位已备：`docs/contracts/app-registry/v1/touch-cashier-manifest.json`
（enabled=false、target `ti-touch-engine-billing`）。上线时：
1. 业主确认 touch-engine 生产 canonical_host。
2. `docs/contracts/app-registry/v1/production-manifest.json` 的 touch-engine 条目
   加 cashier launch target（enabled=true）+ 同步三组 production_truth 测试。
3. A 机 `/etc/platform-shared/app-registry.v1.json` 按 app_id 合并落盘；
   identity/billing/notify 三服务 `*_TARGET_MANIFEST` 指针核对；`systemctl restart`。
4. 探活：仿 `scripts/touchleads-registry-probe.sh` 写 touch-cashier probe
   （只报键存在，不打印 token）。

## 4. 价格发布前置（合同 §4）

Touch 每个可售 (kind/price_ref/pricing_version) 必须先经 operator 在 public-ai
PublishPrices 发布（source.app=touch-engine），金额逐字一致；续付端点展示的
价格行直接取自已发布条目——**未发布即 409 price_not_registered**。

## 5. 联测顺序

1. stub 联测：测试钩子注入渠道替身（payino 无 sandbox 分支，测试环境经
   `PayinoOptions` 注入）跑 mint→webhook→payment_paid gating→回发全链。
2. 真实联测（R3 PR-5）：真实 payino.yml + 测试订单 ¥ 级支付，核对
   公共付款事实、Touch 投影、页面、服务生效四层一致。
3. 工程证据与正式资金证据分开保留。
