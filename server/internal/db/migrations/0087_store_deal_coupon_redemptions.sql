-- HUI-4590: 外部平台券的核销事实。
-- 事实只能来自授权适配器的返回（复用 HUI-1671 边界纪律）：
--   - 缺适配器（未接通）或缺授权引用 → 端点诚实拒绝，本表零行落库；
--   - 适配器返回 redeemed → 落一行 redeemed（同券终态唯一，部分唯一索引），
--     重放只读回、不再调适配器；
--   - 适配器返回 failed → 追加 failed 行（审计留痕，允许重试）；
--   - 传输失败/结果未知 → 诚实未知响应，零行落库（永不冒充核销）。
-- coupon_ref 是敏感外部券引用：不进日志、不进公共面。
-- 本表与本地奖励账（reward_benefits）结构性隔离：本地门店券不能核销外部券，
-- 外部券核销也不写本地奖励账任何行。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_deal_coupon_redemptions (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    store_id   TEXT NOT NULL,
    offer_id   TEXT NOT NULL,
    platform   TEXT NOT NULL,
    coupon_ref TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('redeemed', 'failed')),
    detail     TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_store_deal_coupon_redeemed
    ON store_deal_coupon_redemptions(tenant_id, offer_id, coupon_ref)
    WHERE state = 'redeemed';

CREATE INDEX IF NOT EXISTS idx_store_deal_coupon_redemptions_store
    ON store_deal_coupon_redemptions(store_id, created_at);
