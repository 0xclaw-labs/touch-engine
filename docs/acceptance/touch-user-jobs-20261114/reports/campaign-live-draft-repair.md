# 活动草稿与线上承诺分离修复

- 分支：`codex/touch-campaign-live-draft-repair`；起点 `9b3de33ccc7a722fb05899e943dda0a49a16f4c1`。
- 本地提交：`8fa00ae68dcd7c01dc39bb55cdf4b9b85f8dea76`（20 个文件，`fix: preserve published campaign promise while editing drafts`）。未 push、未修改 main、未修改 Linear 状态。
- 需求依据：`issues/HUI-4656.md`“草稿与已发布版本分开，新参与读当前生效版本”；`reviews/customer-staff-review.md` 的 8c903fc 合同补核，以及 root 对原预留恢复边界的本轮裁决。
- 验收状态：工程修复，**仍待 root 的独立 Terra 同一正常入口浏览器复验及对抗审查，不作为 Done 依据**。

## 确定性缺陷

原 UpsertPublishConfigDraft 把 enabled 配置改成 draft。上一批 CampaignPublishedContent 据此拒绝新参与；其他读面又把新的草稿 spec 与旧 enabled_version/disclosure 混用，导致内容、顾客承诺、库存版本和资格不一致。正常 UI 只称“保存草稿”，没有执行或确认下线，因此不能靠补一段“保存会下线”文案合法化副作用。

第一条 HTTP red 使用真正的原配置保存/启用接口：启用 v1，正常采用新内容并保存 v2 草稿，再读公共 content-choices，原结果为 404。日志原文：`saving v2 draft withdrew live v1 choices: 404`。

另有两条独立 red：配置暂停但 campaign 仍 active 时，reward-confirm 原返回 200 并发新券；以及暂停已提交后才让确认进入 ledger 事务，原仍发券。两者不能仅靠 UI 隐藏按钮修复。

## 实现与唯一权威

### 保存、正式读取与显式发布

`store_campaign_publish_config.go` 保留原 `campaign_publish_configs` 编辑稿和 `enabled_version` 钉版，新增 `GetPublishedConfig` 只读解析原 `campaign_publish_config_revisions` 中对应版本的 enable/resume 修订。没有第二配置表、没有复制配置权威、没有迁移。

- 保存只更新编辑稿 spec/version 和 update 修订；原生效 pin、正式告知、显式暂停状态不变。初次未启用仍为 draft；已有线上版时生命周期保持 enabled 或 paused。
- GetPublishConfig 继续返回编辑稿；GetPublishedConfig 返回正式 spec/version/disclosure，明确区分两种用途。
- 正式修订必须有 enabled 状态、匹配的 enabled_version、合法 spec，以及与该 spec/version 一致的完整 disclosure。缺失修订、非法 payload、非法告知或内容不匹配，一律未知/拒绝，不回退当前草稿。
- 历史旧代码“保存后 status=draft，但原 enabled pin 存在且没有显式 PausedAt”的行，从原 enable 修订恢复正式读取；保存于 paused 的历史行仍因 PausedAt 保持暂停，不偷偷恢复。
- 显式启用按预检所见编辑稿 version 原子 CAS；并发保存发生在前时返回 version_mismatch，不把未检查的新稿顺带上线；修订与 pin 同事务提交。
- 新增 owner/scoped 的 POST publish-config/resume，只恢复原 enabled_version。保存于暂停期的新草稿保持原编辑稿版本，不随恢复上线；恢复同样检查 expected published version。

### 公开合同与实时权限

活动公开 choices/snapshot、参与 content/rule 派生、G3 snapshot、reward-summary 的 promise 和可用库存版本、campaignManagement 可参与性与活动状态预检，统一读真正正式版本。编辑与库存准备仍读编辑稿。

