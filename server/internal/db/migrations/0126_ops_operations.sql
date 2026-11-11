-- HUI-4592:统一操作信封(POST /api/v1/ops/operations 的可重试操作记录)。
-- 纪律:
--   - idempotency_key UNIQUE:重试查询同一操作,不盲目重复执行;
--     同键异负载拒绝(应用层按 canonical 负载判等 → 409 IDEMPOTENCY_CONFLICT);
--   - state:running(已受理未完成)|succeeded|failed|rejected(白名单外/
--     权限不足/stale revision——拒绝也是记录,不静默丢弃);
--   - action 必须在冻结白名单内(ops 域包裁决),resource_ref 定位目标;
--   - result_json 保存权威结果引用/快照;expected_revision 保存请求声明,
--     供事后审计对照;
--   - actor 由已验证会话解析,永不取自请求体。

CREATE TABLE IF NOT EXISTS ops_operations (
    id                TEXT PRIMARY KEY,
    idempotency_key   TEXT NOT NULL UNIQUE,
    action            TEXT NOT NULL,
    resource_ref      TEXT NOT NULL,
    tenant_id         TEXT NOT NULL DEFAULT '',
    expected_revision TEXT NOT NULL DEFAULT '',
    reason            TEXT NOT NULL,
    actor_ref         TEXT NOT NULL,
    actor_role        TEXT NOT NULL DEFAULT '',
    authority_source  TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'running',
    error_code        TEXT NOT NULL DEFAULT '',
    result_json       TEXT NOT NULL DEFAULT 'null',
    created_at        TEXT NOT NULL,
    finished_at       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ops_operations_time ON ops_operations(created_at);
CREATE INDEX IF NOT EXISTS idx_ops_operations_actor ON ops_operations(actor_ref, created_at);
