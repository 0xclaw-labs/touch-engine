# HUI-1663 实施计划｜实体 NFC 与门店交付：出库绑定、写卡回读、真机验收与补换

- worktree: /Users/jimfu/works/派诺生态/wt-touch-1663 ｜ 分支: touch/hui-1663-nfc-delivery ｜ 基线: 1fb747e（wave1+wave2 全合流）
- 需求权威: Linear HUI-1663 全文（MCP 已读）+ briefs/hui-1663.md 裁定书 + 4591 报告 §7 硬件/§concerns-3 + 1665 报告
- 迁移号段: 0129-0131 ｜ 联调端口: web 18361 / go 18261 ｜ 真机项: 无硬件环境,诚实 NOT_VERIFIED（绝不标已验收）

## 裁定核对（brainstorm,以裁定书为答）

| 决策点 | 裁定 | 落法 |
|---|---|---|
| 七步状态机 | 付款→出库→绑定→写卡→回读→现场验收→交付,每步独立事实,不可跳跃 | `delivery` 纯状态机包 + 0130 事件账本;付款事实归 4591 订单投影（只读前置）,出库/写卡/回读/验收四步推 4591 投影,绑定/交付为本域两步 |
| 4591 接线 | 出库→验收由 hardware_step 推进;绑定校验在 1663 侧 | store 层事务内调 4591 纯状态机（store_billing.go 增 6 行导出事务版委托,窄补丁,4591 报告 §7 明示授权） |
| 写卡资料 | NFC URL（/t/{code}）,卡片零凭证 | 复用 1665 CSV/QR 已落纪律;回读=读回内容与绑定 code 一致性核对记录（matches 事实） |
| 补换 | 旧卡停用→新卡出库绑定;换绑不改旧参与快照;原因与审计 | damaged_recoverable=保留逻辑入口（同触点换物理单元）;lost_uncontrolled=撤销旧入口（停用旧触点+新触点）;0131 补换审计表;新交付记录重走全流程 |
| stale 防线 | 换域/改目标/换卡不得沿用旧「已验证」 | 交付行记 bound/verified binding_revision,与触点当前 revision 比对出 binding_stale,展示面如实暴露 |
| M8 | 卡片状态/写卡回读记录/批量导出 | 面板补交付插槽（七步+stale+回读摘要+事件详情+补换）;导出复用既有 tagcsv CSV（1665 已落,零改动） |
| 真机 | 无硬件=工程面完整+诚实未验证 | not-verified.md/verification-log.md 更新;绝不标已验收 |

## 任务序列（TDD:每任务红→绿→commit）

### T1 迁移 0129-0131 + db_test 表登记
- 0129 `touchpoint_deliveries`: id/tenant/store/touchpoint/billing_order 引用+delivery_ref/physical_unit_ref/tag_batch/status（七步+superseded 白名单 CHECK）/bound+verified_binding_revision/write+readback+device_evidence_ref/replacement_of/created_by+at。部分唯一索引 `(tenant_id,touchpoint_id) WHERE status!='superseded'` = 一触点至多一条活交付。
- 0130 `touchpoint_delivery_events`: 只追加账本, event_type 白名单（dispatched|bound|written|readback|accepted|delivered|replaced）, UNIQUE(tenant_id,source,event_id) 幂等, observed_binding_revision/payload。
- 0131 `touchpoint_delivery_replacements`: 补换审计（old/new delivery+touchpoint, reason_category ∈ damaged_recoverable|lost_uncontrolled, old_entry_revoked）。
- 测试: db_test.go 表名登记窄补丁（+3 行）;迁移重放由 db.Open 自动 Migrate 覆盖。

### T2 delivery 纯状态机包（server/internal/delivery/）
- `ApplyDeliveryStatus(status, step) (next, error)`: 七步严格线性;未知步 ErrUnknownStep;非下一步 ErrStepSkipped;superseded 拒写 ErrDeliveryClosed。
- 步骤↔4591 映射表 `HardwareStepFor(step)`: dispatched→shipment/written→write_card/readback→readback/accepted→acceptance;payment 只读、bound/delivered 无投影。
- 回读核对纯函数 `ReadbackVerdict(readURL, expectedURL, boundCode) (matches bool, reason)`: 逐字符;码段 Crockford 归一比对;PUBLIC_BASE_URL 已配置时 host 必须一致（错误域名不得通过——反例钉死）。
- 测试矩阵: 顺序全通过/跳步拒/未知步拒/终态拒。

