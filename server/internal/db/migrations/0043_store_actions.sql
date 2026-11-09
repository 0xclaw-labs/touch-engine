-- HUI-4584 G1: 门店动作实例模型。
-- store_actions 取代「(campaign_id,kind) 唯一」的旧约束：action_id 是门店动作
-- 实例的稳定不透明 ID；同门店同 catalog_code+platform+variant 可并存多条
-- （五个点评、多个关注互不覆盖）。kind 只是分类维度，不是唯一键。
-- catalog_code = A01-A30 固定目录（internal/actioncatalog）；'' = 旧数据迁移
-- 时平台推不出的待映射行，由商家在 M4 明确采用，不猜选。
-- target_ref 只存平台主页/POI/商品/服务目标；content_binding_ref 单独引用
-- HUI-3553 已采用内容集合；capability_ref 是 HUI-3535 能力判断的引用位
-- （本票不填）。secret/凭据永不进入本表。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_actions (
    id                 TEXT PRIMARY KEY,             -- action_id: sa_<hex>，门店范围内随机，不用自增
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    store_id           TEXT NOT NULL REFERENCES stores(id),
    catalog_code       TEXT NOT NULL DEFAULT '' CHECK (catalog_code = '' OR catalog_code GLOB 'A[0-9][0-9]'),
    platform           TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL DEFAULT '',
    variant            TEXT NOT NULL DEFAULT '',
    label              TEXT NOT NULL DEFAULT '',
    target_ref         TEXT NOT NULL DEFAULT '',
    content_binding_ref TEXT NOT NULL DEFAULT '',
    capability_ref     TEXT NOT NULL DEFAULT '',
    presentation       TEXT NOT NULL DEFAULT 'link',
    sort_order         INTEGER NOT NULL DEFAULT 0,
    enabled            INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    revision           INTEGER NOT NULL DEFAULT 1,
    revoked            INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0,1)),
    expires_at         TEXT NOT NULL DEFAULT '',
    legacy_campaign_id TEXT NOT NULL DEFAULT '',     -- 旧 (campaign_id,kind) 行来源；'' = 门店级直配
    legacy_kind        TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_store_actions_store ON store_actions(store_id, sort_order);

-- 门店级配置修订计数（GET/PUT config_revision 乐观锁）。
CREATE TABLE IF NOT EXISTS store_action_configs (
    store_id        TEXT PRIMARY KEY REFERENCES stores(id),
    config_revision INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT NOT NULL
);

-- 每次成功保存形成候选不可变修订（线上由 HUI-1676 的首页发布版本明确引用）。
CREATE TABLE IF NOT EXISTS store_action_snapshots (
    id              TEXT PRIMARY KEY,
    store_id        TEXT NOT NULL REFERENCES stores(id),
    config_revision INTEGER NOT NULL,
    payload         TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    UNIQUE (store_id, config_revision)
);

-- 公共门店码与门店面字段（GET /api/v1/public/storefronts/{storeCode}）。
-- 另建表，不改 stores 列。storefront_revision 本票恒 0：发布版本归 HUI-1676。
CREATE TABLE IF NOT EXISTS store_storefronts (
    store_id           TEXT PRIMARY KEY REFERENCES stores(id),
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    store_code         TEXT NOT NULL UNIQUE,
    opening_hours      TEXT NOT NULL DEFAULT '',
    storefront_revision INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
