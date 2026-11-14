# store 实施报告：HUI-2628 / HUI-4655

## 交付边界与版本

- 基线：`9dd04a7467020602bae130186d4ed738404fb65d`。
- 工作树：`/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/store`。
- 分支：`codex/touch-2628-4655-store`。
- 工程主提交：`08df49112d0c64def39c169b2b8ff758cc96f8f0`。
- 主提交测试通过后，由 root 集成 content，再应用素材 census 的单独补丁 `485924dfb27ee9fd04998ffae4b7cec1f86cec6d`（仅两处 /work/materials markers=[]）。
- 本报告不把工程替身、编译、旧票 Done 视为实际平台、NFC 或正式同版验收；未改 Linear 状态。

## 实际行为

1. 新增 `/work/stores/[id]` 门店工作区；“顾客页与服务 / 投放入口”是同一宿主。服务目录、Wi-Fi/微信/社群、内容采用、真实预览发布是就地组件，不是跨首页的链接集合。已打开面板保持挂载，切页签保留未保存修改；租户或门店更换时以 key 清理旧组件和迟到请求可见面。
2. 只有一家有权门店时 `/work/stores` 自动进入本店；`?manage=1` 明确打开门店管理/切店。多店继续沿服务端有权门店列表选择，不用 URL 授权。
3. 原 channels/services/page 深链保留原面，并提供正确同店工作区返回；原公开、店员、平台运营、代理等路由零删除。page-map/census 新增一个实际 workspace 路由，原 33 条账本变 34 条；业务视图没有因为合并宿主减少。
4. 完整 A01–A30 服务目录可搜索，标识必需目录/建议补充，允许同类型多实例；未选择项改称可添加服务，不计为必须清零的欠项。A17/A18 进入原连接服务字段编辑器，避免在通用 URL 表单伪装 Wi-Fi 配置。已采用内容包保留原引用，缺失/失败不强制清空。
5. 内容插槽沿 content 组统一编辑器兼容导出：storeId/storeName、purpose=`门店顾客页`、returnHref 到本店服务面、onAdopted 刷新绑定列表/标记需要更新预览并切回原用途。内容采用不自动绑定/发布。content 组已书面确认 props/purpose 相容；本工作树未复制其生产组件。
6. 保存服务候选后，顾客页会提示纳入新草稿；“保存草稿并预检”真实保存版本、读取真实预览、运行服务端预检。确认变更和指纹闸继续保留，受限项目不静默剔除；工作区可就地切回服务配置修复。发布成功依据服务端发布结果，不设本地假成功。
7. 投放面自动带入并固定本店筛选/创建门店；沿原二维码/CSV/单卡停用/换绑/实体交付读写。跨店批量管理仍从“管理与更多”可找到；下载、预览不写实体成功状态。
8. 冲突仍由服务端 expected_revision 拒绝，配置面保留冲突前副本，显式刷新后可恢复自己的修改再核对保存。首页草稿同样保留冲突栏目副本。草稿行更新和发布指针增加 CAS，避免读后写覆盖。
9. 共享导航一致采用 今天 / 门店 / 推广活动 / 内容中心 / 经营结果；服务台按现有登记制功能和核销角色出现。线索、权益与团购、跨店入口、账单和团队/代理协作收纳为可展开的“管理与更多”。
10. Today 消费真实 `/api/dashboard/operations-summary`，展示真实已发/核销和受权的当前待确认/失败订单。暂停/结束/过期活动、停用动作、已发未核销库存、空内容引用、未知摘要不伪装成可办待办。失败明示未知并可重试，部分缺字段保持未知；不再要求先建活动或记矩阵草稿。每项有状态/影响/主操作及返回今天的深链。

## 核现代码后补齐的权威缺口

### 连接服务原来保存即对客生效

实查 `store_services` 与 `ListPublicStoreServices` 原先脱离 storefront 发布版本，Wi-Fi 第二版保存会直接影响第一版顾客。经 root 明确扩展写面授权后：

