# M11 原线索接续对抗审查

审查基线：Touch `9049489cf5cea94485bafaed041c2535b457e229`，Leads `6d948d348c2352c707688985b055d937ab461fd6`；主线程同时开展其他工作，后续改动不在本审查结论内。

## 发现：Leads 换账号仍沿用旧租户记忆

- 位置：Leads `web/src/app/api/auth/sso/exchange/route.ts:75`、`web/src/components/eco-nav/CrmShell.tsx:131`、`web/src/lib/eco-nav/use-crm-scope.ts:27`。
- 复现条件：浏览器 A 账号留下 `leads_tenant_id=T`，B 账号在 T 和 U 都有有效成员资格；B 从普通 shell SSO 进入，或未知业务 h 回退首页。换码端写入 `leads_sso_reset=1`，但 `web/src` 中没有任何读取或清除此 cookie 的代码；工作台仍先读取 T，以 T 请求 whoami，并可继续采用 T。因此 B 的初始组织继承了 A 的选择。
- 影响：错误保留跨账号的工作范围选择，用户可能在非预期组织操作；现有服务端仍重新核验成员与操作权限，静态证据不支持将其描述为越权。成功 M11 业务接续会先以服务器返回的映射 CRM 租户调用 `commitCrmTenant`，因此该具体情况主要影响普通入口或未知/失败接续后的回退。
- 建议：在业务 continuation 与普通入口协调消费换账号重置标记，清理旧选择及内存状态、作废在途旧对账；已验证的业务 continuation 应在重置之后写入本次服务端选出的租户。验证跨账号同租户、多租户及在途请求竞态。

## 其他审查结果与边界

- Touch 发起时校验源提交的租户、原店读权、已入库和未撤回；Leads 按完整来源四元组解析，要求原授权和实时源记录，重新校验成员/读权，未观察到按手机号或裸 source_ref 选择目标的路径。
- 前后向 resume 均按已保存主体、服务器选择的租户和当前权限复核；条件更新实现一次消费。返回登记从 Touch 已保存上下文派生原店。两个页面使用按 h 缓存 Promise 与 effect 清理，避免 StrictMode 重复消费和旧 h 响应覆盖当前 h。
- SSO 桥从请求中分别接收 code 和 h，应用层以会话 principal 与导航上下文 principal 一致性绑定；没有看到应用层验证 code 与 h 是同一次 shell launch 的额外证明。仅凭这一观察未确定可行的跨主体攻击；实际 Identity 的协议与攻击面需由平台独立验证。
- 已运行 Touch `go test ./internal/httpapi -run 'TestWorkLeadFollowUp|TestWorkLeads' -count=1`、`-run TestFollowUpCanonicalSSOCookieUsesOriginalSessionResolution`，及 Leads `-run TestTouchFollowUp`，均通过。使用项目提供的 Go 工具链；未修改业务库、启动浏览器或变更服务。
- 本次为静态及既有测试审查，未复现浏览器跨应用真实链路、真实平台 Identity/Notify 配置或实际生产部署；现有测试通过不能证明上述竞态和生产可用性。Touch 正式返回目标尚无生产平台注册证据，工程 fixture 不构成生产资格。

## 复审补记：Leads 4ab3290，Touch 187a40b

原报告的同标签页 SSO 重置遗漏已处理：业务 continuation 与普通壳均消费 reset，清除旧 storage/cache；壳对成员发现、whoami、导航请求使用会话票据与 scope epoch 防止旧结果提交。成员发现从 Identity 会话取主体，按真实成员及代理授权过滤，空范围与服务不可用分开；确认选定范围仍通过原 whoami。两个 canonical host 中间件检查请求 host 并将根路径 code+h 导向固定 HTTPS host。

### 尚存跨标签页换账号残留

- 位置：Leads `web/src/lib/eco-nav/sso-scope-reset.ts:10`、`web/src/lib/eco-nav/use-crm-scope.ts:8`、`web/src/components/eco-nav/CrmShell.tsx:129`。
- 条件：标签页 A 已打开账号 A 的 CRM 详情；同一浏览器标签页 B 完成账号 B 的 SSO。B 消费 reset 时移除共享 localStorage 项，但 cache、epoch、票据均是标签页私有内存，A 没有 `storage` 或同类监听；A 的已显示详情不会因此自动卸载或重新对账。
- 影响：换号后 A 页面仍显示先前已获取的数据，直到刷新或重新导航。新 API 请求仍以当前共享 cookie 经服务端身份和权限核验；这里没有证据表明 B 可以通过 API 越权获取新数据。该结论为静态代码推导，未实测多标签浏览器。
- 建议：评估以跨标签页通知失效本地范围并强制重新核对会话；确保成功接续所选的服务端租户以及同一账号的普通租户切换不被错误清空。