- 仅采用 v2 或保存 v2，不撤销正式 v1；新参与仍为 v1，旧参与/attempt/handoff 不改。
- 显式启用 v2 前必须有 v2 对应库存和原真实渠道/内容/门店/期限资格，不能拿 v1 库存或回查到的旧批次冒充新稿已准备。
- 正式运行预检复核原冻结内容的实时 block/grant、原渠道、门店、期限和原版本库存，不因当前采用指针换成候选 v2 就撤销已正式发布 v1。
- 新参与也执行同一正式版预检和领取窗判断；零余量、渠道撤权或其他真实缺项不会仅靠客户端标记绕过。
- 原未配置 publish-config 的 legacy 活动保留原路径；存在配置但没有合法正式修订，不回退 legacy 或当前草稿。

### 暂停、账本原事实与原预留恢复

在 Reserve/Confirm 的原 ledger 事务内，仅新增同一正式生命周期的只读校验。helper 支持 `tx.QueryRow`，没有在 SQLite 单连接事务中嵌套调用 s.DB。

- Reserve 仅首次或过期后重新预留检查正式生命周期；进行中的原预留续期仍遵守原合同。
- Confirm 只在实际新增 issue 前检查。暂停先提交、确认后进入事务时拒绝，原 reserved 行不转 issued。
- 已提交 issue 的同 event 重放、新 event 的 already_issued 事实回读及核销保持原样；暂停/结束不改写已发有效券。
- 恢复原正式版只拆出“必须有 AvailableQty>0”这条新参与余量条件。原库存真实存在、tenant/campaign/slot/version、类型、门店绑定、核验模式、active 状态和有效期仍检查；block/grant、渠道、门店及期限继续实时裁决。没有泛化吞掉 benefit_stock_missing。
- 全部余量已 reserved 时，可恢复原正式版并合法确认原预留；同刻新用户仍因零余量拒绝且不建新参与。

### 库存与 UI

- PrepareRewardInventory 新增可选 expected_version，UI 传当前已读编辑稿版本；pending 请求 checkpoint 保留原版本。原事务现有 version guard 继续兜底，不在并发保存后静默改绑。
- 原 request 的 GET 回查仍返回原批次与 rule_version；恢复到旧批次时，UI 明示它不是当前新草稿的库存，需另准备新版本。
- 配置页分别显示“编辑稿第 N 版”和“线上第 M 版生效/暂停/状态未读到”；保存、启用新草稿、恢复原线上版、紧急暂停分别表达真实后果。
- M9 也标明当前展示的是编辑稿还是同版摘要，以及原线上版状态，避免把未启用的新摘要说成已经生效。
- 原 campaign_rules 防薅羊毛规则仍复用原独立写源和版本合同；本次未将它复制进另一份配置或伪称受 publish-config 草稿冻结。

## 文件范围

后端权威：`store_campaign_publish_config.go`、`store_campaign_content.go`、`store_campaign_promise.go`、`store_reward_ops.go`（仅两处原事务只读闸调用）。
HTTP：`campaign_content_authority.go`、`handlers_campaign_config.go`、`handlers_business.go`、`handlers_reward_ops.go`、`server.go`（新增 resume 路由）。
UI：`campaign-config-panel.tsx`、`campaign-inventory-prepare.tsx`、`web/src/app/work/rewards/page.tsx`、`web/src/lib/campaign-config.ts`。
回归：新增 `server_campaign_live_draft_test.go`、`store_campaign_live_config_test.go`；纠正原 `server_campaign_content_contract_test.go` 和 publish-config/promise fixture 的错误前提；更新两处已有前端源码守卫断言。

未修改 content adoption 两文件/0137、billing、leads 或原 Terra 数据库。

## 验证与失败收敛

