# 无活动门店稳定入口与实体交付接续修复

分支 `codex/touch-store-entry-repair`，工作树 `touch-engine/.worktrees/store-entry-repair`，起点 `53a76f2`。仅本地提交；未 push、未写 Linear、未标 Done。对应 HUI-4655、HUI-4664 的真实商业走查断链。本文为工程实施记录，正式验收仍待 root 安排 gpt-6.1-terra 从原商业库正常浏览器路径复验。

## 实际修复

- 原 `POST /api/v1/touchpoints` 新增无 tag 的门店根创建：显式当前 `store_id`、名称、位置及 `creation_request_key`。服务端依原 manage_tags、租户与门店记录范围裁决，停用门店拒绝。旧 tag 创建仍从 tag 派生门店/活动，tag 与客户端 store/key 混填拒绝，避免目标篡改。
- 继续写原 `touchpoint_aliases`；服务端原熵源生成不可猜 code，`target_type=store`、tag/campaign 均 NULL、`registered`，通过既有 `EnsureStorefront` 保证原门店目标存在；公共入口仍是原 `/t/{code}`→原 `/s/{store_code}`，未创建活动、第二链接域或另一个履约权威。
- 0136 仅给原 alias 增加 `creation_request_key` 与 canonical request payload，tenant/key 唯一索引。事务内查询/插入/audit，同 key 同 payload 返回原 alias（200），异 payload 409。先授权请求门店、原 key 所指记录，并对事务实际获胜记录重查 scope，跨门店不能借 key 判断存在性。
- 全 suite 的并发测试发现原 `EnsureStorefront` 首次并发时会因 store_id 唯一冲突反复换码，最终 500。只在本组 `ProvisionStoreTouchpoint` 内补失败后的原首页纯读恢复（`storefrontByStore`），有 canonical 行才继续；不改其他组 `store_actions.go`。
- 门店“投放入口”默认直接创建本店稳定入口，提供名称与摆放位置。旧卡接续及旧共享链接停用留在可选展开；用户无需输入内部 ID。已有复制、二维码、CSV、停用、换绑及显式阶段功能保留，增加直接“打开顾客入口”链接且使用服务端原 `/t` URL。
- 前端按 tenant/当前账号/门店持久化同一次创建意图，发送前冻结 key+payload；双击锁住同请求，丢响应或刷新后再次 POST 恢复原入口；成功仍保留 key，只有明确“另建一个入口”才清除意图并生成新 key。未确认成功时不鼓励另建，避免丢响应后重复入口。
- 无 delivery 的稳定入口增加“关联硬件交付”：复用原 `GET billing/orders?kind=hardware` 与原订单详情，只列硬件 payment projection=done 的原订单，使用商品名/时间供选择，调用原 `POST touchpoint-deliveries`。实体卡编号可在取得实物后由操作者提供，创建只到 `awaiting_dispatch`，不自动登记设备、证据、写卡或验收。
- manager 无 billing:view 时原接口仍 403，不新增全组织财务投影、不扩张账单/下单/付款权；提示有账单权限负责人先关联订单，manager 继续原已关联 delivery。只有已有账单读权时显示正确的本商家账单去向。付款未知、无订单、付款未确认及上游失败均如实提示；账单网络失败不阻断稳定入口列表与创建。
- 原 delivery store 方法遗漏停用入口前置：新增原 `ErrTouchpointDisabled` gate，保证 disabled alias 不新建实体交付。已有事件、回读、补换未改。

## 有意义的红绿验证

1. 新增真实 HTTP 回归后先执行 `go test ./internal/httpapi -run TestStoreRootTouchpointCreationAndRecoveryHTTP -count=1`，旧实现失败：`400 bad_request: tag_id is required`，预期201。
2. 改原模型后同测试通过；无活动无 tag 的门店真实 POST 创建 /t，再通过原服务草稿/首页 preflight/publish API 发布 Wi-Fi，公共 touchpoint 返回原 store_code，再由该根读取已发布 Wi-Fi。
3. HTTP 覆盖同 key 恢复/异负载409、8路并发仅1条新 alias、跨tenant/store掩码、原key指其他门店不能查询、manager本店、成员停用、门店停用、tag+store歧义拒绝、QR/CSV零部署推进。原tag兼容与完整旧触点 suite 一并运行。
4. 实体交付 HTTP 用工程 Billing 受信 authority seam 记录付款，先验证未付款原订单409，付款后只创建 awaiting_dispatch；manager账单仍403、逻辑入口仍registered。补停用入口测试先红（旧方法201，应409），加入 gate 后通过。此付款缝是工程替身，不能证明真实支付。
5. 全 suite 第一次暴露 EnsureStorefront 并发500，修后 focused `go test ./internal/httpapi -run TestStoreRoot -count=12` 通过（10.247s）；最终纯读收口后 `-count=3` 再通过（2.330s）。
6. 前端4个相关文件59测通过（creation helper 1、panel 19、touchpoints 29、deliveries 10）。helper以可重新实例化的storage验证同请求双击、丢响应后页面恢复、冻结payload及明确另建。
7. `npx tsc --noEmit` 通过。最终 production build 与全相关 Go suite 结果见下方提交补记。

## 复验入口与证据边界

Terra 应在原 `/tmp/commercial-preserve/touch.db`（本文未打开、更改或插入）与原 commercial 组织/门店，经正常 `/` 登录→当前门店→投放入口，创建稳定入口；刷新应恢复同请求，打开/复制必须为 `/t`，实际由原 `/s` 呈现已发布门店服务。可对照原 commercial-ops-review.md 的无活动门店。观察创建前后列表数量、复制/下载前后 registered 状态、430/390px 与空态/错误。丢响应后只按“恢复／查询本次入口”，成功后显式“另建”才增加第二条。

若原商业库没有真实已付款硬件订单，界面应清楚显示未确认/无订单与负责人去向，不能为浏览器验收改库造付款；工程交付正向已由受信seam测试覆盖，真实已付款订单、实卡编号、写卡器、回读URL、真机、发货签收、现场验收均需实际提供。记录按钮与二维码下载不构成物理证据。

权限保留现有 manage_tags 角色矩阵；本轮不另授 agent/staff 原本没有的标签管理权限。店员/代理配置与付款授权沿原权威，若后续产品需要更细的标签任务授权，应另行消费已定义权限合同，不能由本补丁推导财务权限。

## 文件与最终检查

涉及原 store_touchpoints、handlers_touchpoints、touchpoints-panel，0136，touchpoint-creation helper/测试，HTTP回归与原delivery创建gate；未改 campaign content、store_action_publication 或发布/冻结快照实现。

最终本地 commit：`d27f685`（`fix: create recoverable store entries without campaign tags`）。

- `go test ./internal/httpapi ./internal/store ./internal/db -count=1`：全部通过，httpapi 119.507s、store 74.267s、db 6.888s。该轮包含修复后并发路径；之后将同一恢复改为包内真正纯读 `storefrontByStore`，最终 `TestStoreRoot -count=3` 再通过。
- 最终 `npx tsc --noEmit`：通过。
- 最终 `npm run build`：通过（Next 15.5.25，production compile/types/static pages完整成功）。
- 相关前端59测通过；`git diff --check` 通过。已本地提交，工作树干净。
