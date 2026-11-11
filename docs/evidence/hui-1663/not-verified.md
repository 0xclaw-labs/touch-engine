# HUI-1663 NOT_VERIFIED 清单（及已机验对照）

- 日期：2026-10-14（工程准备）；**2026-11-11 增补（TOUCH-ALL-20261109-v1 交付域实施，分支 `touch/hui-1663-nfc-delivery`，基线 1fb747e）**。口径不变：**没有 NFC 硬件与真机，本票硬件检查未完成**（票面原文）；G2 其他发布/参与/页面开发不受阻塞；本票与总控（HUI-3531）的实体 NFC 交付不得据此标 Done。
- 本清单与 PR 正文逐条一致；解锁条件指「什么时候、凭什么证据可以把该项改为已验证」。

## A. 硬件项（全部 NOT_VERIFIED）

| # | 项目 | 票面出处 | 为什么未验 | 解锁条件（证据形态） | 执行载体 |
|---|------|---------|-----------|---------------------|---------|
| A1 | 真实 NFC 卡写入（标准 NDEF URI 物理写入） | 必做①⑥「真实卡写入…创建数据库标签或导出CSV不算物理写入」「合法写卡工具」 | 本环境无 NFC 卡片与写卡器 | 脱敏录屏：写卡工具写入 → 读回 URI 与 CSV url 一致 | field-checklist 阶段 1 |
| A2 | 写卡后回读核对（URL/归属/uid_hint 回填） | 必做①、必做⑦「回读」 | 同上 | 回读录屏 + uid_hint 回填截图（管理面） | field-checklist 阶段 2 |
| A3 | 顾客真机碰触打开正确活动（系统通知/解锁确认步骤记录、耗时） | 必做①②、交付物「脱敏录屏」 | 无真机 | 分机型录屏：碰卡 → 系统提示 → 活动页首屏，含耗时 | field-checklist 阶段 3 + 矩阵 A/B 表 |
| A4 | iPhone 机型矩阵实测 | 必做②「覆盖计划交付的 iPhone 具体型号」 | 无真机 | device-tag-matrix.md A 表全部行 PASS/FAIL（不许外推全机型） | 矩阵 A 表 |
| A5 | Android 机型矩阵实测（含 NFC 开关设置路径、关 NFC 走扫码） | 必做②「Android…NFC 设置」「不支持/关闭NFC可扫码」 | 无真机 | B 表全部行；B3（关 NFC 扫码）必测 | 矩阵 B 表 |
| A6 | 桌贴/立牌实物验证 | 必做③「用实际桌贴/立牌」 | 无实物 | C1/C2 行实测 + 摆放照片 | 矩阵 C/D 表 |
| A7 | 抗金属标签金属表面实测 | 必做③「抗金属能力由实测支持，不靠服务器配置」 | 无实物 | C3 + A4/B4 金属面行实测 | 矩阵 C3 |
| A8 | 错误域名/弱网/损坏卡现场文案实拍 | 必做⑤「有明确提示」的现场面 | 服务端文案已机验（见 B5–B7），真机呈现未拍 | 对应场景录屏/截图 | field-checklist 故障段 |
| A9 | 正式域名/白标最终实体回归 | 「正式域名/白标的最终实体回归仍保留在本票和3531」 | 测试域名受控验证通过**不冒充**生产验收 | 正式域名上复跑 A1–A7 抽样 | 本票收尾 + HUI-3531 |

## B. 已机验项（本仓自动化测试，可回归）

