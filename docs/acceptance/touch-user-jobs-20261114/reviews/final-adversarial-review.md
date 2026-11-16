# 末轮独立对抗审查（进行中）

候选 `3da2f2c23f20e36569154ba3ed70ef24b4dc416b`，独立 review-customer-staff 树干净快进后 build 成功；保留 `/tmp/touch-e2e-ZLFKmv/touch.db` 与 media，未 seed。审阅 9dd04a7 至候选中适配器、共享交接、统计和旧首页发布兼容相关路径，工程身份与上下游为替身。以下发现已即时报 root，未自行修改产品代码。

## 发现

### P1：A27 顾客正常入口没有分享或复制

原店 `/`→门店→旗舰旗舰店→添加服务，选择 A27「QQ好友/群分享」，配置工程示例公开地址 `https://example.com/`，不绑定内容包，启用并保存；首页新增动作段、预检可发布、确认发布为线上第2版。原 `/s/sf_eeeffe9978ae3cf2` 点击动作进入 `/service/sa_ac1a567d21b117441e35d144`，显示「当前设备路线:纯链接分享」但仅称「社群信息暂未开通在线展示,请向店员索取」，没有分享或复制控件，无法交接用户所配置的链接。`runtime/a27-service-block-430.png`。这不是无系统分享目标的可恢复提示；尚未到达系统分享调用。`service-view.tsx:231` 将 share 与 join_group 合并并读取群号；`ActionPublishFlow` 只接内容页，服务页未使用其 link_share 路径。后端工程合同测试正例不证明此用户入口通过。

### P1/P2 待 root 裁决：统计内容版本筛选错认冻结作品

`server/internal/store/store_dashboard_ops.go:267-268,289-295` 对内容选择级按可变 `participations.content_pack_version` 筛，再统计任意非空 attempt；`participation/service.go:330-340` 允许预览期换当前版本、attempt 独立冻结，`dashboard/funnel.go:645` 又声称来源是 attempt 冻结版本。独立临时隔离测试仅准备一条 participation 当前 v2 和一条冻结 v1 attempt：筛 v1 的内容选择实际 0、筛 v2 实际 1，分别应为 1、0。临时测试未改原业务库、运行后删除。交接与权益各级也复用当前参与版本，应按实际关联事实审慎处理、不能关联时标不适用；本次精确动态红测只覆盖内容选择级。

### P1：受限门店统计读取其他门店活动库存

`server/internal/store/store_dashboard_ops.go:676-686` 以 `(i.store_id=? OR i.store_id='')` 包含所有无显式门店的批次，未核对关联活动的门店范围；同一条件用于 `:792-800` 未录成本批次。使用现有 `seedOpsWorld` 的独立工程库，活动归属门店 A，合法创建库存时批次 store_id 为空；限定门店 B 的 DashboardOps 实际返回 A 活动的「开业券」及总数 100（预期不应读取 A 专属活动）。库存行还提供发放/核销、面额与成本字段。临时固定事实测试红、运行后删除，未修改原业务库。真正可共享的批次应按原权限/活动归属裁决，不应单凭空门店字段对所有受限门店放开。

## 局部验证及证据限制

- 独立定向 Go：旧捕获 baseline 保留冻结内容、明确平台采用、版本化首页发布边界、旧会话候选、A27 无内容包服务端合同、受限方法拒绝、旧 handoff 原行/unknown 只查询、刷新重签仍沿原权威、30编号目录，均通过。旧 baseline 仅不执行无时间戳的版本化过滤；具体旧冻结ID、实时动作与媒体撤权路径已代码审阅，局部工程测试不等于全部历史数据已修复。
- Web adapter 六文件 41 项测试通过；官方文档核对抖音 H5 参数/签名和 share_id、小红书 verifyConfig、快手上传及 Web Share 的取消/无目标不易区分。工程向量和替身不证明真实平台回执。
- UI 完整目录确见 A01–A21「必需目录」及 A22–A30「建议补充」；保留标识不等于各动作已实际可用。M1–M13 和其他视图有既有映射与各组局部证据，但本轮未逐一追到28项业务功能，不能因此称28视图完成。
- 统计上一轮空组织、错误态、移动版、组织切换已在 `analytics-review.md` 留证；真实回焦撤权、费用与跨店固定事实全量复算及正式上游仍未证。
- 没有获批真实主体、平台账号、手机分享目标、正式回执与外部支付，不能宣称任何真实渠道正式发布/到账/收件；真 File、取消/未知等工程测试不等同真机。顾客已发权益不应由本审查改变。

