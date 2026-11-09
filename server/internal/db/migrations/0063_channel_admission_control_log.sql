-- HUI-3535: 受控变更审计。平台端更新（受控记录端点, internal token 管理员
-- 权限）必留痕：操作者/原因/动作/载荷指纹（票面 F20：来源、操作者、时间、
-- 原因）。迁移种子（0061/0062）是版本化发布初值，不经本表。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS channel_admission_control_log (
    id            TEXT PRIMARY KEY,
    capability_id TEXT NOT NULL,
    catalog_code  TEXT NOT NULL,
    platform      TEXT NOT NULL,
    method        TEXT NOT NULL,
    scenario      TEXT NOT NULL,
    tenant_scope  TEXT NOT NULL,
    action        TEXT NOT NULL CHECK (action IN
        ('create','update','emergency_disable','emergency_enable')),
    actor         TEXT NOT NULL,
    reason        TEXT NOT NULL,
    payload_hash  TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_channel_admission_control_log_code
    ON channel_admission_control_log(catalog_code, created_at);
