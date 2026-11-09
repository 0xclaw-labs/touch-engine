-- HUI-4591 订购、收费与账单:价格目录投影(0108)。
-- 纪律(裁定书):
--   - 本仓不自建价格权威:目录行是派诺生态统一账单价格目录的**可重建投影**,
--     经受信权威缝(服务生产者)写入;Go 代码零价格常量,一切金额/币种/服务期/
--     门店档位/加店单价/阶梯都来自本表行。
--   - scope_tenant '' = 全局默认价;非空 = 租户专属价(解析顺序:租户行 > 全局)。
--   - (scope_tenant, plan_code, pricing_version) 唯一:同版本同套餐同 scope 只有一行,
--     价格修订=新 pricing_version 新行;订单引用 price_ref+快照,目录变更不改历史订单。
--   - tier_json:阶梯加店价(JSON 数组文本,'[]'=不用阶梯,统一 extra_store_price_minor)。
--   - 生效窗口 RFC3339;at 不在窗口内的行不参与解析。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS billing_price_catalog (
    id                     TEXT PRIMARY KEY,
    -- '' = 全局默认价(非空时应用层校验租户存在;空值不能挂 FK,故不设引用)
    scope_tenant           TEXT NOT NULL DEFAULT '',
    plan_code              TEXT NOT NULL CHECK (plan_code IN ('store.plan.month', 'store.plan.year')),
    pricing_version        TEXT NOT NULL,
    price_ref              TEXT NOT NULL,
    currency               TEXT NOT NULL,
    price_minor            INTEGER NOT NULL CHECK (price_minor >= 0),
    service_days           INTEGER NOT NULL CHECK (service_days > 0),
    included_store_count   INTEGER NOT NULL CHECK (included_store_count >= 0),
    extra_store_price_minor INTEGER NOT NULL DEFAULT 0 CHECK (extra_store_price_minor >= 0),
    tier_json              TEXT NOT NULL DEFAULT '[]',
    grace_days             INTEGER NOT NULL DEFAULT 0 CHECK (grace_days >= 0),
    effective_from         TEXT NOT NULL DEFAULT '',
    effective_to           TEXT NOT NULL DEFAULT '',
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL,
    UNIQUE (scope_tenant, plan_code, pricing_version)
);

CREATE INDEX IF NOT EXISTS idx_billing_price_catalog_plan
    ON billing_price_catalog(scope_tenant, plan_code, effective_from);
