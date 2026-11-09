-- HUI-1671 B09 前半: 优惠供给目录,区分外部平台团购与本地门店券。
-- kind: external_product(美团/点评/抖音团购等外部平台商品) 与
--       merchant_benefit(本地门店券/赠品/折扣)。
-- issuer 记发行方(meituan/dianping/douyin/merchant/...);外部券的核销事实
-- 只能由授权适配器返回,本地账无权核销外部券(HUI-4590 提供外部适配)。
-- authorization_ref 是适配器授权引用:缺授权不允许生成任何"平台券"事实。
-- 面额是营销面值,不是成本,不与订单款/AI 消费混账。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS deal_offers (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    campaign_id       TEXT NOT NULL REFERENCES campaigns(id),
    kind              TEXT NOT NULL CHECK (kind IN ('external_product', 'merchant_benefit')),
    issuer            TEXT NOT NULL CHECK (length(issuer) > 0),
    provider          TEXT NOT NULL DEFAULT '',
    provider_offer_id TEXT NOT NULL DEFAULT '',
    authorization_ref TEXT NOT NULL DEFAULT '',
    title             TEXT NOT NULL,
    face_value_minor  INTEGER NOT NULL CHECK (face_value_minor >= 0),
    currency          TEXT NOT NULL DEFAULT '',
    valid_from        TEXT NOT NULL DEFAULT '',
    valid_until       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'retired')),
    note              TEXT NOT NULL DEFAULT '',
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);

-- 外部商品的平台唯一性: 同租户同平台同 provider_offer_id 只挂一个目录行。
-- 部分索引: merchant_benefit(本地券)不受此限,商家可建多个本地优惠。
CREATE UNIQUE INDEX IF NOT EXISTS uq_deal_offers_external_product
    ON deal_offers(tenant_id, provider, provider_offer_id)
    WHERE kind = 'external_product' AND provider_offer_id <> '';

CREATE INDEX IF NOT EXISTS idx_deal_offers_campaign
    ON deal_offers(tenant_id, campaign_id, status);

-- 批次可引用一个优惠供给:引用外部供给(external_product)的批次所发权益
-- 只能显示与等待外部事实,本地不可核销。
ALTER TABLE reward_inventory ADD COLUMN offer_id TEXT NOT NULL DEFAULT '';
