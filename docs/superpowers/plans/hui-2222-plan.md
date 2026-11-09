# HUI-2222 正式商家入口与统一登录 · Implementation Plan（wave2 2026-11-10）

**Goal:** 正式商家入口的受控回跳闭环：return_url 白名单（防开放重定向）+ 登录后回原页/上下文保持 + 平台身份复用免重复登录（refresh 单飞重试）；SSO 跨应用跳转（M11）凭据短时单用途（复用 3546 凭据纪律）；B15 公开域名/媒体域名健康检查端点（工程面）；loopback 仓库历史说明实查入报告。

**Architecture:**
- 回跳白名单收口 `web/src/lib/login-return.ts`（复用 HUI-1664 `safe-redirect.ts` 的站内路径红线），MerchantGate 登录成功（含组织选择后）消费合法回跳目标 `router.replace`。
- 免重复登录：`web/src/lib/session-continuity.ts` 单飞 refresh（401 → POST /api/auth/refresh 一次 → 重试 memberships），merchant-session 消费；identity 权威与既有 session 机制零改动。
- SSO 跳转两阶段：Go `POST /api/v1/eco-nav/switch` 不再直回 launch_url，改 mint 短时单用途跳转凭据（`jtg_` 高熵随机+校验位，SHA-256 哈希入库，TTL 60s，绑定 principal+tenant）；`POST /api/v1/eco-nav/switch/redeem` 事务条件更新单次消费后回 launch_url。shell launch 仍是唯一 mint 权威（public-ai），本票只给凭据交接环节上纪律。新域包 `server/internal/appjump`。
- B15：`GET /api/v1/ops/domain-health`（requirePrincipal），只探服务端配置声明的公开域名（PUBLIC_BASE_URL）与媒体域名（PUBLISH_HANDOFF_MEDIA_BASE_URL 绝对 http(s) 时），请求输入零影响；诚实态 not_configured/invalid_config/reachable/unreachable。
- 不新建路由（page-map 先例决策「不造 /login」+ 2628 独占 page 面；正式入口=既有门闸统一登录页 SharedLogin）。

**Tech Stack:** Go 标准库（`cd server && GOWORK=off go vet ./... && go test ./...`）；Next.js 15（`npx vitest run`、`npm run build`）；Playwright（`bash web/e2e/run.sh 11-controlled-entry` partial + `14-eco-nav-switch`）。

**Spec:** 裁定书 `/Users/jimfu/works/派诺生态/touch-all-20261109/briefs/hui-2222.md`（预裁定=答案基线）；迁移号段 0105-0107（0107 留空：B15 探针无状态，不为用满号段造表）；端口 web 18355 / go 18255。

## Global Constraints

- 禁碰：`/c/**`（1896）、`/work/rewards`（1671）、`/work/materials`（3553）、`/t/**`（1665）、`web/vendor`（只读）、`web/src/lib/eco-nav/model|load|allowlist|registry`（既有 nav 系统只窄改 switch 消费面）、page-map/page-census（2628，不新增路由即不动）、中央 layout 样式（2628）、globals.css。
- 公共装配窄补丁允许：server.go 新增 `Jumps *appjump.Service` 字段+一行接线+两行路由注册。
- 生产路径不伪造：shell launch 拒绝/过期/已用一律诚实拒绝；健康探针不编造 ok；refresh 失败落匿名登录不假装已登录。
- 不保存 secret；凭据明文只在 mint 响应出现一次，永不落库/进日志。

---

### Task 1: 迁移 0105 sso_jump_grants + 0106 sso_jump_events

**Files:** `server/internal/db/migrations/0105_sso_jump_grants.sql`、`server/internal/db/migrations/0106_sso_jump_events.sql`

- [ ] 红：`go test ./internal/db/` 迁移清单断言（若 db_test 校验表清单则同步）；两张表不存在时 appjump 测试建库即失败。
- [ ] 绿：0105 grants 表（id `jgr_` PK、tenant_id FK tenants、principal_id、app_id、launch_target_id、launch_url、token_hash UNIQUE、expires_at、used_at DEFAULT ''、revoked_at DEFAULT ''、created_at；expires_at 索引）。0106 events 表（追加式审计，kind CHECK IN ('minted','redeemed','rejected','revoked')，source+event_id UNIQUE 去重，无外键，不含凭据明文）。
- [ ] 提交 `feat(hui-2222): sso jump grants + events migrations (0105-0106)`。

