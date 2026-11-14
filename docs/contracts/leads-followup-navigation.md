# M11 原线索逐条跟进与原店返回

只处理 `/work/leads` 中原接收方确认入库且许可未撤回的单条提交。不会把 `receiver_id`、手机号或 `source_ref` 当作 Leads lead ID，也不更新 ACK、consent 或撤回水位。

- `POST /api/v1/work/leads/{ref}/follow-up`：原成员会话、当前租户、原门店与 ReadRecord 权限。跨店原记录统一 404；待同步/撤回/归属未知 409。通过现 `LEADS_FOLLOW_UP_BASE_URL/TOKEN` 受信通路，完整来源固定为 `touch-engine/notify/sourceTenant/ref`，解析真实 CRM 租户和记录。
- 原 Identity `shell/launch` 仍严格四字段，目标为登记的 `leads-engine / ti-leads-engine-web`，使用解析所得 CRM 租户。原 code+h 与 launch URL 原样保存；仍通过原 jump mint/redeem 一次性交给浏览器。通用 switch 的两字段协议不变。
- `POST /api/v1/internal/lead-follow-ups/{originH}/returns`：仅原登记 lead_receiver 服务身份。只接新 shell handoff 和原 principal，不接受门店、租户映射或 return URL。原源租户由 `X-Leads-Tenant` 对已保存 origin 核验，当前源成员与门店读权再次裁决。
- `POST /api/v1/lead-follow-up-returns/{h}/resume`：原 SSO 后真实会话核身份；从服务端上下文选租户及原店，当前 store scope 与读权通过才一次消费并返回固定 `/work/stores/{id}`。不需要恢复已撤回的咨询许可即可安全返回原店。

迁移 `0138_follow_up_navigation_contexts.sql` 只保存短期导航来源/主体/门店/记录引用；不保存联系方式，不复制 CRM。来源上下文 30 分钟，返回 h 60 秒、一次消费；未知 h 没有业务信息，已知句柄对错误身份不会返回记录。

Touch 新 SSO 桥从登记根路径原样接收 code+h，经同源表单与原 Identity exchange 换码；只转发 Identity 签发的 `touch-engine_session`，不生成/改名/复制会话。部署必须显式配置 `SSO_APP_HOST`、`IDENTITY_SSO_EXCHANGE_URL` 与 `TOUCH_SESSION_COOKIE=touch-engine_session`。没有 canonical host 时 fail closed。桥只接受固定的 `/lead-follow-up-return/hnd_<32 hex>` 站内接续路径，不接用户自报 return URL。

当前 public-ai 生产 manifest 没有 Touch app/launch target，不能宣称正式返回已获准。Leads 返回按钮如实显示“原店返回入口未获平台登记或当前不可用”，保持原详情并允许重试。工程审查只能在明确声明的 Identity fixture 登记 `touch-engine / ti-touch-engine-web`，不能改生产 manifest 冒充资格。

完整双端合同、部署变量及正常入口浏览器 runner 见 Leads 仓 `docs/integration/touch-followup-navigation.md`。本端相关回归：`go -C server test ./internal/httpapi -run 'TestWorkLeadFollowUp|TestWorkLeads' -count=1`，Web `tests/follow-up-sso.test.ts`。

配置补充样例 `deploy/touch-followup-navigation.env.example` 必须在原运行 env 后显式加载。canonical cookie 已由真实 HTTP 工程 Identity exchange→原 Go session/resolve→whoami 回归核验；没有绕过 Go 身份解析。`touch_sso_reset` 由原 merchant-session 入口消费，清旧 tenant memory 并作废旧 tenant 确认序号；原店 continuation 先消费 reset，再保存本次服务端核准的源租户。多租户新账号因此回到原受控组织选择，不沿用上一账号的组织记忆。