复审运行 Leads `go test ./internal/httpapi -run 'TestSessionMemberships|TestTouchFollowUp' -count=1`，以及 `crmSessionEntry`、`ssoScopeReset`、`session-memberships-bff`、`sso-root-redirect`、`finishR1.shellScope` 共 13 个前端测试；Touch `follow-up-sso` 6 个测试，均通过。未启动浏览器或改业务库。测试不覆盖前述跨标签页场景，也不证明生产平台注册或部署可用。

Touch `187a40b` 亦存在同类跨标签页残留：`web/src/lib/sso-workspace-reset.ts:3` 只移除共享租户记忆并更新当前标签页的 `beginTenantOp()`；`web/src/components/work/merchant-session.tsx:234` 仅在挂载或重试时处理，没有跨标签页失效监听。账号 A 在 `/work/leads` 已加载列表，另一个标签页以账号 B 完成 Touch SSO 后，原标签页 `MerchantGate` 仍为 ready，`web/src/components/work/leads/leads-view.tsx:100` 已获取的数据继续显示，旧的在途确认也不会因另页的内存计数变化而失效。建议接收账户代次变化后先卸载业务子树、作废原确认，再按当前会话重新发现与确认；新请求仍经过服务端身份权限校验。此为静态推导，未进行多标签浏览器实测。

## 最终复审：跨标签页修复

基线：Touch `68be5cbe9210f50767f98ac407010aafd0837290`，Leads `44defd4d1526b88f5f75e2810117f969350f3f49`。上文“尚存跨标签页换账号残留”描述的是修复前状态，不代表此基线仍未修复。

两端成功 SSO 重置时均写独立随机账户代次；原页面监听 storage 和 focus 变化，作废本页身份或 scope 代次，并先卸载业务子树，再重新发现、核验当前会话。continuation 收到变化会丢弃旧 await；相关跨应用跳转检查代次避免旧请求完成后继续跳转。单标签 StrictMode 下，同一 h 仍复用一个请求；第二次 effect 在首次 reset 后捕获新代次，不会因为同次 reset 而被当作旧账号。此轮仅涉及前端账户上下文，未更改先前审查的源身份、撤回和原记录服务端校验。静态复审未发现新的具体问题。

针对既有测试，Leads 相关 5 个文件 15 例、Touch 相关 2 个文件 10 例均通过。未启动浏览器；上述测试不代替独立真实多标签页验收，真实 Identity/Notify 与正式 Touch 返回 target 的登记仍须按平台事实分别确认。

## 普通登录与退出增量复核

Touch `2e74e6b6ef54a5f1ccbbdcad47905a6f999dbed4` 补充了普通登录/退出成功时的账户代次通知。修复前该通知只覆盖 SSO，正常登录或退出不会让其他标签页重新核验；修复后两种成功路径复用清除租户记忆、作废旧票据、广播新代次的逻辑。退出请求失败或返回非成功时进入错误状态，不冒充退出成功。SSO 路径仍复用相同重置逻辑；本增量未改服务端来源校验。静态复核未发现新增具体问题。

已重新运行 Touch `follow-up-continuation` 与 `follow-up-sso` 既有测试，11 例通过；未启动浏览器。正常登录/退出跨标签页的真实交互仍需独立浏览器验收；测试仅覆盖相关辅助逻辑及 SSO，不单独证明所有实际页面交互。

## `/admin` 原路径复核与上一条范围更正

上一条 `2e74e6b` 的普通登录/退出结论仅适用于 `merchant-session` 工作区，不能覆盖独立的 `/admin` 页面。实际原路径双标签红测表明 `/admin` 的原登录/退出当时仍未发出账户代次信号；不应把 merchant 工作区的修复视作原路径已修复。

对 Touch `9be70714a0c5f207cee1522f4fec89e31981ed32` 的 `web/src/app/admin/page.tsx` 增量只读复核：原 `createBffSignIn` 成功回调及独立退出成功分支均调用账户切换重置，通知其他标签页；退出失败显示错误并保留现有会话。外层 `AdminPage` 监听账户代次变化并更换 `AdminWorkspace` 的 key，重建原路由的列表、表单和会话状态；新子树以原 membership/whoami 流程重核，并消费 SSO 标记。未见该文件增量中新的具体问题。

本次没有运行浏览器，也未以新入口替代 `/admin` 原路径验收；原两标签浏览器复验由独立验收者进行。先前最终结论均应按对应基线和入口理解。
