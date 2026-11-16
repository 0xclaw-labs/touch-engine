# commercial｜HUI-4663 + HUI-4664 实施交接

状态：实现已本地提交，尚未通过独立正常入口浏览器与对抗审查，不能标 Done。未 push、未改 main、未写 Linear。

- 分支：`codex/touch-4663-4664-commercial`
- 工作树：`/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/commercial`
- 基线：`abbedd3`
- 实施提交：`67dcaec82f964824cfda14424814caae278f9029`
- 不新增迁移，不复制 Identity、订单、钱包、权限和实卡权威。

## 实际改动与原权威

### M13 人员与任务

1. 新增受控 `POST /api/v1/admin/members/lookup`，仅沿既有 `manage_members` 授权门。客户端只填完整已知邮箱；服务端规范化、拒部分/批量邮箱、每主体跨租户共用 10 次/10 分钟限频、`Cache-Control: no-store`，再请求原 Identity `GET /internal/v1/identity/users/exists?email=...`，携原 internal token + App-ID。只接受原始 active `usr_`，不从邮箱生成账号，不转发 account_id、status 或无关目录。无权请求不触碰原 Identity。
2. M13 门店经理、店员和建立代理均用 `PersonPicker` 核对邮箱、选择原人员。结果保存可辨识邮箱为 display_name；旧成员仍读原记录。服务不可用明确重试/联系平台恢复；账号未就绪明确先注册并确认可用，无手填内部编号退路。
3. 授权前用原生 HTML dialog（不是 window.confirm）展示受权人、组织、门店、实际任务、期限与付款资格，支持返回修改、Escape 和焦点约束。店员新增可选择原 `STAFF_PERM_LABELS` 白名单的任务；新旧成员默认不扩权。人员先加入但后续任务授权失败时如实提示从该人员行重试，避免再次加人。
4. 配置授权不授予付款权。原代理付款 checkbox 不再用 `granted-by-owner` 铸造占位代付引用；设置任务时保留既有独立 `payer_grant_ref`，未获者交回负责人或已有付款授权人办理。
5. 普通授予、撤销、停用、模板下发的审计原因由真实业务动作形成；例外更正保留可选说明。停用与撤回仍有明确后果确认。店员保存无改动时沿实际现行权限，不误提交空集合撤权。
6. 技术权限明细折叠，常用任务显示产品名；模板/白标保留在独立折叠区域。店员选范围必须显式选择门店或全部门店。
7. M13 读面增加代际校验，切换组织/卸载或新一轮读取后，迟到的旧数据不能写回。人员查询编辑邮箱或开始新查询即废弃旧选择。
8. 成员沿原长期授权+即时撤回合同；代理期限沿原 `valid_until`。未建立第二套到期机制。

### A1 真实授权与受控进入

1. 原 `agency/relations/granted` 增加原成员 enabled/agent 与原 `HasValidAgencyRelation` 校验，过期/停用目标不再作为可进入商户展示；曾有未撤销但过期仍出现在列表的缺口，已先红后绿回归。
2. A1 按真实授权商户读取原订阅/订单；只在 renewal 授权内查询账单，显示当前服务与期限、未付/已付待开通/异常/硬件原单的接续；无权或上游不可用明确交回商家负责人，不生成示例任务。
3. 修复门店配置原先 `switchTenant` 后立即硬跳的竞速；工作台、门店与账单统一等原 whoami 确认目标组织后才进入受控目标。
4. `MerchantGate` 窄接 `AgencyWorkContext`，在门店/内容/账单等页面持续显示正在为谁办理、获准任务、独立付款资格及返回代理商家列表。未改共享 work-frame 导航。
5. 新增原会话 `clearWorkspace()`：废弃在途组织确认，清旧组织/角色/名称/features 与记忆，保登录和组织选择；返回 A1 时清理旧商户上下文。抽屉读取加序号，关闭或换商家后的旧响应不回填。
6. A1 的门店配置、硬件验收去向沿原门店/账单入口；实卡详细原事实在 M12 读原 1663 投影，无独立代理履约账。

### M12 服务、原单和交付

