-- HUI-1665 增量(2026-10-14): 触点审计事件 —— 启用/换绑/停用/部署五态推进留痕。
-- 纪律:
--   - 只追加不修改;detail 是操作员备注/机器可读原因,不含凭据或顾客数据。
--   - 换绑审计同事务落行(谁在何时把哪个目标改成哪个);部署五态每步留痕,
--     供 HUI-1663 真机验收与运营回溯。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS touchpoint_events (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    alias_id   TEXT NOT NULL REFERENCES touchpoint_aliases(id),
    kind       TEXT NOT NULL CHECK (kind IN ('created','rebind','status_change','stage_change')),
    detail     TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_touchpoint_events_alias ON touchpoint_events(alias_id, created_at);
