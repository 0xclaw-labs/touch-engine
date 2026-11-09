-- HUI-3553 (TOUCH-ALL-20261109): 门店根内容包(内容归 tenant/store,campaign 可选)。
-- Additive only:既有表零改动,不重编号。纪律:
--   - 门店是根:无活动门店也能组织可采用内容;campaign_id 可空,有活动时
--     仅作归属引用(不为无活动门店强建活动,不复制 CMS);
--   - 旧 campaign 内容包(0028)保留不动:既有活动端点继续读写旧表;「已有
--     活动包 ↔ 门店域」的映射是查询适配(ListStoreContentPacks 把本店活动
--     包经 campaigns.store_id 一并返回并带 legacy 标记),数据不搬运、
--     正在进行的会话快照零修改;
--   - 租户内 (tenant_id, store_id, title) 唯一。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_content_packs (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    store_id    TEXT NOT NULL REFERENCES stores(id),
    campaign_id TEXT REFERENCES campaigns(id),
    title       TEXT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (tenant_id, store_id, title)
);

CREATE INDEX IF NOT EXISTS idx_store_content_packs_store ON store_content_packs(tenant_id, store_id, created_at);
CREATE INDEX IF NOT EXISTS idx_store_content_packs_campaign ON store_content_packs(tenant_id, campaign_id) WHERE campaign_id IS NOT NULL;
