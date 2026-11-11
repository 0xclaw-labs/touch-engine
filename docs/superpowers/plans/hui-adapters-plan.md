# 计划｜HUI-4585/4586/4587/4588 平台适配器四票（一批一智能体）

分支 touch/hui-adapters @ bafb03a。迁移 0119-0123。报告 reports/hui-adapters-report.md。

## 契约基线（1670 冻结，adapter 按此实现）
- Adapter 四方法：Prepare/Submit/Query/VerifyCallback（adapter2.go）；不适用=ErrUnsupported。
- plan.kind 五值；client_action 五个登记类型（open_url/open_miniapp/js_media_handoff/oauth_consent/manual_export）。
- evidence_level 镜像 3535 platform_receipt；能力值更新只走 3535 受控端点，adapter 不自建白名单。
- 回调端点（更正后）：POST /api/v1/public/handoff-callbacks/{platform}；快手 OAuth 回跳为
  GET /api/v1/public/handoff-callbacks/kuaishou/oauth（浏览器 302 回跳，与 POST 验签回调同前缀不同段）。
- client_action 类型映射（4588 票面词→冻结词）：show_manual_steps→manual_export；
  open_system_share→manual_export+Params 共享提示；open_url→open_url。冻结词表不扩（写面纪律）。

## 写面（唯一）
server/internal/publishhandoff/adapters/（新包）：douyin.go / xiaohongshu.go / kuaishou.go /
manual.go / platform_steps.go + 各自 _test.go。
web/src/lib/publish-adapters/：client-action.ts / douyin.ts / xiaohongshu.ts / kuaishou.ts /
manual-material.ts + web/tests/ 下同名测试。
窄补丁（共享文件，只加行）：config.go（凭据 env 项）、httpapi/server.go（New() 注册 + OAuth 回跳路由
+ 回跳 handler 文件 server_kuaishou_oauth.go 归 4587）。
迁移：0119 douyin_share_ids；0120 douyin_provider_refs；0121 xiaohongshu_handoff_refs；
0122 kuaishou_oauth_states+kuaishou_oauth_tokens；0123 kuaishou_publish_jobs；4588 无。

## 4585 抖音
- 闸面：A01 h5_share/manual store_promo=not_admitted → refused（无手工后门）；A01 miniapp
  reward_task=unknown → pending_external。adapter 只消费裁决，不绕。
- Prepare（h5_share 获准时）：铸 share_id（0119，绑定 tenant/handoff/session/内容指纹/app_ref，
  带有效期；同 handoff 幂等复用）→ client_action=js_media_handoff（sdk=douyin_h5，只带
  client_key/share_id/受控媒体 URL 等官方参数）；签名器为可插拔 DouyinSigner，无默认实现——
  未配置=Prepare 诚实报错列缺项（不自造"抖音HMAC协议"）。
- Prepare（miniapp 专项获准时）：open_miniapp + 任务参数；success 语义注明必须读平台 result。
- Submit/VerifyCallback：ErrUnsupported（无服务端投稿通道/无已核实 webhook 契约）。
- Query：按 share_id 走可配置官方查询端点（测试=fixture server）；显式已发布才铸 Receipt
  （Outcome=published，五绑定+HandoffID）；其余照录 unknown；无凭据=unknown。
- 0120 provider_refs 持久化 share_id→item_id 映射。

## 4586 小红书
- 闸面：A02/A07 js_media_handoff=unknown → pending_external（官网快照暂停接入=诚实态）；
  manual_material=admitted → manual_only。
- Prepare（js 路线获准时）：client_action=js_media_handoff（sdk=xhs_js；type=video|normal；
  images 有序引用或 video+cover；verify_config 只带公开参数）；**载荷白名单不含
  title/content/topics**；参考文案走 ClientAction.Caption（独立 copy_view，用户主动复制）。
- Submit/Query/VerifyCallback：ErrUnsupported（JS fail≠发布回执；无官方成功查询/回执契约，
  不接受本地伪 success；小程序回调不冒充 JS 回执）。
- 0121 handoff_refs：服务端签发的 JS 载荷记录（载荷哈希/媒体引用/签发时间），重试回同一载荷。

