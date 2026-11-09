# 实施计划｜HUI-4589 五平台点评、七类关注与 Wi-Fi/微信/社群真实入口

- worktree: /Users/jimfu/works/派诺生态/wt-touch-4589 ｜ 分支: touch/hui-4589-store-services ｜ 基线: ce47dde（wave1 合流后）
- 需求权威: 裁定书 briefs/hui-4589.md + SHARED-BRIEF 12 条 + 派遣单要点（Linear 票面在本任务执行时服务不可达，fetch failed；
  契约字段按裁定书+4584/3535 既有合同推导，报告挂账供指挥校准）。
- 迁移号段: 0079-0083（实际用 0079/0080/0081） ｜ 端口: web 18348 / go 18248 ｜ FEATURE 开关: 复用 FEATURE_STOREFRONT（公共读面）+ 新增 FEATURE_WECOM_CALLBACK（回调面，默认 off）

## 硬纪律（不可妥协项，测试逐条锁定）

1. **点评零模板零AIGC零截图核验零奖励触发，后端+UI 双拦**：
   - services 公共投影对 review 类**不含任何文本内容字段**（无 template/copy/draft/suggest 字段——payload 断言锁定）；
   - guidance 只写「打开正确平台门店，自主评价」；
   - 点评面板 UI 无任何输入框/生成按钮/截图上传；文案明示不提供模板、不自动生成、不索要截图、评价不与奖励挂钩；
   - 奖励门: 点评类 reward_eligibility 读 3535 能力真值（种子=unsupported），绝不本地判 true。
2. **Wi-Fi 密码仅展示+主动复制**：不强制留资后才看（services 读端点无任何会话/留资前置）；复制是顾客主动点击；连接失败→手动指引回退（T12）。
3. **证据分层**：事件表只存 `open|jump_attempt|copy` 三种顾客行为证据；数据库 CHECK 从结构上不存在
   「followed/connected/added/reviewed」成功枚举；点击/复制回执文案明示「不代表已关注/已连接/已添加/已点评」。
4. **无官方回执绝不记成功**：企微回调验签通过也只是证据事件（digest-only），永不升级任何门店/顾客状态；
   资质凭据未配置 = 「待资质」诚实态（pending_qualification），URL 验证无法完成也如实回答，不伪造 echo。
5. **租户/门店从可信路径解析**：公共面从 store_code 反查（悬置租户 fail-closed）；商家面会话+authz（GET=ReadRecord、PUT=Update，store scope）；action_id/service_id 不是授权。

## 任务拆解（TDD：每任务先红后绿再 commit）

### T1 迁移 0079-0081（server/internal/db/migrations/）
- `0079_store_services.sql`：`store_services`（id=sv_<24hex> 不透明 PK、tenant_id、store_id、catalog_code CHECK(''或 A[0-9][0-9])、
  kind CHECK('wifi','contact','community')、variant、label、config JSON（按 kind 形状校验在模型层）、sort_order、enabled、
  revision、created_at、updated_at）+ `store_service_configs`（store_id PK、config_revision）+ `store_service_snapshots`
  （每次真实保存一条不可变 JSON 修订，4584 同款）。
- `0080_store_service_events.sql`：`store_service_events`（id、tenant_id、store_id、service_id、event_kind
  CHECK('open','jump_attempt','copy')、environment、evidence_tier CHECK('click','jump_attempt')、occurred_at）——
  结构上无成功语义。索引 (store_id, occurred_at)、(service_id)。
- `0081_wecom_callback_log.sql`：`wecom_callback_log`（id、occurred_at、msg_signature、signature_valid 0/1、
  decrypt_state CHECK('pending_qualification','signature_mismatch','decrypt_failed','verified')、payload_digest、
  source_ip 可空、note）——只落摘要与状态，不落回调明文，无「添加成功」列。
- db 迁移测试：表存在、二次重放幂等、CHECK 拒成功语义枚举。

### T2 store 层（server/internal/store/store_services.go + store_service_events.go）
- `StoreService`/`StoreServiceInput` + 模型级 ValidateStoreServiceConfig：
  - wifi: ssid 1..64；encryption ∈ open|wpa|wpa2|wpa2_wpa3|wpa3；非 open 必须有 password 8..64；open 时 password 置空。
  - contact: variant ∈ personal|wecom；wecom→url 必填且 https 且 host=work.weixin.qq.com（官方「联系我」链接）；
    personal→wechat_id（6..20，[A-Za-z][A-Za-z0-9_-]{5,19}）或 qr_ref（公网 https）至少其一。
  - community: platform ∈ qq|wechat；group_no/invite_url/qr_ref 至少其一；invite_url 公网 https；approval_note 原样透传展示（≤200）。
  - qr_ref/外链一律 extrajump.OfficialAddress（公网 https）。
- ReplaceStoreServices：4584 同款（expected_revision 乐观锁、重复 id 400、未知 id 400、内容一致幂等重放、
  真实变更 bump revision+快照、platform/kind 不接受客户端回显）；ServiceScope（404 统一）；GetStoreServiceByID；
  ListPublicStoreServices（enabled 且 config 合法才可见，fail-closed）。
- RecordServiceEvent（只收三种枚举）；RecordWecomCallback（digest-only）。

### T3 商家面端点（handlers_store_services.go + server.go 窄注册）
- `GET /api/v1/stores/{id}/services`、`PUT /api/v1/stores/{id}/services`（expected_revision；409 stale_version；
  400 duplicate_service_id/unknown_service_id/bad_service_config(field 级 reason)/bad_target_ref；
  幂等重放不推修订）。文档含 wecom_callback 状态（configured / pending_qualification，诚实）。
