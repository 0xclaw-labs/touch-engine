# analytics / HUI-4665 实施报告

状态：已实现并完成工程回归，等待 root 集成同 SHA 后的正常浏览器入口、390/430px/桌面及独立对抗审查；不标 Done。
基线：9dd04a7467020602bae130186d4ed738404fb65d。
工作树：`/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/analytics`。
分支：`codex/touch-4665-analytics`。
本地提交：`bfe12f1d043daa3ca93d3545d49132f40589655e`。

## A 段：可复现根因与修复

真实 HTTP 空租户夹具沿既有 requireSession → 成员权限 → DashboardOps → BuildOperations → JSON 读面发请求，租户 B 没有门店、活动、素材或事件。新增 `TestDashboardOpsEmptyTenantCollectionsAreReadable` 修前失败：`empty filter_echo must be {}, got <nil>`。Go 的 `len(nil)==0` 无法抓住此跨层问题。

具体有两个独立崩溃点：

1. 无筛选的 BuildOperations 没有初始化 FilterEcho，HTTP 为 `filter_echo:null`；页面在 `stageNotAppliedLabels` 中直接 `filterEcho[key]`，前端回归原样复现 `Cannot read properties of null (reading 'campaign_id')`。
2. 没有库存批次时响应 Inventory 没有初始化，HTTP 为 `inventory:null`；页面后续直接 `.length` / `.map`，同样违反消费端的数组契约。

服务端现在在统计组装边界返回 `{}` 与 `[]`。客户端新增 `parseOperationsResponse`，兼容旧服务的 nil 集合；数字缺失、null、NaN 不补业务零。增量证据/线索/费用字段缺席保持未知；损坏集合、损坏响应或组织范围不匹配变成可重试的响应不完整状态。原来把缺少证据类别直接补 0 的 `evidenceRowList` 已改为未知。

`readAnalytics` 分别判断经营读面和目录读面。404 为未开通；401/403 为无权并提供返回/授权下一步；网络/5xx/契约错误为失败并可重试；成功且已读取的本地空账本才展示已证明的零。目录单独失败不再把已成功读取的经营事实整页抹掉。订阅/硬件金额缺席不宣称窗口内已证明没有支付事实。

请求结果附带筛选/刷新范围键，筛选第一次 render 就隐藏旧结果；effect 的 alive 清理丢弃迟到结果。组织/角色以 React key 隔离整个统计读面，包括草稿筛选；焦点恢复自动重新读取权限与事实。新增刷新按钮及工作台返回入口。

还发现并修复一个统计读边界越权：原 DashboardOps 的订阅、AI、硬件对账只按租户取数，店经理/店员却不能访问原账单接口。统计 HTTP 现在复用原 `authz.PermBillingView`，没有账单权时仅该区块返回 `{availability:"forbidden", note:...}`，不序列化金额和单数，其他本店事实继续可读。新增回归修前明确失败（店经理获得全部费用投影字段），修后整个 HTTP suite 通过；组织所有者原字段兼容保留。

## B 段：经营事实呈现

复用原统计权威，没有写入业务成功，没有新增统计账或 ROI 权威：

