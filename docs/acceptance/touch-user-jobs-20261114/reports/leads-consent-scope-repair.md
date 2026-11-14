# Leads：独立审查后单次咨询撤回作用域修复

本地提交 `404b9f4ff2b0b5e86771338d1566127befd4ad31`，基于 `43c86aa`；原 Leads 分支工作树干净。未 push、未写 Linear/Done，仍需 root 指派独立 Terra 复审。

## 核实与修改

独立审查发现属实：原两条撤回 SQL 只约束 tenant/contact/channel，会撤掉同联系人的其他咨询/partner 许可；contact_consents 的裸 source_submission_ref 也没有 app/源租户命名空间。

按 root 裁定复用原不透明许可键、唯一键及 merge/undo 模型，无迁移：

- 新 Touch 许可键由 app/ns/sourceTenant/rawRef 规范 JSON 的摘要稳定派生，使用 `touch-notify/v1/` 保留前缀；该 `/` 不符合 Notify 原始 ref 字符校验，partner 原始 ref 不能命名内部 Touch 键。原 ref 留在 inbox/source_refs/ACK。
- 新 `RevokeTouchSubmissionTx` 仅修改 tenant/contact/该来源键下四种许可；直接 revoke 和旧 accepted tombstone 回放都走此函数。旧来源协议、真正 global opt-out 及通用 consent upsert/merge 实现不扩改。
- 历史裸 ref 只有先前 accepted inbox 唯一证明同一 app/ns/源租户，且 source_refs 权威快照完整匹配才认领；保原 consent ID 和已有撤回标记。多来源证据或不足时保留歧义行，不发 ACK，返回 503 `consent_scope_conflict` 并持久原来源水位和 pending receipt。
- Touch 的 Find/Ensure source_ref 现在核对已存 auth_scope_snapshot 的 app/ns/sourceTenant；不能借同 rawRef 取另一来源的 lead/contact。既有水位也不能通过 ON CONFLICT 被另一来源推进或改归属。
- 已接受的原 inbox 发生源归属异常时，安全持久 pending 与已观察到的单调水位（必要时保存在该 receipt 的 source_tombstone_revision），不发送 ACK、旧 submit 不恢复许可。日志只提供命名空间摘要与生成的 contact/source-row/inbox ID。
- 明确已属于 A 的 rawRef，B 新冲突事件直接 409（活跃提交）/503（撤回），不新增本地业务/inbox/revocation 键、不 ACK；B 的意图由其真实源端 outbox/tombstone 保留。动态验证 B 冲突之后 A 合法 revoke 仍能正常落库、ACK。

文件：`server/internal/store/notify_consent_scope.go`、`server/internal/httpapi/handlers_notify_ingest.go`、`notify_source_ack.go`、`notify_consent_scope_test.go`、`docs/integration/touch-leads-contract.md`。

## 动态红 → 绿

- `leads-consent-scope-red.log`：真实 HTTP 摄取/SQLite 同 contact 两 Touch refs + partner 独立 ref；直接撤回和旧 accepted tombstone 回放均把 8 条无关许可撤掉。同 rawRef 的 Touch/partner 第二份授权新增许可数为 0，证明实际键碰撞。
- `leads-source-scope-red.log`：同目标、同 app、同 rawRef、不同来源 tenant 原响应为 500 `inbox write failed`（旧唯一键阻止 commit，不能冒称已成功跨租户复用）。修后提前明确 source_scope_conflict，原业务不改；B 异源撤回也不占 A 键。
- 绿用例覆盖上述路径、历史唯一认领保原 ID、历史双来源/缺源身份不误撤/不 ACK、水位 2→3 后旧 tombstone 不降低、source_refs 缺 app/ns/源 tenant 均 pending、不改原许可；B 冲突后 A 合法 revoke 及 ACK 正常。

最终相关 suite：

```sh
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
go -C server test ./internal/httpapi ./internal/store ./internal/notifyingest -count=1
```

全部通过：HTTP 21.246s / store 5.257s / notifyingest 0.013s；日志 `leads-consent-scope-suite-final.log`。`git diff --check` 通过。本轮按 root 范围未扩跑无关测试或再次 race。

## 真实双进程合同复验

最终同原 `scripts/verify-touch-contract.py` 实际执行，输出目录 `/tmp/leads-touch-consent-scope-final-20261114`；只读 backup 原正常 C6 DB，真实 Touch+Leads，Notify 仍明确为工程 relay。新公开 HTTP 咨询 `sub_3bed9f2036945522776f5598`。

真实 forwarder 两次首次交付仍各触发未 forwarded 的源 ACK409→Leads503，原事件重投后 ACK201。源公开状态先 crm_received/stored，再经公开撤回变为 revoked、revocation_pending=false；旧 submit 重放只发 revoked revision 2。最终单 contact / 单 lead / 零活跃许可；真实 Touch receipts 为原 submit stored1 / 原 revoke revoked2 / 旧 submit revoked2。

日志 `leads-consent-scope-cross-process-final.log`、脱敏证据 `leads-consent-scope-cross-process-evidence.json`。这是复制实例的公开 HTTP 工程合同复验，未冒称正常顾客浏览器或正式 Notify 服务交付；原 C6 suppressed 记录仍保真实拒绝。

## 保留限制

现有 source_refs、inbox/revocation 的原目标租户/app/rawRef 键不支持同 app 不同来源 tenant 同 rawRef 同时成为两份 lead，本修复按 root 裁定明确拒绝，不扩大迁移或默默合并。Touch 正常 submission ID 为全局随机引用。

历史共享裸 ref 可能已不可逆合并授权；pending 不是撤回已完成，不能猜归属或伪造 ACK。需要另行授权且有审计的数据归属处置，证据不足保持 pending。本轮未将这些历史不可逆冲突标作完成。定位和处置边界见已提交合同文档“单次咨询撤回范围与历史许可归属”；严禁改原 Touch outbox/ACK 状态或清除已撤标记凑成功。
