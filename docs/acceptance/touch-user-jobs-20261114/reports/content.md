# HUI-4657 实施报告

日期：2026-11-14。基线：9dd04a7467020602bae130186d4ed738404fb65d。
工作树：`/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/content`。
分支：`codex/touch-4657-content`。
本地提交按顺序：`cee0ee5`（主实现）、`ee7516c`（已有活动正文与真实能力状态补充）。未写 Linear，未标 Done。

## 实际实现

1. `/work/materials` 改为当前用途的薄壳：按查询参数门店/可选活动读原权威资料，门店名、活动标题与已有 `public_content` 自动带入；活动上下文固定归属门店，不再把第一场活动误当当前用途。无活动合法。
2. 将原 `StoreContentDrawer` 升为一个统一编辑器，并导出 `ContentEditor` 兼容别名。来源为“上传文件／选择已有／帮我制作”单选，只保留一份平台、标题、正文、门店事实与标识表单。
3. 保留 B05 真实 multipart 上传、服务器 MIME/字节/哈希校验与受控媒体预览；上传结果自动选入成片/封面。上传和选用已有内容不走制作报价、预占或 AI 生成。
4. “选择已有”可选原候选或已采用版本并带入资料。未修改时复用原版本，只请求预览；改文案或媒体后才落新候选。原候选、版本与既有顾客快照继续复用原权威。
5. 首次上传候选或首次制作由系统按标题创建/复用空门店包，活动可空。不再要求商家先另建空包、手抄内容包引用或报价引用。
6. 制作正常路径已接真实公共 Billing 协议：`source_reservation_v1` 的 `usage`（仅登记）→`quote`→就地展示→商家明确确认→按同一 scope 查询既有事实→`hold(quote_id, expected_amount_minor)`。报价前没有 hold 或 charge 调用；本适配器没有 charge 调用。
7. 报价展示交付内容、权威金额、付款账户和有效期。金额与过期时间只映射原 `quote_id / amount_minor / expires_at_unix`，没有本地估价。交付内容取受信生产方的部署合同，明确不冒称 Billing 返回交付条款。
8. 付款资格继续使用原 `callerHasPayerGrant / payerDecisionFor`；付款用户来自 Identity 解析的 Principal；付款账户由服务端租户映射带入。浏览器不提供 payer 字段。实际账户成员/额度裁决仍由 Billing hold 完成。
9. 服务端签名 offer 绑定租户、门店、包、付款用户/账户、原 brief、报价及同一请求凭据。确认提交带用户所见金额、币种和有效期；篡改、不同用途、不同请求键均拒绝。
10. 可信 Billing hold 结果接入原 `billing_ai_usage` 投影，继续调用原报价裁决与消费；没有浏览器写 confirmed 或伪造内部回调。0133 只存 offer/hold 的恢复上下文，不是第二本账。
11. 发送前把完整确认负载保存到租户/门店/可选活动作用域的 localStorage；丢响应、刷新、重登回查或重试同一负载。后端同请求重放不重复 hold/任务；记录后未确认任务受理时用原 retry，原任务幂等键与 usage/hold/payer/交付条款都保持。
12. 真实 source offer 到期但已有 open hold 时恢复原 hold；无既有 hold 时拒绝新预占并让用户重新获取/确认。余额不足、无付款权限、权威不可用与报价变化分别给出恢复文案。上游未确定的状态保留待回查，不说“没有提交”。
13. 制作结果只进原候选；预览后明确采用，回调结果为 `adopted_pending_publish`。内容编辑器与原活动内容面板均明确“已选用待发布”，线上使用以原用途发布版本为准。没有增加“采用并上线”捷径。
14. 请求/任务引用移入折叠诊断。保留版权声明、广告与 AI 标识、实测媒体信息。failed/canceled 按真实回执展示；释放/退款仍以公共账务事实为准，没有伪造取消或退款成功。

## 被删除步骤/字段及权威替代来源

| 原步骤/字段 | 原用途 | 现在来源/处理 |
| --- | --- | --- |
| 两套几乎相同的上传/素材库表单 | 选择来源并编辑文案 | 单选来源，共用同一 draft；真实上传在当前编辑器内 |
| 活动必填 | 给内容包确定用途 | 门店为根；可选活动来自宿主/URL且经原接口确认归属 |
| 首先另建空包 | 给制作建立内容锚点 | `ensureContentPack` 调原门店包接口自动创建/复用 |
| 手工包引用 | 指定生产目标 | 选已有版本或由系统准备，引用内部传递 |
| 手填 `quote_ref` | 告诉后台报价 | 公共 Billing quote_id 内部映射，浏览器自动携带签名报价 |
| 展示随机幂等凭据要求用户记忆 | 恢复同一次请求 | 系统保存完整请求，列表/原 retry 回查同一记录 |
| 正常页面任务号/费用引用堆叠 | 故障诊断 | 折叠诊断，不再作为操作前置知识 |
| “采用后顾客看到这一版”笼统文案 | 描述采用结果 | 明确已选用待发布，发布及既有会话快照仍由原事务裁决 |

