# AG07 supervisor status · 2026-10-10

## ticket / owner

- HUI-2628、HUI-2390。本会话是唯一实施者，也是这两张票的 Linear 更新者。
- HUI-1666 保持 Done。只复用 `server/internal/assetlib`，没有重做素材库。
- 历史作者是已合并提交里的 jimfu。`hui-2628`、`hui-2628-r2`、`hui-2390-r3`、`touch-engine-hui2628`、`touch-engine-hui2628-fix2` 都落后于 `origin/main`，没有续写，没有覆盖。
- 无在途 PR。

## 仓 / remote / branch / HEAD

- 仓：`/Volumes/MobileHD/Work/派诺生态/_worktrees/touch-engine/ag07-20261010`
- git 公共目录：`/Volumes/MobileHD/Work/派诺生态/touch-engine/.git`
- remote：`origin` `https://github.com/bianjiefilm/touch-engine.git`
- branch：`ag07/20261010-touch-campaign-lead`（已取消 upstream，避免推到 main）
- 基线：`c8c1eb42310dffe1d01ddce1b3e4a209a9277595`
- 实现提交：`0a1016743a59e3ecbc4d1955d4b61139b14c84ed`
- 供给接线提交：`935854f773e82bdc7aaf6ea1c129e48df577dd1b`
- 本文件提交前的 HEAD 就是接线提交。本文件进入后的尖端是它的子提交。交接时以 `git rev-parse HEAD` 为准；父提交应是 `935854f773e82bdc7aaf6ea1c129e48df577dd1b`。
- 未推送，未合并，未部署。

## 脏文件

- 本分支在写入本文件之前是干净的。`web/package-lock.json` 曾被本地 npm 去掉可选包的 libc 字段，已恢复，未纳入提交。
- `web/node_modules/` 被 gitignore，未提交。
- 正式检出 `/Volumes/MobileHD/Work/派诺生态/touch-engine` 仍停在旧的 detached `63acba852f8223c40184781f2aeea29dbd471dfb`。它原有的 `.gitignore` 修改和未跟踪的 `package-lock.json` 不是本票，没有暂存。

## 实现范围

- HUI-2628：`GET /api/v1/session/memberships` 只按 principal 列启用成员，忽略手填的 `X-Tenant-ID` 和 query。0 个组织不要求填编号；1 个自动选中；多个只给名称。localStorage 只在命中启用成员后写入。admin 与商家登录去掉租户输入。页头显示组织名或「当前组织」。
- 两张裸表移入 `web/src/components/admin/AdminDataTable.tsx`。admin 页面源不再有 `<table`。表单和按钮没有改成整套设计系统。页面普查账本去掉 admin 的 raw table。
- HUI-2390：`matrixconsume` 记下草稿。`outbound_complete` 固定 false。文案不写成发布成功或外发完成。权限或矩阵供给缺失只停矩阵按钮。留资和 UGC 仍各自可用。`TOUCH_PUBLIC_PERMISSION_URL` / `TOUCH_MATRIX_DRAFT_URL` 都为空时不安装客户端、不拨号。
- 留资提交和撤回附上同一条 trace。素材引用和版本来自活动自己的素材行。`source_version` 撤回仍是原值加一。
- 产品图：新增 `launchCampaignImage` / `receiveCandidate` 和 `POST /api/eco-nav/campaign-image/launch`。不调用产品图，不带文件字节。上游声称已生成则拒绝。原测试交接路径未改。

## 测试

| 命令 | 结果 |
| --- | --- |
| `cd server && GOWORK=off go test ./internal/matrixconsume/ ./internal/leads/ ./internal/httpapi/ ./internal/store/` | PASS（接线前；store 之后未再改） |
| `cd server && GOWORK=off go test ./internal/config/ ./internal/httpapi/ ./internal/matrixconsume/ ./internal/leads/ -count=1` | PASS（接线后。config 0.007s，httpapi 7.265s，matrixconsume 0.013s，leads 0.011s） |
| `cd web && npx vitest run` | PASS。34 files，319 tests |
| Playwright / e2e | NOT_RUN。helper 已改为只填邮箱和密码，没有开浏览器 |
| NFC 实机 | NOT_RUN。没有硬件 |
| 真实 AG01 / 矩阵 URL | NOT_RUN。空配置不拨号；httptest 只打本机假服务 |
| 付费生成、生产部署、推送、合并 | NOT_RUN。没有授权 |
| Linear 写入 | NOT_RUN。没有 linear CLI，MCP 在父会话要授权 |

