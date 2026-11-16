# HUI-4659 / 4660 / 4662 customer 实施报告

日期：2026-11-14。工作树：`touch-engine/.worktrees/customer`，分支 `codex/touch-4659-4660-4662-customer`。未 push、未写 Linear、未标 Done。独立 gpt-6.1-terra 正常入口与对抗审查由 root 接手；本文不把实施者自动化测试当成独立浏览器验收。

## 提交与依赖

自身提交按顺序集成：

- `4a11593`：顾客发布原交接恢复、Service2四项合同、最小咨询许可、参与前真实承诺接线。
- `f8e6b0c`：原 `/c/{code}` 活动交接复用真实平台 adapter，原行/原参与/原尝试恢复及场景防绕过。
- `7c1c8db`：领取资格用中文业务说明，移除公共面内部枚举显示。
- `b048db9`：关闭新咨询后仍可查原许可、撤回，保留现场处理方向。

依赖：基线 `7b2afe6` 已含 adapters `bf430a5`；campaign/rewards 集成提交 `c14079b` 在本树 cherry-pick 为 `0784e90`；store-fix 集成 `5e6d563` 在本树为 `39c1691`。这些依赖 root 已集成，不应重复 cherry-pick。

另有必要消费者兼容仓库 `leads-engine`，分支 `codex/touch-lead-optional-name`（基线 main `1af4ec40`）：自身 `75c8aa9`、`65ded8a`，均仅本地提交。

## 实际改动与复用

### C1 / C3 / C5 / 触点

- C1继续消费已发布门店投影、保同类多实例 action_id。简短公共说明不再输出钱包/工程措辞。权益/隐私入口移到加载、门店不可用状态也可到达的位置；凭门店码不读取任何个人资料，C5仍完全沿本人 visitor 与原权益端点读取，不加新参与闸。
- Wi-Fi等基础服务不再展示与其无关的发布准入 pending_external 提示；真实服务数据/授权路径不放宽，不经发布 adapter，不建参与、不占库存、不留资。
- `/t/{code}` 未知/收回/停用仍不猜门店；给重新读取、核对标签、现场索取正确入口及回原浏览器/本人恢复凭据的具体方向。

### C2 / C4真实交接

- 新共享 `ActionPublishFlow.tsx` 按服务端 plan.kind 与原持久 status 选当前主动作：内容确认、平台授权、可信账号与本次内容确认提交、真实 SDK/系统分享、先查询原投稿。保留刷新、取消、逐个保存、复制参考文案与原权益入口。
- 接 adapters 冻结执行模块：准备真实 File/SDK在点击前进行；系统分享按钮直接同步调用 sharePreparedFiles，保留 user activation；XHS异步fail显示到UI；抖音沿真实签名schema。下载、系统share resolve、SDK唤起只记弱事实，不升格官方发布或奖励。
- 删除旧参考文案流程手填平台账号输入；该字段仅是不可验证的声明。`custpublish.Preview`允许空 account_label，内容确认仍核精确指纹，平台归属由原平台授权事实确定，旧带label记录/协议保留。
- 原 `CustomerPublish` 文案预览/修改/导出/自报/证明功能仍在辅助展开区；去掉原没有实际 handler 的唤起/授权空操作主按钮，不把它们称为可用。
- C2先恢复本人3546 session，再GET content携唯一参数 `action_session_id`，换选/跨端恢复后重读完整冻结正文/候选与媒体，不仅换一个版本ID。内容不可用时恢复码区仍可达。
- 已有本人session在动作撤下后可恢复只读原进度，不新建参与。后续素材重签/实际交接仍检查实时动作、3535准入、block/grant，不能凭恢复绕撤权。

### Service2四项

