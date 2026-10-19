# HUI-3553 queue-view 受信服务身份 — operator 配置与回滚（W5）

本文件面向部署/运维：如何启用"受信服务生产身份"向 touch-engine 同步 C0 `job_queue_view`。
代码面见 `server/internal/httpapi/server.go`（`requireServiceProducer`）与
`server/internal/httpapi/handlers_content_packs.go`（`handleContentHandoffQueueView`）。
**默认关闭**：不配置 registry 时，行为与历史版本完全一致（只有旧 session+manage 路径）。

## 1. 环境变量（写入 0600 root env 文件，禁止进 git/日志/回复）

| 变量 | 必填 | 说明 |
|---|---|---|
| `TOUCH_SERVICE_TOKENS` | 启用时必填 | 受信服务 → 该服务自己的 token：`modelxing-runtime:<token>;another-service:<token>`。**不得**复用 `TOUCH_INTERNAL_TOKEN`（BFF 共享密钥）。 |
| `TOUCH_SERVICE_ROLES` | 可选 | 期望角色：`modelxing-runtime:queue_producer`。已登记但未写条目的服务默认 `queue_producer`；写了非 `queue_producer` 的值则该服务被 403 拒绝。 |
| `TOUCH_INTERNAL_TOKEN` 等既有变量 | 旧路径 | 保持不变；旧 session/BFF 路径仍需要它们。 |

配置纪律：
- 文件权限 0600，owner=运行用户；只通过 systemd `EnvironmentFile`/`LoadCredential` 注入。
- token 值含空白/`;`/`:` 会破坏 parser；使用无空白的高熵串（建议 32+ 字符随机）。
- 坏项/重复 service id/给未登记服务配角色 → 进程 `Gate()` 记 problem，服务生产者路径 **整体 fail-closed**
  （不会"部分生效"）；此时旧路径照常工作。

## 2. 请求约定（服务 → touch-engine，loopback）

```http
POST /api/v1/content-handoffs/{handoffId}/queue-view
X-Service-ID: modelxing-runtime
X-Service-Token: <该服务自己的 token>
Content-Type: application/json

{ ...mx-ops-c0/v1 job_queue_view 文档（严格 22 字段/枚举/ETA 成对/0≠null/internal_debug 会被丢弃）... }
```

- 不需要 session cookie、不需要 `X-Tenant-ID`、不需要共享 internal token。
- tenant 由服务端从 `content_handoffs` 行反查（忽略任何 `X-Tenant-ID`）；handoff 不存在/已删 → 统一 404。
- 服务身份只能写 queue-view（`job_ref` + 严格校验后的 `job_queue_view` 公开投影），不能写其他字段。
- 响应与旧 session 路径完全同形：`{"handoff_id","job_ref","job_queue_view"}`。
- 同时带服务身份与 session 凭据时**服务路径优先**。

## 3. 状态码

| 情形 | 状态 |
|---|---|
| 正常写入 | 200 同形 JSON |
| 服务 token 缺失/错误/未登记（统一） | 401 `unauthorized` |
| 服务无 queue_producer 角色 | 403 `forbidden` |
| registry 未配置/配置坏项 | 503 `service_producer_unavailable`（fail-closed） |
| handoff 不存在 | 404 `not_found`（不泄露跨租户存在性） |
| queue_view 非法 | 422 `mx_ops_c0_invalid_queue_view` |
| body >64KiB / 不可读 | 400/422（与旧路径一致） |

## 4. 轮换

1. 生成新 token，更新该服务与 touch 两侧同一 0600 env（先加新、保留旧不可行——registry 每服务单 token）；
2. 用同一服务 id 覆盖 `TOUCH_SERVICE_TOKENS` 中该服务 token → 重启/重载 touch-engine 进程；
3. 更新调用方 token 后验证一条 queue-view 同步（200 + 行内 `job_ref` 更新）；
4. 从 env 清除旧值；若失败回滚见下。
- 审计日志 `mx_ops_queue_view_producer service=... handoff=... tenant=... job_ref=... result=ok|rejected` 可用于核对；
  **日志绝不含 token/body**。

## 5. 回滚

- 关闭新路径：从 env 删除 `TOUCH_SERVICE_TOKENS`（或整文件恢复上一版）→ 重启；
  旧 session 路径不变，服务请求 fail-closed 503（不会误写）。
- 代码回滚：W5 无 DB 迁移、无新表；回退代码后 `job_queue_view` 列数据仍在（只读），无数据迁移。
- 不要用 `TOUCH_INTERNAL_TOKEN` 顶替服务 token；不要把服务 token 写入只读共享文件或聊天。

## 6. 验收（本批 fixture；真实联调 UNKNOWN）

- 单测/HTTP e2e：`cd server && go test -count=1 ./internal/config/ ./internal/store/ ./internal/httpapi/ ./internal/mxopsview/`
  覆盖：旧路径回归、服务通过、伪造/缺失/未登记/角色拒绝、跨租户/未知 404、tenant 反查、非法合同/超限、审计。
- 真实 Runtime 推送 = HUI-3836 授权窗口（UNKNOWN）；真实队列语义/ETA 精度同日验。
