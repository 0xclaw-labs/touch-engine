# 统一发布交接合同 v2｜touch-handoff/v2（HUI-1670，2026-11-10 增量）

状态：**实施契约（本分支已实现，服务端真源）**。权威链：SHARED-BRIEF ＞ 裁定书 briefs/hui-1670.md ＞ 3535 合同 §6 ＞ 本文档。

消费方：**HUI-4585（抖音）/ 4586（小红书）/ 4587（快手）/ 4588（手动家族）** 按本合同写各自 adapter；1896（顾客视图）按端点形状渲染。

包面：`server/internal/publishhandoff`（交接状态机唯一写面；publish_handoffs / publish_handoff_events 两表只由本包修改，迁移 0073）。adapter 落 `server/internal/publishhandoff/adapters/<platform>.go`，经 `Service.SetAdapter(Adapter)` 注册。

## 1. plan.kind（五值，冻结）

| plan.kind | 语义 | 判定来源 |
|---|---|---|
| `refused` | 准入拒绝（not_admitted / emergency_disabled / channel_closed / admission_expired / wrong_binding / reward_play_not_authorized）：无入口、无指引、**零落行** | `JudgeActionRoute` !Allowed 且非 PendingExternal |
| `pending_external` | 资格未知（status=unknown / 无核查窗 / 环境未声明）：**不承诺、不显示可完成态、无手工指引** | `d.PendingExternal` |
| `manual_only` | admitted 且 method=manual_material：合规导出+手动指引，管道照常 | 允许 ∧ method |
| `official_handoff` | admitted 且 method ∈ {h5_share, miniapp, js_media_handoff, native_sdk, link_share, direct_open}：官方承接（导航打开/媒体交接/纯链接） | 允许 ∧ method |
| `oauth_publish` | admitted 且 method=oauth_server_push：OAuth+服务端投稿（4587） | 允许 ∧ method |

- 3535 合同 §6 指令②已落实：`admission_unknown → manual_only` 旧映射废除（v1 /c 面同步：pending_external 与 refused 同样拒绝开交接）。
- 自动化通道（h5/miniapp/js/oauth）未接 adapter 时诚实回退为 `manual_export` client_action（玩法已获准，手动路径合法），kind 不变；adapter 接入后由 `Prepare` 升级为官方交接动作。**绝不编造深链/SDK/OAuth 入口**。
- 既有交接的 plan 冻结在行内（`plan_kind` 列），GET 回冻结面——准入变化只影响新交接。

## 2. Adapter 接口（冻结，四平台票按此实现）

```go
type Adapter interface {
    Platform() string                                     // "douyin"/"xiaohongshu"/"kuaishou"/...
    Prepare(ctx context.Context, req PrepareRequest) (PrepareResult, error)
    Submit(ctx context.Context, h Handoff, p PrepareResult) error
    Query(ctx context.Context, h Handoff) (QueryOutcome, error)
    VerifyCallback(ctx context.Context, raw CallbackPayload) (Receipt, error)
}
var ErrUnsupported = errors.New("publishhandoff: capability not supported by this adapter")
```

- **不适用的方法返回 `ErrUnsupported`**（诚实缺位，调用方 422，状态不推进，绝不伪造成功）。
- `PrepareRequest{Handoff, Plan, Text, Media, MediaRefs, ShareURL, SessionID, Environment}`——MediaRefs 是 3553 media-access 短 TTL 受控 URL（purpose=platform_fetch），adapter 拉素材用；**文件字节零过本包、内部 token 永不进 adapter 请求之外**。
- `PrepareResult{ClientAction, UnsupportedFields, Payload}`——ClientAction 是**类型化描述**（type/url/miniapp_path/params/caption/steps/note），绝不返回可执行 JS。
- `Submit` 成功后服务端把 handoff_ready/editor_opened → `submitting`（证据=system_adapter）；失败=状态不变（顾客可重试/取消）。
- `Query` 只回查不重投（先回查纪律）；结果落 `platform_query` 事件（照录不确认）。若查询产出平台侧受信凭据，以 `QueryOutcome.Receipt` 回流，经与回调**完全相同**的绑定→去重→确认管线。
- `VerifyCallback` 验签在先、信任在后；返回的 `Receipt` 必须带 `HandoffID`（回执必须回声我们的不透明交接 id）+ 五绑定字段。验签失败返回错误（管线记 receipt_rejected/拒收，绝不入库为确认）。
- fixture 回执（`SignReceiptForTest`/`HMACVerifier` 测试密钥）**只进测试**；生产未配 `PUBLISH_HANDOFF_RECEIPT_SECRET` 且无 adapter verifier = 拒绝一切回执（NOT_VERIFIED 残差）。

