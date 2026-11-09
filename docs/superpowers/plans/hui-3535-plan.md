# HUI-3535 渠道准入与能力清单（30 项动作、四分能力、环境、应用授权）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline, 本票禁止派生子智能体) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把渠道准入从 5 条平台路线（platform×entry_mode×scenario）扩展为 30 项动作目录（A01–A30）× method × environment × scenario × app_binding 的能力真源，四分能力独立判定（navigation / media_handoff / platform_receipt / reward_eligibility），unknown 永不自动当 manual 许可（PENDING_EXTERNAL 语义），迁移 0060–0063 落 A01–A30 种子行，公开能力投影 + 平台端受控读写 API。

**Architecture:** 现有 `channeladmission` 包（v1 五路线 Gate 纯函数 + vendored seed）原样保留；新增 **动作级能力记录** `channel_action_capabilities`（行键 = catalog_code+platform+method+scenario+tenant_scope，六环境四分能力以 JSON 结构列分别给值）与 **受控变更审计表** `channel_admission_control_log`。种子 = 迁移 INSERT（裁定书拍板，权威初值）；平台端更新 = internal-token 受控端点（写必留痕：操作者/原因/前后哈希）。判定纯函数 `JudgeActionRoute` 与既有 `Gate` 共用绑定/有效期原语，固定拒绝顺序：emergency_disabled → channel_closed → not_admitted → pending_external → admission_expired → wrong_binding → reward_play_not_authorized。

**Tech Stack:** Go 1.x net/http（std mux）、SQLite（modernc.org/sqlite，embed 迁移）、encoding/json。

**Spec:**
- 票面: Linear HUI-3535（权威）
- 裁定书: /Users/jimfu/works/派诺生态/touch-all-20261109/briefs/hui-3535.md
- 共享简报: /Users/jimfu/works/派诺生态/touch-all-20261109/SHARED-BRIEF.md
- 动作目录表: Linear 文档「门店碰一碰全入口实施说明与验收基准｜2026-11-09」§动作目录（A01–A30）

## Global Constraints（裁定书+票面逐条，硬性）

1. 准入维度：platform × action(catalog_code) × method × environment × scenario × app_binding，带有效期与证据引用。
2. 四分能力独立判定，绝不互相推断：「能打开」不推出「能带素材发布」，「有 SDK」不推出「奖励准入」；判定结构上禁止互相派生（各列独立存储、原样返回）。
3. 环境枚举固定六类：desktop_browser / ios_safari / android_browser / ios_wechat / android_wechat / target_app（T07 口径），能力记录按环境分别给值。
4. unknown 永不自动当 manual 许可：资格未知返回 PENDING_EXTERNAL 语义（`pending_external`），顾客侧表现 = 不承诺、不显示可完成态。
5. A01–A30 种子即权威初值（迁移种子行）；抖音店宣通用投稿 = 未准入且拒绝原因明确（含显式手工路线 not_admitted 行，不提供手工绕过）；点评类 = navigation only + reward_eligibility=unsupported；微信普通 H5 = manual 交接（原生发帖契约未核实保持 unknown，不编造 scheme）。
6. 平台端更新走受控记录端点（管理员权限 = internal token；商家会话永不写准入真值）；写必留审计（操作者/原因/动作/载荷指纹）。
7. 返回结构必须含 refused 原因码与 human 说明（reason_code + reason_text）。
8. 公开投影不返回 secret 或内部审核附件（白名单字段）；真实平台资格无账号 = 未验证，诚实标注 evidence_grade，不伪造已获批。
9. A22–A30 为建议补充（`suggested_only=true` 标识），不得悄悄升级为获批自动发布承诺。
10. 只动 channeladmission 领域、该领域 handler/store/迁移和测试；不写平台 SDK、动作注册配置、奖励判定或共享页面。server.go 只允许窄补丁（新增注册行）。
11. 迁移号段 0060–0063，命名 `<NNNN>_<slug>.sql`，放 `server/internal/db/migrations/`。
12. 已知基线红 7 个 httpapi 测试不属本域不修；publishhandoff/adapter.go 的 unknown→manual_only 是 1670 域接线，本票只交付正确语义与报告（见 Concerns）。
13. 测试：`cd server && go test ./...`；每任务红→绿→commit；分支 touch/hui-3535-admission，不 push 不 merge。

## File Structure（写面地图）

- Create: `server/internal/channeladmission/actioncapability.go` — ActionCapability 模型 + 六环境四分能力 + JudgeActionRoute 纯函数 + 原因码常量。
- Create: `server/internal/channeladmission/actioncapability_test.go` — 模型验证、Judge 固定顺序、pending_external 语义、四分独立性。
- Create: `server/internal/db/migrations/0060_channel_action_capabilities.sql` — 动作能力 DDL。
- Create: `server/internal/db/migrations/0061_action_capability_seed_a01_a15.sql` — A01–A15 种子行。
- Create: `server/internal/db/migrations/0062_action_capability_seed_a16_a30.sql` — A16–A30 种子行。
- Create: `server/internal/db/migrations/0063_channel_admission_control_log.sql` — 受控变更审计 DDL。
- Create: `server/internal/store/store_action_capabilities.go` — 动作能力持久层（扫描/查询/受控 upsert/紧急停用/审计）。
- Create: `server/internal/store/store_action_capabilities_test.go` — 迁移种子真值锁定 + 持久层行为。
- Create: `server/internal/httpapi/handlers_action_capabilities.go` — 公开投影 + 管理端读写 handler。
- Create: `server/internal/httpapi/server_action_capabilities_test.go` — HTTP 面（正反例、鉴权、语义）。
- Modify: `server/internal/httpapi/server.go` — 窄补丁：FEATURE_CHANNEL_ADMISSION 块内新增 5 行注册。
- Create: `docs/contracts/channel-admission/v2/action-capabilities.md` — B04 版本化合同（消费方：1670/1676/4589/4592）。

