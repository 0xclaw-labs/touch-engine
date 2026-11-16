# 本轮浏览器验收前只读准备

日期：2026-11-15（UTC）。依据 canonical 运行脚本、现有 E2E 准备脚本、HTTP 权威入口及原 18 票范围表。先完成只读核对，随后按授权仅在忽略的 runtime 目录准备可复用桌面入口及工程配置；没有改数据库、删数据、启动服务或浏览器。以下操作是交给主代理及 Terra 的候选构建后清单，不是已完成的浏览器证据。

## 1. 新工程库保留与 RESUME 条件

选用 `.superpowers/sdd/touch-resume-20261115/runtime/serve-desktop.sh`。该入口现委托同目录 `desktop_runtime.py`，保留原工程库并在正常服务就绪后等待；不会调用浏览器或 driver。旧 `serve-touch.sh` 仍会调用 `driver.cjs`，本轮不运行它。

本次文件元信息核对：`runtime/touch-new.db` 已存在，`runtime/tenants.txt` 有两行；没有读取或修改业务行。检查时无 WAL/SHM 伴随文件。数据库文件、`tenants.txt` 和 `runtime/media` 应作为本轮环境连续保留，不回收或重新 seed。将来如出现 SQLite 伴随文件，应按正常停止与备份规则整体保留，不能手删。

续跑入口，供主代理在候选构建完成后执行：

```bash
RESUME=1 bash .superpowers/sdd/touch-resume-20261115/runtime/serve-desktop.sh
```

脚本的实际条件与边界：

- 默认 `RESUME=1`，拒绝其他值；必须已有本轮数据库与组织记录，缺失时拒绝创建替代库，不重新 seed 或覆盖 tenants.txt。
- 根据主代理已有 `runtime/build-manifest.json` 核验候选 commit、候选与当前 HEAD 的 server/web 树、产品工作区无改动、Go SHA256、Next Build ID。允许候选后的纯报告提交；产品树变化则拒绝。脚本不写原 manifest、不重建产品。
- 使用运行时环境中的 Node，Next 依赖目录必须可用；启动前预检 Go、Identity、Web、Seams 四个 loopback 端口，不停止已有监听者。
- 不调用 `provision-tenant` 或 `provision-member`。保留已存在的双组织/同 owner 关系；owner 不因此获得 ops 权限。
- 每次启动单建带 UTC 微秒时间戳与脚本 PID 的日志目录，四服务日志不会覆盖上轮；退出只停止本次直接创建的子进程，不删除数据库、媒体及日志。
- 启动后轮询 Identity、Seams、Go 的 `/healthz` 与 Web `/`；总超时默认 60 秒，可用 `--ready-timeout` 在 1–300 秒内调整。成功后只等待，不自动登录或执行业务准备。

**不要用 `web/e2e/run.sh` / `web/e2e/scripts/env.mjs up|serve` 替代本轮续跑。** 旧流程会清空自己的产物/证据目录、创建另一临时数据库并执行 seed；其 down/serve 清理有临时库回收逻辑，且按端口停止服务。`env.mjs` 还有直接写 lib_assets 的旧工程素材 seed，不能用它替代本轮真实上传及采用的浏览器步骤。

旧 E2E helpers/specs 含自动填写登录凭据的实现。仅参考它们的业务步骤与断言，不运行其登录 helper，不绕过当前安全登录规则；本轮由 Terra 经允许的安全登录流程取得会话后继续正常页面操作。

## 2. 优先补齐的运行时开关与服务边界

下表只列键名与用途，不列配置值。准备后的 runtime 已显式开启本批已有页面能力，统一传给 Go 与 Next；未具备的上传、Notify 和线索提交保持关闭。脚本清除继承的相关集成环境键，再使用本轮限定配置，避免意外沿用其他环境连接。

