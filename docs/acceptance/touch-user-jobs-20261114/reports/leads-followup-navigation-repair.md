# M11 原来源线索详情与返回原店：实施交接

实施候选已本地提交，等待 root 集成后的独立 Terra 原入口浏览器及代码复验。没有 push、PR、merge、Linear 或 Done 操作。没有写原 Touch `/tmp/touch-e2e-ZLFKmv/touch.db` 或原 Leads `/tmp/leads-browser-review-20261114/leads.db`，没有 reseed 原记录、清撤回或造 ACK。

## 不可变候选

| 仓库 | 独立树 / 分支 | 起点 | 提交 |
| --- | --- | --- | --- |
| Touch | `.worktrees/leads-followup-navigation` / `codex/touch-leads-followup-navigation` | `7393e19` | `92a525b7666f4c1833609557898a6aca00e7820f` |
| Leads | `.worktrees/touch-followup-navigation` / `codex/touch-followup-navigation` | `3b83dd6` | `6d948d348c2352c707688985b055d937ab461fd6` |

Touch 19 个文件；Leads 20 个文件。显式路径 git add，node_modules 未纳入提交。提交前两仓 `git diff --check` 与 `git diff --cached --check` 均通过。两仓本次写面不包含生产 manifest、通用 app switching、活动权益、媒体导出或历史 CustomerPublish。

## 最小合同与实现

Touch 正常 `/work/leads` 原行增加“进入获客详情”。只有原接收方确认入库、原许可有效且当前成员对原门店有 ReadRecord 权限的行才可发起；后台按原 source ref 找 submission，并核租户、原店及当前 scope，不能由客户端替换。跨店 404，待同步/撤回/无法核验 409。

沿现 `LEADS_FOLLOW_UP_BASE_URL/TOKEN` 服务通路，以固定完整 tuple `touch-engine/notify/sourceTenant/ref` 解析真实 CRM tenant/lead。Leads 从原 committed inbox/source scope/active source consent 解析唯一记录，拒绝缺映射和歧义；核当前 CRM 成员、代理授权、原记录读权，并实时走原 Touch receiver 接口重核许可。Notify 尚未来得及撤回也不能绕过源 tombstone。手机号、ACK receiver_id、bare source_ref 均不当作 lead ID。

原 Identity shell 请求仍只有 principal/tenant/app/target 四字段，原 launch URL/code+h 不修改。源端使用解析所得 CRM 租户请求登记目标 `leads-engine / ti-leads-engine-web`，将随机 h 的业务上下文通过受信服务登记到目标，再复用原 Touch jump mint→redeem 一次性交给浏览器。通用 strict2 switch 未改。新 restricted 服务另以原 token 对 exact path+raw body 做域隔离 HMAC，浏览器 BFF 注入 internal token 不足以伪造 principal 登记。

Leads 正常 SSO 桥保留原 h；真实 SSO 后 continuation 以服务器上下文选 CRM 租户，重新核当前真实 principal/成员/读权/原 tuple/新鲜源许可，一次消费 60 秒 h，再进入原 `/leads/{leadID}` 及原 timeline API。未知业务 h 回原首页，不影响普通生态入站。错误身份不返回记录，过期/重放 410。两端 continuation 请求缓存按 h 键控，StrictMode 复用同一次请求，换 h 不会读旧记录响应。

原详情只增加 opaque visit 返回按钮，仍是原 CRM 详情。返回时当前成员/记录读权再次核验，原 shell 对 `touch-engine / ti-touch-engine-web` 生成新 code+h；Leads 沿原 lead_receiver service 身份通知 Touch。源端只从已存 origin 派生原租户/门店，不接 return URL/客户端 store ID；真实 Touch SSO 后再次核 principal/当前源成员/store scope，消费返回 h 后到原 `/work/stores/{id}`。安全返回不恢复咨询许可。当前生产 manifest 没有 Touch target，按钮诚实显示未获登记或当前不可用，可重试，不拼 URL。

## 实际写面

Touch：`handlers_lead_followup_navigation.go`、原路线注册、`follow_up_navigation.go`、migration `0138_follow_up_navigation_contexts.sql`；原 leads-view 行操作、merchant-session 原 tenant reset 消费、failure copy；新固定技术承载 `/lead-follow-up-return/[h]`、原根路径 SSO relay middleware、canonical SSO bridge；窄测试、合同及 env 样例。

Leads：`handlers_touch_followup.go`、`touch_followup.go`、`follow_up_navigation.go`、原路线注册、Identity 原 shell client、migration `0032_follow_up_navigation_contexts.sql`；原 landing/exchange保留 h、新 `/touch-follow-up/[h]` 技术承载、原详情返回组件；窄测试、工程 Identity/browser runner、合同与 env 样例。

