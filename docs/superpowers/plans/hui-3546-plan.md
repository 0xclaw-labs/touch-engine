# HUI-3546 实施计划｜匿名动作会话与跨端恢复（2026-11-09 增量轮）

| 字段 | 值 |
| --- | --- |
| 日期 | 2026-11-10 |
| 票面 | HUI-3546（2026-11-09 修订版，B08） |
| 裁定书 | /Users/jimfu/works/派诺生态/touch-all-20261109/briefs/hui-3546.md |
| worktree | /Users/jimfu/works/派诺生态/wt-touch-3546，分支 touch/hui-3546-action-sessions，基线 161fadc |
| 迁移号段 | 0056-0059（本轮使用 0056-0058，0059 留空不占） |
| 硬门禁 | `cd server && go test ./...`（已知基线红 7 个 httpapi 测试不属本域，不修）；`cd web && npx vitest run` |
| 纪律 | TDD 逐任务红→绿→commit；不 push 不 merge；唯一写面 = participation 包 + 对应 httpapi handler/装配窄补丁 + web lib/participation |

## 范围（裁定书为准）

- action_sessions 是匿名权威：`POST /api/v1/public/actions/{actionId}/sessions` 显式动作创建或同浏览器恢复；不建生态账号、不留手机号；浏览 GET 不自动创建。
- 快照冻结（创建时）：storefront_revision + action_revision + rule_version/rule 快照 + 内容引用；停用/撤权是独立实时检查（快照不掩盖当前状态）。
- 恢复三层：① 同浏览器 cookie 自动恢复；② 跨浏览器 `POST /api/v1/public/resume/exchange` 短时单用途凭据换绑；③ 全部凭据丢失不可认回（无指纹/手机号后门）。
- 店员码（staff_verify）与顾客凭据（customer_resume）分权：店员码不能兑换顾客会话。
- 只有显式办理权益才升级建 participation（既有语义不变），并冻结规则、调用既有 BenefitReserver 受信端口。
- 旧 /c/{code} 参与链路零改动；券查询端点归 1671，本票只保证恢复路径可携带会话上下文。

## 关键设计裁定（依裁定书 + 仓内既有模式）

1. **动作解析端口**：`participation.ActionDirectory` 接口（ResolveAction → 已发布动作引用）。动作目录表是 HUI-4584 写面，本票不建第二份目录；生产接线（SetActionDirectory 一行窄补丁）归 4584。目录未接线时会话面 fail-closed 503 `action_directory_unavailable`（同 BenefitReserver nil → pending 的仓内模式）。测试夹具只进测试。
2. **开关**：复用 FEATURE_PARTICIPATION（本票参与域），不动共享 config 文件；off = 路由不注册 + handler gate 统一 404 双保险。
3. **凭据格式**：复用 ptx_ 令牌纪律（128-bit 随机 + 校验位 + SHA-256 哈希入库，明文一次即焚，永不进 URL 查询参数/日志）；TTL 默认 10 分钟、硬上限 15 分钟；单用途事务置位防重放。
4. **恢复凭据独立表**（action_session_credentials，purpose CHECK 只允许 customer_resume）→ 结构性保证店员码/参与交接码进不了会话兑换面。
5. **访客键**：复用 touch_participation_visitor 首方 cookie（同浏览器 = 同 visitor，参与与会话可互认）；会话行访客绑定是唯一访问凭据，裸 ID/顺序 ID 探测一律统一 404。
6. **升级 participation**：显式 + 服务器实时解析动作（须有真实 campaign + 窗口有效 + 服务器权威 rule_version）；客户端 rule_version 只是期望值；无活动动作拒绝升级（campaign_required），绝不伪造活动 ID。
7. **实时检查语义**：status 旧会话/已停用动作仍可读（旧权益保护），但 resume_available=false；签发/兑换/换选/升级在动作停用或未发布时拒绝（action_unavailable）。

## 任务（每任务先红后绿，独立 commit）

### T1 迁移 0056-0058
- `0056_action_sessions.sql`：action_sessions（tenant/store FK、action_id、catalog_code/platform/kind、冻结 storefront_revision/action_revision/rule_version/rule_snapshot/content_pack_version、visitor_id、participation_id 可空、status CHECK active|ended、idempotency UNIQUE(tenant,action,key)）。
- `0057_action_session_credentials.sql`：purpose CHECK 仅 customer_resume、token_hash UNIQUE、used_at/revoked_at。
- `0058_action_session_events.sql`：会话审计事件（source+event_id UNIQUE 去重、action/revision/引用字段），无 FK（审计不随行删除）。
- 测试：包内测试宿主建库 → 三表存在、CHECK 拒非法状态/用途、UNIQUE 拒重复哈希与同源事件。