- GET原handoff和OAuth返回重新Prepare同一记录并持久 client_action。KS的服务端有效token决定 confirm_submit/account_ref，oauth=ok仅提示，不能成为授权或发布事实。
- 每次Prepare沿原冻结版本重新签MediaRefs，恢复过期链接不创建新的handoff/attempt/内容；block/grant/动作实时撤权否决仍生效。
- 仅 errors.As 到 `RequiredAction()==query_original` 的明确未知结果错误落 unknown。普通OAuth/媒体前置条件错误不推进。unknown仅回查原任务，不能重投。状态机允许 system_adapter证明这种“请求后结果不明”，不谎称超时。
- Start直接传原3535 FieldSupport；恢复/Submit重读同方法当前字段权威。未知/不支持字段不自动填；参考文案可独立复制。
- action session的合法customer_resume消费后，新持有人沿该session读取原handoff；原session已经转交时，旧visitor不能只靠handoff旧visitor列继续读。staff_verify并不进入此恢复路径。

### C4原活动入口窄桥（不重建目录或第二参与）

- 代码确认旧发布活动没有可用的legacy publish action回填，不能强建action目录来凑v2。
- 保留原 `participation_id/attempt_id/handoff_id`、原 `payload_kind=''`、原恢复凭据和冻结snapshot；显式adapter_mode将**同一行**补充方法/plan/client_action元数据，调用原Adapter.Prepare/Submit/Query。
- `CampaignPublishFlow`在原 `/c/{code}` 内使用同一执行内核；平台来自原内容候选与本人已有尝试，刷新查回原记录。旧method为空的handoff可在原权威准入核对后原行接线，不新建参与/尝试/交接。
- C4有奖励config时，服务器强制reward_task场景（包括原adapter_mode=false兼容API），不采信浏览器store_promo借权限。原场景与现获准场景不一致时阻断继续投稿，原记录/已发券独立保留，不自动转手动领奖。
- KS仅增加一个服务端回跳分支：无ActionID的原legacy行，按tenant/participation/attempt一致读取原link_code，回 `/c/{code}?handoff_id=...&oauth=ok|denied`。未知/错attempt仍拒，state/App/一次性校验不放宽。真实官方结果仍走原五绑定回执管线。
- C4已开始参与展示“继续这次参与”，不把最新商家草稿承诺冒充原合同；选中的旧campaign内容凭本人participation精确绑定读取，素材实时block/grant照常否决。

### C4参与前权益

- 公共reward-summary只读原 `GetPublishConfig` + 原inventory + `campaignManagement/Window/campaignenable.Check`，不新建规则写源。
- 展示原权益名/商家承诺、适用门店名、领取/使用期限、认可核验方式、余量和资格依据。无config、未启用、资格/库存未知或0时不出现领奖主承诺。无活动基础服务继续独立。
- 公共响应未输出券码、成本/预算或个人记录。已参与者不把最新promise当自己冻结合同；权益详情仍沿RewardStatus/C5原账本。

### C4咨询 / C6与留资消费者

- 默认只给“请门店联系我”，主动选择再展开；姓名仅作称呼选填，手机号保留本次联系、验证原提交与撤回用途，微信选填原渠道保留。服务许可与营销独立。
- campaign/store两条源schema同步取消name必填，保姓名长度上限、phone规范化及严格字段白名单；原record `name`字段继续存在，空串不伪造姓名。
- C6无code直接使用原store级lead-form/submission/status/revocation；可按原提交编号+手机号读取原结果，不把资料放URL/本地存储。新咨询关闭后仍可尝试读取原许可。
- 公共状态补充原receiver_state、revocation_pending与告知版本；本地停止许可、撤回待接收方确认、接收方ACK分别呈现，查询旧响应不回退本地已撤状态。后端原撤回水位与旧ACK拒绝规则保留。
- 在线无法处理时提供明确的门店服务台现场撤回方式，含所需核对资料；未捏造未配置电话或外部“联系”地址。
- leads-engine实际消费者原先在notify ingest和IntakeLeadInTx强制name，已修为**仅已授权touch-engine来源 + touch_campaign +有效手机号**允许空称呼；其他来源与通用HTTP手工建档校验保持。
- 追踪至同事务contacts/leads、原consents、同事务AssignLeadInTx、notify inbox/read-receipt：此链是本地CRM落库，不存在另一个必须同步name的下游CRM。原分配依据region/industry/tenant与lead ID，ACK不要求姓名。contacts/leads UI显示“未填写称呼”，不写假姓名。旧提交/重放/撤回和原记录SHA字段形状兼容。

