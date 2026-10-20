# 碰一碰 · 商家创建流 / 顾客公共页 / 设计语言 / 生态导航（HUI-2628、HUI-2222、HUI-2085/2227 consumer）

日期：2026-10-21
工作树：`wt/touch-engine/mb-eco-touch`
分支：`eco/hui-2628-touch-shell`（基线 `main@f9d5af7`）

## 0. 本轮要解决的问题（不是重做）

上一轮已经把页面铺齐：TodayDesk、八代表页、公共页五态、EcoTopNav 组件、内容包/素材库/发布交接/奖励状态。本轮不推翻这些，只修**正常用户走不通的路径**，并把设计语言/导航接入从“夹具能看”推进到“真实权威能接、缺权威就诚实降级”。

已用真实本地栈（identity stub + Go 18460 + Next 18462 + e2e seed）看到的事实：

1. `/work/campaigns` 的新建是一张裸表单：RFC3339 手填、无日期选择、无门店上下文、无素材、无 AI 标识、无短码/分享；创建后停在列表，用户要自己再找活动、点“生成链接”。
2. `/work/campaigns/[id]` 同时有两个实心主行动（“启用”“生成链接”），状态显示 `active` 英文原值；“为本活动制作图片”按钮只发 `{campaign_id}`，路由要求 trace/asset/sha/grant 等字段，**必然 400**，是一条死路径。
3. `/api/eco-nav` 在没有 `PUBLIC_AI_ECO_NAV_URL` 时返回**预览夹具**，登录后的真实商家页因此显示“数海观澜 / 客户甲 / 付款主体需确认”等假上下文；网络失败也回夹具。这是正常路径上的假服务结果。
4. EcoTopNav 的“应用”是静态 `<a href>`，直接用 registry URL，**绕过 shell/launch SSO**；没有 `POST` app-switch BFF。
5. 公共页在 `FEATURE_PARTICIPATION` 默认关闭时仍显示实心“选择内容并参与”，点击得到 404。页面没有任何“本店当前开不开在线参与”的权威信号。
6. 顾客内容版本：`content-snapshot` 只按 `pack_id + platform` 回落“当前采用版”，不校验参与时选中的 `content_pack_version`；商家换版后顾客可能看到与参与记录不一致的内容，且无提示。
7. 内容包已有 `marking.ai_generated`（AI 合成/广告/激励标识），公共页与商家内容面板都没有展示。
8. 公共页直接显示 `2026-10-19T18:32:36.946Z` 这种 ISO 串；手机上占两行，不是顾客语言。
9. `RewardStatus` 的“出示参与码”在成功设置 `qr_payload` 后仍用闭包旧值判断，**总是同时显示“参与码暂时签发不出来”**。
10. 公共页把旧的 1670 能力矩阵整块展开，390 宽下页面 3111px 高，发布说明压过参与与领奖。
11. 语义 token 已 vendor，但 `globals.css`/`touch.css`/EcoNav CSS 仍以 `--tk-*` 硬编码为主（`--tk-ink-2`、`--tk-ink-sub`、`--tk-danger`、`--tk-warning-bg`、`--tk-ok` 未桥接）；没有可执行的对比度门。
12. 短码 `CreateLink` 直接插入唯一键，碰撞没有重试；主行动“生成链接”在极小概率冲突时会 500。

## 1. Brainstorm Q/A（Lead 预答 + 仓库证据，非人类实时问答）

**Q1：这一轮最重要的是“更多功能”还是“正常路径走通”？**
A：正常路径。票面验收是“门店/地点上下文 → 活动与内容 → 链接/分享 → 发布/结束”，不是再堆面板。删掉挡路的重复与死按钮，比新增页面更重要。

**Q2：创建流必须是单页表单还是分步？**
A：分步，但不要让用户填技术字段。步骤是 ①门店与活动 ②内容与素材 ③链接与发布；每步一个实心主行动；日期用本地 `datetime-local`；门店从真实门店列表选，没有门店先引导登记。

**Q3：草稿丢失怎么根除？**
A：两层。第一层前端 localStorage 自动存草稿（租户隔离、版本化、成功创建后清除）；第二层第①步“保存草稿并继续”立即在服务端落 draft，后续步骤围绕这个 draft 进行。导航/刷新/换页不丢。

**Q4：素材与 AI 标注从哪来？**
A：素材库 `GET /assets`（授权声明 source/purpose/grant_ref/ai 由内容包 `marking.ai_generated` 承载）。素材库关闭时显示真实 404/503 原因并允许“本次不附素材”，不伪造素材。AI 标注以内容包 `marking.ai_generated` 为唯一真值，商家预览与顾客快照都必须显示；素材来源 `aicut` 额外提示“AI 成片”。