## 3. 事件六层（kind 常量冻结）+ source/event_id 去重

| 层 | kind | 写入方 | 红线 |
|---|---|---|---|
| 1 | `customer_click` | 公共 facts 面 | 永不升级状态 |
| 2 | `wake_attempt` | 公共 facts 面 | 永不升级状态 |
| 3 | `media_handoff` | 公共 facts 面 / adapter | 素材交出≠发布；下载开始不算平台成功 |
| 4 | `customer_self_report` | 公共 facts 面 | 仅核验请求；不改状态、不触发奖励 |
| 5 | `staff_confirm` | 店员面（requireSession+authz；payload.fact=staff_saw_submit\|staff_verified_work） | 人工事实；永不转官方 |
| 6 | `platform_confirm` | 回调/受信 Query 验签管线 | 仅验签通过到达；receipt_id 部分唯一 |

- 附加 kind：`platform_query`（回查留痕）；v1 八种 kind 在 /c 旧行上原样保留（v2 行的平台证实写 `platform_confirm`，v1 行写 `official_confirmed`）。
- **去重**：每事件带 `source`（写入方标识，如 customer_page / staff_console / adapter:douyin / platform:douyin）+ `event_id`（写入方侧幂等 ID）。`(tenant_id, handoff_id, source, event_id)` 唯一部分索引：同键同 kind=静默去重（`AppendFact` 返回 deduped=true）；**同键不同 kind=ErrEventConflict（HTTP 409 idempotency_conflict）**。

## 4. evidence_level（plan 级，镜像 3535 platform_receipt 单元）

`official_receipt`（cell=supported，平台可证实）｜`none`（cell=unsupported，结果只到人工/自报层）｜`unknown`（cell=unknown，不承诺）。**adapter 不得越过 3535 单元升级**；准入真值更新走 3535 受控端点。

## 5. payload_kind 分流

- `media`：记录 required_inputs 含 `content_pack_version` 的路线必走 media。content_pack_version 必须是 **3553 已采用且可分发版本**（blocked/授权过期=409 拒新分发）；`confirmation` = 快照指纹（`ContentFingerprint`，与 3553/计划预览同一函数）；换内容=换指纹=必须重新确认。受控媒体引用按 media-access 机制铸造（官方交接 purpose=platform_fetch，manual_only purpose=download），每次铸造落审计。
- `link_share`（A27 纯链接家族）：required_inputs 为空的路线。**无内容包、无 media_access、不伪建内容包/不建参与**；`share_url` 由服务端从已发布动作 TargetRef（4584 已验证公网 https）生成；`confirmation` = share_url 本身。
- payload 与路线 required_inputs 不匹配 = 422 payload_mismatch。

## 6. 端点（FEATURE_PUBLISH_HANDOFF；off=不注册+统一 404 双保险）