- server.go：注册两行 + FEATURE_WECOM_CALLBACK 路由块。

### T4 公共 services 读端点（handlers_public_store_services.go）
- `GET /api/v1/public/storefronts/{storeCode}/services[?environment=][&action_id=]`（FEATURE_STOREFRONT 双保险 gate 同款）：
  - store_actions 投影（kind ∈ review/follow/join_group + A27 share 不投影——裁定书面板清单为点评/关注/Wi-Fi/微信/QQ群）：
    service_id=action 实例 id、target_url（受控解析——4584 公共面刻意不下发、归本票的目标地址）、
    guidance、routes=3535 direct_open 六环境判定（GetActionCapabilitiesByCode(catalog, tenant) best-match；
    无能力记录→routes=[] + admission="unknown"，绝不 promised）；review 项零内容字段。
  - store_services 投影：wifi（ssid/password/encryption+手动连接指引字段）、contact（variant/qr_url/wechat_id/
    wecom_url/source_note）、community（platform/group_no/invite_url/qr_url/approval_note）。
  - 白名单面：无 tenant_id/无内部字段；悬置租户 404；停用店 services 空。
- 负例：非法 environment 400；未知 store_code 404 同形；flag off 404。

### T5 点击事件端点（handlers_public_store_services.go 内）
- `POST /api/v1/public/storefronts/{storeCode}/services/events`（同一 FEATURE_STOREFRONT gate；独立 RateLimiter
  leads.NewRateLimiter 实例）：{service_id, event, environment?}→200 {recorded:true, evidence_tier,
  honesty_note}；service 必须当帧可见（404 否则）；枚举外事件 400——成功语义无处落。
- 事件响应文案锁定：click/jump_attempt 永远附带「不代表已关注/已连接/已添加/已点评」。

### T6 企微回调（handlers_wecom_callback.go + server.go/config.go 窄补丁）
- `GET|POST /api/v1/public/wecom/callback`（FEATURE_WECOM_CALLBACK 默认 off=路由不注册+gate 404 双保险）。
- 验签骨架（真实现非 stub）：signature=sha1(lexsort(token,timestamp,nonce,encrypt))；GET URL 验证：验签+AES-256-CBC
  （EncodingAESKey base64→key，PKCS7 解包）解 echostr 回明文；POST：验签解密→digest-only 落 log（verified=1），
  永不写顾客/门店状态。
- 待资质诚实态：凭据未配置→GET/POST 都答 200 {state:"pending_qualification", verified:false}，
  URL 验证无法完成（不伪造 echostr 应答）；POST 同样落一条 pending_qualification 证据（digest-only）。
- 商家面文档回显 callback 状态；本票无任何「客户已添加」写入路径（结构上不存在）。

### T7 Web 商家配置页
- `web/src/lib/store-services.ts`（纯逻辑镜像 store-channels.ts：normalize/blocker/putBody/saveFailure/canSave/
  wifi 文案回退）；`web/src/app/work/stores/[id]/services/page.tsx`（4584 channels 同风格：列表/启停/排序/新增/
  kind 专属表单（Wi-Fi SSID/密码/加密方式；微信 personal wechat_id+二维码 vs wecom 官方链接；社群群号/邀请链接/
  二维码引用+审批备注）/保存/stale 409 冲突刷新链/待资质徽章；绝不展示 AppSecret 类凭据）。
- channels 页窄补丁：底部一行入口链接到 services 页（同 4584 曾加 stores 列表链接的先例）。
- page-map.ts/page-census.ts 窄补丁（+1 路由，census 16→17）。

### T8 Web 顾客承接面板（components/customer/StoreServicePanels.tsx + lib/store-service-panels.ts）
- 五面板（按 actionId 复用，props=services 投影单项）：ReviewPanel/FollowPanel/WifiPanel/ContactPanel/GroupPanel。
- T12 Wi-Fi：展示 SSID/密码+复制（记录 copy 事件）+「连不上？」手动指引回退区块（无密码强制留资前置）。
- T13 微信与关注：二维码渲染（https 形状校验）+复制微信号+按环境识别步骤；主页链接+可识别账号+未安装手动步骤；
  一切面板零成功话术（无「已关注/已添加/已连接」状态显示）；点评面板零模板零输入零生成（双拦 UI 侧）。
- open/jump_attempt 事件经 POST events 上报（fire-and-forget，失败静默——不上报不阻断使用）。
- 测试：lib 纯逻辑行为测 + 组件源码级断言（仓内惯例，无 DOM harness），锁定双拦与零成功话术。

### T9 自审 + 全量 verify
- code-review 自查 diff（spec 符合性/越权写面/泄漏面）；`cd server && go test ./...`；`cd web && npx vitest run`；
  gofmt；不 push 不 merge。

## 正反验收清单

正例：商家配置 Wi-Fi→公共面读出（密码明文仅展示）；企微官方链接校验通过投影；点评动作投出 target_url+
导航 only 能力；点击事件落库 evidence_tier=click；验签通过的回调落 verified digest 行。
负例：review payload 无内容字段；非官方 wecom 域名 400；密码留资门不存在；事件枚举无成功值；
flag off 全部 404；stale 409 零写入；悬置租户/停用店 fail-closed；凭据缺失→pending_qualification 不伪造验证。

## 明确不做（越界挂账）
- 不动 1671 ChannelChecked 接线（1671 报告自身挂账等渠道实时状态，非本票裁决书面）。
- 不做顾客 C3 页面路由（1896 域）；只交组件+端点。
- 不接真实企微第三方凭据/不改 identity/billing；无 push/merge。
