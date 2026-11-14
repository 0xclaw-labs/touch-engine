# HUI-4662 / HUI-4666：Leads 接收合同窄修实施报告

本阶段实施与工程验证完成，等待 root 指派的独立 gpt-6.1-terra 正常浏览器及代码审查；本实施者不标 Done、不写 Linear、不 push/main。

## 提交与写面

- 工作树 `/workspace/scratch/b0e2059d4a63/leads-engine`，原分支 `codex/touch-lead-optional-name`。
- 保留原 customer 两个提交及空称呼授权边界，起点 `65ded8a2dcf0b913059ecaf5a19393aaa421772c`。
- 本轮独立本地提交：`43c86aa0072946195d48d1c11cda515eba4c96d0`，提交后 `git status --short` 无输出。
- 只写 Leads 仓；Touch integration 源码及原正常走查数据库均未修改。无迁移、无新增 CRM / queue / wallet。

实际文件：

| 文件 | 作用 |
| --- | --- |
| `server/internal/notifyingest/fetch.go` | Touch v2 取件、明确授权元数据验证、严格五元 ACK HTTP 客户端；其他来源旧取件协议保留；取件和 ACK 均拒绝重定向 |
| `server/internal/httpapi/handlers_notify_ingest.go` | 所有成功出口统一 commit 后补源 ACK；可信零联系 tombstone 不做联系字段哈希；四类 Touch 许可与实际撤回水位收敛 |
| `server/internal/httpapi/notify_source_ack.go` | 原来源/租户/事件/提交/版本/状态确定性 receipt；从原持久 inbox 恢复 ACK；旧 accepted inbox 补 ACK；重放实时核源撤回，先持久收敛再发 revoked |
| `server/internal/store/notify_inbox.go` | 复用原 ReceiptJSON 持久源 ACK 意图，不另造队列 |
| `server/internal/config/config.go`、`deploy/leads-server.env.example` | 新增部署注入 `LEADS_INGEST_TOUCH_ENGINE_FETCH_SERVICE_ID`，Touch 缺独立服务身份 fail-closed；令牌仍由 FETCH_TOKEN 注入 |
| `server/internal/config/config_test.go`、`server/internal/notifyingest/fetch_test.go` | 配置门、真实 HTTP 路径/三 header/身份/租户/metadata/严格 ACK/重定向与旧来源回归 |
| `server/internal/httpapi/notify_ingest_ack_test.go`、`notify_ingest_test.go`、`server_test.go` | SQLite 摄取、事务提交时序、ACK 拒绝/响应丢失、重放/撤回/乱序/隔离与旧 inbox 回填；旧 recordSource 改为明确 v2 形状 |
| `scripts/verify-touch-contract.py` | 可复跑的真实 Touch + Leads 独立进程工程联调，Notify 为明确工程 relay |
| `docs/integration/touch-leads-contract.md` | 部署身份、事务/重试语义、复跑命令和外部条件说明 |

Touch 的唯一服务合同为 `GET /api/v1/internal/lead-records/{ref}` 与 `POST …/{ref}/acknowledgements`，共同携带 `X-Service-ID/X-Service-Token/X-Leads-Tenant`。服务 ID 和专属令牌来自部署登记，源端必须有 `lead_receiver` 角色；未以 BFF key 或商家 session 代替。ACK 严格只有 `receipt_id/source_event_id/submission_id/source_revision/receiver_state`。事件 ID 来自原持久 inbox 的 profile event ID，版本来自实际源记录/撤回水位；没有新造 source event ID。

本地联系人/线索、consents、分配及 inbox 仍使用原业务事务。ACK 意图同 receipt 持久化；任何摄取成功出口都在 commit 后进入统一 ACK 流程。源 ACK 失败（包括原 outbox 尚未 markForwarded 的 409）回 503 `source_ack_pending`，重投原事件恢复原 receipt，既不重复建档也不捏造接收成功。源已撤回或本地有撤回水位时不得发送 stored。Touch 的 form_authorization/channel_reply/marketing_phone/marketing_sms 全部撤回；其他来源保持原取件和营销撤回协议。

## 先红后绿

实际运行并观察到的产品失败：

