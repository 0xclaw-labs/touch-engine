# HUI-1663 NOT_VERIFIED 清单（及已机验对照）

- 日期：2026-10-14。口径：**没有 NFC 硬件与真机，本票硬件检查未完成**（票面原文）；G2 其他发布/参与/页面开发不受阻塞；本票与总控（HUI-3531）的实体 NFC 交付不得据此标 Done。
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

## C. 边界声明

- 隔离测试使用临时 sqlite（`t.TempDir()`），不污染正式券账（必做⑦）。
- 本票工程准备**零数据库迁移、零生产代码改动**（只新增测试与文档；rebase 后确认稳定入口机制已在 main，撤销了开工时记录的 GAP-1，见 rebind-verification.md 第 4 节）。
- E2E（`web/e2e/run.sh`）按任务书执行：若端口 18460-18462 被占则跳过并如实记录（见 verification-log.md）。
