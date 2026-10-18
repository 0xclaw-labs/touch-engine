# HUI-3553 W5：touch queue-view 受信服务生产身份（设计）

日期：2026-10-19
票 / 组：HUI-3553（G08 碰一碰，本次为收口项 W5「touch queue-view 受信服务身份」）
仓 / 分支：`0xclaw-labs/touch-engine` / `mx-ops/3553-trusted-identity`（base `origin/main ea11bd3`，含已合并 #65）
工作量：M（最小改动；无跨仓 wire、无 DB 迁移）

## 1. 意图

`#65` 的 queue-view 同步接缝（`POST /api/v1/content-handoffs/{handoffId}/queue-view`）目前挂在
`requireSession`（共享 `X-Internal-Token` + session cookie + `X-Tenant-ID` + `manage_content_packs`），
只能由管理员浏览器/BFF 会话写入。但合同语义上该投影的合法生产者是**星模 Runtime/受信服务身份**。
W5 目标：让受信服务用**自己的 per-service token** 生产队列投影，无需用户 session/租户头；
同时保留旧 session 路径（双 auth 过渡，默认行为不变），并保持 fail-closed 与不泄露跨租户存在性。

依据：`_g00/plans/research-E-p2-backlog.md` §8；`touch-engine/docs/mx-ops/3553/handoff.md`
已知偏差 2（"同步接缝当前走 session+manage，真实接线应改受信服务身份"）。

## 2. 目标 / 非目标

### 目标
- G1 新中间件 `requireServiceProducer`：校验 `X-Service-ID` + 该服务自己的 token（独立 registry，
  不得复用 `TOUCH_INTERNAL_TOKEN`）；不要求 session/`X-Tenant-ID`；registry 未配置时服务请求 fail-closed。
- G2 **双 auth 过渡**：同一路由，请求带 `X-Service-ID` → 服务生产身份路径；不带 → 走旧
  `requireSession`（含 internal token/session/tenant/manage）**字节与状态码不变**。默认（未配置 registry）行为与现 main 完全一致。
- G3 tenant 从 handoff 行反查（store 新增按 ID 查询），服务伪造 `X-Tenant-ID` 无效；
  handoff 不存在/跨租户一律 404（不泄露存在性）。
- G4 写入面不变：仍 `mxopsview.Parse` 严格校验 + `PublicJSON()` 只存公开投影 + 64KiB 上限 +
  422 `mx_ops_c0_invalid_queue_view`；不新增 DB 迁移/表/字段。
- G5 审计：结构化日志记录 `producer_service`/`handoff_id`/`tenant_id`/`job_ref`/结果；
  **不记录 token/secret**、不记录 body。
- G6 测试：旧 session 路径回归（字节/状态码）、新 service 路径通过、缺失/错误 token/未注册服务拒绝、
  伪造 X-Tenant-ID 无效、跨租户/未知 handoff 404、非法 queue_view 422/超限、审计字段、tenant 反查。

### 非目标 / 反目标
- 不改公共 API / Runtime / 其他仓；不建新服务/端口/DB/表/迁移；不改 `mxopsview` 合同副本语义。
- 不放宽旧路径权限（旧路径仍需 internal token+session+manage）；不让服务身份写 queue-view 以外字段。
- 不做真实 Runtime 联调（UNKNOWN，留 HUI-3836）；不轮换/存储生产 token。
- 反目标：用 `X-Service-ID` 单独授权（必须绑定 token）；把共享 internal token 当服务 token；
  服务可指定 tenant；错误信息泄露 handoff 是否存在/跨租户；registry 为空时放行。

## 3. 约束与现状事实

