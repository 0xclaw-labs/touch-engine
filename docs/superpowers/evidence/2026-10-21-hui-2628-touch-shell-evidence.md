# HUI-2628 / HUI-2222 / HUI-2227 touch-shell 证据

日期：2026-10-21
分支：`eco/hui-2628-touch-shell`
工作树：`/Volumes/MacintoshHD/派诺生态/wt/touch-engine/mb-eco-touch`
基线：`main@f9d5af7`

## 1. 结论摘要

| 项 | 结果 |
|---|---|
| 商家创建流 | 新增 `/work/campaigns/new` 三步向导；localStorage + 服务端草稿双层防丢；真实门店/本地时间；链接/分享/发布一条主路径 |
| 顾客公共页 | 服务端 capabilities 门控参与/内容；参与开始时钉住商家已采用版本；AI/广告标识；本地化时间；核验码不再渲染不存在的 `/p/...` 死链 |
| 生态导航 | 无配置/不可达/契约不符 → blocked（不渲染、不换夹具）；夹具只保留 `/admin/preview`；应用切换走 BFF → Go → shell launch → 校验后的 `launch_url` |
| 语义 token | 手写 CSS 裸色值 0；全部颜色/焦点/圆角走 `--pn-*`；对比度门 138 行 0 失败（最低 5.58） |
| Handoff | 详情页 400 死按钮替换为真实矩阵草稿交接，响应带 `activity_version`；普通应用切换只带两个 id |
| Web 测试 | `76 files / 719 tests` 全绿 |
| Go 测试 | `39 packages ok / 0 FAIL` |
| Next build | exit 0 |
| 本地 e2e | 8 spec / 19 用例全 pass（新增 3 spec 4 用例 + 回归 5 spec 15 用例） |

## 2. 缺陷 → 根因 → 修复

| # | 实测缺陷（修复前） | 根因 | 根修 |
|---|---|---|---|
| D1 | 创建是裸表单；手填 RFC3339；换页/刷新丢输入 | 无草稿模型、无分步 | 新 creation 路由 + localStorage 草稿 + 第①步立即落服务端 draft |
| D2 | 创建后停在列表，必须自己找活动再点“生成链接” | 创建与链接两步割裂 | 第③步“生成链接并发布”一次完成短码 + 启用；详情页分享区 |
| D3 | 详情页两个实心主行动，状态显示 `active` | 启用/生成链接都画成 primary；无状态映射 | 状态中文 + 每状态唯一 primary（发布/恢复/分享） |
| D4 | “为本活动制作图片”只发 `{campaign_id}`，路由要求完整 handoff → 必然 400 | 死调用 | 改为真实 `POST /campaigns/{id}/matrix-draft`，展示活动版本/草稿 id，明确“没有外发” |
| D5 | 参与开关关闭时公共页仍显示实心“选择内容并参与”，点击 404 | 页面没有服务端能力信号 | `GET /public/links/{code}` 增加 capabilities；页面按真值门控并显示真实原因 |
| D6 | 内容快照按当前采用版回，参与记录的版本为空/旧版时可能静默换内容 | 参与开始未钉版本；快照不校验版本 | 参与开始前取已采用 version；`content-snapshot?version_id=` 不符返回 409 `version_mismatch`；页面显示 mismatch，不回落 |
| D7 | 内容包 `marking.ai_generated` 存在但商家/顾客都不显示 | 未消费字段 | 内容面板、公共快照、发布交接三处显示 AI 标识；素材页可勾选 AI 合成 |
| D8 | 公共页直出 `2026-10-19T18:32:36.946Z` | 无格式化 | `formatCampaignWindow` 本地化；无效时间不伪造 |
| D9 | “出示参与码”成功签发后仍显示“签发不出来” | 闭包读旧 state | 纯函数 `credentialIssueNotice` + 同值渲染 |
| D10 | 核验码链接 `/p/<token>` 没有 Next 路由 = 死链；且 `customer_resume` 无 attempt 时 422 | 路由缺失 + purpose 不符 | 改为 `staff_verify` 文本核验码，给店员在确认与核销台输入；不再渲染死链 |
| D11 | 登录后 EcoTopNav 显示“数海观澜/客户甲/预览上下文” | 无配置/失败回预览夹具 | 缺配置/失败/契约不符 → blocked unavailable；生产 route 不 import fixture |
| D12 | 应用是静态 `<a href=registry URL>`，绕过 SSO | 渲染器直接拼 URL | POST `/api/eco-nav/switch` → Go → public-ai shell launch → web 校验 `launch_url` 同源+路径前缀；渲染器只发两个 id |
| D13 | 短码唯一键冲突直接 500 | 无重试 | `CreateLink` 有界重试 5 次；测试覆盖碰撞与耗尽 |
| D14 | 无门店活动参与时 500（participation.store_id NOT NULL） | 插入前没检查门店 | 无门店活动 capabilities.participation=false；直接调用返回 422 `store_required` |
| D15 | 公共页 3111px 能力墙 | 1670 能力矩阵整块展开 | 摘要一行 + `<details>` 折叠；仍保留可用/不可用与官方说明 |
| D16 | 语义 token 已引入但手写 CSS 仍以裸值/旧变量为主 | 兼容层未补全 | `globals.css` 只留 `--tk-* → --pn-*` 别名；焦点/圆角/颜色 token 化；对比度门自行计算 |

