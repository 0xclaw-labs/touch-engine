# 2026-10-11 AG07 Touch 设计（HUI-2390 / HUI-2628 续接）

状态：协调者自批（本角色无人可回答，普通问题由协调者裁定并记录理由）。
分支：`ag07/20261010-touch-campaign-lead`（PR #48，OPEN）。基线 `origin/main` `c8c1eb42310dffe1d01ddce1b3e4a209a9277595`。
本文件不替代 `2026-10-10-ag07-hui-2628-2390-design.md`，只记录 2026-10-11 续接阶段的决定。

## 目标（本阶段）

1. 核 PR #48 在最新 HEAD 上的真实测试结果，不沿用 10-10 的旧数字。
2. 修复 HUI-2390 中“用户重复点击 / 超时 / 重登恢复同草稿”的缺口：当前每次点击都向矩阵 POST 一条新草稿。
3. 明确 HUI-2628 中 Notify→Leads 真实回执的前置条件，不伪造 `crm_received`。
4. 跨群缺口（矩阵目的端 authority、请求丢失的幂等键、本仓 minsupply 的定位）交 AG00 裁定，不在本仓绕开。

## 决定与理由

- D1 续接而不是新开。`_worktrees/touch-engine/ag07-20261010` 干净、分支即 PR #48、与票同源。按协议“有则续接，不再开第二个作者”，不新建 worktree。根 checkout（detached `63acba8`，有 `.gitignore` 修改与未跟踪文件）不触碰。
- D2 不新开 PR。PR #48 已是本票的唯一在途分支。推送为同分支快进，不 force-push，不新开 draft 造成重复。
- D3 幂等放在权限检查之后。撤权必须立即拦住重复点击，所以 `Consumer.Schedule` 先做 `decide`，通过后才复用已记录草稿（`matrixhandoff.Service.Existing`）。复用时不调用 `DraftSink.PutDraft`，因此不产生第二条矩阵计划。
- D4 复用条件取 `identityKey`（租户、品牌、活动、版本、素材哈希、到期时间）。内容或版本变化自然产生新草稿，旧草稿不被覆盖。
- D5 测试期望的修正。`httpapi` 原有 replay 断言把重复 POST 记为 2（`sink.puts != 2`）。这是 bug 的期望值。本轮改为 1，并在 replay 处加 `puts == 1`。这是收紧，不是降门槛，证据见 plan 文件。
- D6 “请求丢失后重试”的幂等未在本轮实现。若矩阵已建计划而 Touch 没收到响应，重试仍会二次建草稿。根治需要矩阵侧接受 `Idempotency-Key`，这是跨群契约变更，记入 escalations，不在 Touch 单边加头。
- D7 Notify→Leads 真实旅程本轮不可跑。leads-engine 的非测试代码没有 platform-notify directed event 的接收路由，`config.go` 注明 notify 为 L0 scaffolding。缺此端，`crm_received=true` 不可能真实取得。记为 BLOCKED，解除条件见 plan。
- D8 独立审查本轮无法派出。当前会话没有可用的子代理派生工具，按协议记 NOT_RUN 并写入 escalations，不自审签字。
- D9 `server/cmd/minsupply` 是本仓内的本地供给桩。它只在配置了 URL 时才被拨号，空配置不拨号。它不是独立矩阵服务。是否保留在 PR 中，由 AG00 按 HUI-2390 的说明裁定。本轮不删除、不扩展。
- D10 不部署、不推送到 main、不合并、不付费生成、不外发。

## 不在本轮

- 真实 AG01 公共权限与矩阵 2373 持久草稿接口的接入（等待上游 URL 与契约）。
- NFC 实机，真实官方平台发布，奖励资金。
- 浏览器 E2E（Playwright），本轮 NOT_RUN。