不修改（他人域，只读）：`publishhandoff/*`（adapter.go PlanRoute 缺 RewardPlay/MerchantAppRef 的接线归 1670，报告挂账）、`campaignrules`、奖励判定、web。

---

### Task 1: ActionCapability 模型 + JudgeActionRoute（四分能力 + PENDING_EXTERNAL）

**Files:**
- Create: `server/internal/channeladmission/actioncapability.go`
- Test: `server/internal/channeladmission/actioncapability_test.go`

**Interfaces:**
- Produces（后续任务依赖）:
  - `type ActionCapability struct { ID, CatalogCode, Platform, Method, Scenario, AppBinding, TenantScope, Status string; ActionKind, Label string; SuggestedOnly bool; Environments map[string]EnvCapabilities; FieldSupport map[string]string; EvidenceGrade string; OfficialSources []string; CheckedAt, ValidUntil string; EmergencyDisabled bool; CloseReason string; Missing, RequiredInputs []string; ReasonCode, ReasonText string; SeedVersion int }`
  - `type EnvCapabilities struct { Navigation, MediaHandoff, PlatformReceipt, RewardEligibility string }`
  - `func (r *ActionCapability) Validate() error`、`func (r ActionCapability) Env(environment string) (EnvCapabilities, error)`
  - 环境常量 `EnvDesktopBrowser/EnvIOSSafari/EnvAndroidBrowser/EnvIOSWechat/EnvAndroidWechat/EnvTargetApp` + `Environments`（定序数组）；方法常量 `ActionMethodH5Share/ActionMethodMiniapp/ActionMethodJSMedia/ActionMethodOAuthPush/ActionMethodNativeSDK/ActionMethodLinkShare/ActionMethodManualMaterial/ActionMethodDirectOpen`；场景常量 `ActionScenarioStorePromo/ActionScenarioRewardTask/ActionScenarioReview/ActionScenarioFollow/ActionScenarioService/ActionScenarioGroupBuy`；动作类别 `ActionKindPublish/ActionKindReview/ActionKindFollow/ActionKindService/ActionKindGroupBuy`；证据档 `EvidenceOfficialSpec/EvidenceOfficialDoc/EvidenceOfficialSnapshot/EvidenceDeviceTest/EvidenceUnverified`。
  - `const ReasonEmergencyDisabled = "emergency_disabled"`、`const ReasonPendingExternal = "pending_external"`（gate.go 原因码追加）。
  - `type ActionQuery struct { Now time.Time; TenantID, Environment string; RewardPlay bool; MerchantAppRef string }`
  - `type RouteDecision struct { Allowed bool; Reason, ReasonText string; Missing []string; PendingExternal, Promised bool; Caps EnvCapabilities }`
  - `func JudgeActionRoute(q ActionQuery, r ActionCapability) RouteDecision`

- [ ] **Step 1: 写失败测试** `actioncapability_test.go`，至少覆盖：
  1. `TestActionCapabilityValidate`: 非法 status/method/scenario/kind/evidence/环境键缺失/环境键未知/四分能力非法值 → error；`reward` 类语义约束：status != admitted 时 reward_eligibility 不得为 supported。
  2. `TestJudgeFixedRefusalOrder`: 同一条记录叠加 emergency_disabled+close+not_admitted 只答 `emergency_disabled`（顺序第一位）；去掉 emergency 后答 `channel_closed`；再答 `not_admitted`；unknown → `pending_external` 且 `PendingExternal=true`、`Allowed=false`；过期 → `admission_expired`；错租户/错绑定 → `wrong_binding`；`RewardPlay=true` 且该环境 reward_eligibility != supported → `reward_play_not_authorized`。
  3. `TestPendingExternalNeverManual`: unknown 路由判定后 `PendingExternal=true && Promised=false && Allowed=false`（unknown 永不自动当 manual 许可）；admitted+navigation=supported → `Promised=true`；admitted+manual_material（navigation=unsupported）→ `Promised=true`（顾客自足可完成的诚实可完成态）。
  4. `TestFourWayIndependence`: 构造一条 nav=supported / media=unsupported / receipt=unsupported / reward=unsupported 的点评行，断言 Judge 原样返回四值且不互相改写（改变 navigation 输入不影响其他三值的返回）。
  5. `TestEnvRequired`: `q.Environment` 不在六类 → `Reason=ReasonAdmissionUnknown, PendingExternal=true`（fail-closed）。
- [ ] **Step 2: 运行确认红**: `cd server && go test ./internal/channeladmission/ -run 'TestActionCapability|TestJudge|TestPending|TestFourWay|TestEnvRequired' -v` → 编译失败（类型未定义）。
- [ ] **Step 3: 实现** `actioncapability.go`：
  - 常量与类型如上；`Validate()` 用与 v1 `Record.Validate` 同风格枚举矩阵；`Environments` 必须恰好六个键。
  - `JudgeActionRoute` 复用 gate.go 的 `admissionFresh`；绑定校验抽出 `scopeBindingMatch(tenantScope, appBinding, tenantID, merchantAppRef string) bool`（gate.go `bindingMatches` 改调它，行为不变，v1 测试守护）。
  - 固定顺序实现（每分支一次性 return，禁止穿透）：
    ```
    env 非法 → unknown/pending；EmergencyDisabled → emergency_disabled；
    CloseReason != "" → channel_closed；Status=not_admitted → not_admitted；
    Status=unknown → pending_external(PendingExternal=true)；
    valid_until 缺失/不可解析 → pending_external；过期 → admission_expired；
    绑定不符 → wrong_binding；RewardPlay && caps.RewardEligibility != supported → reward_play_not_authorized；
    否则 Allowed=true，Promised = navigation==supported || method==manual_material。
    ```
