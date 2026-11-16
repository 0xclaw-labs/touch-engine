# HUI-4585～4588 平台适配器实施报告

日期：2026-11-14。基线：`d22da8e1d8e4a4799eb7bb44e66472d956dea188`。
工作树：`/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/adapters`。
本地提交：`bf430a5d4f6b483900407f703feeab5a8377fe6d`。未推送、未写 Linear、未标 Done。

## 当前交付判断

| 栏目 | 已验证事实 | 待完成/不能冒充的事实 |
| --- | --- | --- |
| 本组工程验证 | 官方协议生产装配、真实文件执行模块、错误/关联/幂等合同和下面的自动化测试通过 | HTTP 端点使用 TLS 工程替身，非真实平台发布 |
| 跨组真实集成 | 冻结下列接口与三个原核心接线点，源代码可供 customer 组集成 | 公共顾客组件不是本组写面；正常入口、RefreshPrepare、重签媒体和 unknown 回查仍需 customer/core 合入及独立浏览器复验 |
| 外部渠道与正式交付 | 保留原方法/场景准入；官方限制有当前官方依据 | 没有本部署主体控制台、获批 App/方法/奖励场景、正式账号或四类真机证据，四票均不能据此认定正式交付完成 |

## 实际增量与已复用基线

保留原 channeladmission 方法级裁决、touch-handoff/v2 核心状态/回执绑定、平台侧 0119～0123 表、手工家族平台差异步骤、受控媒体与官方结果/奖励分离。不重建原页面，不新建迁移，不改奖励、内容采用、门店/活动宿主。

1. 抖音：原 nil signer 仍产可执行形状已改为失败关闭。`official_http.go` 实装官方 client_token、openTicket 缓存和官方 MD5 签名；share_id 改为从官方 `/share-id/` 申请，不再自行造平台 ID。官方有效期按一小时封顶，恢复不会延长原 ID 的显示有效期。校验媒体 HTTPS，严格保留 share_id→租户/动作/会话/内容/App 关联；错上下文拒绝。前端仅用官方明确列出的字段生成 `snssdk1128://openplatform/share`，签名缺失/过期不调用；title/hashtag_list 仅来自服务端 `FieldSupport=supported`。
2. 抖音结果：新增可配置 HTTPS 受信结果服务 `PUBLISH_HANDOFF_DOUYIN_QUERY_URL/_QUERY_BEARER`，请求携带 client_key/share_id；响应要求同一关联、`state=published`、item_id 和 evidence_ref 才形成查询回执。**这是受信结果归集服务合同，不是捏造一个抖音官方查询 API**。现查到的官方页面实际说明 share-id 申请与 create_video webhook，未给可通用调用的结果查询接口；正式部署必须让该服务归集经官方验证的证据，否则保持 unknown。本票原 webhook VerifyCallback 仍明确 unsupported，不把自定义 HMAC 充官方验签。
3. 小红书：`XhsHTTP` 按官方 JS 指南 Java 示例实现两次 SHA-256（第一次 appSecret 换 token，第二次 token 签 verifyConfig），进程内串行缓存/到期提前刷新 token，公开载荷只含 appKey/nonce/timestamp/signature。AppID 单独存在不再发无签名载荷。前端默认加载官方固定版本 `xhs-1.0.1.js`，调用真实 `xhs.share({shareInfo,verifyConfig,fail})`，有加载超时/失败和异步 fail 回传。视频、封面、有序图片与独立参考文案保留；SDK 自动载荷无 title/content/topics；没有 JS 发布结果合同，始终只有交接/unknown。
4. 快手：生产注册注入 `KuaishouHTTP`，按网站 OAuth 官方 `app_id` 参数、code 换 token、start_upload、真实二进制/分片上传、complete、multipart 封面+caption 发布、photo/info 查询接线。result=1 仅受理；只在同 photo_id、明确 pending=false 且有播放链接时形成官方查询观察，visibility 仍标 not_attested，不声称公开范围。
5. 快手恢复与防重：回调 URI 改为已有 BFF `/api/public/handoff-callbacks/kuaishou/oauth`，经 Go canonical callback 处理后回服务端解析的 `/s/{store_code}/content/{actionId}`，携 handoff_id/oauth 提示。ReturnBase 严格 HTTPS origin；state 一次性 CAS 消费并核 App/期限；拒绝授权可回原内容页。有效 token 决定账号/confirm_submit，过期则重授权；发布、查询 scopes 分别校验。封面下载/上传失败不再静默继续。提交以持久 SQL CAS 占用 uploading，上传中/creating/未知发布结果禁止第二次创建；网络结果不明保留 creating，不回 uploaded 盲重投。
6. 素材/链接：新增 `execution.ts`，在分享按钮启用前预取真实 File，保图片/视频/封面次序，拒空文件/错误 MIME/403/过大；用户点击时直接调用真实 navigator.canShare({files})/navigator.share({files})。逐个保存操作使用真实文件+download。A27 公开链接直接 sharePublicLink，不要求媒体/内容包。所有返回只为 LocalHandoffFact/handoff_only 或 download_requested，不表示入相册、发朋友圈、发消息、平台发布或奖励。取消/无目标/权限不足/未知结果保留恢复语义；AbortError 不擅自断定一定由用户取消。

