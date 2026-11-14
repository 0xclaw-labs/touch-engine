# HUI-4663/4664 商业与运营独立走查（进行中）

候选 `e6ddf424f7367cc1b1e4af2607d9636771e1b47f`；worktree `review-commercial`；`npm run build` 成功。Go/Next/Identity/Billing/Task 工程替身与官方 Chrome Headless Shell 同一隔离会话 34325；正常 `/` 登录，原数据库 `/tmp/touch-e2e-9uQJ1V/touch.db`，组织 `tnt_ab3320a42147de87b14316baf47cae73`、门店 `sto_087ca1942ab8706424eaba9c479d6cda`。原始工程 fixture 仅预置账号、组织、门店及其他非本次待验数据，成员、代理及其授权操作均经产品 UI。会话与数据库保留；runner `runtime/commercial-review.mjs`。未更改产品代码、未 push、未改 Linear。

## 发现及待复验

- 无活动门店投放无法建立正常入口。商家投放提示先在「出库与写卡」登记卡；经受信工程 ops_grant_manager 授予 Identity 既存主体 `usr_e2e_owner` 同组织最小 `ops:operations.read`（`ops_f1b9ee7b3838988c4335f1ceec512da0`，生产者 `ops-fixture`，受信独立 token，授权有审计），正常登录 `/ops/operations` 后仅见库存及硬件订单只读投影，没有登记/出库/写卡操作。`runtime/commercial-ops-readonly.png`。root 核实旧 `/admin` tag 表单强制活动+链接，而新触点 UI/API 依赖旧 tag；原 alias 权威支持门店根。已报 root，修复后须同一原门店与原数据重验 `/t`→`/s`，不可创建空活动或改库绕过。grant 不是正式平台授权；无真实卡、支付或真机写卡证据。
- 受控运营核验使用 SQLite 正规 backup 保存原记录至 `/tmp/commercial-preserve/touch.db`，无媒体目录（当时无上传媒体），另起会话 84888、端口19760/19762，重启脚本 `runtime/commercial-ops-restart.mjs`；grant 后发生的写仅限受信 grant，自此副本与原库分叉。此会话重新正常登录，未继承 cookie。

## 已观察

1. owner 从 `/`→管理与更多→团队、权限与代理协作，以完整邮箱核对并选择 `member-commercial@e2e.test`，选择本店、首页配置；确认框列组织、门店、店员任务、长期可撤回及不授付款。确认后同一行显示本店与现行首页配置。部分邮箱禁用核对按钮。成员独立正常登录可进入该店首页。owner 在同一行撤回首页任务，确认显示撤回全部配置任务，保存后行显示未获配置权限。成员已打开页面尝试保存时，本地空栏目校验阻止请求，故**尚未证明撤权后下一写请求被服务端拒绝**；越店及停用也未验。
2. owner 选 `agent-commercial@e2e.test` 建代管，预览初始只读/开子账号、无付款，随后授权首页配置+续费协助，显示无独立付款授权。代理独立登录后横幅显示商家、任务及交负责人付款；返回代理列表后进入工作台，账单中持续同一商家与无付款权，显示无生效目录、无收费通道及负责人处理。owner 撤销同一代理关系后，代理返回列表显示无授权商家。尚未检验已打开页面撤权后实际写入、过期、延迟响应或另一商家切换。
3. 390px 成员页、430px 账单及1440px 页面记录的 `scrollWidth` 均等于 viewport；截图分别 `runtime/commercial-settings-390.png`、`runtime/commercial-billing-430.png`。这仅验证当时状态，不代表所有弹框和错误状态均覆盖。
4. 后续 owner UI 为同一门店保存启用 Wi-Fi，用于准备撤权写入测试；首页空栏目本地校验仍挡住写请求，故未记为通过。此操作在原库，晚于上述 ops 备份；如需复验应选择并明确正确数据库。

## 尚未完成 / 三层边界

