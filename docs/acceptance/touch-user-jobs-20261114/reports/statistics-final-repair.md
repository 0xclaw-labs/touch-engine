# HUI-4665 末轮统计缺陷修复

- 日期：2026-11-14；实施树：`.worktrees/statistics-final-repair`；分支：`codex/touch-statistics-final-repair`。
- 基线：`1ca6647`；本地提交：`cc2c7ee043d34dede3ae2289ac383fd68dbbd4ae`。
- 已读取本轮 constraints、HUI-4665 正文与 final-adversarial-review；仅处理原审查动态复现的两项统计缺陷及其直接派生的披露契约。未修改 Linear、原浏览器业务库，未 push、PR、merge。

## 根因与实际改动

1. **历史内容版本错归属**：原内容选择按可变 `participations.content_pack_version` 筛，实际却统计任意非空 attempt。改为过滤 `participation_attempts.content_pack_version`；保持按参与去重、匹配后最早事件落窗。凭据交接沿 `handoff_credentials.attempt_id` → 同租户、同参与的准确 attempt 做 EXISTS 版本筛选，无绑定或错绑定不会借当前选择追认。
2. **无历史版本证据的事实不冒充可筛**：参与开始、权益证据、发放、核销与权益成本无不可变的内容版本绑定，移除当前 participation 版本筛选及 `applied_filters` 声明。数字保留原作用域事实，前端明确内容版本筛选未生效。旧官方查询引用 `customer_publish_attempts`，其中 `content_version` 与内容包版本标识不是同一合同，不猜作包版本。
3. **保留真实可关联的发布快照能力**：六层中来自 `publish_handoffs` 或其准确关联事件的计数按原生冻结 `content_pack_version_id` 筛；混合层新增逐 count 的 `applied_filters`，层级只声明各行共同适用的交集，前端逐项说明，不暗示权益或旧自报已按版本收窄。
4. **库存批次越店**：统一库存和未录成本批次可见范围。受限范围必须有同租户活动：显式批次门店匹配且活动门店匹配或未绑定；空批次门店须由活动明确归属本店。活动缺失、跨租户、门店冲突均拒绝。两者均未绑定不能证明共享授权，仅总部保留租户批次视图。
5. **库存状态事实越店**：库存 LEFT JOIN 的权益加同租户、同活动约束；受限视图另按权益自身 `store_id` 限制预留、发放、核销、撤销和过期，空或他店事实不进入聚合。总部保留完整本租户账本计数。
6. **不虚造全批次可用量**：受限视图无法证明全部占用时返回 `available: null` 和 `available_unknown: true`；前端显示“可用未知”并说明本店状态数不能推算全批次可用量。总部已知可用量仍为原总额度减完整占用。该只读标记不改变库存或发放权威，不新增可用量真源。

## 文件

- `server/internal/store/store_dashboard_ops.go`：冻结版本关联、批次归属及权益聚合范围。
- `server/internal/store/store_dashboard_ops_attribution_test.go`：新增隔离固定事实回归。
- `server/internal/store/store_dashboard_ops_test.go`：更正原先把空门店假定共享及当前版本用于历史开始的错误预期。
- `server/internal/dashboard/funnel.go`、`funnel_test.go`：适用维度、逐 count 披露和未知可用量 JSON 契约。
- `server/internal/httpapi/handlers_dashboard_ops.go`：可用量完整性事实映射。
- `web/src/lib/analytics.ts`、`web/src/components/work/analytics/analytics-view.tsx`、`web/tests/analytics-render.test.ts`：新字段兼容解析及真实渲染回归。

## Red → Green

先写测试并运行，未先改生产代码：

