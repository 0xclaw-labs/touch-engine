# 28 项业务视图静态覆盖账

核对对象：integration `79367fd`；规格摘录 `runtime/business-views-spec.md`。这是路由、组件与已有审查记录的静态映射，不是逐页浏览器验收。路径均相对于 `web/src`；表内「浏览器」指既有独立审查，版本与范围以引用报告为准，并非本次新测。page-map 为声明账，page-census 只扫描 page 文件且明写状态、响应式、无障碍与截图均未测；两者和路由数量均不能证明功能完成。原规格的 `/work` 建议路径不是实际首页，实际商家首页为 `/`。

| 视图 | 真实承载、实现文件 | 上游工单／本轮范围 | 既有证据及缺口 |
|---|---|---|---|
| C1 门店首页 | `/s/[storeCode]`；`app/s/[storeCode]/storefront-home.tsx` | 1896 / 4655 | store-content-review：原店已发布首页、390px；真机/NFC 未验。 |
| C2 内容交接 | `/s/[storeCode]/content/[actionId]`；同目录 `content-view.tsx` | 1896、1670、3553 / 4657、4660 | final-adversarial-review：原会话恢复及未准入媒体导出拒绝；正常获准平台交接缺证。 |
| C3 服务详情 | `/s/[storeCode]/service/[actionId]`；同目录 `service-view.tsx` | 1896、4589、4590 / 4655 | store-content-review 首页展开 Wi-Fi；真实连接、各目标与失败恢复未逐项验。 |
| C4 活动进度 | `/c/[code]`；`app/c/[code]/public-campaign.tsx` | 1896、3546 / 4656、4658、4659、4661 | campaign-live-final-review 原活动草稿对顾客不暴露；渠道未获准，正常预留/发布正向待验。 |
| C5 我的权益 | `/s/[storeCode]/benefits`、`/c/[code]/benefits`；复用 `app/s/[storeCode]/benefits/benefits-view.tsx`，活动容器接入口 | 1896、1671 / 4659、4661 | 0ef5509 原活动权益页空态与返回活动已浏览器复验；原库无 p 或券，真实旧券浏览器/核销闭环缺证。 |
| C6 隐私授权 | `/s/[storeCode]/privacy`；同目录 `privacy-view.tsx`；活动 `/c/[code]/contact` | 1896、4594 / 4662 | leads-contract-review 原店提交、工程 relay 接收、撤回、刷新恢复同记录；正式 Notify/Identity 未验。 |
| M1 今天 | `/`；`components/work/today-desk.tsx`，登录 `/admin` | 2628 / 2628 | store-content-review 正常登录和导航；全部提醒及组织切换未逐一验。 |
| M2 门店 | `/work/stores`、`/work/stores/[id]`；对应 `app/work/stores` | 2628、4593 / 4655 | store-content-review 单店进入和配置；全角色范围未全面验。 |
| M3 首页设置 | `/work/stores/[id]/page`；`components/work/storefront/storefront-page-desk.tsx` | 1676 / 4655 | store-content-review 原店保存/发布及顾客读取；部分撤权和设备缺证。 |
| M4 渠道账号 | `/work/stores/[id]/channels`；对应 page；服务配置 `.../services` | 4584 / 4655 | 原渠道未获准；真实平台授权、回调及各渠道未验。 |
| M5 素材内容 | `/work/materials`、门店抽屉；`app/work/materials/page.tsx`、`components/work/store-content-drawer.tsx` | 3553、4595 / 4657、4660 | store-content-review 真实文件上传、采用、发布及顾客读取；正式制作付款/渠道未验。 |
| M6 活动列表 | `/work/campaigns`；`app/work/campaigns/page.tsx` | 1676、2628 / 4656 | customer-staff-review 正常新建并进入活动；所有状态与批量场景未验。 |
| M7 活动编辑 | `/work/campaigns/new`、`/work/campaigns/[id]`；对应 page 与配置组件 | 1676、2628 / 4656、4658 | campaign-live-final-review 原草稿保存、渠道不足拒绝启用；真实上线、恢复未验。 |
| M8 触点台卡 | `/work/touchpoints` 与门店工作区；`components/work/touchpoints/touchpoints-panel.tsx` | 1665、1663 / 4655、4664 | commercial-ops-review 登记、导出及 `/t` 重定向；换绑、实体写卡和真机未验。 |
| M9 优惠团购 | `/work/rewards`；`app/work/rewards/page.tsx` | 1671、4590 / 4658、4661 | campaign-rewards-review 创建及补批、上限；外部券、并发履约、真实资格缺证。 |
| M10 经营数据 | `/work/analytics`；`components/work/analytics/analytics-view.tsx` | 1677 / 4665 | final-adversarial-review 原门店动作交接筛选、空态与统计文案复验；费用及全部更正/迟到事实缺证。 |
| M11 客户线索 | `/work/leads`；`components/work/leads/leads-view.tsx`，WorkFrame「管理与更多」入口 | 4594、2222 / 4662 | Touch 列表展示授权及接收状态；当前组件未见到 Leads 详情的受控跳转或返回原店链接，规格该项缺口已报 root。C6 工程 relay 闭环不等于商家 SSO 详情验证。 |
| M12 订阅账单 | `/work/billing`；`components/work/billing/billing-desk.tsx` | 4591 / 4664 | commercial-ops-review 工程目录和报价，收费不可用诚实禁用；真实收银、支付、开通未验。 |
| M13 成员权限 | `/work/settings`；`components/work/settings/settings-desk.tsx` | 4593 / 4663（费用相关 4664） | commercial-ops-review 成员与代理授权、撤回和原请求403；其他角色及跨组织竞态未全面验。 |
| S1 确认核销 | `/work/service`；`components/work/service-desk/service-desk.tsx` 与 `redemption-card.tsx` | 3562 / 4661 | 页面与受控入口存在；原活动真实凭证发放核销闭环缺证。 |
| S2 记录处理 | `/work/service` 同页「历史与交班」；`service-desk.tsx` 的 history 页签、`stores/{scope}/service-history` | 3562 / 4661 | customer-staff-review 未选店仅提示选择，修复虚假零态；失败重查、撤销申请和交班全流程未验。无需另造路由。 |
| O1 商户开通 | `/ops/tenants`；`components/ops/tenants-desk.tsx` | 4592 / 4666 总验；本轮仅受影响接线 | 静态运营壳 `OpsGate` 与页面存在；正常受权浏览器和全部操作缺证。 |
| O2 套餐价格 | `/ops/plans`；`components/ops/plans-desk.tsx` | 4592、4591 / 4666 总验；本轮仅受影响接线 | commercial-ops-review 工程受权进入空目录；仅已有价格发布/下架，真实价格生产、订单引用未证。 |
| O3 订单对账 | `/ops/orders`；`components/ops/orders-desk.tsx` | 4592、4591 / 4666 总验；本轮仅受影响接线 | 静态页面及运营壳存在；受权浏览器、真实账单退款及重查缺证。 |
| O4 渠道健康 | `/ops/channels`；`components/ops/channels-desk.tsx` | 4592、3535 / 4666 总验；本轮仅受影响接线 | 静态承载存在；真实渠道资格、授权过期及暂停恢复缺证。 |
| O5 模板内容 | `/ops/templates`；`components/ops/templates-desk.tsx` | 4592 / 4666 总验；本轮仅受影响接线 | 静态承载存在；受权发布及风险下架浏览器缺证。 |
| O6 运行交付 | `/ops/operations`；`components/ops/operations-desk.tsx` | 4592、1663 / 4666 总验；本轮仅受影响接线 | 静态聚合和操作入口存在；真实服务、硬件交付与恢复演练缺证。 |
| A1 代理代管 | `/agency`；`components/work/agency-console/agency-console.tsx` | 4593 / 4663（费用相关 4664） | commercial-ops-review 代理登录、切入、撤销后无商家；硬件、结算及多商家切换缺证。 |

