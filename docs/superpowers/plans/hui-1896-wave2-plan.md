# 计划｜HUI-1896 顾客六视图（Wave2 二批）

- 票: HUI-1896（G1 顾客六视图）｜ worktree: ../wt-touch-1896 ｜ 分支: touch/hui-1896-customer-views
- 基线: 1460146（wave1 六分支已合流 + 修复轮 1-4；go 全绿、web 803/803 绿，已实测）
- 纯 FE 票（迁移 0099-0101 不用）：全部依赖端点已在基线 main 或属并行票（fixture 契约验证）。

## 基线端点盘点（实测于本 worktree）

| 消费 | 端点 | 基线状态 | C 视图 |
|---|---|---|---|
| 1676 storefronts | GET /api/v1/public/storefronts/{storeCode} | **已在**（4584 数据面；storefront_revision=0、sections 默认单节、activity_cards/policy_links 空数组；FEATURE_STOREFRONT） | C1 |
| 3553 action content | GET /api/v1/public/actions/{actionId}/content + media-access + 受控媒体 | **已在**（FEATURE_CONTENT_PACKS） | C2 |
| 3546 sessions | POST /public/actions/{id}/sessions、/action-sessions/{id}/status・content・participations・resume-credentials、/resume/exchange | **已在**（FEATURE_PARTICIPATION） | C2/C4 恢复 |
| 3535 capabilities | GET /api/v1/public/actions/{id}/capabilities?environment= | **已在**（FEATURE_CHANNEL_ADMISSION；六环境 routes 展开） | C2/C3 环境差异 |
| 1671 权益 | GET /api/v1/visitor/reward-benefits、POST /api/v1/visitor/reward-benefits/{id}/display-code | **已在**（FEATURE_REWARD_OPS） | C5 |
| 1747 留资/撤回 | /api/v1/public/links/{code}/lead-form・lead-submissions・lead-status・lead-revocations | **已在**（既有 /contact 链） | C6 |
| 1670 handoff v2 | POST /api/v1/public/actions/{actionId}/handoffs、GET /api/v1/public/handoffs/{handoffId} | **不在基线**（并行实施）→ 契约 fixture 只进 vitest；生产面诚实待办态 | C2 交接 |
| 4589 services | GET /api/v1/public/storefronts/{code}/services | **不在基线** → 同上 | C3 内容 |
| 4590 offers | GET /api/v1/public/storefronts/{code}/offers | **不在基线** → 同上 | C1 套餐区 |

## 裁定书落点

- C1 `/s/{storeCode}` 门店首页（店名/地址/营业、意图分组常用入口、内容发布、点评/关注、Wi-Fi/微信、团购区、活动卡可选）；C2 `/s/{storeCode}/content/{actionId}`；C3 `/s/{storeCode}/service/{actionId}`（地址化宿主）；C4 `/c/{code}` **零改动兼容**+首页活动深链；C5 `/s/{storeCode}/benefits`；C6 `/s/{storeCode}/privacy`。
- 硬纪律：无 EcoTopNav；不强制注册/留资/下载；未配置动作不渲染（4584 白名单面天然保证）；素材缺失=诚实待办态；「换一个」只在已采用候选池（3553 candidates 字段）、零收费。
- 返回恢复：C2 进门即 POST sessions（幂等=同浏览器恢复）+ status（resume_available/selected_content_pack_version）；跨浏览器=customer_resume 凭据 POST 体兑换；凭据只在响应出现一次。
- 390/430/桌面全适配；环境（ios_safari/android_browser/ios_wechat/android_wechat/desktop）按 3535 routes 过滤渲染。

## 任务拆解（每任务红→绿→commit）

### T1 `src/lib/storefront-view.ts` + tests/storefront-view.test.ts
纯函数（零 React）：
- `StorefrontView` 类型 + `parseStorefront(payload)`：schema_version 校验、store（name/address/opening_hours/state）、sections、actions 白名单字段（action_id/action_revision/catalog_code/platform/kind/variant/label/presentation/capability_ref）；未知字段丢弃；非法 payload → null。
- `groupActions(actions)`：kind→意图分组（publish 内容发布 / review 点评 / follow 关注 / wifi / contact 微信 / groupbuy 团购 / share/other 其他），每项给出 href（publish→content/{id}；review/follow/contact/groupbuy/wifi→service/{id}）；分组空则整组不渲染（未配置不渲染）。
- `storeStateFace(state)`：active/disabled 诚实文案；`activityCards(payload)`：容忍解析 activity_cards（card_id/title/state/campaign_code），无 campaign_code 不渲染链接（不伪造深链）；`offersView(payload)`：契约 fixture 验证用（4590 契约形：platform/title/price_note/checked_at/state），state 非可购→「到平台查看」、售罄/失效停入口。
- 1676 fixture 验证：sections 多分组+revision>0 的契约样本走 parseStorefront → 结构正确（隔离数据面只进测试）。

