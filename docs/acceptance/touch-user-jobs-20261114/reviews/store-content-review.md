# HUI-2628 / HUI-4655 / HUI-4657 独立审查（首轮）

审查版本 `d22da8e`，基线 `9d1e2fa`；固定工作树 `.worktrees/review-store-content`。本轮只读源码，浏览器使用同版本服务、本机临时库和 Identity 等工程替身。不是外部生产系统证据。总控已自行完成完整 web suite、类型检查和构建；本审查没有重复执行实现者整套测试。

## 阻断发现

**高：已发布动作的参与与内容读取仍使用最新草稿或最新采用版本，突破明确发布边界。** `server/internal/httpapi/server_action_directory.go:74` 的 `ResolveAction` 使用实时 `ListPublicStoreActions`，而非 `ActionsForStorefrontVersion`；同文件 126–139 行读取当前动作绑定及当前采用版本。`server/internal/httpapi/handlers_public_action_content.go:163`、175 行的公开内容接口也直接读取当前绑定和当前采用版本。`server/internal/store/store_storefront_versions.go:791` 虽冻结目标和绑定，但没有冻结具体内容版本；`server/internal/store/store_content_packs_store.go:760` 新采用时把旧版本设为 retired。商家从正常门店工作区发布一版，编辑已发布动作目标或选用新的内容版本而不发布，再由原顾客入口发起参与或读取内容，顾客参与冻结的动作修订及内容可提前变为新版，或原版本被退休导致不可分发。此结论由调用路径静态证据确认，尚未完成浏览器端全链复现；发布首页显示正确不能证明参与和内容权威正确。需统一冻结发布时的具体内容版本并让参与、公开内容与旧会话读取相应快照，同时保留真实撤权/过期实时否决。首发前既有 legacy fallback 与新工作区草稿语义需区分，不应粗暴关闭旧兼容。

## 已观察的正常入口

以 seed 普通商家通过 `/admin` 登录，进入商家首页，点“门店”；单店自动进入门店工作区。选择“Wi-Fi / 微信 / 社群”，新增并启用 Wi-Fi、保存，显示配置第 1 版和“请预览并发布”。返回“预览与发布”，保存草稿并预检；草稿预览展示刚配置的 Wi-Fi，勾选变更确认后发布成功，显示线上第 1 版。桌面截图 `runtime/store-publish-desktop.png`。此步验证的是工程替身环境，未证明外部渠道与真实设备。未对未发布前顾客页面、第二版未发布线上保持进行浏览器完整核验。

## 报价代码初审

`server/internal/production/quote_request.go` 使用服务端 scope 进行 usage→quote，确认时先查原账务事实，再通过 quote_id 与 expected_amount_minor hold；`handlers_content_quote.go` 校验签名报价、租户/门店/包、本人付款主体、金额/币种/有效期以及生产能力后才预占。此为静态初查，不等于真实 Billing/Task 完整合同通过。后续需要配置明确的工程付款 fixture，从正常用户入口完成报价前无 hold、确认后任务、丢响应恢复与不重复/不错付款主体的对抗验证；真实公共账务与生产方仍需独立证据。

## 逐票裁决

- HUI-2628：Needs fixes；已验证普通登录、导航、单店进入及桌面门店工作区，未完成 390/430、切组织/迟到请求、摘要异常、权限和全视图验收。
- HUI-4655：Needs fixes；桌面真实 UI 可配置 Wi-Fi 并经明确确认发布；公开参与权威与快照不一致是阻断项。第二版、撤权及并发冲突恢复待修复后沿正常入口复验。
- HUI-4657：Needs fixes；采用后当前内容可绕发布进入顾客权威，且旧版本退休影响已发布版本；上传、已有内容采用、原用途返回、报价、任务恢复及外部生产方均尚未完成浏览器验证。

本轮按总控协调在阻断发现后暂停对旧代码的更多可选全链；修复后须在相同审查工作树/版本从原入口复验，不能据本报告标记 Done。

## 第二轮独立浏览器复审（进行中，05bf121）

候选 `05bf121e060ada924ca70cfeb429754809e7e1eb`，本树 Next 生产构建通过。正常 `/admin` 表单登录→`/`→门店；本地 Identity、Billing、Task 是工程替身。保留 `/tmp/touch-e2e-9ceKxD/touch.db` 和同目录 media；同店 `sto_6c88242881222ec01fc591693ec89c47`，顾客页 `sf_f3431f9dcf10438d`，动作 `sa_84df977e105dd2c40262655f`，内容包 `scpk_8915756144ea8d7e97304a920d38b2a0`。保留库供同记录修后复验，非改库制造结果。fixture 初始 seed 自带历史引用；以下新媒体均由浏览器选择仓库内真实文件上传。

