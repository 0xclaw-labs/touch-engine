# 原权益预留接线独立审查

2026-11-14，候选 `0a6fa46`（源 `a157df5`），独立树 `.worktrees/review-reward-reserver-final`。读 `reports/reward-reserver-repair.md` 及 79367fd..0a6fa46 差异，审查生产 StartResolver、原账本 adapter、原参与恢复、HTTP/BFF、C4 和交接闸。本报告区分初始发现、修后证据与未覆盖边界；该范围内以下复验结果不等同整体验收。

## 原库正常入口及负例

沿用 `/tmp/touch-e2e-ZLFKmv/touch.db`、media，官方 Chrome 桌面模拟、Go/Next/Identity/工程 seams 同一启动生命周期；未 seed、改库或伪造准入。Next production build 通过。以原商家 `/admin` 登录→`/`→推广活动→原「复核到店优惠」：仍为编辑稿第1版、尚未启用；点击启用后显示至少须一条准入有效渠道。原 C4 `/c/HDR2727ZSH71` 在430px 显示活动开放、推广优惠尚未启用，没有草稿承诺或开始按钮；截图 `runtime/reward-final-denied.png`、`runtime/reward-final-c4-negative.png`。它不能作为真实受准活动的预留正例。

## 工程合同与对抗

- 独立运行 `go test ./internal/httpapi ./internal/participation ./internal/store -run 'TestParticipationRewardReserver|TestCampaignLiveDraft|TestPublishedConfig|TestPublishedRewardPauseCommit' -count=1` 均通过。测试在受控 admission fixture、真实服务调用栈及 SQLite 下，覆盖正常 start 直接产生真实 reserved/原权益引用、最后一份并发唯一 winner、重复恢复同 p 同券及一个 reserve、跨 visitor 与跨 tenant/store、v1 原参与在 v2 上线后恢复原 slot/版本、未知提交前后回查、session rule 77 与奖励承诺 1 不混、BeginHandoff 未确认 409 不冻结、领取截止后原 issued 仍读到并暂停后核销。工程夹具不是正式平台准入。
- 独立运行 `npm test -- tests/customer-reservation-contract.test.ts`：4 项通过；测真实 Next catchall POST 原 p reservation 的路径与 visitor cookie 转发、组件静态状态和未知结果处理。不是实际已准入 C4 浏览器正例。
- 临时隔离测试 `reviewer_legacy_temp_test.go`：真实建立已 handed_off attempt 后，将预留端设未知，再从 legacy publish-handoff HTTP 入口请求，返回 409 reservation_unconfirmed；另将可信时钟推进至 hold 与领取期后，同入口 409。受控渠道目录为工程夹具，并非平台执行；没有断言每种失效竞态。

## 初始发现及处理

1. C4 原权益入口把活动短码拼作 `/s/{storeCode}/benefits`，对应参数是不同身份，导致错店码。root 正改为同一权益读面的活动上下文承接。
2. 隔离 HTTP 动态 red：正常创建早于首个正式承诺的旧 p（not_requested/required=false），正常首次保存并启用承诺后再 GET 同 p，返回 pending/required=true，旧合同被新承诺闸住。原记录未被改库；需以可证历史身份区分旧参与并防借新奖励库存，缺失/损坏历史仍 fail closed。后续已修并见下方复验。
3. 原库浏览器正常 C4 未启用、无预留下，展开「参考文案预览与历史手动操作」，预览→确认→导出，实际显示「请按步骤在官方 App 里手动发布」和打开抖音、粘贴及自行发布步骤（`runtime/reward-legacy-export-before-hold.png`）。同时明确旧奖励停用、证明按钮被禁，故此证据不是绕过发奖或实际平台发布；但它不只是只读，仍鼓励先做发布工作。后续已修并见下方复验，保真正 legacy 兼容。

正式渠道身份、生产平台回执/付款、实体卡、手机 NFC、原活动真实已准入预留及原券的浏览器正例均未证；不声称18票或28视图全部通过。
4. 库存过期死锁：`ListRewardInventory` 将全部 `reserved` 算占用，不按 reserved_until 区分过期；`ResolveStart` 先按该读值 `assemblePublishedFacts` 拒绝零库存，原账本 `sweepDueInTx` 却只在后续写事务触发，显式清理函数无生产调用。最后一份 hold 自然到期且无其他权益写操作时，新顾客被预检拒绝、不能到达释放它的 reserve；受控未来时钟测试出现 422 benefit_stock_missing，代码证据说明时钟并非唯一原因。需原权威一致时钟下证明到期后新 start 能竞争该库存且并发不超发。后续已修并见下方复验。