两迁移均仅导航 context 表，保存主体/来源/租户/门店/lead 引用，不保存联系方式，不复制 CRM，不写原 ACK、consent、撤回水位。源 origin/详情 visit 30 分钟，入站/返回 h 60 秒一次消费。

root 需在最终候选更新 Touch page-map/page-census 技术承载映射及物理 route 数；本树基线旧，不覆盖 root 已新增 C5 映射，原 28 个业务视图不新增一个业务页。

## 已观察 red 与修复回归

1. 初始 `TestTouchFollowUpResolvesOriginalTupleAndCurrentAuthority`：端点缺失，正例全为 404。新 resolver 后通过。
2. `TestWorkLeadFollowUpGatesOriginalStoreAndOneTimeReturn`：新真实 HTTP 回归发现 store manager 跨店返回 403、未用既有原记录 404 mask；又发现 register 201 空 body 被客户端错误当作不可用 503。分别改为原 scoped read 404 mask，以及允许成功空响应体后通过。该项属于新接线被红测捕获的问题，没有伪称原基线已有此端点。
3. root 预审发现 unknown h 落不存在的 `/work`、缓存未按 h 区分，已修为 `/` 和按 h 键控，补行为函数断言。
4. 新 cookie 回归确认工程 Identity 通过真实 HTTP exchange 颁发 `touch-engine_session` 后，同一 cookie 进入 Go 原 requireSession→Identity session/resolve→whoami 成功；同 cookie 选其他租户仍 403。不是绕过 Go 或改名注入会话。
5. Touch `touch_sso_reset` 已由原 workspace-tenant 机制消费，清旧组织记忆并作废在途确认。多租户新主体回原组织选择；业务 continuation 先消费 reset，再保存服务器核准源 tenant。

测试夹具曾因断言用错 whoami 字段 `principal_id`（实际 `principal_ref`）与读取 nil 默认 HTTP client 而失败，均修正测试夹具后重跑；没有据此声称产品缺陷。

## 运行验证

环境 `source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`，Go 1.26.5，原共享依赖；无提权或新下浏览器。

- Touch 最终：`go -C server test ./internal/httpapi -run 'TestWorkLeadFollowUp|TestFollowUpCanonical|TestWorkLeads|TestLeadRecord|TestEcoNav' -count=1`：通过，3.817s。
- Touch 相关三包前轮：`go -C server test ./internal/httpapi ./internal/store ./internal/identity -run 'TestWorkLead|TestLeadRecord|TestLeadReceiver|TestEcoNav' -count=1`：通过，HTTP 3.087s；后两包此筛选无匹配测试，不把它们表述为全包覆盖。
- Leads 最终：`go -C server test ./internal/httpapi -run 'TestTouchFollowUp|TestTenantBinding|TestNotify|TestTouch' -count=1`：通过，3.183s。覆盖 sourceTenant≠CRMtenant、同 phone 多 source、原 tuple、源撤回先于 Notify、当前成员停用、实际会话、错误主体、h 过期/重放、原返回 target 拒绝后重试与原 receiver 服务身份。
- Leads 前轮相关三包同筛选：通过，HTTP 3.560s；后两包此筛选无匹配测试，不冒称全包测试。
- Touch Web `follow-up-sso / follow-up-continuation / workspace-tenant / session-continuity`：4 文件 19 项通过。
- Leads Web `sso-landing / sso-exchange / follow-up-continuation`：3 文件 40 项通过。
- 两仓 `tsc --project web/tsconfig.json --noEmit --incremental false`：最终通过。
- 两工程 runner `node --check`：通过；未把语法验证当作已跑浏览器。
- 首次误用 npx tsc 命中非 TypeScript tsc 提示，未作为验证；之后明确使用原共享 node_modules/.bin/tsc，未修改依赖锁。

遵 root 交接要求，未重复整个原业务浏览器流程或跑重复全包后才交 SHA。root 合并最终候选负责全量；独立 Terra 负责原正常入口实际浏览器验收。

## 工程 runner 与审查前提

Leads 仓 `scripts/touch-followup-identity-fixture.mjs CONFIG.json` 和 `scripts/verify-touch-followup-browser.mjs browser.json`；完整部署字段与可执行配置格式见 `docs/integration/touch-followup-navigation.md`，两仓各有 `deploy/touch-followup-navigation.env.example`。

