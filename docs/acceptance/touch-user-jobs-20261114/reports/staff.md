# HUI-4661 店员统一办理台实施报告

- 状态：工程实现已本地提交，等待 root 指派 gpt-6.1-terra 独立浏览器与对抗门；未标 Done。
- 基线：44bb519；分支 codex/touch-4661-staff；提交 40913bcde8efa60c18e3ea504771d772eaea3cb8。
- 工作树：/workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/staff。
- 未 push、未修改 main、未写 Linear；无新迁移；未触碰顾客容器、session.go、平台 adapter、M9 或 prepare handler。

## 实际改动与沿用权威

1. `/work/service` 仅一个“扫描顾客凭证”入口，手输与相机都交给同一只读解析流程。参与核验凭据用原 `participations/staff/peek`，短时展示码用原 `reward-display-codes/lookup`，旧券原码用原 `reward-benefits/by-code`。32 位十六进制旧券与展示码形状重合时，展示码查无定位后仍由原券码权威确认；明确到期/权限拒绝不改道。
2. 删除扫描前的活动选择和独立券码输入；准确活动、门店、规则版本来自原参与/权益行。活动筛选保留在“待确认与权益记录”页签用于查记录；全门店人员的交班门店必须显式选择有权门店，不再猜第一家门店。
3. 相机修复原先“先等 video.readyState 才赋 srcObject”的死等：先等 DOM video，再挂真实 MediaStream；识别结果直接进入统一只读解析。权限拒绝/不支持仍提供同一手输入口；离开办理页签停止相机。
4. `staff/peek` 补齐用途、目标、期限和撤销校验：customer_resume 不作为店员核验授权；过期/撤销凭据严格拒绝。只读开卡不消费凭据；已消费核验码仍可回看同一参与，原兑换端点仍负责消费。
5. `staff/peek` 返回当前服务端 `confirmation_allowed` 与禁止原因，卡面据此显示可办理动作。暂停活动或参与期限结束不能新发放；没有向旧券查询/核销扩散新发放闸。
6. 经 root 协调授权，仅在 `handleRewardConfirm` 内补 `p.CampaignID == camp.ID` 和 `body.RuleVersion == p.RuleVersion` 冻结绑定。此前真实反例可拿 A 活动参与在 B 活动发券；现在进入账本前拒绝403。
7. 参与卡区分 reserved 与 issued/redeemed/reversed/expired；reserved 继续核对确认，并沿原 inventory_id/benefit_slot，不把预留写成已发放。金额展示传递0134的 `cost_recorded`，真实零成本与未知分开。
8. 502/5xx或200缺业务outcome均按未知先回查；该反例先红后绿。同步 ref 拒绝同页并发重入；请求体按原 participation/benefit 缓存，丢响应保留原 event_id。未知确认只提供“重试同一次确认”，不允许变更证据/现场兑现方式；重新打开同一凭据恢复原待确认操作。回查使用新读权益，reserved 不算发放；核销/更正回读同一权益并保留历史事实。
9. M9 交接契约 `/work/service?campaign=...&benefit=...&return=...` 已接。只从原授权权益列表定位 benefit；不因打开 URL 发放或核销。return 只允许规范化后的同应用 `/work` 路径。

## 文件

- server/internal/httpapi/handlers_participation.go：只窄改 staffPeek。
- server/internal/httpapi/handlers_reward_ops.go：只窄改 rewardConfirm 冻结绑定，prepare 段未动。
- server/internal/httpapi/server_service_desk_test.go：真实 SQLite/HTTP 权限、冻结上下文、只读动作投影反例。
- web/src/components/work/service-desk/service-desk.tsx：统一入口、相机时序、页签、M9只读交接、原操作回查。
- web/src/components/work/service-desk/participation-card.tsx：预留与真实发放分离、原操作重试、成本完整性。
- web/src/lib/service-desk.ts：只读凭证路由、原操作体复用、受控返回路径、预留语义。
- web/tests/service-desk.test.ts、service-desk-page.test.ts、service-desk-credential.test.ts、service-desk-cards-render.test.ts。

## 先红后绿与验证结果

已 source `/workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`。

