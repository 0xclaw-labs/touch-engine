# 活动内容读契约与真实媒体交接修复

- 负责范围：HUI-4656 / HUI-4657 / HUI-4660 集成断链；仅本地分支 `codex/touch-campaign-content-contract`，起点 `e6ddf424f7367cc1b1e4af2607d9636771e1b47f`。
- 提交：`0758683`（`fix: unify campaign content reads with store media authority`），13 个文件，本地提交，未 push。
- 独立验收状态：**仍待 root 安排 Terra 正常浏览器复验与对抗审查；本报告不作为 Done 依据。**

## 原始缺陷与定位

Terra 正常入口创建活动，在 StoreContentDrawer 上传 tiny.mp4、pixel.png、建候选并明确采用 douyin，返回第三步 CampaignConfigPanel 却显示没有已采用内容；再次打开同一 drawer 则显示 v1 已采用。原活动 `cmp_6f20f5350102a4c00e08b5ffd22abb01` 与 `/tmp/touch-e2e-ZLFKmv/touch.db` 未被本实现修改。

写端 `stores/{store}/content-packs/{pack}/versions/{ver}/adopt` 使用 `store_content_*` 以及真实 `content_media`。活动列表、启用事实、C4 choices/snapshot、旧活动 handoff 则只读 `content_*` / `lib_assets`。旧资产引用与新真实媒体快照并非同一模型，直接转换 asset_id 或另造 libasset 会丢失真实媒体权威。

新增 HTTP 回归先验证了相同写路径：真实上传 mp4/png、建版本、adopt 200 后，活动 GET 返回 `items:[]`，得到真实 red。

## 实际修复

1. `server/internal/store/store_campaign_content.go` 提供活动专用只读 union：包、版本、采用指针、新分发正式版本。保留原 pack/version/media ID；旧表仍通过旧 getter 读取，旧写路径完全未扩展。新包必须明确绑定该 campaign，且 tenant/store 与 campaign 一致。本店通用包、其他活动包不会进入该活动。
2. `CampaignContentVersion` 显式区分旧 AssetSnapshot 与新 StoreMediaSnapshot，保留 MIME、字节数、时长、宽高、顺序、hash。新快照不伪装为 libasset。新旧含非空 blocked_reason 的历史错态，在只读投影中继续视为 blocked，拒绝再分发，不反写版本。
3. 活动内容列表与 `assembleEnableFacts` 改用同一适配；每个所选平台必须明确采用所选 version。列表保留同平台多个包，精确 pack/platform 查询；未指定包且多包歧义时不猜最近指针。活动列表补用既有 campaignScoped 检查门店范围。
4. C4 公共 choices/snapshot 与 handoff plan/start 使用原 ID 的统一查询。有 publish config 的活动，只从 `Status=enabled` 且 `Version=EnabledVersion` 的正式配置分发；新采用 v2 不替换 cfg 仍正式使用的 v1。无 config 的旧活动维持原采用池读法。
5. 新参与按已启用配置由服务器确定 content/rule version，拒绝客户端注入其他版本或规则号，draft/paused 不接受新参与。旧 participation snapshot/handoff 使用原选择；配置保存或采用换版不静默替换。内容 PATCH 写服务器解析后的版本，空输入不能清除正式冻结内容。未配置且关闭内容功能的旧参与接口继续支持原 opaque 引用，但这种引用不授予真实媒体读取或签发权限。
6. `publishhandoff.Service` 新增可选 `MediaMint` callback，默认未处理时保留旧资产协议。服务器只为真实门店媒体接入现有 MediaAccess 签名、审计与兑换路径。签发前核原 handoff、本人 participation、精确冻结 attempt、platform、tenant/store/campaign、version、每个媒体的 hash/MIME、真实文件存在；不能仅凭同包签任意版本。兑换继续复用现有 token 校验、期限、版本归属与 block/grant 到期检查。
7. 新发现的原授权边界问题单独补了最小修复：旧 handoff 幂等分支必须匹配 visitor、participation、platform；同租户其他参与不能借旧 key 拿到原媒体。legacy handoff GET/cancel/done 等本人入口再核 URL 所属 campaign，不能借另一活动链接读取原 handoff。

未修改 root 独占的 storefront 发布冻结文件、公共奖励文案、采用写行或迁移；无新增迁移，无双写/复制内容权威。

## 回归与证据

