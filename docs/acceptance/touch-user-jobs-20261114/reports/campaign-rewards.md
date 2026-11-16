# HUI-4656 + HUI-4658 实施报告

日期：2026-11-14。实际基线 `d22da8e1d8e4a4799eb7bb44e66472d956dea188`（包含 content initialBody 与统计/测试类型修复）；不是 brief 中旧基线重新建设。
工作树：`/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/campaign-rewards`。
分支：`codex/touch-4656-4658-campaign`。
本地源码提交：`f27d40b702ba8bc4010a71f9d32d131c11513dbd`。未 push、未改 main、未写 Linear、未标 Done。等待 root 指定 gpt-6.1-terra 的真实浏览器及对抗审查。

## 实际差距与本轮改动

1. 创建活动先选目标：普通门店服务直接进入门店工作区，不创建活动；主动咨询跳过推广内容/权益配置；内容推广与优惠才调用统一 ContentEditor 并维护顾客承诺。门店查询上下文及单一门店自动带入，恢复草稿保持原目标。
2. 活动详情日常流程移除 Motion Handoff Brief、重复制作工程面板、重复内容包面板、重复门店跳转表。内容上传/选材/制作统一调用已交付 ContentEditor，传 storeId/storeName/campaignId/purpose/initialTitle/initialBody/returnHref；onAdopted 仅选候选，不自动发布。历史工程记录保留显式兼容入口，原 #store-motion/#motion-handoff/#production 与 history=1 仍能受控回看；旧组件/端点未删除。
3. 唯一活动承诺仍写原 publish-config；M9 只读该 spec 摘要，修改链接回同一活动编辑源。普通用户不填权益槽位、规则版本、证据枚举、内部门店 ID 或规则快照引用。数量/成本上限、条件、领取期、使用期、门店、缺货处理、未完成处理和顾客路径集中呈现。
4. 新增原库存域 `POST campaigns/{id}/reward-inventory/prepare`，仅收业务库存名称/数量或合法券码/面额/可选成本/系统请求键。服务端从当前已保存配置派生 kind、slot、version、唯一适用门店、允许核验证据、币种、使用期及 rule_ref；不信任浏览器传这些绑定。当前表型库存只代表一门店；多门店历史规则需先在唯一编辑源明确，不静默扩大到全店。
5. 准备库存仍进入原 CreateRewardInventory 事务；稳定请求键派生原库存主键，数据库约束保障重试不新增批次，同请求改负载拒绝。同一事务核对当前配置版本以及同版已准备数量/成本，禁止超过承诺；成本模式不接受未知成本，并防止乘法溢出。重放回读当前库存余量。
6. 修复原正常闭环缺口：无变更保存过去总是 version+1，使准备好的库存落在旧版；现在同一业务承诺（含时间格式与渠道/门店顺序归一）保存复用原版本，不写无变化修订。真正修改继续新版本、回草稿，原冻结参与/已发权益不改写。
7. 原 raw reward-inventory HTTP 接口只兼容从未维护 publish-config 的旧活动；配置已存在时拒绝人工覆盖绑定，必须走上述派生准备事务，避免从人工核验证据绕过当前规则。
8. 活动列表/Get 增加服务端 availability 投影（state/label/can_start/reason），复用 campaign.Window 与原 campaignenable.Check/assembleEnableFacts，读取无写入/预留副作用。主状态反映未开始、窗口结束、暂停、结束、推广尚未启用及真实库存/渠道预检。逐渠道仍沿原准入判断，单渠道失败不关闭无关门店服务。
9. 配置存在的活动转 active 时，服务端要求配置已 enabled 并再次复用同一预检；前端不可通过直接 status active 绕开。未配置咨询活动继续原生命周期路径。暂停/结束前页面显示新参与/新发放、在途预留及已发有效券各自影响；规则保存/启用/暂停后宿主重读主状态。
10. M9 按优惠库存、待处理、履约记录与异常呈现。具体业务记录链接携带 campaign/benefit/return 到原店员台；打开链接不办理，不复制扫码/核验/核销流程。店员组已冻结支持这三个受控参数。更正仍经原权限与追加记录端点，取得原因，不删原事实。
11. 金额以元输入/展示、内部仍整数 minor；日期选择与展示明确北京时间 Asia/Shanghai UTC+8，不再随操作设备时区漂移。库存未来使用期明确暂不可参与领取，继续保留原 inventoryUsable 同时约束预留和新发放的合同，不提前鼓励顾客开始任务。
12. M9 账本失败保持错误态，不把失败画成空库存或无履约义务；换活动请求防旧响应覆盖，刷新库存不卸载成功后的“另准备一批”入口。外部团购操作反馈恢复在页签外可见，标题去开发票号，金额/日期改业务单位，授权与外部结果仍由原适配器裁决。

## 已批准最窄成本事实迁移 0134

`reward_inventory.cost_recorded` 与 `reward_benefits.cost_recorded` 新增布尔事实，不替换现 cost_minor，不新增账本。