1. 首屏先当前组织服务、期限、额度、付款资格与待处理订单。无目录、无付款资格、无收费通道、订单暂不可读分别给具体处理方；无效购买表单不占主流程，已有订单仍可回查。
2. 新增只读 `POST /api/v1/billing/subscription-quote`：沿原 billing:view，直接消费原 `ResolveCatalog` 与 `ExpansionQuote`（含阶梯），不写订单/预占/付款。页面确认实际金额、币种、价格版本、服务天数及付款组织，原 price_ref 带入下单；目录变化沿既有 `catalog_changed` 拒绝，未另造报价规则。
3. 待支付、已付待开通与已付款后异常优先回查原单，不引导重复买。浏览器返回不是支付事实；硬件付款不是实卡交付。
4. 原下单的 idempotency_key 按一次明确意图持久至 sessionStorage，同次响应丢失/刷新/重试沿用，双击单飞；已知原单终态后才清该意图键。付款/开通异常不把配置权升成代付权。
5. 回查原单可更新列表；读取/回查/报价均有组织代际或请求序号，晚到响应不污染当前商家或当前所选原单。异常能重试且不会锁死下单按钮。
6. 订单页增加 `GET /api/v1/touchpoint-deliveries` 读面，复用 1663 `deliveryStatusFace`/`deliveryEventFace`/`readbackFace` 展示绑定、写卡、回读、验收、交付、补换与绑定失效。原权限不足时明确由商家负责人去门店投放入口核对。账务五步和实卡七步各保留原事实，未把 paid 变成实卡成功。

## 删除/调整字段及其数据来源

| 原字段/步骤 | 调整后来源或必要性 |
| --- | --- |
| 成员/代理手填 usr_ | 完整邮箱经服务端权限、限频查询原 Identity，用户选可辨识邮箱；原 user_id 隐式带入 |
| 普通授权、停用、撤回必写原因 | 系统用业务动作形成原审计上下文；例外更正可写真实说明 |
| 配置页代付 checkbox | 不再生成占位付款许可，读并保留原独立付款引用，付款资格交原权威 |
| 账单直接下单、只在订单页发现旧单 | 首屏原单接续；报价后明确金额/组织/版本确认，按原单回查 |
| 无目录仍出现加店数输入 | 目录、订单、收费通道可用且有付款资格才进入办理表单；否则具体恢复路径 |
| 前端扩店单价乘数量估算 | 原账务 `ExpansionQuote` 只读报价，含原阶梯计价 |
| 设置页默认铺满模板白标 | 独立折叠入口保留原能力，不混在选人与任务授权中 |

## 文件

- `server/internal/identity/client.go`：原 Identity 精确查询客户端。
- `server/internal/httpapi/handlers_member_lookup{,_test}.go`、`server.go`：原成员授权门下人员查询与限频。
- `server/internal/httpapi/handlers_subscription_quote{,_test}.go`、`handlers_billing.go`：原计价函数只读接线。
- `server/internal/httpapi/handlers_agency.go`：组合列表排除过期/停用。
- `web/src/components/work/settings/{settings-desk,person-picker,task-review}.tsx`：M13 与确认。
- `web/src/components/work/agency-console/{agency-console,agency-work-context}.tsx`、`merchant-session.tsx`：A1 与持续上下文/返回清理。
- `web/src/components/work/billing/billing-desk.tsx`、`web/src/lib/billing.ts`：服务、报价、原单、实卡读面。
- `web/tests/{settings-page,agency-console-page,commercial-jobs}.test.ts`：调整原结构断言并覆盖付款/开通/实卡接续判断。
- `web/e2e/scripts/identity-stub.mjs`、`web/e2e/specs/15-commercial-jobs.spec.ts`：明确注册身份工程替身和正常产品入口回归。

## 验证命令与结果

全部 source `/workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`。

