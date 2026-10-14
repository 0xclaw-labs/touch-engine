# HUI-1663 贴卡与写卡现场检查表（field checklist）

- 日期：2026-10-14（模板创建；所有硬件步骤初始 NOT_VERIFIED）
- 票：HUI-1663。上游依赖：HUI-1665 触点管理（`/work/touchpoints`，稳定入口 `/t/{code}`）与活动/短码模型；最终硬件验收消费 1665 稳定入口与有效部署（HUI-3531 总控）。
- 使用方式：打印或带平板逐项执行；每项填 操作人/时间/版本；**任何一步未执行即整卡标 NOT_VERIFIED**。四阶段（准备→写入→回读→真机验证）对应触点管理台的部署五态（registered → awaiting_write → written_awaiting_readback → readback_done → device_verified），阶段推进只能单步显式进行（仓内已钉：`TestTouchpointStageExplicitOnly`）。

## 阶段 0：准备（触点管理台：registered）

| # | 步骤 | 通过判据 | 操作人 | 时间 | 版本 | 状态 |
|---|------|---------|--------|------|------|------|
| 0.1 | 确认正式稳定 HTTPS 入口域名 | `PUBLIC_BASE_URL` 为正式域名且 **HTTPS**；触点详情 URL 与之一致（管理面可见 `https://<域名>/t/<码>`） | — | — | — | NOT_VERIFIED |
| 0.2 | 确认活动可用 | 触点详情「查看目标」显示「顾客打开会进入活动」（available） | — | — | — | NOT_VERIFIED |
| 0.3 | 建立触点 | 每张物理卡一个触点（一卡一别名）；label/position 按摆放位置表填写（见 device-tag-matrix.md D 表） | — | — | — | NOT_VERIFIED |
| 0.4 | 取号 | 从触点管理台「导出 CSV」（列：label,short_code,url,store,group,uid_hint）或触点 QR/复制按钮取得 `url` 列；**url 即写入内容** | — | — | — | NOT_VERIFIED |

判据细节：
- 0.1 若域名非正式/非 HTTPS：停止，本批卡不得写入（错误域名写卡=整批返工）。测试域名通过不冒充生产验收。
- 0.4 `uid_hint` 列留待回读后回填（管理面 only，绝不印在卡上）。

## 阶段 1：写入（触点管理台推进到 awaiting_write → written_awaiting_readback）

| # | 步骤 | 通过判据 | 操作人 | 时间 | 版本 | 状态 |
|---|------|---------|--------|------|------|------|
| 1.1 | 选写卡工具 | 使用运营预写或**合法写卡工具**（如 NXP TagWriter / NFC Tools 等市售工具）；不等自研 App。**不采用 WebView 页面冒充 NFC 读写**；如用搭子原生写卡，另交原生模块/权限/构建与真机证据（票面要求） | — | — | — | NOT_VERIFIED |
| 1.2 | 写入记录类型 | **标准 NDEF URI Record**（NDEF TNF URI，「U」record），载荷 = 0.4 取得的完整 `https://…/t/<码>` URL；不写 AAR/不写自研 App 依赖/不写其他 payload | — | — | — | NOT_VERIFIED |
| 1.3 | 写入执行 | 工具报写入成功；一卡一 URL，与 CSV 该行逐字符一致 | — | — | — | NOT_VERIFIED |
| 1.4 | 触点阶段推进 | 管理台把该触点推进 `awaiting_write` → 写入完成后推进 `written_awaiting_readback`（单步、由实际操作驱动） | — | — | — | NOT_VERIFIED |

写卡失败处置：
- 写入报错/校验失败 → 该卡换新卡重写（1.1–1.3 重跑），坏卡按「损坏卡」处置（见故障段），并在 D 表记录坏卡去向。
- 写入成功的判据以**回读**为准，不以写卡工具的「成功」提示为准。

## 阶段 2：回读（触点管理台推进到 readback_done）

| # | 步骤 | 通过判据 | 操作人 | 时间 | 版本 | 状态 |
|---|------|---------|--------|------|------|------|
| 2.1 | 读卡核对 URL | 读卡工具读出 NDEF URI 与 CSV 该行 url **逐字符一致**（含 https 与路径，无多余尾随字符） | — | — | — | NOT_VERIFIED |
| 2.2 | 核对归属 | URL 中的码与该触点别名码一致；管理面该触点「查看目标」= 预期活动 | — | — | — | NOT_VERIFIED |
| 2.3 | uid_hint 回填 | 把回读到的标签 UID 回填触点对应标签的 uid_hint（管理面 only 字段） | — | — | — | NOT_VERIFIED |
| 2.4 | 触点阶段推进 | 推进 `readback_done`（回读确认 ≠ 真机验证，不跳级） | — | — | — | NOT_VERIFIED |

## 阶段 3：顾客真机碰触验证（触点管理台推进到 device_verified）

