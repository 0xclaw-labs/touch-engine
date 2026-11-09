-- HUI-4590: 门店面团购套餐目录（美团/大众点评/抖音）。
-- 与 HUI-1671 的 deal_offers（活动强绑的奖励供给目录）互不改写：
-- 本表是门店基础服务（无活动也须可用，约束 1），生命周期归门店面。
-- platform 固定三渠道；美团与点评即使底层同生态也分渠道存行（归因分渠道保留）。
-- product_url 保存侧强校验（https 公网 + 平台域名匹配 = 正确商品跳转），
-- 校验逻辑在 internal/deals，表只存事实。
-- price_source: platform(平台页实价) | merchant(商家手填)。
-- price_verified_at 是商家最近一次核对平台价格的校验时间（RFC3339 UTC）；
-- 超过 deals.PriceVerifiedTTL(72h) 或未填 → 顾客面不显示价格（「到平台查看」）。
-- authorization_ref 是外部券授权适配器的授权引用：缺授权的外部券只诚实
-- 未接通（not_connected / authorization_required），不产生任何本地核销事实。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_deal_offers (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    platform          TEXT NOT NULL CHECK (platform IN ('meituan', 'dianping', 'douyin')),
    provider_offer_id TEXT NOT NULL DEFAULT '',
    product_url       TEXT NOT NULL DEFAULT '',
    title             TEXT NOT NULL,
    image_ref         TEXT NOT NULL DEFAULT '',
    price_minor       INTEGER NOT NULL CHECK (price_minor >= 0),
    currency          TEXT NOT NULL DEFAULT '',
    price_source      TEXT NOT NULL DEFAULT 'merchant' CHECK (price_source IN ('platform', 'merchant')),
    price_verified_at TEXT NOT NULL DEFAULT '',
    valid_from        TEXT NOT NULL DEFAULT '',
    valid_until       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'retired')),
    authorization_ref TEXT NOT NULL DEFAULT '',
    note              TEXT NOT NULL DEFAULT '',
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

-- 同租户同平台同一平台商品 ID 只挂一个目录行（未填商品 ID 的行不受此限）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_store_deal_offers_platform_product
    ON store_deal_offers(tenant_id, platform, provider_offer_id)
    WHERE provider_offer_id <> '';

CREATE INDEX IF NOT EXISTS idx_store_deal_offers_tenant_status
    ON store_deal_offers(tenant_id, status);
