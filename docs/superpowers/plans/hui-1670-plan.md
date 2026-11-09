# 计划｜HUI-1670 统一发布交接 touch-handoff/v2（Wave2）

- worktree: /Users/jimfu/works/派诺生态/wt-touch-1670 ｜ 分支: touch/hui-1670-handoff-v2 ｜ 基线: ce47dde
- 需求权威: Linear HUI-1670（本轮 Linear API 持续不可用，以 v1 票面设计档 docs/superpowers/specs/2026-10-14-hui-1670-design.md + 裁定书 briefs/hui-1670.md + 3535 合同 §6 为授权链，报告中声明）
- 唯一写面: **publishhandoff 包扩展 v2**（server/internal/publishhandoff/**）、httpapi 交接 handler（新增 handlers_action_handoffs.go + 既有 handlers_publish_handoff.go 窄改）、迁移 0073-0078 号段（实际用 0073）、config.go 窄补丁（新增两个键）、渠道交接契约文档
- 波及: 4585/4586/4587/4588 四平台票按本票 Adapter 接口/plan.kind/事件类型命名写 adapter（合同见 docs/contracts/publish-handoff/v2/handoff-contract.md）

## 冻结契约（对 4585-4588 的承诺）

### 1. plan.kind（v2 五值，字符串常量在 publishhandoff 包）

| plan.kind | 语义 | 来源 |
|---|---|---|
| `refused` | 准入拒绝（not_admitted/紧急停用/渠道关闭/过期/错绑定/奖励未授权）：无入口、无指引、零落行 | JudgeActionRoute `!Allowed && !PendingExternal` |
| `pending_external` | 资格未知（unknown/无核查窗）：**不承诺、不显示可完成态、无手工指引**（§6 指令②：替换旧 admission_unknown→manual_only 映射） | `d.PendingExternal` |
| `manual_only` | admitted 且 method=manual_material：合规导出+手动指引，管道照常 | 允许 ∧ rec.Method=manual_material |
| `official_handoff` | admitted 且 method ∈ {h5_share, miniapp, js_media_handoff, native_sdk, link_share, direct_open}：官方承接（导航/媒体交接/纯链接） | 允许 ∧ 其余方法 |
| `oauth_publish` | admitted 且 method=oauth_server_push：OAuth+服务端投稿（4587 快手） | 允许 ∧ method=oauth_server_push |

v1 遗留 kind `automated` 仅存在于旧 /c 参与面（兼容保留，不进 v2 响应）。

### 2. Adapter 接口（4585-4588 按此实现）

```go
type Adapter interface {
    Platform() string                                    // "douyin"/"xiaohongshu"/"kuaishou"/...
    Prepare(ctx, PrepareRequest) (PrepareResult, error)  // 构造结构化 client_action（类型化描述，绝不返回可执行 JS）
    Submit(ctx, Handoff, PrepareResult) error            // 自动投稿路径；不适用→ErrUnsupported
    Query(ctx, Handoff) (QueryOutcome, error)            // 状态回查（如 photo_id）；不适用→ErrUnsupported
    VerifyCallback(ctx, CallbackPayload) (Receipt, error)// 官方回执验签；不适用→ErrUnsupported
}
var ErrUnsupported = errors.New("publishhandoff: capability not supported by this adapter")
```

- adapter 落 `server/internal/publishhandoff/adapters/<platform>.go`；经 `Service.SetAdapter(Adapter)` 注册（每平台至多一个，后注册替换）。
- 未注册/unsupported 的语义：Submit→422 unsupported_by_adapter（状态不推进）；Query→422；VerifyCallback→回执一律拒绝（NOT_VERIFIED 残差不变）。
- fixture 回执（SignReceiptForTest/HMACVerifier 测试密钥）只进测试；生产未配密钥=拒绝一切回执。

### 3. 事件六层（kind 常量冻结；source+event_id 去重）

| 层 | kind 常量 | 写入方 | 语义红线 |
|---|---|---|---|
| 1 点击 | `customer_click` | 公共 facts 面 | 永不升级状态 |
| 2 唤起尝试 | `wake_attempt` | 公共 facts 面 | 永不升级状态 |
| 3 媒体交接 | `media_handoff` | 公共 facts 面 / adapter | 素材交出≠发布；下载开始不得算平台成功 |
| 4 顾客自报 | `customer_self_report` | 公共 facts 面 | 仅核验请求，不改状态、不触发奖励 |
| 5 店员确认 | `staff_confirm` | 店员面（requireSession+authz） | 人工事实，永不转官方 |
| 6 平台证实 | `platform_confirm` | 回调验签管线 | 仅验签通过到达；receipt_id 部分唯一 |

- 每事件带 `source`（写入方标识，如 customer_page/staff_console/platform:douyin）+ `event_id`（写入方侧幂等 ID）；`(tenant_id, handoff_id, source, event_id)` 唯一部分索引；同键重放=静默去重，**同键不同 kind=IDEMPOTENCY_CONFLICT**。
- v1 事件 kind（official_confirmed 等）在旧 /c 行上原样保留；v2 行（payload_kind 非空）的平台证实写 `platform_confirm`。

### 4. evidence_level（plan 级，服务端派生，镜像 3535 platform_receipt 单元）

`official_receipt`（cell=supported，平台可证实）｜ `none`（cell=unsupported，无可证实回执，结果只到人工层）｜ `unknown`（cell=unknown，不承诺）。adapter 不得越过 3535 单元升级。

### 5. 端点（FEATURE_PUBLISH_HANDOFF；off=不注册+统一 404 双保险）

| 端点 | 鉴权 | 语义 |
|---|---|---|
| `GET /api/v1/public/actions/{actionId}/handoffs/plan?method=&environment=&payload_kind=` | visitor cookie | 计划预览：plan.kind/client_action/unsupported_fields/evidence_level/confirmation_required；拒绝面同样可预览（诚实解释）；零落行 |
| `POST /api/v1/public/actions/{actionId}/handoffs` | visitor cookie | 开交接（gate-first：refused/pending_external 零落行）。必填 action_session_id/action_revision/payload_kind/method/confirmation/idempotency_key；participation_id 可空 |
| `GET /api/v1/public/handoffs/{handoffId}` | visitor cookie | 查态（反枚举 404）；media→新鲜 media-access URL；link_share→share_url |
| `POST /api/v1/public/handoffs/{handoffId}/facts` | visitor cookie | 弱证据层落事件（click/wake_attempt/media_handoff/customer_self_report），带 source+event_id |
| `POST /api/v1/public/handoffs/{handoffId}/submit` | visitor cookie | 经 adapter.Submit（unsupported→422；成功→submitting） |
| `POST /api/v1/public/handoffs/{handoffId}/query` | visitor cookie | 经 adapter.Query（unsupported→422；计数 query_count） |
| `POST /api/v1/public/handoffs/{handoffId}/cancel` | visitor cookie | 顾客取消（终态） |
| `POST /api/v1/public/handoffs/callbacks/{platform}` | 无会话（平台→服务端） | adapter.VerifyCallback 验签→五绑定→重放去重→platform_confirm |
| `POST /api/v1/publish-handoffs/{id}/staff-events`（既有，v2 行扩写 staff_confirm） | requireSession | 人工事实 |

## 裁定书要点落实

1. **Gate 上下文服务端冻结（§6 指令①）**：v2 用 `channeladmission.JudgeActionRoute(ActionQuery{Now, TenantID, Environment, RewardPlay, MerchantAppRef}, rec)`。RewardPlay=服务端按记录 scenario==reward_task 派生；MerchantAppRef=新配置键 `PUBLISH_HANDOFF_MERCHANT_APP_REF`（空=merchant_authorized 路线 fail-closed 拒绝）。客户端永不传 reward/binding。
2. **unknown→pending_external（§6 指令②）**：v1 `PlanRoute` 的 admission_unknown→manual_only 映射替换为 pending_external（无 Manual guide、Notice 不承诺）；v1 PlanInput 补 RewardPlay/MerchantAppRef 服务端派生传入（/c 面场景不变，store_promo）。
3. **payload_kind 分流**：media 必填 content_pack_version（=session 所选/绑定版本的已采用可分发快照，confirmation==ContentFingerprint 快照指纹）；link_share 无内容包、无 media_access、share_url=动作发布 TargetRef（4584 已验证公网 https），confirmation==share_url 本身；均不伪建内容包；link_share 不要求 participation。
4. **action_revision**：请求值必须等于会话冻结值，否则 409 stale_version（3546 同码）。
5. **幂等**：新列 request_hash；同键同负载=回原行（Created=false），同键不同负载=409 idempotency_conflict。
6. **兼容**：旧 /c 参与发布路径（handlers_publish_handoff.go v1 流程）行为保持，仅 PlanRoute 两处按 §6 修正（相关 v1 测试同步更新）。

## 迁移 0073（单文件，两表重建，子先父后）

`0073_publish_handoff_v2.sql`：
- publish_handoff_events 重建：kind CHECK 白名单扩 v2 六层+保留 v1 八种；新列 `source TEXT NOT NULL DEFAULT ''`、`event_id TEXT NOT NULL DEFAULT ''`；新唯一部分索引 `(tenant_id, handoff_id, source, event_id) WHERE event_id != ''`；receipt 部分唯一索引扩为 kind IN (official_confirmed, platform_confirm)；旧行原样搬运。
- publish_handoffs 重建（子表已换名后）：campaign_id/participation_id/attempt_id 改 nullable（FK 保留；link_share 无参与合法）；新列 `action_id/catalog_code TEXT DEFAULT ''`、`action_session_id TEXT DEFAULT ''`、`action_revision INTEGER DEFAULT 0`、`payload_kind TEXT DEFAULT '' CHECK IN ('','media','link_share')`、`method TEXT DEFAULT ''`、`environment TEXT DEFAULT ''`、`plan_kind TEXT DEFAULT ''`、`evidence_level TEXT DEFAULT ''`、`client_action_json TEXT DEFAULT '{}'`、`unsupported_fields_json TEXT DEFAULT '[]'`、`share_url TEXT DEFAULT ''`、`request_hash TEXT DEFAULT ''`、`gate_json TEXT DEFAULT '{}'`（冻结 Gate 上下文审计）；旧行原样搬运，索引重建。
- （0074-0078 号段备用，不用不留文件。）

## 任务分解（TDD，每任务红→绿→commit）

- **T1 迁移 0073**：先写迁移测试（v1 行存活+v2 列存在+CHECK 放行新事件 kind+去重索引冲突），再写 SQL。验收：db 包测试 + publishhandoff 包测试全绿。
- **T2 计划面 v2（publishhandoff/plan2.go）**：`PlanPendingExternal` 常量；v1 PlanRoute 两处修复（未知→pending_external 无指引；GateInput 补 RewardPlay/MerchantAppRef）；`JudgeActionPlan(q ActionQuery, rec ActionCapability) ActionPlan` 纯函数产出 plan.kind/client_action(结构化)/unsupported_fields/evidence_level/confirmation 语义。正反例：五 kind 各自映射、manual guide 只在 manual_only、pending_external 无指引、reward=false 无法绕过（服务端 RewardPlay 恒按记录）。
- **T3 Adapter 接口（publishhandoff/adapter2.go）**：接口+ErrUnsupported+Registry（SetAdapter/AdapterFor）；未注册平台全 unsupported。测试：接口消费契约（unsupported 四方法、替换注册）。
- **T4 事件层（publishhandoff/events.go）**：六层 kind 常量+`AppendFact(source,event_id,kind,actor,payload)`（同键同 kind 去重返回 deduped、同键不同 kind ErrEventConflict）。测试：去重、冲突、v1 kind 兼容。
- **T5 Service v2（publishhandoff/service2.go）**：`StartAction(input)`（session 权威校验在 httpapi，服务层收 ActionStartInput：租户/门店/动作/catalog/platform/method/env/scenario/payload_kind/指纹或 share_url/participation+attempt 可空）；幂等 request_hash 冲突；gate-first 零落行；previewed→preparing→ready 管道；`Submit/Query`（adapter 路径）；`ApplyPlatformCallback`（验签→五绑定→platform_confirm）；v2 行 CustomerSelfReport/Cancel/Facts。测试矩阵：admitted fixture 全贯通、pending_external/refused 零落行、link_share 无参与建行、media 缺内容 409、确认指纹不符 422、同键不同负载 409、回执重放/乱序/错绑定、submit/query unsupported、取消终态。
- **T6 httpapi v2 面（handlers_action_handoffs.go + server.go 窄补丁 + config 两键）**：九端点接线；动作会话校验（visitor 绑定、session.action==path action、revision、active）；场景服务端派生（kind→scenario 映射：publish→store_promo/review→review/follow→follow/service→service/groupbuy→group_buy；v2 平台动作用 store_promo，reward_task 场景记录经 RewardPlay 判定）；store 能力行 best-match 解析；media-access URL 铸造在 handler（复用 3553 claim/audit）。测试：HTTP 正反例（400 缺字段/404 会话不符/409 stale/409 幂等冲突/422 各类）。
- **T7 契约文档**：docs/contracts/publish-handoff/v2/handoff-contract.md（冻结契约 §1-5 展开+示例 JSON+adapter 实现清单）。

## 明确不做（负面清单）

- 不动 web 前端（顾客视图归 1896；本票无 web 改动，vitest 仅需不回归）。
- 不写四个平台 adapter（4585-4588）；不建 OAuth 凭据存储；不接真实平台外呼。
- 不动 3535/3546/3553/1671 包本体（只 import）；v1 /c 路径除 §6 两处修正外零行为变化。
- fixture 回执/密钥只进测试文件。

## 验收门

1. `cd server && go test ./...` 全绿（基线 40 包全绿，本分支不引入回归）。
2. `cd web && npx vitest run` 全绿（792 测试基线）。
3. 正反验收：pending_external 无手工指引无承诺文案；客户端构造 reward=false/entry_mode=merchant 请求无法改变 Gate 输入；A27 纯链接在无任何内容包/参与的租户可完成交接；同幂等键不同负载 409；回执重放幂等；人工事件到不了 platform_confirm。