### T2 `src/lib/env-capability.ts` + tests/env-capability.test.ts
- `detectEnvironment(ua)`：iOS Safari / Android 浏览器 / iOS 微信 / Android 微信 / 桌面 → 3535 六环境枚举（含 target_app 不由 UA 判定）。
- `pickRoute(routes, env)`：allowed && promised 优先；pending_external=true → 诚实「暂未放行」视图（永不降级 manual）；refused → reason_text；emergency_disabled → 停用文案。
- `methodLabel(method)`、`routeNote(route)`：按 method 给「普通跳转/App 唤起/平台确认/店员确认」独立真实状态文案。

### T3 `src/lib/store-session.ts` + `src/lib/store-handoff.ts` + tests/store-session-api.test.ts + tests/store-handoff-contract.test.ts
- store-session（fetch 薄面，同 customer-journey 纪律）：`startOrResumeSession(actionId, {source, expectedActionRevision})`、`sessionStatus(id)`、`switchSessionContent(id, version)`、`issueCustomerResume(id)`、`exchangeResume(token)`、`mintMediaAccess(version, {actionSessionId, mediaIds, purpose})`。凭据/令牌只在内存返回值，永不落盘；404=无会话非错误。
- store-handoff（1670 契约，**fixture 只进测试**）：`HandoffPlanView` 类型（plan.kind=refused|manual_only|official_handoff|oauth_publish、client_action、unsupported_fields、evidence_level）+ `parseHandoffPlan(payload)` + `planCopy(plan)`；unknown/未知 kind → 未知语气（绝不绿态）；`parseHandoffStatus(payload)`（attempt 状态→等待/完成/失败诚实文案）。契约 fixture 样本照票面请求/响应字段写进测试做本地契约验证，报告标「待 1670 联验」。

### T4 C1 `/s/[storeCode]`（app/s/[storeCode]/layout.tsx + page.tsx + storefront-home.tsx）
- layout：data-pn-surface="public.consumer"（同 /c 布局，无 EcoTopNav）。
- 首页：GET `/api/public/storefronts/{code}` → parseStorefront；状态机 loading/available/disabled/not_found/network_error（复用 failureState 语义+重试）；门店头（店名/地址/营业时间）；分组动作卡（T1 分组；空组不渲染；disabled 门店不渲染动作只给门店提示）；弱网/断网重试；活动卡（campaign_code 有→深链 `/c/{code}?entry=web`，无→不渲染活动区，基础服务照常）；底部「我的权益」链（→benefits）与「隐私与授权」链（→privacy）；响应式 390/430/桌面（复用 tk-public 卡片与 columns 模式）。
- 套餐区：GET offers 不在基线→404 时整区不渲染（production 诚实），fixture 契约测试保证 4590 合流后即渲染。

### T5 C2 `/s/[storeCode]/content/[actionId]`（content-view.tsx）
- 进门：GET content（404→「门店还没准备好在线内容」待办态；409→素材暂不可分发）；POST sessions 幂等恢复 → status（resume_available/selected_content_pack_version）。
- 预览：selected version 缺省用 content.selected_content_pack_version；POST media-access(purpose=preview, action_session_id) → `<video>` 真实播放 + 文案（title/body/topics/marking）；availability=missing 的媒体不给播放入口只给待办文案。
- 换选：candidates（已采用池）按钮；canSwitch=会话 active 且无 handed_off handoff；切换走 POST action-sessions/{id}/content；**已开始 attempt 不被悄悄替换**（session 冻结后端 409→「已交接版本保持不变」文案）。
- 复制/保存：文案复制到剪贴板（fe 无收费）；媒体保存走 media-access(purpose=download) 触发下载。
- 交接：POST handoffs（1670 契约）— 基线无此端点→交接区渲染「发布交接尚未开通」诚实态；plan fixture 契约测试保证 1670 合流后正确渲染 plan.kind 四态+unsupported_fields+unknown 不绿态。
- 返回恢复：status 的 selected_content_pack_version 驱动回显同版本；跨浏览器恢复码输入框（POST /resume/exchange，错误码 resume_expired/resume_used/target_mismatch 各自真实文案）；离开前会话已持久化（3546 服务端），无回跳可主动返回原入口（返回门店首页链）。
- 无活动动作（campaign_required）：内容预览照常可用（3553 无需活动）。