- 迁移 `0132_storefront_service_snapshots.sql`（root 分配、唯一持有）增加 `store_service_configs.public_baseline` 与 `storefront_versions.services/services_revision`，不新建第二套发布权威。
- 新版/旧服务页均发送 `publication_mode: draft`。首次 staged CRUD 在同一服务事务中冻结原对客 baseline，保存候选不改原线上服务；迁移不回填假数据，既有门店在首次编辑前仍保持原实时服务效果。
- `SaveStorefrontDraft` 自动捕获本店当前服务快照/配置 revision，不让客户端手输服务 ID 引用。
- 服务差异参与发布确认指纹，发布事务内再次校验服务 revision；预检之后别的会话改服务，发布 409，不动旧指针。
- 公开服务读当前发布快照；旧发布行缺快照时读首轮 baseline；尚未进入新版发布管理的旧 API 调用沿原兼容规则。预览与公开用同一白名单投影，支持没有活动、没有动作段的纯连接服务上线。
- 门店停用/租户生命周期沿原公开 gate 立即拒绝或清空；连接服务无独立 revoked/expiry 字段，候选 enabled 属于待发布内容，不混作即时撤权。动作域实时否决继续独立生效。

### 动作可编辑目标原来会渗入已发布服务

实查发布 ActionSnapshot 原先不包含目标；C3/纯链接交接读取当前 `store_actions.TargetRef`。经 root 再次确认本票范围后：

- 沿已有 actions JSON 冻结 target_ref/content_binding_ref，无额外迁移。C3 与纯链接 share 从已发布动作目标读取。
- 旧发布行缺目标时，从已有 `store_action_snapshots` 按同一 action_id + action_revision 恢复；不能确定时不猜选当前候选目标。新版本直接带快照，不扫描历史。
- 当前 enabled/revoked/expiry 仍实时否决。候选目标换地址不改变旧版地址；新版本发布才切换。公开服务仅列当前版本引用的动作。
- 同时修复旧 `draftFingerprint` 只基于粗粒度变更摘要导致同 ID 改目标不产生新稿的问题，改用完整候选文档指纹。

## 文件写面

- 新宿主/上下文：`web/src/components/work/store-workspace.tsx`、`store-workspace-context.tsx`、`web/src/app/work/stores/[id]/page.tsx`、`web/src/lib/work/store-workspace.ts`。
- 接线：stores 列表、channels/services 原页、storefront-page-desk、touchpoints-panel、work layout。
- 共享壳/Today：work-frame、today-desk、product-finish、today-obligations；page-map/page-census。
- 发布权威：store_services、store_storefront_versions、storefrontversion；对应 services/storefront/public handlers；仅纯链接 handler 的 target resolver 做发布读取窄改。
- 测试：新增 store-workspace / today-obligations 纯逻辑测试及真实 HTTP `server_storefront_services_publish_test.go`，更新与新版导航/路由/Today 行为冲突的共享断言。
- 未改 content 生产组件、campaign-config-panel、顾客 C4、analytics、商业、店员业务页面。

## 工程验证

- `npm test`：111 test files / 1137 tests 全通过。最终日志 `store-vitest.log`；新冲突/内容返回调整后相关 36 tests 再通过。
- `npm run build`：Next.js 15.5.25 生产构建成功，包含新 workspace 路由；日志 `store-build.log`。没有共享 `.next`。
- `tsc --noEmit --incremental false`：无本组源码新增错误；仍有原测试的 `contrast-gate.test.ts` unused ts-expect-error 1处及 `customer-journey-api.test.ts` ParticipationResult 联合类型读取6处。完整输出 `store-tsc.log`，未越写面修改这些测试。
- Go 环境：`PATH=/workspace/scratch/b0e2059d4a63/runtime/golang/go/bin:$PATH GOCACHE=/workspace/scratch/b0e2059d4a63/runtime/go-build-cache`。
- 服务快照轮完整 `go -C server test ./internal/storefrontversion ./internal/store ./internal/httpapi ./internal/db` 全通过，日志 `store-go.log`（store 184.109s / httpapi 252.070s）。
- 后续目标冻结增量：`go test ./internal/httpapi -run 'TestWorkspace|TestStorefront|TestAction.*Handoff|Test.*LinkShare' -count=1 -timeout=60s` 通过；storefrontversion/store 发布/草稿/回滚相关匹配测试通过。
- 最终工程提交四包完整回归全部通过：storefrontversion 0.002s、store 62.814s、httpapi 99.964s、db cached；完整日志 `store-go-final.log`。
- `git diff --check` 通过。

