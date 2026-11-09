-- HUI-1671: 已发权益的顾客展示码(短时,可重签)。
-- 持有效访客会话者可对自身有效 grant 重签 staff_verify 展示码;码只定位查验,
-- 不能代替店员后台权限,更不能换取顾客身份(店员 lookup 不回 visitor)。
-- 只存哈希:明文只在铸造响应里出现一次,不进日志。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS reward_display_codes (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    benefit_id         TEXT NOT NULL REFERENCES reward_benefits(id),
    token_hash         TEXT NOT NULL UNIQUE,
    purpose            TEXT NOT NULL DEFAULT 'staff_verify_display'
                       CHECK (purpose = 'staff_verify_display'),
    created_by_visitor TEXT NOT NULL,
    expires_at         TEXT NOT NULL,
    used_at            TEXT NOT NULL DEFAULT '',
    revoked_at         TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reward_display_codes_benefit
    ON reward_display_codes(tenant_id, benefit_id, expires_at);
