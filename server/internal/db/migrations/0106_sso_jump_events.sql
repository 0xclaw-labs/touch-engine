-- HUI-2222 (wave2 2026-11-10, M11): SSO 跳转审计事件。
-- 纪律(同 HUI-3546 action_session_events):
--   - 追加式审计:minted/redeemed/revoked 各留一行(拒绝尝试不留行:
--     拒绝多发生在凭据未知/过期/已用之后,无 grant 行可挂,写入会引入
--     无凭据引用的噪音;运营观测走服务端请求日志)。
--   - source + event_id 去重(UNIQUE),与全局事件契约同语义;
--   - 无外键:审计轨迹独立于行生命周期(行清理后证据仍在);
--   - 不含凭据明文/token 哈希——只留引用与目标契约 id。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sso_jump_events (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    principal_id     TEXT NOT NULL,
    grant_id         TEXT NOT NULL DEFAULT '',
    app_id           TEXT NOT NULL DEFAULT '',
    launch_target_id TEXT NOT NULL DEFAULT '',
    kind             TEXT NOT NULL CHECK (kind IN ('minted', 'redeemed', 'revoked')),
    source           TEXT NOT NULL DEFAULT 'touch-engine',
    event_id         TEXT NOT NULL,
    occurred_at      TEXT NOT NULL,
    UNIQUE (source, event_id)
);

CREATE INDEX IF NOT EXISTS idx_sso_jump_events_principal
    ON sso_jump_events(principal_id, occurred_at);