- [ ] **Step 4: 跑绿**: 同 Step 2 命令 → 全 PASS；再跑 `go test ./internal/channeladmission/` 全包绿（v1 测试不回归）。
- [ ] **Step 5: Commit**: `git add -A && git commit -m "feat(3535): 动作级能力模型与 JudgeActionRoute——四分能力独立判定+PENDING_EXTERNAL 语义"`

### Task 2: 迁移 0060–0063（DDL + A01–A30 种子 + 审计表）

**Files:**
- Create: `server/internal/db/migrations/0060_channel_action_capabilities.sql`
- Create: `server/internal/db/migrations/0061_action_capability_seed_a01_a15.sql`
- Create: `server/internal/db/migrations/0062_action_capability_seed_a16_a30.sql`
- Create: `server/internal/db/migrations/0063_channel_admission_control_log.sql`
- Test: `server/internal/store/store_action_capabilities_test.go`（本任务只写种子真值断言部分；Task 3 补持久层行为）

**Interfaces:**
- Produces: 表 `channel_action_capabilities`（列见下）与 `channel_admission_control_log`；45 行种子（Task 3 store 扫描依赖列序）。

**DDL（0060）：**

```sql
-- HUI-3535 (2026-11-09 增量): 动作级渠道能力真源。行键 catalog_code+platform+
-- method+scenario+tenant_scope；六环境四分能力(navigation/media_handoff/
-- platform_receipt/reward_eligibility)在 environments JSON 列按环境分别给值，
-- 四者独立存储、绝不互相推断。种子=迁移权威初值(裁定书)；平台端更新走受控
-- 记录端点(internal token)，本迁移只落初值不承担后续维护。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS channel_action_capabilities (
    id                 TEXT PRIMARY KEY,
    catalog_code       TEXT NOT NULL,
    platform           TEXT NOT NULL,
    method             TEXT NOT NULL CHECK (method IN
        ('h5_share','miniapp','js_media_handoff','oauth_server_push','native_sdk',
         'link_share','manual_material','direct_open')),
    scenario           TEXT NOT NULL CHECK (scenario IN
        ('store_promo','reward_task','review','follow','service','group_buy')),
    action_kind        TEXT NOT NULL CHECK (action_kind IN
        ('publish','review','follow','service','group_buy')),
    label              TEXT NOT NULL,
    suggested_only     INTEGER NOT NULL CHECK (suggested_only IN (0,1)),
    app_binding        TEXT NOT NULL CHECK (app_binding IN ('platform_app','merchant_authorized','none')),
    tenant_scope       TEXT NOT NULL DEFAULT '*',
    status             TEXT NOT NULL CHECK (status IN ('admitted','not_admitted','unknown')),
    environments       TEXT NOT NULL,           -- JSON: 六环境键 -> 四分能力三态
    field_support      TEXT NOT NULL DEFAULT '{}', -- JSON: media/title/body/topic/poi 三态
    required_inputs    TEXT NOT NULL DEFAULT '[]', -- JSON: content_pack_version 等
    evidence_grade     TEXT NOT NULL CHECK (evidence_grade IN
        ('official_spec','official_doc','official_snapshot','device_test','unverified')),
    official_sources   TEXT NOT NULL DEFAULT '[]',
    checked_at         TEXT NOT NULL,
    valid_until        TEXT NOT NULL,
    emergency_disabled INTEGER NOT NULL DEFAULT 0 CHECK (emergency_disabled IN (0,1)),
    close_reason       TEXT NOT NULL DEFAULT '',
    missing            TEXT NOT NULL DEFAULT '[]',
    reason_code        TEXT NOT NULL,
    reason_text        TEXT NOT NULL,
    seed_version       INTEGER NOT NULL CHECK (seed_version >= 1),
    payload_hash       TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (catalog_code, platform, method, scenario, tenant_scope)
);
CREATE INDEX IF NOT EXISTS idx_channel_action_capabilities_code
    ON channel_action_capabilities(catalog_code, tenant_scope);
```

**种子真值规格（45 行；checked_at=2026-11-09（票面官方检索日），valid_until=2027-02-07（90 天复检窗），tenant_scope='*'，seed_version=1；id = `cap_<code小写>_<method>`；六环境四分能力默认同值——无真机差分证据前不给环境差分承诺，publish 类 missing 含「六环境真机差分验证(T07)」）：**

