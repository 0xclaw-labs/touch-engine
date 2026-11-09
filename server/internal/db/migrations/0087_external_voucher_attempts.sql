-- HUI-4590: 外部平台券核销尝试（external_voucher_attempt 语义）。
-- 事实只能来自授权适配器的返回（复用 HUI-1671 边界纪律）：
--   - 缺适配器（未接通）或缺授权引用 → 端点诚实拒绝，本表零行落库；
--   - 适配器返回 redeemed → 落一行 redeemed（同券终态唯一，部分唯一索引），
--     重放只读回、不再调适配器；
--   - 适配器返回 failed → 追加 failed 行（审计留痕，可重试）；
--   - 传输失败/结果未知 → 落 unknown 行（待确认）：重试/轮询沿同一请求先
--     Lookup 回查，确认前绝不重发核销（票面硬约束），确认后才升级终态。
-- coupon_ref 是敏感外部券引用：不进日志、不进公共面、不进任何 JSON 响应。
-- 本表与本地奖励账（reward_benefits）结构性隔离：本地门店券不能核销外部券，
-- 外部券核销也不写本地奖励账任何行。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS external_voucher_attempts (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    store_id   TEXT NOT NULL,
    offer_id   TEXT NOT NULL,
    platform   TEXT NOT NULL,
    coupon_ref TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('redeemed', 'failed', 'unknown')),
    detail     TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_external_voucher_redeemed
    ON external_voucher_attempts(tenant_id, offer_id, coupon_ref)
    WHERE state = 'redeemed';

CREATE INDEX IF NOT EXISTS idx_external_voucher_attempts_store
    ON external_voucher_attempts(store_id, created_at);
