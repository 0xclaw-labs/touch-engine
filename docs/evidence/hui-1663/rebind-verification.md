# HUI-1663 同卡换绑验证清单

- 日期：2026-10-14（模板创建；硬件相关行初始 NOT_VERIFIED）
- 票面验收原文：「同卡后台换活动后新访客进入新目标、原参与保留原规则权益；卡内URL不变。验证单卡停用不影响其他卡、暂停/到期、缓存失效、旧二维码兼容。」
- 结论先行：该验收的**服务端语义已全部在仓内以自动化测试钉住**（下表「仓内依据」列）；剩余为现场实物复验步骤（NOT_VERIFIED）。

## 1. 语义事实（两条入口路径，行为不同，现场必须分清）

| 路径 | 卡上 URL | 换绑方式 | 换绑后卡内 URL | 换绑后访客去向 | 适用 |
|------|---------|---------|---------------|---------------|------|
| **稳定入口（推荐）** | `https://<域名>/t/<别名码>` | 触点管理台：换绑预览 → 确认（乐观并发，预期不符 409 不覆盖） | **不变**（别名码是印刷/写入内容，永不改变） | 新访客进**新活动** | HUI-1663 票面验收的标准路径 |
| 旧路径（标签直绑短码） | `https://<域名>/c/<短码>` | 标签管理 PATCH link_id | **改变**（导出 URL 随新短码变化）| 旧卡 URL 不变 ⇒ 访客**仍进旧活动**（必须重写卡/重印码） | 兼容已按旧路径发出的卡与已印二维码 |

旧路径行为依据：`TestFieldRebindWorkflowOldCardKeepsOldTarget`（换绑=URL 变、旧卡仍指旧活动、原参与保留）。
两路径并存不冲突：停用别名不影响共享短码、底层短码照常（`TestTouchpointDisableIndependentOfSharedLink`）。

## 2. 票面四要素 × 仓内机验映射（全部 PASS，机器可回归）

| # | 票面要素 | 仓内依据（测试函数，httpapi 包） | 断言内容 |
|---|---------|--------------------------------|---------|
| 2.1 | 卡内URL不变 | `TestFieldRebindStableURLKeepsOriginalParticipation` + `TestTouchpointRebindPreviewConfirmAndConflict` | 换绑前后触点详情 url 逐字节一致；并发换绑 expected 过期 409 不静默覆盖 |
| 2.2 | 新访客进入新目标 | `TestFieldRebindStableURLKeepsOriginalParticipation` + `TestTouchpointDisableInvalidatesCacheImmediately` | 换绑后 /t 解析码变为新活动短码；/c 落新活动标题；缓存开启下立即生效（epoch） |
| 2.3 | 原参与保留原规则权益 | `TestFieldRebindStableURLKeepsOriginalParticipation` | 换绑前经旧码开始的参与，换绑后 lookup 原样恢复（同 id、同旧 link）；预览面板带出已开始参与数（只读） |
| 2.4 | 旧二维码兼容 | 同 2.3 尾段 + `TestFieldPauseExpireAndPrintedURLStability` | 旧短码仍 available（旧活动），已发二维码不失效；管理面编辑活动内容时同一 URL 继续有效（印刷物料不用换） |

附加机验（票面同句的其余两项）：

| # | 票面要素 | 仓内依据 | 断言内容 |
|---|---------|---------|---------|
| 2.5 | 单卡停用不影响其他卡 | `TestTouchpointDisableIndependentOfSharedLink`（稳定入口：停别名只关自己，兄弟卡/底层短码/标签状态零影响）；`TestFieldSingleCardDisableIsolation`（旧路径：一卡一链 rotate 时成立）；`TestFieldSharedLinkAliasSemantics`（旧路径 shared 别名：停一卡=停共享入口，检查表强制一卡一链） | 见各测试 |
| 2.6 | 暂停/到期/缓存失效 | `TestFieldPauseExpireAndPrintedURLStability`（paused/expired 态、恢复后同一 URL 复活）；`TestFieldDisableVisibleNextRequestWithCache`、`TestTouchpointDisableInvalidatesCacheImmediately`（FEATURE_PUBLIC_CACHE 开启下停用/换绑下一次请求立即生效） | 见各测试 |

## 3. 现场复验步骤（硬件相关，NOT_VERIFIED）

前置：阶段 0–3（field-checklist.md）已对 A、B 两个活动各完成至少一卡。

| # | 步骤 | 通过判据 | 操作人 | 时间 | 版本 | 状态 |
|---|------|---------|--------|------|------|------|
| 3.1 | 换绑前碰卡 | 卡 A（稳定入口）打开活动「周年庆」 | — | — | — | NOT_VERIFIED |
| 3.2 | 后台换绑 | 触点管理台对卡 A 预览（显示已开始参与数）→ 确认换绑到「春季展」；操作成功且无 409 | — | — | — | NOT_VERIFIED |
| 3.3 | 同卡复碰 | **同一张卡不重写**，再次碰触 → 打开「春季展」 | — | — | — | NOT_VERIFIED |
| 3.4 | 原参与核验 | 换绑前在「周年庆」开始参与的顾客设备，重复碰触/刷新恢复原参与与权益（原活动语义不变） | — | — | — | NOT_VERIFIED |
| 3.5 | 旧码兼容 | 换绑前印发的「周年庆」二维码/旧路径卡仍可打开「周年庆」 | — | — | — | NOT_VERIFIED |
| 3.6 | 单卡停用 | 停用卡 A → 卡 A 提示「入口已被商家停用」；旁边卡 B 照常打开 | — | — | — | NOT_VERIFIED |
| 3.7 | 暂停/到期 | 后台暂停「春季展」→ 碰卡 A 出现「活动暂停中」；恢复后同卡直接可用 | — | — | — | NOT_VERIFIED |
| 3.8 | 记录 | 以上每步记录操作人/时间/触点与活动版本；录屏归档 | — | — | — | NOT_VERIFIED |

## 4. 决策记录（按票面最严格口径处理的过程留痕）

- 本票开工基线 c803816 时尚无稳定入口机制，曾按旧路径语义把「同卡换标 URL 不变」记录为模型缺口（GAP-1）并声明不擅自加 link 级换绑。**2026-10-15 rebase 到前进后的 main（072dfb3）后确认：HUI-1665 增量已交付 TouchpointAlias 稳定入口（/t/{code}），换绑 URL 不变语义已存在并已有测试**。据此 GAP-1 撤销，改为本文第 1 节的双路径语义说明；未新增任何生产代码/模型/迁移。
- 旧路径「换绑=URL 变」不是缺陷而是 1665 既定拍板（绑定以 link 为准）；现场规程据此要求旧路径卡片换绑后重写卡，或直接改用稳定入口。