| # | 步骤 | 通过判据 | 操作人 | 时间 | 版本 | 状态 |
|---|------|---------|--------|------|------|------|
| 3.1 | 真机碰触 | 手机碰卡 → 系统通知/确认 → 浏览器打开 → **活动页首屏**（活动标题与 0.2 一致，商家名正确） | — | — | — | NOT_VERIFIED |
| 3.2 | 记录确认步骤 | 如实记录：锁屏是否弹出通知、点击确认了什么、是否需要解锁（票面：不承诺所有手机无提示自动跳转） | — | — | — | NOT_VERIFIED |
| 3.3 | 记录耗时 | 碰卡→活动页首屏秒数（矩阵 A/B 表对应行同步填） | — | — | — | NOT_VERIFIED |
| 3.4 | 扫码兜底验证 | 同一活动的 QR 兜底码（活动详情短码 QR）扫码 → 同一活动页（QR 与 NFC 最终落同一 `/c/{code}` 解析） | — | — | — | NOT_VERIFIED |
| 3.5 | 触点阶段推进 | 推进 `device_verified`（只有真机打开成功才允许出现「已验证」） | — | — | — | NOT_VERIFIED |
| 3.6 | 脱敏录屏 | 每机型至少一条，命名与归档见 device-tag-matrix.md 填写说明第 6 条 | — | — | — | NOT_VERIFIED |

## 故障与提示对照（预期提示 = 仓内已钉文案）

| 场景 | 预期顾客所见 | 仓内依据 | 现场验证 | 状态 |
|------|-------------|---------|---------|------|
| 损坏卡（读不出/读出错码） | 「活动不存在 / 请核对二维码是否正确，或联系商家」 | `TestFieldPauseExpireAndPrintedURLStability`（坏码统一 404 单字段，防探测） | 用破损卡实碰一次记录文案 | NOT_VERIFIED |
| 停用卡（单卡停用） | 「这个入口已被商家停用。请联系门店工作人员。」 | `TestTouchpointDisableIndependentOfSharedLink` | 停用一张卡实碰 | NOT_VERIFIED |
| 换绑后旧入口（旧路径卡） | 旧路径卡继续进旧活动（需重写卡）；停用旧短码则「链接已停用」 | `TestFieldRebindWorkflowOldCardKeepsOldTarget` | — | NOT_VERIFIED |
| 错租户/卡摆错店 | 页面显示**归属商家名**（merchant_name）；无跨店拦截语义——以回读 2.2 步防呆 | `TestFieldEntryChainFromCSVURLToCampaign`（归属可见） | 抽查一卡核对商家名 | NOT_VERIFIED |
| 错误域名（非正式域名打开） | 「无法确认服务品牌 / 域名未通过验证 / 这个入口不属于当前域名」（品牌闸开启时） | `handlers_brand.go`（unknown_brand/domain_error）+ `web/tests/nfc-field-entry.test.ts` | 在非正式域名手工打开同码 URL 记录文案 | NOT_VERIFIED |
| 弱网/服务不可达 | 「网络异常,活动加载失败 / 请检查网络后重试」+ 重试按钮 | `failureState`（public-state.ts）+ `web/tests/public-state.test.ts` | 飞行模式实碰一次记录文案 | NOT_VERIFIED |
| 暂停服务 | 稳定入口仍带码进入 /c 页 →「活动暂停中 / 请稍后再来」；商家暂停接待 →「商家暂停接待新的参与，已提交的信息仍然保留」 | `TestTouchpointPublicResolutionAPI`（paused 仍 200 带码）+ `STATE_TEXT` | 后台暂停后实碰 | NOT_VERIFIED |
| 活动到期/未开始 | 「活动已结束/活动尚未开始」+ 对应动作文案 | `TestFieldPauseExpireAndPrintedURLStability` | 窗口边界抽测 | NOT_VERIFIED |

「不误标部署成功」红线：以上任一场景的现场实测未做，对应行保持 NOT_VERIFIED；服务端状态/配置不作为硬件行为证据。

## 换卡 / 扫码兜底说明（现场速查）

1. **换卡**：阶段 1 重跑（新卡新写入）→ 回读 → 真机验证；坏卡物理销毁或退仓，不流入门店。触点别名不变（URL 不变，无需重取号）。
2. **扫码兜底**：任何 NFC 不可用场景（设备不支持/开关关闭/标签损坏），顾客用活动详情短码的 QR 码进入——QR 与 NFC 共用同一 canonical URL 解析链（`TestFieldEntryChainFromCSVURLToCampaign`：QR 载荷 = CSV url 同源），活动内容一致。QR 码打印件与卡片同批次验收（3.4）。
3. **决不允许**：用后台「创建数据库标签/导出 CSV」冒充物理写入（票面原文）；用 WebView 页面冒充 NFC 读写（票面原文）。
