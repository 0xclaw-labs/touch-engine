-- HUI-3546 增量轮(2026-11-09, B08): 匿名动作会话权威。
-- 纪律:
--   - action_sessions 是动作维度:campaign 可空(本表不存 campaign 列,
--     无活动/活动结束照建会话,绝不伪造活动 id);只有显式办理权益才经
--     AttachParticipation 升级建 participation(既有表语义不变);
--   - 创建时冻结快照:storefront_revision + action_revision +
--     rule_version/rule_snapshot + 内容引用;停用/撤权是独立实时检查
--     (域层经 ActionDirectory 端口复核),快照不掩盖当前状态;
--   - visitor_id 是首方随机 cookie 续接键(同 touch_participation_visitor),
--     非手机号、非平台 open_id;会话行访客绑定是唯一访问凭据,
--     顺序 id/guess 无探测面(域层统一 404);
--   - rule_snapshot 是服务器冻结的规则快照原文,不出公共载荷;
--     客户端提交的 rule_version 只是期望值,不能成为权威规则;
--   - 不建生态账号:本表零身份字段(无 phone/open_id/unionid)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS action_sessions (
    id                        TEXT PRIMARY KEY,
    tenant_id                 TEXT NOT NULL REFERENCES tenants(id),
    store_id                  TEXT NOT NULL REFERENCES stores(id),
    action_id                 TEXT NOT NULL,
    catalog_code              TEXT NOT NULL DEFAULT '',
    platform                  TEXT NOT NULL DEFAULT '',
    kind                      TEXT NOT NULL DEFAULT '',
    storefront_revision       INTEGER NOT NULL DEFAULT 0,
    action_revision           INTEGER NOT NULL DEFAULT 0,
    rule_version              INTEGER NOT NULL DEFAULT 0,
    rule_snapshot             TEXT NOT NULL DEFAULT '',
    content_pack_version      TEXT NOT NULL DEFAULT '',
    visitor_id                TEXT NOT NULL,
    participation_id          TEXT NOT NULL DEFAULT '',
    status                    TEXT NOT NULL CHECK (status IN ('active', 'ended')),
    -- 幂等键可空(NULL = 无键创建):SQLite UNIQUE 下多个 NULL 互不冲突,
    -- 同键重复才被拒;空串会令不同访客的无键创建互相碰撞,故不用 '' 哨兵。
    idempotency_key           TEXT,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    UNIQUE (tenant_id, action_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_action_sessions_visitor
    ON action_sessions(tenant_id, action_id, visitor_id, status);
CREATE INDEX IF NOT EXISTS idx_action_sessions_participation
    ON action_sessions(participation_id) WHERE participation_id != '';
