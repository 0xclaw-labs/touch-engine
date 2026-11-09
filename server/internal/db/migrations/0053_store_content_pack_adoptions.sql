-- HUI-3553 (TOUCH-ALL-20261109): 门店×平台当前采用版本指针(采用权威)。
-- Additive only:既有表零改动。纪律(同 0030 词汇):
--   - 每行 = 某门店在某平台的「当前采用版」;显式采用端点才写,换版覆盖指针,
--     旧版本内容行零改动(历史在 store_content_pack_version_events);
--   - 采用动作记录操作者(adopted_by)与时间(created_at/updated_at);
--   - (tenant, store, platform) 唯一:公开读面按服务器指针返回唯一当前版,
--     顾客返回同一版本,不随机刷新换片;
--   - version_id 无外键级联:版本行不可变永不删除,指针行保持可追溯;
--   - 旧 campaign 采用(0030)保留不动:活动包采用继续走旧指针;本表只管
--     门店根内容包。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_content_pack_adoptions (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    store_id    TEXT NOT NULL REFERENCES stores(id),
    campaign_id TEXT REFERENCES campaigns(id),
    platform    TEXT NOT NULL,
    pack_id     TEXT NOT NULL REFERENCES store_content_packs(id),
    version_id  TEXT NOT NULL REFERENCES store_content_pack_versions(id),
    adopted_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (tenant_id, store_id, platform)
);

CREATE INDEX IF NOT EXISTS idx_scpa_store ON store_content_pack_adoptions(tenant_id, store_id, platform);
CREATE INDEX IF NOT EXISTS idx_scpa_pack ON store_content_pack_adoptions(tenant_id, pack_id);