## 验证证据

使用 `source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`。日志同目录：`customer-go-all.log`、`customer-go-final.log`、`customer-web-final.log`、`customer-leads-final.log`。

有意义先红再绿：

- `TestSubmitUnknownPersistsAndOnlyQueriesOriginal`：原实现返回未知后仍持久handoff_ready，可再提交；先红，修后unknown持久、第二Submit拒绝、Query原ID。
- `TestLeadNameOptionalBothCaptureLanes`：原campaign源400 name is required；修后campaign/store均201，空称呼不扩大联系渠道权限。
- `TestManualPreviewDoesNotRequireUntrustedAccountLabel`：原missing_copy_or_account；修后能确认原内容，不产生平台postID。
- leads `TestTouchNotifyIntakeOptionalNameKeepsOriginalConsentAndReplay`：原422 insufficient_contact；修后真实notify摄取路径落一条contact，重放不建第二条。
- KS legacy OAuth测试暴露重复问号/handoff_id，修后正确回原/c路径并继续原state，错attempt拒绝。

完整相关Go：

`go -C server test ./internal/publishhandoff ./internal/publishhandoff/adapters ./internal/participation ./internal/custpublish ./internal/leads ./internal/store ./internal/httpapi -count=1`

全部通过（HTTP106.624s，store68.172s）；最后旧method空兼容与场景防绕过后又重跑相关三包：HTTP106.926s、publishhandoff7.143s、adapters5.254s全部通过。额外最后新增 `TestCampaignRewardCannotBorrowPromotionPermissionFromLegacyAPI` 与原C4正常入口测试一起跑，通过（0.367s）。

- 覆盖 `TestRefreshPrepareRetainsHandoffAndReadsCurrentAuthority`：同handoff/session、字段权威与新media refs、持久可信账号、实时媒体否决。
- 覆盖 `TestLegacyAdapterUsesSameFrozenHandoffAndUnknownQuery`：单row、原p/attempt/指纹、unknown不重投、错误visitor拒绝。
- 覆盖 `TestCampaignNormalHandoffUsesOriginalAdapterWithoutSecondParticipation`：原C4 HTTP端点读取plan/确认/同row恢复、真实adapter接口接收受控媒体/FieldSupport、错误visitor404、participations仍一条。
- 覆盖 `TestStartResumesOriginalAfterActionWithdrawnWithoutNewParticipation`：原内容/会话恢复、新访客仍拒、新权益升级仍拒。

Web：`npm --prefix web test -- --run`，116文件1167测试全通过（5.42s）。`npx --prefix web tsc --noEmit -p web/tsconfig.json`通过。最后C6关闭新咨询保留历史管理的窄条件改动，再跑privacy-view 8测试通过、tsc通过。`git diff --check`通过，工作树干净。

消费者：`go -C server test ./internal/httpapi ./internal/store ./internal/notifyingest -count=1` 全通过（22.153s/6.541s/0.012s）；最终去掉touch渠道主体分支的伪姓名fallback后，重跑全部NotifyIngest相关与新增测试，通过（0.782s）。此消费者前端仅两处缺省显示文本，未安装其整套前端依赖，未冒称其整站tsc/build通过。

## 独立正常入口与数据准备

root提供同exec Go/Next+Chrome隔离环境后，由独立Terra使用已发布门店的真实入口：

