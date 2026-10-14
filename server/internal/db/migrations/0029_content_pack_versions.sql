-- HUI-3553: 内容包不可变版本 + 只追加处理记录。
-- Additive only:既有表零改动。纪律:
--   - 版本不可变:内容列(引用快照/文案/门店事实/平台适配/授权/标识)零 UPDATE;
--     改文案、改价、源素材更新 = 一律新开版本;旧版本绝不被回流覆盖;
--   - 引用快照冻结 (asset_id, asset_ref, sha256) 三元组,复用 HUI-1666
--     lib_assets 既有行,零物理文件;图文引用同样逐张冻结;
--   - 平台适配版本列表 platform_versions(JSON 数组):每项必含 platform、
--     note、source_version —— 同一包的不同平台版本仍可追溯同一来源;
--   - 授权语义:grant_refs 为引用级声明快照(同 1666 纪律,HUI-1732 前为
--     不透明引用);grant_until 可空 RFC3339,过期即拒绝新分发(fail-closed);
--   - status 工作流: draft|candidate|adopted|retired|blocked;blocked =
--     撤权/重大内容问题,阻止新分发,旧参与保留原版本与事件表处理记录;
--   - idempotency_key:回流候选/制作工程去重键(结构预留):非空时租户内唯一,
--     同键重放=返回既有行(应用层),不新建;NULL 可重复(SQLite 唯一约束语义);
--   - content_pack_version_events 只追加:created|submitted|adopted|superseded|
--     blocked|returned 的处理记录,永不改写删除(处理记录是审计事实)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS content_pack_versions (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    pack_id         TEXT NOT NULL REFERENCES content_packs(id),
    version_no      INTEGER NOT NULL,
    content_kind    TEXT NOT NULL CHECK (content_kind IN ('video','imagetext')),
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','candidate','adopted','retired','blocked')),
    source          TEXT NOT NULL CHECK (source IN ('merchant_upload','asset_lib','product_photo_return')),
    -- video / cover 引用快照(imagetext 时置空串;图文引用走 image_text JSON)
    video_asset_id  TEXT NOT NULL DEFAULT '',
    video_asset_ref TEXT NOT NULL DEFAULT '',
    video_sha256    TEXT NOT NULL DEFAULT '',
    cover_asset_id  TEXT NOT NULL DEFAULT '',
    cover_asset_ref TEXT NOT NULL DEFAULT '',
    cover_sha256    TEXT NOT NULL DEFAULT '',
    image_text      TEXT NOT NULL DEFAULT '[]',
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,
    topics          TEXT NOT NULL DEFAULT '[]',
    store_name      TEXT NOT NULL,
    price_note      TEXT NOT NULL,
    valid_note      TEXT NOT NULL,
    platform_versions TEXT NOT NULL DEFAULT '[]',
    grant_refs      TEXT NOT NULL DEFAULT '[]',
    grant_until     TEXT NOT NULL DEFAULT '',
    marking         TEXT NOT NULL DEFAULT '{}',
    blocked_reason  TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    UNIQUE (pack_id, version_no)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_cpv_idem ON content_pack_versions(tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cpv_pack ON content_pack_versions(tenant_id, pack_id, version_no);

CREATE TABLE IF NOT EXISTS content_pack_version_events (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    version_id TEXT NOT NULL REFERENCES content_pack_versions(id),
    kind       TEXT NOT NULL CHECK (kind IN ('created','submitted','adopted','superseded','blocked','returned')),
    note       TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cpve_version ON content_pack_version_events(tenant_id, version_id, created_at);