### T3 store 层（store_touchpoint_deliveries.go + store_billing.go 窄补丁）
- store_billing.go: `ApplyHardwareStepInTx(tx,...)` 导出委托（复用 upsertHardwareStep,6 行,零既有行为改动——4591 报告 §7 接线授权,报告声明）。
- CreateTouchpointDelivery: 校验触点本租户+store 归属一致+订单本租户且 4591 payment 步 done（付款步事实前置）+无活交付;补换链 replacement_of 存在。
- ApplyTouchpointDeliveryEvent（单事务）: superseded 拒; (source,event_id) 幂等读回/异负载 IDEMPOTENCY_CONFLICT;状态机裁决;bind 校验（触点未停用+store 一致,记 binding_revision）;write 前置=bound（裁定书:写卡前必须已出库+绑定）;readback matches=false 只记事实不推进,matches=true 推进+verified_binding_revision;accept/deliver 推进;4591 投影同事务推送（重放路径零推送——物理事件重放不推进两次）。
- ListTouchpointDeliveries（filter store/touchpoint）+ GetTouchpointDelivery（含事件账本）+ steps 投影构建 + binding_stale 计算。
- ReplaceTouchpointDelivery: 两分支;lost 分支先停用旧触点（SetTouchpointStatus,epoch 失效公共缓存）;旧行 superseded+replaced 事件+0131 审计行;新行独立重走全流程;参与/权益零触碰（结构保证快照不挪）。
- 测试: 全链绿+反例矩阵（未付款建交付拒/跳步拒/重放幂等不双推/回读不一致不推进/越租户 404 掩码语义在 handler 验）。

### T4 httpapi（handlers_touchpoint_deliveries.go + server.go 路由窄补丁 +4 行）
- GET/POST /api/v1/touchpoint-deliveries; GET /api/v1/touchpoint-deliveries/{id}; POST .../{id}/events; POST .../{id}/replace。
- 权限: requireTagFace + 记录级 store scope（同 touchpointScoped;越店/跨租户 404 掩码）;operator/门店从会话与服务端事实解析,客户端声明不采信。
- 错误码: step_skipped 409/delivery_conflict 409/IDEMPOTENCY_CONFLICT 409/payment_required 409/bad_request 400/not_found 404。
- 创建输入: touchpoint_id+hardware_order_id+physical_unit_ref?+tag_batch?+delivery_ref?;store 从触点派生（客户端不作为绑定输入）。
- 测试: HTTP 全矩阵（权限/stale 展示/域名不匹配 409 语义/replaced 链/幂等重放 200 replayed）。

### T5 web（lib/touchpoint-deliveries.ts + 面板交付插槽 + vitest）
- 纯函数: deliveryStatusFace（七步文案,未到交付绝不显示「已交付/已验证」）、bindingStaleFace（绑定版本≠实测版本警示）、readbackFace（matches/unmatched）、eventFace、replaceReasonFace。
- 面板: load 增拉 deliveries（按门店过滤服务端收口）;每卡渲染交付块（状态七步/付款事实/绑定与实测版本/stale 警示/回读摘要）;「回读记录」展开拉详情事件账本;录事实表单（event_id 客户端生成 UUID 幂等键）;补换表单（原因分类+新物理单元,lost 分支选新触点）;下载 CSV 复用既有按钮零改动。
- vitest: 纯函数矩阵+面板源契约（data-action/data-field 存在性,未验证文案不冒充）。

### T6 证据文档诚实化
- not-verified.md: 增本轮工程面钉住的条目（交付状态机/接线/补换/回读核对=已机验 B 系）;A 系真机项全部保持 NOT_VERIFIED+阻断原因（无 NFC 卡/写卡器/真机）;补 T19 移交真实环境补验清单指引。
- verification-log.md: 增记本分支实跑结果（go/vitest/build 输出摘录）。

### T7 自审+全量验证
- code-review 自审 diff（spec 符合性+写面纪律）;go test ./... 全绿/vitest run 全绿/npm run build;gofmt/vet。
- 全 commit 在分支;不 push 不 merge;报告 → /Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-1663-report.md。

## 写面纪律声明
- 唯一写面: 迁移 0129-0131、`server/internal/delivery/`、`server/internal/store/store_touchpoint_deliveries.go(+test)`、`server/internal/httpapi/handlers_touchpoint_deliveries.go(+test)`、`web/src/lib/touchpoint-deliveries.ts`、`web/tests/touchpoint-deliveries.test.ts`、面板交付插槽、evidence 文档。
- 共享文件窄补丁: server.go（+4 行路由）、db_test.go（+3 行表名）、store_billing.go（+6 行导出事务委托,4591 报告 §7 接线指令）、touchpoints-panel.tsx（交付插槽增量,1665 既有行为零改动）。
- 触点状态表（1665 域）零直改;参与/权益表零触碰;billing 域既有行为零改动。