- 浏览器上传 tiny.mp4、pixel.jpg，标题正文填写、价格及期限留空，创建候选，点预览后明确采用，自动返回本店服务；添加 A01 绑定该包，保存、手动加入动作段、预检并勾选发布。顾客页读取第1版。相同包创建并采用第2版，未发布时顾客仍见第1版；再次保存预检并明确发布后顾客见第2版。PENDING_EXTERNAL 资格阻止真实参与，因此没有证明旧参与冻结、真实平台发布。首发前匿名接口、媒体、旧会话和 block/grant 由修复组 HTTP 测试覆盖，浏览器全链尚未复验。
- 图文：430px 下上传 pixel.png、pixel.jpg，选两张创建候选、预览、采用，返回同店服务；绑定列表可见 douyin/wechat。同包混合不同平台为本次操作结果；逐文件下载实际字节和顺序尚未完整验收。
- 报价：同店工程 fixture 初次报价 CNY 99，usage1/quote1/hold0/charge0/task0。修改正文后界面提示资料变化须重新报价确认；新报价确认后 usage2/quote2/hold1/charge0/task1；刷新页面仍显示同一条处理中记录。未注入响应丢失，未覆盖超时、权限、零价和存储故障的浏览器对抗。此结果不是实际支付或生产方交付。
- 390px 顾客页面、430px 内容编辑页无横向滚动（scrollWidth 等于 viewport），截图 `runtime/store-content-public-390.png`、`runtime/store-content-editor-430.png`；不代表完整移动可访问性验收。A01–A30 在服务目录 UI 可见，28 业务视图与多组织迟到响应未全走完。

### 待修与复验

1. 新店仅配 A01 后，点击「基础到店」自动生成空套餐段；点保存草稿并预检，显示笼统失败，无法说明哪个项目需处理。手工移除空段后可以继续，但这不是修复。root 已接手修补，待同店沿原路径复验。
2. 已采用且线上使用的内容条目只显示预览和下载，无「停止分发」；源码 `store-content-drawer.tsx` 限制 `!isAdopted`。商家无法在原用途从该条目及时撤权。root 已接手修补，待原记录复验即时顾客拒绝。
3. 正常添加并启用 `ReviewWiFiV1` 后，草稿预览展示该 Wi-Fi；未发布的顾客首页不显示，符合隔离。勾选确认发布，后台显示第3版发布完成，顾客首页仍无 Wi-Fi 入口（等待后仍无），预览与实际不一致。root 正修共享 C1，待同店同发布记录复验。

逐票当前裁决：HUI-2628、HUI-4655、HUI-4657 均未完成；本轮仅为局部工程和替身联调证据，真实身份、平台资格、付款/制作、手机/NFC 及其余未走场景仍无正式证据，不标 Done。

### e6ddf42 修复轮复验与新阻断

`e6ddf424f7367cc1b1e4af2607d9636771e1b47f` 已在审查树快进，Next 生产构建通过。关闭旧进程，新同版 Go/Next/Identity/Chrome 在同一执行生命周期启动，沿用 `/tmp/touch-e2e-9ceKxD/touch.db` 与同目录媒体；重新以 seed 同一账号正常登录。原浏览器 cookie 因驱动不支持导出未继承；未曾创建匿名参与，不能把这些操作当旧匿名会话冻结复验。