### T2 领域：端口 + 快照 + 创建/恢复（session.go）
- ActionRef / ActionDirectory 端口；SessionStatus（active|ended）；哨兵错误（ErrActionUnavailable/ErrStaleVersion/ErrSessionEnded/ErrCampaignRequired/ErrDirectoryUnavailable）。
- StartActionSession：目录解析（未知→统一 404；停用→action_unavailable）；期望 revision 只是期望值（不符→stale_version）；幂等键同 visitor 重放回原行、异 visitor 冲突；同浏览器（tenant+action+visitor）恢复最近 active 会话；无活动/活动结束照建（不伪造 campaign）；创建时冻结快照；事件 created/resumed。
- UpdateActionSessionContent：active 且动作实时可用才可换选；事件 content_changed。
- 测试：快照冻结值、恢复同版本、stale 拒绝、幂等矩阵、停用拒绝、无活动合法。

### T3 领域：恢复凭据
- IssueResumeCredential（visitor 绑定 + active + 实时可用；purpose 固定 customer_resume；TTL ≤15min）；RevokeResumeCredential（未用未撤销才可撤）。
- ExchangeResumeCredential：形状→哈希→目标→过期→已用→撤销→会话 active→实时可用→事务性单次消费→换绑 visitor；任何拒绝不烧单用途；并发只有一方成功。
- 测试：签发/兑换/过期/重放拒绝/撤销后拒/店员码与参与凭据进不了会话兑换/并发单胜。

### T4 领域：status / 升级 / 取消 / 清理
- ActionSessionStatusView：快照字段 + action_available/resume_available 实时 + 关联 participation 摘要（status+benefit_reservation_state，无访客键/幂等键/预留引用）；已停用仍可读。
- AttachParticipation：显式升级；无活动→campaign_required；窗口过期→既有 ErrFrozen；rule_version 用服务器实时值；复用 Start()（ BenefitReserver nil → not_requested 不伪报）；幂等重复升级回同一参与。
- CancelActionSession（active→ended + 撤销未用凭据）；CleanupSessions（过期凭据过宽限删除；ended 会话过保留窗匿名 visitor）。
- 测试：status 白名单与实时位、升级链路四态、幂等升级、取消后凭据全废、清理。

### T5 httpapi 面 + 装配窄补丁
- handlers_action_sessions.go：8 条路由（见范围）；off 双保险 404；目录未接线 503；载荷白名单（token 只在创建响应一次）。
- server.go 窄补丁：Server 无新共享文件改动，仅注册块新增行（FEATURE_PARTICIPATION 块内）。
- 错误映射：新哨兵 → 404/409/410/422/503，未知坍缩 500。
- 测试：off=404；create/restore/status 正例；负例矩阵（未知动作、stale、无 cookie、错 visitor、顺序 ID 探测、过期/重放凭据、店员码兑换会话、目录未接线 503）。

### T6 重启持久化验收（httpapi 集成）
- 真实 SQLite 文件库：create→issue→关库→重开→exchange 成功；已消费凭据保持已消费；冻结快照不变；ended 会话不可兑换。
- 测试红→绿。

### T7 web lib/participation 扩展 + vitest
- ActionSessionView/ResumeCredentialView 类型、resumeAvailable/status 判定、文案纪律（复用 benefitCopy，pending/failed 不出「已保留」）。
- 测试进 tests/participation 域测试文件；不触公共页面布局（1896 写面）。

### T8 自审 + 全量验证 + 报告
- 自我 code-review：spec 符合性（票面正反例逐条）+ 仓内纪律（fail-closed、白名单、双保险、无 inline style、注释纪律）。
- `cd server && go test ./...` + `go vet ./...`；`cd web && npx vitest run`。
- 报告 /Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-3546-report.md。

## 明确不做
- 不建 store_actions/动作目录表（4584 写面）；不改 1896 公共页布局；不改 1670 交接状态机；不动 1671 奖励账；不改旧 /c 与 /t 链路；不新增 config 开关键。
