# HUI-1668 活动文案草稿：本轮只验证并合入

| 字段 | 值 |
| --- | --- |
| 日期 | 2026-09-28 |
| Linear | HUI-1668 |
| 已有实现 | PR https://github.com/bianjiefilm/touch-engine/pull/14 ，分支 `hui-1668-ai-copy`，头 `7a4adc40596b4864e3785053345db271c312f31d` |
| 基线 | `origin/main` |

已有实现，本轮只验证并合入，不标 Done。

## 不改的行为

- 不接入假模型，不把 `usable` 改成 true，不用演示文案冒充真实可用文案。没有真实模型授权时草稿保持 `usable=false`。
- 不编造价格、地址、营业时间或营销宣称。过期价格、不确定地址或营业时间、无证据宣称只提示补充。
- 地点名不等于 POI 绑定。客户端的 `channel_mount` 不能单独把地点标成已挂载。
- 不给公共活动页加生成入口，不加 EcoTopNav，不自动发布，不发奖励，不扣生产客户的费。
- 选定版本不改活动状态。

## 本轮允许的改动

相对 `origin/main` 只修冲突和因此失败的测试。main 已占用迁移 `0010` 到 `0014`，文案表迁移顺延为 `0015_copy_drafts.sql`。`TestOpenCreatesSchema` 同时保留 main 已有表和 `copy_drafts`、`copy_versions`。

## 合入后的工单

Linear 保持 In Review。缺真实模型授权，不能当成活动页内一次真实可用文案。若 GitHub 把票自动改成 Done，改回 In Review 并写明该原因。
