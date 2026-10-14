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

-- 制作交接 brief:「帮我制作」入口收集的活动目的/门店资料/选定素材/期望形式。
-- 纪律:本票未接真实制作引擎 —— brief 只落库去重(同一交接重试不新建制作
-- 工程:幂等键非空时租户内唯一,同键重放=返回既有行),引擎接线与扣费归
-- HUI-1745 与后续联调;brief 行不含文件字节,素材以 lib_assets 引用表达。
CREATE TABLE IF NOT EXISTS content_handoffs (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    campaign_id     TEXT NOT NULL REFERENCES campaigns(id),
    purpose         TEXT NOT NULL,
    store_brief     TEXT NOT NULL DEFAULT '',
    desired_form    TEXT NOT NULL DEFAULT '',
    asset_ids       TEXT NOT NULL DEFAULT '[]',
    idempotency_key TEXT,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_content_handoffs_idem ON content_handoffs(tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_content_handoffs_campaign ON content_handoffs(tenant_id, campaign_id, created_at);
