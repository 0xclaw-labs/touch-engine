# 停止分发禁令保留修复

## 真实发现

Terra 在同一原门店、原内容包采用 douyin v4 / wechat v5 时，原先由 UI 停止分发的 v2/v3 被改为 retired，blocked_reason 仍为原停止原因。原库保留在 `/tmp/touch-e2e-9ceKxD/touch.db`，待启动正式迁移并沿原记录复验。

## 修复

root 本地提交 `89d4acd`。新旧两个内容域的采用在一个事务内重读分发条件、更新指针、记录版本状态与事件；被停止分发或仍有停止原因的旧版本不能因采用新版而退化为普通 retired。被停止版本不可再采用。事件持久化失败时，原指针和两边状态完整回滚。

迁移 `0137_preserve_content_distribution_blocks.sql` 只依据原有非空停止原因将受影响状态纠正为 blocked，正文、媒体、指针、历史事件不改；正常 retired 版本不受影响。没有新增解除禁令操作。

## 证据与边界

- `logs/root-content-block-red.log`：两个域均真实失败，采用新版覆盖旧禁令。
- `logs/root-content-block-final.log`：store 与 HTTP 相关回归通过；历史错态迁移、拒绝重采、失败回滚均验证。
- `logs/root-content-block-http.log`：真实 HTTP 原版本取材凭证、停止、再采用新版后旧签名 URL 和原会话取材均拒绝；合法旧 retired 会话此前仍可取材。
- 第一轮 `root-content-block-green.log` 的 HTTP 过滤名未匹配，未作为 HTTP 通过证据；后续使用精确测试名补验通过。
- 尚待 Terra 在同一原库、原页面、原版本完成新候选浏览器复验；本报告不等于正式平台发布成功。
