# HUI-1671 实施计划｜权益兑现与六种到期

- 票: HUI-1671（G4 兑现与验收）｜ 裁定书: /Users/jimfu/works/派诺生态/touch-all-20261109/briefs/hui-1671.md
- worktree: /Users/jimfu/works/派诺生态/wt-touch-1671 ｜ 分支: touch/hui-1671-benefits ｜ 基线: 161fadc
- 迁移号段: 0068-0072 ｜ 写面: server/internal/rewardops（+rewardrule 扩展）、store_reward_ops 系、handlers_reward_ops 系、web /work/rewards；他人域只读（participations/campaigns 表只 SELECT）
- 流程: 每任务 TDD 红→绿→commit；不 push 不 merge

## 根因裁定（基线红 4 测试）

7 个基线红（含非本域 3 个）同一根因：测试夹具硬编码 `ends_at:"2026-10-31T00:00:00Z"`，今天（2026-11-10）已过窗口，`store.ResolveLink` 按活动窗口诚实判定 `expired` → 404 `state:expired`。**实现是正确的诚实到期行为**（SHARED-BRIEF 约束 9），过期的是测试数据。
裁定：修测试数据——本域 3 个共享 helper `activePublicCampaign`（server_activity_spend_test.go，属本域测试文件）与 `TestStorelessCampaignClosesParticipationHonestly`（server_public_visitor_test.go 内联行 447）改为相对 `time.Now()` 的动态窗口；**不动** server_public_visitor_test.go 其他内联日期（NamesMerchant/CapabilityFlags 非 1896/2628 域，保持原样）。共享 helper 修复会连带改变 TestWorkbenchShowsThreeAccountPanes 的失败面（同 helper），其自身断言不改，如实报告。

## 任务分解

### T1 基线红修复（测试数据动态化）
- 红现状已录：4 测试 404 state:expired。
- 绿：`activePublicCampaign` helper 改为 `starts_at = now-24h`、`ends_at = now+48h`（RFC3339 UTC）；storeless 测试内联日期同改。
- 验收：`go test ./internal/httpapi/ -run 'TestPublicClaimDoesNotCreateUserOrCash|TestPublicPageHidesEnterpriseBalance|TestExpiryKeepsCampaignAndRewardWithoutRecharge|TestStorelessCampaignClosesParticipationHonestly'` 全绿；其余 3 基线红失败面如实记录（不追）。

### T2 六种到期独立判定领域函数（rewardops/expiry.go，纯函数）
- `GrantDeadlines{SubscriptionUntil, CampaignUntil, ParticipationUntil, ReservationUntil, BenefitUntil string; ChannelOpen, ChannelChecked bool}` + `ExpiryOp`（promise/query/resign/redeem）+ `AdjudicateExpiry(op, d, now) ExpiryVerdict{Allowed bool; Reasons []string}`。
- 原因码固定六种：`subscription_expired / campaign_ended / channel_closed / participation_closed / reservation_ttl_elapsed / benefit_expired`；各判各的，一次判定给出命中的**全部**原因（查询时分别给出原因）。
- 语义：promise（新预留/新确认）被订阅/活动/渠道（已勾选时）/参与/券有效期拦；query 永不拦（返回展示用原因）；resign/redeem 只被 `benefit_expired` 拦（已发未过期权益按原承诺存活，T16）；`reservation_ttl_elapsed` 只影响占用释放（领域内已有 sweep），不拦任何操作。''=无期限（不判）。
- TDD：表驱动测六个期限 × 四操作 × 独立性（订阅+活动双过期但券有效 → redeem 通过；仅 benefit_expired → redeem/resign 拒）。

### T3 迁移 0068-0072 + store 落库
- `0068_reward_deal_offers.sql`：deal_offers（kind external_product|merchant_benefit、issuer 发行方、provider、provider_offer_id、authorization_ref、面额/币种/有效期/状态）+ `reward_inventory` ADD `offer_id TEXT NOT NULL DEFAULT ''`（兼容增量）。
- `0069_reward_rule_action_binding.sql`：reward_inventory ADD `action_id TEXT NOT NULL DEFAULT ''`、`action_revision INTEGER NOT NULL DEFAULT 0`、`reward_eligibility_ref TEXT NOT NULL DEFAULT ''`、`rule_freeze_ref TEXT NOT NULL DEFAULT ''`。
- `0070_reward_expiry_facts.sql`：reward_expiry_facts（benefit_id UNIQUE，六期限列 + judged_at）——六种到期字段落库，查询分别给原因。
- `0071_reward_display_codes.sql`：reward_display_codes（benefit_id、token_hash UNIQUE、purpose 固定 staff_verify_display、expires_at、used_at/revoked_at、created_by_visitor）。
- `0072_reward_external_issuances.sql`：reward_external_issuances（UNIQUE(tenant_id,idempotency_key)、state issued|pending_unknown|failed、provider、coupon_ref 敏感、detail）。
- store 层（store_reward_ops.go 或新文件 store_reward_expiry.go / store_reward_external.go）：offer CRUD 最小面、benefit 创建/确认时写到期快照、benefit 按 visitor 查询（只读 join participations.visitor_id）、display code 铸造/校验/查找、外部发放登记与同键回查。
- 幂等核实（裁定项）：0024 UNIQUE(tenant,participation,slot)、0025 dedup 部分唯一索引、TTL sweep 已在库——本任务只补「外部发放幂等持久化」与测试，不重造奖励账。

