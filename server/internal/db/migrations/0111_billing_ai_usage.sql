-- HUI-4591:AI 用量投影(0111,F13)。
-- 纪律(裁定书):
--   - 投影自 4595 的 quote/charge 引用:quote_ref 是内容制作/收费 AI 生成的
--     报价引用;本表只投影"已确认的收费事实引用",浏览/复制等不计费动作
--     在调用侧约束,永不进本表。
--   - 按 4595 裁定书契约的 quote_ref 校验缝(exported):
--     quote_not_found/quote_cross_tenant/quote_not_confirmed/
--     quote_already_consumed/quote_capability_mismatch。
--   - UNIQUE(tenant_id, quote_ref):重试幂等(同引用只落一行,不重复计量)。
--   - state: confirmed(已确认未消费)|consumed(已被 handoff 消费)|
--     refunded(已退款)|failed(失败)。失败/退款状态可查(F13/4595)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS billing_ai_usage (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    quote_ref       TEXT NOT NULL,
    capability      TEXT NOT NULL DEFAULT '',
    quantity        INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    amount_minor    INTEGER NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    currency        TEXT NOT NULL DEFAULT '',
    pricing_version TEXT NOT NULL DEFAULT '',
    -- confirmed | consumed | refunded | failed
    state           TEXT NOT NULL DEFAULT 'confirmed' CHECK (state IN
        ('confirmed', 'consumed', 'refunded', 'failed')),
    business_ref    TEXT NOT NULL DEFAULT '',
    consumed_by_ref TEXT NOT NULL DEFAULT '',
    occurred_at     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    UNIQUE (tenant_id, quote_ref)
);

CREATE INDEX IF NOT EXISTS idx_billing_ai_usage_tenant_state
    ON billing_ai_usage(tenant_id, state, occurred_at);