补充：独立运行 `go -C server test ./internal/publishhandoff/adapters -run 'TestDouyinHTTPUsesOfficialIDSignatureAndCachesCredentials|TestXhsHTTPSignatureAndTokenCache|TestKuaishouHTTPPublishAndQueryAreDifferentFacts|TestKuaishouHTTPUploadsActualVideoAndCompletesFragments|TestKuaishouConcurrentSubmitCannotCreateTwoWorks|TestKuaishouAutomaticCaptionUsesAdmissionFieldSupportOnly' -count=1` 通过。受限门店批次即使被授权视为共享，原 `opsInventory` 仍对该批次全部 `reward_benefits` 聚合、不按 benefit 的 store_id 收窄，可能再跨店暴露发放/核销事实，已一并报 root；共享授权须依据原事实合同判断。

## A27 修复候选 bf0d57b 增量复验：仍阻断

独立树 FF `bf0d57b3f894fbdf22d2476b78878ce241baa857`，build 成功，保留原数据库、同门店线上第2版和原 A27 `sa_ac1a567d21b117441e35d144`，用先前浏览器保存的访客 cookie；从原 `/s` 点击同一动作。实际显示「原分享进度暂时读不到，请重试」，正常页面发起 `POST /api/public/actions/{action}/sessions` 返回 422 `invalid_state`，因此没有进入确认链接、复制或系统分享。原因 `LinkShareFlow.tsx` 传 source `web`，而 `participation/session.go` 只接受 nfc/qr/shorturl/manual 或空值，且在恢复 session 前验证来源。原 C2 `content-view.tsx:137` 同样显式传 web，共享默认也为 web；静态可证也会被会话服务拒绝，需同合同一起修复和沿原入口复验。不能以手造 session 掩盖。重试提示将此 422 称为读取失败，未解释动作请求无效。`runtime/a27-session-block-430.png`。

## a69a3a0 原 A27 与原 C2 增量

独立 FF `a69a3a0`、build 通过，原 customer 库、原首页第2版及原A27不重建：原/s→分享真实建立会话 `as_5c6a1ed7d4ca6fc2fed61d78f2d1852a`，确认公开测试URL后创建 `pha_70336c3e5a78beb58eb35bb8340c00fe`，无媒体。桌面不支持系统分享时提示复制；真实复制后读取剪贴板为原URL，刷新以同cookie恢复同session/handoff。截图 `runtime/a27-link-ready-430.png`。单列工程能力注入：clipboard 拒绝提示长按，navigator.share 抛 AbortError 提示取消或无目标并保留复制；无真实目标，且原handoff仍为 ready/evidence none，不称发出、发布、加群或发券。

另用原 store 审查库 `/tmp/touch-e2e-9ceKxD/touch.db` 与原media，未重发、未改准入。无继承 cookie 的新浏览器从 `/s/sf_f3431f9dcf10438d`→A01 `sa_84df977e105dd2c40262655f`，原首页第5版实际 session 201 `as_86ea663171602e2ef90379050345aa82`，显示已发布抖音第4版 `scpv_c005d40038e9d37ab6d077e66b0ee7bc`；刷新200恢复同session/版本。新 session 201 页面却称「已接上你上次的进度」，将 resume_available 错当已恢复，已报 root。

### P1：C2 未准入仍能获取并保存真实素材

原 A01 能力响应明确 h5_share 与 manual_material 的 store_promo 均 not_admitted，顾客页面也称「当前路线未放行」，但同时提供复制文案及保存视频。正常点击「保存视频」，真实 `POST /api/public/content/{原v4}/media-access` 的 download 签发200，浏览器 download 事件为 `tiny.mp4` 且 failure=null（`runtime/c2-denied-save-430.png`）。`content-view.tsx:261-279,406-412` 未以可用方法限制外发；需同时核服务端签发/兑换，不能只隐藏按钮。该反例与 A27 独立，不代表真平台发布或发奖。

## e2cfe3a 统计增量复验

独立固定事实库按原复现重新测试：当前版本v2、唯一冻结attempt v1 时，筛v1内容选择1、v2为0；B店不读A活动空门店批次，A店批次不计B店权益，受限可用量为未知。原两项红例及直接跨店权益派生在该隔离样本转绿；不把历史空库/错误/迟到浏览器结果冒充本SHA全量验收。

### P1：无活动门店交接在统计中消失

