# Touch-shell 双轴 review（HUI-2628 / HUI-2222 / HUI-2227）

范围：`main@f9d5af7...eco/hui-2628-touch-shell`
方式：本轮受委派深度限制无法派并行子代理；按 `code-review` skill 的两条轴线**分别**逐项自审（不是合并成一条），审查后一次性修复 Critical/Important。

## Standards

| # | 发现 | 判定 | 处置 |
|---|---|---|---|
| S1 | 创建向导的草稿保存 effect 在读取 localStorage 前可能先写空草稿 | Important（正常路径丢草稿风险） | 已修：`hydrated` ref，读取完成前不写 |
| S2 | 详情页草稿态时分享组件也画实心“复制分享文案”，与“发布活动”两个主行动 | Important（票面每步一个主行动） | 已修：分享 primary 仅在 `status==="active"` |
| S3 | 移动端 `.tk-input/.tk-select` 命中高度未显式 ≥44 | Important（touch density） | 已修：≤430 下 `min-height: var(--pn-control-height-touch)` |
| S4 | 向导复制文案直接 `navigator.clipboard?.writeText(...).then`，无剪贴板时不反馈 | Minor | 已修：显式 `copyShare()` + 失败文案 |
| S5 | `EcoTopNav.switchTo` 对注入的 `onSwitchApp` 抛错未 catch | Minor | 已修：catch + 错误条 |
| S6 | `LinkRec`/`ShareLink` 用交叉断言携带 `id`，`CampaignShare` 里 `(link as ...).id ?? ""` | Minor（类型噪声） | 延期：两个组件的本地视图类型后续合并；当前运行时 id 恒在 |
| S7 | 多个 `statusLabel` 映射分散在列表/详情 | Minor（重复） | 延期：列表/详情用途不同，先保留 |
| S8 | 无 inline style、手写 CSS 裸 hex 0、无硬编码生态域名 | 通过 | — |
| S9 | 公共页仍保留既有的留资、私域/跳转区块 | 通过（baseline，不是本轮新增） | 未删除；票面只要求不新增后台/生态导航 |

Fowler baseline：未见明显 Divergent Change / Shotgun Surgery；`campaign-create.ts`/`campaign-share.ts`/`public-time.ts` 为纯函数，页面只组装；`use-app-switch` 把网络 seam 从渲染器移出，避免 Feature Envy。重复的 URL/copy 逻辑已集中到 `campaign-share`/`use-app-switch`。

## Spec

| # | Spec 要求 | 实现 | 判定 |
|---|---|---|---|
| P1 | 门店/地点上下文 | 第①步真实 `/stores`，无门店引导登记；服务端无门店参与 fail-closed | 完成 |
| P2 | 活动与内容（受权素材选择/预览、AI 标注） | 第②步真实内容包面板 + 素材页入口；`marking.ai_generated` 商家/顾客/交接展示；原始素材选择仍走既有素材页 | 部分完成（R1） |
| P3 | 链接/短码生成与分享 | 第③步生成短码 + 启用；详情分享文案/链接/二维码/停用短码；同源 URL | 完成 |
| P4 | 发布/结束状态 | 中文状态 + 窗口过期 + 唯一主行动；结束不自动改状态 | 完成 |
| P5 | 丢失草稿/素材权限/发布状态/短码冲突/预览 | 草稿双层、素材 404/503 文案、发布状态、短码重试、日期/分享预览 | 完成 |
| P6 | 顾客页只参与/预览/发布/奖励，无后台导航 | `/c/*` 无 EcoTopNav/AdminShell；主行动门控；能力矩阵折叠 | 完成（保留既有留资/私域区块，R2） |
| P7 | 诚实 expired/invalid/limit/reward | 五态 + capabilities + 限领 `data-state=limit_reached` + 奖励未知不绿 | 完成 |
| P8 | 共享内容版本与商家选择一致 | 参与开始钉 version；snapshot 带 `version_id`，409 mismatch；UI 不静默换版；请求竞态守卫 | 完成 |
| P9 | 语义 token + page-pattern | 手写 CSS 全 token 化；六 surface 对比度自算 138 行 0 fail；catalog vendor + 四 pattern 映射 | 完成（静态门，R3） |
| P10 | EcoTopNav 真实模型、server-side 会话/品牌/租户、blocked/offline、不硬编码域名 | `/api/eco-nav` 转发 cookie/tenant；无配置/失败→blocked；fixture 只留 preview；渲染器不 fetch/不拼 URL | 完成（真实 public-ai 端点未运行，R4） |
| P11 | 应用切换 BFF → shell/launch → 校验 launch_url | 渲染器→`POST /api/eco-nav/switch`→Go→identity shell；web 按 registry 同源+路径校验；失败闭合 | 完成（真实 shell 未运行，R4） |
| P12 | 账号/用量复用既有 wiring，无第二钱包 | 未新增钱包/发券；EcoNav 只显示既有 billing 文案 | 完成 |
| P13 | Touch↔Leads/GoBoost handoff 带精确版本与返回；普通切换不带业务数据 | matrix-draft 带 activity_version/asset/hash/grant/return token；切换 body 仅两 id；线索 payload 既有 trace/version 保留 | 完成 |
| P14 | 两轴 review + 修复 | 本文 + 修复提交 | 完成 |

### Rulings（自答，未停问人类）

- **R1（P2 部分）**：第②步不重做原始素材选择器，复用既有素材库/内容包；新活动第②步空态给真实入口。代价：若素材库/上传关闭，创建流只能先建链接，顾客看到“还没有准备好在线内容”。已在证据 NOT_VERIFIED 记录。
- **R2（P6 baseline）**：既有留资、私域、跳转区块保留；本轮只折叠发布能力墙，不删除既有业务。代价：公共页仍比最小参与流长；若票面要求“只允许四类流”，需另开删除票。
- **R3（P9 静态）**：对比度门只做 token/静态 CSS；图片/视频背景、原生端不测，未宣称 runtime 通过。
- **R4（P10/P11 真实上游）**：public-ai eco-nav 生产端点和 shell launch 在本机不可用；用 identity stub 验证本仓链路，不用 fixture 进入正常页。代价：登录后本机不显示顶栏（blocked），真实 SSO 落地未验证。
- **R5（S6）**：类型噪声延期，不影响运行时；不为了 review 做无测试的跨组件重构。

## 修复后复验

- `npx vitest run`：76 files / 719 tests 通过（review 后重跑）。
- `GOWORK=off go test ./...`：见证据；一次全量运行出现既有 `TestCacheRecomputeConsistencyAndBoundedStaleness` 在机器高负载下的 timing 计数 flake（期望 7 得 8，单测重跑通过），与本轮 diff 无关，证据如实记录。
- `node scripts/contrast-gate.mjs`：138/0，最低 5.58。
- `npm run build`：exit 0。
- e2e：01/12/13/14 复跑通过。