## 宿主调用合同

导入保留：`@/components/work/store-content-drawer` 的 `StoreContentDrawer`；同文件也导出 `ContentEditor`。

```ts
interface ContentEditorProps {
  storeId: string;
  storeName?: string;
  campaignId?: string;
  purpose?: string;
  initialTitle?: string;
  initialBody?: string;
  returnHref?: string;
  onAdopted?: (result: ContentAdoption) => void;
}
interface ContentAdoption {
  storeId: string;
  campaignId?: string;
  packId: string;
  versionId: string;
  platform: string;
  state: "adopted_pending_publish";
}
```

`purpose` 为用途说明，不是枚举；store 组的“门店顾客页”合法。`onAdopted` 只在原采用接口成功后调用，宿主可刷新绑定列表并标记草稿待预览；这不是发布完成回调。`returnHref` 由宿主提供可信站内原用途地址。组件不改 store/campaign 宿主页或共享壳。

## 主要文件

- `web/src/app/work/materials/page.tsx`
- `web/src/components/work/store-content-drawer.tsx`
- `web/src/components/work/store-production-panel.tsx`
- `web/src/components/work/content-pack-panel.tsx`
- `web/src/components/work/content-editor.module.css`
- `web/src/lib/work/content-editor-flow.ts`
- 内容相关前端回归测试；customer-normal-path 的 AI 标识断言跟随到统一编辑器原写面
- `server/internal/production/quote_request.go` 与对应测试
- `server/internal/production/production.go`（新增真实 source 协议错误/报价字段；旧 ConfirmedQuote seam 明确标弃用，保留基线兼容）
- `server/internal/httpapi/handlers_content_quote.go` 与 `server_content_quote_test.go`
- `server/internal/httpapi/handlers_content_production.go`（窄接确认、原任务参数/恢复、能力状态）
- `server/internal/httpapi/server.go`（只加一条 quote 路由）
- `server/internal/store/store_content_offers.go`
- `server/internal/db/migrations/0133_content_production_offers.sql`
- `server/internal/config/config.go`（只增下面三个明确配置）

## 运行配置与原公共协议核实

沿已有 `PLATFORM_BILLING_BASE_URL / PLATFORM_BILLING_TOKEN / PLATFORM_TASK_BASE_URL / PLATFORM_TASK_TOKEN / FEATURE_TASK` 以及受信生产方注册。

新增：

- `TOUCH_PRODUCTION_BILLING_ACCOUNTS`：JSON 对象，键为真实租户，值为该租户已有公共 Billing 账户。只作服务端账户路由，不能凭此跳过 Billing 成员/资金裁决，缺映射拒绝正常收费路径。
- `TOUCH_PRODUCTION_PRICING_VERSION`：生产方与公共 Billing 已发布的内容制作价格版本；没有默认猜测版本。
- `TOUCH_PRODUCTION_DELIVERABLES`：受信生产方已约定的交付内容；没有虚构默认交付。

本轮通过连接器实读 `0xclaw-labs/public-ai` 当前 main（检索索引 commit `a0c5b24c0e1e4024e58c33b918531b9a750e6a86`）的：

- `internal/billing/unified_http.go`
- `internal/billing/unified_usage.go`
- `internal/billing/unified_reservation.go`

原协议明确 IngestUsageFact 仅登记 `ingested`，never prices/charges；quote、hold、charge 分开。真实报价是 quote_id/usage_id/amount_minor/expires_at_unix，确认是带 expected_amount_minor 的 source hold，不是旧 touch 代码假设的 quote_ref/status=confirmed 远程 lookup。正常新路径没有使用旧虚 shape。恢复先查原 usage facts，open hold 原样复用；未新增任意付款账户输入、第二钱包、独立报价目录或新任务队列。

## 验证结果

### 工程验证

1. 新 `content-editor-flow.test.ts` 先红（模块不存在），实现后绿，验证自动建包/复用、报价必要事实与过期判定、完整确认负载。
2. 六包相关 Go suite：
   ```sh
   GOCACHE=/workspace/scratch/b0e2059d4a63/runtime/go-build-cache PATH=/workspace/scratch/b0e2059d4a63/runtime/golang/go/bin:$PATH go test -C server ./internal/production ./internal/httpapi ./internal/store ./internal/billing ./internal/contentpack ./internal/config
   ```
   修复 AuthorityError 对旧 ErrUnavailable 的兼容后，完整执行出口 0；HTTP 249.182s、store 175.082s，其他包通过。首次失败与第二次成功曾共写同一临时日志，所以不将该混合日志最后一行当作最终出口证据；最终成功由独立命令会话返回码确认。
