# HUI-2390 W2 双端证据 · 2026-10-12

真实双端旅程：touch-engine 服务二进制（本仓工作树 `ag07/20261010-touch-campaign-lead` @ `fe7636f` 构建）拨号真实的 matrixdraft 服务二进制（`bianjiefilm/public-ai@6d1aadea` `internal/matrixdraft` 源码构建）。全部 127.0.0.1，无付费、无部署。

## 真/桩清单

- **真**：`matrixdraft` 二进制（sha256 `d305f00870011ef96877752e9cd5d380c7394d54e38e19e83b3beae886f78dce`，源码钉 `public-ai@6d1aadea`，grants JSON 注入 GrantFacts——该 facts 面本身在 public-ai 侧就是「注入表，AG01 wiring NOT_RUN」）；`touch-server` 二进制（sha256 `cfdf747099a067525c1f27fe32a8942d427f5395d0aaf097bb90eb488ee481f6`，本仓源码）。
- **桩（工程本地，明确标注）**：platform identity/upload 元数据面 + AG01 `/permissions` 面（返回 allowed/supply/reason + payer_id/license_ref/license_expires_at，reason=`engineering_stub_permission`）+ 透明录制代理（:18741 → :18742，逐字节转发并记录两侧交换，见 `exchange-*.json`）。AG01 权限 HTTP 面在 public-ai main 上尚不存在（全库 grep 无 `brand_matrix_draft` 路由），此桩只为驱动旅程，不代表 AG01 已接入。
- provision（tenant/member）、登录态、活动/素材/挂载全部走 touch 真实 HTTP 端点（`FEATURE_UPLOAD`/`FEATURE_ASSET_LIB=on`，upload 校验指向桩的 `/internal/v1/upload/assets/{id}`）。

## 旅程与结论

1. **active**（`journey-active.json`，`exchange-01.json`）：商家登录 → 建店/活动/登记视频素材（grant_live_1，sha256 见 `asset-sha256.txt`）→ 挂载 → `POST /api/v1/campaigns/{id}/matrix-draft` → 200，`matrix_button.enabled=true reason=draft_recorded`，本地草稿 `mh_cae851de71582fe2b0e9e9d1`。真实 matrixdraft 201 签发 `md_84ecac562dc801f1`；其 sqlite 行（`matrix-sqlite-dump.txt`）与 Touch 发送的 wire 文档逐字段一致（source_app=touch、content_type=activity、grant_ref、payer_id、license_ref、license_expires_at、return_target、published=0、charged=0）。Touch 服务日志记录同一 ack（`both-ends-draft-id.txt`）。
2. **revoked**（`journey-revoked.json`，`exchange-02.json`）：撤权素材（grant_dead_1，grants.json 标 `revoked`）→ 真实 matrixdraft 403 `grant_revoked` → Touch 按钮 `matrix_button.enabled=false reason=matrix_grant_revoked`，零本地草稿，矩阵侧 row_count 仍为 1，lead/ugc 按钮状态原样返回。精确原因透传，不是零日志错误替代。

## NOT_RUN / 边界

- AG01 权限面、双租户撤权矩阵化旅程、响应丢失重试、旧 source 拒绝：本目录只覆盖 active+revoked 两旅程；其余由 `server/internal/matrixconsume/wire_contract_test.go` 的 wire-faithful fixture 覆盖（fixture 标注同一钉定 SHA），正式独立 blind 未做。
- 会员/发布执行、计费、媒体、NFC、外部平台、奖励资金：NOT_RUN，matrixdraft 本身也不做这些。
- minsupply（`server/cmd/minsupply`，本地旧 wire 桩）一字节未动；客户端重钉后它不再被默认路径拨通（`:18741` 代理背后是真服务；minsupply 的旧 `/drafts` 路由已不在客户端路径上）。去留待 Root 裁定。

## 复现

```
cd /tmp/ag07-evidence   # rig 脚本与本文档同源；重跑前 node run-evidence.mjs 会自清端口与库文件
GOWORK=off go build -o /tmp/ag07-evidence/touch-server ./cmd/touch-server   # 本仓 server/
# matrixdraft: public-ai@6d1aadea internal/matrixdraft(+cmd) 抽取为独立模块构建，见上文 sha256
node run-evidence.mjs
```