## 冻结接口：customer/core 必须接线

唯一共享 type 修改文件：`server/internal/publishhandoff/adapter2.go`，仅新增 `PrepareRequest.FieldSupport map[string]string`，缺失、unknown、unsupported 不自动填写字段。Start 和恢复必须从 3535 原方法记录传入；禁止让浏览器或部署开关自行授权。已删除原 KS IncludeCaption 技术开关；完整参考文案在 ClientAction.Caption，自动投稿只使用服务端按权威过滤出的 Params.publish_caption。

三个原核心接线点：

1. 恢复/GET 原 handoff 时重新 `adapter.Prepare` 并持久化 client_action，KS 应从有效服务端 token 呈现 account_ref/confirm_submit；oauth=ok 仅为提示，不能替代服务器账号/内容确认。
2. 调 Prepare 前沿同 action_session、内容快照重新签发 MediaRefs，刷新签名/短时 URL；不能创建新的 handoff、attempt 或换内容。KS ensureJob 收到新 refs 会更新原 job 的引用。
3. Submit 遇明确“结果未知”信号时把原 handoff 持久 unknown 或允许安全 Query。`adapters.ResultUnknownError` 实现 `RequiredAction() string == "query_original"`；core 可 `errors.As` 到该结构接口，避免 import 叶子包造成循环。此错误兼容 unwrap ErrCustomerActionRequired，但缺 OAuth/过期素材等普通前置条件错误没有 query_original，不能全部落 unknown。处理中/结果不明只查原任务，不能重投。

前端纯模块 `web/src/lib/publish-adapters/execution.ts`：

- `preparePlatformHandoff(raw) -> Promise<{files: File[]}>`：预取素材或 SDK，不调用平台、不修改剪贴板。
- `executePlatformHandoff(raw, {onFailure})`：用户主动触发 XHS/Douyin；XHS 异步 fail 必须交回 UI 更新错误状态。
- `sharePreparedFiles(files)`：必须在点击处理器里直接调用，先前已准备真实 File，避免 await fetch 消耗移动端用户激活。
- `sharePublicLink(url,title)`、`savePreparedFile(file)`：分别用于无媒体链接与逐个下载；`handoffFailureMessage(error)` 返回准确中文恢复说明。
- 返回 `{evidence:'handoff_only',operation:'sdk_invoked'|'system_share'|'download_requested',officialResult:'unknown'}`，不调用奖励、不新增总成功态。

## 官方来源（本轮查询 2026-11-14）

- 抖音 H5：https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/sdk/web-app/h5/share-to-h5 。明确服务器签名、MD5 公式、nonce_str/timestamp/signature、state 填官方 share_id、video_path/image_path 及用户确认流程。
- 抖音 token：https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/account-permission/client-token 。POST JSON、client_credential、缓存期限与重复获取影响。
- 抖音 ticket：https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/tools-ability/jsb-management/open-ticket 。GET /open/getticket/、access-token 请求头。
- 抖音 share-id：https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/video-management/douyin/search-video/video-share-result/ 。正式表格为 POST /share-id/，示例另写 GET；本实现按正式表格 POST，需真 App 验证；share_id 一小时，need_callback=true 才回调。
- 抖音使用规范：https://developer.open-douyin.com/docs/resource/zh-CN/dop/operation-standard/platform-capabilities/useclue 。H5 投稿具体能力仍排除商铺宣传/商品介绍/诱导分享；保持原 store_promo 拒绝，不用介绍页激励概述推翻具体规范。
- 小红书 JS：https://agora.xiaohongshu.com/doc/js 。搜索引擎当前可读完整官方页含脚本版本、真实调用、二次加签 Java 示例及自动文字限制。页面直接 open 抽取为空，已用官方域搜索完整核对；未以第三方教程作协议来源。
- 小红书概览/FAQ：https://agora.xiaohongshu.com/doc 、https://agora.xiaohongshu.com/doc/qa 。当前概览仍展示暂停接入，但不推断所有既有主体无资格；媒体须平台可访问，JS 回页/未报错不是发布回执。
- 快手网站：https://open.kuaishou.com/platformDocs/develop/web-app ；授权：https://open.kuaishou.com/platformDocs/openAbility/kwaiLogin ；创建：https://open.kuaishou.com/platformDocs/openAbility/contentManagement/createAVideo.html ；查询：https://open.kuaishou.com/platformDocs/openAbility/contentManagement/searchForVideos.html 。区分 app_id、两个 scopes、异步受理、pending 和查询状态。
- 系统分享：https://www.w3.org/TR/web-share/ 。需用户激活、真实 files，resolve 只说明交给目标/系统；AbortError 也可能没有可用目标。