- `go test ./internal/httpapi -run TestStaffPeekRejectsInvalidCredentialAuthority -count=1`：先红，customer_resume/错目标/到期/撤销均实际200；修后 `TestStaffPeek` 全绿，拒绝不消费凭据。
- `go test ./internal/httpapi -run TestStaffConfirmCannotChangeParticipationFrozenContext -count=1`：先红，跨活动实际200并产生真实权益；另冻结版本反例未按目标403拒绝；绑定后全绿且非法上下文无权益行。
- `go test ./internal/httpapi -run TestStaffPeekReportsAllowedActionsWithoutIssuing -count=1`：先红缺服务端动作投影；修后活动进行/暂停投影准确，开卡权益数量为零。
- `service-desk-credential.test.ts`：初始5项缺能力全红→全绿；追加32位hex旧券反例先红（未定位旧券）→兼容后绿。
- `service-desk.test.ts` 新增 reserved≠issued 语义反例先红→绿。
- 真实 React 静态渲染3项覆盖 reserved仍有明确确认、issued不再确认、未知操作只可重试原操作、服务端禁止原因、已录零成本；初次渲染发现组件缺 React classic runtime 导入，补齐后绿。
- 最终定向：`cd web && npm test -- --run tests/service-desk.test.ts tests/service-desk-credential.test.ts tests/service-desk-page.test.ts tests/service-desk-entry.test.ts tests/service-desk-cards-render.test.ts`：5 files / 58 tests 全绿。
- 最终 `cd web && npx tsc --noEmit`：exit0。
- 最终 `cd server && go test ./internal/httpapi -run 'TestStaff|TestServiceDesk|TestReward|TestStoreServiceHistory' -count=1`：exit0，10.055s。
- `git diff --check`：干净。

完整套件已各跑一次，结果按实记录：

- `cd web && npm test`：115 files / 1167 tests通过，1 file / 3 tests失败。失败均 `store-content-packs.test.ts` 旧源码文案断言：`未挂活动|门店根`、`换版:同一包新开一版`、`门店根内容，不需要先建活动`。root已接手其内容写面基线更新。此全套是在后续新增真实渲染3项/hex旧券1项/网关未知1项前执行，最终staff58项另已重跑绿。
- `cd server && go test ./...`：唯一失败 `httpapi/TestContentPacksMerchantFlow` 的 missing price 旧预期（现实际201候选），httpapi用时117.914s；其余全部包通过。与root既有内容字段流程改动有关，root已通知接手，staff未改内容写面。
- npm有环境http-proxy/Vite CJS弃用提示；无新增运行错误。

## 独立浏览器正常入口与数据准备

- 正常登录后走工作台导航“确认与核销”到 `/work/service`；无需选活动直接输入顾客原核验凭据、权益展示码或旧券原码。输入后必须先见准确原参与/权益，再明确点击确认或核销。
- 工程环境可使用现有 `web/e2e/scripts/env.mjs` 的身份/计费/任务替身及正常 HTTP seed；独立实例建议设 E2E_TAG 和 E2E_GO_PORT/E2E_IDENTITY_PORT/E2E_WEB_PORT/E2E_SEAMS_PORT，避免与root现有服务冲突。不得用改库制造浏览器成功。
- 商家正常配置奖励/导入真实测试券后，顾客从 `/t/{触点码}` 的正常活动入口开始参与，出示店员核验凭据；既有权益在顾客原权益页刷新展示码。旧券数据从原库存导入/发放流程准备；M9记录进入上述带 campaign/benefit/return 的链接。
- 待独立走查：390/430px与桌面；参与/展示码/旧券；M9只读开卡与返回；确认/现场兑现两事实；相机拒绝与手输；多店授权/撤权/错店/customer_resume/到期；暂停后旧券；两店员并发与丢响应后同记录回查；历史与交班返回位置。

## 证据边界与已知限制

- 工程验证：真实SQLite/HTTP原账本和上述58项前端回归已执行；未新增账本或放宽授权。
- 跨模块实际集成：既有服务端集成测试覆盖确认、核销、并发、旧券；本次浏览器正常路径和独立对抗门仍由root安排，不把测试替身当真实平台结果。
- 正式外部交付：本机没有真实店员手机相机、NFC实卡或生产平台凭据；没有声称真实手机扫码/实卡/平台官方结果完成。现相机工程逻辑已修复，需真实手机补证。
- 原展示码权威将过期/未知统一404，页面诚实提示“可能已到期、无效或无权”，并引导回原权益页刷新同一券；不虚构能区分未知码的证据。
- 原操作提交体缓存在当前页面内存；刷新后仍需输入原顾客凭据/从原记录打开，由原参与槽位/权益账本回读去重，未把短时顾客凭据持久落盘。
