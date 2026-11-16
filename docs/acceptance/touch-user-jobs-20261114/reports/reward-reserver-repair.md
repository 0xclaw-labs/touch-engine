# 正常参与 → 原权益真实预留修复

日期：2026-11-14。分支 `codex/touch-reward-reserver-repair`，基点 `e2cfe3a`。本地提交 `a157df54e3aa7ea3086fd97b7563f7385ffecaa3`。

## 根因与真实红证据

生产 `Server.New` 将 participation 构造为 nil BenefitReserver，正常公开 POST `/api/v1/participations` 仅写原参与，返回 `not_requested`，没有原权益账本 reservation。`TestParticipationRewardReserverNormalHTTPStart` 在实现前真实失败，响应即 `not_requested`。工程手动调用 internal reward reservation 不能证明正常顾客链路。

## 唯一权威、版本映射与冻结

启动时接受信 `campaignBenefitReserver`，allocation 仅调用原 `Store.ReserveRewardBenefit`，继续使用原 reward_inventory / reward_benefits / reward_operations 事务、库存守恒和幂等，没有新库存表、券表、迁移或成功桩。

- participation.BenefitSlot 稳定等于原 p.ID，作为整个参与的幂等身份；rewardops benefit_slot 来自该参与冻结的正式 publish-config Spec.Reward.Slot，两者不混用。
- action session.RuleVersion / RuleSnapshot 继续是原 campaign_rules 反薅羊毛快照；仅正式奖励活动在真正新建参与时由 GetPublishedConfig 冻结参与 RuleVersion=正式承诺版本。HTTP 回归用 session 77 / reward 1 证明互不覆盖。
- 普通 start 与 action upgrade 共用受信 StartResolver。正式版由原 enable/resume revision 解析。客户端 chosen content 不符正式承诺直接拒绝，不偷换已确认作品。无配置 legacy 继续 not_requested，明确 reservation_required=false，不声称已预留。
- 已有原 p 先认本人、原活动和原门店，再读/恢复。原 p.rule_version 通过 GetPublishedConfigVersion 解析其原历史正式修订；FirstPublishedConfigAt 排除正式承诺出现之前的旧参与被反套新承诺。新 v2 不影响 p1 的原 v1 slot、原库存与内容。
- 原预留为最后一份时不重新要求新的 free stock；真正的新占用仍由原账本事务裁决。暂停、结束、期限、内容 block/grant/渠道撤权继续对尚需预留的任务有效。
- 只读投影复核原 reservation 是否仍有效；既发 issued/redeemed 事实先认，不用领取期限制仍有效的原券。身份解析与尚需领取资格分开。领取过期 → 原 issued 仍 confirmed/同引用；新用户仍拒绝；暂停后原券仍可核销。

## 未知结果与顾客恢复

新建参与与奖励账本是现有两个事务；不能声称原子回滚。前者成功、后者未知时保留同一 p.pending；明确拒绝为 failed。超时无提交与提交后丢响应均有真实账本回归。GET 仅投影原事实；POST `/api/v1/participations/{id}/reservation` 只允许原 visitor 恢复同一 p；不创建替代参与、不改 frozen rule/content、不重复占用。首次 allocation event 按 p.ID 稳定，确需过期续留使用原 reserved_until 派生确定性重试键；有效原 hold 重放不追加 allocation。

DTO 增加 reservation_required。C4/ActivityIntro/RewardStatus 在 required=true 且状态非 confirmed 时仅提供原 p 回查恢复，隐藏有奖励发布与完成/核验动作，保留原权益入口。未知 HTTP 5xx/网络结果回查原参与，稳定 start key 不换新参与。BeginHandoff 在冻结 attempt/签发凭据前核原 hold，未确认不冻结、不签发；legacy start 入口亦覆盖复用 handed_off attempt 的检查。没有修改 root 的 ActionPublishFlow 或媒体导出面。

Next 实际 catchall route 的 POST 已测试：`/api/participations/p1/reservation` → `/api/v1/participations/p1/reservation`，保留原 visitor cookie；无新增白名单缺口。

## 验证与边界

正常 HTTP start → 原 reserved / 原权益编号；最后一份并发仅一 winner、重放同 p/同权益且仅一 reserve 操作；同 key 异 visitor 拒绝，原 p 恢复异 visitor 404；跨 tenant/store port 拒绝；action 77→reward 1、确认作品冲突拒绝；超时未提交/已提交均恢复原 p；v2 上线后 p1 仍原 v1 库存；未确认不得交接；暂停/结束/领取过期拒新任务；原 issued 不受领取结束/暂停核销影响。

完整 6 包首次结果：participation、store、campaignenable、contentpack、publishhandoff 通过；httpapi 两个兼容差异已修并定向复跑通过：恢复 legacy grant_expired 码；promotion permission 负例夹具改成真实 legacy 参与先存在、随后保存未发布奖励草稿，仍证明草稿不能借 promotion 准入完成奖励任务。没有为了测试放宽正式启用或渠道条件。