**Q5：链接与分享怎么算“正常路径”？**
A：发布步骤生成短码并给出同源 `/c/<code>` 链接、可复制的分享文案、二维码。域名不硬编码，取自 `window.location.origin`。停用短码不得继续给分享按钮；活动结束/过期要明说链接会显示“已结束”。短码冲突服务端重试。

**Q6：顾客页要保留多少旧能力？**
A：只保留参与、预览、用自己的账号发布、奖励/核验四类。旧的 1670 能力矩阵保留“可用/不可用 + 一条原因”，不再整页展开；没有权威能力信号时不显示主行动。公共页永远不挂 EcoTopNav/AdminShell。

**Q7：内容版本怎么保证一致？**
A：`content-snapshot` 增加可选 `version_id`；传入时必须等于该活动该平台当前采用版，否则 409 `version_mismatch`。页面把参与里的 `content_pack_version` 传下去；版本不符时显示“你参与时选的内容已不是门店当前版”，提供换选（未交接时）或请店员协助，绝不静默换成新版。

**Q8：EcoTopNav 没有 public-ai 生产者端点怎么办？**
A：没有配置/网络失败/契约不符一律返回契约合法的 **blocked unavailable** 模型，不渲染顶栏，绝不回退预览夹具。夹具只允许出现在 `/admin/preview`（显式预览页）。真实模型从 `PUBLIC_AI_ECO_NAV_URL` 读取，带会话 cookie 与 `x-tenant-id`，严格 parse。

**Q9：应用切换怎么接？**
A：渲染器只认 `launch_target_id`，不拼 URL。点击 → `POST /api/eco-nav/switch {app_id, launch_target_id}` → 本仓 Go BFF → public-ai `POST /internal/v1/identity/shell/launch` → 返回 `launch_url` → Web 侧按 vendored registry 校验 scheme/host/port/path 前缀 → 跳转。缺配置/未注册/校验失败一律 fail-closed，不伪造地址。切换载荷只有两个 id。

**Q10：任务型 handoff 与普通导航的边界？**
A：矩阵草稿/线索交接走业务 CTA，携带活动 id、活动版本、素材 ref/sha、授权 ref、返回目标；普通应用切换只带 `(app_id, launch_target_id)`，不建 handoff、不带 campaign/asset/payer。

**Q11：设计语言照谁？**
A：GoBoost 的密度与层级：商家页信息密度高、一个主行动、状态用弱色块；顾客页单列、触控 ≥44px、少解释。颜色/间距/字体只走 public-ai semantic tokens 的 `--pn-*` 角色，`--tk-*` 仅作为兼容别名指向 token，不再有新的裸色值。

**Q12：本地验证边界？**
A：public-ai 的 eco-nav 生产端点与 shell/launch 在本机没有挂载；本机 identity stub 可模拟 launch 合同。素材库需要平台 upload，本机未开。能跑的真实流程：创建草稿→（素材库关闭时诚实跳过）→生成短码→分享→启用→公共页；内容包可开开关并用真实 Go API seed；参与/奖励默认关闭时验证“不显示死 CTA”。不能跑的步骤如实写 NOT_VERIFIED。

## 2. Rulings（本轮记录，不再问人）

- **R1 页面范围**：新增 `/work/campaigns/new`（creation pattern），列表页只保留列表 + 一个“新建活动”主行动；不重建 `/admin`。
- **R2 状态词**：商家页状态一律中文（草稿/进行中/暂停/已结束/窗口已过），不显示 raw `active`；`data-state` 保留机器值供测试。
- **R3 主行动**：每个步骤至多一个实心 `.tk-primary`/`.tk-button[data-primary-action]`；创建流第①步“保存草稿并继续”，第③步“生成链接并发布”。
- **R4 内容版本**：快照请求带 `version_id`；服务端校验当前采用版，不匹配 409；页面显示 mismatch，不回落。
- **R5 AI 标识**：唯一真值是内容包 `marking.ai_generated`；商家面板、顾客快照、发布交接快照都显示“含 AI 合成内容”标签。
- **R6 公共能力门**：`GET /public/links/{code}` 增加 `capabilities`（participation/content_packs/reward_ops/leads_capture/publish_reward 的布尔值，全部来自服务端配置）。页面只在 true 时显示对应动作。
- **R7 导航失败闭合**：无配置/不可达/契约不符 → blocked unavailable；禁止 fixture 进入登录后正常页。
- **R8 应用切换**：只走上文 BFF；不直接使用 registry URL；`launch_url` 必须与目标登记 URL 同源且路径前缀一致。
- **R9 短码冲突**：`CreateLink` 对唯一键冲突重试（有界），耗尽返回明确错误；不返回 500 含糊失败。
- **R10 对比度门**：脚本自行计算 `resolved.json` 中本票页面实际使用的 token 对（不读取上游 `verdict` 当结论），文本 ≥4.5、非文本 ≥3；任一不达标 exit 1。
- **R11 不做**：不发券、不新增钱包、不伪造发布回执、不改 public-ai 生产 manifest、不 push、不部署、不改 Linear。

