# 计划｜HUI-4592 平台运营控制台（六视图可操作闭环）

- worktree: ../wt-touch-4592 ｜ 分支: touch/hui-4592-ops-console ｜ 基线: bafb03a（wave1+wave2 全合流）
- 迁移号段: 0124-0128 ｜ 端口: web 18360 / go 18260 ｜ 报告: reports/hui-4592-report.md
- 需求权威: Linear HUI-4592 票面 + briefs/hui-4592.md 裁定书 + SHARED-BRIEF.md

## 0. 权限模型（4593 消费，不自造）

- ops 命令只来自受信 ops_grants（`ops_grant_manager` 服务缝签发，绑定 principal+tenant；商家会话自授=无效，直打签发缝=401/403——4593 已有，本票只消费）。
- 新 gate `requireOpsSession(command)`：FEATURE_AUTHZ 门（off=404）+ requireSession + resolveAccess + `authz.Can(a, "ops:<command>", storeID)`。命令白名单冻结在 ops 包。
- 受控支持访问独立于 ops grant：`ops_support_access` 行（限时、指定目标租户、原因必填），每请求实时校验（到期/撤销立即停止）；进入后所有深读/写均带 support_access_ref 审计。

## 1. 冻结契约

### ops 命令（ops:<command>，只增不改）
`ops:tenants.read` / `ops:tenants.manage` / `ops:plans.read` / `ops:plans.manage` / `ops:orders.read` / `ops:orders.manage` / `ops:channels.read` / `ops:channels.manage` / `ops:templates.read` / `ops:templates.manage` / `ops:operations.read` / `ops:operations.manage` / `ops:support.manage`

### 操作信封（POST /api/v1/ops/operations 统一接收）
`{action, resource_ref, expected_revision?, reason, idempotency_key, tenant_id?}` →
`{operation_id, state, result, replayed?}`；`GET /api/v1/ops/operations/{id}` 追踪权威结果。
同 idempotency_key 同负载=幂等读回（不重执行）；异负载=409 IDEMPOTENCY_CONFLICT；缺 reason=400；expected_revision 不符=409 stale_version。

### 白名单 action（分派到各域唯一写面）
| action | 命令 | 分派（唯一写面归属） |
|---|---|---|
| tenant.suspend / tenant.resume | ops:tenants.manage | SetTenantLifecycleOps（2053 lifecycle 语义，revision 单语句守卫） |
| tenant.charge_hold.set / .clear | ops:tenants.manage | SetChargeHoldOps（2053 charge hold 语义） |
| plan.publish / plan.retire | ops:plans.manage | UpsertBillingCatalogEntry（4591 目录唯一写入函数，改生效窗口，零价格常量） |
| order.recheck | ops:orders.manage | 只读重查订单+退款+履约投影，快照落 result；不改金额/权益 |
| channel.disable / channel.restore | ops:channels.manage | SetActionCapabilityEmergency（3535 唯一写面，只停对应方法行） |
| content.retract | ops:templates.manage | BlockStoreContentPackVersion（3553 风险下架唯一写面，blocked_reason=原因） |
| template.assignment.cancel | ops:templates.manage | UpdateTemplateAssignmentStatus(cancelled)（4593 分发撤回唯一写面） |
| ticket.open/confirm/resolve/close | ops:operations.manage | ops_tickets 状态机（本票域） |
| drill.record | ops:operations.manage | ops_drills；restore 演练必带 verification_report |

