# 计划｜HUI-4595 真实内容制作接线：公共任务与计费、成片回流、候选预览和商家采用

worktree: ../wt-touch-4595 ｜ 分支: touch/hui-4595-production ｜ 基线: ce47dde（wave1 已合流）
迁移号段: 0091-0094 ｜ 端口: web 18351 / go 18251 ｜ 报告: /Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-4595-report.md

权威: Linear HUI-4595 票面（已读全文）+ briefs/hui-4595.md 裁定书 + SHARED-BRIEF 12 条。

## 交付边界（写面纪律）

- 只新增/窄补丁：`server/internal/db/migrations/0091-0092`、`server/internal/store/store_content_production.go(+test)`、
  `server/internal/production/`（新包：quote/task seam）、`server/internal/httpapi/handlers_content_production.go(+test)`、
  `server.go` 窄补丁（新增 Register 行 + import + Server 字段）、`internal/db/db_test.go` 表清单两行、
  `web/src/lib/bff.ts` internal 拒透传、`web/src/components/work/store-production-panel.tsx`（新）、
  `web/src/components/work/store-content-drawer.tsx`（候选预览窄补丁）、`web/src/app/work/materials/page.tsx`（接线窄补丁）、
  web 测试新文件 + 既有文件窄补丁。
- 不动：handlers_content_packs.go / 采用事务（AddStoreReturnCandidate/AdoptStoreContentPackVersion 只调用不重写）、
  旧 campaign content-handoffs 路径（继续兼容）、web/vendor、globals.css。

## 契约（拟兼容增量，逐条可验证）

### A. 公共发起（商家会话面）
`POST /api/v1/stores/{storeId}/content-production-handoffs`
- 输入 JSON（DisallowUnknownFields）: `content_pack_ref`（必填，"pack:<id>" 或裸 id）、
  `content_binding_ref`（可空；非空必须 "pack:<id>" 且与 content_pack_ref 同包，否则 400 bad_content_binding_ref）、
  `quote_ref`（必填非空 ≤128）、`idempotency_key`（必填 ≤128，租户内唯一）、可选 `note`。
- 服务器核实: 会话→租户成员→authz.manage_content_packs→门店存在且属于租户→包存在、属于本店
  （空包（零版本/零采用）完全合法——首次制作路径，T05；绝不为通过校验伪建 campaign/伪建采用）。
- quote seam: 注入/装配 `production.QuoteAuthority`；已配置→必须返回 confirmed（否则 422 quote_not_confirmed，不落行）；
  未配置→quote_ref 仅作为**申报值**落库（响应与行面如实 `quote_verified:false`，绝不声称已核实）。
- task seam: FEATURE_TASK=on 且 Task/Billing 配齐→经平台任务权威提交（幂等键=handoff 幂等键），落真实 task_ref，
  status=submitted；未配置→**诚实关闭**: 不提交、status=recorded、响应 notes 如实说明。
- 受信生产方（TOUCH_SERVICE_TOKENS 含 content_return_producer 角色）未注册→**503 production_unavailable** +
  交接行 JSON + 如实 reason（沿用 3553「记录 brief + 诚实降级」户型）；已注册→201。
- 幂等: 同租户同 idempotency_key 重放返回**同一行**（200 + replayed:true），不新建制作工程、不重复报价（T15）。
- 响应字段: id/store_id/pack_id/content_binding_ref/quote_ref/quote_verified/status/task_ref/notes[]/replayed/created_at。

`GET /api/v1/stores/{storeId}/content-production-handoffs` — 本店发起记录（status 处理中/失败/取消/已产出/
未提交独立可辨；task_ref/quote_ref/charge_ref 投影可见；重登/断网后查询同一任务=同一 handoff_id）。
`GET /api/v1/stores/{storeId}/content-production-handoffs/capabilities` — 如实申报:
producer_configured / quote_authority_configured / task_authority_configured / production_available / reason。
（生产方开关未配诚实关闭的直接消费面。）

### B. 受信回流（服务生产方专属；/api/v1/internal/*，公共 BFF 拒透传）
`POST /api/v1/internal/content-production-handoffs/{handoffId}/results`
- 认证: `X-Service-ID`+`X-Service-Token`（TOUCH_SERVICE_TOKENS 常量时间比较）且角色=content_return_producer；
  无服务身份不落会话路径（ops-grant 户型）→统一 401；注册表未配→503 fail-closed；角色不符→403。全程审计日志
  （只记 service id/handoff/结果，永不记 token/体）。
- tenant/store/pack **只由已存 handoff 行恢复**（GetProductionHandoffByID 无租户作用域，行=唯一租户真值；
  请求体/头里的租户声明不存在——结构性不可覆盖，票面「请求中的tenant/store不能覆盖权威映射」）。
- multipart 输入: `task_ref`(必填) `result_revision`(≥1) `charge_ref`(必填 费用引用) `source_app`(必填,
  ^[a-z0-9][a-z0-9-.]{1,62}$) `event_id`(必填 ≤64) `outcome`(produced|failed|canceled，缺省 produced)
  `video`/`cover` 文件（produced 必填，真实字节经 Media.Save 实测）+ 声明哈希 `video_sha256`/`cover_sha256`
  （必填；与实测不符→422 hash_mismatch——两端 SHA 都记录）+ 文案字段 title/body/store_name/price_note/valid_note/
  topics/ai_generated/ad_required。
