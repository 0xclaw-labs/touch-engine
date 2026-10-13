# HUI-1668 验证并合入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 审查并合入已有 PR 14，不重做文案功能，不合入后把 Linear 标成 Done。

**Architecture:** 已有实现留在 `hui-1668-ai-copy`。本轮把 `origin/main` 合并进来，只解决 `db_test.go` 的表清单冲突，并把文案迁移从已被占用的 `0010` 顺延到 `0015`。合入使用 `gh pr merge`，不使用 `--admin`。

**Tech Stack:** Go（`server`）、Vitest（`web`）、GitHub PR 14、Linear HUI-1668。

**Spec:** `docs/superpowers/specs/2026-09-28-hui-1668-verify-merge-design.md`

## Global Constraints

- 已有实现，本轮只验证并合入，不标 Done。
- 不接入假模型，不把 `usable` 改成 true。
- 不编造价格、地址、营业时间或营销宣称。
- 不给公共活动页加生成入口，不加 EcoTopNav，不自动发布，不发奖励，不扣费。
- 不 force-push，不跳过 hooks，不改 git config，不删旧工作树。
- 不提交密钥，不改指定的仓库外文件。

---

### Task 1: 合并 main 并修好表清单

**Files:**
- Modify: `server/internal/db/db_test.go`
- Rename: `server/internal/db/migrations/0010_copy_drafts.sql` → `server/internal/db/migrations/0015_copy_drafts.sql`

**Interfaces:**
- Consumes: `origin/main` 的 `TestOpenCreatesSchema` 表名单
- Produces: 按文件名排序后，`copy_drafts` 与 `copy_versions` 出现在 `campaigns` 之后、`customer_publish_attempts` 之前

- [ ] **Step 1: 合并 origin/main**

```bash
git merge origin/main
```

- [ ] **Step 2: 表清单保留双方的表**

`want` 在 `campaigns` 后加入：

```go
		"copy_drafts",
		"copy_versions",
```

其余表与 `origin/main` 的名单一致。

- [ ] **Step 3: 迁移顺延到 0015**

```bash
git mv server/internal/db/migrations/0010_copy_drafts.sql server/internal/db/migrations/0015_copy_drafts.sql
```

- [ ] **Step 4: 跑测试**

```bash
cd server && go test ./...
cd web && npx vitest run
```

Expected: 两边都通过。

- [ ] **Step 5: 提交冲突修复并合入 PR 14**

只提交冲突修复和本轮设计说明。然后 `git push` 与 `gh pr merge 14`，不使用 `--admin`。Linear 保持 In Review。
