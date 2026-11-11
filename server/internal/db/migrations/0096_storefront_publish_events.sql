-- HUI-1676 (0096): 门店首页发布/回滚事件审计 storefront_publish_events。
-- Append-only: 发布与回滚各落一条(含此前指针→此后指针、确认的变更摘要),
-- 永不改写删除;版本表行是"状态",本表是"动作史"。回滚也是一次发布事件
-- (追加,不复活已撤销内容的否决在应用层,这里只留痕)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS storefront_publish_events (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL,
    store_id          TEXT NOT NULL,
    action            TEXT NOT NULL CHECK (action IN ('publish','rollback')),
    revision          INTEGER NOT NULL CHECK (revision >= 1),
    previous_revision INTEGER NOT NULL DEFAULT 0 CHECK (previous_revision >= 0),
    actor             TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '[]',
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_storefront_publish_events_store
    ON storefront_publish_events(store_id, created_at);
