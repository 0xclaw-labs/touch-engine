# HUI-3553 W5 实现计划：touch queue-view 受信服务生产身份

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans（depth-1 无子代理）。步骤用 `- [ ]`。

**Goal:** 让受信服务（星模 Runtime）用 per-service token 走独立生产身份写入 `POST /api/v1/content-handoffs/{handoffId}/queue-view`；旧 session+manage 路径作为双 auth 过渡保持字节/状态码不变；tenant 从 handoff 行反查、fail-closed、审计无 secret。

**Architecture:** `requireServiceProducer` 中间件（`X-Service-ID`+`X-Service-Token` → registry 校验）替换路由上的 `requireSession`；无服务头时委托旧 `requireSession` 实现双 auth。handler 分叉：service 路径用新增 `GetContentHandoffByID` 反查 tenant 后复用既有严格 parse/公开投影/写入；session 路径原样。无 DB 迁移。

**Tech Stack:** Go 1.2x + chi + database/sql；仓内 `internal/mxopsview` 合同副本；测试 `httptest` + 既有 `contentPackFixture`。

**Spec:** `docs/superpowers/specs/2026-10-19-touch-trusted-identity-design.md`

## Global Constraints
- 只写本仓本 worktree（`.worktrees/mx-ops-touch-identity`）；不动 main、其他 worktree、他组仓。
- 不改公共 API/Runtime/合同副本语义；不新增 DB 迁移/表/服务/端口；不改旧路径权限。
- 未知一律 fail-closed；错误不泄露跨租户/服务是否存在；不复用 `TOUCH_INTERNAL_TOKEN` 作服务 token。
- 测试名英文、文档中文、commit `feat(mx-ops-3553): ...` / `test(...)` / `docs(...)`。

## Review Focus
1. 旧路径字节/状态码不变（internal token/session/tenant/manage）——回归测试 + 显式键集合断言。
2. 伪造服务身份：只带 `X-Service-ID` 或错 token/未登记 → 401/403；registry 空 → 503（fail-closed）。
3. 跨租户/未知 handoff → 404，不泄露存在性；伪造 `X-Tenant-ID` 无效。
4. 非法 queue_view / 超 64KiB → 422 / 400，不落库、错误码与旧路径一致。
5. 审计日志含 service/handoff/tenant/job_ref/result，绝不含 token/body。

---

### Task 1: config 新增 per-service registry（`server/internal/config/config.go`, `config_test.go`）

**Interfaces:**
- `type Config struct { ...; ServiceTokens map[string]string; ServiceRoles map[string]string }`
- `func parseServiceTokens(raw string) (map[string]string, error)`、`parseServiceRoles`
- Load: `TOUCH_SERVICE_TOKENS` / `TOUCH_SERVICE_ROLES`；坏项/重复 service id → 在 `Gate()` 增 problem（fail-closed），默认零值合法。
- 不与 `InternalToken`/现有 `Gate()` 平台集成要求耦合；不新增必需启动项。

- [ ] S1 失败测试：`TestLoadServiceTokens`（空→nil；`a:t1;b:t2`→map；空格/尾分号；重复→error；缺 token→error）；`TestServiceRegistryGateProblems`（坏项产生 problem；空 registry 无 problem）。
- [ ] S2 `cd server && go test ./internal/config/ -run 'TestLoadServiceTokens|TestServiceRegistryGateProblems' -v` → FAIL。
- [ ] S3 实现 parser + Load + Gate problem。
- [ ] S4 同上 PASS。
- [ ] S5 commit `feat(mx-ops-3553): TOUCH_SERVICE_TOKENS/ROLES 受信服务 registry（默认关闭）`。

### Task 2: store 按 ID 反查（`server/internal/store/store_content_packs.go`, 测试）

**Interfaces:**
- `func (s *Store) GetContentHandoffByID(handoffID string) (ContentHandoff, error)`；未知 → `ErrNotFound`；不加 tenant 过滤（调用方必须用返回行的 TenantID）。

- [ ] S1 失败测试：`TestGetContentHandoffByID`（存在返回行/tenant；未知 ErrNotFound；不会跨租户误命中——按 id 唯一即可）。
- [ ] S2 `cd server && go test ./internal/store/ -run TestGetContentHandoffByID -v` → FAIL。
- [ ] S3 实现（`SELECT handoffCols FROM content_handoffs WHERE id=?` + scanHandoff + ErrNotFound）。
- [ ] S4 PASS。
- [ ] S5 commit `feat(mx-ops-3553): store 按 handoff id 反查（服务生产者用）`。

### Task 3: 中间件 + 路由（`server/internal/httpapi/server.go`, 测试）

**Interfaces:**
- `const serviceIDHeader = "X-Service-ID"; const serviceTokenHeader = "X-Service-Token"`
- `type serviceProducer struct{ ServiceID string }` + `serviceProducerFrom(ctx) *serviceProducer`
- `func (s *Server) requireServiceProducer(next http.HandlerFunc) http.Handler`
- `func (s *Server) serviceProducerRole(serviceID string) string`（默认 `queue_producer`）
- 路由改：`mux.Handle("POST /api/v1/content-handoffs/{handoffId}/queue-view", s.requireServiceProducer(s.handleContentHandoffQueueView))`