| 拟验范围 | 开关或配置键 | 已有边界与准备条件 |
| --- | --- | --- |
| 组织、成员、代理、运营授权 | `FEATURE_AUTHZ`、`FEATURE_AGENCY`、`FEATURE_STOREFRONT` | runtime 已显式设置；正常身份确认仍走原 Identity 与 memberships / whoami |
| 经营数据 | `FEATURE_DASHBOARD` | 本地可直接启用原统计读面；无事实时验空/未知，不能制造成功计数 |
| 门店稳定入口与二维码/CSV | `FEATURE_TOUCHPOINT_ENTRY`、`PUBLIC_BASE_URL` | 同一开关须传到 Go 与 Next；公共基址必须对应本轮用户实际可打开的入口，不能保留旧地址 |
| 活动配置、内容、参与与权益 | `FEATURE_CAMPAIGN_RULES`、`FEATURE_CONTENT_PACKS`、`FEATURE_PARTICIPATION`、`FEATURE_REWARD_OPS`、`FEATURE_SERVICE_DESK` | 打开页面能力不等于活动已获准；仍需当前订阅、目录/权益库存、发布配置和渠道资格 |
| 内容交接与渠道控制 | `FEATURE_CHANNEL_ADMISSION`、`FEATURE_PUBLISH_HANDOFF` | 准入、方法、场景和内容授权仍由原权威判断；不能用开关把 refused 改成 admitted |
| 素材上传与内容采用 | `FEATURE_ASSET_LIB`、`FEATURE_VIDEO_TEMPLATES`、`FEATURE_UPLOAD`、`PLATFORM_UPLOAD_BASE_URL`、`PLATFORM_UPLOAD_TOKEN` | 素材和模板读面已开启；仓内没有可复用的独立上传服务，因此上传保持关闭。旧 seed 直接写素材引用不能替代本轮正常上传 |
| 内容制作报价、预留与任务 | `PLATFORM_BILLING_BASE_URL`、`PLATFORM_BILLING_TOKEN`、`FEATURE_TASK`、`PLATFORM_TASK_BASE_URL`、`PLATFORM_TASK_TOKEN`、`TOUCH_PRODUCTION_BILLING_ACCOUNTS`、`TOUCH_PRODUCTION_PRICING_VERSION`、`TOUCH_PRODUCTION_DELIVERABLES` | runtime 已准备沿原路径启动 `seams-stub.mjs`，其内部复用 `source-billing-stub.mjs`；账户映射来自本轮组织。状态仅保留于桩进程内，重启不保证账务夹具连续，也不自动补成功。工程报价、预留和任务 ID 不证明真实扣款或交付 |
| 顾客咨询与线索 | `FEATURE_LEADS_CAPTURE`、`PLATFORM_NOTIFY_BASE_URL`、`PLATFORM_NOTIFY_TOKEN`、`LEADS_TARGET_APP_ID`、`LEADS_PHONE_PEPPER` | config 明确校验这些依赖。Identity stub 没有 Notify 投递/ACK 协议，不能把它的端口当正向 Notify 接收链 |
| 工程权威服务角色 | `TOUCH_SERVICE_TOKENS`、`TOUCH_SERVICE_ROLES` | 按既有独立服务身份登记工程生产者；角色映射所指服务必须已登记。不得把 owner 或普通会员当生产者 |

相关校验来源：`server/internal/config/config.go`；上传/任务/线索依赖见其 Validate 部分。以上仅针对本轮隔离工程环境，不要求或授权修改生产配置。

### 服务角色与支持的权威接口

| 角色名 | 现有入口 | 可准备的内容 |
| --- | --- | --- |
| `ops_grant_manager` | `POST /api/v1/ops-grants`，以及同入口 GET、`DELETE /api/v1/ops-grants/{id}` | 绑定本轮 tenant/principal、命令、门店范围与到期的工程 grant；撤回走同签发者入口 |
| `billing_authority` | `POST /api/v1/billing/authority/catalog`、`POST /api/v1/billing/authority/events` | 工程价格目录、订单付款/开通/退款等投影事实；必须单列为验收前置夹具，不算浏览器完成支付 |
| `content_return_producer` | 原内容制作回写缝，参照 `handlers_content_production.go` 与旧 env.mjs 角色配置 | 受信工程制作结果回写；不把工程结果当真实制作供应方交付 |
| `lead_receiver` | 原 lead-records 取数及 ACK 缝，参照 `handlers_lead_record.go` | 对本轮实际提交的线索读取和确认；不能绕过原来源、同意状态及跨仓匹配合同 |

runtime 已准备四个彼此独立的工程服务身份：`resume-ops-producer`、`resume-billing-producer`、`resume-content-producer`、`resume-lead-receiver`，分别只映射表中对应角色。私有配置仅当前用户可读写，值不进入报告、截图或聊天。此登记只是允许后续通过原受信接口准备工程夹具，不会自动签发 grant、写账务事实、回写内容或接收 ACK，也不代表外部正式资格。

## 3. 三类重点 fixture 的可操作路径

### 3.1 运营正向 grant：原代码入口及工程服务配置已准备，尚未签发

`handlers_access.go` 的 `handleOpsGrantCreate` 已支持 tenant_id、principal_ref、store_scope_set、commands、expires_at、reason；要求真实存在的本轮 tenant，并通过 `ops_grant_manager` 服务门。

六页只读最小命令集对应 `ops:tenants.read`、`ops:plans.read`、`ops:orders.read`、`ops:channels.read`、`ops:templates.read`、`ops:operations.read`。写入与支持访问另按所验动作增加明确命令，不能用任意通配授权。原 `server_ops_console_test.go` 的 `opsFixture` / `opsCommandList` 提供受信准备先例；six-view 无 grant 的 owner 必须保持受限。