覆盖到的行为：成员列表忽略伪造租户；停用成员消失；whoami 仍拒绝缺头和跨租户；矩阵供给缺失只停矩阵按钮；合法视频记下草稿且 effects 为 0；过期和缺视频不调用草稿；跨租户活动 404 且不写草稿；职员 422；留资事实带 trace，撤回同一条 trace 且 source_version 为 2；既有留资用例里的过期、重复、撤回、跨租户列表随 `./internal/httpapi` 一起通过。浏览器重登没有跑。

## 证据路径

- `server/internal/httpapi/server_ag07_test.go`
- `server/internal/httpapi/server_ag07_leads_test.go`
- `server/internal/leads/source_trace_test.go`
- `server/internal/matrixconsume/consume_test.go`
- `server/internal/matrixconsume/http_supply_test.go`
- `web/tests/workspace-tenant.test.ts`
- `web/tests/admin-surface.test.ts`
- `web/tests/campaign-image-launch.test.ts`
- `docs/superpowers/specs/2026-10-10-ag07-hui-2628-2390-design.md`
- `docs/superpowers/plans/2026-10-10-ag07-hui-2628-2390.md`

## 未测

- NFC 写入与实机。
- Playwright 登录、二维码页面、重登恢复的浏览器路径。
- 真实公共权限和真实矩阵草稿服务。
- 产品图仓、leads-engine 落库、收费、人工验收、生产。

## 交给 AG06 的 payload

提交事实（联系方式不在内）示例：

```json
{
  "trace_id": "lead:{submission_ref}",
  "return_target": "/c/{link_code}",
  "campaign_ref": "{campaign_id}",
  "store_ref": "{store_id}",
  "asset_ref": "ast_trace",
  "campaign_version": "7",
  "grant_ref": "{order_ref，有订单才有}",
  "source_version": 1,
  "consent_ref": "touch://leads/{submission_ref}",
  "consent_version": "v1",
  "marketing_optin": true,
  "channel": "wecom"
}
```

撤回沿用同一个 `trace_id` 和 `return_target`。`source_version` 为上一版加一。测试里提交后撤回是 2。没有手机号和姓名。公共响应不声称 CRM 已接收。

产品图候选入口（不调用产品图）：

```json
{
  "trace_id": "lead:sub_1",
  "campaign_id": "cmp_1",
  "campaign_version": "7",
  "asset_ref": "ast_vid",
  "sha256": "<64 hex>",
  "grant_ref": "grant_1",
  "return_target_id": "return:cmp_1",
  "candidate": true,
  "generated": false,
  "charges_customer": false
}
```

## 跨群阻塞

- AG01 公共权限的可调用 base URL 和字段还没有。解除条件：给出 URL，响应含 `allowed`、`supply`（`ready` 或 `missing`）、`reason`。在此之前矩阵按钮保持停用，不拨号。
- 矩阵草稿 URL 同样未知。解除条件：`POST {base}/drafts` 回 `plan_id`、`status=draft`、`executed=false`。若回 published 或 executed，Touch 记为不是草稿，不标外发完成。
- AG06 负责 leads 落库。Touch 已给出上面的事实。不需要等 AG06 整票 Done。
- 费用和正式外发语义没有新的授权。不付费生成，不把草稿写成发布成功。

## Linear 评论草稿（写入 NOT_RUN）

```
HUI-2628 / HUI-2390 · AG07 · 2026-10-10
branch ag07/20261010-touch-campaign-lead
impl 0a1016743a59e3ecbc4d1955d4b61139b14c84ed
supply 935854f773e82bdc7aaf6ea1c129e48df577dd1b
未推送，未合并。

组织改为成员关系解析，不再手填租户编号。
admin 两张表移入 AdminDataTable。
矩阵草稿 outbound_complete=false；供给缺失只停矩阵按钮。
留资事实带 trace_id、return_target、asset_ref、campaign_version；撤回同一条 trace。
NFC、E2E、真实权限 URL、Linear 写入均为 NOT_RUN。
```

## 下一步

停在本分支和上面的证据。父会话把 AG01 URL 与矩阵 URL 的缺口带给 AG00。AG06 读取本 payload。没有新的推送或部署授权，就不推、不合、不部署。
