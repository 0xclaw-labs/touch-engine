# HUI-2628 / HUI-4666 运营入口会话修复

日期：2026-11-15（UTC）  
基线：`9eff481d9ab4e5477c39c75f4d4baccda821de74`  
工作分支：`codex/touch-ops-session-20261115`

## 已确认的实际缺陷

1. `web/src/components/ops/ops-shell.tsx` 的原 `OpsGate` 把会话的 `choose`、`none`、`error` 均显示为未登录，匿名态也没有原有登录表单。两个有效组织且无组织记忆时，用户留在当前页无法完成选择，页面提示的 `/work` 不提供实际恢复路径。
2. 原 `commands` 只是一份未绑定会话的数组。旧 `access/effective` 成功响应可以在同组织换号后覆盖新账号的无权结果；进入新组织、等待新授权时也会沿用上一份成功结果重新挂载业务。
3. 六个 desk 分别再次调用 `useMerchantSession`。真实挂载时，每个 desk 首次以空 `x-tenant-id` 读取业务，且再次启动 memberships / whoami，竞争同一模块内的组织确认代次。
4. 授权服务 503 没有重试入口；网络异常产生未捕获的 Promise rejection；授权读取 401 后没有回到原身份确认流程的入口。

## 窄修范围

`OpsGate` 组合原有 `MerchantGate` 与新的内部 `OpsAccessGate`。登录、组织选择、空态、会话服务错误重试、SSO 账号变化仍由原来的 `MerchantGate` 和 `SessionContext` 处理。六个 desk 与 `OpsAccessGate` 全部消费 `useSession`，没有新增身份读取实现，也没有修改 `merchant-session.tsx`。

授权判定保存当前共享会话快照及重试代次。渲染时若判定不属于当前快照或代次，立即只显示授权加载态；会话离开 ready 时，原 `MerchantGate` 卸载授权门和业务子树。effect 清理同时使旧请求失效，因此旧成功、拒绝及网络错误不能覆盖新判定。没有用脱敏邮箱或只用 tenant ID 充当身份主键。

恢复行为：

| 权威响应 | 当前页面行为 | 恢复入口 |
| --- | --- | --- |
| 200 且有 `ops_permissions` 中的 `ops:` 命令 | 挂载运营视图 | 保持原六视图与后端逐命令门 |
| 200 无 ops grant、403、404 | 受限态，不挂载运营业务 | 重新核对授权 |
| 401 | 错误态，不挂载运营业务 | 重新核对会话，复用 memberships / refresh / 原登录表单 |
| 503 等外部服务错误、网络异常 | 错误态，不挂载运营业务 | 重试授权，复用当前已确认会话 |

六个 desk 的业务读写逻辑、六视图导航、命令标签、后端授权门、支持访问、原因必填、幂等键和 operation_id 反馈保持原实现。`org_owner` 不因此获得 ops 权限。

## 行为回归与红绿证据

测试文件：`web/tests/ops-session.test.ts`。

测试使用 ReactDOM 挂载真实 `OpsGate`、原有 `MerchantGate`、原登录表单和六个 desk；只在 fetch 与 Next 路由边界控制返回。没有模拟 `useSession` / `useMerchantSession`，也没有通过 grep 源码证明交互成立。

- 两个有效组织无记忆时显示原选择器；点击二店后，授权请求与组织记忆均对应二店，页面没有转向 `/work`。
- 匿名、无组织、会话服务失败分别展示登录表单、空态、可重试错误态。
- 同组织换号、同一脱敏邮箱条件下，新账号无权后旧 grant 晚到仍保持受限。
- 旧账号已授权，换号时先卸载旧业务；新组织确认完成但新 grant 仍挂起时，不复用旧授权。
- `org_owner` 在无 grant / 403 / 404 时不挂载真实商户 desk，也不发任何 `/api/ops/` 业务请求。
- 授权 503、网络断开可原页重试；401 恢复经原 memberships / refresh 路径回原登录表单。
- 六个 desk 逐个挂载，每页只有一轮 memberships、whoami、access/effective，所有业务请求携带已确认 tenant。

执行环境：本轮独立工作树，Node v24.22.1，npm 11.9.0，Vitest 2.1.8。测试数据是本轮受控工程夹具，不连接业务数据库。

| 阶段与时间（UTC） | 命令（在 web 目录） | 实际结果 |
| --- | --- | --- |
| 基线红测，21:52:26 | `npm test -- tests/ops-session.test.ts` | 初始 16 例：13 失败、3 通过；失败为真实页面/请求边界断言 |
| 补充恢复红测，21:53:06 | `npm test -- tests/ops-session.test.ts -t '授权服务网络断开\|授权会话已过期'` | 两例均失败；网络断开另有未捕获 rejection |
| 修复后行为回归，21:53:28 | `npm test -- tests/ops-session.test.ts` | 18 / 18 通过，无未捕获异常 |
| 完整前端回归，21:53:40 | `npm test` | 126 个测试文件、1230 个测试全部通过 |
| 同轮类型检查 | `npx tsc --noEmit` | exit 0 |

新增且仅新增测试开发依赖 `jsdom@26.1.0`，版本在 package.json 精确固定，传递依赖写入 package-lock.json。原环境只有 Node / SSR 测试，不能实际运行 effect、DOM 事件和账号切换时序；此依赖用于最小真实组件行为回归。没有新增运行时依赖。

## 验收边界

本报告证明本轮工程组件回归及类型检查成立。未自动输入浏览器登录凭据，未将单测称为浏览器验收，未做正式 Identity、生产 ops grant 或原数据库复验。

生产部署、正式账号联调及真实浏览器操作验收仍需在具有正式身份和授权的环境继续。此修复没有写生产配置、注册目标、改动真实付款或对外发送消息，也没有更新 Linear 或 PR。集成由主工作树审查后进行，独立复审与本报告分开记账。