- 门店服务独立呈现已有服务打开/唤起事件，明确不代表办理完成、到店或成交。
- 活动参与与权益七项独立口径、三类核验证据、六层发布证据继续分别显示，不合成成功总数；同一参与的覆盖数沿原 distinct participation，官方按可信 attempt。不可用层即使携带残留数值也显示未知。
- 点击、唤起、媒体交接、自报、店员观察/核实、平台证实保持分层；0 条本地官方回执不等于平台已授权或实际零发布，未同步的外部事实不可计。
- 授权线索的提交、待同步、持久化接收、撤回和去重联系人继续独立展示。待同步不升级为已获客。
- 券面额、奖励兑现成本、内容生成费用、组织订阅实付、AI 投影、硬件、退款分别显示。存在未录成本批次时，不把已录部分和 0.00 当成完整成本；缺金额显示未知。内容结算费用与 AI 费用可能关联，不叠加计算总支出。
- 来源、去重键、事件时间、窗口、分母继续可展开；补充库存当前快照、异常当前队列与窗口流水、证据类别及成本口径。目录原接口提供的 definition 也展示出来。
- 原目录接口没有应用页面活动/门店筛选，故明确披露其受信授权范围。组织账单没有门店分摊维度，明确说明业务筛选未生效。服务、核验证据、官方尝试、线索、库存、奖励成本、异常及六层分别提示不适用的已选筛选，不能伪装过滤后算转化。
- 活动七项真实零不再否定门店服务、线索或费用事实。没有一致分母不计算转化率；缺真实成交、完整费用和因果证据不展示完整 ROI 或增长效果。
- 没有删除输入字段；没有改共享导航/Today/其他业务页面；今天摘要原消费端保持不动。内容包版本/平台筛选保留用于查找已冻结的具体经营事实，内部门店/动作引用继续由现有选择器带入。

## 文件

- `server/internal/dashboard/funnel.go`
- `server/internal/httpapi/handlers_dashboard_ops.go`
- `server/internal/httpapi/server_dashboard_ops_test.go`
- `web/src/components/work/analytics/analytics-view.tsx`
- `web/src/lib/analytics.ts`
- `web/tests/analytics.test.ts`
- `web/tests/analytics-render.test.ts`

## 工程验证（已执行）

1. 前端先红：原 null 筛选崩溃、缺证据错误补零、缺费用输出 NaN 共 3 项；增量边界、真实渲染、未授权金额、无效筛选披露分别补回归并先见失败。
2. 服务端先红：真实空租户 JSON 合同、账单权限旁路两项，均已记录上述实际失败信息。
3. `GOCACHE=/workspace/scratch/b0e2059d4a63/runtime/go-build-cache /workspace/scratch/b0e2059d4a63/runtime/golang/go/bin/go test ./internal/dashboard ./internal/store ./internal/httpapi`：三包全部通过（dashboard 0.012s，store 64.963s，httpapi 102.912s）。包括原有固定事实集、API 对独立 SQL 复算、重复、晚到官方回执、更正撤销、同参与多证据、跨租户和受信门店筛选相关用例；这些工程夹具中官方事实/受信回执的模拟不冒充真实平台接入。
4. 最终 `cd web && npm test`：110 files / 1150 tests 全通过，耗时 8.12s。新增统计真实 React 静态渲染 7 项；统计纯函数测试 28 项，既有统计页面测试 20 项。
5. `git diff --check`：通过。
6. `tsc --noEmit`：仍因既有 `tests/contrast-gate.test.ts:4` 未使用的 `@ts-expect-error` 和 `tests/customer-journey-api.test.ts:43,44,62,63,91,106` 未收窄 ParticipationResult 的 6 处失败；analytics 生产代码及新增测试没有类型错误。未改他组测试来遮蔽。未声称本工作树的生产 build 已通过，root 在集成候选 SHA 统一构建核验。

## 正常浏览器复验步骤与数据条件（交接 root / 独立审查）

浏览器本组尚未完成：root 正在统一保持 repo e2e 服务生命周期，要求集成本提交后使用同 SHA 的 Go 与 Next，再用托管浏览器正常入口验收；本组没有混用旧 Go 给权限修复宣称通过，也没有改库造验收结果。以下步骤是可重复执行的验收清单，不是已通过截图证据。

