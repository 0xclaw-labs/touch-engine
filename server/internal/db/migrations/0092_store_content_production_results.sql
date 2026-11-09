-- HUI-4595: 受信生产方成片回流的只追加登记(store_content_production_handoffs
-- 的结果事实面)。纪律:
--   - 仅受信生产服务(服务 token + content_return_producer 角色)可写入 HTTP
--     之外的事实;行内 tenant/store/pack 永远继承自 handoff 行(请求不可覆盖);
--   - source_app+event_id 去重(SHARED-BRIEF 事件语义):同键同负载摘要重放返回
--     同一行,同键不同负载拒绝(409 idempotency_conflict);
--   - result_revision 单调水位:旧稿迟到(revision ≤ 水位的新事件)被拒,
--     旧回流绝不覆盖新采用(采用指针只在 3553 采用事务内移动);
--   - 两端 SHA:declared(生产方申报)与 measured(服务器实测)都落行,
--     不符者在写入前即拒(hash_mismatch),不落半批事实;
--   - outcome=produced 才携带 version_id(经 3553 AddStoreReturnCandidate
--     唯一候选登记事务产出);failed/canceled 只记回执事实与原因。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_content_production_results (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    handoff_id       TEXT NOT NULL REFERENCES store_content_production_handoffs(id),
    source_app       TEXT NOT NULL,
    event_id         TEXT NOT NULL,
    outcome          TEXT NOT NULL DEFAULT 'produced'
                     CHECK (outcome IN ('produced','failed','canceled')),
    task_ref         TEXT NOT NULL DEFAULT '',
    result_revision  INTEGER NOT NULL CHECK (result_revision >= 1),
    charge_ref       TEXT NOT NULL DEFAULT '',
    version_id       TEXT NOT NULL DEFAULT '',
    payload_digest   TEXT NOT NULL DEFAULT '',
    declared_sha256  TEXT NOT NULL DEFAULT '',
    measured_sha256  TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    UNIQUE (source_app, event_id)
);

CREATE INDEX IF NOT EXISTS idx_store_cpr_handoff ON store_content_production_results(tenant_id, handoff_id, created_at);