| 事实 | 证据 |
|---|---|
| 路由当前 `s.requireSession(s.handleContentHandoffQueueView)` | `server/internal/httpapi/server.go:347` |
| `requireSession` = `requireIdentity(next,true)`：内部 token → config gate → session → tenant member | `server.go:695-760` |
| handler 用 `callerFrom(r)` + `packTenantAction(manage_content_packs)`；随后 parse/写库 | `handlers_content_packs.go:514-522` |
| store 更新 `WHERE tenant_id=? AND id=?`，0 行 → `ErrNotFound` | `store_content_packs.go:792-806` |
| 配置从 `config.Load(get)` 读取（测试可注入），`Gate()` 校验平台集成 | `server/internal/config/config.go` |
| 测试有 `contentPackFixture`（真 server+sqlite+fake identity），owner/do 请求助手 | `server/internal/httpapi/server_content_packs_test.go` |
| queue-view 合同测试向量 | `server/internal/mxopsview/testdata/job-queue-view.waiting.json` |

## 4. 设计

### 4.1 配置（`server/internal/config/config.go`）
新增（默认零值 = 关闭新路径，不影响 `Gate()`）：
```go
// ServiceTokens maps trusted service id -> its own bearer token.
// Parsed from TOUCH_SERVICE_TOKENS="svc-id:token;svc2:token2".
// Empty means the service-producer path is unavailable (fail-closed).
ServiceTokens map[string]string
// ServiceRoles optionally narrows roles: TOUCH_SERVICE_ROLES="svc-id:queue_producer;...".
// Missing entry defaults to queue_producer for registered services.
ServiceRoles map[string]string
```
`Load` 用小型 parser（trim、跳过空项、重复 service id 保留首个/或拒绝，选择**拒绝**并在
`Gate()` 增 problem 以防配置误写；注意 `Gate` 只影响需要平台集成的旧路径，服务路径独立）。
token 只存内存，绝不打印；`String()`/日志不展开。

### 4.2 中间件（`server/internal/httpapi/server.go`）
```go
const serviceIDHeader = "X-Service-ID"; const serviceTokenHeader = "X-Service-Token"
type serviceProducer struct{ ServiceID string }
func (s *Server) requireServiceProducer(next http.HandlerFunc) http.Handler {
  return http.HandlerFunc(func(w, r) {
    serviceID := strings.TrimSpace(r.Header.Get(serviceIDHeader))
    if serviceID == "" { s.requireSession(next)(w,r); return } // 双 auth：无服务头 = 旧路径
    tokens := s.Cfg.ServiceTokens
    if len(tokens) == 0 { fail 503 service_producer_unavailable } // fail-closed
    want, ok := tokens[serviceID]; if !ok { fail 401 unauthorized }
    got := strings.TrimSpace(r.Header.Get(serviceTokenHeader))
    if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 { fail 401 unauthorized }
    if !s.serviceHasRole(serviceID, "queue_producer") { fail 403 forbidden }
    ctx := context.WithValue(r.Context(), serviceProducerKey, &serviceProducer{ServiceID: serviceID})
    next(w, r.WithContext(ctx))
  })
}
```
不要求 `X-Internal-Token`/session/tenant；不改 `requireSession`/`requireIdentity`。

### 4.3 handler（`handlers_content_packs.go` `handleContentHandoffQueueView`）
- 读出、parse、PublicJSON 保持现状（顺序不变：先 parse 再决定写哪条租户路径——注意旧路径本来
  先鉴权再 parse；服务路径鉴权已在中间件完成，因此 parse 提前不影响旧路径状态码，因为旧路径
  在进入 handler 前已鉴权。**为保持旧路径字节/状态码完全不变**：handler 先判断 producer：
  - producer != nil：`h, err := s.St.GetContentHandoffByID(handoffID)` → 不存在 404；
    `UpdateContentHandoffQueueView(h.TenantID, handoffID, view.JobRef, public)`；审计日志；
    返回与旧路径相同的 JSON（`handoff_id/job_ref/job_queue_view`）。
  - producer == nil：原逻辑原样（`callerFrom`/`packTenantAction`/`c.Member.TenantID`）。
- 服务路径忽略 `X-Tenant-ID`（不读）；错误映射沿用 `packStoreFail`（404/500 不泄露）。
- 422/64KiB 逻辑两条路径共用（保持现状）。

