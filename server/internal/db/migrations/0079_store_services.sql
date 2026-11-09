-- HUI-4589 B07 门店连接域：门店服务配置（Wi-Fi / 加微信 / 公众号·社群二维码引用）。
-- 4584 store_actions 同风格：不透明实例 id（sv_<hex>）、同店同 kind 可并存多条
-- （个人微信与企微、公众号与社群互不覆盖）、config_revision 乐观锁 + 每次真实
-- 保存一条不可变 JSON 快照。config 的按 kind 形状校验在模型层（store 包），
-- CHECK 只拦 kind/catalog 枚举。Wi-Fi 密码是商家主动公示的连接凭据（顾客侧
-- 仅展示+主动复制，不强制留资），不属 AppSecret 类凭据。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_services (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    store_id     TEXT NOT NULL,
    catalog_code TEXT NOT NULL DEFAULT '' CHECK (catalog_code = '' OR catalog_code GLOB 'A[0-9][0-9]'),
    kind         TEXT NOT NULL CHECK (kind IN ('wifi','contact','community')),
    variant      TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    config       TEXT NOT NULL DEFAULT '{}',
    sort_order   INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL CHECK (enabled IN (0,1)),
    revision     INTEGER NOT NULL CHECK (revision >= 1),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_store_services_store ON store_services(store_id, sort_order);

CREATE TABLE IF NOT EXISTS store_service_configs (
    store_id        TEXT PRIMARY KEY,
    config_revision INTEGER NOT NULL CHECK (config_revision >= 0),
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS store_service_snapshots (
    id              TEXT PRIMARY KEY,
    store_id        TEXT NOT NULL,
    config_revision INTEGER NOT NULL,
    payload         TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
