-- HUI-4594: 线索接收回执(持久化 ACK)。纪律:
--   - Notify 投递成功(delivered)≠落库。只有受信 Leads 接收方在入站与领域存储
--     事务完成后回 ACK(receipt_id/source_event_id/submission_id/source_revision/
--     receiver_state 五元组齐),同步才结束(crm_received);
--   - receipt_id 由接收方铸造,作主键:同一回执重放幂等,同键不同负载被拒
--     (SHARED-BRIEF 幂等语义: 同幂等键异负载拒绝);
--   - receiver_state 是接收方申报的事实,存列于 lead_submissions 供商家面/1677
--     统计消费(待同步不算已获客);
--   - 本表是回执账本,不是第二线索库:无任何联系方式列。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS lead_receipts (
    id              TEXT PRIMARY KEY, -- receipt_id (接收方铸造)
    tenant_id       TEXT NOT NULL,
    submission_ref  TEXT NOT NULL,
    source_event_id TEXT NOT NULL,    -- 本侧已发事实: outbox event_id 或 notify_event_id
    source_revision INTEGER NOT NULL,
    receiver_state  TEXT NOT NULL CHECK (receiver_state IN ('stored','revoked')),
    receiver_id     TEXT NOT NULL DEFAULT '', -- TOUCH_SERVICE_TOKENS 注册的服务身份
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_lead_receipts_ref ON lead_receipts(submission_ref);

ALTER TABLE lead_submissions ADD COLUMN receiver_state TEXT NOT NULL DEFAULT '';
ALTER TABLE lead_submissions ADD COLUMN receipt_id    TEXT NOT NULL DEFAULT '';