| 行 | code/platform/method/scenario | status | nav/media/receipt/reward | evidence | 要点 |
|---|---|---|---|---|---|
| 1 | A01 douyin h5_share store_promo | not_admitted | supported/supported/unknown/unsupported | official_spec | 技术能力存在与商铺宣传不准入同时表达；reason=not_admitted |
| 2 | A01 douyin miniapp reward_task | unknown | unknown×4 | official_doc | 任务能力需申请批复；reason=pending_external |
| 3 | A01 douyin manual_material store_promo | not_admitted | unsupported/unsupported/unsupported/unsupported | official_spec | 显式手工绕过拒绝行：手工交接不构成绕过通道 |
| 4 | A02 xiaohongshu js_media_handoff store_promo | unknown | supported/supported/unknown/unknown | official_snapshot | 2026-11-09 快照暂停新接入；SDK 不自动填 title/body/topic；reason=pending_external |
| 5 | A02 xiaohongshu manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | official_snapshot | 允许场景可手动保存/自主编辑（用户主动复制另记动作） |
| 6 | A03 kuaishou oauth_server_push store_promo | unknown | supported/supported/unknown/unknown | official_doc | 异步接受≠最终公开，需作品查询；scopes 未批 |
| 7 | A03 kuaishou native_sdk store_promo | unknown | supported/supported/unknown/unknown | official_doc | 移动应用路线，话题携带另有限制 |
| 8 | A03 kuaishou manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | official_doc | 适用场景手动指引 |
| 9 | A04 wechat manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | unverified | 保存后微信内自行发布；不声称直接发出 |
| 10 | A04 wechat h5_share store_promo | unknown | unknown×4 | unverified | 普通 H5 原生发帖契约未核实，不编造 scheme；reason=pending_external |
| 11 | A04 wechat link_share store_promo | admitted | supported/unsupported/unsupported/unknown | official_doc | 链接分享与原生发布为不同能力；required_inputs=[] |
| 12 | A05 wechat manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | unverified | 同 A04 图片组 |
| 13 | A05 wechat h5_share store_promo | unknown | unknown×4 | unverified | 同 A04 |
| 14 | A05 wechat link_share store_promo | admitted | supported/unsupported/unsupported/unknown | official_doc | required_inputs=[] |
| 15 | A06 wechat_channels manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | unverified | 打开主页/作品≠创建新视频 |
| 16 | A06 wechat_channels h5_share store_promo | unknown | unknown×4 | unverified | 官方可用路线单独登记；reason=pending_external |
| 17 | A07 xiaohongshu js_media_handoff store_promo | unknown | supported/supported/unknown/unknown | official_snapshot | JS type normal 交接图片；同 A02 纪律 |
| 18 | A07 xiaohongshu manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | official_snapshot | 同 A02 |
| 19-23 | A08 dianping / A09 douyin / A10 amap / A11 baidu / A12 meituan —— direct_open review | admitted | supported/unsupported/±/unsupported | official_doc | 点评=navigation only+reward=unsupported；receipt：A08=unsupported、A09-A12=unknown（各自原因：rateAwemeOrder 限宿主订单/仅记尝试/URI≠评价API/分渠道统计）；无模板/AIGC、不查截图、不以评价换奖励写进 reason_text |
| 24 | A13 douyin direct_open follow | admitted | supported/unsupported/unknown/unknown | official_doc | 小程序关注组件有绑定/权限条件；H5 点击不算关注 |
| 25 | A14 kuaishou direct_open follow | admitted | supported/unsupported/unsupported/unknown | official_doc | 无可靠回执仅记访问 |
| 26 | A15 xiaohongshu direct_open follow | admitted | supported/unsupported/unknown/unknown | official_doc | followOfficialAccount 结果≠H5 普遍可查 |
| 27 | A16 wechat direct_open follow | admitted | supported/unsupported/unknown/unknown | official_doc | 二维码+复制微信号；不伪造直接关注 |
| 28 | A17 wifi direct_open service | admitted | supported/unsupported/unsupported/unknown | official_doc | H5 展示并复制密码；实际连接与展示分开；required_inputs=[] |
| 29 | A17 wechat miniapp service | unknown | unknown/unsupported/unknown/unknown | official_doc | wx.connectWifi 需小程序接入；reason=pending_external |
| 30 | A18 wechat direct_open service | admitted | supported/unsupported/unknown/unknown | unverified | 个人微信无通用添加回执；企微验签本轮未核实；required_inputs=[] |
| 31 | A19 meituan direct_open group_buy | admitted | supported/unsupported/unknown/unsupported | official_doc | 平台支付/退款/核销归平台；本地不假核销；required_inputs=[] |
| 32 | A20 dianping direct_open group_buy | admitted | supported/unsupported/unknown/unsupported | official_doc | 同 A19；保留渠道归因 |
| 33 | A21 douyin direct_open group_buy | admitted | supported/unsupported/unknown/unsupported | official_doc | 点击不算成交；本地券不能核销抖音券 |
| 34 | A21 douyin miniapp group_buy | unknown | unknown/unsupported/unknown/unknown | official_doc | 订单核销仅获准接口；reason=pending_external |
| 35 | A22 bilibili manual_material store_promo | admitted | unsupported/supported/unknown/unknown | official_doc | 素材交接+官方投稿指引；OpenAPI 另登记 |
| 36 | A22 bilibili h5_share store_promo | unknown | unknown×4 | official_doc | 服务端 API 不使移动 H5 编辑器 supported；reason=pending_external |
| 37 | A23 bilibili direct_open follow | admitted | supported/unsupported/unsupported/unknown | official_doc | 点击不算关注 |
| 38 | A24 weibo manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | unverified | 不冒充免授权服务端发布 |
| 39 | A25 weibo manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | unverified | 发布接受与实际公开分开 |
| 40 | A26 weibo direct_open follow | admitted | supported/unsupported/unsupported/unknown | official_doc | 点击不算关注 |
| 41 | A27 qq link_share store_promo | admitted | supported/unsupported/unsupported/unknown | unverified | 合法纯链接分享；required_inputs=[]（无内容包/媒体授权）；目标由系统决定 |
| 42 | A28 qzone manual_material store_promo | admitted | unsupported/supported/unsupported/unknown | unverified | PC 分享组件≠移动 H5 原生多图 |
| 43 | A28 qzone link_share store_promo | unknown | unknown/unsupported/unsupported/unknown | unverified | 移动端分享组件未核实；reason=pending_external |
| 44 | A29 qq direct_open follow | admitted | supported/unsupported/unknown/unknown | official_doc | 仅点击不能确认入群 |
| 45 | A30 wechat_channels direct_open follow | admitted | supported/unsupported/unknown/unknown | unverified | 打开主页≠关注；环境差异实测 |

