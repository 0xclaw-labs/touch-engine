-- HUI-3553: 活动内容包(活动归属、商家、标题)。
-- Additive only:既有表零改动。纪律:
--   - 内容包是对「可采用版本」的组织单元:包本身只做归属与命名,内容全部在
--     content_pack_versions(0029);文件零进本库,版本行引用 lib_assets 既有行
--     (HUI-1666 基线)并冻结 (asset_id, asset_ref, sha256) 快照;
--   - 包按活动归属:同一活动可建多个包(不同主题供顾客挑选,少量真实差异,
--     不做规避平台风控的伪差异批量广告);租户内 (campaign_id, title) 唯一。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS content_packs (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL REFERENCES campaigns(id),
    store_id    TEXT REFERENCES stores(id),
    title       TEXT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, title)
);

CREATE INDEX IF NOT EXISTS idx_content_packs_campaign ON content_packs(tenant_id, campaign_id, created_at);