### 4.4 store（`store_content_packs.go`）
```go
// GetContentHandoffByID returns one handoff WITHOUT tenant scoping; callers must
// be trusted producers and always use the returned row's TenantID. Unknown -> ErrNotFound.
func (s *Store) GetContentHandoffByID(handoffID string) (ContentHandoff, error)
```
不新增写接口：复用 `UpdateContentHandoffQueueView(row.TenantID, id, ...)`。

### 4.5 审计
`Server.Log.Printf("mx_ops_queue_view_producer service=%s handoff=%s tenant=%s job_ref=%s result=ok")`；
拒绝在中间件记录 `result=rejected reason=<code>`（不打印 token/body）。不新增 DB 事件/表。

## 5. 兼容与失败姿态

| 情形 | 行为 |
|---|---|
| 未配置 registry（默认） | 不带 `X-Service-ID` 的旧调用完全不变；带服务头 → 503 `service_producer_unavailable` |
| 旧 session 调用（internal token+session+tenant+manage） | 与现 main 相同的状态码/响应字节 |
| 服务 token 缺失/错误/服务未登记 | 401（统一，不区分存在性） |
| 服务无 `queue_producer` 角色 | 403（默认登记即具备） |
| handoff 不存在/已删 | 404（不泄露跨租户） |
| 非法 queue_view / 超 64KiB | 422 / 400（与旧路径一致） |
| store/DB 故障 | 503/500 by 现有 `packStoreFail`（不假装成功） |

## 6. 测试策略（TDD）

1. `config`：`TOUCH_SERVICE_TOKENS`/`ROLES` 解析（正常/空/重复/坏项 fail-closed problem），默认零值。
2. `store`：`GetContentHandoffByID` found/unknown；跨租户仅通过行 tenant 写。
3. `httpapi`（沿用 `contentPackFixture` + 新增 service 配置注入/请求助手）：
   - `TestQueueViewServiceProducerWritesRow`：`X-Service-ID`+token，无 session/tenant → 200，行被写，
     job_ref/公开投影正确；响应与旧路径 JSON 同形（逐键相等）。
   - `TestQueueViewServiceProducerRejectsAuth`：缺失/错误 token、未登记服务 → 401；registry 空 → 503。
   - `TestQueueViewServiceProducerCrossTenant404`：tenB handoff + 服务写 → 404（无行/无泄露）。
   - `TestQueueViewServiceProducerIgnoresTenantHeader`：带伪造 `X-Tenant-ID` 不影响反查 tenant。
   - `TestQueueViewServiceProducerRejectsBadContract`：422 / >64KiB 400，不落库。
   - `TestQueueViewLegacySessionPathUnchanged`：既有测试全绿 + 显式断言旧响应键集合/状态码。
   - 审计：`log.New(bytes.Buffer,...)` 捕获含 service/handoff/tenant/job_ref/result，不含 token。
4. 回归：`go build ./...`、`go vet ./...`、`go test ./internal/httpapi/ ./internal/store/ ./internal/mxopsview/`；
   web 无改动（不跑 web 全量，handoff 记 N/A）。

## 7. 风险与回滚

- 风险：service token 泄露 = 可写任意已知 handoff 的 queue-view（只限该字段）。缓解：per-service token、
  只允许 queue-view、审计、registry 空 fail-closed、不与其他写面共用。
- 回滚：删除 registry 环境变量即新路径不可用，旧行为不变；无迁移/数据回滚。
- 真实运行/生产 = UNKNOWN（未授权窗口）。

## 8. Lead-delegated default（可被 Lead 覆盖）

- 头名 `X-Service-ID` + `X-Service-Token`；env `TOUCH_SERVICE_TOKENS`（`svc:token;...`）、
  `TOUCH_SERVICE_ROLES`（可选，缺省 `queue_producer`）。
- 双 auth 由"是否带 `X-Service-ID`"自动分流；不加额外 feature flag（registry 空即关闭）。
- 服务身份可写任意已知 handoffID；tenant 只从行反查，不读 `X-Tenant-ID`。
- 审计仅结构化日志，不新增 DB 迁移/表。
- 两条路径响应 JSON 完全一致。
