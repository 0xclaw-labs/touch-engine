# 计划｜HUI-4594 门店授权线索真实接收与撤回（无活动留资、客户页和三端回执）

worktree: ../wt-touch-4594 ｜ 分支: touch/hui-4594-lead-receipt ｜ 基线: ce47dde（wave1 已合流）
迁移号段: 0088-0090 ｜ 端口: web 18350 / go 18250 ｜ 报告: ../../../../../touch-all-20261109/reports/hui-4594-report.md

> 票面来源说明：Linear MCP 在本次会话持续不可用（get_issue 多次 fetch failed）。
> 需求权威降级为【指挥预裁定书 briefs/hui-4594.md + SHARED-BRIEF】，裁定书即为
> brainstorm 答案基线；本计划不引入裁定书之外的新裁定。此缺口记入报告 concerns。

## 裁定→实施映射（无新增开放问题）

| 裁定 | 实施落点 |
|---|---|
| 无活动留资复用 /contact 提交链 | 公共门店面新端点（form/submit/status/revoke），复用同一 notice v1/同意/normalize/PII/幂等/outbox 核心，campaign_id='' 挂 store 维度 |
| GET /api/v1/internal/lead-records/{submissionId} 只给受信方 | TOUCH_SERVICE_TOKENS + 新角色 lead_receiver；X-Leads-Tenant 租户校验；tombstone+最小字段（source_revision/consent_revision/purpose） |
| 持久化 ACK 五元组 | POST …/acknowledgements：receipt_id/source_event_id/submission_id/source_revision/receiver_state 齐才结束同步（crm_received），receipt+状态迁移+审计同事务 |
| 撤回 tombstone+水位 | fetch 对 revoked 只回 tombstone（无联系方式）；revoke_source_revision 水位拦 stored-ACK 复活；旧 /internal/v1/lead-records（BFF 内部令牌取联系方式）退役 |
| 重放不重复 | ACK receipt_id UNIQUE（同键同负载幂等、同键异负载 409）；source_event_id 必须是本租户已发事实（outbox event_id 或 notify_event_id） |
| M11 /work/leads | 商家线索页：已授权/待同步/接收确认/撤回 分开计数，待同步不算已获客；1677 读 receiver_state |
| 复用既有 leads/notifytask/outbox | 不建新 CRM 表；receipts 是 receipts，不是第二线索库 |

## 任务分解（TDD，每任务红→绿→commit）

### T1 迁移 0088 lead_receipts + 接收投影列
- `0088_lead_receiver_receipts.sql`：lead_receipts(id PK, tenant_id, submission_ref,
  source_event_id, source_revision, receiver_state(stored|revoked), receiver_id,
  created_at, receipt_id UNIQUE)；lead_submissions ADD receiver_state/receipt_id。
- store：AckLeadReceipt（同事务：receipt 插入 + lead 投影列更新 + sync 迁移 + 审计）、
  GetLeadReceipt。
- 测试：同 receipt 重放幂等；同 receipt 异负载冲突；stored-ACK 只在 pending_sync/
  rejected 上落 crm_received。

### T2 迁移 0089 撤回水位
- `0089_lead_revocation_waterline.sql`：lead_submissions ADD revoke_source_revision
  INTEGER NOT NULL DEFAULT 0。
- RevokeLeadSubmission 同事务置水位 = source_version+1（与 revoke 信封版本一致）。
- 测试：撤销后水位 >0；重复撤销不改水位。

### T3 迁移 0090 无活动留资（表重建释放 campaign FK）
- `0090_lead_store_capture.sql`：重建 lead_submissions（campaign_id TEXT NOT NULL
  DEFAULT '' 无 FK，ADD capture_source('campaign'|'store')/session_id）与 lead_forms
  （去 FK/inline UNIQUE，partial unique index：campaign 非空唯一；store 行
  campaign='' 时 store_id 唯一）；数据全量搬迁（含 T1/T2 新列）。
- store：UpsertLeadFormForStore/GetLeadFormByStore、ListLeadsByTenant、
  CountTenantLeadStates（sync_state×receiver_state）、SubmitLead 增加 capture_source/
  session_id 传递。
- 测试：旧数据搬迁后 campaign 线索原样；store 线索 campaign_id='' 可插；
  store 表单每店一行。

### T4 forwarder 语义：notify 投递成功≠落库
- confirmState：delivered → 不再 MarkLeadDelivered（保持 pending_sync 待同步可见）；
  dead/subscription_disabled → rejected 维持。
- 测试改造既有 forwarder/leads 测试：DeliverAll 后行仍 pending_sync；
  dead 仍 rejected。publicLeadProgress 的 crm_received 语义随之=「接收方已 ACK」。