## 3. 商家创建流 IA

```
/work/campaigns
  ├─ 列表（全部/草稿/进行中/暂停/结束/窗口已过）
  └─ [新建活动] → /work/campaigns/new
       ① 门店与活动
          门店（真实列表；无门店→引导登记；必须选）
          标题 / 顾客能看到的内容 / 开始 / 结束（datetime-local）
          [保存草稿并继续]  ← 唯一主行动；服务端已落 draft
       ② 内容与素材
          内容包（该 draft 的 packs/versions/adoptions）
          AI 合成标识 / 授权声明 / 预览文案
          素材库关闭：真实原因 + “本次不附素材” + 去素材页
          [继续：链接与发布]
       ③ 链接与发布
          复核摘要（门店/时间/内容/AI）
          链接：已有启用短码则复用；否则生成
          分享文案 + 复制链接 + 二维码
          [生成链接并发布]  ← 唯一主行动（创建链接 + 启用）
          [先保存草稿，稍后发布]（次级）
```

规则：

- 第①步提交前客户端校验标题非空、内容非空、结束时间晚于开始时间且不在过去；错误就地显示，不跳步。
- 草稿自动保存 key：`touch.campaignDraft.v1:<tenantId>`；创建/发布成功清除；点“清空草稿”显式清除。
- 第②步内容包读取失败时区分 403/404/5xx，用 `failureText` 产品语言；不把失败当“没有内容”。
- 第③步链接生成失败：活动仍是草稿，页面显示失败与重试；不谎报已发布。
- 发布后跳 `/work/campaigns/<id>?created=1`，详情页顶部显示分享区与发布状态。

## 4. 顾客公共页规则

- 路由 `/c/[code]`、`/c/[code]/contact` 保持公开、无会话、无 Admin/EcoTopNav。
- 状态：`available / not_started / expired / paused / ended / link_disabled / not_found / network_error`；每个非可用态都有下一步。
- 内容：门店 → 价值/时间（本地化，不显示 ISO）→ 参与动作（能力门）→ 内容预览/换选（版本一致）→ 用自己账号发布/交接 → 奖励/核验。
- 主行动：未参与时“选择内容并参与”（仅 `participation=true`）；参与后“用我自己的账号发布”；奖励区不出现“自己让奖励通过”的按钮。
- 移动：单列、触控目标 ≥44px、按钮不并排挤压；能力矩阵折叠为一句可用/不可用摘要 + 详情。
- AI：`marking.ai_generated` 显示“含 AI 合成内容”；广告/激励标识同样展示。
- 分享：公共页不显示商家后台链接；页脚只保留品牌技术支持。

## 5. Token / page-pattern 映射

| 页面 | pattern | surface/theme | 主要语义角色 |
|---|---|---|---|
| `/work/campaigns` | `list` | `work.light/light` | `text.primary/secondary/muted`, `status.*`, `brand.*`, `border.*` |
| `/work/campaigns/new` | `creation` | `work.light/light` | 同上 + `control.height.touch`, `spacing.*`, `radius.*` |
| `/work/campaigns/[id]` | `detail` | `work.light/light` | 同上 |
| `/c/[code]` | `public_consumer` | `public.consumer/light` | `text.*`, `brand.*`, `status.success/warning/danger`, `control.height.touch` |
| EcoTopNav | （组件，不单独占 pattern） | 当前 surface | `bg.surface`, `border.default`, `brand.*`, `focus.*` |

落地方式：

- `globals.css` 只保留 reset 与 `--tk-* → --pn-*` 兼容别名（不再出现裸色值）。
- `touch.css`/EcoNav CSS 的颜色、圆角、间距、焦点环改走 `--pn-*` 或别名。
- `web/scripts/contrast-gate.mjs` 从 vendored `resolved.json` 读取六组 touch surface/theme，按本票页面实际使用的 12 组 pair 自行计算比值，输出 JSON + 失败退出码。
- `page-patterns`：vendor `catalog.json` 到 `web/src/contracts/page-patterns/v1/`，`web/src/lib/page-patterns.ts` 维护路由→pattern 与 required regions；测试校验 catalog 指纹、pattern 存在、region 完整。

## 6. 导航 / 启动接线

