-- HUI-4591:订阅服务权益投影(0109)。
-- 纪律(裁定书):
--   - subscription_ref 是派诺生态统一账单的订阅引用(权威在生态);本表只存
--     可重建投影,不存余额、不存扣费事实,绝不自增任何 balance。
--   - status 四态开通对齐 F16:processing(处理中)→opening(开通中)→
--     active(已开通)|failed(失败);unconfirmed = 无权威事实(诚实未知)。
--   - paid_until 是服务期截止(RFC3339):HUI-1671 六种到期的订阅期限真源
--     (接线:rewardPromiseDeadlines 按租户事实查询本列)。
--   - entitlement_projection:权益投影 JSON(可重建,权威事实到达时整行刷新)。
--   - store_limit 是门店档位(目录 included_store_count+加店);grace_days 是
--     维护保留期(到期后数据保留只读,不删除任何事实)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS billing_subscriptions (
    tenant_id               TEXT PRIMARY KEY REFERENCES tenants(id),
    subscription_ref        TEXT NOT NULL DEFAULT '',
    plan_code               TEXT NOT NULL DEFAULT '',
    pricing_version         TEXT NOT NULL DEFAULT '',
    -- unconfirmed | processing | opening | active | failed | expired
    status                  TEXT NOT NULL DEFAULT 'unconfirmed' CHECK (status IN
        ('unconfirmed', 'processing', 'opening', 'active', 'failed', 'expired')),
    paid_until              TEXT NOT NULL DEFAULT '',
    grace_days              INTEGER NOT NULL DEFAULT 0 CHECK (grace_days >= 0),
    store_limit             INTEGER NOT NULL DEFAULT 0 CHECK (store_limit >= 0),
    entitlement_projection  TEXT NOT NULL DEFAULT '{}',
    last_order_id           TEXT NOT NULL DEFAULT '',
    subscription_ref_event  TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL
);
