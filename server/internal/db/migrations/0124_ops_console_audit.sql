-- HUI-4592:平台运营审计(append-only)与商户行乐观并发 revision。
-- 纪律:
--   - append-only:只有 INSERT,无 UPDATE/DELETE 面;撤销/纠正本身也是新行;
--   - 审计三件套硬性:操作者(actor_ref+actor_role)/发生时间(occurred_at)/
--     原因(reason,ops 写面强制必填);before/after 用 JSON 文本快照,
--     字面量 "null" = 新建无前值(与 {} = 前值空集 相区分,同 0065 语义);
--   - authority_source 记录权限来源(ops_grant 引用 / support_access 引用),
--     审计不可由调用方伪造 actor 或结果——两列由服务端从已验证会话解析;
--   - operation_id 关联统一操作信封(0126),高权限操作结果可按其追踪;
--   - tenants.ops_revision:商户 lifecycle/charge_hold 变更单语句
--     revision 守卫(WHERE ops_revision=?),防止过期页面覆盖新状态。

CREATE TABLE IF NOT EXISTS ops_audit_log (
    id               TEXT PRIMARY KEY,
    occurred_at      TEXT NOT NULL,
    actor_ref        TEXT NOT NULL,
    actor_role       TEXT NOT NULL DEFAULT '',
    authority_source TEXT NOT NULL DEFAULT '',
    command          TEXT NOT NULL DEFAULT '',
    action           TEXT NOT NULL,
    target_type      TEXT NOT NULL,
    target_id        TEXT NOT NULL,
    tenant_id        TEXT NOT NULL DEFAULT '',
    before_json      TEXT NOT NULL DEFAULT 'null',
    after_json       TEXT NOT NULL DEFAULT 'null',
    reason           TEXT NOT NULL DEFAULT '',
    operation_id     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ops_audit_time ON ops_audit_log(occurred_at);
CREATE INDEX IF NOT EXISTS idx_ops_audit_tenant ON ops_audit_log(tenant_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_ops_audit_target ON ops_audit_log(target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_ops_audit_operation ON ops_audit_log(operation_id);

ALTER TABLE tenants ADD COLUMN ops_revision INTEGER NOT NULL DEFAULT 1;
