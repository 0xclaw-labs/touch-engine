-- HUI-1671 B3 真实奖励交付: 券批次(库存)与合法导入券码池。
-- 面额(face_value_minor)与实际成本(cost_minor)分列,都是 touch 域内营销账:
-- 不与 public-ai 模型钱包、订单款混账。规则快照由批次行承载
-- (benefit_slot + rule_version + allowed_evidence + rule_ref 审计引用),
-- G3 不编辑活动规则,只保存被告知版本的绑定。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS reward_inventory (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    campaign_id      TEXT NOT NULL REFERENCES campaigns(id),
    store_id         TEXT NOT NULL DEFAULT '',
    kind             TEXT NOT NULL CHECK (kind IN ('store_coupon', 'imported_code', 'gift_discount')),
    name             TEXT NOT NULL,
    benefit_slot     TEXT NOT NULL,
    rule_version     INTEGER NOT NULL CHECK (rule_version >= 1),
    allowed_evidence TEXT NOT NULL CHECK (length(allowed_evidence) > 0),
    rule_ref         TEXT NOT NULL DEFAULT '',
    total_qty        INTEGER NOT NULL CHECK (total_qty >= 0),
    face_value_minor INTEGER NOT NULL CHECK (face_value_minor >= 0),
    cost_minor       INTEGER NOT NULL CHECK (cost_minor >= 0),
    currency         TEXT NOT NULL DEFAULT '',
    valid_from       TEXT NOT NULL DEFAULT '',
    valid_until      TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'retired')),
    note             TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reward_inventory_campaign
    ON reward_inventory(tenant_id, campaign_id, benefit_slot, rule_version);

-- 合法导入券码池:一行一码。码是敏感凭据:不进公共面、不进流水 payload、
-- 不进日志;只在管理面(authenticated)可见。
CREATE TABLE IF NOT EXISTS reward_code_pool (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    inventory_id      TEXT NOT NULL REFERENCES reward_inventory(id),
    code              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'allocated')),
    allocated_benefit TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    UNIQUE (tenant_id, inventory_id, code)
);

CREATE INDEX IF NOT EXISTS idx_reward_code_pool_alloc
    ON reward_code_pool(inventory_id, status);