### 读面（真实数据，unavailable≠零记录）
- GET /api/v1/ops/tenants?q=&lifecycle=（列表：lifecycle/charge_hold/brand/门店数/成员数/服务期投影 paid_until/代理关系数）
- GET /api/v1/ops/tenants/{id}（平台层事实；深层数据 stores/members/orders 需活跃 support access，无则 `business_data: "locked"`）
- GET /api/v1/ops/plans（目录全集+生效状态；历史订单价格快照不受影响的明示）
- GET /api/v1/ops/orders?kind=（跨租户订阅/AI/硬件订单+退款发票状态+异常队列=failed/refund 差异）
- GET /api/v1/ops/channels（3535 全量能力行，含 emergency_disabled/valid_until/证据档/审计）
- GET /api/v1/ops/templates（跨租户视频模板目录+分发引用+内容版本风险态）
- GET /api/v1/ops/operations（O6 聚合：域名健康探针/硬件履约只读投影[4591 五状态，1663 未先行 seam]/触点库存计数/工单/演练/近期审计）
- GET /api/v1/ops/audit?tenant=&limit=（审计回查）
- GET/POST /api/v1/ops/support-access、POST /api/v1/ops/support-access/{id}/close

### 迁移（0124-0128，全部 additive）
- 0124: ops_audit_log（append-only：前后值 JSON/操作者/时间/原因/authority_source/operation_id）+ tenants.ops_revision 列（expected_revision 单语句守卫）
- 0125: ops_support_access（expires_at 非空=强制限时；closed 墓碑）
- 0126: ops_operations（idempotency_key UNIQUE；state=running|succeeded|failed|rejected；result_json）
- 0127: ops_tickets（open→confirmed→resolved→closed 状态机；owner/原因）
- 0128: ops_drills（kind=backup|restore；restore 必带 verification_report——「只写已备份」不算）

## 2. 任务拆解（TDD，每任务红→绿→commit）

1. 迁移 0124-0128 + db_test 表名窄补丁（红：表存在性测试）
2. ops 域包（纯函数）：命令/动作白名单、工单状态机、support TTL ≤ 240min、信封校验（红：域测试）
3. store_ops_console.go：审计/支持访问/操作信封/跨租户读/revision 写（红：store 测试）
4. httpapi 读面六视图 + gate（红：权限矩阵/空态/unavailable）
5. httpapi 操作信封分派 + 幂等/revision/审计三件套（红：正反例）
6. T17 负例矩阵：普通商家冒充 ops=403、无 grant、缺原因=400、过期 support access=立即停、stale revision=409、重复命令=幂等读回、跨租户不泄漏
7. T20 面：O6 聚合读（探针 degraded 不填 0、硬件五状态投影来源标明）
8. Web：components/ops/（OpsGate + 六 desk）+ 六薄壳页 + page-map/census 窄补丁（21→27）
9. Web 测试（源码级断言+census 同步）+ tsc + build
10. 全量 go test + 起服冒烟（18260/18360）+ 自审 diff

## 3. 验收对照
- T20：注入渠道到期/证书域名错误→O6 告警定位具体对象；修复后探针真实恢复（不把错误填 0）。
- T17：上条负例矩阵全拒/幂等，不泄露别户数据。
- 价格发布不改历史订单（订单行 price_snapshot 不可变=4591 结构保证，测试复验）；渠道关闭不删既有权益（3535 结构保证）；高权限操作按 operation_id 追踪。
- O4 不关闭无关导航：3535 紧急停用只作用能力行（其回归测试已锁），ops 面复验。

## 4. 写面纪律
- 新增：migrations 0124-0128、internal/ops/、store_ops_console.go(+test)、handlers_ops_console.go(+test)、web/src/app/ops/*、web/src/components/ops/*、web/src/lib/ops-console.ts、web/tests/ops-console*.ts
- 共享文件窄补丁：server.go（+1 注册行）、db_test.go（+表名行）、handlers_domain_health.go（探针逻辑抽 `domainHealthSnapshot()` 供 O6 复用，行为逐字节不变）、page-map.ts/page-census.ts/page-census.test.ts（+6 页）、config.go（如需 ops 特性常量，尽量零新增）
- 不动：他人域写面（billing authority 缝、3535 internal 端点、模板域发布）、work-frame（导航入口归 2628，报告挂账）、globals.css