静态路由存在不保证对应权限、外部依赖或端到端行为可用。运营六页和代理入口均需真实受权前提；M11 的统一 Leads 详情/受控 SSO 尚未在本实现中找到。跨工单列为业务范围映射，不能推断各工单已经完成。

## M11 跨仓只读定位

Leads `3b83dd6`（代码 404b9f4）存在 `/leads/[id]`，由 `web/src/app/(shell)/leads/[id]/page.tsx` 以 Leads 自己的 lead ID 和当前 CRM 租户读取 timeline。Leads `server/internal/store/notify_inbox.go` 的 inbox 行持有来源应用、命名空间、源租户、source ref、映射目标及 lead_id；`handlers_notify_ingest.go` 本地 receipt 可含 lead_id。Touch 的 ACK 记录不是该 lead ID，`workLeadView` 也只提供原 submission_ref 与状态。当前没有证明对外可用、按完整来源键和授权解析详情的合同，不能把 ref 当作 lead ID；`FindLeadBySourceTx` 的 `LIMIT 1` 也不能用来证明来源歧义时的唯一性。

Touch `web/src/components/eco-nav/use-app-switch.ts`、`web/src/app/api/eco-nav/switch/route.ts`、`server/internal/httpapi/handlers_eco_nav.go` 的切换合同只接受 app_id 和 launch_target_id，明确拒绝其他参数；已有一次性跳转凭据绑定主体和租户，但本身不传单条 ref 或回跳上下文。最小可行修复需两仓明确受控合同：Touch 在现有权限及门店范围裁决后按原行发起绑定主体、源租户、来源 ref 与安全回跳的短期一次性上下文；Leads 核验来源到目标租户映射、当前成员和记录访问权，以完整来源键解析唯一 lead 并进入既有详情，待同步、撤回、歧义、越权、过期等如实拒绝或显示状态，回跳仅指向受控原门店位置。具体授权及受控票据应复用双方原权威，不把普通应用菜单或 URL 拼接称为记录级 SSO。此为可修内部契约缺口，尚未有代码改动或端到端验证。


## 后续 M11 缺口修复与复验

以上静态表与缺口结论固定于79367fd，并非末轮候选状态。M11后续已由两仓受控来源解析、原SSO接续与原门店返回实现补齐。Terra在原两库从正常C6新咨询取得真实stored ACK，沿Touch原行进入真实Leads详情timeline200；返回target工程禁用503后在同详情重试，恢复工程登记后回原店。原已撤回咨询未复活。此范围的最新代码、失败记录、换账号增量与正式登记限制见leads-followup-navigation-review.md和leads-followup-adversarial-review.md。

新增Touch `/lead-follow-up-return/[h]` 与Leads `/touch-follow-up/[h]` 只是SSO后技术接续承载，已登记各自路由census，不新增业务视图。Touch物理page路由现36个，原28项业务视图口径保持；数量仍不等于逐页浏览器验收。