1. `TestTouchRecordUsesRegisteredReceiverContract`：原 FetchRecord 打退役路径，真实 v2 HTTP 合同返回 `source record status 404`。改为登记接收方协议后通过。
2. `TestTouchAckFailureRetriesCommittedOriginalReceipt`：原摄取没有向源 ACK，即使源设为冲突也返回 200 accepted。修后先提交真实 SQLite 业务/inbox，源 ACK 拒绝返回 503；同事件重投以完全相同五元 ACK 成功，单 contact/lead/inbox。
3. `TestTouchTombstoneRevokesWithoutContactHashAndOldReplayCannotResurrect`：原零联系字段 tombstone 返回 409 `payload_hash_mismatch`。修后接受真实撤回事实，四类许可停止，旧 submit 重放只能发送 revoked revision 2。

其余有意义回归包括：旧 accepted inbox 去掉未曾实现的 ACK 元数据后原事件补回；未收到 revoke Notify 时，仅旧 accepted submit 重放也能读取源 tombstone 并持久撤回；撤回先到不创建 contact；源租户相同 ref/event ID 的不同租户隔离；缺 metadata、错源租户、缺服务 ID、业务/inbox 回滚均不发送 ACK；源已接收 ACK 但 TCP 响应丢失后重试相同 receipt；trace alias / 旧版本乱序沿原 inbox 恢复原事件引用；partner 来源旧协议继续通过。

## 验证命令与结果

环境：`source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`，Go 1.26.5。

- 初轮相关包 `go -C server test ./internal/httpapi ./internal/notifyingest ./internal/config ./internal/store -count=1`：全部通过（HTTP 18.155s / store 4.929s）。
- 完整服务 `go -C server test ./... -count=1`：全部通过。最终当前代码/测试再次全量通过，HTTP 20.971s / store 4.642s；日志 `leads-contract-go-final.log`。初次完整日志 `leads-contract-go-all.log` 同样全绿。
- 关键合同 race：`go -C server test -race ./internal/httpapi ./internal/notifyingest -run 'TestTouch|TestNotifyIngest|TestTenantMap|TestAcknowledgement|TestLegacyRegistered' -count=1`：最终通过，HTTP 33.720s / notifyingest 1.038s；日志 `leads-contract-race-final.log`。
- race 首次发现新“ACK 响应丢失”测试夹具的 `recordSource.ackStatus` 无锁切换，已改为互斥读写并以同命令复跑通过。保留首轮失败日志 `leads-contract-race.log`，不是隐去的生产代码 race。
- `python3 -m py_compile scripts/verify-touch-contract.py`、`git diff --check`：通过。
- 本轮无前端产品改动，不声称 Leads 全站前端 build/tsc 验证。

## 真实跨仓独立进程 HTTP 工程联调

已实际执行，不是仅列拓扑：

```sh
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
python3 scripts/verify-touch-contract.py \
  --touch-repo /workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/integration \
  --source-db /tmp/touch-e2e-ZLFKmv/touch.db \
  --original-revoked-ref sub_555c19fad47a379566202232 \
  --output-dir /tmp/leads-touch-contract-20261114-final
```

来源：Terra 正常 C6 浏览器走查的原数据库（只读 backup），原真实撤回记录 `sub_555c19fad47a379566202232`。独立副本 `/tmp/leads-touch-contract-20261114-final/touch-copy.db`，没有改原库，没有声明恢复原匿名 cookie。

构建真实 Touch 的 subprocess cwd 明确为 `integration/server`，真实 Leads 为本工作树 `server`；同 exec 启动两进程与明确工程 Notify relay。目标租户由 Leads 原 `provision-tenant` CLI 创建，源→目标映射部署登记，专用本地接收服务令牌真实注册到 Touch lead_receiver。未调用 Identity 登录接口，未把任何工程配置当正式平台身份/凭据。