fixture 是明确工程 Identity/受信 target 替身，仅 loopback、不碰应用 DB、不造成员或线索，严格四字段 shell、原 code+h、app audience/Host/Origin、60 秒 code 和一次兑换；真实 Go 的 app session/权限仍独立核验。浏览器 runner 只将两登记 HTTPS canonical origin 传输代理至本地 Next，保留浏览器原 URL、Host/Origin 和真实 Identity cookie。没有 mock 业务或 SSO 响应。按正常 merchant 登录、原 row click、原 timeline 200、原详情返回按钮与原店标题输出三张截图和脱敏网络证据。

独立 Terra 已收到精确短 SHA 与入口。正例需原 C6 正常新建并真实 relay 接收 ACK，填实际 full tuple 对应 lead ID；原两个已撤回记录必须保持撤回。原 Leads 工程目标尚无成员时，只可在 root 已认可的初始工程前提下经原 CLI 配置实际共同 principal，不能 SQL 造收件/清水位。Identity fixture 的配置与测试凭据仅本地 0700/0600 文件，不在聊天或仓库粘贴。

Touch canonical host 必须由部署明确给 `SSO_APP_HOST`；Go 同时设 `TOUCH_SESSION_COOKIE=touch-engine_session`，Leads 为 `leads-engine_session`。桥原样转发 Identity cookie，不改名或造 token。没有这些运行配置就应真实失败，不能把工程注入 cookie 冒充正常 SSO。

## 限制与风险

- 正式 Touch app/target 未在已核 public-ai production manifest 登记，正式安全返回不能宣称完成；工程 target 不替代正式资格。
- 跨进程源实时复核仍存在正常分布式读与用户随即撤回的时间窗口；已签 h 进入时再次复核，原 CRM 详情后续操作仍走原业务权限/许可合同。没有新增授权真源。
- 新表按 TTL 拒绝过期上下文，当前没有新增后台 GC；只存 opaque routing/source 引用，无联系方式。后续可沿既有维护流程清理过期上下文，不影响业务账本。
- 未声称原正常浏览器全流程已通过；须 Terra 对 root 最终不可变候选沿原库、原 C6 新咨询与正常 M11 行完成实际复验。

## M11 普通 SSO 无已选租户续修（2026-11-14）

root 正常流程复核发现：清旧账号 tenant memory 后，原 whoami 强制 X-Tenant-ID，普通 SSO/未知业务 h 无法发现可选 CRM 范围。按 root 指派只修后端范围发现，前端 reset/epoch/CrmShell/continuation/middleware 由 root 负责，本补丁未编辑这四处文件。

- 独立 Leads 原树先 FF `8aa3e8a`，新增本地提交 `7129cacfed3768497aa2a6bd89842fa01adc7086`；6 文件、无新迁移；提交后树干净，无依赖/原 DB/远端改动。
- 搜索确认原仓仅有 tenant-scoped `ListMembers/GetMemberByPrincipal` 与原 `GetAgentGrant`，没有无已选租户的当前主体发现端点。原 tenant 模型及全部迁移无 enabled/lifecycle 字段，未发明新状态。
- 新 `GET /api/v1/session/memberships`，浏览器 `/api/session/memberships`；200 `{items:[{tenant_id,display_name}]}`。复用原 internal/config/Identity session resolve 共用链，仅当前发现 handler不要求已选 tenant；具体业务 requireSession 强制 tenant 与后续权限腿保持原行为。
- 只读此真实 principal 原成员与 tenant name，再用原 ActionReadList 和当前 per-tenant AgentGrant 过滤。没有成员/停用/未授权代理：200 items:[]；无 session 401；Identity、成员或 grant 权威故障503，不能伪作空数组。伪造租户/主体 header/query 不改变解析主体。渠道 operator 不是 CRM member，未变成 CRM 范围。
- 返回数组排序只用于稳定呈现，无 selected/default 字段。root UI负责多范围显式选择，单范围也先原whoami确认；不把发现当业务授权，不默认第一租户。
- Red：新增 `TestSessionMemberships*` 先实际返回404，缺失接口复现。Green：`go -C server test ./internal/httpapi -run 'TestSessionMemberships|TestHTTPPermissionMatrix|TestIdentity|TestTouchFollowUp' -count=1`通过，1.342s，覆盖无会话/无权/停用/多租户/伪造输入/代理授权撤销/权威故障以及原业务 tenant 门未放宽。
- Web原 BFF无需新特例；新增真实 loopback HTTP proxy 回归，保持 canonical cookie/专用 BFF token与无 tenant 请求，拒绝让自报 principal成为上游权威，200/401/503不改。`session-memberships-bff.test.ts + bff.test.ts` 27项通过；tsc通过；diff/cached --check通过。
- 合同 `docs/integration/session-memberships.md` 已提交。root 接 UI并统一最终全量及原浏览器复验；本补丁未冒称普通 SSO 浏览器已完成。
