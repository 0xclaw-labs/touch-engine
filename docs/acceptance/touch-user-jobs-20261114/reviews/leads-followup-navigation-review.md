# M11 逐条线索受控跳转独立复验（准备中）

## 规格与原始事实

M11 需要从 Touch 工作台原授权线索进入 Leads 真实详情并受控返回原门店；两应用租户映射及完整 source app/namespace/源租户/ref 必须准确，submission_ref 或 ACK receiver_id 不是 Leads lead ID。原 SSO code/h 只用于交接，仍须独立验证主体、目标CRM成员与记录权限、源撤回水位、有效期及单次消费；未接收、撤回或来源歧义不跳详情。不能将工程 Identity 登记或工程 Notify relay 当正式 SSO/投递验收。

保留原 Touch `/tmp/touch-e2e-ZLFKmv/touch.db`、media 及 Leads `/tmp/leads-browser-review-20261114/leads.db`。只读核对旧 `sub_555c19fad47a379566202232` 与经真实工程接收的 `sub_8b61d688bc2a6cff45614417` 均已撤回；前者从未转发、不能伪造 ACK。另一个工程预置活跃 ref 未证明接收，不能当正例。待候选提交后原两库上从顾客 UI 新建咨询、真实 forwarder+工程 relay→Leads 持久化→原 ACK，才能作为新活跃正例；撤回旧记录保留作反例。工程身份和租户映射的必要初始注册须明确标注，不用改业务库伪造完成。

截至准备时，尚未读取最终候选、启动服务或执行本轮导航；等待 root 合入两仓 SHA 与部署 runner。未 push、未写 Linear/Done。

## 首轮最终候选浏览器与阻断

独立 Touch `9049489cf5cea94485bafaed041c2535b457e229`、Leads `6d948d348c2352c707688985b055d937ab461fd6`，两独立 Next production build成功；原两库及media保留。用原 Leads `provision-member` 在原CRM工程租户为已有 Touch owner `usr_e2e_owner` 建工程成员，未造业务线索/ACK。真实 Touch/Leads Go 与 Next、工程 Identity 和明确工程 Notify relay同namespace，canonical HTTPS host通过本地传输代理，未注入浏览器cookie或伪造业务响应；工程身份注册不代表正式平台登记。

正常 `/`→管理与更多→线索：旧 `sub_555c…2232` 与 `sub_8b61…4417` 均显示许可撤回不能跟进；旧工程预置未发出记录如实显示接收方未确认。原 `/s/sf_eeeffe9978ae3cf2`→隐私→请门店联系我，空称呼、仅本次服务同意、不勾营销，正常 UI 创建 `sub_6127191218bedd77553449d0`，初始待同步；原 Touch forwarder经工程relay发送，Leads首次503，原事件重投200，Touch真实 stored ACK 201。原工作台同一新记录显示接收方已确认入库，可进入获客详情（`runtime/m11-original-row-904.png`）。

点击该原行：Touch `/api/v1/work/leads/{ref}/follow-up` 200，Leads 原 `/internal/v1/touch-follow-ups/resolve` 200、register 201，Touch Go 原 `/api/v1/eco-nav/switch/redeem` 200；Touch Next `/api/eco-nav/switch/redeem` 返回502 `launch_target_mismatch`，浏览器留在列表，未进入Leads。只读定位：Touch 原 handler 固定 app_id `leads-engine` 与 target `ti-leads-engine-web`；工程 Identity 明确登记 `https://leads.modelxing.com/` 并按原 shell 返回同app/target与一次性 launch URL；Touch vendored production-manifest 目标集中无该目标，Next 的 `validateLaunchUrl` 因登记项缺失拒绝。未改manifest、未借其它App、未通过手拼地址或注入cookie绕过，已报 root。一次性 code/h/token 不写报告。

两仓工程定向 HTTP：Touch `TestWorkLeadFollowUp|TestFollowUpCanonical`、Leads `TestTouchFollowUp` 通过。到当前为止这是部分真实工程链，未证明正常 Leads 详情 timeline、回店、受限/重复h等浏览器全链或正式平台注册；修复后须从原新ref和原行重新发起并验收。

## 原记录增量复验：Leads landing 地址阻断

