-- HUI-4664 / touch-order-checkout/v1.1：Touch↔公共收银台映射投影（合同 §1 两侧职责）。
-- 纪律：
--   - UNIQUE(tenant_id, order_id) 一单一行，ref 恒定（合同 §1 两侧职责原文）：
--     同原单重试/查回/续付只读回同一映射，绝不换键。
--   - checkout_ref = Touch 本地续付引用 ck_*（主路由键，§0 统一契约：双向
--     事件以它路由；producer 的 tco_* 只是本表映射的远端值）。
--   - remote_checkout_ref = producer（platform-billing）mint 签发的支付引用
--     （tco_*），是向公共侧查单/回发的路由键；两者一一对应。
--   - 状态投影：pending|paid|activated|closed——paid 只由 payment_paid
--     gating 事件（金额比对裁决通过）推进；activated 只由 activation_active
--     回发成功推进。浏览器返回/success query 一律不写本表。

CREATE TABLE IF NOT EXISTS billing_checkout_refs (
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    order_id            TEXT NOT NULL,
    checkout_ref        TEXT NOT NULL,
    remote_checkout_ref TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'paid', 'activated', 'closed')),
    amount_minor        INTEGER NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    currency            TEXT NOT NULL DEFAULT 'CNY',
    return_context      TEXT NOT NULL DEFAULT '{}',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (tenant_id, order_id),
    UNIQUE (tenant_id, checkout_ref)
);

CREATE INDEX IF NOT EXISTS idx_billing_checkout_refs_remote
    ON billing_checkout_refs(tenant_id, remote_checkout_ref)
    WHERE remote_checkout_ref != '';
