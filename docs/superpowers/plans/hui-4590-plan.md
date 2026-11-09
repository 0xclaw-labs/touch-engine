# HUI-4590 实施计划｜美团/大众点评/抖音团购套餐与外部券授权核销

- 票: HUI-4590 ｜ 裁定书: /Users/jimfu/works/派诺生态/touch-all-20261109/briefs/hui-4590.md
- worktree: /Users/jimfu/works/派诺生态/wt-touch-4590 ｜ 分支: touch/hui-4590-deals ｜ 基线: ce47dde（wave1 已合流）
- 迁移号段: 0084-0087 ｜ 端口: web 18349 / go 18249 ｜ 报告: /Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-4590-report.md
- 写面: server/internal/deals（新包）、server/internal/store/store_deals.go（新）、server/internal/httpapi/handlers_deals.go + server_deals_test.go（新）、server.go 仅一行注册、server/internal/db/db_test.go 表清单加行、web /work/rewards 外部团购 tab + lib；他人域只读
- 流程: 每任务 TDD 红→绿→commit；不 push 不 merge
- 备注: Linear MCP 全程不可用（get_issue/whoami 均 fetch failed，多次重试），票面细节以裁定书+派遣单要点为权威基线；差异待指挥复核。

## 裁定书预答（brainstorm 基线，全部按裁定书执行）

1. 三平台 deal-offers CRUD（GET/POST /api/v1/stores/{id}/deal-offers）+ 顾客读端点（GET /api/v1/public/storefronts/{code}/offers）。
2. 价格过期/未核实 = 「到平台查看」；售罄/失效停购买入口；点击≠成交；分渠道归因统计保留（美团/点评分渠道，不同合）。
3. 未授权外部券 = 诚实未接通，不假核销（复用 1671 边界语义；1671 的 GrantExternalCoupon/external_redeem_local_forbidden 不动）。
4. M9 外部团购 tab 落地：上下架/编辑/校验时间管理（替换 1671 占位）。
5. 1671 集成决策点（本票定夺）：**保留外部供给批次的本地 grant 行 = 本地承诺/展示记录语义，不加确认侧 gate**。理由：(a) 本地核销已被 1671 硬拒（external_redeem_local_forbidden），承诺记录不产生任何冒充核销路径；(b) 保留审计/展示链；(c) 不改 1671 已固化的语义与测试；(d) gate 保留为"未采用选项"，在报告中声明。

## brainstorm 自答（裁定书+简报+仓内先例均能覆盖，无阻塞）

- Q 新表还是扩展 deal_offers？→ **新表 store_deal_offers**。1671 的 deal_offers 是 campaign 强绑（campaign_id NOT NULL）的奖励供给目录；本票 offer 是门店面（无活动也须可用，SHARED-BRIEF 约束 1），生命周期不同，不互改。
- Q 价格"过期"阈值？→ 固定常量 72h（PriceVerifiedTTL，deals 包内文档化）。M9 管理的是校验时间（重新核对按钮=服务端 now），不是 per-offer TTL 配置。
- Q 公共面给不给 product_url？→ 给。门店动作的 target_ref 下发禁令是 4589 那张面的纪律（目标地址按方法受控解析）；本票 offer 的商品页 URL 是卡片的内容本体（顾客要去平台下单），保存侧已做强校验（https 公网 + 平台域名匹配=正确商品跳转），读出侧再验一次（fail-closed）。
- Q 售罄？→ 平台态，本地不可知（诚实）：不发明 sold_out 字段；失效（paused/retired/过窗口）直接不进公共 payload（fail-closed），替代品=其余在列 offer（渲染方 1896 有全列表）。
- Q 鉴权？→ 复用既有 authz（不新建权限体系，新权限动作归 4593）：GET=ReadRecord(store scope)、写=Update(store scope)，跨店 404 掩码；store_manager 建店跨店适用面越权=拒绝（作用域事实在服务端）。
- Q 开关？→ 不新增 flag。公共 offers/点击 beacon 挂 FEATURE_STOREFRONT 双保险（同 4584 公共门店面）；商家面无开关（同 reward ops 管理面）。
- Q 外部适配器生产接线？→ 无真实平台授权，生产路径不接适配器（Server.DealCoupons 默认 nil），端点诚实 not_connected；测试用 fake 适配器只进测试入口。

## 任务分解

