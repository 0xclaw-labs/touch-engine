-- HUI-1671 B3: 权益实例。UNIQUE (tenant_id, participation_id, benefit_slot)
-- 是"一份奖励"的数据库级保证:不同 attempt、换渠道、改规则版本、人工与官方
-- 双证据、并发店员,都不能产生第二份同 slot 权益。
-- evidence 记录实际采用的证据类型,永不改写(人工不冒充官方)。
-- 面额/成本在分配时从批次快照拷贝,两个数分开记。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS reward_benefits (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    campaign_id       TEXT NOT NULL REFERENCES campaigns(id),
    inventory_id      TEXT NOT NULL REFERENCES reward_inventory(id),
    participation_id  TEXT NOT NULL,
    benefit_slot      TEXT NOT NULL,
    rule_version      INTEGER NOT NULL CHECK (rule_version >= 1),
    evidence          TEXT NOT NULL CHECK (evidence IN ('staff_observed_submission', 'staff_verified_post', 'official_publish_confirmed')),
    code              TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL CHECK (status IN ('reserved', 'issued', 'redeemed', 'reversed', 'expired')),
    store_id          TEXT NOT NULL DEFAULT '',
    face_value_minor  INTEGER NOT NULL CHECK (face_value_minor >= 0),
    cost_minor        INTEGER NOT NULL CHECK (cost_minor >= 0),
    currency          TEXT NOT NULL DEFAULT '',
    reserved_at       TEXT NOT NULL DEFAULT '',
    reserved_until    TEXT NOT NULL DEFAULT '',
    issued_at         TEXT NOT NULL DEFAULT '',
    issued_by         TEXT NOT NULL DEFAULT '',
    redeemed_at       TEXT NOT NULL DEFAULT '',
    redeemed_by       TEXT NOT NULL DEFAULT '',
    reversed_at       TEXT NOT NULL DEFAULT '',
    reversed_by       TEXT NOT NULL DEFAULT '',
    reverse_reason    TEXT NOT NULL DEFAULT '',
    note              TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (tenant_id, participation_id, benefit_slot)
);

CREATE INDEX IF NOT EXISTS idx_reward_benefits_inventory_status
    ON reward_benefits(inventory_id, status);
CREATE INDEX IF NOT EXISTS idx_reward_benefits_campaign
    ON reward_benefits(tenant_id, campaign_id, status);
