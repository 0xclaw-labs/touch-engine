# 渠道准入应用申请材料清单（HUI-3535 交付物）

- 版本：v1（对应 seed.json version=1，核查日期 2026-10-14）
- 纪律：本清单**只列缺什么**。文中没有任何一条代表「已申请 / 已获准」；
  一切平台批复以平台官方答复为准，批复落地后由有权人员更新
  `docs/contracts/channel-admission/v1/seed.json` 并重审（修订留痕见
  channel_admission_revisions 表）。
- 应用注册由有权人员完成；全程不索要、不存储顾客密码或 cookie。

## 1. 抖音

### 1a. 普通 H5 发布（store_promo）— 当前 not_admitted
| 缺项 | 说明 |
|---|---|
| 官方对营销内容场景的准入 | [发布能力使用规范](https://developer.open-douyin.com/docs/resource/zh-CN/dop/operation-standard/platform-capabilities/useclue)明确排除商铺宣传、商品介绍等营销内容。除非平台规则修订或另有书面准入，此路线不可用；不存在可提交的「申请材料」，只有规则本身。 |

### 1b. 抖音小程序 拍抖音/任务（reward_task）— 当前 unknown
| 缺项 | 说明 |
|---|---|
| 小程序主体与开发者账号 | 需以有权主体注册抖音小程序（未注册）。 |
| 任务能力申请 | [拍抖音/任务能力](https://developer.open-douyin.com/docs/resource/zh-CN/mini-app/open-capacity/operation/douyin-video/douyin-task)与基础发布是不同能力，任务查询/状态推送需单独申请（未申请）。 |
| 拟用玩法的平台答复 | 需向平台提交「顾客转发门店预制内容换券」具体玩法并取得书面答复（未提交）；有生活服务体验视频奖励案例，但不构成本玩法获准。 |
| 拟用玩法描述材料 | 玩法流程、奖励口径、内容来源声明（门店预制素材授权链）。 |

## 2. 小红书

### 小红书小程序 + 发小红书组件（store_promo）— 当前 unknown
组件与参数[存在](https://miniapp.xiaohongshu.com/doc/DC530301)（媒体/标题/正文/话题/关联参数/回调入口列明），以下资格项未确认：
| 缺项 | 说明 |
|---|---|
| 小程序主体注册 | [小红书小程序](https://miniapp.xiaohongshu.com/doc/DC137160)开发者账号与主体资质（未注册）。 |
| 外部拉起权限 | 从 Touch 公共页拉起小程序的能力/域名配置是否对非宿主环境开放（未确认）。 |
| 发布回调权限 | 组件回调入口对小程序的实际可用性（未确认；不默认有，也不默认没有）。 |
| 激励/奖励规则 | 平台对激励发布的规则边界（未确认；不默认任意激励方式允许）。 |

## 3. B站

### H5 带入手机发布页（store_promo）— 当前 unknown
[开放平台](https://open.bilibili.com/)存在认证/应用/授权接入，但「外部 H5 把稿件带入手机发布页」的官方路线待核实：
| 缺项 | 说明 |
|---|---|
| 移动端发布路线核实 | 需官方渠道确认是否存在合规的带入路线；在此之前不编造 deep link、不假设已有权限（未核实）。 |
| 应用接入与授权范围 | 若存在路线，所需应用类型、scopes 与审核材料（未申请）。 |

## 4. 快手

### 移动应用（native）单视频发布（store_promo）— 当前 unknown
[Android SDK](https://open.kuaishou.com/platformDocs/develop/mobile-app/android.html)能打开单视频发布页，属移动应用路线：
| 缺项 | 说明 |
|---|---|
| 开放平台开发者账号与应用创建 | （未注册/未创建）。 |
| 移动应用接入审核 | Android SDK 接入的 app 签名/包名审核材料（未申请）。 |
| 话题携带边界确认 | FAQ 对话题携带另有限制，需逐字段确认可带入范围（未确认）。 |
| 真实设备验证 | 官方最小样例在真机上的素材带入/取消/未安装/版本过低结果（未验证）。 |
| iOS 路线 | 是否存在对等能力（未核实）。 |

## 5. 全渠道通用缺项（票面要求）

- **官方最小样例 + 真实设备验证**：每条候选路线须实测素材带入/文案带入/用户确认/回执/取消/未安装/版本过低，只打开 App 不算结果（全部未验证）。
- **发布前合规清单**：所选平台的广告/激励披露、素材授权、AI 内容标识要求，按届时官方依据落到发布前清单（未落）。
- **授权应用绑定**：区分自有平台应用与商家授权应用的绑定/额度/回调来源；应用注册由有权人员完成（机制已建：seed 的 app_binding/tenant_scope + Gate wrong_binding，实例为空）。
- **回执渠道**：一切「已发布」结论只认 official_query 回执；当前无任何渠道具备可用回执（capabilities.receipt 全 unknown）。

## 6. 更新流程（谁改、怎么改）

1. 取得平台官方批复/确认后，由有权人员在 seed.json 增量更新对应记录（status、reward_admitted、capabilities、missing、official_sources、last_checked_at、valid_until）。
2. 同步更新本清单对应小节（移除已补齐缺项）。
3. 跑 `cd server && GOWORK=off go test ./internal/channeladmission/ ./internal/store/`——种子锁定测试会核对结构不变量；新增字段或记录需同步更新测试真值。
4. 提交 PR；启动装载会自动写入 revision 留痕（payload 变化才追加）。