1. 同一候选 SHA 启动 repo e2e 环境，正常访问 `/work`，使用该轮 `web/e2e/evidence/seed.json` 的账号凭据登录。从导航“经营看板”进入 `/work/analytics`。Identity/Billing/Task 替身仅为工程环境。
2. 新组织不创建门店、活动、素材或事件，进入统计：加载完成，无 Application error；七项本地事实为 0，库存为无批次，未接入指标/成本未知；展开来源口径后可返回工作台。保存 API 脱敏响应，确认 filter_echo={}、inventory=[]。
3. 通过正常门店入口创建门店，暂不创建活动，从门店公共服务入口产生打开/唤起事件后返回统计：门店服务有真实事件记录，活动七项零不否定门店服务，服务事件不升级为完成/成交。
4. 在已有固定已知事实的数据组织，选择门店 A，再选择门店 B，应用/重置，逐项对 API 以及上述既有独立 SQL 测试口径。关注同参与多类证据只增加各自证据、不增加 distinct 人数；晚到、更正、撤回按原窗口/当前态披露。
5. 选择平台/动作/环境：线索和库存等不支持维度的区块必须提示未生效，目录与组织费用范围必须披露；展开口径后返回仍可操作筛选。
6. 以店经理或店员登录同一组织：本店经营事实可读，费用区块只显示无账单权限；网络响应不得含组织费用金额/单数。越店筛选应由服务端 403 拒绝。所有者仍可读原组织账单投影。
7. 网络层分别令统计请求延迟、503、断网、401/403、404，以及目录独立失败；确认加载、失败重试、无权返回、未开通、部分未知语义。此处故障注入只验证读边界，不计作生产链路成功。
8. 延迟门店 A 请求后切门店 B；延迟旧组织请求后切组织；旧结果必须立即隐藏，迟到响应不能覆盖新范围。撤权后刷新/浏览器回焦，不能保留旧金额或事实。
9. 分别以 390px、430px、桌面宽度完成应用/重置筛选、展开来源口径、刷新与返回，检查无横向遮挡；保存同 SHA、配置、步骤、预期/实际及脱敏引用和截图。

## 分层证据与遗留

- 工程验证：上述 HTTP、真实 React 渲染和完整相关 suite 已通过；空库根因已由真实 HTTP JSON 加客户端原错误定位。
- 跨模块实际集成：root 待集成本提交后验证正常入口、整条门店操作和响应隔离；本报告不以静态渲染替代浏览器证据。
- 外部渠道/正式交付：真实身份、平台授权、官方发布回执、支付、实卡/NFC 与生产方没有本组新增证据；仍按总控 HUI-4666 的独立真实环境验收。0 条本地官方记录不证明外部零发布。
- 商业首要指标未拍板不阻塞已交付的 A/B 工程改动，但不能据此宣称票面全部验收或 Done。

## root修复轮1（terra发现）
- 内容生成费用沿copy_jobs→同租户campaign归属应用受信门店scope/门店筛选，未归属不分摊；页面披露此口径不是完整门店总成本。
- 检测每条已结算记录的有效整数金额；任一缺失/null/非数值/负值都保持合计未知，不显示部分SUM冒充完整费用。
- 两统计读面复用同一opsScope，并沿既有authz对成员受信门店RecordScope裁决；带范围店员可看本店、越店403、撤权同会话403。真实红测澄清：原staff先被allowScopedList误拒403，未证实跨店泄漏；该表述纠正terra静态猜测。
- 浏览器证实analytics返回/work为404。真实首页是/，已改正常/错误态两处返回，未另造路由。报告前文/work入口说明应替换为/，兼容/admin可用于表单登录但不是首页。
- 测试先红：费用范围3断言、金额4类型、staff正常本店读403；修后 `go test ./internal/store ./internal/httpapi ./internal/dashboard -run 'TestDashboard.*|TestOperationsSummary|TestBuildOperations|TestCost' -count=1`三包通过。
- 前端 `npm test -- --run tests/analytics.test.ts tests/analytics-render.test.ts tests/analytics-page.test.ts` 3文件55项通过；git diff --check通过。浏览器返回修复待terra同SHA复验。
- 新测试首轮空子测试名产生#00与SQLite文件URI解析冲突，已改成missing/null等描述性测试名（未改变断言或业务路径）；重跑通过。