- [ ] S1 失败测试：`TestRequireServiceProducerAuth`（无服务头→委托旧路径仍 401/可通场景见 Task 5；带服务头+错 token→401；未登记→401；registry 空+带服务头→503；无 queue_producer 角色→403；正确→handler 被调用（用 stub handler 200））；`TestServiceProducerFromContext`。
- [ ] S2 `cd server && go test ./internal/httpapi/ -run 'TestRequireServiceProducer' -v` → FAIL。
- [ ] S3 实现常量/类型/中间件/路由；`subtle.ConstantTimeCompare`；审计拒绝日志（无 token）。
- [ ] S4 PASS（旧测试仅路由签名变化，应仍绿）。
- [ ] S5 commit `feat(mx-ops-3553): queue-view 路由挂受信服务生产身份（双 auth）`。

### Task 4: handler 分叉 + 审计（`server/internal/httpapi/handlers_content_packs.go`, 测试）

**Interfaces:**
- `handleContentHandoffQueueView`：
  - `producer := serviceProducerFrom(r.Context())`
  - `if producer != nil { h, err := s.St.GetContentHandoffByID(id); if ErrNotFound → 404 not_found; 原 parse/PublicJSON 后 UpdateContentHandoffQueueView(h.TenantID, ...); log 审计; 返回同形 JSON }`
  - `else { 原 callerFrom/packTenantAction/UpdateContentHandoffQueueView(c.Member.TenantID,...) 原样 }`
- 响应 JSON 键：`handoff_id`、`job_ref`、`job_queue_view`（两条路径完全一致）。

- [ ] S1 失败测试（fixture 扩展：`newContentPackFixtureWithServiceTokens(t, tokens, roles)` 或复用 cfg 注入）：`TestQueueViewServiceProducerWritesRow`（无 session/tenant→200；行 tenant 正确；job_ref/public 投影；响应键集合==旧路径）、`TestQueueViewServiceProducerIgnoresTenantHeader`、`TestQueueViewServiceProducerUnknownHandoff404`、`TestQueueViewServiceProducerCrossTenant404`、审计缓冲断言（含 service/handoff/tenant/job_ref，不含 token/body）。
- [ ] S2 `cd server && go test ./internal/httpapi/ -run 'TestQueueViewServiceProducer' -v` → FAIL。
- [ ] S3 实现分叉+审计。
- [ ] S4 PASS。
- [ ] S5 commit `feat(mx-ops-3553): queue-view 服务生产者写入路径与审计`。

### Task 5: 回归/边界测试（`server/internal/httpapi/server_content_handoffs_test.go` 或新文件）

- [ ] S1 添加：旧 session 路径显式键集合/状态码不变；非法 queue_view 422（两条路径）；64KiB 超限 400；未配置 registry 时旧路径全绿、服务头 503。
- [ ] S2 `cd server && go test ./internal/httpapi/ -run 'TestContentHandoff|TestQueueView' -v` → PASS（含既有 6 例）。
- [ ] S3 全量门禁：`go build ./...`；`go vet ./internal/config/ ./internal/store/ ./internal/httpapi/`；`go test -count=1 ./internal/config/ ./internal/store/ ./internal/httpapi/ ./internal/mxopsview/`。
- [ ] S4 commit `test(mx-ops-3553): 双 auth/跨租户/合同/大小边界回归`。

### Task 6: 文档 + handoff + review + PR

- [ ] S1 更新 `docs/mx-ops/3553/handoff.md`：偏差 2 状态改"已收口（双 auth，默认旧行为不变；新服务身份路径 ready，真实 Runtime 接线 UNKNOWN）"；补测试实际输出、UNKNOWN、`TOUCH_SERVICE_TOKENS/ROLES`/header 约定与 0600 env 纪律。
- [ ] S2 code-review 自审（Standards+Spec，depth-1 记录）；`git push -u origin mx-ops/3553-trusted-identity`；`gh pr create --base main`（body 含票号/合同 `mx-ops-c0/v1 @ 6ffc90ca`/无迁移/测试实际输出/双 auth 说明/UNKNOWN）。
- [ ] S3 报 Lead：PR URL + head SHA + 测试输出 + UNKNOWN。

## Self-Review
- 覆盖：G1 Task1+3；G2 Task3+5；G3 Task2+4；G4 无迁移、Task4 复用严格 parse；G5 Task4；G6 Task1–5。
- 类型一致：`ServiceTokens/ServiceRoles`（Task1）→ 中间件（Task3）→ handler 分叉（Task4）；`GetContentHandoffByID`（Task2）→ Task4；`serviceProducer`/context key 在 Task3 定义、Task4 消费。
- Review Focus 5 条各有 Task：1→5、2→3、3→4、4→5、5→4。