### T1 迁移 0084-0087（TDD：db_test 表清单先红）
- 0084_store_deal_offers.sql：id/tenant_id/platform(check meituan|dianping|douyin)/provider_offer_id/product_url/title/image_ref/price_minor(>=0)/currency/price_source(check platform|merchant)/price_verified_at/valid_from/valid_until/status(check active|paused|retired)/authorization_ref/note/created_by/created_at/updated_at；部分唯一索引 (tenant,platform,provider_offer_id) WHERE provider_offer_id<>''。
- 0085_store_deal_offer_stores.sql：适用门店 join（PK(offer_id,store_id)，FK offer ON DELETE CASCADE + store，tenant_id 冗余列，idx store_id）。
- 0086_store_deal_click_stats.sql：(offer_id,day) PK 聚合计数（tenant/store/platform/day/clicks>=0/updated_at，idx (store_id,day)）。
- 0087_store_deal_coupon_redemptions.sql：外部券核销事实（id/tenant/store/offer/platform/coupon_ref/state check redeemed|failed/detail/actor/created_at；部分唯一 (tenant,offer,coupon_ref) WHERE state='redeemed'——redeemed 终态一行，failed 追加；idx (store_id,created_at)）。
- db_test.go 表清单加 4 行（窄补丁先例=1671）。
- 验收：`go test ./internal/db/` 绿。

### T2 deals 领域包（纯函数，表驱动测试）
- server/internal/deals/deals.go：
  - `Platforms`/`ValidPlatform(p)`：meituan|dianping|douyin。
  - `PlatformDomains`：meituan.com/dianping.com/douyin.com；`ProductURLValid(platform, url)`＝extrajump.OfficialAddress(https 公网) ∧ host 平台域名匹配（正确商品跳转，防错挂渠道）。
  - `PriceVerifiedTTL = 72h`；`PriceState(verifiedAt, status, validFrom, validUntil, now) → "show"|"platform"`：仅 status=active ∧ 窗口内 ∧ verified_at 在 72h 内 → show（过期/未核实→「到平台查看」）。
  - `Fact`/`Adapter` 接口：`Check(CheckRequest) (CheckResult, error)`、`Redeem(RedeemRequest) (RedeemResult, error)`；RedeemResult.State ∈ redeemed|failed|unknown；error=传输失败→unknown（诚实未知）。
- 验收：`go test ./internal/deals/` 全绿（72h 边界、三平台域名正反例、非 https/内网/.invalid 拒、跨平台 URL 拒）。

### T3 store 层 store_deals.go（TDD store 级测试）
- Create/Get/List（按适用门店过滤，tenant 强制）/Update/Status/SetApplicableStores（org_owner 全租户门店；store_manager 仅本店，apply_all/跨店=拒）；校验复用 deals 包（平台、URL、价格≥0、窗口 RFC3339、唯一冲突→ErrDealConflict 409）。
- PublicOffersForStore(storeID, now)：status=active ∧ 窗口内 ∧ 价格态计算 ∧ URL 读出侧复验（任一不过=该行不出现，fail-closed）。
- IncrementOfferClick（幂等 upsert +1）；ClickTotals(offer) / ClickTotalsByPlatform(store)。
- RedeemStoreDealCoupon(input, redeem func() deals.RedeemResult, err源, nowTS)：授权校验（offer.AuthorizationRef 空→ErrDealAuthorizationRequired）→ redeemed 行回查（UNIQUE 部分索引）= 终态回读不重放适配器 → 调 redeem 闭包一次：redeemed→落 redeemed 行；failed→追加 failed 行（可重试）；unknown/error→诚实未知、零行落库。
- 语义红线测试：外部券核销不写 reward_benefits/reward_inventory 任何行（本地门店券不能核销抖音券，反之亦然）；coupon_ref 不进日志面（error 无敏感字段）。
- 验收：`go test ./internal/store/ -run Deal` 全绿。

