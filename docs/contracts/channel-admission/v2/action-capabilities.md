# 渠道能力合同 v2｜动作级准入与四分能力（HUI-3535，2026-11-09 增量）

状态：**实施契约（本分支已实现，服务端真源）**。种子初值由迁移 `0060_channel_action_capabilities.sql` / `0061` / `0062` 落库（45 行 / A01–A30 全目录 / 6 环境四分能力），平台端更新只走受控记录端点（internal token），每次写留审计（`channel_admission_control_log`）。v1 平台路线面（`channel_admissions` 五行 + vendored seed）原样保留，两套并存互不影响。

本合同是 B04 的服务端口径。消费方：1670（交接 Gate）、1676（商家配置缺项）、1896（顾客六视图）、4589/4590（场景裁决）、4592（O4 渠道健康读）、3546。

## 1. 维度与语义

记录键：`catalog_code (A01–A30) × platform × method × scenario × tenant_scope`。六类环境（T07 口径）按环境分别给值：

```
desktop_browser | ios_safari | android_browser | ios_wechat | android_wechat | target_app
```

每环境四分能力（三态 `supported|unsupported|unknown`，**独立判定，绝不互相推断**）：

| 能力 | 语义 |
|---|---|
| `navigation` | 能否（由系统）打开平台目标 |
| `media_handoff` | 能否带素材（受控媒体交接） |
| `platform_receipt` | 有无官方回执（平台可证实结果） |
| `reward_eligibility` | 该场景奖励是否允许（须平台批复；unknown 永不开奖励） |

「能打开」不推出「能带素材发布」，「有 SDK」不推出「奖励准入」——判定代码没有任何一列由其他列派生（`channeladmission.JudgeActionRoute` 原样返回存储值，结构保证）。

方法枚举：`h5_share | miniapp | js_media_handoff | oauth_server_push | native_sdk | link_share | manual_material | direct_open`。
场景枚举：`store_promo | reward_task | review | follow | service | group_buy`。
证据档：`official_spec | official_doc | official_snapshot | device_test | unverified`。种子 checked_at=2026-11-09（票面官方检索日），valid_until=2027-02-07（90 天复检窗），过窗即 `admission_expired`。

## 2. 固定拒绝顺序（`JudgeActionRoute`）

```
emergency_disabled → channel_closed → not_admitted → pending_external
→ admission_expired → wrong_binding → reward_play_not_authorized → allowed
```

- `pending_external`：资格未知（PENDING_EXTERNAL）——**unknown 永不自动当 manual 许可**；顾客侧不承诺、不显示可完成态。
- `emergency_disabled`：独立否决第一位；只停对应方法/动作，无关导航与其他动作不受影响。
- 每个拒绝答案必带 `reason`（机器码）+ `reason_text`（human 说明）+ `missing`（缺项清单）。

顾客侧可完成态（`promised=true`）只有两种诚实来源：`status=admitted ∧ (navigation=supported ∨ method=manual_material)`。`RewardPlay/MerchantAppRef` 是服务端派生事实（营销场景性质与真实 App 绑定），客户端标记不可信。

## 3. 端点（FEATURE_CHANNEL_ADMISSION 开关；off=路由不注册+统一 404）

| 端点 | 鉴权 | 用途 |
|---|---|---|
| `GET /api/v1/public/actions/{id}/capabilities?environment=&method=&scenario=` | 公开 | B04 投影：`{id}`=目录码（A01–A30，大小写不敏感）；按 `method×environment` 展开 routes；只答 `tenant_scope='*'` 平台真值，不接受客户端租户参数；白名单投影（无 seed_version/payload_hash/tenant_scope/id）；未知动作 404；非法环境 400 |
| `GET /api/v1/channel-actions` | 双模 | internal token 且无会话 → `scope=platform` 全量（含他租户定向行，O4/4592 读）；否则 requireSession → `scope=tenant`（'*'+本租户，1676 读缺项） |
| `POST /api/v1/channel-actions` | internal token | 受控写入：整条记录 + `actor` + `reason`（必填）；模型层校验失败 422；响应 `created(201)/updated(200)/unchanged(200)`；审计动作由存储端派生（insert=create/update=update），哈希口径与迁移种子一致——不变的种子行重写=no-op 无审计 |
| `POST /api/v1/channel-actions/{id}/emergency` | internal token | `{disabled, reason, actor?}`；独立否决开关，只停对应方法/动作；未知 id 404 |
| `GET /api/v1/channel-actions/{code}/audit` | internal token | 受控变更留痕查询（actor/reason/action/payload_hash/created_at） |

商家会话**永不**写准入真值（商户租户不是平台权威）。

## 4. route JSON 形状（公开投影）