### T6 C3 `/s/[storeCode]/service/[actionId]`（service-view.tsx）
- 宿主：GET storefront → 找到本 actionId 实例（找不到=404 态「这个入口不存在」）；GET capabilities?environment=detectEnvironment(ua) → pickRoute 渲染方法与真实状态。
- 面板按 kind：review（点评指引：打开正确平台门店自主评价；零模板/AIGC/截图核验/领奖暗示文案）、follow（主页链接指引）、wifi（4589 服务面不在基线→「门店 Wi-Fi 信息暂未开通在线展示」待办态+请店员协助）、contact/groupbuy 同理；services 端点 fixture 只进测试验证契约解析。
- 地址化直开可用（无 referer 依赖）；返回门店首页链。

### T7 C5 `/s/[storeCode]/benefits`（benefits-view.tsx）+ RewardStatus 窄扩展
- GET `/api/visitor/reward-benefits`：401 →「需要访客会话才能查看自己的权益」（引导从碰一碰入口进入，不自动造身份）；items 空 →「还没有记录到可用的权益」；每条：状态（issued/redeemed）、券码掩码（复用 rewardCardCopy/maskCode 语义）、面值/发行方/适用门店（benefit 字段如实）、per-deadline 到期原因 expiry_reasons、redeem_blockers、external_state（pending_unknown 不拖黑已发放）。
- 展示码重签：POST display-code → token 一次性显示；过期旧码不续，重签短时新码（文案：旧码作废以新码为准；staff_verify 码不进本页兑换）。
- **活动结束/订阅到期不拦截**：读取不经参与门（服务端保证），页面不给「活动已结束看不到券」类错误；REWARD_OPS 关闭（404）→ 诚实「权益查询暂未开通」。
- RewardStatus 扩展：新增可选 `benefitsHref` prop 渲染「查看我的权益与凭证」链（无则不渲染，零行为变化）。

### T8 C6 `/s/[storeCode]/privacy`（privacy-view.tsx）
- 复用 /contact 链（lead-form/lead-submissions/lead-status/lead-revocations）；URL `?code=`（活动/门店链接上下文）存在且 lead-form enabled → 展示告知（接收商家/用途/必填字段/告知版本/独立营销许可）+ 提交 + 撤销；lead-form 404/disabled 或无 code → 诚实「本店暂未开通在线联系与授权管理」。
- 已提交查询：lead-status（submission_ref+phone）→ state 真实映射（pending 同步中=「待同步，商家还没确认收到」不伪报销售已收到；crm_paused/revoked 等如实）；撤销成功=revoked 文案（复用 LEAD_REVOKED_COPY 语义）。
- 拒绝营销许可不阻断浏览（表单文案与 /c 同源纪律）；页内完成，返回原动作链（history.back fallback 门店首页）。

## 验收对照（正/反例在本仓可验部分）
- 正例：无登录进 /s 用基础服务；点评/关注动作在正确分组下；内容可真实播放/保存；关闭重开同版本恢复；活动结束后旧权益可查可重签展示码。
- 反例（文案/结构断言）：无 EcoTopNav/生态导航/强制注册留资下载；未配置动作无死按钮；换选不动已交接版本；handoff unknown 不绿态；点评无代写/截图核验/领奖暗示文案；他人权益不可枚举（401/404 诚实态）。
- 390/430/桌面：响应式列模式断言 + 视口 meta（既有 layout）。

## 明确不做
- 不改 /c/[code] 与 touchpoints（1665 域）；不改 Go（零新端点/迁移）；不改 globals.css；不 push/不 merge。
- 不在生产路径渲染 fixture 数据：fixture 只存在于 tests/（契约消费方本地验证）。