### T5 受信接收面（handlers_lead_record.go 重写）
- leadReceiverRole="lead_receiver"；requireLeadReceiver（requireServiceProducer 孪生：
  常量时间比较、registry 空则 503 fail-closed、无服务身份一律 401、角色不符 403、
  审计 auth 决策）。
- GET /api/v1/internal/lead-records/{submissionId}：X-Leads-Tenant 必带且等于线索
  tenant（否则统一 404 不泄漏存在性）；granted → 最小字段（submission_ref/tenant/
  name/phone/wechat/authorization/purpose/source_revision/consent 块）；revoked →
  tombstone（零联系方式，authorization=revoked、source_revision=水位）。
- 旧 GET /internal/v1/lead-records/{submissionRef} 路由退役；lead_record_test.go
  迁移到新面（BFF 内部令牌不再能取联系方式）。
- 测试：无服务身份 401/坏 token 401/无角色 403/跨租户 404 不泄漏/granted 最小字段/
  tombstone 无联系方式。

### T6 ACK 端点
- POST /api/v1/internal/lead-records/{submissionId}/acknowledgements。
- 严格 schema（DisallowUnknownFields）；五元组缺一 → 400 ack_incomplete 且状态不动；
  submission_id 与路径不一致 → 400；receiver_state ∉ stored|revoked → 400。
- source_event_id 校验：必须命中该 submission 的 outbox event_id 或 notify_event_id
  （否则 409 unknown_source_event，不结束同步）。
- 水位：revoked 线索收到 stored-ACK → 409 revocation_waterline（拦晚到重放复活）；
  revoked-ACK 记 receiver_state=revoked（本地未撤销时只记账不改本地态，不伪造状态）。
- 测试：T1/T2 全部负例 + 201/200(duplicate) 正例 + 审计行存在。

### T7 无活动门店留资公共面（handlers_public_leads.go 提炼共用核心）
- GET/POST /api/v1/public/storefronts/{storeCode}/lead-form|lead-submissions、
  POST …/lead-status|lead-revocations（leadsGate 门内；storeCode 解析走
  GetStorefrontPublicByCode + 租户 active + 门店 active，非可用统一 404 带状态文本）。
- 与 campaign 链共用：notice v1、consent 必检、NormalizePhone、DedupKey（桶=storeCode）、
  每 IP 限流、PII 扫描、lead+outbox 同事务、审计；body 可带 action_session_id
  （可选，必须真实存在且属同租户同门店，否则 400）。
- 信封：campaign=''、StoreRef=storeID、Correlation.Ref=storeCode；不伪造活动引用。
- 测试：无活动可留资/表单关 404-ish/同店同日幂等/撤销 tombstone/跨店码 404/会话引用校验。

### T8 /api/v1/work/leads 后端（handlers_admin_leads.go 域内）
- GET /api/v1/work/leads：requireSession + requireScopedAction(ActionReadList,
  TenantID)+店长按门店 scope 过滤；返回 items（最小线索视图：ref/来源(campaign 或
  store)/渠道/时间/sync_state/receiver_state/receipt_id 摘要/revoked）+ stats
  （authorized/pending_sync/crm_received/revoked）；FEATURE_LEADS_CAPTURE off →
  leadsGate 统一 404。
- 测试：租户隔离 404/店长只见本店/待同步与接收分开计数/feature off 404。

### T9 M11 /work/leads 页（web）
- page-map 台账 +1 条（list 模式）；WorkFrame 导航 +「线索」。
- page.tsx 薄壳 + components/work/leads/leads-view.tsx：
  stats 行（已获客=接收确认数；「待同步不计入已获客」口径注记）、
  items 列表（来源/状态/接收状态/撤回）、loading/empty/error+重试三态、
  data-state 静态声明（同 ContactPage 口径）。
- vitest：台账恰好一条、页面零交互标记、视图三态与口径注记、390 无固定像素宽。

### T10 env.mjs + 收尾
- e2e/scripts/env.mjs goEnv 增 TOUCH_SERVICE_TOKENS="leads-ingest:e2e-leads-token"、
  TOUCH_SERVICE_ROLES="leads-ingest:lead_receiver"（LEADS_* 先例同位）；seed 增
  门店 storefront code 线索一条（走真实公共端点）。
- 全量 verify：cd server && go test ./...；cd web && npx vitest run；自审 diff。

## 纪律核对
- 写面：server/internal/{leads,store(store_leads.go),httpapi(lead 域文件+server.go 窄补丁),
  db/migrations(0088-0090)}、web(page-map/WorkFrame 窄补丁+新 leads 页/组件/测试+env.mjs)。
  公共文件只窄补丁（Register 行/台账行/导航行/env 键）。
- 不 push 不 merge；fixture 只进测试；不伪造接收/成功状态。