A22–A30 行 `suggested_only=1`；A01–A21 行 `suggested_only=0`。每行 official_sources 取票面官方依据 URL（douyin useclue / douyin H5 share / xiaohongshu agora JS+QA / kuaishou createAVideo / weixin JS-SDK+channels / kuaishou android SDK / open.bilibili.com 等，与 v1 seed 同库）；点评/关注/服务/团购行 official_sources 给对应平台公开页或按 v1 惯例给官方入口 URL，missing 如实列「无官方回执」等缺项。

**0063（审计表）：**

```sql
-- HUI-3535: 受控变更审计。平台端更新(受控记录端点, internal token)必留痕:
-- 操作者/原因/动作/载荷指纹。迁移种子是版本化发布初值,不经本表。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS channel_admission_control_log (
    id            TEXT PRIMARY KEY,
    capability_id TEXT NOT NULL,
    catalog_code  TEXT NOT NULL,
    platform      TEXT NOT NULL,
    method        TEXT NOT NULL,
    scenario      TEXT NOT NULL,
    tenant_scope  TEXT NOT NULL,
    action        TEXT NOT NULL CHECK (action IN
        ('create','update','emergency_disable','emergency_enable')),
    actor         TEXT NOT NULL,
    reason        TEXT NOT NULL,
    payload_hash  TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_channel_admission_control_log_code
    ON channel_admission_control_log(catalog_code, created_at);
```

- [ ] **Step 1: 写失败测试**（store 包新文件 `store_action_capabilities_test.go` 的种子真值段）：`db.Open(t.TempDir())` 后 `st.ListActionCapabilities("t_any")`（Task 3 才有——本任务先断言原生 SQL：`d.Query("SELECT COUNT(1) FROM channel_action_capabilities")`）：
  1. `TestMigrationSeeds45Rows`: 总行数 = 45；`SELECT COUNT(DISTINCT catalog_code)` = 30；`SELECT COUNT(1) ... WHERE suggested_only=1` = 11；A01–A30 逐码存在。
  2. `TestSeedRulingPins`: A01 h5_share → status=not_admitted, reason_code=not_admitted；A01 manual_material → status=not_admitted（手工绕过显式拒绝）；A08–A12 → nav=supported ∧ media=unsupported ∧ reward=unsupported；A04/A05 h5_share → status=unknown ∧ evidence_grade=unverified；A27 link_share → status=admitted ∧ required_inputs='[]'；全部行 reward 受约束：status≠admitted ⇒ 四分中 reward_eligibility≠supported；checked_at='2026-11-09' ∧ valid_until='2027-02-07'；六环境 JSON 键恰为六类且各含四分三态合法值。
  3. `TestControlLogEmpty`: channel_admission_control_log 存在且为空（种子不经审计表）。
- [ ] **Step 2: 跑红**: `cd server && go test ./internal/store/ -run 'TestMigrationSeeds|TestSeedRulingPins|TestControlLogEmpty' -v` → no such table。
- [ ] **Step 3: 生成并落盘四个迁移文件**。种子 SQL 用一次性 Python 生成器（值表逐行编码上表；canonical JSON 由 `json.dumps(..., sort_keys=True, separators=(',',':'), ensure_ascii=False)`；payload_hash=sha256(canonical JSON of (catalog_code,platform,method,scenario,app_binding,tenant_scope,status,environments,field_support,required_inputs,evidence_grade,checked_at,valid_until))，生成器对每行断言六环境键完整与三态合法——生成器跑在临时目录，产物只有迁移 SQL 入库）：
  ```python
  #!/usr/bin/env python3 -I
  # 一次性生成 0061/0062 种子 SQL；值表硬编码（上表）；临时目录运行，产物粘贴进迁移文件。
  import json, hashlib
  ENVS = ["desktop_browser","ios_safari","android_browser","ios_wechat","android_wechat","target_app"]
  TRI = {"supported","unsupported","unknown"}
  # rows = [ (code, platform, method, scenario, kind, label, suggested, binding, status,
  #           (nav,media,receipt,reward), field_support dict, required_inputs list,
  #           evidence, sources list, reason_code, reason_text, missing list), ... ]  # 45 行硬编码
  def env_json(caps):
      m = {e: dict(zip(("navigation","media_handoff","platform_receipt","reward_eligibility"), caps)) for e in ENVS}
      for e, c in m.items():
          assert set(c.values()) <= TRI, c
      return json.dumps(m, sort_keys=True, separators=(",",":"), ensure_ascii=False)
  def sql_str(s): return "'" + s.replace("'", "''") + "'"
  def emit(rows, path, comment):
      out = [comment, "PRAGMA foreign_keys = ON;\n"]
      for r in rows:
          (code,platform,method,scenario,kind,label,sugg,binding,status,caps,fs,ri,ev,src,rc,rt,mi) = r
          payload = {"catalog_code":code,"platform":platform,"method":method,"scenario":scenario,
                     "app_binding":binding,"tenant_scope":"*","status":status,"environments":json.loads(env_json(caps)),
                     "field_support":fs,"required_inputs":ri,"evidence_grade":ev,
                     "checked_at":"2026-11-09","valid_until":"2027-02-07"}
          h = hashlib.sha256(json.dumps(payload,sort_keys=True,separators=(",",":"),ensure_ascii=False).encode()).hexdigest()
          cid = "cap_%s_%s" % (code.lower(), method)
          vals = [sql_str(x) for x in (cid,code,platform,method,scenario,kind,label,
                                         "1" if sugg else "0",binding,"*",status,
                                         sql_str(env_json(caps)),sql_str(json.dumps(fs,sort_keys=True,separators=(",",":"),ensure_ascii=False)),
                                         sql_str(json.dumps(ri,separators=(",",":"),ensure_ascii=False)),
                                         sql_str(ev),sql_str(json.dumps(src,separators=(",",":"),ensure_ascii=False)),
                                         sql_str("2026-11-09"),sql_str("2027-02-07"),
                                         sql_str(json.dumps(mi,separators=(",",":"),ensure_ascii=False)),
                                         sql_str(rc),sql_str(rt),"1",sql_str(h))]
          out.append("INSERT INTO channel_action_capabilities(id,catalog_code,platform,method,scenario,"
                     "action_kind,label,suggested_only,app_binding,tenant_scope,status,environments,"
                     "field_support,required_inputs,evidence_grade,official_sources,checked_at,valid_until,"
                     "missing,reason_code,reason_text,seed_version,payload_hash)\nVALUES (%s,'2026-11-09T00:00:00Z');" % ",".join(vals))
      open(path,"w").write("\n\n".join(out)+"\n")
  ```
  （45 行 `rows` 值表按上表逐行硬编码；本计划不内联全部 45 行 Python 字面量，生成器以 Step 3 表格为唯一值源，产物迁移 SQL 里每一行可对照上表 review。）