## 3. 商家创建流 before / after

**Before（实测）**

- `/work/campaigns` 底部是“新建草稿”表单：标题、内容、`RFC3339` 开始/结束、可选门店、创建活动。
- 创建后停在列表；没有短码、没有分享、没有下一步。
- 详情页同时有“启用”“生成链接”两个实心按钮；状态 `status active`。
- “为本活动制作图片”必 400。

**After（本地真实栈）**

1. `/work/campaigns` 只有列表 + 一个“新建活动”主行动。
2. `/work/campaigns/new` 三步：
   - ① 门店（真实 `/api/stores`；无门店→登记引导）、标题、顾客内容、本地 datetime；刷新后 `draft-restored` 保留内容；点击“保存草稿并继续”立即 `POST /campaigns`。
   - ② 内容包面板（真实 `/campaigns/{id}/content-packs`，AI 标识、预览、采用）；无内容包显示真实空态与素材页入口。
   - ③ 短码与分享；点击“生成链接并发布”创建短码并 `draft→active`，跳详情 `?created=1`。
3. 详情页显示中文状态、活动时间、唯一主行动（草稿=发布 / 暂停=恢复 / 进行中=去分享）、分享文案/链接/二维码/停用短码。

截图：

- [创建第①步 1440](./merchant-create-step1-1440.png)
- [创建第②步 1440](./merchant-create-step2-1440.png)
- [创建第③步 1440](./merchant-create-step3-1440.png)
- [详情分享区 1440](./merchant-detail-share-1440.png)

## 4. 顾客页 before / after

**Before**

- 参与接口关闭时仍有“选择内容并参与”实心按钮，点击 404。
- 参与后内容版本为空，页面回落到“当前采用版”；商家换版后顾客可能发错内容且无提示。
- AI 合成/广告标识不显示。
- 时间 ISO 直出；“出示参与码”自相矛盾；核验码是死链。

**After**

- `GET /public/links/{code}` 返回 `capabilities`；无门店活动 participation=false；页面显示真实原因，不出现死 CTA。
- 参与开始时先读 `/content-choices`，把已采用 `version_id` 写进参与；快照请求带 `version_id`，服务端不符返回 409；页面显示“你参与时选的内容已不是当前版”，未交接可换选，否则请店员协助。
- 内容区显示 `<AI 合成>` 与广告标识；`data-version` 可在浏览器断言。
- 活动窗口为“2026年10月20日 03:05 至 2026年10月23日 03:05”。
- “出示核验码”签发 `staff_verify` token，文本显示给店员，不再渲染不存在的 `/p/...` 链接。
- 能力矩阵折叠为一行摘要 + `<details>`。

截图：[顾客参与后 AI 标识 390](./public-participating-ai-390.png)

## 5. 真实命令 / 输出

```text
$ cd web && npx vitest run
 Test Files  76 passed (76)
      Tests  719 passed (719)

$ cd server && GOWORK=off go test ./... -count=1
EXIT:0
39 packages ok / 0 FAIL
（完整日志 /tmp/touch-go-full2.log）

$ cd web && npm run build
EXIT:0
Route (app) 含 /work/campaigns/new、/api/eco-nav/switch；static/dynamic 均生成成功

$ cd web && node scripts/contrast-gate.mjs
EXIT:0
checked 138, failed 0, min ratio 5.58
（138 = 6 个 touch surface/theme × 23 个页面实际使用的 token pair）

$ cd web && e2e/node_modules/.bin/playwright test -c e2e/playwright.config.ts \
    e2e/specs/01-merchant-first-entry.spec.ts e2e/specs/02-public-states.spec.ts \
    e2e/specs/05-no-eco-nav-on-public.spec.ts e2e/specs/10-state-scenarios.spec.ts \
    e2e/specs/11-controlled-entry.spec.ts e2e/specs/12-campaign-creation.spec.ts \
    e2e/specs/13-public-content.spec.ts e2e/specs/14-eco-nav-switch.spec.ts
  Running 19 tests using 1 worker
  ✓ 01 today desk with real seed data
  ✓ 02 public five states + loading
  ✓ 05 no eco nav on public page
  ✓ 10 partial / disabled / offline state scenarios
  ✓ 11 controlled entry, multi-org, session loss, 390/1440
  ✓ 12 merchant creates, publishes and shares a campaign through one normal path
  ✓ 13 guest starts participation, pinned content + AI marking + text verification code
  ✓ 14 authenticated app switch BFF + no-fixture nav
  19 passed (3.3m)
```