### Task 2: server/internal/appjump 域包（3546 凭据纪律）

**Files:** 新 `server/internal/appjump/appjump.go`、`server/internal/appjump/token.go`、`server/internal/appjump/appjump_test.go`

- [ ] 红：token 纪律（`jtg_` 前缀、128-bit+校验位、ValidTokenShape、HashToken=SHA-256 hex）；Mint(tenant, principal, app, target, launchURL) → {Grant, Token}（明文一次）；Redeem(token, principal, tenant) → launchURL：形态→哈希查→过期(ErrExpired)→已用(ErrUsed)→撤销(ErrRevoked)→principal/tenant 不匹配(ErrNotFound 同形防枚举)→事务条件 UPDATE 单次消费→事件 redeemed；撤销 Revoke；CleanupJumpGrants 清过期+已用超 grace 行。
- [ ] 绿：实现 + 并发 Redeem 恰一方成功（goroutine 竞态测试）。
- [ ] 提交 `feat(hui-2222): appjump short-lived single-use jump credentials`。

### Task 3: Go 两阶段跳转 handler

**Files:** `server/internal/httpapi/handlers_eco_nav.go`、新 `server/internal/httpapi/server_eco_nav_jump_test.go`、`server/internal/httpapi/server.go`（窄补丁：Jumps 字段+接线+redeem 路由注册）

- [ ] 红：switch 200 响应含 jump_token/expires_at/grant_id 且**不含** launch_url；shell 403/429/503 映射不变；Jumps nil → 503 fail-closed（诚实关闸）。redeem：合法 token → {launch_url, app_id, launch_target_id}；同 token 二次 → 404 单用途；过期 → 410；错形态/未知 → 404 同形；跨 principal/跨 tenant（multi-org principal 场景，参照 4593 e2e multi 先例）→ 404；DisallowUnknownFields；未登录 401。
- [ ] 绿：实现。safeLaunchURL 校验保留在 mint 侧（launch_url 入库前先验 https 非环回）。
- [ ] 提交 `feat(hui-2222): two-phase eco-nav switch with single-use jump grant redemption`。

### Task 4: B15 domain-health 端点

**Files:** 新 `server/internal/httpapi/handlers_domain_health.go`、`server/internal/httpapi/server_domain_health_test.go`、`server/internal/httpapi/server.go`（一行路由注册）

- [ ] 红：GET /api/v1/ops/domain-health（requirePrincipal）；未配置 → targets[].configured=false（不伪造 ok）；配置非绝对 http(s) URL → invalid_config+原因；httptest 上游可达 → reachable+http_status+latency_ms+checked_at；上游拒绝/超时 → unreachable+错误类；上游 5xx → unreachable（http_status 如实带）；探针目标仅来自服务端配置（请求输入零影响）。
- [ ] 绿：PublicBaseURL（公开 HTTPS 域名）+ PublishHandoffMediaBaseURL 绝对 URL（媒体域名）两目标；5s 超时。
- [ ] 提交 `feat(hui-2222): ops domain health endpoint (public + media domains)`。

### Task 5: web 回跳白名单 lib

**Files:** 新 `web/src/lib/login-return.ts`、`web/tests/login-return.test.ts`

- [ ] 红：`loginReturnTarget(get)` = REDIRECT_KEYS(next/redirect/return) 首个非空 → sanitizeInSitePath → 额外拒绝 `/api` 前缀与 `/api` 全等；合法（`/work/stores`、`/work/campaigns/abc-1`、`/`）通过；外域/协议相对(`//`)/反斜杠/百分号编码/大写/查询串全拒；空→null。门店(store)/订单(order)/运营上下文参数不可经 return 传递（白名单只认路径，查询串整体拒绝）。
- [ ] 绿 + 提交 `feat(hui-2222): login return target whitelist resolver`。

### Task 6: merchant-session 受控回跳 + refresh 免重复登录

**Files:** `web/src/components/work/merchant-session.tsx`、新 `web/src/lib/session-continuity.ts`、`web/tests/session-continuity.test.ts`、`web/tests/product-pages.test.ts`（增量断言，若存在门闸登录断言）

