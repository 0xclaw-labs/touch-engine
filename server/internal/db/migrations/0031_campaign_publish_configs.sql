-- HUI-1676 增量(2026-10-14): 商家发布兑奖配置 —— 启用状态、权益引用、告知快照。
-- Additive only:既有表零改动;规则 4 字段仍住 0005 的 campaign_rules,本表是
-- 「配置编辑器」的完整配置面。纪律:
--   - 每活动一行现行配置(UNIQUE campaign_id),挂既有 campaigns 维度;
--   - 变更不静默改写:每次保存/启用/暂停都在 campaign_publish_config_revisions
--     落一条全量快照修订(revision 按活动内单调递增),版本可回查;
--   - verification_mode 与 rewardops 三个 Evidence 类逐字对齐;配置时写死
--     顾客文案(customer_copy),不在完成后临时加严;
--   - 权益只存引用(benefit_slot)与预算口径,不建库存;不写参与/奖励表
--     (G2/G3 权威);奖励真值在 reward_inventory(0023)只读;
--   - channels/store_ids/disclosure 为 JSON TEXT(与 0028/0029 惯例一致);
--     channels 每项必须含 platform+entry_mode+scenario 三元组(应用层把关),
--     不存在「只存平台字符串」的合法形状;
--   - enabled_version + disclosure 是启用时点的钉住快照:配置改版回 draft 后
--     二者保留,供在途参与按原告知处理(不追溯、不重发)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS campaign_publish_configs (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    campaign_id         TEXT NOT NULL REFERENCES campaigns(id),
    version             INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    status              TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','enabled','paused')),
    verification_mode   TEXT NOT NULL CHECK (verification_mode IN
        ('staff_observed_submission','staff_verified_post','official_publish_confirmed')),
    customer_copy       TEXT NOT NULL DEFAULT '',
    benefit_kind        TEXT NOT NULL CHECK (benefit_kind IN
        ('store_coupon','imported_code','onsite_gift','onsite_discount')),
    benefit_slot        TEXT NOT NULL,
    budget_mode         TEXT NOT NULL CHECK (budget_mode IN ('total_quantity','cost_budget')),
    total_quantity      INTEGER CHECK (total_quantity IS NULL OR total_quantity >= 0),
    cost_budget_minor   INTEGER CHECK (cost_budget_minor IS NULL OR cost_budget_minor >= 0),
    currency            TEXT NOT NULL DEFAULT '',
    store_ids           TEXT NOT NULL DEFAULT '[]',
    claim_from          TEXT NOT NULL DEFAULT '',
    claim_until         TEXT NOT NULL DEFAULT '',
    redeem_from         TEXT NOT NULL DEFAULT '',
    redeem_until        TEXT NOT NULL DEFAULT '',
    onsite_redemption   INTEGER NOT NULL DEFAULT 0 CHECK (onsite_redemption IN (0,1)),
    staff_scope         TEXT NOT NULL CHECK (staff_scope IN ('store_staff','org_owner')),
    out_of_stock_rule   TEXT NOT NULL CHECK (out_of_stock_rule IN ('stop_new','mark_sold_out')),
    dedup_basis         TEXT NOT NULL CHECK (dedup_basis IN
        ('verifiable_receipt','platform_authorized_subject','onsite_manual')),
    per_order_limit     INTEGER NOT NULL DEFAULT 0 CHECK (per_order_limit IN (0,1)),
    channels            TEXT NOT NULL DEFAULT '[]',
    content_version_id  TEXT NOT NULL DEFAULT '',
    enabled_version     INTEGER,
    enabled_at          TEXT NOT NULL DEFAULT '',
    paused_at           TEXT NOT NULL DEFAULT '',
    disclosure          TEXT NOT NULL DEFAULT '',
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (campaign_id)
);

-- 修订表:save(create/update)/enable/pause/resume 各一条全量快照,payload
-- 为应用层 JSON(spec + status + enabled_version + disclosure),永不改写删除。
CREATE TABLE IF NOT EXISTS campaign_publish_config_revisions (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    campaign_id TEXT NOT NULL,
    revision    INTEGER NOT NULL CHECK (revision >= 1),
    action      TEXT NOT NULL CHECK (action IN ('create','update','enable','pause','resume')),
    version     INTEGER NOT NULL,
    payload     TEXT NOT NULL,
    changed_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE (campaign_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_campaign_publish_configs_campaign
    ON campaign_publish_configs(tenant_id, campaign_id);
CREATE INDEX IF NOT EXISTS idx_campaign_publish_config_revisions_campaign
    ON campaign_publish_config_revisions(campaign_id, revision);
