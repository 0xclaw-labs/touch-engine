# 2026-11-15 运营台会话独立对抗复审

受审：Touch `4316b8aa7026bdae9067c28007c000f99172ec6f`，基线 `9eff481d9ab4e5477c39c75f4d4baccda821de74`。**会话门和旧授权隔离的定向行为测试通过，但发现一条既有多步写入路径在换号后仍可发请求；不宜声称所有迟到动作已隔离。** 以下为 React/jsdom 组件与受控 fetch 边界测试及代码分析，不是正常浏览器或正式身份验收。

## 已确认：迟到读取可在换号后触发关闭支持访问

在真实 `OpsGate`、`MerchantGate`、`TenantsDesk` 组件中，按以下顺序控制响应：

1. A 已获运营授权，打开目标商户详情，点击“结束支持访问”；暂停其 `GET /api/ops/support-access` 响应。
2. 模拟同一租户切换到 B，B 无运营 grant；观察旧业务卸载且界面已进入受限态。
3. 返回 A 发起的旧 GET，包含目标商户一条未关闭记录；尽管组件已卸载，`SupportAccessForm.closeLast` 继续调用 `POST /api/ops/support-access/grant-a/close`。

独立测试期望换号后 POST 数为 0，实测为 1。命令：

```bash
cd /workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/resume-ops/web
cp /tmp/terra-ops-adversarial.test.ts tests/terra-ops-adversarial.test.ts
npm test -- tests/terra-ops-adversarial.test.ts -t 'Terra:'
rm tests/terra-ops-adversarial.test.ts
```

实测退出 1：`expected 1 to be +0`；测试仅使用模拟 fetch，未向业务数据库写入。红测完整源码保留在 `/tmp/terra-ops-adversarial.test.ts`。原路径是 `web/src/components/ops/tenants-desk.tsx` 的 `closeLast`，GET 后没有核对当前会话代次或组件存活。该路径本身早于本提交；本提交的会话门可以卸载旧视图，但卸载不会取消已运行异步函数。

后端关闭端点仍要求当前主体具有 `ops:support.manage`：测试中的无权 B 应被服务端拒绝；若 B 也有该权限，浏览器共享 cookie 换号后旧 A 意图可能随 B 会话发送并由后端按 B 的权限处理。该后果是代码路径推断，**本测试未证明真实后端执行成功或越权**。建议在多阶段动作的每个等待后确认会话身份代次和组件存活，旧意图失效后不发后续写请求；已发出的写请求仍由服务端按实际身份和权限裁决。

另外，`ListSupportAccess` 返回所有运营人的记录，当前 `closeLast` 仅匹配目标租户且未关闭，未按当前主体或到期时间筛选，与“当前主体最近的活跃授权”注释不符；因此可能选中其他运营人或已过期记录。这是既有代码的静态发现，需要按预期语义单独验证；不能归因于本提交。

六个 desk 的其他明确 await 链中，未发现第二处先读后自动写：其余 `postOperation` 和打开支持访问均直接发用户触发的首个 POST，成功后可能再发 GET 刷新。卸载后刷新仍可能继续发旧上下文读取，但不能把它说成第二项已证实写入。

## 本次修复已验证的范围

独立运行 `npm test -- tests/ops-session.test.ts`：18 项通过、退出 0。该测试挂载真实会话门和各 desk，在 fetch 边界提供工程响应，覆盖：

- 两组织无记忆时显示选择器，选定后以该组织核查；匿名、无组织、错误态各有正常恢复路径。
- 同租户换号时旧 grant 迟到不覆盖新账号无权结果；等待新授权时不渲染旧业务。
- 403/404/无 grant 不挂载运营业务；401、503、网络失败各有核对或重试路径。
- 六页首次业务请求使用已确认的同一租户，并各只读取一轮 memberships、whoami 和 access/effective。

代码上 `OpsAccessGate` 的结果携带会话对象和重试代次，render 时先比较，effect 清理丢弃旧授权响应；六页改为复用 `SessionContext`，避免各自创建会话实例。测试不能替代跨标签真实浏览器、旧业务请求的所有排列、正式 Identity 或正式 ops grant。服务端运营路由仍需逐命令授权；导航显示本身不构成访问许可。

## c68fda1 复验

受测 canonical `c68fda12306668aaf699f3ee2b0383ebce0d1120`。原流程仍由用户在目标商户详情点击“结束支持访问”，先请求该商户原详情，再从服务端按当前主体、目标商户及有效期计算的 `business_data.support_access_ref` 取得目标；不再从全平台列表猜选。新 `mounted` 与 `currentTenantOp` 守卫使旧详情 GET 迟到时不能继续发 close POST，目标商户变化时表单重新挂载；三个组件回归分别覆盖换无权账号、换有权账号及换目标。POST 携带 `own_access_only: true`，服务端对执行时实际主体再次比对。

独立执行 `npm test -- tests/ops-session.test.ts`：24 项通过；`go test ./internal/httpapi ./internal/store -run TestOps -count=1`：两包通过。其中真实 HTTP 测试证明两个账号均有 ops 权限时，B 不能以 own-only 关闭 A 授权（403、记录未改），A 可以关闭。另用独立 overlay `/tmp/terra-ops-compat-test.go`、`/tmp/terra-ops-compat-overlay.json` 实际运行 `go test -overlay /tmp/terra-ops-compat-overlay.json ./internal/httpapi -run '^TestTerraOpsExplicitManagerCloseRemainsCompatible$' -count=1 -v`：通过；B 不传 own-only 时作为显式平台管理操作关闭 A 的授权，HTTP 200 且落库 closed_by 为 B，保留原管理契约。

**新发现的恢复反馈问题**：若核对支持访问的详情 GET 首次 503，页面显示“没有读到当前支持访问，请重试”；原按钮再次操作，详情读取恢复、close POST 返回 200 后，旧错误仍在页面。`TenantsDesk` 的 `onError` 设置父级 `opError`，重试及 `onDone` 未清除它。独立 `React/jsdom` 红测 `/tmp/terra-ops-retry.test.ts` 可复制到 `web/tests/terra-ops-retry.test.ts` 后运行 `npm test -- tests/terra-ops-retry.test.ts -t 'Terra retry'`，实测关闭请求发送一次而旧错误文本仍存在。该测试为受控 fetch，不证明真实后端关闭结果；它暴露的是用户反馈不更新。网络异常时当前组件会提示“没有确认关闭结果，请刷新商户详情核对后重试”，没有将异常说成成功；进一步回归应覆盖失败后重试、关闭结果未知以及成功后的旧错误清除与可见确认。

此次结果表明原迟到 GET→POST 和错选其他主体问题在对应工程测试范围内已修复，保留上方原发现与红测记录；新的恢复反馈红例尚待修复。尚无正常浏览器、正式 Identity 或真实 ops grant 验收。

## 47ecebb 恢复反馈复验

受测 canonical `47ecebb9a4dab875556f0b3a6bac7e53fa3cb463`。将此前保存的独立红例 `/tmp/terra-ops-retry.test.ts` 原样复制到 Web 测试目录，运行 `npm test -- tests/terra-ops-retry.test.ts -t 'Terra retry'`：目标 1 项通过，另外 24 项按名称过滤跳过，退出 0；临时副本已删除。该例复用原流程：详情读取 503 后重试、模拟关闭返回成功，旧错误不再残留。此结果关闭本报告的恢复反馈工程红例；测试为 React/jsdom 与受控 fetch，不是正常浏览器或正式服务验证。