- [ ] 红：session-continuity `createScopeReader(fetchImpl)`：readScopes 401 → 单飞 POST /api/auth/refresh 一次 → 重试 memberships；refresh 也 401 → null（匿名）；网络错 → throw（error 态，不当已登录）；并发多次 401 只发一次 refresh（single-flight）。merchant-session：ready 且挂合法 `?return=` → `router.replace` 回目标一次（useRef 防重复）；无 return 行为逐字节不变；refresh 失败落匿名登录不假装已登录。
- [ ] 绿：实现。
- [ ] 提交 `feat(hui-2222): controlled login return + single-flight session refresh`。

### Task 7: web 两阶段 switch + BFF redeem

**Files:** `web/src/components/eco-nav/use-app-switch.ts`、`web/src/app/api/eco-nav/switch/route.ts`、新 `web/src/app/api/eco-nav/switch/redeem/route.ts`、`web/tests/eco-nav/switch.test.ts`（更新+redeem 用例）

- [ ] 红：switch 路由透传并返回 {jump_token,...}（无 launch_url 断言）；redeem 路由转发 {jump_token} → validateLaunchUrl(launch_target_id, launch_url) 过才回浏览器，形 mismatch 502；upstream 404/410 诚实映射；use-app-switch switchApp = mint → redeem → {ok, launchUrl}，redeem 失败给产品文案不导航。
- [ ] 绿：实现。allowlist/validateLaunchUrl 逻辑本身零改动（只新增消费方）。
- [ ] 提交 `feat(hui-2222): two-phase app switch client + redeem BFF`。

### Task 8: e2e 受控链增补 + 两阶段回归

**Files:** `web/e2e/specs/11-controlled-entry.spec.ts`（增补）、`web/e2e/specs/14-eco-nav-switch.spec.ts`（两阶段改写）

- [ ] 11 增补：①未登录直达 `/work/stores` → 门闸登录 → 回原页（kicker=租户名、URL 不变）；②`/?return=/work/campaigns` 登录 → 落 campaigns；③`/?return=https://evil.example`、`//evil.example`、`%2F%2Fevil` 登录 → 不离站（落 `/`）；④免重复登录：登录后只清 touch_session 留 refresh cookie → 访问 `/` 直达今日台不见登录表单；⑤两阶段跳转：switch 返回 jump_token 无 launch_url，redeem 一次成功、二次 404、跨组织 404、错形态 404。
- [ ] 14 改写：既有 validated launch_url 断言迁到 redeem 响应；forged payload 拒绝断言保持。
- [ ] 提交 `test(hui-2222): controlled return + refresh + two-phase jump e2e`。

### Task 9: 全量门禁 + 证据

- [ ] `cd server && GOWORK=off go vet ./... && go test ./...` 全绿（基线红已清零，无已知红豁免）。
- [ ] `cd web && npx vitest run` 全绿；`npm run build` 过。
- [ ] 端口 18355/18255（本票分配）：`lsof` 检查后 `E2E_GO_PORT=18255 E2E_WEB_PORT=18355 E2E_IDENTITY_PORT=18465 bash web/e2e/run.sh 11-controlled-entry`（partial）+ `14-eco-nav-switch`；截图落 evidence。
- [ ] 提交 `docs(hui-2222): evidence`。

### Task 10: 双轴自审 + loopback 实查 + 报告

- [ ] Standards 轴：登记制注释纪律、无 inline style、uifinish/page-census 不破、凭据明文不落库/不进日志。
- [ ] Spec 轴：裁定书四项逐条核对（回跳白名单/免重复登录/SSO 凭据纪律/B15 端点）+ T01/T17/T19 登录环节走查面说明。
- [ ] loopback 实查：config.go HTTPAddr 默认 127.0.0.1:18240 + http.go 生产纪律注释 + deploy env 示例 + extrajump.OfficialAddress 公共跳转拒环回 —— 现状写进报告（「必须由操作者经 TOUCH_HTTP_ADDR 强制，进程不强制拒绝非环回绑定」如实陈述）。
- [ ] 报告 `/Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-2222-report.md`（验证分级口径：Engineering VERIFIED / Browser VERIFIED(partial) / Production NOT_VERIFIED）；分支不 push 不 merge。
