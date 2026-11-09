-- HUI-3553 (TOUCH-ALL-20261109): 动作内容绑定(服务器侧采用引用索引)。
-- Additive only:既有表零改动。纪律:
--   - content_binding_ref 的写端点(actions PUT)归 HUI-4584;本表是内容域的
--     服务器权威索引:只能由「采用后建立绑定」的内部调用(本票 exported
--     store 方法)写入——版本必须已明确采用才允许建绑定(首次制作链:空包/
--     候选阶段无绑定,公开内容读面 404 语义);
--   - (tenant_id, action_id) 唯一:action_id 是稳定实例 ID,重新采用+重绑 =
--     覆盖行(旧引用不受影响:旧会话读已冻结版本行);
--   - last_version_id 只作最近一次绑定引用记录,公开读面按「包的当前采用
--     指针」取真实内容,避免绑定行滞后于指针。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_action_content_bindings (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    store_id        TEXT NOT NULL REFERENCES stores(id),
    action_id       TEXT NOT NULL,
    pack_id         TEXT NOT NULL REFERENCES store_content_packs(id),
    last_version_id TEXT NOT NULL REFERENCES store_content_pack_versions(id),
    action_revision INTEGER NOT NULL DEFAULT 0,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (tenant_id, action_id)
);

CREATE INDEX IF NOT EXISTS idx_sacb_store ON store_action_content_bindings(tenant_id, store_id);
