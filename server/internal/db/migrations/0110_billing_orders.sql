-- HUI-4591:公共订单投影与幂等键(0110)。
-- 纪律(裁定书):
--   - order_ref 是公共订单权威引用;amount/price_snapshot 是下单时引用的目录
--     快照——目录此后变更不改历史订单(4592 O2 消费纪律)。
--   - 本表不是扣费账:扣费事实归公共权威;status 只是四态开通投影
--     processing(处理中)→opening(开通中)→active(已开通)|failed(失败),
--     cancelled = 商家撤单。浏览器返回(收银台跳回)永远只读,不推进状态。
--   - UNIQUE(tenant_id, idempotency_key):同键重试只读回既有订单,不重复下单。
--   - payer_grant_ref/agency_relation_ref/payer_principal 是代理代付授权快照
--     (服务端解析 4593 激活代管关系所得;org_owner 直购三列空)。
--   - billing_order_events:权威事实事件(source+event_id 去重;同键不同负载
--     拒 IDEMPOTENCY_CONFLICT),状态推进只由本表事件驱动。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS billing_orders (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    kind                TEXT NOT NULL CHECK (kind IN ('subscription', 'hardware')),
    action              TEXT NOT NULL DEFAULT 'new' CHECK (action IN ('new', 'renew', 'expand', 'downgrade', 'purchase')),
    plan_code           TEXT NOT NULL DEFAULT '',
    price_ref           TEXT NOT NULL DEFAULT '',
    price_snapshot      TEXT NOT NULL DEFAULT '{}',
    amount_minor        INTEGER NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    currency            TEXT NOT NULL DEFAULT '',
    -- processing | opening | active | failed | cancelled
    status              TEXT NOT NULL DEFAULT 'processing' CHECK (status IN
        ('processing', 'opening', 'active', 'failed', 'cancelled')),
    order_ref           TEXT NOT NULL DEFAULT '',
    payment_ref         TEXT NOT NULL DEFAULT '',
    idempotency_key     TEXT NOT NULL,
    payer_grant_ref     TEXT NOT NULL DEFAULT '',
    agency_relation_ref TEXT NOT NULL DEFAULT '',
    payer_principal     TEXT NOT NULL DEFAULT '',
    actor_principal     TEXT NOT NULL DEFAULT '',
    actor_role          TEXT NOT NULL DEFAULT '',
    fail_reason         TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_billing_orders_tenant_kind
    ON billing_orders(tenant_id, kind, created_at);

CREATE TABLE IF NOT EXISTS billing_order_events (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    order_id    TEXT NOT NULL REFERENCES billing_orders(id),
    source      TEXT NOT NULL,
    event_id    TEXT NOT NULL,
    event_type  TEXT NOT NULL CHECK (event_type IN
        ('order_registered', 'payment_paid', 'activation_opening', 'activation_active',
         'activation_failed', 'order_cancelled')),
    payload     TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE (tenant_id, source, event_id)
);

CREATE INDEX IF NOT EXISTS idx_billing_order_events_order
    ON billing_order_events(order_id, occurred_at);
