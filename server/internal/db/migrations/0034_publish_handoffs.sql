-- HUI-1670: 顾客发布交接(真实渠道交接与恢复)——交接主表。
-- Additive only:既有表零改动。纪律(design spec Q2/Q8/Q9):
--   - 一次交接一行;幂等键租户内唯一,同键重放回原行,不新建交接;
--   - 状态九态: previewed|handoff_preparing|handoff_ready|editor_opened|
--     submitting|awaiting_review|platform_confirmed|canceled|failed|unknown
--     (无单一 success/published 布尔;platform_confirmed 只能由验签通过的
--     官方回执到达);
--   - evidence_source 必填: 每次状态落库都回答"谁证明的"
--     (customer_action|staff_observation|official_receipt|system_timeout|
--      system_recovery|system_adapter);无来源的状态迁移在应用层拒绝;
--   - attempt_id 引用 3546 冻结语义(handed_off);本表不写参与表;
--   - query_count/timed_out 支撑"超时先回查,不盲目重投";
--   - visitor_id 是首方续接键,非手机号/非平台 open_id。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS publish_handoffs (
    id                       TEXT PRIMARY KEY,
    tenant_id                TEXT NOT NULL REFERENCES tenants(id),
    store_id                 TEXT NOT NULL REFERENCES stores(id),
    campaign_id              TEXT NOT NULL REFERENCES campaigns(id),
    link_code                TEXT NOT NULL DEFAULT '',
    participation_id         TEXT NOT NULL REFERENCES participations(id),
    attempt_id               TEXT NOT NULL REFERENCES participation_attempts(id),
    platform                 TEXT NOT NULL,
    entry_mode               TEXT NOT NULL,
    scenario                 TEXT NOT NULL,
    idempotency_key          TEXT NOT NULL,
    status                   TEXT NOT NULL CHECK (status IN
        ('previewed','handoff_preparing','handoff_ready','editor_opened',
         'submitting','awaiting_review','platform_confirmed','canceled',
         'failed','unknown')),
    evidence_source          TEXT NOT NULL CHECK (evidence_source != ''),
    content_version          TEXT NOT NULL DEFAULT '',
    content_pack_version_id  TEXT NOT NULL DEFAULT '',
    visitor_id               TEXT NOT NULL DEFAULT '',
    query_count              INTEGER NOT NULL DEFAULT 0,
    timed_out                INTEGER NOT NULL DEFAULT 0,
    cancel_reason            TEXT NOT NULL DEFAULT '',
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ph_idem
    ON publish_handoffs(tenant_id, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_ph_participation
    ON publish_handoffs(tenant_id, participation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ph_attempt
    ON publish_handoffs(tenant_id, attempt_id);