- 同店同已发布第3版顾客 C1：原缺失的「店内无线」出现，展开显示原 ReviewWiFiV1 和密码；复制在 headless 环境被拒时如实给手动复制提示，手动连接指引显示步骤。未证明剪贴板实际写入或真机连网。
- 同店「基础到店」不再生成空套餐段，保留原抖音动作，并说明没有套餐不加入；直接保存草稿并预检成功，无须手删，线上仍第3版。
- 同一包原已采用/被取代版本均出现停止分发按钮。以真实 UI 为第2版和第3版分别输入原因「素材授权到期复核」并停止，状态和原因显示。冻结在已发布第3版的真实默认第3版停止后，顾客原动作内容页立即显示素材暂不可分发、不展示原文。第2版停止后顾客仍见第3版，根因如下，不能据此把第2版称为该发布默认。
- **新阻断：跨平台采用导致发布快照冻结错误。** 同一包通过正常 UI 采用 douyin 视频第2版和 wechat 图文第3版；此后仅添加、预览并发布 Wi-Fi，后台发布第3版时 A01/douyin 被绑定同包，顾客访问原抖音内容页竟显示微信图文第3版。只读数据库核对：两条 adoption 正确分别 douyin→`scpv_b8034236c727b0013e0319fd25089186`、wechat→`scpv_7f27fa902ab6b098bb6d02b0eb852c0b`；发布快照 `sfv_4a07df063b632ff5813e4f81d7997d6d` 的 A01 `content_version_id` 已错误为 wechat 图文，候选列表还包含它和 douyin 视频。仅后续撤权无关此快照选择。`server/internal/store/store_action_publication.go:39` 的 FreezeActionContent 调用 `GetStoreAdoptedPackVersion(tenantID, packID)`；后者 `store_content_packs_store.go:813` 只按租户/包与更新时间选最新 adoption，未按动作平台选取；错误默认立即加入候选，之后只对其它项检查平台。需修后按同店原记录通过 UI 新建两个平台版本、重新发布与验证，不得 SQL 复活已阻断版本。现有发布数据和媒体保留。

逐票裁决仍为未完成。模板和 C1 原问题经同店复验通过，真实媒体撤权入口及实际冻结默认的即时拒绝得到有限浏览器证据；跨平台冻结新缺陷阻断 HUI-4655/4657，旧参与、媒体 ticket、grant 到期、报价丢响应及真实外部系统等仍待对应证据。

### 916bbf0 同原库复验中发现撤权状态被覆盖

候选 `916bbf0` Next 生产构建通过。以同一 DB/media、新进程重新正常登录（原 cookie 未继承）。原不兼容的已发布第3版在顾客页拒绝展示微信内容，提示门店尚未准备好在线内容，可咨询或稍后再来，不自动改用新采用版本。

从原店、原包的 UI，使用先前真实上传的 tiny.mp4 和图片，分别创建、预览、明确采用 douyin 视频第4版 `scpv_c005d40038e9d37ab6d077e66b0ee7bc` 与 wechat 图文第5版 `scpv_4e87bb2f180ebd6d103319d39eb1eeae`。采用后回本店服务；两者均未发布时旧不兼容线上仍拒绝。保存草稿第5版已预检，线上仍第3版；遵 root 指示暂停，未将其发布，故跨平台修复尚未完成浏览器判定。

**新高风险阻断**：此前通过正常 UI 有原因停止分发的 douyin v2 `scpv_b8034236c727b0013e0319fd25089186`，采用 v4 后状态从 blocked 变成 retired、界面改称“已被新版本取代”并重新出现停止按钮；wechat v3 `scpv_7f27fa902ab6b098bb6d02b0eb852c0b` 在 v5 采用后同样发生。只读 DB 确认两者 `blocked_reason` 保留而 `status=retired`；retired 在原分发权威下仍可分发，因此可能使已撤权旧版本重新可用。`server/internal/store/store_content_packs_store.go:758` 对旧 adoption 指针无条件写 retired。问题是覆盖显式 blocked，不是正常退休本身。root 已接手修并核对新旧内容域。保留同一库及媒体，不用 SQL 复活；修后须以原版本、合法旧快照/媒体或会话验证持续拒绝，再完成双平台与 Wi-Fi 发布复验。尚无旧匿名 cookie/参与事实。

### 6f3fb98 原记录复验：草稿时间导致发布后内容不可读

审查树干净后快进至 `6f3fb98cdde21e8b951b0e7f3863bc45f158975a`，独立 Next 生产构建成功，Go/Next/Identity/seams/Chrome 新进程在同一生命周期内启动；使用原 `/tmp/touch-e2e-9ceKxD/touch.db` 和 media，由正常启动执行迁移，未 seed 或 SQL 改业务数据。浏览器以原测试商家重新正常登录 `/admin`，cookie 未继承，后续点击首页→门店自动进入原店 `sto_6c88242881222ec01fc591693ec89c47`。