| 核对项 | 实际结果 |
| --- | --- |
| 原 C6 记录 | 源端零 phone/name tombstone，authorization=revoked，revision 2；其 submit 在撤回前未转发，outbox forwarded=2 被抑制，无 revoke outbox，真实 ACK 拒绝 409，Leads 返回可重试 503；Touch receipts 仍为 0。未改状态凑 ACK |
| 实际源安全接口 | 错专用令牌 401、错源租户 404、带 BFF 内部令牌的退役旧路径不可用 |
| 副本新公开咨询 | 使用已启用的原 storefront，公开 HTTP 提交空称呼、有效合成手机号，正常返回 201；新 ref `sub_b624790a8a52b6babb4d0a1b` |
| 真实 forwarder + 首次交付 | 原 outbox/profile envelope 经工程 relay 投 Leads；Leads 已提交本地业务/inbox，但 Touch 尚未 markForwarded，真实 ACK 409 → Leads 503 |
| 原事件重试 | 工程 relay 仅重投原 envelope；Touch 原 forwarder 自行完成 forwarded 标记后真实 ACK 201，Touch 公开状态 `state=crm_received, receiver_state=stored` |
| 正常公开撤回 | 本次 ref + 原手机号调用公开 lead-revocations；本地立即撤回，原 revoke outbox 由真实 forwarder 投递；再次经历真实 409/503，原事件重投后真实 revoked ACK 201 |
| 旧提交重放 | 原 submit envelope 再投；源返回零联系 tombstone，Leads 不复活任何许可，发送原 submit event ID + revoked revision 2 的真实 ACK |
| 最终业务结果 | Leads 单 contact / 单 lead / 零活跃 consents；Touch 公开状态 revoked、receiver_state=revoked、revocation_pending=false |
| 源端持久 receipts | 原 submit event stored revision 1；原 revoke event revoked revision 2；旧 submit event revoked revision 2。均由真实 Leads HTTP 客户端写入 Touch ACK 端点 |

脱敏结果 `leads-contract-cross-process-evidence.json`；完整脱敏运行日志 `leads-contract-cross-process-final.log`。实际双进程原日志、二进制、业务副本保留在 `/tmp/leads-touch-contract-20261114-final/`；目录权限 0700，不在报告输出联系方式和令牌。

初次工程脚本在退役旧路径的纯文本 404 上尝试 JSON decode 失败（产品路径并未假绿），修为正确处理非 JSON 错误后整个流程实际通过；首轮日志 `leads-contract-cross-process.log` 保留。成功后最终脚本仅补充排除继承的应用环境变量与副本媒体目录隔离，未更改合同联调逻辑；依 root 指示不再扩测，最终脚本经语法编译。

构建可追溯限制：当前 integration HEAD 在报告时为 `53a76f2a488cc7d4ace03ac46c46f74630f29231`。Go 二进制嵌入的 vcs 信息却显示仓根旧 `9dd04a7`，不能把该标记当成 integration 不可变提交构建证明；明确以脚本指定源码 cwd、实际 HTTP/事务结果以及二进制 SHA256 核对。路径、实际 SHA256 与此限制保存在 `leads-contract-build-manifest.json`。没有拿错误内嵌 SHA 冒称已验收当前整个集成分支。

## 尚需独立与外部条件

- 本次新咨询/撤回是复制实例的公开 HTTP 合同联调，Notify relay 是工程替身；不等于正常顾客浏览器整条闭环或正式 Notify 服务验收。
- root 按用户要求指派独立 gpt-6.1-terra 从原 C4/C6 正常浏览器入口验证，并对本 commit 独立代码审查；出现问题沿原路径修复复验后才可讨论 Done。
- 正式 Notify 的定向订阅、实际签名密钥/写入身份与正式投递服务，实际 Touch lead_receiver 登记与 token、真实源/目标租户映射，需要获准部署环境。不能用本地工程 relay 代替正式交付。
- 原 C6 被抑制、从未发出的事件不存在可合法引用的 forwarded 源事件；该原记录不能伪造为接收方已确认。本报告保留真实失败前提，由 root 判断源端“未曾外发即撤回”的产品状态是否需独立处理；本消费者未开放旧口或造 ACK。
- 已接受的历史 inbox 可随原事件重投补 ACK；若没有持久记录所应用的实际版本，而源已更新为更高活跃版本，需要原新版本事件，不猜已存储。原只读 receipt 查询不会偷偷触发写 ACK。

## 独立审查后补修

独立 Terra 发现单次咨询撤回 SQL 范围过宽；已由 `404b9f4` 按原提交作用域修复并补来源/源租户权威核对。本文关于许可收敛的初版实现以 `leads-consent-scope-repair.md` 后续报告及新提交为准；历史歧义保持 pending，不冒称已撤回。等待 root 独立复审。
