# 末轮正常入口修复记录

## A27 分享与共享会话来源

`bf0d57b` 将服务页误放入社群分支的 A27 接到原 `LinkShareFlow` → `ActionPublishFlow`。
原服务端会话、已发布目标、方法/场景准入及原 handoff 保持权威；无需媒体、内容包或奖励参与。
展示真实链接，由用户明确准备分享；系统支持时主动分享，不支持时主动复制。
复制、取消、无目标和系统接收不升级成已发送、已发布、加群或奖励。

Terra 原门店复验发现 C2 和共享默认同样错误发送 `source=web`，服务端合法枚举不含该值，
导致新建与恢复在执行之前都 422。`a69a3a0` 修为普通站内入口的 `manual`，类型限制合法四枚举，
保留 NFC/QR/shorturl 显式调用；invalid_state 不再冒充网络失败。服务端校验未放宽。

- 新默认来源合同先红后绿：`logs/root-session-source-red.log`、`root-session-source-green.log`。
- 5 文件65项与 TypeScript 通过；A27 初次接线另有4文件31项。
- Terra 原 A27、原首页第2版创建真实会话与原 handoff，复制剪贴板与已发布URL一致，刷新恢复同记录。
- clipboard拒绝与 AbortError 是单列浏览器运行时模拟；无真实手机或平台送达证据。
- 原另一个门店首页第5版的 C2 真实创建会话201并恢复同会话、原v4内容；首次新会话错误声称恢复，后续改为中性“本次内容与进度已保存”。

## C2 主动导出边界

Terra 在原 A01、当前两条发布路线均 not_admitted 时，从正常保存按钮成功下载 tiny.mp4。
这是实际文件下载，不能以测试绿或隐藏按钮结束。

前端 `3c16361` 移除预览旁未过交接的主动复制/保存，保留真实媒体预览；取材操作统一由原
已准入 handoff 提供。拒绝或刷新失败时停止使用旧的可执行取材状态；取消后不继续呈现保存按钮。
视频预览 `nodownload` 仅为产品控件呈现，不宣称防止用户获取预览字节。删除旧的源码字符串
“预览组件必须含download”断言，实际能力以服务端与正常浏览器行为检验。

4文件50项与TypeScript通过；后端媒体签发/兑换的当前方法、场景和签名来源校验由独立
media-export-repair组补齐。该后端尚未合入时，本条仍是待完成修复，不得标通过。

## 无活动交接统计

Terra 固定事实复现：门店根 v2 handoff 的 campaign_id 为 NULL，统计却内连接 campaigns，
导致交接与事件均为0。原实现还会按后来活动归属改变历史交接的门店。

root 改为原 publish_handoffs.store_id 冻结归属，活动筛选仅在明确选择时约束原 campaign_id。
事件关联同时核 handoff 与 tenant，不能以其他租户的事件填入当前事实。原无活动链接分享
没有内容版本，不把它归入某个媒体版本；有原生冻结版本的媒体交接照常筛选。

新的固定事实测试覆盖两个门店、无活动链接/媒体交接、旧活动交接、活动过滤、版本过滤、
门店scope优先、跨租户事件与活动移动；前置fixture第一次因空FK失败已更正，最终red日志
记录的是原查询实际漏计0/0，不以fixture失败冒充缺陷证据。

`root-store-handoff-stats-red.log` 与 `root-store-handoff-stats-final.log` 为红/绿记录。
store相关回归和dashboard全包通过，待Terra回原A27记录及固定事实复验。

## 后续闭环与准确证据范围

上述按时间记录的待验事项，后续已完成以下增量，不能仅停留在前面的旧状态：

- 后端媒体门禁 `1bc73b2` 已合：签发与兑换都验证原准入、签名来源和当前内容授权；Terra 原 A01 不改准入，预览 readyState=4，download/platform_fetch 拒绝。旧缓存票据与取消等工程回归单列。
- `79367fd` 将合法 manual 已签媒体按原顺序投影到前端真正消费的 client_action；`7393e19` 修复 A05 把媒体用途误选 link_share、plan/start 裁决不同的问题。Terra 保原 A05 会话、微信 v5 和最终同 handoff，正常下载 01-image.png，SHA256 与原 pixel.png 一致。本地自签 TLS 仅用于工程生产形状，不是正式平台或真机证据。
- `6055395` 在当前准入 refused 时仍对原 unknown/submitting/awaiting_review 保留只读查询，终态先认原结果；不重投原未知投稿。该项有真实逻辑测试与代码复核，无真实官方未知投稿证据。
- `5787965` 的原 A27 统计已由 Terra 从正常工作台复验；`0cdcd6a` 又把“媒体交接完成”纠正为准备态，原记录复制后交接发起/准备/操作=1/1/0，未把复制算作送达。
- `a358435` 将活动权益入口接到复用组件的 `/c/[code]/benefits` 并返回同一活动。`0ef5509` 收起已配置新版承诺活动的旧 CustomerPublish 发稿步骤，旧服务器端点同样409。Terra 原活动正常页面、本人空态、返回及旧端点复验，未造参与或权益正例。

## 草稿中途对原合同的保护

`1150423`（源提交 d3d7f5）修复旧参与被后来首次正式发布的同数字版本误闸；root 和 Terra 都进一步复现了“仅保存首份草稿、还未启用”的同类问题。root HTTP red 日志为 `root-legacy-draft-red.log`；原 p 实际变为 pending/required=true。另 `root-legacy-held-draft-red.log` 证明原已 reserved 在首份草稿后 reward-confirm 422。