```
browser EcoTopNav app button
  POST /api/eco-nav/switch {app_id, launch_target_id}
    → web BFF (session cookie + x-tenant-id)
    → Go POST /api/v1/eco-nav/switch
       - requireSession；app_id ≠ current app；target id 形状校验
       - identity.Client.LaunchShell(principal, tenant, app, target)
    → public-ai POST /internal/v1/identity/shell/launch
    → {launch_url}
  → web validates launch_url against registered target URL (https/origin/path prefix)
  → 200 {launch_url}; client location.assign
```

- `/api/eco-nav`：缺配置 → `blockedUnavailableEcoNav()`（200，state=unavailable，reason=authority_unavailable）；上游非 2xx/契约坏 → 同；绝不 fixture。
- AdminShell：model 为 blocked 时 `EcoTopNav` 返回 null，页面照常可用；错误不阻塞商家页。
- `/admin/preview` 保留 fixture 并显式标注“预览”。
- 启动失败（403/429/503）在顶栏内显示短原因，不跳转。
- 普通切换只发两个 id；回归测试锁请求体形状，禁止 campaign/asset/payer 字段。

Handoff：

- 详情页主行动改为“记下矩阵草稿”：`POST /campaigns/{id}/matrix-draft`，展示活动版本、draft id/状态与“没有外发”；失败显示服务端可读原因（brand/store/video/expiry）。
- 线索读面保持既有 `campaigns/{id}/leads`；交接来源追踪已在服务端带 trace_id/return_target/asset_ref/campaign_version/grant_ref。
- 删除详情页对 `/api/eco-nav/campaign-image` 的破碎主调用；该路由保留给显式预览/测试，不再作为正常路径。

## 7. 缺陷 → 根因 → 修法

| # | 缺陷 | 根因 | 根修 |
|---|---|---|---|
| D1 | 创建是裸表单/丢草稿 | 无草稿模型、无分步、手填 RFC3339 | 新 creation 页 + 双层草稿 + 本地时间输入 |
| D2 | 创建后无链接/分享 | 创建与短码两步割裂 | 第③步生成链接 + 同源分享/二维码 |
| D3 | 发布状态不清/两个主行动 | 详情页把启用与生成链接都做成实心 | 状态 stepper + 单一主行动 |
| D4 | “制作图片”必 400 | UI 只发 campaign_id，路由要完整 handoff | 改为真实 matrix-draft 交接 |
| D5 | 公共页死 CTA（参与关闭） | 页面无服务端能力信号 | public link capabilities + 门控 |
| D6 | 内容版本静默漂移 | 快照不校验参与版本 | `version_id` 校验 + 409 + mismatch UI |
| D7 | AI 标识缺失 | 未消费 `marking.ai_generated` | 商家/顾客/交接三处展示 |
| D8 | ISO 时间直出 | 无格式化 helper | 本地化窗口文案 |
| D9 | 参与码提示自相矛盾 | 闭包旧值 | 纯函数 `codeIssueNotice` |
| D10 | 公共页 3111px 能力墙 | 1670 矩阵整块展开 | 折叠为摘要 + details |
| D11 | 登录页假导航上下文 | loader/service 回 fixture | blocked unavailable，fixture 只留 preview |
| D12 | 应用直链绕过 SSO | 渲染器用 registry URL | BFF switch + launch_url 校验 |
| D13 | 短码碰撞 500 | 无唯一键重试 | 有界重试 + 明确错误 |
| D14 | token 未真正采用 | 兼容别名覆盖不全、裸值 | 全量别名 + token 化 + 对比度门 |

## 8. 非目标 / 反目标

- 不做：新钱包、发券、真实平台发布回执、多门店治理、public-ai 生产 manifest 修改、跨仓批量迁移。
- 不做：把所有旧组件删掉重写；保留既有测试覆盖的行为，只在“正常路径确实断裂”处改。
- 反目标：用 fixture 假装真实导航；用 `setTimeout` 假装核验；把素材库不可用写成“没有素材”；把版本漂移静默吞掉。

## 9. 验证策略

- web：`npx vitest run` 全量 + 新增纯函数/契约测试 + `npm run build`。
- server：`GOWORK=off go test ./...` + 新增短码重试、launch BFF、公共 capabilities、version mismatch 测试。
- 对比度：`node scripts/contrast-gate.mjs` 真实计算并输出表格。
- e2e：真实本地栈跑创建→链接→分享→启用→公共页；内容包开启后 seed 真实 pack/version/adopt 验证版本一致；导航 blocked 状态；应用切换用 identity stub 验证 BFF 合同（明确标注 stub）。
- 不能跑：public-ai 生产 eco-nav 端点、真实 shell/launch 签发、平台 upload 素材授权验证。写入 NOT_VERIFIED。
