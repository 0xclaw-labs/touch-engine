-- HUI-3546 增量轮(2026-11-09, B08): 会话审计事件(action_sessions/events)。
-- 纪律:
--   - 追加式审计:创建/恢复/换选/凭据签发/兑换/撤销/升级/取消各留一行;
--   - source + event_id 去重(UNIQUE),与全局事件契约同语义;
--   - 无外键:审计轨迹独立于行生命周期(行清理后证据仍在);
--   - 不含凭据明文/访客键/手机号——只留引用与版本。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS action_session_events (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL,
    store_id             TEXT NOT NULL DEFAULT '',
    session_id           TEXT NOT NULL,
    action_id            TEXT NOT NULL DEFAULT '',
    action_revision      INTEGER NOT NULL DEFAULT 0,
    kind                 TEXT NOT NULL CHECK (kind IN (
        'created', 'resumed', 'content_changed', 'resume_credential_issued',
        'resume_credential_exchanged', 'resume_credential_revoked',
        'participation_attached', 'canceled')),
    source               TEXT NOT NULL DEFAULT 'touch-engine',
    event_id             TEXT NOT NULL,
    participation_id     TEXT NOT NULL DEFAULT '',
    credential_id        TEXT NOT NULL DEFAULT '',
    content_pack_version TEXT NOT NULL DEFAULT '',
    occurred_at          TEXT NOT NULL,
    UNIQUE (source, event_id)
);

CREATE INDEX IF NOT EXISTS idx_action_session_events_session
    ON action_session_events(session_id, occurred_at);