1. `go test ./internal/httpapi -run TestMemberLookupControlledIdentity -count=1`：路由未实现时 405 红；实现后绿。覆盖无权不调用原服务、拒部分邮箱、原 user_id、最小披露、限频。
2. `go test ./internal/httpapi -run TestGrantedPortfolioExcludesExpiredAndDisabled -count=1`：原过期授权仍可见时红；过滤后绿。最终覆盖另有本人组织、目标授权到期与目标成员停用的组合。
3. `go test ./internal/httpapi -run TestSubscriptionQuoteUsesOriginalTierAndCreatesNoOrder -count=1`：未有路由时 404 红；最终绿。三家加店按 2×30+1×100 = 160 原阶梯报价，staff 403，报价不新建订单。
4. `go test ./internal/identity ./internal/authz ./internal/billing ./internal/store ./internal/httpapi`：identity/authz/billing/store 全绿；完整 httpapi 只有继承内容域 `TestContentPacksMerchantFlow:276` 旧缺价断言失败（merchant_upload 缺 price_note 返回201）。root 已有内容修正，未碰其他组写面。
5. 最终 `go test ./internal/httpapi -run 'Billing|Agency|Member|Access|Granted|Subscription|Auth|Scope|Template' -count=1`：绿，18.190s；完整商业相关 HTTP 回归。
6. 新 `web/tests/commercial-jobs.test.ts` 最初因函数未实现2项红，最终绿，覆盖待支付/已付待开通/已付异常阻止重买与实卡分离。
7. `npx tsc --noEmit`、`npx tsc --noEmit -p e2e/tsconfig.json`：绿。
8. `npm test`：116文件，115通过；1163项通过，仅继承内容3条过期文案断言失败。root 已确认1fb68f6修复，无商业域失败；UI finish 闸绿。
9. 最终 `npm test -- --run tests/settings-page.test.ts tests/agency-console-page.test.ts tests/billing-page.test.ts tests/commercial-jobs.test.ts tests/uifinish.test.ts tests/workspace-tenant.test.ts tests/touchpoint-deliveries.test.ts tests/eco-nav-identity-switch.test.ts tests/session-continuity.test.ts`：9文件90项全绿。
10. `npm run build`：生产构建成功。
11. `node --check web/e2e/scripts/identity-stub.mjs`、`git diff --check`：绿。

## 正常浏览器入口、数据与独立审查

新增 `web/e2e/specs/15-commercial-jobs.spec.ts` 从正常 `/` 登录，打开「管理与更多→团队、权限与代理协作」，选择邮箱、门店和任务，预览、授权与撤回；代理正常登录后由持续横幅返回商家列表、点进入商户，再返回确认清旧上下文；M12 检查无生效目录与无效表单；390/430/桌面横向溢出检查。

- 原工程 owner：`owner@e2e.test`，原 env fixture 密码 `e2e-pass-123`。
- 明确注册的成员：`member-commercial@e2e.test` → 原 fixture `usr_commercial_member_original`。
- 明确注册的代理：`agent-commercial@e2e.test` → 原 fixture `usr_commercial_agent_original`。
- 两者登录解析与 exists 查询共用同一固定 fixture 记录，未现场从邮箱派生商业选人账号；新增探针要求 e2e Identity internal token + touch-engine App-ID。其他原 fixture 行为兼容。
- 服务曾以隔离端口19160/19161/19162/19199起动，`env.mjs serve` 已报告 up complete。`playwright ... specs/15-commercial-jobs.spec.ts` 使用 `/usr/bin/chromium` 在进程启动就遇到 socket()/ptrace Operation not permitted，未执行到任何产品步骤；没有成功浏览器截图，不计为产品失败或成功。root 已提供 runtime 内官方 Headless Shell，独立 Terra 应使用 source 后的 `HARNESS_BROWSER_EXECUTABLE`，不能沿用 `/usr/bin/chromium`。
- 可运行：`E2E_GO_PORT=... E2E_IDENTITY_PORT=... E2E_WEB_PORT=... npx playwright test -c playwright.config.ts specs/15-commercial-jobs.spec.ts`，使用对应 env.mjs seed。独立审查还应覆盖第二商家进入/返回、迟到订单/人员响应、撤权后下一请求、原单已付待开通、目录改变与无独立 payer 配置代理负例。
- 浏览器尚未独立通过，故本报告只交代码与工程回归，不给 Done。

## 分层证据与外部阻断

- 工程验证：Go 原域函数/HTTP/持久化组合、TS/类型/构建、固定注册 Identity 替身；上述测试结果仅在此层成立。
- 跨模块实际集成：代码消费原 Identity、权限、账务计价/订单、1663投影的合同；生产候选浏览器与组合对抗须由 root/Terra 集成版补验，本报告没有把自己未执行的浏览器路径算通过。
- 外部渠道与正式交付：本机没有正式公共 Identity/真实支付与实卡凭据；未进行真实扣费、服务生效生产同步、写卡/回读/发货签收与现场验收，不伪造正式成功，仍归4666整店验收收口。
