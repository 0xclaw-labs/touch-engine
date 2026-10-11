# 2026-10-11 AG07 Touch 设计 W2（HUI-2390 / HUI-2628 续接，第二波）

状态：协调者自批（本角色无人可回答，普通问题按 fail-closed 默认作答并在此记录，交 Root 复核）。
分支：`ag07/20261010-touch-campaign-lead`（PR #48，OPEN）。基线 `origin/main` `c8c1eb42310dffe1d01ddce1b3e4a209a9277595`。
前序：`20261011-ag07-touch-design.md`（W1）继续有效；本文件只记录 W2 新决定，不改写 W1。

## 触发本波的新事实（开工时刷新实读，非沿用审计快照）

- F1 `public-ai` `origin/main` `6d1aadea` 已含 `internal/matrixdraft`（HTTP 面 `POST /v1/matrix-drafts`、`GET /v1/matrix-drafts/{id}[/preview|/receipt]`）与可独立运行进程 `cmd/matrixdraft`（127.0.0.1 + 文件 sqlite + grants JSON）。AG10 的 `codex/20261011-ag10-matrix-draft` 分支已全部入 main（`origin/main..分支` 为空）。这是 HUI-2373 持久草稿接口在 main 上**实际可获得版本**，按协议钉定消费，不再等待整票 Done。
- F2 该服务的 `consume_wire_test.go` 表明 grant 事实已接真实 `assetauthz` registry（HUI-2375 双端入账），`grant_ref` 是**素材级** assetauthz grant（grantor=商家 owner，grantee=matrix principal）。
- F3 `allowedType["touch"]="activity"`：Touch 源的 content_type 钉为 `activity`；schema 钉为 `matrix-draft-request/v1`；响应 `Draft`（`draft_id/status/published/charged/idempotent_replay`）。
- F4 服务端 `DisallowUnknownFields`：请求体只接受固定字段集。Touch 现网线（`POST {base}/drafts`，`{plan_id,status,executed}`）与真实契约**不兼容**，必须重钉。
- F5 服务端幂等键 = (source_app, source_project_id, campaign_id, asset_id, tenant_id, revision) 字段身份 + fingerprint 比对；重放返回 200 + `idempotent_replay=true`。因此 W1-D6 的「请求丢失后重试二次建草稿」**无需** `Idempotency-Key` 跨群契约：Touch 确定性发送同一字段集即可安全重放。
- F6 Touch `LibAsset` 已有 `GrantRef`/`SHA256`，`matrixVideo` 已强制 grant_ref 非空。**Touch 仍缺**：`payer_id`、`license_ref`、`license_expires_at`（素材授权到期时间）。AG01 面向 Touch 的权限 HTTP 端点（action `brand_matrix_draft`）在 public-ai main 上不存在（全库 grep 无此路由）。

## 问答记录（brainstorming，fail-closed 自答，待 Root 复核项标 ⚑）

- Q1 是否把 Touch 草稿 sink 重钉到真实 matrixdraft 契约？→ 是。F1-F4，按 main 实际可获得版本钉定。⚑ 无（这是协议明文要求）。
- Q2 `grant_ref` 从哪来？→ `LibAsset.GrantRef`（素材级授权，F2/F6）。无 grant_ref 的素材本来就进不了 matrixVideo 候选。
- Q3 `payer_id` / `license_ref` / `license_expires_at` 从哪来？→ Touch 数据模型没有；AG01 权限 HTTP 面缺失（F6）。fail-closed：权限响应解析**可选**新增 `payer_id/license_ref/license_expires_at` 字段（additive，不破坏既有 allowed/supply/reason）；缺任一 → 不向真实矩阵 POST、不落本地草稿记录，矩阵按钮禁用并给精确原因 `matrix_draft_authority_missing`。⚑ Root 复核项：payer 语义（运营商管理权 ≠ 客户账号发布权）与 license_ref 的权威来源，须 Root 裁定后由 AG01 落 HTTP 面。
- Q4 W1-D6 的 Idempotency-Key 跨群契约还等吗？→ 不等。F5：字段身份重放已闭环，升级 W1 的 BLOCKED 为已解除（以测试证明：响应丢失后重试 → 矩阵侧 1 条草稿 + Touch 侧 1 条记录）。
- Q5 `return_target` 怎么给？→ 服务端要求 `return.app == source_app` 且 path 为站内绝对路径。钉 `{"app":"touch","path":"/work/campaigns/<活动id>"}`；Touch 本地记录仍存 `ReturnLocationToken`（不变）。
- Q6 minsupply（W1-D9 待 Root 裁定）怎么处理？→ **一字节不动**。客户端改钉真实契约后 minsupply 不再被默认路径拨通（`/v1/matrix-drafts` 404 → Touch 显示矩阵供给缺失，fail-closed）。去留仍交 Root；本波不删除、不扩展、不美化。
- Q7 矩阵返回 403/409（grant_revoked / grant_expired / grant_unknown / license_expired / wrong_tenant / wrong_type / old_version / hash_conflict / idempotency_conflict / bad_request / 5xx）怎么办？→ 全部 fail-closed：按钮禁用 + 精确原因透传（`matrix_<code>` 或既有同义原因），不落记录、不重试风暴、不阻断留资/UGC 主链。
- Q8 ack 声称 published/charged 怎么办？→ 沿 W1：非 `status=draft ∧ published=false ∧ charged=false` 一律 `ErrNotDraft`，不记为完成。
- Q9 品牌发布与顾客 UGC/奖励分离怎么证明？→ 测试断言商家草稿路径：Lead/UGC 按钮状态原样返回、顾客效果计数零增量、store 无 reward/lead 新行。
- Q10 HUI-2628 两处 off_ds（campaigns L89 / materials L93，root 终裁锚点）怎么修？→ 抽共享工作台组件承载错误态重试控件（与本仓 TaskHandoffActions 持控same惯例），路由文件不再手写裸 `<button>`。两文件其余 findings（列表行按钮、挂素材表单等）**不动**，如实记录复扫 delta，全绿与否归 2625 gate owner。
- Q11 /admin 受控组织上下文验证？→ W1 已落 memberships 解析（无手填租户输入，`setTenantId` 全部程序化）。本轮用既有 web 测试 + e2e 复证，不重做。
- Q12 正式 blind 复评 / Notify→Leads 真实回执 / NFC / 外部平台 / 奖励资金？→ 本轮仍 NOT_RUN / BLOCKED（解除条件沿 W1 plan 第 8 条），不伪造。
- Q13 PR 载体？→ 沿 W1-D2：同分支快进推 PR #48，并按本轮指示把 PR 转 **draft** 等 Root 审后合并。

## 不在本波

- AG01 权限 HTTP 面的真实接入（等 Root 裁定 payer/license 语义 + AG01 落端点）。
- 2374 外发连接器、NFC 实机、外部平台发布、奖励资金、生产部署。
- minsupply 去留的执行（只等裁定）。