## 0ef5509 原路径增量复验

独立树切换到 `0ef5509`、Next production build 成功，沿用原 DB/media 并完整重启工程服务。原 C4 同一展开位置现叫「参考文案」，只显示原介绍和先确认名额提示，无导出/自报按钮；直接通过真实 Next BFF POST 旧 `publish-attempts` 得 409 `original_participation_required`，同活动 `reward-summary` 仍 200 且 `reward_not_enabled`。截图 `runtime/reward-fixed-reference.png`。新 `/c/HDR2727ZSH71/benefits` 真实浏览器显示本人空态，点「返回活动」返回原 `/c/HDR2727ZSH71`，截图 `runtime/reward-fixed-benefits-empty.png`。只读核查原库 participation 表无记录，无法不造资格地证明从同一原 p 点击入口或查询已发券；工程静态组件测试不能替代。原 `/s/{storeCode}/benefits` 保留。

## 1150423 历史参与增量

独立原 red：旧 p 经真实 HTTP 先于首个承诺建立，再经正常保存/启用首个承诺后读取同 p，已恢复 `reservation_required=false` / `not_requested`；原 unknown 和到期 handed_off 闸两条 HTTP 对抗仍通过。进一步在同一原测试插入中间步骤发现 **仍有 red**：仅保存首份草稿、尚未启用时读取旧 p 返回 `required=true/pending`，后续已修并见下方复验；未用原库造参与。以上均隔离工程 fixture。

首份草稿另一独立 red：真实旧 p 在保存配置前通过原可信预留接口取得有效 hold，之后仅保存首份草稿（未启用），对同 p/原 slot/版本正常 reward-confirm 返回 422 `reward_conflict`，未兑现原 hold。测试 `TestReviewerLegacyHeldBeforeFirstDraftConfirm` 使用独立 fixture；后续已修并见下方复验；无原 hold 的旧参与不得借新草稿库存。

## ae53f2f 修后独立复验

候选 `ae53f2f`，含 `5a42106` 草稿历史修复和 `2c0dfa8` 到期库存修复；与 `0ef5509` 之间无 web 文件变更，上述原浏览器复验对应同一前端代码，但没有将其称为 ae53f2f 全栈重启复测。

- 同一独立 HTTP red 用例原路径重跑：旧 p 在首份草稿保存后及首个承诺正式启用后均维持 `reservation_required=false / not_requested`；先前真实预留的同一旧 benefit，保存首份草稿后可确认原权益而非另建。两项由先前 red 转绿。原已 handed_off attempt 在模拟预留未知、以及 hold 和领取期已过的两条直接 legacy HTTP 请求均 409。临时审查测试已归档 `/tmp/reviewer_legacy_temp_final_test.go`，并从审查树移除。
- 独立运行 `go test ./internal/httpapi -run '^(TestReviewer|TestRewardExpiredLastHoldReadOnlyAvailabilityAndNewStart|TestRewardAvailabilityExpiryUsesSameInstantAsLedger)' -count=1 -v`，四条 Reviewer 测试与 HTTP 到期测试通过；另 `go test ./internal/store -run '^TestRewardAvailabilityExpiryUsesSameInstantAsLedger$' -count=1 -v` 通过。到期测试使用统一受控业务时钟：到期前仍占用、到期后公开读不写而恢复余量、两名新顾客并发仅一人取得最后份、原 p 再恢复失败；边界测试覆盖到期瞬间、issued/redeemed 仍占用。分配仍经原账本事务 sweep 与库存裁决。
- 审读新增来源判别：完整草稿创建/更新历史与当前草稿一致，才能证实先于首稿的旧 p；无原 hold 不获新承诺库存；原 hold 可以按原权益履行；缺损历史按未知关闭。具体损坏矩阵由实现者测试，非本次浏览器实测。

本审查在以上工程与浏览器范围未再发现未修阻断；原活动无真实有效渠道及 participation/已发券，真实获准 C4 开始→预留→恢复、同 p 点击权益及已有券核销的浏览器正例仍未证。正式 Identity、平台、支付、制作、手机/NFC 与28视图逐一覆盖同样不能据此推断。最终整合全量测试由 root 负责。
