-- HUI-3553: 活动×平台的当前采用版本指针。
-- Additive only:既有表零改动。纪律:
--   - 每行 = 某活动在某平台的「当前采用版」:显式采用(adopt 端点)才写;
--     换版 = 覆盖指针,旧版本内容行零改动(历史在 content_pack_version_events);
--   - (campaign_id, platform) 唯一:顾客侧快照读端点按此返回唯一当前版,
--     采用后顾客返回同一版本,不随机刷新换片;
--   - version_id 不设外键级联:版本行不可变永不删除,指针行保持可追溯。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS content_pack_adoptions (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    platform    TEXT NOT NULL,
    version_id  TEXT NOT NULL REFERENCES content_pack_versions(id),
    adopted_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, platform)
);

CREATE INDEX IF NOT EXISTS idx_cpa_campaign ON content_pack_adoptions(tenant_id, campaign_id, platform);