- [ ] **Step 4: 跑绿**: Step 2 命令全 PASS；`go test ./internal/db/` 绿。
- [ ] **Step 5: Commit**: `git add -A && git commit -m "feat(3535): 迁移0060-0063——动作能力DDL+A01-A30四分能力种子+受控审计表"`

### Task 3: store 持久层（扫描/查询/受控 upsert/紧急停用/审计）

**Files:**
- Create: `server/internal/store/store_action_capabilities.go`
- Test: `server/internal/store/store_action_capabilities_test.go`（追加行为段）

**Interfaces:**
- Consumes: Task 1 类型；`newID("cap_")`/`newID("cac_")`/`now()`（store.go 既有）。
- Produces:
  - `func scanActionCapability(sc interface{ Scan(...any) error }) (channeladmission.ActionCapability, error)`（JSON 列扫描即验证：六环境键、三态、field_support 键白名单 media/title/body/topic/poi）
  - `func actionCapabilityVals(r channeladmission.ActionCapability, payloadHash string) ([]any, error)`
  - `func actionCapabilityPayloadHash(r channeladmission.ActionCapability) string`（canonical JSON sha256，与 store_channel_admission.go 同法）
  - `func (s *Store) ListActionCapabilities(tenantID string) ([]channeladmission.ActionCapability, error)`（'*' + 本租户；ORDER BY catalog_code,method）
  - `func (s *Store) ListActionCapabilitiesAll() ([]channeladmission.ActionCapability, error)`（平台运维读，含所有租户定向行）
  - `func (s *Store) GetActionCapabilitiesByCode(catalogCode, tenantID string) ([]channeladmission.ActionCapability, error)`（'*' OR tenant best-match 不做单行取舍，返回该码全部可见行）
  - `func (s *Store) UpsertActionCapability(r channeladmission.ActionCapability, audit ControlAudit) (created bool, err error)`（同事务：hash 未变 = no-op false；变了 = insert/update + 审计行）
  - `func (s *Store) SetActionCapabilityEmergency(id string, disabled bool, audit ControlAudit) (bool, error)`（found=false → ErrNotFound；变更必写审计）
  - `type ControlAudit struct { Actor, Reason, Action string }`（Action ∈ create|update|emergency_disable|emergency_enable，Upsert 内部按 insert/update 归一）
  - `func (s *Store) ListActionCapabilityAudit(catalogCode string, limit int) ([]ActionCapabilityAuditRow, error)`、`type ActionCapabilityAuditRow struct { ID, CapabilityID, CatalogCode, Platform, Method, Scenario, TenantScope, Action, Actor, Reason, PayloadHash, CreatedAt string }`
- 域纪律：存储层零业务判断（Validate 在模型层，Upsert 先 `r.Validate()`）；审计在同一事务（票面 F20：来源/操作者/原因留痕）。

- [ ] **Step 1: 写失败测试**（追加到 store_action_capabilities_test.go）：
  1. `TestActionCapScanRoundTrip`: 原生 INSERT 一行后 `GetActionCapabilitiesByCode("A01","")` 能扫出且字段/JSON 往返一致；坏 environments JSON → 扫描报错（fail-closed）。
  2. `TestActionCapTenantScope`: 插入 tenant_scope='t_x' 定向行后：ListActionCapabilities("t_x") 含它，ListActionCapabilities("t_y") 只见 '*' 行；ListActionCapabilitiesAll 全见。
  3. `TestUpsertAuditIdempotent`: 同记录连续两次 UpsertActionCapability（同 actor/reason）→ 第二次 created=false 且审计行数不增；改 status 再 upsert → created=false（update 路径）+ 审计 +1，行内容更新。
  4. `TestEmergencyToggleAudit`: SetActionCapabilityEmergency(id,true,...) → 行 emergency_disabled=1 + 审计 emergency_disable；未 gen id → ErrNotFound 且无审计。
  5. `TestAuditList`: ListActionCapabilityAudit 按 created_at 序返回。
- [ ] **Step 2: 跑红**: `go test ./internal/store/ -run 'TestActionCap|TestUpsertAudit|TestEmergencyToggle|TestAuditList' -v` → 编译失败。
- [ ] **Step 3: 实现** store_action_capabilities.go（列序与 0060 DDL 一致；UPSERT 用「先查 hash 再 insert/update」同 store_channel_admission.go 模式；审计行在事务内）。
- [ ] **Step 4: 跑绿**: 上述命令 PASS；`go test ./internal/store/` 全包绿（v1 admission 测试不回归）。
- [ ] **Step 5: Commit**: `git add -A && git commit -m "feat(3535): 动作能力持久层——租户作用域读取+受控upsert+同事务审计+紧急停用"`

### Task 4: 公开能力投影 GET /api/v1/public/actions/{id}/capabilities

