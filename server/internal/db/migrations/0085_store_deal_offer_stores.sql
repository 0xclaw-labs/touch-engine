-- HUI-4590: 套餐的适用门店（多对多）。
-- 一个套餐可适用多门店（连锁同款团购）；顾客按门店码读到自己可用的套餐。
-- tenant_id 冗余列让租户隔离查询不必 join 主表；写路径保证与主表同租户。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_deal_offer_stores (
    offer_id   TEXT NOT NULL REFERENCES store_deal_offers(id) ON DELETE CASCADE,
    store_id   TEXT NOT NULL REFERENCES stores(id),
    tenant_id  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (offer_id, store_id)
);

CREATE INDEX IF NOT EXISTS idx_store_deal_offer_stores_store
    ON store_deal_offer_stores(store_id);