应用切换实测（真实 Next + Go + identity stub）：

```text
$ curl -b cookies -H 'x-tenant-id: <tnt>' -H 'content-type: application/json' \
    -X POST http://127.0.0.1:18462/api/eco-nav/switch \
    --data '{"app_id":"goboost","launch_target_id":"ti-goboost-web"}'
{"launch_url":"https://goboost.modelxing.com/projects?code=e2e-one-time"}

$ ... --data '{"app_id":"goboost","launch_target_id":"ti-goboost-web","campaign_id":"cmp_x"}'
400 {"error":"bad_request","message":"only app_id and launch_target_id are accepted"}

$ curl ... /api/eco-nav
renderable=false, state=unavailable, reasons=["authority_unavailable"], brand=null
```

identity stub 不是 public-ai 生产实现；它只验证本仓 BFF/Go/校验链的合同形状。

## 6. 测试覆盖（新增/修改）

- `web/tests/campaign-create.test.ts`（8）：本地时间 RFC3339、草稿校验、草稿序列化/租户隔离。
- `web/tests/campaign-share.test.ts`（4）：同源链接、分享模板、只用启用短码、结束/过期诚实提示。
- `web/tests/campaign-creation-page.test.ts`（5）：creation 路由三态、列表不再是表单墙、每步一个 primary、详情唯一 primary。
- `web/tests/public-time.test.ts`（2）、`reward-code-notice.test.ts`（2）。
- `web/tests/customer-normal-path.test.ts`（5）：能力门控、版本钉死、AI 标识、核验码、限领状态。
- `web/tests/eco-nav/switch.test.ts`（5）：launch_url 同源/路径校验、body 只两个 id、缺配置/上游拒绝/伪造 URL。
- `web/tests/eco-nav/nav-authority.test.ts`（4）：生产路由不 import fixture、渲染器不 fetch/不含 http、hook 不带业务字段、availability 不返 URL。
- `web/tests/contrast-gate.test.ts`（5）：自行计算 WCAG、全对通过、注入坏值会失败、手写 CSS 无裸 hex。
- `web/tests/page-patterns.test.ts`（3）：catalog SHA256 对拍、四个 pattern 区域完整、触达路由声明。
- `web/tests/task-handoff.test.ts`（2）：矩阵草稿替换死调用、唯一 primary。
- Go：短码碰撞重试/耗尽、public capabilities、无门店参与 422、内容快照 `version_id` 409、shell launch 合同/拒绝不安全 URL、EcoNav switch 会话/非法 target/无配置。
- e2e：`12-campaign-creation`、`13-public-content`、`14-eco-nav-switch`。

## 7. NOT_VERIFIED（明确边界）

1. **public-ai eco-nav 生产端点未挂载/未配置**：本机没有 producer 端点，登录后实测为 blocked（不渲染顶栏）。`EcoTopNav` 真实模型渲染只有契约单测，没有真实 BFF→public-ai 的端到端运行。阻塞步骤：`GET PUBLIC_AI_ECO_NAV_URL`。
2. **真实 shell/launch 未运行**：应用切换只对 identity stub 验证；真实 public-ai `POST /internal/v1/identity/shell/launch`、一次性 code 消费、目标 app 的 SSO 落地均为 NOT_VERIFIED。
3. **平台 upload / 素材登记 HTTP 未运行**：本机 `FEATURE_UPLOAD=off`。e2e 用 sqlite 写入一条真实 `lib_assets` 引用行（与素材库已登记的 DB 事实同形），后续建包/采用/快照全部走真实 HTTP；没有调用平台 upload，也没有伪造其响应。
4. **新创建活动的第②步内容包采用未跑**：新活动没有内容包，e2e 只验证空态 + 继续；已采用内容包在种子活动上验证（AI 标识、版本钉死、快照）。
5. **完整 e2e 套件/48 张证据门未跑**：本轮跑 8 spec / 19 用例（新增 3 + 回归 5），未跑 spec 03/04/06/07/08/09 与 `evidence-gate` 全量 48 张截图/axe 全量。
6. **对比度是静态 gate**：只计算 token 对与手写 CSS 声明，不测量真实图片/视频背景、渐变、原生端、设备；`runtimeReviewRequired` 的上游 36 行仍未关闭。
7. **没有真实浏览器移动设备/真机 NFC/微信内打开**；390 截图是 Playwright 视口。
8. **没有动 public-ai production manifest**；orders/leads/product-image/matrix 等未登记 app 仍不可切换（诚实缺失）。
