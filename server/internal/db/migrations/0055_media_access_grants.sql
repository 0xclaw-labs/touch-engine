-- HUI-3553 (TOUCH-ALL-20261109): media-access 签发审计(只追加)。
-- Additive only:既有表零改动。纪律:
--   - 每次受控媒体凭据签发落一行:版本/媒体/用途/会话引用/TTL 到期时间;
--   - session_ref 是请求带来的动作会话引用(审计用途;会话权威归 HUI-3546,
--     引用不是授权源——授权=版本已采用且可分发+媒体属于该版本);
--   - 永不改写删除:续签/重试各成一行,审计可重放。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS media_access_grants (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    version_id       TEXT NOT NULL,
    media_id         TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    session_ref      TEXT NOT NULL DEFAULT '',
    expires_at       TEXT NOT NULL,
    created_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mag_version ON media_access_grants(tenant_id, version_id, created_at);