1. `/t/{code}` → `/s/{store_code}`：无活动Wi-Fi/联系门店、同类多实例；390/430px与桌面；未知/停用触点的现场指引与重试；门店不可用时C5链接。
2. `/s/{store_code}/content/{action_id}`：已发布具体内容/候选白名单；原session换选/刷新/合法customer_resume、SDK或真实文件准备失败、取消/原状态恢复；在途unknown先Query。
3. 原 `/c/{code}`：先看真实权益承诺/可参与性，显式开始原一次参与后，CampaignPublishFlow的真实交接；对比原participation/attempt/handoff，拒绝用store_promo绕reward_task、旧record不变成新参与；已发券与发布unknown分别显示。
4. `/s/{store_code}/privacy` 与C4咨询：浏览默认不展开资料；无姓名有效手机号/营销拒绝；待同步与ACK/撤回待处理分开；刷新用原编号+手机号；关闭新咨询后仍管原记录；错商家/错手机号不可读。

工程HTTP fixture可通过上述测试创建数据，但不得把测试SQL/fixture作为产品正常入口验收。独立浏览器应从原商家配置与发布入口准备门店/素材/活动/库存；按用户要求遇到产品入口问题修产品，不改库造成功。

## 外部条件与不能冒称的交付

- 本机无正式平台App/OAuth账号、获批部署主体/场景/奖励方法证据、正式真机四环境与外部线索消费者正式凭据。真实XHS/Douyin/KS、系统相册/分享目标、正式ACK仍需要对应环境验收。
- 自动化调用fake adapter/HTTP工程替身证明接线、关联、幂等与状态边界，不证明平台已发布、实际发奖或真机入相册。
- 原平台限定及adapters报告所有外部缺项继续有效；受限/未知场景不会回退成手动领奖。KS在平台已创建却丢失photoID的外部未知保持unknown，需要原任务/平台核对，不会盲重投。
- 未进行独立Terra浏览器/对抗验收，因此三票均不能由本实施组自行标Done。后续缺陷由root按原正常路径组织修复复验。

## 补充：Notify → Leads真实启动依赖与尚未联通的合同缺口

**本报告的两仓测试是各自真实SQLite/HTTP测试，不是已经实际启动Touch + Notify + Leads并取得五元ACK的三进程联调。** 本轮最后只读追查发现下列原有跨仓合同缺口，应由root纳入HUI-4666/4594真实联调，不能用兼容替身掩盖：

1. leads `server/internal/notifyingest/fetch.go:FetchRecord` 仍向已登记FetchBase拼 `/internal/v1/lead-records/{ref}`，只送 `X-Internal-Token` 和 `X-Leads-Tenant`。
2. touch `handlers_lead_record.go` 已退役这条旧路径；真实口为 `GET /api/v1/internal/lead-records/{ref}`，由 `requireLeadReceiver` 验独立服务身份、签名令牌和 `lead_receiver` 角色，不能拿全局内部令牌替代。
3. leads的 `handlers_notify_ingest.go` 当前把contacts/leads、授权、分配及notify inbox/read-receipt落在本地事务；本次只读检索没有向touch `POST /api/v1/internal/lead-records/{ref}/acknowledgements` 发送五元ACK的客户端。Notify delivered/read-receipt不等于touch合法接收ACK。
4. 因此仅设FETCH_BASE到touch，不能使当前主分支完整联通；也不能开放旧路径、共享主密钥、改库把state置crm_received来验收。本轮name兼容修复没有创建或扩大该接口权限。

最小真实本地拓扑与命令（应在root已有隔离runner中启动；以下仅为命令/依赖清单，不是已执行成功的证据）：

```sh
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
# 先由root准备独立环境文件、真实已注册的服务身份和租户归属；不要把值写进仓库。
go -C /workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/integration/server run ./cmd/touch-server
go -C /workspace/scratch/b0e2059d4a63/leads-engine/server run ./cmd/leads-server
# platform-notify须用其本仓服务启动命令并登记定向投递目标，不能用本测试recordSource冒充。
```