- 主 HTTP 正反链：v1 启用→采用/保存 v2→公开内容、G3 告知、promise、新参与仍 v1；v2 未备库存启用拒绝；准备 v2 后显式启用才切 v2；旧参与仍 v1。
- 保存于 paused 保留原 PausedAt；resume 原 v1 保留编辑稿 v3；不把未准备的新稿发布。配置暂停拒新参与、新发放；结束后原有效券仍可核销。
- stale enable expected_version 与 stale stock preparation 拒绝，无新库存半记录；原请求在后续改稿后仍回查到原批次；恢复的 v2 批次不满足 v3 启用。
- 缺失/非法正式修订拒绝公开分发、拒参与、UI 返回 published_status=unknown；不回退编辑稿。
- 8 轮并发 save/expected-enable：若 enable 先提交，正式为明确检查的 v2；若 save 先提交，enable 返回冲突且正式仍 v1；v3 从未未经确认上线，修订与 pin 无部分提交。
- 可控暂停竞态先 red：确认 goroutine 等待 channel，pause 提交后才放行，原仍成功 issue；green 后 ErrPromiseBlocked，原 reservation 保留。反向已提交 issue 的重放、already_issued 和暂停时有效券核销均保留；原进行中预留续期保留。
- 全 reserved HTTP 闭环：原正式库存仅一份→合法参与及受信预留占满→暂停→批次本身 paused 时恢复仍拒绝→批次 active 时恢复成功→新用户 benefit_stock_missing、不建参与→原预留沿原 benefit ID 变 issued，总量 1、可用 0。仅回退“恢复不要求新余量”一处时，测试重新得到 resume 422 的 red；恢复实现后通过。

使用 Go 1.26.5，命令前 source runtime/touch-env.sh。

1. 新 HTTP/content/正式修订/原成本冻结 focused：通过，httpapi 7.382s、store 3.638s。
2. 最终全 reserved 复现/修复：red exit 1；恢复实现后同一测试通过 0.243s。
3. 一轮完整六包（事务增量前）全部通过：httpapi 114.223s、store 75.101s、publishhandoff 6.948s、participation 8.765s、rewardops/campaignenable 各 0.003s。
4. 后续事务增量全量首次发现原成本 fixture 在“仅保存草稿、未启用”时直接预留/发券；已补 fixture 的正式启用和 active 前提，继续验证原成本未知/真实零的冻结，不放宽产品闸。
5. 最终 `go test -p 1 ./internal/httpapi ./internal/store ./internal/publishhandoff ./internal/participation ./internal/rewardops ./internal/campaignenable -count=1`：全部通过，exit 0；分别为 123.854s / 64.220s / 7.082s / 9.533s / 0.002s / 0.002s。
6. Next `npm run build`：通过，包含 TypeScript 与页面产物构建。
7. 前端 `npm test`：最终 120 个文件、1186 个测试全部通过，5.91s。首次仅上一批 getter 改名遗留源码断言失败；已改为核真实 campaignPublicVersion→CampaignPublishedContent 只读路径，保留顾客零生成守卫。
8. `git diff --check`：通过。

日志：`/tmp/campaign-live-final-go-complete.log`、`/tmp/campaign-live-web-build.log`、`/tmp/campaign-live-web-tests-final.log`。这些是本地工程日志，不包含正式平台成功证据。

## 待 Terra 复验与外部条件

请在 root 整合同一候选 SHA 后，从原商家正常活动配置入口验证：原线上 v1→仅保存 v2 草稿→新顾客仍见 v1；返回编辑页看到 v2 草稿和 v1 线上版；旧库存恢复明示原版本；明确准备 v2 并通过真实准入后才启用 v2。继续沿原参与、原券、暂停期间改稿、恢复原线上版、全预留后原办理与新用户库存不足走正反路径。

工程测试里的 Identity、渠道准入与受信 reservation 调用为明确控制 fixture；它们证明实现及跨模块 HTTP 合同，不证明正式平台已获准、真实平台成功回执、收费制作、支付、真机或实卡。没有为了浏览器成功放宽真实渠道，正式条件缺失继续列未验。

原集成边界补记：Server.New 的 Participation 构造仍传 nil reserver；本批全预留测试明确通过原受信 HTTP reservation 接口建立原预留，没有声称普通 participation/start 已自动完成库存预留，也没有接入成功桩。该现存集成边界交 root 后续正式全链验收判断。