同 runtime 目录的 `prepare-ops-grant.py` 接收已确认的本轮 tenant、Identity principal 和原因，默认仅预览六页只读、60 分钟有效期的请求正文；可按明确所验动作收窄门店或增加白名单命令。只有显式 `--execute` 才使用私有工程身份调用原 `POST /api/v1/ops-grants`。本轮没有执行该准备写入。

最小浏览器顺序：同页选择组织 → 无 grant 受限 → 准备限定 grant 后“重新核对授权” → 六页逐项真实读 → 按原接口撤回 → 下次请求拒绝 → 换到无 grant 的第二主体，不残留旧页面。授权发放是准备步骤，页面进入、重试、撤权结果才是浏览器验收。

### 3.2 第二主体：可复用已有 Identity 工程记录及成员入口

`identity-stub.mjs` 已对不同登录身份生成不同 principal；另有供完整邮箱核对的工程成员与工程代理原记录。可由 owner 在正常“团队、权限与代理协作”页面选人、按门店授任务、建立/撤销代管，再由第二主体安全登录核验。该路径参考 `15-commercial-jobs.spec.ts`，只复用其业务步骤。

如果仅需会话隔离的第二主体，已有 `touch-server provision-member` CLI 能绑定本轮已有组织及已确认工程 principal，支持 owner、store_manager、staff 的原规则。该 CLI 是显式工程准备，不属于成员授权的 UI 通过证据；不得把邮箱自行推导的 ID 当正式 Identity 人员。

当前 runtime 只初建同一个 owner 的双组织成员关系；没有证明第二主体已在现库具备成员关系。可用原页面查明后补齐，不覆盖现有关系。无成员主体可直接用于“没有可进入的组织”负例；有成员但无 ops 的主体用于同 tenant 换号旧授权迟到负例。

### 3.3 原单历史：可经原 API 准备，当前 UI 收银正向仍不可达

价格目录可由 `billing_authority` 原 catalog 接口写入工程版本；原订阅/硬件建单 API 仍执行当前 owner / payer 校验、价格快照及幂等键，并只产生 processing 订单。可在明确标记的准备阶段使用这些支持的接口建立本轮历史订单，再通过 UI 验证列表、刷新、原单恢复、变价后的旧快照及不同主体拒绝。不能直接改库造订单状态，也不能把 API 建单记成 UI 下单通过。

当前 `handlers_billing.go` 的 `cashierCheckout()` 按缺失原单消费合同返回不可用。仅补 cashier 环境变量或登记 app/target 不会建立订单消费链，也不应通过工程假目标打开 UI 收费入口。报价、原订单读取、错误恢复可验；真实原单继续支付、实际开通仍缺正式收银消费合同和部署。

付款、开通、退款历史如需准备，应走 `billing/authority/events` 并保留其测试来源、source/event_id、原单与金额关系。它们只能支撑“浏览器正确显示工程历史事实”，不支撑真实支付、退款或开通成功结论。退款身份与空 PaymentRef 的边界另见本目录 `payment-contract-review.md`。

## 4. 登录后覆盖 18 票的最短主流程

范围来自 `docs/acceptance/touch-user-jobs-20261114.md`：2628、4655–4666、4585–4588、3531，共 18 票。以下按一个门店为主、第二组织和第二主体为对照安排；每步保留同一原记录和正常页面返回。没有所需条件的分支停在诚实拒绝/未知处并单独记录，不为凑覆盖补成功状态。

