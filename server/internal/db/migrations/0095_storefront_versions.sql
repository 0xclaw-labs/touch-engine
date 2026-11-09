-- HUI-1676 (0095): 门店首页展示版本 storefront_versions (B01)。
-- Additive only: 既有表零改动(发布指针 = 4584 已建 store_storefronts.storefront_revision,
-- 其票恒 0,本票接管写;不新建第二指针)。纪律:
--   - draft/published 两种状态;最新行是可变草稿,发布后行即冻结为只读历史
--     (历史版本零改写;回滚只动指针,不改任何行内容);
--   - revision 店内单调递增(应用层 COUNT/MAX+1,SQLite 单写者串行化);
--     draft 幂等重放(内容指纹相同)不推高修订;
--   - sections/actions/action_revision_refs/offer_refs/campaign_refs 为 JSON TEXT
--     (与 0031 惯例一致):sections 是段落骨架[{id,type,title,action_ids,offer_refs}],
--     actions 是发布时冻结的白名单载荷(公共投影直接渲染,不回 join live 行;
--     实时撤权/过期/停用在读取层另行否决);
--   - 只存受权引用与冻结展示载荷:不复制目标配置/库存/参与/权益,不写参与表
--     (旧参与快照不随换版改写,奖励真值归 1671);
--   - summary_fingerprint 是 preflight 算出的变更摘要指纹:publish 必须回带匹配值
--     (用户确认变更摘要),草稿在确认后变过则指纹不齐 → 拒绝。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS storefront_versions (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    store_id             TEXT NOT NULL REFERENCES stores(id),
    store_code           TEXT NOT NULL DEFAULT '',
    revision             INTEGER NOT NULL CHECK (revision >= 1),
    status               TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
    sections             TEXT NOT NULL DEFAULT '[]',
    actions              TEXT NOT NULL DEFAULT '[]',
    action_revision_refs TEXT NOT NULL DEFAULT '{}',
    offer_refs           TEXT NOT NULL DEFAULT '[]',
    campaign_refs        TEXT NOT NULL DEFAULT '[]',
    brand_ref            TEXT NOT NULL DEFAULT '',
    summary_fingerprint  TEXT NOT NULL DEFAULT '',
    created_by           TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    published_at         TEXT NOT NULL DEFAULT '',
    UNIQUE (store_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_storefront_versions_store
    ON storefront_versions(tenant_id, store_id, revision);