| # | 项目 | 仓内依据 |
|---|------|---------|
| B1 | 稳定入口链路：/t/{code} 解析 → /c/{code} → 正确活动（标题/商家归属）；CSV 取号 = /t URL 同源；部署五态显式单步 | `TestFieldStableEntryChainTouchpointToCampaign`、`TestTouchpointStageExplicitOnly`、`TestTouchpointPublicResolutionAPI` |
| B2 | 旧路径链路：CSV url（/c/{code}）→ 解析 → 正确活动；QR PNG 载荷解码 = CSV url（QR 与 NFC 同 canonical URL） | `TestFieldEntryChainFromCSVURLToCampaign` |
| B3 | 同卡换绑四要素：URL 不变 / 新访客进新目标 / 原参与保留原权益 / 旧二维码兼容；换绑预览参与数、乐观并发 409 | `TestFieldRebindStableURLKeepsOriginalParticipation`、`TestTouchpointRebindPreviewConfirmAndConflict`、`TestTouchpointDisableInvalidatesCacheImmediately` |
| B4 | 单卡停用不影响其他卡（稳定入口别名停用只关自己；旧路径一卡一链成立；shared 别名影响如实钉住） | `TestTouchpointDisableIndependentOfSharedLink`、`TestFieldSingleCardDisableIsolation`、`TestFieldSharedLinkAliasSemantics` |
| B5 | 暂停/到期/损坏码：paused/expired/not_found 明确态与统一 404 单字段形状（防探测）；管理面编辑后同一 URL 继续有效 | `TestFieldPauseExpireAndPrintedURLStability` |
| B6 | 缓存失效：FEATURE_PUBLIC_CACHE 开启下停用/换绑对下一次公共请求立即生效（epoch） | `TestFieldDisableVisibleNextRequestWithCache`、`TestTouchpointDisableInvalidatesCacheImmediately` |
| B7 | 顾客提示文案：网络异常+重试、暂停接待、错误域名家系、入口停用、活动不存在等，均为白名单文案且不冒充成功 | `web/tests/nfc-field-entry.test.ts`、`web/tests/public-state.test.ts`、`web/tests/touchpoints.test.ts` |
| B8 | 权限与泄漏：游客/店员/跨租户矩阵、公共载荷白名单（uid_hint/租户/角色零泄漏）、CSV 注入转义 | `TestTouchpointPermissionMatrix`、`TestNFCTagPermissionMatrix`、`TestTouchpointExportCSVAPI` |
| B9 | **交付七步状态机不可跳跃**（付款→出库→绑定→写卡→回读→现场验收→交付，每步独立事实：操作者/时间/设备；跳步 409 step_skipped；终态/封存拒写） | `internal/delivery/delivery_test.go`（跳步/未知步/终态矩阵）、`TestTouchpointDeliveryEventPipelineAndProjection` |
| B10 | **交付创建闸=付款事实前置**（4591 订单投影 payment done 只读消费；未付款 409 payment_required；非硬件订单 400；活交付部分唯一索引） | `TestTouchpointDeliveryCreateGates`、`TestTouchpointDeliveryHTTPFullJourney` |
| B11 | **4591 五状态投影同事务接线**（出库/写卡/回读/验收四步推 shipment/write_card/readback/acceptance，操作者/设备一致；幂等重放零双推；多单元订单冲突不回滚本域事实——per-card 权威在交付账本） | `TestTouchpointDeliveryEventPipelineAndProjection`、`TestTouchpointDeliveryEventIdempotencyNoDoublePush` |
| B12 | **回读一致性核对**（错误域名 host_mismatch/错误码 code_mismatch 一律 matches=false：只记事实不推进状态不推投影——「只有 source=nfc 请求/未回读不得显示已验证」的工程半边；逐字符+码段 Crockford 归一；写卡资料=公开 /t URL，卡片零凭证 B15） | `TestCheckReadback`、`TestTouchpointDeliveryReadbackMismatch`、`web/tests/touchpoint-deliveries.test.ts` |
| B13 | **stale 防线**（换绑/换域后 bound/实测 binding_revision ≠ 当前 → `binding_stale=true` 如实暴露，不得沿用旧「已验证」标识） | `TestTouchpointDeliveryBindingStaleAfterRebind`、`TestTouchpointDeliveryHTTPStaleAfterRebind` |
| B14 | **补换两分支**（damaged_recoverable 同触点换载体保留逻辑入口；lost_uncontrolled 撤销旧入口=旧触点停用+公共解析即时停用+新触点新码；0131 审计行含 old_entry_revoked；旧行封存 superseded 拒写；新卡独立重走全流程；参与/权益零触碰） | `TestTouchpointDeliveryReplaceRecoverable`、`TestTouchpointDeliveryReplaceLostRevokesEntry`、`TestTouchpointDeliveryHTTPReplace` |
| B15 | **交付面权限与幂等**（org_owner 全租户/store_manager 本店/越店跨租户 404 掩码；同键同负载 replayed 零推进；同键异负载 409 IDEMPOTENCY_CONFLICT；replaced 事件词表只归补换流程） | `TestTouchpointDeliveryHTTPPermissionMatrix`、`TestTouchpointDeliveryHTTPReplayAndConflicts` |
| B16 | **M8 交付插槽文案不冒充**（未到交付绝不显示「已交付/已验证」；stale 明说「不适用于当前绑定」；回读不一致按原因给恢复动作；批量导出复用既有 tagcsv CSV 零改动） | `web/tests/touchpoint-deliveries.test.ts`、`web/tests/touchpoints-panel.test.ts`（HUI-1663 段） |

## C. 边界声明

- 隔离测试使用临时 sqlite（`t.TempDir()`），不污染正式券账（必做⑦）。
- 2026-10-14 工程准备轮零迁移零生产代码；**2026-11-11 交付域轮**新增迁移 0129-0131 与交付域写面（见 verification-log.md §2026-11-11），A 系真机项**仍然全部 NOT_VERIFIED**——工程面（B9–B16）只是把「不可跳跃/回读核对/补换审计」做成机器裁决，**不能替代**真实 NFC 卡写入、真机碰触与正式域名回归（票面反例：「只有模拟器」「只有 source=nfc 请求」不得显示部署已验证）。
- E2E（`web/e2e/run.sh`）按任务书执行：若端口 18460-18462 被占则跳过并如实记录（见 verification-log.md）。

## D. 交付域真机补验清单（T19 实体卡部分移交真实环境，按此执行）

1. 用 M8「下载写卡资料(CSV)」取 /t/{code} URL → 合法写卡工具写 NDEF URI → 回读 URL 与 CSV url 逐字符一致（解锁 A1/A2；M8 交付插槽「记录:回读」matches=true 落账）。
2. 计划机型真机碰触 → `/t/{code}` 解析 → `/s/{store_code}`（store 根）或旧卡 `/c`（解锁 A3/A4/A5）。
3. 错误域名片/损坏卡在 M8 回读表单如实录入 → matches=false（host_mismatch/code_mismatch）事实落账、状态不推进（对应 B12 的现场半边，解锁 A8 的交付域部分）。
4. 补换现场：damaged_recoverable 换卡后同 URL 重写重读重测；lost_uncontrolled 换卡后旧 /t URL 真机碰触确认已停用（解锁 A8 补换现场部分）。
5. 正式域名复跑以上抽样（A9，归 3531 收尾）。
