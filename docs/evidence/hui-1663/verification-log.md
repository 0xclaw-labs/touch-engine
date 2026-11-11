# HUI-1663 工程准备——受控验证与门禁记录

- 日期：2026-10-15；分支 `hui-1663-nfc-field`（基线 origin/main=072dfb3，开工基线 c803816 后被 main 前进覆盖，已 rebase）
- 本记录全部为实跑输出摘录；E2E 残差如实记录，不冒充全绿。

## 1. 受控验证（票面：本地/受控环境实证「稳定 URL → /c/{code} 解析 → 正确活动」链路）

机验载体 = `server/internal/httpapi/server_field_entry_test.go`（8 用例全 PASS，实跑）：

| 测试 | 钉住的链路/语义 | 结果 |
|------|----------------|------|
| TestFieldEntryChainFromCSVURLToCampaign | 旧路径：CSV url（/c/{code}）→ 解析 → 正确活动+商家归属；QR PNG 载荷解码 = CSV url（同 canonical） | PASS |
| TestFieldRebindWorkflowOldCardKeepsOldTarget | 旧路径换绑：URL 变、旧卡仍指旧活动、原参与 lookup 原样恢复 | PASS |
| TestFieldSingleCardDisableIsolation | 旧路径一卡一链：停一卡只关自己；恢复复用 | PASS |
| TestFieldSharedLinkAliasSemantics | 旧路径 shared 别名：停一卡=停共享入口（如实钉住，检查表据此强制一卡一链） | PASS |
| TestFieldDisableVisibleNextRequestWithCache | FEATURE_PUBLIC_CACHE on：标签停用下一次公共请求立即生效（epoch） | PASS |
| TestFieldPauseExpireAndPrintedURLStability | paused/expired 态；管理面编辑同一 URL 继续有效（印刷物料兼容）；坏码统一 404 单字段 | PASS |
| TestFieldStableEntryChainTouchpointToCampaign | 主链：触点 CSV 取号（/t/{code}）→ 公共解析放行活动码 → /c 落正确活动+归属；部署五态显式单步推进 | PASS |
| TestFieldRebindStableURLKeepsOriginalParticipation | 同卡换绑四要素：URL 不变 / 新访客进新目标 / 原参与保留原权益 / 旧码兼容；预览参与数；乐观并发 | PASS |

## 2. 质量门（PR 前自跑，2026-10-15）

| 门 | 命令 | 结果 |
|----|------|------|
| go vet | `cd server && GOWORK=off go vet ./...` | exit 0，无输出 |
| go test | `cd server && GOWORK=off go test ./... -count=1` | **38 包全 ok，0 FAIL** |
| gofmt | `gofmt -l .`（server 下） | 无输出 |
| vitest | `cd web && npx vitest run`（本地二进制 node_modules/.bin/vitest run） | **57 文件 / 599 用例全过** |
| next build | `cd web && npm run build` | 成功（含 /work/touchpoints、/t/[code] 路由） |

## 3. E2E（`bash web/e2e/run.sh`，端口 18460-18462 先 lsof 检查：空闲）

- 首轮全量：28 用例，**26 过 / 2 fail**：
  - `11-controlled-entry`「logged-in merchant lands on the real today desk…」：**单独重跑 8/8 全过**（首轮为冷启动偶发，环境拉起后首用例竞速；非代码问题）。
  - `06-a11y-screenshots`「a11y + screenshots across representative pages」：axe 阻断面出现 **`link-in-text-block`（serious），页面 work-rewards，宽度 430/1024/1440** → 重跑复现同样失败。
- 残差定性：本分支 diff 全部为**新增文件**（测试+文档），零生产代码改动（`git diff origin/main --stat`：7 文件 +1569/-0），该失败与工作树内容无关，属 **origin/main 基线残差**；且 work-rewards（奖励页）属 G1/G3 禁改文件面，本票无权修复。按票面纪律如实记为基线残差，不冒充全绿。
- 证据门首轮提示缺 `evidence/axe-summary.json`（因 spec 06 失败未产出）；partial 重跑 06/11 时截图 48/48 曾报齐全（首轮全量时亦 48/48 在档）。

## 4. 结论

- 受控链路（稳定 URL → /t/{code} → /c/{code} → 正确活动）在仓内实证**是通的**，未发现断链，无需最小修复。
- 硬件项全部 NOT_VERIFIED（见 not-verified.md）；本票实体 NFC 交付不标 Done。

## 5. 2026-11-11 交付域实施轮（TOUCH-ALL-20261109-v1；分支 `touch/hui-1663-nfc-delivery`，基线 1fb747e = wave1+wave2 全合流）

- 范围：交付七步状态机（付款→出库→绑定→写卡→回读→现场验收→交付，不可跳跃）、写卡资料=NFC URL 复用、回读一致性记录、补换两分支、4591 硬件五状态投影接线、M8 交付插槽。
- 机验载体（全 PASS，实跑）：
  - `server/internal/delivery/delivery_test.go`：七步管线/跳步矩阵/未知步/终态拒写/4591 步映射/回读核对（错误域名、错误码、Crockford 归一、路径形状闸）。
  - `server/internal/store/store_touchpoint_deliveries_test.go`：创建闸（跨租户掩码/订单缺失掩码/非硬件订单/付款事实缺失/活交付唯一）、全链事件+投影断言（shipment/write_card/readback/acceptance 操作者与设备一致）、幂等重放零双推、同键异负载冲突、回读不一致不推进不推投影、换绑 stale、补换两分支（recoverable 保留入口/lost 停用旧入口+解析即时停用+继承订单）、封存拒写。
  - `server/internal/httpapi/server_touchpoint_deliveries_test.go`：HTTP 全旅程（创建→七步→交付；未付款 409；event_id 必填）、幂等重放 replayed/IDEMPOTENCY_CONFLICT/step_skipped/replaced 词表 400、权限矩阵（游客 401/staff 403/经理本店/跨租户 404 掩码含订单跨租户）、换绑后 binding_stale=true、补换 HTTP 全分支+审计随详情返回。
  - `web/tests/touchpoint-deliveries.test.ts` + `web/tests/touchpoints-panel.test.ts`（HUI-1663 段）：状态/事件文案不冒充已验证、stale 文案、回读一致性恢复动作、补换二值、面板源契约（事件端点/幂等键前端生成/操作者不由前端传/lost 必选新触点/CSV 复用零改动）。
- 质量门（PR 前自跑，2026-11-11）：
  - `cd server && go test ./...`：见本轮报告（分支全量实跑输出摘录）。
  - `cd web && npx vitest run`：**83 文件 / 1041 用例全绿**（含新增 10+6 用例）。
  - `npx tsc --noEmit`：src 非测试文件 0 错误；残余 7 处全部在 `tests/contrast-gate.test.ts`、`tests/customer-journey-api.test.ts`（他域既有,非本票写面,未动）。
  - `npm run build`：生产构建通过。
  - `gofmt -l` / `go vet ./...`：干净。
- 真机项：**全部保持 NOT_VERIFIED**（not-verified.md A 系 + §D 真机补验清单）。本环境无 NFC 卡/写卡器/真机；交付域工程面（B9–B16）不冒充实体验收；T19 实体卡部分按 §D 清单移交真实环境。