`publishhandoff/service2.go` 真实 v2 门店动作交接 INSERT 不写 campaign_id，合法为 NULL；`store_dashboard_ops.go` 发布层和事件层均 `JOIN campaigns c ON c.id=ph.campaign_id`，所以店根交接与其事件被排除，统计展示0。临时隔离固定事实构造一条本店、无活动的 handoff_ready 和一条 media_handoff：该店 `LayerHandoffRequested/LayerMediaHandoff` 实得0/0，期望1/1，红测后临时文件已删除。原测试 `rawHandoffV2` 给v2填了活动ID，未覆盖正常无活动形状。此前 A27 浏览器真实创建的是同类店根交接；尚未把浏览器记录作为统计页面端到端复验。已报 root 和统计修复组，需按 ph.store_id 与 tenant 裁决，并让活动筛选如实收窄/披露。

## 5787965 统计收尾复验

独立树 `5787965adbeef84f5e8840732983082af7e804f9`，production build 成功。原无活动交接隔离红测同样输入现转绿，requested/media=1/1；新增工程事实覆盖两门店、活动及内容版本筛选、跨租户事件和活动后移后保留原门店；版本归因与库存/benefit隔离定向测试同轮通过。服务端改用 `ph.store_id` 筛选冻结归属，事件按 handoff ID 和 tenant 同时关联，活动筛选显式限制原 campaign_id；这与所核边界相符。

原 customer 库真实 A27 `pha_70336c3e5a78beb58eb35bb8340c00fe`：工程身份从正常 `/`→经营结果，窗口包含原记录；总部视角和旗舰店+原动作筛选下「媒体交接发起」1，媒体交接事件0，复制未伪造素材交出事件；复验无活动门店筛选发起0。分项披露店与动作适用范围以及对某些来源不适用。390px 页面宽390、返回工作台 `/` 可达；截图 `runtime/stats-a27-390.png`、`runtime/stats-store2-390.png`。

尚有**真实页面表述缺陷**：同一没有媒体的 link_share、只准备但未调用系统分享的 A27，在「媒体交接完成(交接就绪已达)」显示1。ready 是准备就绪，不能将无媒体/未交出显示成媒体交接完成；虽括号解释其为就绪，仍会误导经营结果。已报 root，建议按真实事实明确命名准备态，区分媒体与链接，不升级事实或隐藏原记录。

空组织工程账号从正常登录选择连锁二号店→经营结果，430px 无 pageerror 或横向溢出，已读活动/权益零与未接入及未知费用区分，截图 `runtime/stats-empty-578-430.png`。单一空组织不证明所有缺字段、上游异常和权限组合；此前其他候选的故障/迟到/撤权截图不得自动移作本SHA复验。

HUI-4665 A 段：原始空库客户端异常在本实例未复现，空组织、本地零与未知局部成立，但只有门店无活动、全部失败/无权、真实焦点撤权等本候选仍缺证。B 段：本次已验证无活动交接入数、作用域与筛选披露，原媒体交接“完成”命名仍需修；固定事实覆盖若干版本/跨店/重复，但未对所有更正、迟到、撤回和实际费用进行 API/页面/独立复算全覆盖。外部平台、支付未授权保持未知是正确边界，并非因为未知而判失败。

## 1bc73b2 原 C2 导出与 A27/统计复验

独立树 `1bc73b292b75e6fcd7d332cca6cb861d388c1a2d`，production build 成功。原 store 库 `/tmp/touch-e2e-9ceKxD/touch.db`/media、原访客cookie，从 `/s/sf_f3431f9dcf10438d`→A01，恢复原 session `as_86ea663171602e2ef90379050345aa82`、首页第5版及 v4 `scpv_c005d40038e9d37ab6d077e66b0ee7bc`。页面未出现旧复制文案/保存视频按钮，仍显示当前路线未放行；预览视频真实 readyState=4，无媒体错误（`runtime/c2-export-gate-1bc-430.png`）。基于同一原会话与原媒体的对抗性非 UI 请求，download 与 platform_fetch 均422 `media_handoff_required`。预览字节仍供播放，不声称防止提取预览。原库只有 A01，无已准入 manual 动作；未造准入或新动作，真实正常浏览器 manual 正例未证。

独立重跑 HTTP/contentpack 定向测试，覆盖未准入原动作、合法 manual 原handoff与真实bytes、错误用途、旧无来源非预览票据410、原签名来源隔离、撤准入/动作及内容撤权后新签/缓存兑换、无adapter历史 GET保留而 plan/execution拒绝并清两处执行载荷，均通过。另用隔离工程fixture实际创建合法manual handoff、取消后验证原session新签及缓存兑换均409；临时测试删除。真实平台获批主体、真实设备/官方回执仍无证据；工程fixture不代表正式许可。