3. 最后主实现变更后的制作相关定向复验：
   ```sh
   go test -C server ./internal/production ./internal/httpapi -run 'TestRequestQuote|TestConfirmOffer|TestExpiredOffer|TestContentQuoteConfirm|TestContentProduction' -count=1
   ```
   出口 0，production 0.010s、HTTP 3.737s。覆盖原十项内容制作合同及新 source quote→hold、篡改签名/金额、无付款资格、过期拒绝、既有 hold 恢复、任务受理后响应丢失再 retry 同一幂等键/hold 的回归。测试任务端故意首回 503、重试返回同一任务，只有一个任务幂等键与一个 hold。
4. ee7516c 的报价/能力复验 `go test -C server ./internal/httpapi -run 'TestContentQuoteConfirm|TestContentProductionCapabilities' -count=1` 出口 0（0.705s）。
5. 最后前端定向复验：
   ```sh
   npm --prefix web test -- --maxWorkers=1 --minWorkers=1 tests/content-editor-flow.test.ts tests/content-production.test.ts tests/store-content-packs.test.ts tests/content-packs.test.ts tests/customer-normal-path.test.ts tests/uifinish.test.ts
   ```
   6 文件、63 项通过，出口 0。新的 video 样式已移到内容 CSS module，UI 扫描通过。
6. 完整 web suite 曾执行 110 文件、1133 项：1123 通过，10 失败。9 项由 `/work/materials` 改薄壳后共享 page-census 对旧 raw button/raw input 计数漂移引起，已由 root 指派 store 组独占更新该路由为 `[]`；本组未越界修改共享 census。另 1 项是已修的 video 内联样式，定向 uifinish 复验已通过。集成后 root 需复跑共享 census/全量 suite。
7. `tsc --noEmit -p web/tsconfig.json` 仍有既有测试类型错误：contrast-gate 未使用 @ts-expect-error、customer-journey-api 的 network 联合类型未收窄；本组新增/修改内容源码未出现类型错误。未改其它组写面去掩盖它们。
8. `git diff --check` 通过。

### 浏览器与跨模块实际集成

本组没有声称浏览器通过。root 已确认当前环境完整 harness 在同一次 exec 中 TCP 可达，但跨 exec/托管浏览器不可达，权限提升被环境拒绝；本组按协调先提交，独立审查/正常浏览器走查由 root 后续完成。本轮 HTTP 测试和旧 E2E 使用的是工程替身，不证明真实 Billing/Task/生产方联调。

### 外部渠道与正式交付

尚无真实生产方、真实公共付款账户/价格发布、真实扣费/结算及真实媒体生产回流证据。不能标记本票正式 Done。下游原生产方需复用任务 params 中 `funding_version / usage_id / hold_id / payer_account_id / payer_user_id / accepted_amount_minor / pricing_version / delivery_terms`，不得另建 usage/hold 重复扣费；实际结算和失败/取消后的释放按原 source 账务与任务终态合同执行。本组没有原任务主动取消接口的真实授权合同，故未伪造取消请求或释放成功。

## 正常浏览器入口与数据准备（供 root/独立审查）

1. 正常从 `/admin` 登录表单进入；harness fixture 凭据来自 `web/e2e/evidence/seed.json`，不要手填数据库或人为塞入报价投影。
2. 集成后的门店正常入口：`/work/stores/{storeId}?tab=page&panel=content`（store 组宿主）；素材正常入口 `/work/materials?store={storeId}`。活动入口 `/work/materials?campaign={campaignId}` 自动查原活动的 store/title/public_content。
3. 使用同一无活动、无内容包门店：上传真实合法视频与封面→一份表单补文案→提交候选（系统自动建包）→真实受控预览→明确采用→返回门店草稿；确认显示待发布、原发布事务尚未触发。测试媒体属于工程上传样本，不可冒充生产方成片。
4. 改“选择已有”，选刚才原版本，不修改再次预览，检查没有新建版本；修改文案提交新候选，再采用，检查旧会话快照仍原样。
5. 改“帮我制作”，保留相同资料→获取权威报价→检查金额/付款主体/交付/有效期→未点击确认前检查无 hold/任务。明确确认后，拦截响应/刷新/重登应恢复同一请求；已登记未受理走同一 retry。
6. 对照无付款权限、未配真实报价参数、上游503、过期、金额篡改、生产方/任务未接状态。检查没有编造价、没有新 hold/任务，没有把未知当失败或成功。
7. 使用 390、430 与桌面宽度各走来源切换、提交错误、版本预览与采用，截图或录像记 root 集成 SHA、配置类别和脱敏引用。