| 端点 | 鉴权 | 请求要点 | 响应 |
|---|---|---|---|
| `GET /api/v1/public/actions/{id}/handoffs/plan?method=&environment=&payload_kind=&action_session_id=` | visitor | 环境六枚举；payload_kind 可省 | `plan{kind,allowed,pending_external,reason,reason_text,missing,notice,evidence_level,client_action,unsupported_fields}` + `confirmation_required`（确认令牌，POST 时原样回传） |
| `POST /api/v1/public/actions/{actionId}/handoffs` | visitor | 必填 `action_session_id, action_revision, payload_kind, method, environment, confirmation, idempotency_key`；可选 `participation_id`（给了必须 handed_off 冻结） | 201/200 `{handoff{...frozen plan...}, plan, media[]}` |
| `GET /api/v1/public/handoffs/{handoffId}` | visitor | — | 冻结 plan + 新鲜 media 引用（弱网重试不新建行） |
| `POST /api/v1/public/handoffs/{handoffId}/facts` | visitor | `{kind∈四弱层, source, event_id, payload?}` | `{handoff, deduped, notice}` |
| `POST /api/v1/public/handoffs/{handoffId}/submit` | visitor | — | 经 adapter.Submit；unsupported=422 |
| `POST /api/v1/public/handoffs/{handoffId}/query` | visitor | — | 经 adapter.Query；计数+platform_query 事件 |
| `POST /api/v1/public/handoffs/{handoffId}/cancel` | visitor | `{reason?}` | canceled（终态，不复活） |
| `POST /api/v1/public/handoff-callbacks/{platform}` | 无会话（平台→服务端） | 原始回调载荷+签名头 | 验签通过且绑定通过→`{accepted:true,status}`；已验真被拒→`{accepted:false}`（200 停重试）；未注册平台/验签失败→422 receipt_unverified；未知交接引用→404 |
| `POST /api/v1/publish-handoffs/{id}/staff-events`（v1 既有） | requireSession | kind ∈ staff_saw_submit\|staff_verified_work | v2 行落 staff_confirm 事件 |

请求权威与安全语义（全部服务端单点）：
- 会话权威 = 3546 动作会话（visitor cookie 唯一访问凭据；path actionId 必须等于会话动作，否则同形 404 反枚举；`action_revision` ≠ 会话冻结值 = 409 stale_version）。
- **Gate 上下文服务端冻结**（3535 合同 §6 指令①）：scenario 由动作 Kind 派生（publish→store_promo/review/follow/service/groupbuy→group_buy）；`RewardPlay` = 记录 scenario==reward_task（服务端派生，客户端 reward=false 永远进不了闸）；`MerchantAppRef` = `PUBLISH_HANDOFF_MERCHANT_APP_REF` 配置（空=merchant_authorized 路线一律 wrong_binding 拒绝）。
- Gate 拒绝 / pending_external / confirmation 不符 / payload 不匹配：**零落行**。
- 幂等：`(tenant_id, idempotency_key)` 唯一 + `request_hash` 负载指纹：同键同负载=回原行（200）；**同键不同负载=409 idempotency_conflict**。
- 状态机：v1 十态与证据门禁原样复用（platform_confirmed 只由官方回执到达；editor_opened 只由 verifier 证明；弱四层结构性进不了任何迁移）。

## 7. 迁移 0073（本票）

`publish_handoffs` 重建：campaign_id/participation_id/attempt_id 改 nullable（FK 保留；link_share 无参与合法）；新增 action_id/catalog_code/action_session_id/action_revision/payload_kind(CHECK ''|media|link_share)/method/environment/plan_kind/evidence_level/client_action_json/unsupported_fields_json/share_url/request_hash/gate_json。`publish_handoff_events` 重建：kind CHECK 白名单扩 v2 六层+platform_query（v1 八种保留）；新增 source/event_id；唯一索引 `(tenant,handoff,source,event_id) WHERE event_id!=''`；receipt 部分唯一扩至 platform_confirm；handoff_id FK 解除（父表可重建；行级引用由唯一写面纪律保证）。

## 8. 4585-4588 实现清单（各票责任）

1. `publishhandoff/adapters/<platform>.go` 实现四方法；每平台至多一个 adapter，`SetAdapter` 后注册替换。
2. 环境差异按六环境枚举逐平台给值（四分能力更新走 3535 受控端点，不在 adapter 内自建白名单）。
3. OAuth 凭据服务端保管（4587）；客户端永无 secret；回调验签各自实现，共享 VerifyCallback 契约（Receipt.HandoffID 必填）。
4. JS 失败≠发布回执（4586）；异步接受≠最终公开（4587 用 Query 核对 photo_id）；未准入场景（4585 抖音店宣）拒绝且不用手工路线绕过——闸在 3535，adapter 只消费裁决。
5. fixture 回执/测试密钥只进 `_test.go`；真机证据缺失处诚实标注未验证，不伪造成功。