### T4 商家面 HTTP（handlers_deals.go，TDD HTTP 级）
- GET /api/v1/stores/{id}/deal-offers：ReadRecord(store scope, mask404)；items（全字段+price_state+click_total+applicable store_ids）+ by_platform clicks 汇总 + 诚实注记「点击不是成交；平台成交/核销本地不可知（除授权适配器事实）」。
- POST 同路径：Update；body 校验（platform 三选一、product_url 平台匹配、title 必填、价格≥0、store_ids 越权拒、provider_offer_id 平台内唯一→409 conflict）。
- PATCH /{offerId}：Update；可改 title/image_ref/product_url/price_minor/currency/price_source/valid_from/valid_until/note/provider_offer_id/apply_all/store_ids；mark_price_verified=true → verified_at=服务端 now（「校验时间管理」）。
- POST /{offerId}/status：Update；active|paused|retired（上下架）；retired 不可逆。
- dealsError 映射：ErrNotFound→404；ErrDealInvalid→422 invalid；ErrDealConflict→409 conflict；ErrDealAuthorizationRequired→422 authorization_required；ErrDealNotConnected→503 not_connected；跨店 404 掩码同 store actions。
- 验收：正例 CRUD 全绿；负例（未知平台/跨平台 URL/内网 URL/负价/越权 store_ids/跨租户 404/staff 越店掩码）全绿。

### T5 顾客读端点 + 点击 beacon（TDD HTTP 级）
- GET /api/v1/public/storefronts/{code}/offers：FEATURE_STOREFRONT off→统一 404（路由不注册+handler gate 双保险）；租户 lifecycle 非 active→404 unauthorized；停用店→state=disabled+offers=[]；payload schema_version="store_deals.v1"，白名单字段（offer_id/platform/title/image_ref/product_url/price_display/price_minor+currency 仅 show 时下发/valid_until）——**无 tenant_id/note/created_by/authorization_ref/未核实价格**；替代可见=全列表返回。
- POST /api/v1/public/storefronts/{code}/offers/{offerId}/clicks：同 flag；无会话匿名聚合 +1（view beacon 先例）；204；未知 offer/停用店→404 掩码；body 拒绝未知字段。
- 验收：开关 off 404、越态租户 404、停用店空列表、白名单字段冻结断言、未核实价不下发、点击计数只进 stats 表。

### T6 外部券 check/redeem HTTP（TDD HTTP 级）
- POST /api/v1/stores/{id}/deal-coupons/check（ReadRecord）/ POST .../deal-coupons/redeem（Update）：body {offer_id, code}。
- Server.DealCoupons deals.Adapter（默认 nil；测试注入 fake）。
- 未接通矩阵：adapter nil→503 not_connected（零行落库）；offer 无授权→422 authorization_required（零行落库）；适配器 unknown/传输错→503 result_unknown（零行落库）；平台 failed→200 outcome=failed（failed 行落库，可重试）；redeemed→200 outcome=redeemed（事实行）；同券重放→200 outcome=already_redeemed 回读（不再调适配器）。
- check：授权+适配器在→200 {valid,detail}；同样诚实未接通矩阵；零持久化。
- 验收：全矩阵绿 + 断言 reward_benefits 零写入。

### T7 web M9 外部团购 tab（TDD：vitest 文件断言+lib 单测）
- web/src/lib/deals.ts：platformText、priceStateText、offerStatusText、dealFailureText。
- page.tsx 外部 tab 替换占位：offer 列表（平台徽标/标题/价格态/校验时间/状态/适用门店/点击数）、新建表单（平台/商品ID/URL/标题/图/价格/来源/有效期/适用门店）、编辑、上架/下架/退役、标记已核对价格；1671 外部发放事实列表保留；诚实文案（点击不是成交；核销事实只来自授权适配器；未接通不假核销；待确认不是已发放）。
- 验收：新增 tests/deals-tab.test.ts（或扩展 reward-ops-page.test.ts）全绿；`npx vitest run` 全绿。

### T8 自审 + 全量验证 + 报告
- code-review 自审 diff（spec 符合性+写面纪律+敏感字段）；gofmt/go vet。
- `cd server && go test ./...` 全绿（唯一允许红=NamesMerchant 已知基线红，1896/2628 域）；`cd web && npx vitest run` 全绿。
- 报告写 reports/hui-4590-report.md：STATUS/SHA/测试摘要/concerns（Linear 不可用、TTL 72h 为自定常量、1671 gate 未采用声明、生产适配器未接线=诚实未接通）。

## 正反验收对照（T14 核心）
- 正：三平台各建 offer→顾客公共端点读到正确商品跳转（域名匹配）→点击计数入分渠道统计→授权适配器在时核销外部券出事实。
- 反：价格超 72h/未核实→payload 无价格（渲染「到平台查看」）；无适配器/无授权→核销 503/422 且零事实；停用店/悬置租户→404/空；点击只计点击（stats 里无成交字段，payload 无成交声明）。