测试源：`server/internal/httpapi/server_campaign_content_contract_test.go`。旧 handoff HTTP fixture 原使用占位 `v1`，现改为原实际已采用旧版本，继续验证旧内容路径，未改产品权限。

- 初始 red：新门店真实 upload/version/adopt 成功，活动列表丢失，`campaign list lost store-root adoption: map[items:[]]`。
- 活动列表保留原 pack/version/media ID、完整真实快照；原 ID 未写入 `content_pack_versions`（无第二内容权威）。
- 正常 HTTP publish-config 保存→真实启用事实→enable 成功，正式版本仍为原门店内容 ID。
- 同平台多个包、同包跨平台采用不同版本，读取各自原 version；歧义不猜最近。
- 已采用 v2 未正式发布时，公共 choices/snapshot 与新参与仍为已启用 v1；draft 保存阻止新参与，旧参与仍读原 v1，不能 PATCH 注入 v2。
- 真实 handoff 与真实 adapter Prepare 均能匿名 GET 获得 tiny.mp4 / pixel.png，与上传文件逐字节相同，Content-Type 分别为 video/mp4 与 image/png。
- 原参与后采用新版本，handoff 存储的 version 仍为 v1；撤权后拒新引用与缓存 token。
- grant_until 使用真实三秒期限并等待自然到期，无改数据库制造成功；到期 snapshot、新参与与已签 token 均拒绝。
- 通用包不进入活动；跨 tenant/campaign 查询拒绝；现有旧内容/参与/手动交接测试保持兼容。
- 独立 red→green：另一参与复用原幂等 key 原返回 200 和原媒体，现 404；另一活动链接 GET 原 handoff 原返回 200，现 404。
- 独立 red→green：空内容 PATCH 原返回 200 且清空 content_pack_version，现返回原服务器派生 v1。
- 独立 red→green：模拟旧 supersede 已破坏的 `status=retired, blocked_reason=版权方撤回` 历史错态，适配原读为 retired 可分发，现 effective blocked、snapshot 409，原数据库行仍 retired，证明只读防御而非隐藏写入。原采用写缺陷和历史数据迁移由 root 的另一个补丁负责。

运行环境：`source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`，Go 1.26.5。

1. `go test ./internal/httpapi -run TestCampaignContentContract -count=1`：通过（最终增加跨店/跨租户 HTTP 负例后的 5.402s）。
2. `go test ./internal/httpapi -run 'TestCampaignContentContract|TestParticipation|TestCampaignNormalHandoff|TestCampaignRewardCannot' -count=1`：通过（8.209s）。
3. 一轮 `go test ./internal/httpapi ./internal/store ./internal/publishhandoff -count=1`：全部通过，97.854s / 56.757s / 6.485s。
4. 后续全量运行：httpapi 仅原 `TestCacheConcurrentMissMerged` 在高负载下报 dashboard loader 2 次，store 与 publishhandoff 通过（70.610s / 7.178s）；该原缓存测试定向 `-count=3` 通过（0.652s），未修改缓存实现或测试。
5. 最终 `go test -p 1 ./internal/httpapi ./internal/store ./internal/publishhandoff -count=1`：全部通过，118.882s / 69.160s / 7.276s。该串行运行后追加的跨店/跨租户 HTTP 负例也已通过上述最终 focused 命令；生产代码没有再变化。
6. `git diff --check`：通过。

## Terra 正常入口复验建议

使用 root 整合后同一候选 SHA，原账号从商家门店工作区正常入口进入原活动：上传/复用真实 mp4/png→候选→原平台明确采用→返回第三步。应能选择同一原版本并通过内容准备检查，而非只改空态提示。继续沿原活动库存/规则/渠道真实能力检查启用；记录原 pack/version ID。

分别从正常 C4 开始新参与、回到旧参与，采用另一候选但暂不上线，再刷新/关页恢复：新分发应遵从正式 cfg，原参与/attempt/handoff 保持原冻结；检查真实媒体下载与匿名平台抓取、blocked、自然授权到期、跨范围与错误 visitor。请结合 root 的 0137 修复复验“先 block 旧版再采用新版”不会撤销旧版撤权。

本轮工程验证使用测试身份、明确受控的准入记录和测试 adapter；未证明外部生产平台资格、真实账号发布成功、正式回执、收费制作、手机或 NFC 实卡。未 push，未改 Linear 状态，未改 main。
