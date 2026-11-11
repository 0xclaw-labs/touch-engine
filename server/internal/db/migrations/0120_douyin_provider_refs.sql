-- HUI-4585 抖音视频适配:provider_ref 持久化(0120)。
--
-- 票面:回执关联使用 share_id→内部 handoff 的服务器映射;按官方机制验源、
-- 验签/查询。本表保存 share_id → 平台侧产物(item_id/photo_id 等可观察引用)
-- 与最近一次回查的照录状态(不确认——确认只走验签回执管线)。
--
-- handoff_id 唯一:一个交接一条 provider 引用;查询结果词表原样保存
-- (state=平台语义词,本表不翻译成确认)。

CREATE TABLE douyin_provider_refs (
    id              TEXT PRIMARY KEY,                -- phdpr_* (adapter 铸造)
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    handoff_id      TEXT NOT NULL UNIQUE,
    share_id        TEXT NOT NULL DEFAULT '',
    provider_item   TEXT NOT NULL DEFAULT '',        -- 平台侧内容引用(回执/查询出现时照录)
    state           TEXT NOT NULL DEFAULT '',        -- 最近一次平台侧观察词(照录,不确认)
    last_checked_at TEXT NOT NULL DEFAULT '',
    detail_json     TEXT NOT NULL DEFAULT '{}',      -- 最近一次查询的原始可观察字段
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX idx_douyin_provider_refs_tenant ON douyin_provider_refs(tenant_id, updated_at);
