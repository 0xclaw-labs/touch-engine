# HUI-3553 — C0 合同采用记录

- 合同：`mx-ops-c0/v1`
- 发布 commit：`6ffc90ca`（0xclaw-labs/public-ai，`docs/contracts/mx-ops-c0/v1/`）
- 上一版快照：`45b32f4b`（G00 errata：accepted 回执允许 per-object convergence=unknown，只影响
  `config-command-response` 消费方；本仓不消费该对象，因此仅记录 superseding，无需重 vendored）
- 取用日期：2026-10-19
- 校验：本合同仓内 37 artifact digest / 56 vectors 由 G01 与 Lead 复核；本仓只消费下列对象。

## 采用对象与字段

| 对象 | 采用字段 | 用途 |
|---|---|---|
| `execution_policy` | `template`、`allowed_policy_templates`（model-offering 侧）、`time_window`、`dispatch`、`compute_permission`、`cost_bearing`、`service_level`、`enabled`、`policy_version` | 商家执行偏好选项与费用承担展示；只做交集展示，不做服务端政策判断 |
| `job_queue_view` | 全部公开字段（`phase`/`stage`/`wait_reason`/`position`/`jobs_ahead`/`waiting_jobs_ahead`/`running_jobs`/`eta_seconds_min|max`/`next_eligible_at_unix`/`policy_source`/`policy_version`/`capacity_state`/`borrowed`/`as_of_unix`）；`internal_debug` 仅内部、公开投影整键删除 | 云端制作等待/队列/可空 ETA |
| `execution_grant` | `cost_bearing`、`price_ref`、`entitlement`、`validity`（只读展示） | 费用承担与授权到期提示；不建钱包、不预扣 |
| `error_taxonomy` | `class`/`code`/`retryable`（只映射商家文案） | 失败/等待文案，不回显机器码给商家 |

未采用：`model_offering` 写面、`compute_pool`、`config_command`、`config_command_response`（归 G01/G02/3824/3826）。

## 使用方式与边界

- TS 类型副本：`web/src/lib/mx-ops/contract.ts`（从 `6ffc90ca` 原样拷贝，头部标注 sha；不改语义）。
- 机器可读 fixture：`web/tests/fixtures/mx-ops/`（均为合同正例，另有本仓回流例）。
- 读取规则：未知=`null`，`0` 是有效测量值；ETA 半未知/倒置按合同判非法；`internal_debug` 不进入公开投影。
- 服务端同步接缝：`server/internal/mxopsview` 严格校验 `job_queue_view` 后只存公开投影。
- 本仓**不新增**合同字段/枚举；无 additive 需求。
- 真实上游投影/策略/账务：`UNKNOWN`，由 HUI-3836 窗口验收，不在本记录内。