`5a42106` 仅在 p 早于配置创建、完整 create/update 草稿修订链和当前 draft pin 相符、没有正式发布痕迹时识别原 legacy 合同。缺失 create、坏 payload 或残留 enable 都不推断为旧合同。原 p 的 GET、恢复、重放和原 attempt 继续，既有原 hold 确认同一 benefit；未持有者不能借草稿获得新库存，正式暂停仍阻止新发放。

原 store 暂停竞态测试使用了不存在的 participation ID，真实合同检查后不再代表合法输入；fixture 改为通过原 participation.Start 建立正式 v3 的实际参与，保留原暂停前后顺序、重放、预留、已发券核销全部断言。不是删除门禁或改变预期结果。过程中一次重构漏接正式生命周期被现有 HTTP 暂停测试捕获，已恢复门禁，最终定向 HTTP/store/participation 全绿（9.069s/2.311s/0.004s）。最终同候选全量另见总报告。

`ae53f2f` 合入到期余量修复（源 2c0dfa），只读时间投影与原写事务使用同一解析判据；详细 red/green、截止边界和库存守恒在 reward-reserver-repair.md。

Terra 在 ae53f2f 上独立重跑原审查用例：旧 p 在首份草稿和首次启用前后都保原合同，原已持有权益同 ID，旧 handed_off 的未知/到期闸继续生效；自然到期读面不写库、新参与仅一胜者、旧参与恢复失败和 issued/redeemed 占用均通过。原浏览器库没有 p/券，工程夹具不能替代该正式正向链。


## M11 浏览器正常路径的最后修复（2026-11-14）

原两库保持。Terra 从原门店 C6 用空称呼、仅本次服务许可创建真实新咨询 `sub_6127191218bedd77553449d0`，经原 forwarder 与工程 Notify relay 得 stored ACK。首先出现 Touch Go mint/redeem 成功但 BFF 502：vendor 生产登记快照落后，Leads 目标实际已在上游登记。root 只整体同步 public-ai 不可变提交 a0c5b24c0e1e4024e58c33b918531b9a750e6a86 的原文件，blob 4c76f4178498ef0f461485a7c87c362e0a43883a 对拍一致；Touch/cashier 仍缺失。提交 2f9c668，18项回归通过。

同一原行再暴露 middleware 用 nextUrl.clone 把反向代理 localhost 带入 SSO Location。两仓改用经Host/forwarded-host核验的canonical HTTPS host，固定landing路径，原code+h不变。Touch187a40b/Leads a715def；Touch6项、Leads41项相关回归通过。Terra 沿同一咨询真实到 lead_2dc908169b6dcad6cf128d56b17f3841 详情，timeline200；工程Touch目标禁用503后从同一详情恢复工程登记并点击同按钮，回到原门店sto_9f6fd1a6618e423416579859509843bc。工程传输从Playwright路由改为CONNECT+TLS仅传输真实Next，不改响应、不注cookie。正式Touch target仍未登记。

另一Terra发现Leads SSO reset未消费。root3885d03清旧tenant/cache与在途结果；旧whoami空tenant400又暴露普通首次SSO死路，独立Sol7129cac/root4ab3290复用原Identity链补当前成员范围发现。root接线唯一范围经whoami确认，多范围按名称显式选择；不能取第一项、不能输入内部tenant。Leads4ab完整Go/Web71文件530项/tsc/build通过。Touch187a完整Go/Web125文件1210项/tsc/build通过。此前全量失败的schema清单和正式参与测试捏造ID也已修：登记真实0138表，成本冻结测试通过真实participation.Start建合同，保留未知成本/显式零成本断言，未放宽业务闸。

同标签修复后，Terra又识别跨标签页旧数据残留。root Touch68be5cb/Leads44defd4写独立非秘密账户代次，storage/focus通知旧页清cache/卸载业务子树后重新核验；continuation、原线索跟进、原详情返回和生态导航拒绝旧epoch结果。静态Terra原缺陷复审无新具体问题；相关Touch10项/Leads15项通过。最终浏览器与全量结果以验收主文和M11独立报告为准，不把静态绿冒作实机或正式身份。


### 原/admin账号切换最后一轮

Terra在68be原/admin正常退出/登录复现旧工作台未重核。2e74e6b仅接merchant-session的正常auth广播，原/admin独立createBffSignIn/退出仍红，未将该提交记为原红已修。9be7071接原/admin成功登录/退出同一代次，并在原路由按账号变化重建完整AdminWorkspace清旧表单与列表；退出网络/非成功不冒退出成功。Terra从同/admin退出工程B登录A，原旧/work/leads与旧/admin均自动whoami200，服务端principal为A；A/B同有原租户权限，只证明当前身份重核，不推为无权账号正例。

最终Touch9be7071与Leads44defd4又沿同新咨询到原Leads timeline及原门店通过。原A01同session/v4预览readyState4且不准入无导出旁路；原A05同session/handoff/v5下载原01-image.png成功且SHA256匹配、刷新恢复；原A27同session/handoff复制原URL、刷新恢复。未用另一平台成功替代原拒绝，未制造发布/送达事实。全部审查进程已退出，原库和媒体保留。

最终门：Touch68be全Go258.694秒通过；9be后端server tree与68be完全相同87ee728f1d963ef6475d0a93487609be3280051e，随后仅web变化，最终9be全前端125文件1212项、tsc、production build通过且候选不变。Leads44def全Go57.408秒、Web71文件531项、tsc/build通过且候选不变。独立Terra浏览器与静态对抗报告均归档；正式目标/Identity/Notify、真机、部分多范围/撤权竞态仍缺证。