- `TestDashboardOpsFrozenVersionAttribution`：唯一 participation 当前 v2、attempt 冻结 v1，筛 v1 实际 0/0（选择/凭据）、预期 1/1；筛 v2 实际 1/1、预期 0/0；无不可变版本事实被错误筛掉。
- `TestDashboardOpsHandoffVersionRequiresExactAttempt`：无 attempt 或指向另一 participation 的凭据，筛 v2 错误得到 1。
- `TestDashboardOpsInventoryStoreAttribution`：店 B 收到店 A 的“开业券”100额度；未归属及冲突批次也泄漏；未录成本批次数同样超出范围。
- `TestDashboardOpsInventoryBenefitsKeepStoreAndTenantScope`：店 A 库存行错误包含他店 redeemed=1 与空门店 reserved=1。
- `TestDashboardOpsMultipleFrozenAttemptsAndPublishSnapshots`：同参与多冻结版本、跨窗重试与两个发布快照时，发布/事件原本无版本过滤（old/new/current 均错误计2）；当前选择使 new attempt 错计0。
- `TestContentVersionOnlyAppliesToImmutableFacts`：开始、权益等错声明版本适用，发布快照计数缺逐行适用声明。
- `analytics-render` 两条红测：逐项适用说明缺失；受限视图仍显示猜测的可用99及“全店共享”。

修复后上述全部转绿，并追加：同参与同版本多attempt/多凭据不重复计；总部两个参与计2、门店计1；缺失或外租户活动在受限视图拒绝而总部保留批次；外租户 benefit 不进入库存；available 未知 JSON 不序列化为猜测数字。

## 实际验证

环境：`source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`；`go version go1.26.5 linux/amd64`；复用原 node_modules，无提权或重新下载浏览器。

- `go test ./internal/store ./internal/dashboard ./internal/httpapi -count=1`：通过（store 76.150s、dashboard 0.007s、httpapi 117.264s）。末尾追加的边界测试和纯函数调整另运行下项确认。
- `go test ./internal/store ./internal/dashboard -run 'TestDashboardOps(Inventory|MultipleFrozen|FrozenVersion|HandoffVersion)|TestContentVersion|TestScopedInventoryAvailability' -count=1`：通过（store 1.472s、dashboard 0.005s）。
- `vitest run tests/analytics-render.test.ts tests/analytics.test.ts tests/analytics-page.test.ts`：3文件58项通过。
- `tsc --noEmit`：退出0，通过。
- `npm test` 全量：119文件通过、1文件失败；1187项通过、1项失败。唯一失败：`tests/mx-ops-visitor-zero-generation.test.ts:43` 的“顾客公共快照只读已采用版本，不经制作队列”，仍检查 `handlers_content_packs.go` 源字符串 `GetAdoptedVersion`。当前基线已切 `campaignPublicVersion`，本提交未触碰内容生产写面。root已确认由 campaign-live 组修正实际已发布版本路径断言，待统一集成验证。本报告不将全量称绿。
- 提交前 `git diff --check` 通过；提交后树干净，9文件均属统计写面。

## 留给原审查的复验入口和边界

正常产品入口：工作台 → 经营统计 `/work/analytics`；内容包版本筛选应在内容选择、准确凭据交接和发布快照计数生效，开始/权益等逐项显示不适用；核验证据与权益成本摘要也明确版本不适用。受限门店仅能看到可证明归属的批次和本店权益状态，可用量未知解释可见；总部完整账本数字保留。

本次数据准备均为 Go `t.TempDir()` 创建的独立 SQLite 固定事实，不改用户原浏览器 DB，不通过改库制造用户成功结果。仅缺失活动防御用例在该隔离测试库暂时关闭外键以模拟历史删除/损坏残留，随后恢复；不属于正常业务播种或成功验收。

尚未执行本修复集成后的正常浏览器复验、390/430px与桌面逐行说明排版、真实撤权回焦及正式平台数据；由 root 交回原独立审查沿已有正常入口复验。统计口径不证明平台发布、真实支付、收件、成交或ROI。API 对无法证明的 scoped `available` 现在为 null（正确未知），使用新布尔标记兼容识别；其他旧消费者若假定永远为数值，需要遵守已有未知契约。未引入“空门店=共享”的新授权合同；若以后正式支持跨店共享批次，应由原权威提供可验证共享范围后再开放。