- 旧 cost_minor>0 迁为已录；旧 0 无法证明真实零，保持未知。
- 新业务请求省略 cost_minor = 未知；显式 cost_minor:0 = 已录真实零。
- 原 Go 内部调用正成本继续兼容为已录；权益从库存预留/发放时冻结 cost_recorded，之后沿同一权益行办理。
- M9 单笔、权益汇总保留未知；不把券面额当成本。
- 统计当前 `unrecorded_batches` 读取当前库存标记；另加 `reward_cost_unrecorded` / `redeemed_cost_unrecorded`，分别按当前过滤条件与 issued_at/redeemed_at 窗口读取冻结权益标记。费用完整性以对应窗口冻结事实判断；当前批次未知数不再冒充历史发放/核销成本完整性。
- 修复已集成 0133 的 schema expected 表名漏登 content_production_offers；仅补清单，不掩盖其他故障。

## 删除字段/步骤及权威来源

| 原输入/步骤 | 原用途 | 正常流程中的权威来源/替代 |
| --- | --- | --- |
| 权益 slot | 关联奖励批次与规则 | 原活动 publish-config.reward.slot；新建默认 first_publish，历史值复用 |
| 规则版本 | 对齐批次与冻结规则 | 当前保存配置 version；服务端检查与派生 |
| 允许证据原始枚举 | 批次发放门槛 | 同一配置 verification_mode，服务端派生，不能扩大 |
| 内部门店 ID/空表示全店 | 核验与兑换门店范围 | 活动绑定门店/配置 store_ids；界面显示门店业务名称 |
| 规则快照引用 | 审计关联 | 服务端生成 publish-config:<id>/v<version>/<请求摘要> |
| 重复批次类型/币种/使用期 | 再维护规则 | 原配置 kind/currency/redeem_from/redeem_until |
| FEN、分、RFC3339 输入 | 存储金额/时间格式 | 元与明确 UTC+8 日期选择器，边界转换为原格式 |
| 人工参与/尝试引用与确认门店 ID | 定位现场办理 | 原待处理/权益记录到店员台的受控深链与扫码上下文 |
| Motion Brief/digest/重复制作步骤 | 旧工程制作交接 | 新内容工作使用统一 ContentEditor；旧记录显式兼容入口 |
| 重复门店跳转表 | 配置外部入口 | 门店工作区 page/entrances；活动宿主仅给上下文链接 |
| 外部可空平台商品 ID/展示图引用/币种字段 | 可选扩展元数据 | 正常新建保留平台、官方商品链接、业务标题、真实价格/有效期；原可选接口兼容，授权仍原权威 |

保留明确输入：顾客要做什么、核验方法、重复领取限制、权益与数量/成本、领取/使用期、缺货处理、实际成本是否已录、合法券码、异常原因。这些分别改变承诺或他人权益，不因去技术字段自动猜测。

## 复用和未改边界

复用唯一 publish-config 与 revisions、campaignrules 防重复规则、既有 30 目录/准入/方法限制、点评服务端禁奖、原 reward_inventory / benefits / operations / expiry_facts / summary、原六种期限裁决、原 store session 权限与门店隔离、原店员台及外部券适配器。

活动暂停/结束继续停止新参与及新发放；在途预留不是已发放，须按原冻结条件核对/恢复，不能笼统保证结束后继续发奖。已发未过期券继续绕过新参与闸查询/核销；本组没有改该保护。

## 验证与红绿证据

已读取实际工具退出码与日志，未以截图冒充外部链路。

- 红：TestUnchangedPromiseKeepsInventoryVersion 原保存使 1→2；修复后通过，原历史修订/启用 suite 继续通过。
- 红：门店日期 09:30 原按设备时区转成 17:30Z；改为固定 UTC+8 后精确为 01:30Z。元→分、未知成本省略、显式零及非法小数/负数回归通过。
- 红：TestCampaignPublishCannotSkipPromisePreflight 原直接 active 返回200；现422 promise_not_enabled，GET状态仍draft。已启用后暂停库存再转 active 仍422 benefit_stock_missing。
- 红：同请求库存重放原回 available_qty=0；现从原账本占用回读正确余量，不重建批次。
- HTTP：业务库存无需技术字段成功建立/重放同ID；越承诺、staff预算写入、伪造slot、raw旧接口绕当前规则均拒绝。
- 成本：0134 旧0/正数迁移、未知/真零经原 ReserveRewardBenefit→ConfirmRewardBenefit→RewardSummary 冻结与汇总回归通过；原并发核销、证据幂等、旧券保护在全库 suite 中通过。
- `source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh; cd server; go test ./...`：首次只失败旧 schema expected 漏表；补清单后全库退出0（httpapi 211.676s、store 164.946s，其余通过/缓存或无测试）。其后库存重放余量的最后局部修复再次运行 promise 三项 store 回归退出0；真实HTTP三项再次退出0。
- `cd web; npm test` 最终：114 文件、1158 测试通过（8.53s）。
- `cd web; npx tsc --noEmit --pretty false`：退出0；只有环境 npm http-proxy 已知提示。
- `git diff --check`：退出0。