- 校验链（顺序）: 认证→handoff 行恢复（404 统一反枚举）→canceled 终态拒新结果（409 handoff_canceled）→
  source_app+event_id 幂等: 同键同负载摘要（canonical digest）→200 重放同一 result 行（version_id 相同，
  不重复登记、不重复扣费——T15）；同键异负载→409 IDEMPOTENCY_CONFLICT→result_revision 水位: 新事件且
  revision ≤ 已存水位→409 result_revision_stale（旧稿迟到有确定结果；旧回流绝不覆盖新采用）→
  task_ref 与行上已记录 task_ref 不一致→409 task_ref_mismatch→produced: 实测媒体+哈希核对→
  RegisterContentMedia（tenant/store 取自行,createdBy=service:<id>）→ **3553 AddStoreReturnCandidate 唯一登记事务**
  （Source=product_photo_return、IdempotencyKey=source_app+":"+event_id；只落 candidate、永不触碰采用指针）→
  落 result 行 + 交接行 status 水位更新（produced/failed/canceled 独立）。
- 产出候选立即「可播放真实媒体」: candidate 状态经既有 media-access 语义预览（Distributable 允许 candidate，
  商家预览=短 TTL 受控 URL，纯预览零计费零生成）；顾客侧仍只有**明确采用后**经 B05 读面取得（结构保证）。

### C. BFF（web）
- `bff.ts`: path 首段为 `internal`（含 `v1` 直通分支的 `v1/internal`）→ 拒透传统一 404 not_found（与 Go 同形，
  无存在性泄漏）。票面硬性: 公共 BFF 不得透传 internal 接口。vitest 锁定。

### D. M5（/work/materials + 门店抽屉）
- 新 `store-production-panel.tsx`（复用 production-panel/production.ts 的诚实表述纪律）:
  capabilities 读取→按钮可用性如实；发起表单（选包+quote_ref 输入+幂等键自动生成展示）→POST→201/503/重放
  三种结果如实呈现；发起记录列表（status/task_ref/quote_ref）。
- 门店内容抽屉: 候选（含 product_photo_return 来源）加「预览」（media-access preview）——采用前商家可看真实成片；
  「采用」既有按钮覆盖回流候选；来源展示「制作回流」。
- 零计费结构: 顾客可触达面（media-access/B05）无任何生成/制作调用；面板只有显式提交动作会建 handoff。

## 任务序列（TDD，红→绿→commit）

1. **迁移+表清单**: 0091 store_content_production_handoffs（UNIQUE(tenant,idempotency_key)；CHECK status∈
   recorded|submitted|produced|failed|canceled）；0092 store_content_production_results（UNIQUE(source_app,event_id)、
   payload_digest、revision、outcome CHECK）。db_test.go 表清单 +2。红: 建表存在性断言先行。
2. **store 层** store_content_production.go: Create/Get/List/GetByIDAnyTenant/AppendResult（digest+水位+终态）/ListResults。
   单测: 幂等重放同 id、digest 冲突、水位、终态、跨租户 404。
3. **seam 包** internal/production: QuoteAuthority（ConfirmedQuote）+ BillingQuoteAuthority（对齐 copyjob 的
   unified/usage+quote 契约形）+ TaskSubmitter（POST /internal/v1/tasks 幂等,回 task_id/hold_id）;
   NewFromConfig 全或无 fail-closed。单测用 httptest 桩（只进测试入口）。
4. **HTTP 面**: handlers_content_production.go + requireContentReturnProducer + server.go 窄注册。
   全部正反例: 空包首作合法/未授权 pack 404/跨租户 404/binding 形状/quote 未确认 422/幂等重放/
   producer 未配 503/回流全链/两端哈希不符 422/迟到 revision 409/idempotency_conflict/task_ref_mismatch/
   canceled 拒流/staff 403/无服务身份 401/错角色 403。
5. **BFF deny + web 测试**。
6. **M5 接线** + web 测试（源文本断言户型）。
7. **全闭环测试**（本票核心验收）: 全新门店→空包→确认报价（seam 桩）→发起（producer 已配,201,submitted）→
   受信生产方 multipart 回流（真实小 mp4+png 字节,实测哈希核对）→候选可 preview（media-access 200）→
   商家明确采用→B05 读面/受控媒体取得同一文件（字节一致）→重复回流重放零新增→旧 revision 迟到 409→
   采用不被覆盖。同一闭环含「重试不重复扣费」断言（结果行计数不变+charge_ref 不变）。
8. **code-review 自审** → 全量 go test + vitest → 报告。

## 明确不做（诚实边界）

- 不实现渲染器/GPU/钱包；平台 Task/Billing 真实对接只在 seam 内（生产环境凭 FEATURE_TASK+token 启用，
  未配=诚实关闭，绝不伪造 task_ref/费用）；真实制作产品（产品图/GoBoost/AiCut/数字人）适配归后续联调票
  （票面允许: 「缺真实provider标未完成而非演示成功」）。
- 真实生态「付款主体/预计费用显示」深度依赖 4591 账单面——本票以 quote_ref/charge_ref 投影 + capabilities
  如实申报交付 seam，联验回填。