- 工程浏览器：应继续同一成员授权与撤权前后真正写入及越店、选人结果过期、代理失效后在途请求；目录、原单与账单加载失败、同一 key 双击/丢响应重试、变价拒绝等均未完成。无目录/收费通道条件下未声称报价、下单或付款通过。
- 工程跨模块：仅接本地 Identity 与 Billing/Task seams，不能证明正式 Identity、收银或服务开通。现有实现报告中的 Go 阶梯报价测试不是本次独立浏览器证据。
- 外部交付：真实支付、服务生效、实体卡、写卡回读、真机 NFC、发货签收及现场验收均未验证。不得标两票 Done。

补充负例：该成员撤回首页配置后，修改同一店 owner 所建 Wi-Fi 并保存，服务端拒绝，UI 提示「你只能配置自己管辖的门店」。该员工范围就是此店，且从未授动作配置；拒绝本身符合权限，提示将缺任务权限误说成越店，已报 root。不能把它算作首页权限撤回即时失效的正反证据。代理关系撤销后再次打开此前账单地址，显示「当前账号没有可进入的组织」。

## 同记录后续权限复验

将原数据库 SQLite backup 到 `/tmp/commercial-followup/touch.db`，重启 e6ddf42（会话44096，19860/19862），正常重新登录，cookie 未迁移。owner UI 给同店配置启用 A08 测试地址，再重新授原 member 首页任务。member 独立登录在同店用基础模板将 A08 加入首页并保存草稿第1版，预检可发布。owner 同行撤销首页任务后，member 保持原打开页面、修改该草稿段落并保存：PUT `/api/stores/sto_087ca1942ab8706424eaba9c479d6cda/storefront-draft` 返回 403 `forbidden`、`store configuration requires an explicit config permission`；页面仍为草稿第1版并留未保存修改，证明此同一请求路径即时撤权。旧版提示含重新登录，修订 SHA 0457c4e 后待原记录复验文案。

同一会话补抓 Wi-Fi 保存 PUT `/api/stores/sto_087ca1942ab8706424eaba9c479d6cda/services` 的 403 `forbidden`、`store configuration requires an explicit config permission`，原 UI 却称只能配置管辖门店。root 已修复集成 0457c4e，尚待同数据浏览器复验。

受信 grant 给原 owner 添加 `ops:plans.read` / `ops:plans.manage`（`ops_b70c958f9fda34171f6a3808ec2724b7`），浏览器进入 `/ops/plans`；初始化捕到两次 400 tenant_required，随后显示空目录。runner 仅记录非2xx，不能排除后续成功读取，不判定空态缺陷。页面仅可发布/下架**已有**公共价格行，不可新增金额/条款。当前无行，不能正常 UI 创建阶梯目录；未擅自调用价格生产者接口或改库。报价、目录变价和下单仍未验。`runtime/commercial-followup*.mjs` 保留。

## 47421ea 商业增量独立复验

审查树原 `e6ddf42` 干净后快进 `47421ea352f70d362551f2c76b40e0e6c86df4a2`，独立 Next build 成功；使用 `/tmp/commercial-followup/touch.db` 原组织 `tnt_ab3320a42147de87b14316baf47cae73`、原店 `sto_087ca1942ab8706424eaba9c479d6cda`。Go/Next/Identity/seams/官方 Chrome 同一执行生命周期；原 cookie 未继承，owner/member/agent 各自正常登录，非真实 Identity。