| 顺序 | 正常页面路径与最小动作 | 覆盖票与依赖 |
| --- | --- | --- |
| 1 | `/` 或兼容 `/admin` 安全登录 → 按名称选组织 → 今天 → 门店；刷新、换组织、返回不混数据 | 2628；双组织已是原 runtime 准备形状。实际首页不是 `/work` |
| 2 | 当前门店配置一个服务及首页栏目 → 预览 → 明确发布 → 顾客 `/s/[storeCode]`；同店创建稳定 `/t`、导出二维码/CSV、刷新恢复原入口 | 4655、4659、4664；需要 storefront、touchpoint 及正确公共基址。导出不等于写卡或实店验收 |
| 3 | 同店素材/内容选择或正常上传 → 如适用先查看制作报价再明确提交 → 预览、采用；顾客从原内容动作建立并恢复原会话/交接 | 4657、4660、4588；上传/制作边界、内容授权与当前准入。预览、复制、下载不当发布完成 |
| 4 | 同店新建活动草稿 → 配奖励与库存 → 保存/刷新 → 明确启用或观察具体拒绝 → 顾客先看可用性与条件再参与；暂停/恢复沿同一活动 | 4656、4658、4659；依赖已获准渠道、正式版、订阅与库存资格。原草稿不得改写原参与合同 |
| 5 | 原参与完成可达步骤 → 原权益页 → 店员安全登录 `/work/service` 扫描/输入同一凭据 → 发放与核销分别查看记录 | 4661、4658；需合法正向活动/权益及店员范围。缺合法券时只验空态与拒绝，不人工造成功券 |
| 6 | 顾客主动咨询 → 查看同一提交接收状态 → 撤回并刷新；商家从该条线索进入 Leads 详情再返回原门店 | 4662；前半需 Notify/接收 ACK，后半需两应用本轮服务、来源映射、受控 SSO 和正式返回目标。当前 runtime 单独不足 |
| 7 | owner 正常选人授权 → 第二主体实际进入允许页面/执行允许任务 → owner 撤回 → 第二主体原页下一写拒绝；代理从 `/agency` 进同店并返回 | 4663；现有工程 Identity 记录可支撑，但需本轮关系与安全登录。续费协助不等于付款权 |
| 8 | `/work/billing` 查看工程目录与报价 → 原单列表/刷新/恢复 → 切主体核原付款人 → 收费不可用与目录错误恢复 | 4664；需工程目录与原单准备。无原单收银合同保持不可支付，不新增假 cashier |
| 9 | `/work/analytics` 看前述实际动作事实 → 按组织/门店/活动/时间切筛选 → 空/未知 → 费用历史读取与错误恢复 | 4665；依赖 dashboard 与前面实际生成或明确标识的权威工程事实。统计不能反向制造业务状态 |
| 10 | 同页进 `/ops/tenants` 选组织、重试授权 → 六页导航 → 一条范围明确的运营操作/原单只读重查 → 撤权与第二主体隔离 | 2628、4666；需工程 ops grant。保留后端逐命令门、原因、幂等信封和支持访问范围 |
| 11 | 在原 handoff 中分别检视抖音、小红书、快手与系统分享的当前可执行分支、拒绝原因和原状态恢复 | 4585、4586、4587、4588；官方主体、场景、签名/OAuth、上传与手机环境未具备时只能记缺条件或工程边界，不能算正式发布 |
| 12 | 同候选整理跨流程记录、浏览器发现、工程夹具与正式外部缺口；必要的 390/430/1440 尺寸与键盘恢复沿真实状态取证 | 4666、3531；不能用路由数量、截图数量或旧轮 DB 结果代替本轮验收 |

这是一条共享数据的最短覆盖骨架，并不压缩 A01–A30、28 业务视图及 T01–T20 的验收义务；各独立渠道、角色、未知结果与真机分支仍按实际条件单列。

## 5. 可参考脚本与仍缺的外部条件

已有 E2E 文件只作为步骤来源：`01-merchant-first-entry`、`11-controlled-entry` 支撑登录后组织/导航断言；`09-campaign-config`、`12-campaign-creation` 支撑草稿与发布步骤；`03-contact-flow` 支撑咨询撤回；`13-public-content` 支撑公共内容；`15-commercial-jobs` 支撑成员与代理；`10-state-scenarios` 支撑错误恢复；`06/07/09-a11y` 支撑尺寸与键盘。它们使用旧 seed、部分旧页面/成员假设及自动登录，不能直接作为本轮同库套件执行。

真正外部缺口仍包括：正式 Identity / Notify 运行服务与跨仓 Leads 返回目标，订单绑定的正式收银消费端，真实付款/退款/开通，真实制作与上传供应方，抖音/小红书/快手正式主体及方法/场景资格，手机系统分享与回跳，实体 NFC 写卡回读、物流签收及现场验收。这些不能通过增加本地开关、工程 service role 或统计 fixture 变成正式通过。

## 6. 运行准备交付与只读验证

三个可复用入口均位于忽略目录 `.superpowers/sdd/touch-resume-20261115/runtime`：`serve-desktop.sh` 是桌面启动入口，`desktop_runtime.py` 负责候选校验、限定环境与子进程生命周期，`prepare-ops-grant.py` 负责工程授权请求的预览及显式准备调用。它们不修改 Git 产品代码或生产配置。

候选 `47ecebb9a4dab875556f0b3a6bac7e53fa3cb463` 已执行 shell 语法、Python 编译与下列只读预检，全部通过：

```bash
bash .superpowers/sdd/touch-resume-20261115/runtime/serve-desktop.sh --check
```

预检确认候选提交与两棵产品树、Go 二进制摘要、Next Build ID、现有数据库/组织记录、私有工程配置形状及四个 loopback 端口。核验前后 `touch-new.db` 与原 `build-manifest.json` 的 SHA256 均保持不变。预检不连接业务 API，也不启动服务。

主代理后续可去掉 `--check` 启动同候选，正常就绪后再交 Terra 安全登录。启动脚本没有自动填写凭据、headless driver、数据库初始化、产品重建、假收银目标、授权或业务 seed。此次只交付启动准备，未启动运行环境；现库保留，尚无新增浏览器验收证据。
