-- HUI-4590: 顾客点击套餐卡的匿名聚合统计（public_view_stats 先例）。
-- 点击只是点击：本表没有任何"成交"字段——下单与成交都在平台完成，
-- 本地不可知，不推算、不伪造（点击≠成交）。
-- 平台维度随套餐行带入：美团/点评分渠道统计保留，不同合。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_deal_click_stats (
    tenant_id  TEXT NOT NULL,
    store_id   TEXT NOT NULL,
    offer_id   TEXT NOT NULL,
    platform   TEXT NOT NULL,
    day        TEXT NOT NULL,
    clicks     INTEGER NOT NULL DEFAULT 0 CHECK (clicks >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (offer_id, day)
);

CREATE INDEX IF NOT EXISTS idx_store_deal_click_stats_store_day
    ON store_deal_click_stats(store_id, day);
