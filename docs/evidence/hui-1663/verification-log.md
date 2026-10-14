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
