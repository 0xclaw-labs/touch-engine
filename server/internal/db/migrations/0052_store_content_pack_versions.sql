-- HUI-3553 (TOUCH-ALL-20261109): 门店根内容包的不可变版本 + 只追加处理记录。
-- Additive only:既有表零改动。纪律(同 0029 词汇):
--   - 版本不可变:内容列零 UPDATE;改文案/换素材 = 新开版本;回流与晚到旧稿
--     一律只落 candidate,绝不覆盖当前采用版;
--   - 引用快照冻结:video/cover/image_text 逐项冻结
--     (media_id, sha256, mime_type, byte_size, duration_ms, width, height)
--     —— 媒体行后续变化不影响已冻结版本;
--   - status 工作流: draft|candidate|adopted|retired|blocked;
--   - idempotency_key 非空时租户内唯一,同键重放 = 返回既有行,零新建;
--   - 事件表只追加:created|submitted|adopted|superseded|blocked|returned。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_content_pack_versions (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    pack_id         TEXT NOT NULL REFERENCES store_content_packs(id),
    version_no      INTEGER NOT NULL,
    content_kind    TEXT NOT NULL CHECK (content_kind IN ('video','imagetext')),
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','candidate','adopted','retired','blocked')),
    source          TEXT NOT NULL CHECK (source IN ('merchant_upload','asset_lib','product_photo_return')),
    video_media_id   TEXT NOT NULL DEFAULT '',
    video_sha256     TEXT NOT NULL DEFAULT '',
    video_mime       TEXT NOT NULL DEFAULT '',
    video_bytes      INTEGER NOT NULL DEFAULT 0,
    video_duration_ms INTEGER NOT NULL DEFAULT 0,
    video_width      INTEGER NOT NULL DEFAULT 0,
    video_height     INTEGER NOT NULL DEFAULT 0,
    cover_media_id   TEXT NOT NULL DEFAULT '',
    cover_sha256     TEXT NOT NULL DEFAULT '',
    cover_mime       TEXT NOT NULL DEFAULT '',
    cover_bytes      INTEGER NOT NULL DEFAULT 0,
    image_text       TEXT NOT NULL DEFAULT '[]',
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_scpv_idem ON store_content_pack_versions(tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_scpv_pack ON store_content_pack_versions(tenant_id, pack_id, version_no);

CREATE TABLE IF NOT EXISTS store_content_pack_version_events (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    version_id TEXT NOT NULL REFERENCES store_content_pack_versions(id),
    kind       TEXT NOT NULL CHECK (kind IN ('created','submitted','adopted','superseded','blocked','returned')),
    note       TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scpve_version ON store_content_pack_version_events(tenant_id, version_id, created_at);