### T4 review 动作禁发 + REWARD_NOT_ELIGIBLE（双保险）
- rewardops：`ReasonRewardNotEligible = "reward_not_eligible"`；`Input` 增 `ActionKind string`、`ActionEligibility *bool`；Adjudicate：kind=review 或 eligibility=false → 拒 `reward_not_eligible`。`ValidateSnapshot` 同拒（创建/启用侧第一道）。
- store：CreateRewardInventory 接收并持久化动作绑定字段（0069 列），创建时 kind=review / eligibility=false 拒绝；ConfirmRewardBenefit 发放侧读批次存量字段再硬拒（第二道，防存量/绕过）。
- httpapi：rewardReason 映射 `reward_not_eligible` → 422 拒绝码 REWARD_NOT_ELIGIBLE；确认表单/库存表单透传 action 字段。
- TDD：配置「点评后领券」（review 触发）被拒；店员用点评截图证据（review 类）确认被拒；库存/账目不变（0 行）。

### T5 六种到期接线（promise gate + 旧券保护）
- httpapi：confirm 与内部 reserve 前置 `AdjudicateExpiry(op=promise)`（订阅=Cfg.SubscriptionStatus=="expired"、活动=EndsAt、参与=participation.Window（FEATURE_PARTICIPATION on 时已有）、渠道=暂无实时源 → ChannelChecked=false 不判、券有效期=批次 ValidUntil）；拒绝码用首个命中原因（422，reason ∈ 六码）。
- redeem/reverse/list 不加 promise gate：`RedeemRewardBenefit` 增加 benefit 自身有效期判定（到期快照优先，回退批次 ValidUntil）→ 过期拒 `benefit_expired`；活动结束/订阅到期不拦 redeem（T16 核心：旧券可核销）。
- TDD：活动结束+订阅到期后 confirm/reserve 拒（原因各归各）；同状态 redeem issued 券成功；benefit_expired 拒 redeem。

### T6 外部券边界
- store：inventory 带 external offer（kind=external_product）时，`RedeemRewardBenefit`/本地核销拒 `external_redeem_local_forbidden`（明确拒绝码，非 409 误读）；外部核销事实只能由授权适配器写入（登记接口收 authorization_ref，缺授权拒 `external_authorization_required`，不生成伪平台券）。
- `GrantExternalCoupon`：超时/传输失败 → 落 pending_unknown（待确认，不显示成功）；同幂等键重试只 Lookup 不新发；Lookup 确认 issued/failed 才升级状态；结果未知保持 pending_unknown。复用 rewardops.IssueWithRequery。
- 汇总面：RewardSummary 增 `external`（own/external/pending_unknown 计数）供 1677/运营台，不另存库存。
- TDD：本地账核销外部券被拒且账不变；超时→同键回查→待确认→不重复出票；无授权不发伪券。

### T7 旧券查询端点 + 展示码重签（不经新参与 Gate）
- `GET /api/v1/visitor/reward-benefits`：访客 cookie（touch_participation_visitor）本人查询有效券+核销码；**不调 resolveAvailableLink**；FEATURE_REWARD_OPS off=404（公共面纪律）。返回项含 status、本人 code、face、六到期原因列表。
- `POST /api/v1/visitor/reward-benefits/{id}/display-code`：持有效访客会话且 participation.visitor_id 匹配、benefit issued、券未过期（AdjudicateExpiry op=resign）→ 铸短时展示码（默认 10min TTL，哈希落库）；旧码过期自身拒绝（校验时 expires_at 判定）；可重复重签。
- `POST /api/v1/reward-display-codes/lookup`（session+authz read，店员侧 3562 消费）：码→benefit 定位卡；**不返回顾客身份**（无 visitor_id/联系方式）——店员码不能换取顾客身份；核销仍走既有 redeem。
- TDD：活动结束+订阅到期+旧展示码过期+券有效 → 重签成功并可被店员定位；过期旧码拒绝；伪造/跨 visitor 拒绝；店员 lookup 无身份泄漏。

### T8 web /work/rewards 页签
- 页面顶部加页签：`门店券库存`（现有批次/有效期管理移入，有效期列显式展示 valid_from/valid_until 与到期原因）｜`外部团购`（禁用占位，诚实文案「外部团购配置由 HUI-4590 提供，此处只显示状态」）。
- 不新建路由（SHARED-BRIEF 约束 11，28 视图按页签复用）；vitest 补页签渲染与外部 tab 占位测试。

### T9 全量验证 + code-review + finishing-branch
- `cd server && go test ./...`（本域全绿；其余 3 基线红保持已知并记录失败面）；`cd web && npx vitest run`。
- 自我 review diff（spec 符合性、他人域零写入、敏感码不入日志/公共面）；commit 全在分支，不 push 不 merge；写报告。

## 明确不做
- S1 核销台 UI（3562）、C5 顾客卡（1896）、外部团购配置 UI（4590）、渠道实时状态源（4589/3535 提供，本票留 ChannelChecked=false 缺省）、订阅到期真实投影（4591）。
- 不重造 rewardops 账本/唯一约束（PR#51 已合并，仅核实+补测试）。
- 不动 server_public_visitor_test.go 中非本域测试的内联日期。
