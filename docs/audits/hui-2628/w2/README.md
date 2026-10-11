# HUI-2628 W2 复扫证据 · 2026-10-12

冻结 detector（`_worktrees/ui-r5-20261008/cache/rerun-2625/cmd/uifinish-scan`，与 rerun-1 同版本同 route table `docs/audits/hui-2625/routes-p0.json`）对本分支复扫。

- 命令：`GOWORK=off go run ./cmd/uifinish-scan -routes docs/audits/hui-2625/routes-p0.json -repo … -repo touch-web=<touch-engine> -ref c7c42aa -out <out>`（scanned_sha = `c7c42aa`，本分支 W2 组件化提交）。
- 基线：rerun-1 `rescan/scan-touch-web.json`（scanned_sha `946c1d1`）。

## Verdict（6 条 touch 路由）

2 pass / 4 fail——与 rerun-1 相同的通过集（/ 与 /c/[code]/contact pass），无路由由 fail 转 pass，也无由 pass 转 fail。**本切片不宣称 finish 全绿；全绿与否归 2625 gate owner。**

## 锚点 delta（root 终裁的两处 off_ds）

- `/work/campaigns`：L89 `off_design_system_table_form_button`（错误态裸重试钮）**已消除**——错误态改由 `web/src/components/work/load-error-state.tsx` 承载，路由文件不再手写该按钮。
- `/work/materials`：L93 同款发现**已消除**，同上。
- 其余 off_ds/unstyled findings 为行号平移（两文件各减少 3-4 行），数量差恰好等于被移除的锚点行；列表行按钮、挂素材表单等未动，与 W2 计划 Q10 一致。

## 如实记录：新出现的 `missing_loading_empty_error`（主页面缺少 error）

`/work/campaigns` 与 `/work/materials` 各新增一条 `missing_loading_empty_error`。原因：detector 按页面源文件文本查找错误相，而错误相标记随组件化移入 `load-error-state.tsx`。行为证据：渲染输出类名/属性不变（vitest 326/326，其中 source 守卫改为沿「路由或其 LoadErrorState 生效来源」检查，强度不降）；e2e 错误态截图仍走真实渲染。**该发现按 detector 口径如实保持 fail，是否按「生效组件树」口径改判由 2625 gate owner / 2619 终审裁定（与 2627 `sourceTenantId:null` 字段名误报同类议题），本切片不自行宣布 pass。**

## 其余残留（未动，留档）

- `/admin`：hand_filled_tenant_or_internal_id 大量命中（known-issues 升级的结构修复票，rerun-1 root 终裁范围，非本切片）。
- `/work/campaigns/[id]`：hand_filled（同上结构项）+ 行动按钮 off_ds（未列入 W2 锚点）。

## 测试

- `cd web && npx vitest run`：35 files / 326 tests 全绿（基线持平，守卫沿生效来源）。
- E2E 证据门 `bash web/e2e/run.sh`（全量模式）：**PASS**——15/15 用例、验收截图 48/48、axe critical+serious=0、skipped=0（`e2e-run.log`）。
- 首轮全量曾 14/15：`10-state-scenarios` 商家离线段失败，根因是组织登录改造（本 PR 前波已合提交）后门面引导先打 `session/memberships`、whoami 跟随，spec 仍只中止 `**/api/whoami`，门面正确落入匿名登录态而非离线错误。fixture 改为同时中止 memberships+whoami（等价「连不上服务」）；断言文案、重试闭环与截图目标一字未动。属 fixture 对陈旧内部耦合的修正，非降门槛。