HTTP 新覆盖：旧服务首次编辑保护；纯 Wi-Fi 多实例无活动上线；草稿预览/公开一致；第二版候选和发布失败保护第一版；陈旧服务 revision 409；跨租户写 404；停店后冻结服务不泄漏；点评目标第二版不发布仍旧链接、预览新目标、发布后切换；撤权即时否决。撤权是工程 fixture 直接设置原权威字段，未将其冒充正常浏览器/真实平台操作。

## 正常浏览器入口与数据准备（交 root 独立同版验收）

1. 用受权 owner 正常登录根页；Identity/Billing/Task 替身只作工程联调，账号/配置沿 root E2E 环境。普通单店点“门店”自动到本店；多店先从有权列表选择。
2. 新店无活动：顾客页与服务 → Wi-Fi/微信/社群 → 添加合法 Wi-Fi 并保存 → 预览与发布 → 保存草稿并预检 → 确认真实变更 → 发布到线上。再从公共门店服务入口核对同一 SSID；编辑候选新 SSID 不发布时另一浏览器仍为旧 SSID。
3. 添加服务 → 搜索“点评” → 原目录 A08 填合法平台目标并保存 → 预览面选择真实动作入栏目并保存/预检/发布。第二版改目标不发布，公共 C3 仍旧地址；发布后新地址。即时停用/授权撤销/expiry仍单独验证。
4. 从内容面采用本店真实内容，返回服务绑定下拉查新包；采用不自动上线。未保存栏目在切内容/服务/入口再返回后仍保留。
5. 投放入口自动本店，下载/预览/CSV 与实体写卡、回读、真机通过分别看记录；一卡停用不影响另一张及既有参与/权益。
6. 两会话同店修改：旧 revision 保存失败，原草稿保留；刷新最新后可显式恢复本人的修改再核对。撤权后提交由原服务端权限拒绝。
7. 根页摘要正常零/真实待确认/摘要失败/缺字段分别验证；正常暂停结束不计待办。有权者主操作到服务台并可由共享“今天”返回。
8. 390px/430px/桌面检查工作区按钮、搜索、长 URL、表单、焦点、错误重试；切组织时挂起旧请求再释放，不能露出旧店。

本组未独立执行浏览器。root 已在准备正常入口环境与独立 terra 审查，本报告不给截图或外部环境虚假签收。

## 功能可达覆盖

原 pageLedger 33 条全部保留；新增 workspace 后 34 条。30 目录仍由原 actions.catalog 提供，搜索只筛显示不删除能力，A22–A30 保留建议补充语义。

- M1 根页；M2 新同店工作区（列表保留）；M3/M4 同店顾客页与服务及旧 /page、/channels；M5 内容中心及就地统一编辑器；M6/M7 原活动列表/新建/编辑；M8 同店入口及跨店原触点台；M9 原权益/团购；M10 经营结果；M11 线索；M12 账单；M13 团队权限/代理协作。
- C1–C6：原 /s 门店首页、content、service、/c 活动、benefits、privacy 零删改路由；S 店员原 /work/service；O 运营六路由；A 代理既有 /agency 及团队授权/品牌入口继续由提供者挂载。业务视图复用及路由不等同，每条原入口和权限仍以既有权威执行。

## 三栏交付结论

| 工程验证 | 跨模块实际集成 | 外部渠道与正式交付 |
|---|---|---|
| 已实现并本地提交；前端完整测试/构建通过，真实Go HTTP替身集成与历史发布回归通过，最终完整Go四包回归亦全部通过 | content合同已确认，root合并后需要真实内容采用→绑定→发布、商家→顾客→店员、完整返回/切组织走查；未冒充跨组签收 | 真 Identity/平台发布授权、支付/制作、外部券、手机与实体NFC均无本组新证据，保持未验证；不标Done |

## 最终校验追加

工程主提交08df491的Go四包完整suite全绿；后续485924d仅是content薄壳的census联调补丁，故单独store worktree旧素材源码与其[]标记暂不一致，须按明确依赖在content之后集成；不宣称这个依赖补丁在旧素材源码上单独全绿。最终工作树无未提交源码。

## 修复轮 1：公开参与、内容与媒体统一发布边界（terra 首轮阻断）

### 固定版本与提交