日志同目录：campaign-go-final.log / campaign-web-final.log / campaign-ts-final.log / campaign-http-final.log / campaign-promise-final.log / campaign-db-final.log。

## 正常产品路径与独立浏览器审查准备

1. 从 `/` → 活动 `/work/campaigns` → 新建；已有门店可通过 `/work/campaigns/new?store=<系统门店引用>` 自动带入。普通服务点击门店工作区，不落新活动。咨询目标只填该次说明/时间并生成链接。
2. 推广优惠目标：填写当前门店/标题/顾客内容/活动时间 → 统一内容编辑器上传/选材/制作、预览采用（候选）→ 同一活动承诺选择获准渠道/核验、数量成本/期限/条件 → 保存草稿 → 下方准备关联库存 → 启用原预检 → 生成链接并发布。
3. 活动详情 `/work/campaigns/<id>` 主状态来自服务端 availability；库存入口 `/work/rewards?campaign=<id>` 选中同活动，展示同承诺摘要。成功后“另准备一批”可在剩余承诺上限内补库存，改承诺必须回唯一编辑源。
4. 未启用、缺库存/暂停库存、未准入/点评渠道、未到活动/领取/使用期、过期、无权限、加载失败分别反向复验；不能通过改库、隐藏入口或人工证据升级为官方来成功。
5. 在同一业务记录中去店员台（campaign/benefit/return），确认发放和核销由该组原事务负责；已结束活动仍可在M9找到已发券办理。更正保留原事实和原因。
6. 浏览器需使用原 E2E/server 正常门店与内容/准入准备入口；HTTP测试夹具是隔离工程证据，不能直接冒充真实用户界面路径。root后续以 gpt-6.1-terra 从上述正常入口进行桌面、390/430px、刷新返回及对抗走查，当前尚未完成此层证据。

## 证据分层/遗留与外部阻断

工程层：上述 Go真实HTTP/事务、Vitest、TypeScript 通过；没有改库造页面成功。当前代码可以通过原本地端点完成新增正常流程。
跨模块层：统一内容编辑器、同源规则/库存与店员受控深链已接线；root 合并所有组后仍需同门店、同活动、同记录的真实浏览器审查，未宣称已通过。
外部正式层：未验证真实 Identity/平台发布官方回执/正式 Billing或制作生产方/手机/NFC实卡/外部券授权和超时回查。沿原适配器诚实返回未授权、待确认；本组无能力以本地成功替代这些证据。

已告 customer 组：本组未修改 public handler；公共C4应读同一配置与冻结参与快照，不能继续硬编码核验或另造规则。管理availability可供其按同一Window+Check在公共面最窄复用。cost_recorded合同已发root与customer，店员深链参数已与staff组冻结。

## 源码文件清单

- `server/internal/dashboard/funnel.go`
- `server/internal/db/db_test.go`
- `server/internal/db/migrations/0134_reward_cost_recorded.sql`
- `server/internal/httpapi/handlers_business.go`
- `server/internal/httpapi/handlers_campaign_config.go`
- `server/internal/httpapi/handlers_dashboard_ops.go`
- `server/internal/httpapi/handlers_reward_ops.go`
- `server/internal/httpapi/server_campaign_config_test.go`
- `server/internal/store/store_campaign_promise.go`
- `server/internal/store/store_campaign_promise_test.go`
- `server/internal/store/store_campaign_publish_config.go`
- `server/internal/store/store_dashboard_ops.go`
- `server/internal/store/store_reward_ops.go`
- `web/src/app/work/campaigns/[id]/page.tsx`
- `web/src/app/work/campaigns/page.tsx`
- `web/src/app/work/rewards/page.tsx`
- `web/src/components/work/analytics/analytics-view.tsx`
- `web/src/components/work/campaign-config-panel.tsx`
- `web/src/components/work/campaign-create-wizard.tsx`
- `web/src/components/work/campaign-inventory-prepare.tsx`
- `web/src/components/work/campaign-legacy-history.tsx`
- `web/src/lib/analytics.ts`
- `web/src/lib/campaign-config.ts`
- `web/src/lib/campaign-create.ts`
- `web/src/lib/reward-ops.ts`
- `web/tests/analytics-render.test.ts`
- `web/tests/campaign-config-panel.test.ts`
- `web/tests/campaign-promise.test.ts`
- `web/tests/content-packs.test.ts`
- `web/tests/motion-handoff-brief.test.ts`
- `web/tests/mx-ops-production-panel.test.ts`
- `web/tests/reward-ops-page.test.ts`
- `web/tests/reward-ops.test.ts`
- `web/tests/store-motion-note.test.ts`