原 customer 库 `/tmp/touch-e2e-ZLFKmv/touch.db`、原 A27 和原visitor cookie恢复同 session `as_5c6a1ed7d4ca6fc2fed61d78f2d1852a`、同 handoff `pha_70336c3e5a78beb58eb35bb8340c00fe`，复制实际取得原URL，不称发送（`runtime/a27-exportfix-1bc-430.png`）。正常工作台→经营结果，原窗口显示「交接准备与操作」：交接发起（媒体或链接）1、交接准备就绪1、取材或分享操作记录0（`runtime/stats-copy-1bc-390.png`），此前将链接就绪称媒体交接完成的表述已消除；复制未计交出事件。此为单记录事实和工程环境，不能升级为分享送达或平台发布。

## 79367fd 手动交接增量复验（阻断待修）

独立 `79367fd97bde5e5a26783f145de81494e8212cb1` build 通过；Go 手动投影/准入定向及前端 next-action/execution 12项通过。原 store 库同visitor原 A01 session、原首页5/v4 复验预览可播放、无导出按钮，download/platform_fetch 仍422；原 customer 库原 A27 同session/handoff复制原URL，正常工作台统计交接发起1、准备1、操作0。

原 store 库商家从 UI 为 A04 配置原内容包并预检，原 v5 虽已采用 wechat，但实际为图文而非 A04 视频，预检拒绝是正确类型校验。按 root 允许，为相同原包已采用 wechat v5 正常配置 A05 朋友圈图文，不改准入；保存、预检通过后发布首页7（含原A01，线上原v4此前已复验），新增 A05 动作 `sa_160f008d90fa1e16b4aa4889`。原/s进入A05新会话 `as_5edb796267f72a9eb875ee9401ae2352`，看到原微信v5，但确认后 POST handoff 422 `payload_mismatch`，没有 handoff（截图 `runtime/a05-payload-mismatch-793-430.png`）。实际 plan 选择 allowed 的 `link_share` 而 content-view start 固定 payload_kind=media，服务端正确拒绝；前端 `content-view.tsx` 的通用 `pickRoute` 未按所需媒体输入挑选已准入 manual_material；plan GET 对错配却先报 allowed，造成用户误导。已报 root 修复后沿原会话和同一新动作复验。A04 未通过，A05 未成功交出真实文件；工程 HTTP 投影测试通过不能替代正常浏览器 File 正例。

`publish-next-action` 单测在当前refused下对 unknown/submitting/awaiting_review 都选原 query、不选submit；后端 query沿原ID查询。该结论为工程测试与静态路径，不声称真实第三方结果。无正式平台主体、真实手机目标或回执，均未证。

## 7393e19 A05 原记录复验

独立树 `7393e19743ce9e750b70985fd314e93733820303`，production build 与相关 HTTP/domain 定向测试通过。原 store 库原首页7、A05动作 `sa_160f008d90fa1e16b4aa4889`、原session `as_5edb796267f72a9eb875ee9401ae2352`、已采用 wechat v5 不变；正常/s入口刷新原进度后选择已准入 `manual_material`，确认后创建原交接 `pha_2cae3a8c7f79244b9311779dc29d2cbd`，媒体为1项，client_action.params 含 media_order/media_00。此前79367fd因本机 PUBLIC_BASE_URL 为 HTTP，前端严格HTTPS检查使素材未预取；为验证真实生产形状，在同一隔离环境配置自签本地TLS终止、同源访问，不放松产品HTTPS、未改原数据或准入。重启后同一 handoff 恢复200，前端实际读取投影文件并请求原 image/png 200、90字节，显示逐个保存。

点击「保存图片1」产生浏览器下载 `01-image.png`、failure=null，独立 SHA256 与原 v5 引用的原 pixel.png 完全一致；页面仅称「已请求保存，请在手机中核对文件。保存不会确认发布或领取奖励」（`runtime/a05-manual-739-430.png`）。刷新再次恢复相同handoff，文件可准备；对抗 GET plan 将 link_share 与 media 混配时422 `payload_mismatch`，不借用另一方式的资格。原 A01 同会话/原v4 在首页7仍可预览且当前路线未放行，不可将 A05 结论冒充 A01/A04。原 A04 视频绑定图文v5预检拒绝为正确输入校验，未做 A04 视频正例。

该结果是原DB、原素材、正常商家和顾客入口的工程浏览器正例；TLS为本机自签工程代理，能力记录为本地默认准入，未验证微信真机目标、相册最终落地、正式主体资格、官方发布或奖励。此前79367fd原 A27/session恢复复制、统计发起1/就绪1/操作0已单列；未知/在途查询优先仅有工程/代码证据，不声称真实平台结果。