- 按 root 指令改用 `.worktrees/store-fix` / `codex/touch-store-publication-fix`，基线 `d22da8e`；原 store 工作树未改。
- 已读 `reviews/store-content-review.md` 并逐项处理真实调用路径。
- 本地修复提交：`a0fbe1fa9538d38dbd9ae0834dc3216362474140`，root 已集成为 `5e6d563`。
- 迁移：独占 `0135_action_publication_content.sql`，不冲突 campaign 的 0134。
- 未将本轮工程结果记为独立浏览器复验通过；原 terra 需在同版继续原入口复验，不标 Done。

### 权威字段与来源

| 权威结果 | 实际来源与写入时点 |
|---|---|
| 发布动作目标/内容绑定 | 已有 `storefront_versions.actions` JSON 的 ActionSnapshot；SaveStorefrontDraft 从本店 action 实例和原 binding 权威冻结；不从顾客请求猜映射 |
| 具体内容版本 | ActionSnapshot 新增 `content_version_id`；商家保存草稿时由该动作绑定包的真实 adoption 指针解析具体不可变 version id |
| 合法换选池 | ActionSnapshot 新增 `content_candidates`；同次保存把本店、适用平台的真实已采用版本冻结成精确 id 集合；公开读取不再扫描当前 adoption 池追加新稿 |
| 会话选择范围 | `action_sessions.content_candidates`（0135新增）；开会话时从实际发布目录复制，Start/Update 都只允许默认版本或此冻结集合；空/缺池绝不等于任意版本可选 |
| 会话历史选择证明 | 原 `action_session_events` 中 `created/content_changed` 的具体 version 与 occurred_at；迁移前无池的会话，只保留其已记录选择，且验证同租户同店及选择发生前真实 adoption 事件 |
| 首发前与旧系统兼容 | `store_action_configs.public_baseline`（0135新增）；NULL沿既有 legacy 兼容，进入正常后台 staged 写入时，同一 action CRUD 事务首次冻结此前实际公开动作。新店冻结 [] 后首发前无公开候选；老店冻结旧目标/内容/候选，编辑新稿不改旧服务 |
| 实时否决 | 原 action enabled/revoked/expires_at、store status、tenant lifecycle；具体 content version 的 blocked/grant_until；快照与会话不能覆盖这些真实否决 |

新采用是否退休旧版本一项已复核并纠正：`contentpack.Distributable` 实际只拒 blocked 和授权过期，retired 本身可继续分发。本轮**保留**原 adoption 的 retire/superseded 行为，未通过删状态或放宽 Distributable 掩盖问题。正常 HTTP 已证明旧版本退休后仍可供合法旧会话和已签抓取票据使用。

### 实际修复

1. `ResolveAction` 改读发布动作投影和具体冻结内容版本；不再从 live action + 当前 binding/adoption 组装新顾客的参与快照。目录的内容不可分发时实时拒绝新会话，既有事实不被改写。
2. `/public/actions/{actionId}/content` 默认读当前发布版本；已有会话以唯一参数 `action_session_id` 携带，验证本人 visitor cookie 后读其冻结正文、精确选择和候选池。参数没有第二个别名。
3. 公共候选池只从该发布/该会话的精确版本集合读真实元数据，逐项再次校验分发状态，不把后来采用的 v2 塞入 v1 顾客页。
4. 会话 Start 和 Update 在写入前检查冻结白名单，HTTP 面另校验具体版本同店与 block/grant；不能先任意写 session.ContentPackVersion 再凭它签媒体。
5. 匿名 `/public/content/{version}/media-access` 只可签实际发布版本；有会话时须本人 cookie、有效会话、精确冻结版本归属和实时动作/内容否决。删除“当前动作绑定同一个 pack 就允许其任意 version”的旁路。
6. 新增原 store content 域的受权媒体路径：`POST /api/v1/stores/{id}/content-packs/{packId}/versions/{versionId}/media-access`，复用 requireSession 和本店 storeContentPackRead，校验 pack/version 真属本店。候选预览由此办理；drawer 只改 openMedia 路径并补现有 DTO 的 pack_id 类型，未抢 root 内容表单重构写面。
7. 已签媒体的兑换仍按原签名/TTL/同 version+media/实时 block+grant 校验，不要求平台抓取方持访客 cookie；换采用或退休不烧毁合法旧票据，明确 block 后旧票据也立即拒绝。
8. 发布/预检/回滚检查冻结的具体内容版本，不再以另一个当前采用版本的健康状态替旧快照背书。
9. 纯链接交接也补齐旧会话目标：按该会话的 storefront/action revision 读取其原发布目标，当前动作启停/撤权/expiry 仍即时否决；不因发布 v2 把 v1 会话的 share_url 改为 v2。
10. 旧发布 JSON 缺内容字段时，只从对应历史版本边界前的可核验 adoption 事件恢复具体版本；不猜当前指针。不能证明的候选不授予新分发权限。原 unmanaged legacy 记录仍维持原兼容入口与本店候选语义。
11. 首稿空态不展示空门店码或 `/s/{门店码}` 技术占位；显示尚未发布及实际下一步，已发布才给真实“查看顾客页”链接。保存草稿按钮允许主动纳入最新已采用内容，避免先去内容面、后首次挂载预览时按钮被 dirty 状态误禁用。