前端全量：121 文件 / 1194 项通过；其后新增 5xx 未知结果测试，相关 15 项通过。`tsc --noEmit --incremental false` 通过。最终 Go 定向日志见附录。最终整合候选的全 Go/web/build 由 root 统一完成，不冒称这里的首轮 httpapi 全量已绿。

测试中的 admission directory、时间与丢响应仅作明确受控夹具；真实 allocation/SQLite/HTTP/正式修订保持原权威，不属于正式平台准入或浏览器证据。待独立 Terra `/root/review_store_block_final` 按原正常 UI 路径复验，未准入平台保持未验，不 push、不更新 Linear、不标 Done。

## 日志

- `/tmp/reward-reserver-go-suite.log`：首次 6 包全量及两处已修兼容差异。
- `/tmp/reward-reserver-final-focused.log`：最终 HTTP 定向合同矩阵。
- `/tmp/reward-reserver-issued-expiry-red.log`：恢复旧提前 expiry 闸后的确定性 red，随后恢复修复再 green。
- `/tmp/reward-reserver-web-suite.log`：前端全量 121 文件通过。
- `/tmp/reward-reserver-tsc.log`：TypeScript 检查。


## 独立审查追加：历史合同及自然到期库存

### 历史合同保护 `d3d7f548f7df373bc4c290fcea57acf2f95b9e1a`

Terra 的真实正常 HTTP red：无 publish-config 时的旧 p 原为 not_requested/required=false，后来首次正常启用 v1 后却被投影成 pending/required=true。本树同路径再次 red 后修复。共享只读 origin helper 用 tenant/campaign scoped 的原 p 出生时间、原 config 创建时间及最早有效 enable 修订证明 genuine legacy；同叫 ruleVersion1 不代表旧 p 接受了后来首次发布的优惠。正常 GET、原 key 重放、同 p 恢复与原 attempt 凭据仍保持原无承诺合同，没有新权益分配。

原 Reserve/Confirm 事务的新分配边界复用相同身份判断；旧 p 无原已成立奖励合同时，受信调用也不能借后来正式库存。已经 issued/redeemed 的原事实不经新承诺判断；首次正式发布之前已真实成立且仍有效的原 reserved 合同仍可按原记录确认。原已发券核销保留。缺失/损坏发布证据、缺少 v1 但 v2 仍在、跨 tenant 或伪造 version 不回退为 legacy。helper 支持原 tx，未在持单连接事务时再调用 s.DB。

定向结果：HTTP reward/history/live-draft 3.603s；store 奖励/配置 9.058s，participation 0.623s；缺 v1 仍有 v2 独立用例 0.593s。后续 root 另行接手“只保存首份草稿尚未首启”分支的 red/窄修，此分支没有混入后面的库存提交。

### 自然到期库存 `2c0dfa859b1fea0a79c5905842c7a71591ffece0`

真实 red：最后一份正常 start 留下 600 秒 hold，共享业务时钟推进 11 分钟，公开 summary 仍 available_qty=0/can_start=false；新参与在进入原事务清理前就被库存预检拒绝。根因是 ListRewardInventory 只按 status 计全部 reserved，且 sweep 只在写事务执行。

修复新增显式 AsOf 只读库存/摘要投影，只有 RFC3339Nano 能解析且在本次时刻确已到期的 reserved 才不计占用；未知/空/非法期限继续占用，issued/redeemed 始终占用。默认生产时钟为真实 UTC，Server.rewardClock 仅作为明确测试时钟接缝；参与创建、reserver、公开摘要、预检、原账本写共享业务时钟。只读投影不改原行、不追加 expire 操作，分配仍进入原 Reserve 事务 sweep 后裁决。sweep 与只读共享 Go 时间解析及同一截止瞬间判据，避免 RFC3339Nano 字符串在整秒/分数秒处比较失真。

HTTP 回归：未到期余量仍 0；自然到期后 summary 可用；浏览后原 row 仍 reserved 且没有 expire 操作；两位新顾客并发只有一个 confirmed，原账本仅一个占用；旧 p 恢复因新名额已占用准确 failed，后续 GET 保持原已明确失败，不能投影回已保留。到期时没有更新任何原期限、没有写原浏览器数据库。

原账本真实方法回归：到期前 1ns 不回收，到期瞬间释放；issued/redeemed 与另一份尚有效 hold 继续占用；读不写，新的真实 reserve 才提交 expire 操作与重新分配，库存守恒。两个旧固定历史时点测试改为显式 AsOf 查询，保留其原重启一致性/当时未过期预留断言。

最新最小相关测试：HTTP 4.132s；store 奖励/配置 8.373s；participation 0.574s，全部通过。最终候选全套仍由 root 统一验证，独立 Terra 在 root 合并后复验。两个追加 commit 均未修改前端权益链接/导航，未 push/Linear/Done。

追加证据：`reward-reserver-repair-evidence` 内 `reward-legacy-origin-red.log`、`reward-legacy-origin-focused.log`、`reward-legacy-ledger-focused.log`、`reward-expired-hold-red.log`、`reward-expired-hold-read-state-red.log`、`reward-expired-hold-http-focused.log`、`reward-expired-hold-store-focused.log`。