- 原已撤销首页任务 member，在原店模板修改并保存，PUT 403 `store configuration requires an explicit config permission`，界面提示账号无权限或原授权收回、请负责人核对；草稿第1版保留未保存修改，未再建议重新登录。截图 `runtime/commercial-474-member403.png`。同 member 改原 Wi-Fi 服务并保存同样 403，提示核对配置任务授权和门店范围，不再断言越店。撤销后的 agent 登录并打开原账单仅显示当前账号没有可进入组织，无旧商家事实。
- owner 在原无活动门店投放创建「前台复验入口」`tp_bf80e4754977752c6dad277d646b41fb`、code `AC3R33V68AAK`，无须输入内部 ID，`PUBLIC_BASE_URL` 为本地 Next。刷新后「恢复／查询本次入口」仍同一链接；下载二维码和 CSV 后只读 DB 确认 deployment_stage 仍 registered、交付记录0；访问 `/t/AC3R33V68AAK` 跳原 `/s/sf_52aea6b707bd26a5`。原顾客页尚未发布，因此不声称服务已上线。**待修**：列表把 `sf_52aea6b707bd26a5` 冗余显示在店名旁，已告 root 修展示；URL 本身保留。
- 经明确注册的工程 `billing-auth:billing_authority` 原接口发布 tenant 工程目录 v1（9900 CNY），owner 界面显示实际价格版本；再经同接口发布 v2（10500 CNY），刷新显示 v2。目录只是测试数据，不是生产定价。缺 cashier 时 UI 诚实提示收费通道不可用，但把续费/扩店和无支付报价入口也一并隐藏，无法通过 UI 检查真实服务端报价；已交 root 修。浏览器注入目录 GET 503 时同时显示「读不到」和无依据的「暂无生效行」；移除拦截后刷新恢复 v2，错误空态也交 root 修。截图 `runtime/commercial-474-catalog503.png`、`runtime/commercial-474-catalog-v2.png`。未伪造收费通道或付款。
- 独立运行 `go test ./internal/store ./internal/httpapi -run 'TestBillingOrderRejectsChangedIntent|TestHardwareOrderAtomicAndItemIntent|TestSubscriptionOriginalOrderRecoveryAfterCatalogChangeHTTP|TestHardwareSameTotalDifferentItemsConflictHTTP|TestSubscriptionReplayRequiresLiveOriginalActorAndPayerHTTP|TestOriginalUnpaidOrderOffersAuthorizedCheckoutWithoutReordering|TestStoreRootTouchpointCreationAndRecoveryHTTP|TestTouchpointQRAPI|TestTouchpointExportCSVAPI' -count=1` 均通过。隔离 fixture 的原单恢复、变价、异 actor/payer/明细与授权、checkout 付款后/撤权拒绝，以及二维码/CSV 行为是工程 HTTP 测试，不是原商家真实收银或支付。源码核对收银接续使用现有 `useAppSwitch`，读取原订单并检查当前付款授权，不声称真实 cashier 正向通过。
- 静态增量审 `3da2f2c`：旧未版本化 baseline 使用捕获 ID 的旧合同而不填入今日 adoption；版本化路径仍执行时间及平台过滤、live veto 保留。未以此替代旧会话实际验证。

当前两处用户界面与目录错误态待修后沿原记录复验；真实生产 Identity/收银、支付、开通、硬件付款交付、写卡回读及真机 NFC 未证，不能 Done。

### 1ca6647 同原商业库复验

审查树干净快进至 `1ca6647fad2e406ebae10ddb1cc034f67cc47596`，独立 Next 生产构建成功；完整重启同版服务和 Chrome，沿用原 commercial-followup 库、原店/原入口及前述 engineering-v2 目录，owner 正常重新登录，未继承 cookie、未配置 cashier。

- 原账单显示 v2 10500 CNY，点击「查看门店版·月付开通报价」显示服务端 10500 CNY、价格版本及服务期；扩店1家报价600 CNY。界面明确收费通道未可用、当前无订单；确认支付按钮实际 disabled，点击无法提交。截图 `runtime/commercial-1ca-quote.png`。本组织无有效订阅事实，界面正确称开通报价，不将其描述为已续费。
- 对价格目录 GET 再次注入503：仅提示目录暂时读不到及等待恢复，不再误报无生效行。解除拦截并刷新后恢复 v2 及报价入口。原入口 `AC3R33V68AAK` 列表现只显示店名、目标和位置，原 `/t` URL 不变，状态仍已登记未写卡，未另建；截图 `runtime/commercial-1ca-entry.png`。换绑表单打开但本库无其他可选目标门店，未实施换绑或声称预览摘要验证。
- 上述仅工程浏览器与已声明目录夹具的结果。未证明真实收银、下单付款、开通或实体交付；本轮此前 HTTP 原单回放/授权测试仍是隔离 fixture。无新增 Done 裁决。