- 原内容包 `scpk_8915756144ea8d7e97304a920d38b2a0` 显示 v2 `scpv_b8034236c727b0013e0319fd25089186`、v3 `scpv_7f27fa902ab6b098bb6d02b0eb852c0b` 均为已停止分发，原因「素材授权到期复核」，且无重新采用入口。v4 douyin 和 v5 wechat 显示各自已选用待发布。只读原库确认两条均 blocked，说明迁移恢复了原禁令；截图 `runtime/store-block-6f3fb98.png`。本轮未直接通过用户可达界面对被禁版本重采用发起请求，不能把 UI 无入口视作服务端拒绝的独立 HTTP 实测。
- 原草稿第5版、线上第3版，直接运行预检，A01 可发布；勾选明确确认发布，线上第5版。只读版本快照确认 A01 `sa_84df977e105dd2c40262655f` 冻结 douyin v4 `scpv_c005d40038e9d37ab6d077e66b0ee7bc`，候选仅 v4，未混入 wechat v5 `scpv_4e87bb2f180ebd6d103319d39eb1eeae`。然而顾客 A01 内容页显示「门店还没准备好在线内容」，后台内容 GET 404；因此不能宣称顾客已读到 v4。
- **新阻断**：第5版草稿 `created_at=2026-11-14T11:41:17.364491301Z` 早于 v4 采用事件 `12:00:00.262482757Z`，但晚于采用才更新/保存并于 `12:24:41Z` 发布；`ActionsForStorefrontVersion` 把草稿创建时间作为 `filterFrozenActionContent` 采用事件截止，错误剔除合法已冻结版本。问题已即时通知 root，修复后须重启同版本服务并沿同原店第5版复验，不能另建新店或素材绕过。
- 基础到店模板于430px保留抖音入口、未插入空套餐，说明未配置项；仅操作未保存。截图 `runtime/store-6f-template430.png`。顾客首页 390px 显示店内无线和抖音入口，scrollWidth=viewport=390；工作区430px为430；桌面1280px为1280。截图 `runtime/store-6f-public390.png`。此为局部布局证据，不代表完整移动无障碍；C1 本轮仅确认入口，实际连接未证。
- 独立运行 `go test ./internal/store ./internal/httpapi -run 'TestNewStoreAdoptionCannotUndoContentBlock|TestNewLegacyAdoptionCannotUndoContentBlock|TestWorkspacePublicationContentBoundary|TestLegacySessionWithoutPoolAuthorizesOnlyRecordedAdoptedVersion|TestControlledMediaRefusesBlockedVersionCachedToken' -count=1` 两包通过；这些是隔离 fixture 的工程自动测试，不是本原库旧会话或旧签名 URL 的浏览器实测。

原 A01 资格 `PENDING_EXTERNAL`，未形成匿名参与会话；无法声明旧 cookie、旧合法冻结会话实际通过。真实渠道/官方结果、制作付费、实卡及真机均未证。逐票仍不能 Done。

### 47421ea 原第5版读取复验

审查树干净快进至 `47421ea352f70d362551f2c76b40e0e6c86df4a2`，独立 Next 生产构建通过，Go/Next/Identity/seams/Chrome 全部重新启动，沿用原 DB/media；正常商家重新登录，cookie 未继承。没有重新保存、发布或改库。线上仍原第5版、原 `published_at=2026-11-14T12:24:41Z`，没有改写。

从原顾客首页点击原 A01，页面现在实际显示「抖音复验第四版」「抖音四版内容」，不再是 404；原发布快照只含 douyin v4，未混入 wechat v5，截图 `runtime/store-content-474-public.png` 与 `runtime/store-content-474-public390.png`。390px 内容页宽度390、无横向滚动。正常首页→门店→添加内容，原 v2/v3 仍显示停止分发和原原因，v4/v5 分别显示 douyin/wechat 采用；430px 编辑页宽度430，截图 `runtime/store-content-474-editor430.png`。顾客首页展开 C1 显示原 ReviewWiFiV1 与连接提示，不能证明真机连网。基础模板在前一版本同记录无空套餐，当前版未重新保存或发布。

同一针对性命令再次于 47421ea 上独立运行，store 和 httpapi 均通过：`go test ./internal/store ./internal/httpapi -run 'TestNewStoreAdoptionCannotUndoContentBlock|TestNewLegacyAdoptionCannotUndoContentBlock|TestWorkspacePublicationContentBoundary|TestLegacySessionWithoutPoolAuthorizesOnlyRecordedAdoptedVersion|TestControlledMediaRefusesBlockedVersionCachedToken' -count=1`。这些是隔离测试数据的 HTTP/存储证据，不能冒充本原 A01 的旧匿名 cookie 实测。本原 A01 `PENDING_EXTERNAL` 未产生匿名会话；合法旧冻结会话、原旧签名重放、真实渠道及官方结果、制作付费、实卡及真机连接仍未获原场景证明。未据局部结果标 Done。