Touch 独立树快进并重建至 `2f9c668d85e0879c8dd41f24f5796cd4aff8e427`，Leads 仍为 `6d948d348c2352c707688985b055d937ab461fd6`。沿上述同一新咨询及真实 ACK，从工作台原行重新点击：原 follow-up、mint/redeem 成功，浏览器到达已登记的 `https://leads.modelxing.com/`；Leads Next middleware 随后返回 307，将 SSO landing 的目标重写为 `https://localhost:19963/api/auth/sso/landing`，导致浏览器 SSL 协议错误，未进入详情。经本地传输代理保留 canonical Host 与 forwarded host/proto，未改响应、注入 cookie 或通过其他 target 绕过。观察到的落点是内部 Next 端口，不是用户可访问的 Leads canonical host；原 code 和 h 均不记录。已将响应阶段、host 与代码中 `req.nextUrl.clone()` 线索交 root，root 正修两端 canonical landing。此轮仅证明首个 manifest 阻断已消除，不能声明详情、回店或换账号通过。

## Canonical 修复后原咨询完整主链

Touch `187a40b9642f35e2cc7b68fc64082157ac34afad`、Leads `a715def919c96890856bb4962367c774bfca4aea` 独立 production build 成功；同 `sub_6127191218bedd77553449d0`、同真实接收 ACK，从原 Touch 工作台按钮继续。Leads 307 的 Location 已指回 `leads.modelxing.com` 的固定 landing，原内部 localhost 地址问题消除。工程 Playwright 的 route 拦截没有覆盖浏览器跟随的重定向，改用仅将两个 canonical HTTPS 域的 CONNECT 传送至本地真实 Next 的工程 TLS 代理；保留原 Host、Origin、状态、响应、cookie 及 code/h，不重写业务 Location、不注入会话。此项是测试传输调整，与产品修复分别记录。

浏览器真实经历 Leads landing 200、exchange 303、follow-up resume 200，实际 `lead_2dc908169b6dcad6cf128d56b17f3841` 的 timeline 200，详情显示“CRM已接收”“没有营销许可”（截图 `runtime/m11-detail-a715.png`）。工程 Identity 中 Touch 目标先禁用，同页“返回原门店”真实返回 503，页面如实显示入口未登记或不可用、详情仍可查看（`runtime/m11-return-blocked-a715.png`）。恢复工程 Identity 目标登记后从同一详情重试，return 200、Touch landing 200、exchange 303，浏览器最终进入原门店 `sto_9f6fd1a6618e423416579859509843bc`（`runtime/m11-return-original-store-a715.png`）。此为原两库、真实应用和业务 ACK 的工程身份/relay 联调；正式 Touch 平台目标仍未登记，此证据不等同正式 SSO 投产。换账号、未知或重放 h、撤权等仍待后续候选逐项验，不将工程主链成功外推为全部通过。

## Leads 4ab3290 范围和重复句柄增量

Touch 仍为 `187a40b9642f35e2cc7b68fc64082157ac34afad`，Leads 独立树快进并 production build `4ab32903b99208440d17c8ea23d8d515ed907b14`。同一原咨询重新从 Touch 正常工作台进入原 Leads 详情，resume 与原 lead timeline 均 200。在该已登录上下文访问不存在的 h，浏览器安全回 Leads 首页，memberships 200、whoami 200，显示当前真实工程 CRM 租户（`runtime/m11-unknown-h-4ab.png`），未进入任意详情，未注入租户或改浏览器存储。对本次真实已消费 h 的再次访问，resume 返回 410，界面提示回原线索列表重新进入，未重放至详情。这里证明的是跟进 SSO 后未知 h 与同句柄重复；首次普通 SSO、多租户选择和跨标签换账号尚未证实。

## 跨标签账号切换：68be / 44def 增量

Touch `68be5cbe9210f50767f98ac407010aafd0837290`、Leads `44defd4d1526b88f5f75e2810117f969350f3f49` 独立构建成功，同原咨询从 Touch 行进入原 lead，timeline 200。为真实换账号，采用两应用原 provision-member 在既有原 Touch 与 CRM 工程租户为 `usr_m11_review_b` 增加 owner 成员，并在工程 Identity 注册 B；未新增业务记录或伪造 ACK。B 与 A 均有原 CRM 租户权限，因此 B 重核后能看到同一线索本身合法。

