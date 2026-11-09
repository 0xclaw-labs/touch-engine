-- HUI-1671: 外部券发放登记(幂等持久化)。
-- 超时/传输失败沿同一 idempotency_key 回查,不新建发券请求;结果未知保持
-- pending_unknown(待确认),永不升级为已发放;确认 issued/failed 才落定。
-- coupon_ref 是外部平台券引用(敏感):不进日志、不进公共面、不进统计 payload。
-- 无授权(authorization_ref 空)不允许生成任何"平台已发"事实。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS reward_external_issuances (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    campaign_id     TEXT NOT NULL REFERENCES campaigns(id),
    participation_id TEXT NOT NULL,
    benefit_id      TEXT NOT NULL DEFAULT '',
    offer_id        TEXT NOT NULL REFERENCES deal_offers(id),
    idempotency_key TEXT NOT NULL,
    -- issued | pending_unknown | failed
    state           TEXT NOT NULL CHECK (state IN ('issued', 'pending_unknown', 'failed')),
    provider        TEXT NOT NULL DEFAULT '',
    coupon_ref      TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_reward_external_issuances_benefit
    ON reward_external_issuances(tenant_id, benefit_id, state);