**Files:**
- Create: `server/internal/httpapi/handlers_action_capabilities.go`
- Test: `server/internal/httpapi/server_action_capabilities_test.go`
- Modify: `server/internal/httpapi/server.go`（FEATURE_CHANNEL_ADMISSION 块内新增注册行——窄补丁）

**Interfaces:**
- Consumes: `s.St.GetActionCapabilitiesByCode`、`channeladmission.JudgeActionRoute`、`admissionGate`（404 双保险）、`fail/writeJSON`。
- Produces:
  - `func (s *Server) handlePublicActionCapabilities(w http.ResponseWriter, r *http.Request)`
  - 投影类型 `publicActionCapabilitiesPayload{ActionID, CatalogCode, ActionKind, Label string; SuggestedOnly bool; Environment string; Routes []publicCapabilityRoute}`；`publicCapabilityRoute{Platform, Method, Scenario, AppBinding, Status string; Allowed, Promised, PendingExternal bool; Reason, ReasonText string; Navigation, MediaHandoff, PlatformReceipt, RewardEligibility string; FieldSupport map[string]string; RequiredInputs, Missing, OfficialSources []string; EvidenceGrade, CheckedAt, ValidUntil string; EmergencyDisabled bool; CloseReason string}`
  - 语义：`{id}` = catalog_code（A01–A30，大小写不敏感归一为大写）；查询参数 `environment`（六类之一，缺省=全部环境展开为每 method×environment 一条 route）、`method`、`scenario`（可选过滤，值非法→400 param_invalid）；`RewardPlay` 恒 false（公开投影只答能力真值，奖励判定在消费方服务端过 Gate）、`TenantID=""`（只匹配 tenant_scope='*' 平台真值；不接受客户端租户参数——拒绝探测面）；unknown code / 零行 → 404 `{"error":"not_found"}`；`RewardPlay/MerchantAppRef` 不进公开面。
  - pending_external 路由：`Allowed=false, PendingExternal=true, Promised=false` 且 reason_text 明示「资格未知（PENDING_EXTERNAL）：不承诺、不显示可完成态，也不降级为手动发布许可」。

- [ ] **Step 1: 写失败测试**（复用 `newAdmissionFixture(t,true)`；新增 fixture helper `f.getJSON`）：
  1. `TestPublicActionCapabilitiesHonest`: `GET /api/v1/public/actions/A01/capabilities` → 200；routes 含 h5_share 行：allowed=false ∧ reason=not_admitted ∧ reason_text 非空（human 说明）；A01 手工路线行 not_admitted（无手工绕过）；`GET .../A01/capabilities?environment=ios_safari` → 每条 route 带 environment=ios_safari 且四分值为该环境值；environment=bogus → 400；`GET .../A99/capabilities` → 404（not_found）。
  2. `TestPublicActionCapabilitiesPendingExternal`: A04 h5_share 路由 → allowed=false ∧ pending_external=true ∧ promised=false；A02 js_media_handoff → pending_external=true（暂停新接入）；顾客侧纪律断言：任何 route 的 promised=true ⇒（status=admitted ∧ (navigation=supported ∨ method=manual_material)）。
  3. `TestPublicActionCapabilitiesLinkShareNoInputs`: A27 → link_share 路由 allowed=true ∧ required_inputs=[]（纯链接无需内容包/媒体授权）；A02 路由 required_inputs=["content_pack_version"]。
  4. `TestPublicActionCapabilitiesReviewsNavigationOnly`: A08 → 恰一 method=direct_open 路由：nav=supported ∧ media=unsupported ∧ receipt=unsupported ∧ reward=unsupported ∧ reason_text 含「评价」纪律字样。
  5. `TestPublicActionCapabilitiesSuggestedOnly`: A22 label/suggested_only=true；A01 suggested_only=false。
  6. `TestPublicActionCapabilitiesOffInvisible`: flag off → 404；`TestPublicActionCapabilitiesNoSecrets`: 响应 JSON 无 seed_version/payload_hash/tenant_scope 字段（白名单投影）。
- [ ] **Step 2: 跑红**: `go test ./internal/httpapi/ -run 'TestPublicActionCapabilities' -v` → 404（路由未注册）。
- [ ] **Step 3: 实现 handler + server.go 窄补丁注册行**：
  ```go
  mux.Handle("GET /api/v1/public/actions/{id}/capabilities", http.HandlerFunc(s.handlePublicActionCapabilities))
  ```
- [ ] **Step 4: 跑绿**: Step 2 命令 PASS；`go test ./internal/httpapi/ -run 'TestChannelAdmission|TestPublicPublishHandoff' -v` 原有相关测试不回归（v1 两面语义不变）。
- [ ] **Step 5: Commit**: `git add -A && git commit -m "feat(3535): 公开动作能力投影——B04 GET /public/actions/{id}/capabilities（四分能力+PENDING_EXTERNAL 白名单投影）"`

### Task 5: 管理端受控读写 API（internal token 平台权威 + 会话租户视图）

**Files:**
- Modify: `server/internal/httpapi/handlers_action_capabilities.go`（追加管理面 handler）
- Test: `server/internal/httpapi/server_action_capabilities_test.go`（追加）
- Modify: `server/internal/httpapi/server.go`（追加 4 行注册——窄补丁）

