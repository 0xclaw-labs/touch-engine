# Leads 43c86aa 独立代码审查（2026-11-14）

审查 `/workspace/scratch/b0e2059d4a63/leads-engine` commit `43c86aa0072946195d48d1c11cda515eba4c96d0`，阅读 `reports/leads-contract-repair.md`、v2 Fetch/ACK、摄取、inbox 和 consent 域代码。只读审查；未作正常 C6 浏览器/Notify 正式服务验收。实施报告的测试和真实 Touch+Leads 工程 relay 证据是实施方报告，本审查未重新声称运行。

## 发现：单次咨询撤回会跨提交撤销同联系人其他许可

`server/internal/httpapi/handlers_notify_ingest.go` 撤回分支先调用 `RevokeMarketingTx`，再调用 `RevokeConsentChannelsTx(... revokedNotifyChannels(...))`；已先有撤回水位的提交路径同样调用后者。`server/internal/httpapi/notify_source_ack.go` 的 tombstone 重放收敛路径也调用后者。`server/internal/store/notify_inbox.go:172` 的营销 UPDATE 只按 tenant/contact/marketing_allowed，`:182` 的渠道 UPDATE 只按 tenant/contact/source_channel；两者均未限定这次 `SourceRef`/`source_submission_ref`。当同一客户在同一目标租户有两个不同来源提交时，撤回其中一个会持久撤销另一提交的联系人授权；重放旧接受事件的收敛同样波及其它提交。

原 `server/internal/store/contact_profile.go` 明确以 `(contact, source_submission_ref, source_channel)` 定义许可键，并要求新授权用新来源提交引用。故 SQL 的影响范围静态可证；尚未以两个实际提交跑动态复现，也尚未确定所有渠道是否存在单独的明确全局撤回合同，需逐项核对，不能简单假定营销全局撤销。建议在 Touch 特定撤回路径按来源提交引用精准收敛四类许可，并保留其他来源既有合同；对同联系人两个不同 ref、不同来源、撤回先到、旧重放、独立新授权、ACK 重试作真实 SQLite/HTTP 正反验证，确认只撤原授权且原事件 ACK 不变。root 已接手。

## 其他审查边界

v2 取件使用登记来源与独立服务身份、租户头，拒绝重定向并核对源引用/租户；五元 ACK 使用原 inbox event/ref、确定性 receipt，业务提交后发送，失败回 503 重投；tombstone 校验无联系方式并先持久撤回再 ACK。上述为代码路径观察，不代替竞争时序、上游真实事件、正式身份和 Notify 服务的实际联验；本轮没有对全部乱序、重复、跨租户场景独立运行测试，不能据此宣称其全通过。等待范围修复后同原 C6 真实入口和工程 relay 复审，正式交付仍需正式 Notify 与注册身份证据。

## 404b9f4 独立增量审查及原库浏览器闭环

2026-11-14；Leads `404b9f4ff2b0b5e86771338d1566127befd4ad31`，原分支工作树干净、只读审查 `1af4ec4..404b9f4`；Touch 独立工作树 `47421ea352f70d362551f2c76b40e0e6c86df4a2`，Next production build 成功。原 `/tmp/touch-e2e-ZLFKmv/touch.db` 与 media 原位保留，未 seed 或以 SQL 制造状态。真实 Touch 与 Leads 服务、Playwright 同一执行环境；Notify 是工程 relay，Leads 目标租户为工程 CLI 新建并以工程身份登记源映射。relay 只允许本次浏览器新提交 ref，其余原待发事件返回可重试错误；不能以工程 relay 推断正式 Notify 投递。

从原 `/s/sf_eeeffe9978ae3cf2`→隐私与授权→「请门店联系我」，姓名留空、输入有效合成手机号、勾本次服务授权、不勾营销，UI 提交得到 `sub_8b61d688bc2a6cff45614417`，初始「待同步，对方客户系统还没确认收到」（`runtime/leads-browser-submitted-430.png`）。原 forwarder 送原事件，Leads 首次响应 503；relay 重投同一原事件后 200，真实源 ACK stored 后通过同页查询，明确显示客户系统已接收、负责人跟进未知（`runtime/leads-browser-received-430.png`）。

同一提交经 UI 撤销，初显接收方待确认；原撤回事件首次 503，重投相同事件 200，真实 revoked ACK 后查询显示「授权已撤回，接收方已确认处理」（`runtime/leads-browser-revoked-430.png`）。刷新后输入同一原编号和原手机号，通过「读取原记录」得到相同确认状态（`runtime/leads-browser-reload-430.png`）。只读事实核查 Touch 接收凭据为 stored revision 1 与 revoked revision 2；Leads 一条 contact、一条 lead、零活跃许可。旧 `sub_555c19fad47a379566202232` receipts 仍为 0，没有对其伪造 ACK；未观察源服务器具体首次失败 HTTP 码，不把其推断为已实测 409。

独立读取 `notify_consent_scope.go`、`notify_source_ack.go` 及对应真实 HTTP/SQLite 测试，重点检查不同 Touch ref 与 partner 不误撤、同 raw ref 异来源许可键隔离、唯一历史认领、歧义 pending 不 ACK、撤回水位单调与旧事件不复活、异源 B 冲突不占 A 撤回键。实际运行 `go -C server test ./internal/httpapi ./internal/store ./internal/notifyingest -run 'TestTouch|TestNotify|TestGlobal' -count=1` 通过；后另运行 `go -C server test ./internal/httpapi -run '^TestConsentLifecyclePerSource$' -count=1` 通过，覆盖原联系人停止营销操作及重放不复活。`git diff --check 1af4ec4..404b9f4` 通过。已审查范围未发现新阻断；测试 fixture 的歧义/异源场景是工程测试，不声称本次浏览器实例中实造了历史冲突。

边界：本轮证实本地真实进程、真实持久化 ACK 与正常浏览器闭环，依赖工程 relay、工程接收身份与租户配置；正式 Notify、正式身份与部署未验。历史来源归属不足按合同 pending，需要可审计人工处置，不能当已撤回。未 push、未写 Linear、未标 Done。
