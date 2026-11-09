-- HUI-4589 证据分层：顾客行为事件只存 open/jump_attempt/copy 三种——
-- 「已关注/已连接/已添加/已点评」这类无官方回执的成功语义在 CHECK 之外，
-- 结构上写不进去（裁定书：一切无官方回执的「已关注/已连接/已添加」绝不记成功）。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_service_events (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    store_id      TEXT NOT NULL,
    service_id    TEXT NOT NULL,
    event_kind    TEXT NOT NULL CHECK (event_kind IN ('open','jump_attempt','copy')),
    environment   TEXT NOT NULL DEFAULT '',
    evidence_tier TEXT NOT NULL CHECK (evidence_tier IN ('click','jump_attempt')),
    occurred_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_store_service_events_store ON store_service_events(store_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_store_service_events_service ON store_service_events(service_id);