- Touch：`TOUCH_HTTP_ADDR/TOUCH_DB_PATH/TOUCH_INTERNAL_TOKEN`、原Identity配置；`FEATURE_LEADS_CAPTURE=on`、`PLATFORM_NOTIFY_BASE_URL/TOKEN`、`LEADS_TARGET_APP_ID=leads-engine`、`LEADS_PHONE_PEPPER`。真实主程序每15秒运行原leads forwarder。v2消费者另需 `TOUCH_SERVICE_TOKENS` 与 `TOUCH_SERVICE_ROLES` 中的专用接收方登记；tenant header必须与源row一致（具体service header/认证方式见 `requireLeadReceiver`，不自行放宽）。
- Leads：`LEADS_HTTP_ADDR/LEADS_DB_PATH/LEADS_INTERNAL_TOKEN`、`PLATFORM_IDENTITY_BASE_URL/TOKEN`；`FEATURE_NOTIFY_INGEST=on`、`LEADS_DEDUP_PEPPER`、`LEADS_INGEST_SOURCES=touch-engine`、`LEADS_INGEST_TOUCH_ENGINE_SECRET/FETCH_BASE/FETCH_TOKEN`（APP键由原ingestEnvKey规范化）。源租户必须能按原tenantmap映射到已存在的真实目标租户；默认不能猜租户。
- Notify：实际服务、有效定向事件写凭据、touch-engine→leads-engine目标订阅与ingest签名密钥，指向Leads `POST /internal/v1/notify/ingest`。不能把测试中的httptest notify/recordSource计为这一步。
- 完整待修合同：Leads按touch v2登记身份取源record，contacts/leads事务成功后回POST原五元ACK（receipt_id/source_event_id/submission_id/source_revision/receiver_state），撤回同样要ACK且遵守水位。当前未实装/验证这段，故只能确认本轮空name在**现有消费者本地摄取事务**兼容。

测试来源可复核：touch `server/internal/httpapi/lead_record_test.go` 验v2服务身份、五元ACK与撤回水位；leads `server/internal/httpapi/notify_ingest_test.go` 的newRecordSource/postIngest是独立HTTP工程fixture，`TestTouchNotifyIntakeOptionalNameKeepsOriginalConsentAndReplay`复用了它，没把它标作真实跨仓ACK证据。

### 接手修复所需精确v2请求

- Fetch：`GET /api/v1/internal/lead-records/{submission_ref}`。头：`X-Service-ID: <专用leads接收方ID>`、`X-Service-Token: <该ID专属令牌>`、`X-Leads-Tenant: <该源row所属租户>`。角色须由 `TOUCH_SERVICE_ROLES` 显式登记 `lead_receiver`，不走商家session fallback。
- 活跃record保留 `name`（现在可为空）/phone/wechat、authorization/purpose/consents，另有 `source_revision/consent_revision/campaign_ref/store_ref/capture_source`。已撤record是tombstone，不再返回contact字段；Leads必须按撤回事实收敛，不能拿缺phone的tombstone当一次新线索。
- ACK：`POST /api/v1/internal/lead-records/{submission_ref}/acknowledgements`，上述三个身份/租户头相同。body严格只含 `receipt_id/source_event_id/submission_id/source_revision/receiver_state`；submission_id精确等于path。receiver_state仅 `stored|revoked`；source_event_id须对应已经forwarded的原outbox事件id、原profile event_id或notify event_id，不能新造unknown引用。receipt_id同payload幂等200，新ACK201，冲突或旧stored越过撤回水位拒绝。
- root接手窄修和跨仓回归；customer停止修改leads，保持其干净HEAD `65ded8a2dcf0b913059ecaf5a19393aaa421772c`。最新顾客端补丁 `b048db9` 仅关闭新咨询时保留原许可管理；root在9bbefc0之后须再合此提交。