## 4587 快手
- 闸面：A03 oauth_server_push=unknown → pending_external；manual_material=admitted。
- Prepare（oauth 路线获准时）：client_action=oauth_consent（URL=服务端拼的 authorize，
  client_id/redirect_uri/scope（适用范围）/state 一次性）；state 落 0122（绑
  handoff/session/app/return_to，TTL，一次性）。
- OAuth 回跳 GET /handoff-callbacks/kuaishou/oauth：state 校验（未用/未过期/绑定匹配）→
  code→token 服务端交换（token 只进服务器；0122 tokens 表）→ 302 回 return_to（同源 https 校验）。
  code/state 重放=拒绝并诚实回跳，不二次发 token。
- Submit：读 B05 platform_fetch 引用→上传/创建（可配置官方端点；测试=fixture）；两阶段
  （upload→create）持久化在 0123 jobs；photo_id 已存在=直接复用（先回查不重投）；stage=creating
  且无 photo_id=拒绝重复创建（in-flight unknown）；异步接受≠公开：只落 submitting/provider_ref。
- Query：photo_id 查官方作品接口；显式 published 才铸 Receipt（同管线确认）；审核中/处理中/
  失败/无权限照录状态词，不造成功。
- VerifyCallback：ErrUnsupported（无已核实 webhook 契约，不搭自定义回调充官方）。
- 凭据：PUBLISH_HANDOFF_KUAISHOU_CLIENT_ID/_CLIENT_SECRET（服务端保管，客户端永无 secret）。

## 4588 手动家族（A04/A05/A06 必需 + A22/A24/A25/A27/A28 建议补充）
- ManualAdapter 按平台注册：wechat/wechat_channels/bilibili/weibo/qq/qzone（platform_steps.go
  逐平台步骤/标签/差异说明；建议平台保留"建议补充"标注）。
- Prepare（manual_material）：manual_export——ordered_media（图片顺序保序）、copy_view（Caption）、
  平台步骤 Steps、按六环境给操作差异说明（如 ios_wechat 需右上角菜单切系统浏览器——能力真值
  仍在 3535，步骤只说怎么做）；Params 带共享提示（web_share 能力探测由前端按实机降级）。
- Prepare（link_share A27/A28）：open_url（服务端已验证 share_url）+Params 复制回退+步骤；
  无内容包/无 media_access。
- Submit/Query/VerifyCallback：ErrUnsupported；系统分享成功=handoff_only（前端只落 media_handoff/
  wake_attempt 弱事实），"已完成"只存 customer_self_report，永不 platform_confirmed。
- 无迁移。分享按钮不记关注/加群/发消息（A23/A26/A29/A30 归 4584/服务票）。

## 前端（web/src/lib/publish-adapters/）
- client-action.ts：ClientAction/params 形状校验、六环境检测（target_app 检测不到=诚实）。
- douyin.ts / xiaohongshu.ts / kuaishou.ts / manual-material.ts：把 server client_action 渲染为
  类型化操作模型；小红书载荷断言无文字字段；系统分享 navigator.canShare 探测降级；复制必须
  用户点击；不执行任何可执行 JS；XHS SDK 真身未接入=诚实 unavailable。
- vitest：每模块正反例（形状拒绝/文字字段注入拒绝/分享降级/复制用户主动）。

## 验收对齐
- T07：每平台 adapter 按六环境给真实三态说明（能力值真源在 3535；adapter 只表达步骤差异）；
  真机证据缺失=报告诚实标注未验证+阻断原因。
- T08：异常恢复——快手 state 重放/过期/换绑、upload 崩溃窗口、douyin share_id 过期、
  xhs SDK fail、手动家族返回/取消保会话与版本。
- fixture/测试密钥只进 _test.go；生产无凭据=adapter 不注册（1670 诚实回退面接管）。

## 顺序
1. 迁移 0119-0123 → 2. adapters 包四文件+测试（红→绿）→ 3. config+注册窄补丁+OAuth 回跳 →
4. web 模块+vitest → 5. 全量 go test ./... + vitest run → 6. 自审 diff → 7. 报告。