正常商家 UI 退出 A、登录 B，B 经原线索行真实 SSO 进入 Leads 后，原 A Leads 标签自动重新发 `/api/whoami`，返回真实 `usr_m11_review_b` 与原 CRM 租户，原 timeline 重新 200；未注入 cookie 或浏览器存储。重核请求和当前 B 主体已观察，旧内容卸载的瞬间未捕获，不声称直接观察到该瞬间。

另测旧 Touch 标签在 B 身份下打开线索页，同浏览器另一标签通过 `/admin` 正常退出 B、登录 A；两秒内旧 Touch 页未发 whoami 重核，仍保留原线索界面（`runtime/m11-touch-oldtab-68be.png`）。A/B 都有该租户权限，因此这一浏览器证据证明普通登录/退出未触发旧页身份重核，不单凭显示相同线索断言越权。此缺陷已报告 root 修复，须沿旧标签原路径复验。正式平台登记仍未证。

Touch `2e74e6b6ef54a5f1ccbbdcad47905a6f999dbed4` 独立 build 后沿同一红路径重验：B 正常登录 `/admin`，第二标签打开原 `/work/leads`，第一标签正常退出并登录 A；旧标签两秒内仍无 `/api/whoami`，仍保留旧线索页面。`/admin` 使用独立登录/退出实现，候选仅修改 merchant-session，未覆盖该入口。两账号均有权，证据是缺少身份重核而非断言同租户越权，已报 root。待修复后原标签路径重验。

同 Touch `2e74e6b` 增量在原 store 数据库/媒体与原访客凭据下，A01 原 session `as_86ea…aa82` 与 v4 仍可预览（video readyState 4、无错误），显示当前路线未放行，未出现复制文案或保存视频旁路（`runtime/m11-a01-2e.png`）。A05 原 session `as_5edb…2352`、原 handoff `pha_2cae…2cbd`、原 wechat v5 恢复；本地工程 TLS 下正常“保存图片 1”产生 01-image.png 下载，SHA-256 `e428b08a81989c5ff39ff637d4da4e9f5b7b38fd6b3a8bc4eb72ba4990c46b63` 与原文件一致，界面仅说已请求保存（`runtime/m11-a05-2e.png`）。此前 Touch `68be5cb` 同原 customer 数据库、访客凭据及原 A27 session `as_5c6a…1852a` 恢复分享，复制得到原 `https://example.com/`，刷新仍恢复，界面没有把复制称为发送。均为工程浏览器事实，不代表真实平台发布、系统目标接收或相册最终落地。

## 最终候选原路径复验与范围

Touch `9be70714a0c5f207cee1522f4fec89e31981ed32`、Leads `44defd4d1526b88f5f75e2810117f969350f3f49` 分别独立 build。原红例：B 经 `/admin` 正常登录，另开旧 `/work/leads` 和旧 `/admin` 两个标签，原 `/admin` 正常退出 B、登录 A；两个旧标签均自动请求 whoami 200，principal_ref 均为 `usr_e2e_owner`；旧 admin 显示 A 邮箱，旧工作台在 A 有原租户权限后重新呈现线索。未改 cookie/storage；未逐帧捕获卸载瞬间。以该最终组合再次从同一原咨询行进入原 Leads 详情并经原 return 回到 `sto_9f6fd1a6618e423416579859509843bc`，无新咨询、无伪 ACK。

同 Touch 最终 SHA，原 store 库 A01 原 session/v4 仍预览 readyState 4、无媒体错误，未准入且无保存视频/复制文案按钮；A05 原 session/原 handoff/v5 恢复，真实工程 TLS 下载 `01-image.png`，文件 SHA-256 与先前原文件一致 `e428b08a81989c5ff39ff637d4da4e9f5b7b38fd6b3a8bc4eb72ba4990c46b63`，刷新仍恢复。原 customer 库 A27 原 session/handoff 恢复，复制得到原 `https://example.com/`，刷新仍恢复；均不声称实际平台接收、发布或手机相册落地。

未证项：正式 Touch target 仍未注册；正式 Identity/Notify、真机平台目标及正式发布均未验证。浏览器未覆盖多租户范围选择、账号无原记录权限时的跨标签展示、当前成员撤权或许可撤回发生在 mint 与 redeem 之间、两条有效 h 并发与到期；不可把工程 HTTP 定向测试或上述有权账号换页外推为这些对抗场景通过。原两条已撤回 ref 未恢复，新咨询和真实 ACK 保留；本次未 push、未更新 Linear 或 Done。