### 回归与证据

新增真实 HTTP 用例 `TestWorkspacePublicationContentBoundary`，先红：首发前 GET content 返回 200（原缺陷）；修复后完整绿：

- 新店 staged 动作首发前 content/session/匿名媒体均 404；有权商家仍可预览候选，匿名受权路由 401。
- 发布 v1 后会话与公开正文为 v1；创建并采用同包 v2、修改目标但不发布，公开正文/新会话仍 v1，候选不含 v2。
- 同 pack 的尚未采用 candidate 与已采用未发布 v2，匿名不能签媒体；旧会话不能选择或签发它们。
- v2 发布后新会话为 v2，旧本人会话携 action_session_id 仍读 v1、签 v1 媒体。
- 已签旧媒体在采用/退休/换版后仍真实可取回，block 后重签和旧票据兑换都 409。
- 另有 `TestLegacySessionWithoutPoolAuthorizesOnlyRecordedAdoptedVersion`：明确模拟迁移前 NULL 池，合法旧选择保留，同包后来版本仍 409。NULL 没有成为绕过白名单的例外。
- participation 新增冻结池注入回归：新会话不能指定未登记版本；目录后续变更不能扩大旧会话候选；原冻结候选仍可选。

已有 public-content 测试以前仅写任意字符串 binding 而不创建实际动作，已改用原动作 CRUD 生成真实动作；handoff fixture 旧伪造 storefront/action revision 改为其真实原域数据。候选制作全环的预览改走受权原域。未给生产代码增加测试绕行逻辑。

最终验证：

- 五包完整：`go -C server test ./internal/httpapi ./internal/participation ./internal/store ./internal/storefrontversion ./internal/db` 全通过（日志 `store-fix-go-final.log`：httpapi 96.717s / store 63.034s，其余 cached；此前同轮 participation 14.104s / db 7.593s 亦通过）。
- 随后纯链接旧会话窄增量的最终定向：`TestActionHandoffV2|TestWorkspacePublicationContentBoundary|TestLegacySessionWithoutPool` 全通过（2.144s），覆盖原8项V2 handoff及本轮正常公开链路。
- 前端完整：113 files / 1156 tests 全通过，`store-fix-web.log`；受权媒体/首页窄改相关36 tests另通过。
- `tsc --noEmit --incremental false` 全通过，`store-fix-tsc.log` 空输出；此前根测试基线已由root修复，本轮无新类型错误。
- Next 生产构建通过，日志 `store-fix-build.log`。未共享 `.next`。
- `git diff --check` 通过，工作树无未提交源码。
- `db_test.go` 同步了此前0133已经存在的 content_production_offers schema表目录；root集成时已处理重复登记，不涉及新增业务表或制造业务成功。

### 下一轮同路径复验交接

root/customer 已收到唯一 GET 参数和 frozen-session 合同；customer 在本提交后负责把 C2 读取接为先恢复本人会话，再带 `action_session_id` 取正文，以及原只读恢复顺序窄修。我未改 C2 UI 容器和 StartActionSession 首段恢复顺序，避免与该组抢写。

root 可据同一店、同一 action、同一 content pack 两个 version，从原商家登录→门店→内容采用→保存预检/确认发布→顾客 C2/新旧会话/媒体的路径复验；同时检查首发前不可公开、v2仅采用不串线、换版旧会话保留、明确block即时否决、390/430px/桌面和本人/他人cookie。此处工程测试使用 Identity/Task/Billing替身，不是外部平台/手机/NFC正式交付证据。