**Interfaces:**
- Produces:
  - `GET /api/v1/channel-actions` — 双模式读：internal token → 全量（含租户定向行）；X-Session-Token 会话 → 租户可见（'*'+本租户）；两者皆无 → 401。会话视图 payload 与公开面同白名单 + tenant_scope。
  - `POST /api/v1/channel-actions` — `requireInternal` 受控写入：body = ActionCapability 全字段 + `actor` + `reason`（必填，空→400 reason_required）；`Validate()` 失败→422 invalid_record；同码元组已存在且 hash 未变→200 unchanged；新建→201；更新→200。响应含 row + audit action。
  - `POST /api/v1/channel-actions/{id}/emergency` — `requireInternal`：body `{disabled bool, reason string}`；reason 必填；404 未知 id；200 返回 {emergency_disabled, reason}。
  - `GET /api/v1/channel-actions/{code}/audit` — `requireInternal`：审计行列表（limit 200）。
  - 注册行（FEATURE_CHANNEL_ADMISSION 块内）：
    ```go
    mux.Handle("GET /api/v1/channel-actions", http.HandlerFunc(s.handleChannelActionList))
    mux.Handle("POST /api/v1/channel-actions", s.requireInternal(s.handleChannelActionUpsert))
    mux.Handle("POST /api/v1/channel-actions/{id}/emergency", s.requireInternal(s.handleChannelActionEmergency))
    mux.Handle("GET /api/v1/channel-actions/{code}/audit", s.requireInternal(s.handleChannelActionAudit))
    ```
  - 纪律：商家会话永不写准入真值（商户租户不是平台权威；O4 暂停/恢复归 4592 走 internal 面）。

- [ ] **Step 1: 写失败测试**：
  1. `TestChannelActionsReadAuth`: 无凭据 → 401；会话（sess-owner-a）→ 200 且只含 '*' 行（数=45）；internal token → 200（全量）。
  2. `TestChannelActionsUpsertControlled`: 无 token POST → 401；有 token 新建合法记录 → 201 + audit=create；再发同记录 → 200 unchanged 且审计不增；改 status=admitted（合法值）→ 200 + audit=update；body 缺 reason → 400；`status="allowed"` → 422 invalid_record（模型层拒绝非法枚举）；reward_eligibility=supported 而 status=unknown → 422（unknown 永不开奖励）。
  3. `TestChannelActionsEmergency`: emergency disable A01 h5_share 行（取 list 得 id）→ POST emergency {disabled:true,reason:"平台故障"} → 200；随后公开面 A01 h5_share 路由 reason=emergency_disabled（只停对应方法/动作——回归：同请求里 A01 manual_material 路由仍答 not_admitted、A02 路由不受影响）；enable 恢复 → 公开面回到 not_admitted；reason 缺失 → 400；未知 id → 404。
  4. `TestChannelActionsAuditRead`: GET audit → 200 按 code 返回上述写操作审计（actor/reason 留痕）。
- [ ] **Step 2: 跑红**: `go test ./internal/httpapi/ -run 'TestChannelActions' -v` → 404。
- [ ] **Step 3: 实现** handler + 注册。
- [ ] **Step 4: 跑绿**: 本命令 PASS + `go test ./internal/httpapi/ -run 'TestChannelAdmission|TestPublicActionCapabilities' -v` 不回归。
- [ ] **Step 5: Commit**: `git add -A && git commit -m "feat(3535): 管理端受控读写——internal token 平台权威+会话租户视图+紧急停用+审计查询"`

### Task 6: 合同文档 + 全量验证 + 自查 + 收尾

**Files:**
- Create: `docs/contracts/channel-admission/v2/action-capabilities.md`（B04 版本化合同：维度/四分/环境枚举/端点/语义/消费指引 1670/1676/4589/4592）

- [ ] **Step 1: 写合同文档**：端点、route JSON 形状（与实现逐字段一致）、固定拒绝顺序、pending_external 顾客侧纪律、受控写权限模型、种子初值摘要（45 行/30 码/建议 9 项标识）、消费方接线指引（1670：PlanRoute 需传 RewardPlay/MerchantAppRef 完整服务器上下文；unknown→manual_only 旧映射必须换 pending_external；1676：点评不可配奖励=reward_eligibility=unsupported；4589/4590：场景裁决读本面；4592：O4 健康视图走管理端读 API）。
- [ ] **Step 2: 全量验证**: `cd server && go test ./...`（后台跑，~10 分钟）。预期：仅 httpapi 既有 7 个基线红（LEDGER 归属非本域）；本包/store/db/httpapi 本票新增测试全绿；`go vet ./...` 干净。
- [ ] **Step 3: 自我 code-review**: diff 全读一遍（spec 符合性：四分独立、unknown 纪律、种子诚实、白名单投影、审计留痕、窄补丁纪律；质量：错误处理/命名/注释/测试断言非空洞）。发现问题即修并补 commit。
- [ ] **Step 4: finishing-branch**: `git status` 干净、log 全在分支；**不 push 不 merge**。
- [ ] **Step 5: 写报告** `/Users/jimfu/works/派诺生态/touch-all-20261109/reports/hui-3535-report.md`：STATUS + 分支头 SHA + 测试摘要 + concerns（PlanRoute RewardPlay/MerchantAppRef 接线归 1670；adapter unknown→manual_only 旧语义须换 pending_external；真实平台资格未验证残留）。

---

## Self-Review

1. **Spec coverage:** 裁定书五条范围裁定 → Task 1（四分+pending_external）、Task 2（六环境+种子三钉子：抖音未准入/点评 navigation-only/微信 manual）、Task 4（公开投影含 refused 原因码+human 说明）、Task 5（受控读写+审计）、Task 6（消费指引）。边界裁定（1670/4584/4589/4590 读我；O4 归 4592 本票只给管理端 API）→ Task 5 + 合同文档。✓
2. **Placeholder scan:** Task 2 Step 3 的 45 行值表以规格表格为唯一值源并要求逐行对照 review，无 "TBD/TODO"。✓
3. **Type consistency:** `JudgeActionRoute/ActionQuery/RouteDecision/EnvCapabilities` 与 Task 3/4/5 引用一致；store 接口名与 handler 调用一致。✓