## 工程验证与正常入口复验准备

环境：`source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`，真实 Go/SQLite/Node。无真实平台凭证或生产外发。

- 先红：`go test ./internal/publishhandoff/adapters -run TestUnconfiguredSignersNeverIssueExecutablePayload -count=1`，原实现失败：nil signer 仍发可执行载荷。
- 先红：`go test ./internal/publishhandoff/adapters -run TestKuaishouConcurrentSubmitCannotCreateTwoWorks -count=1`，原实现失败：第二并发请求创建第二份平台作品；修复后通过。
- 全相关 Go：`go test ./internal/publishhandoff/adapters ./internal/publishhandoff ./internal/config ./internal/httpapi -count=1`，全部通过（httpapi 118.224s）。新增最后一轮上传分片/签名向量后又单跑 adapters，通过（6.167s）。
- Web：`npx tsc --noEmit` 通过；`npm test -- --run tests/publish-adapters*.test.ts` 最终 6 文件 41 测试全通过。含真实 File 字节/顺序、直接用户激活调用、取消/无 files 能力、403、官方 XHS 方法与 fail、签名缺失/过期拒绝、抖音字段白名单；测试替身不升格为真机证据。
- HTTP TLS 替身测试覆盖官方 token/ticket缓存、官方 share-id、签名字段/密钥不出公开参数、XHS 官方示例固定 SHA-256 向量、KS multipart 封面/caption、实际视频字节与 11MiB 两分片+complete、pending 与最终观察分离。
- `git diff --check` 通过。额外 `go test -race` 长时间无结果，按总控开始 customer 集成的要求主动中止（exit 130）；没有计作通过。常规 Go 测试已完成。
- 正常浏览器入口必须是已发布门店动作 `/s/{store_code}/content/{actionId}`，消费原动作会话凭据和采用内容；不能跳裸测试 adapter 页面或改库造发布成功。公共页面接线后由 root 指派独立 gpt-6.1-terra 完整走查，本组没有代做/冒称浏览器验收。

## 仍未验证/外部阻断

- 正式主体、AppKey/secret、获批方法/场景/奖励玩法、正式回调域名与发布/查询权限无证据；四类手机环境（iOS Safari/Android 浏览器/iOS 微信/Android 微信）的未装 App、登录继续、取消、签名/媒体过期、真实视频/封面/图片顺序与官方结果均待对应设备账号证明。
- Douyin 官方结果归集服务未配置；当前默认 unknown。专项 miniapp 路线保持原独立资格与未核能力，不借 H5 资格启用或模拟原生调用。
- XHS token 缓存为单进程内生命周期；多副本部署需统一 token 归集或共享缓存以免平台重复发 token 相互失效，当前没有多副本生产证据。官方 JS 没有已核实发布成功查询/回执，不能把工程装配称为官方发布已完成。
- 快手官方上传文档展示 HTTP uploader；实现优先使用 HTTPS 且不自动降级。实际返回网关 HTTPS 是否可用必须正式环境证明；失败如实阻断，不能以明文降级绕过限制。
- KS 在已发 publish 但丢失 photo_id 的窗口保留 creating/unknown，官方无本实现可用的客户端幂等键或可靠反查关联时，不可自动重复创建；需要原任务/账号平台核对。本地查询不补造 photo_id，不保证能自动消除这个外部未知。
- 未新增点评内容或截图发奖路径；未把导出、用户自报、店员观察、SDK 唤起或系统分享转换为官方发布事实/自动奖励。