```json
{
  "action_id": "A01", "catalog_code": "A01", "action_kind": "publish",
  "label": "发抖音视频", "suggested_only": false, "environment": "ios_safari",
  "routes": [{
    "platform": "douyin", "method": "h5_share", "environment": "ios_safari",
    "scenario": "store_promo", "app_binding": "none", "status": "not_admitted",
    "allowed": false, "promised": false, "pending_external": false,
    "reason": "not_admitted", "reason_text": "抖音发布能力使用规范明确排除商铺宣传…（human 说明）",
    "navigation": "supported", "media_handoff": "supported",
    "platform_receipt": "unknown", "reward_eligibility": "unsupported",
    "field_support": {"media": "supported", "title": "unsupported", "…": "…"},
    "required_inputs": ["content_pack_version"],
    "missing": ["官方对商铺宣传场景的明确准入（现行规范明确排除）", "…"],
    "official_sources": ["https://developer.open-douyin.com/…"],
    "evidence_grade": "official_spec", "checked_at": "2026-11-09", "valid_until": "2027-02-07",
    "emergency_disabled": false, "close_reason": ""
  }]
}
```

`required_inputs` 语义：`content_pack_version` 表示媒体路线必须使用真实采用版本；`[]`（A27 link_share/点评/关注/服务/团购）表示纯链接/目标打开，**不得被「视频库为空」拦截**。

## 5. 种子初值摘要（权威初值，平台端更新走受控端点）

- 抖音店宣通用投稿（A01 h5_share store_promo）= `not_admitted`，拒绝原因明确；**显式手工绕过拒绝行**（A01 manual_material 亦 not_admitted——手工交接不构成被禁玩法的绕过通道）；技术能力（navigation/media=supported）与不准入同时表达。
- 抖音专项任务（A01 miniapp reward_task）= `unknown`（任务能力需申请批复）→ PENDING_EXTERNAL。
- 小红书 JS（A02/A07 js_media_handoff）= `unknown`（2026-11-09 快照暂停新接入，待主体控制台核验）；媒体可交接（media=supported）但 SDK 自动填标题/正文/话题=unsupported；同动作 manual_material=admitted（允许场景手动保存/自主编辑）。
- 快手（A03）：oauth_server_push/native_sdk=`unknown`（异步接受≠最终公开，需作品查询；scopes 未批）；manual_material=admitted。
- 微信（A04/A05/A06）：manual_material=admitted（保存→顾客自行发布，不声称直接发出）；普通 H5 原生发帖=h5_share `unknown`+`unverified`（契约未核实，不编造 scheme）；link_share=admitted（与原生发布分开判定）。
- 点评类（A08–A12）= direct_open `admitted`：navigation only（media=unsupported）+ reward=unsupported；无模板/AIGC、不索截图、不以评价作奖励触发。
- 关注类（A13–A16/A23/A26/A29/A30）= direct_open `admitted`；无可靠回执的平台 receipt=unsupported（点击不算关注），其余 unknown；reward=unknown（不能自动发关注奖励）。
- Wi-Fi（A17）：H5 面板 `admitted`（展示+复制密码；实际连接与展示分开）；小程序连接 `unknown`（wx.connectWifi 需接入）。添加微信（A18）：`admitted`（扫码≠添加；企微验签未核实）。
- 团购（A19–A21）：direct_open `admitted`（打开官方商品；点击不算成交）；reward=unsupported（平台订单权威，本地不假核销）；A21 miniapp 核销=`unknown`（需获准接口）。
- A22–A30 = 建议补充（`suggested_only=true`），不得悄悄升级为获批自动发布承诺。

## 6. 消费方接线指引

- **HUI-1670（交接）**：交接前必须过本面（服务端单点，前端隐藏按钮不算数）。注意两点遗留接线（归 1670 域，本票只交合同）：① `publishhandoff.PlanRoute` 现传 `GateInput{Now,TenantID}`，缺 `RewardPlay/MerchantAppRef`——奖励性质与真实 App 绑定必须服务端派生后传入，不能让客户端以 reward=false 绕过；② 旧映射 `admission_unknown→manual_only（带手工指引）`与本合同冲突，必须换成 `pending_external`（不承诺、不显示可完成态、无手工指引）。
- **HUI-1676（商家配置）**：`GET /api/v1/channel-actions`（会话，scope=tenant）渲染可选渠道与缺项原因；点评动作 reward=unsupported → 禁止配置点评奖励。
- **HUI-4584/4589/4590（动作配置/点评关注/团购）**：营销场景裁决读本面四分能力，不自建白名单。
- **HUI-4592（O4 渠道健康）**：读 `GET /api/v1/channel-actions`（internal token）+ `…/audit`；紧急停用/恢复走 `…/emergency`；不能关闭无关导航（emergency 只停对应方法/动作）。
- **fixture 纪律**：迁移种子即生产初值；测试 fixture 与生产记录隔离，真实来源不足保持缺项，不以测试回执升级资格。

## 7. 已知边界（诚实挂账）

- 真实平台资格（抖音开放平台审批、小红书主体控制台、快手网站应用、微信正文可读性）当前无账号/未接入 = 未验证；种子以 `unknown` + evidence_grade + missing 如实标注，工程面完整，不伪造已获批。
- 六环境种子值暂同值（无真机差分证据不给环境差分承诺）；T07 真机走查后经受控端点按环境更新。
