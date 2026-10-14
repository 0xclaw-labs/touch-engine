-- HUI-3546 碰一碰门店交付 A2: 受控交接凭据(H5→平台、平台→店员)。
-- 纪律:
--   - 短时(默认 10 分钟)、单用途(used_at 事务置位防重放)、可撤销(revoked_at);
--   - 防枚举:token 为 128-bit 随机 + 校验位,库内只存 SHA-256 全 token 哈希,
--     明文永不落库、永不进日志;
--   - 码 ≠ 发奖授权:凭据只绑定参与身份,不触发任何发放;
--   - target_scope 绑定目标(app/租户作用域),兑换时目标不符必须拒绝;
--   - 不放长期 token、电话、平台密钥。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS handoff_credentials (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    participation_id TEXT NOT NULL REFERENCES participations(id),
    attempt_id       TEXT NOT NULL DEFAULT '',
    purpose          TEXT NOT NULL CHECK (purpose IN ('customer_resume', 'staff_verify')),
    target_scope     TEXT NOT NULL DEFAULT '',
    token_hash       TEXT NOT NULL UNIQUE,
    expires_at       TEXT NOT NULL,
    used_at          TEXT NOT NULL DEFAULT '',
    revoked_at       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_handoff_credentials_participation
    ON handoff_credentials(participation_id, created_at);
