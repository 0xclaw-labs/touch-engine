-- HUI-3553 (TOUCH-ALL-20261109): 受控真实媒体元数据。
-- Additive only:既有表零改动。纪律:
--   - 真实文件:字节存本服务受控媒体目录(TOUCH_MEDIA_DIR,默认 data/media,
--     内容寻址 <dir>/<sha256 前两位>/<sha256><ext>),库中零字节、零外部 URL
--     转发;media-access 受控读取直连本表描述的真实文件;
--   - 服务器以真实字节定类型:mime_type 来自魔数探测(非客户端伪填),
--     byte_size/sha256 是落盘时的实测值;duration_ms/width/height 来自对
--     mp4 结构的尽力解析(缺失记 0),不伪造;
--   - kind 由探测出的 MIME 决定(video|image),不是客户端声明;
--   - 同租户同 sha256 去重复用同一行(平台重复取材零新增)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS content_media (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    store_id    TEXT NOT NULL REFERENCES stores(id),
    kind        TEXT NOT NULL CHECK (kind IN ('video','image')),
    mime_type   TEXT NOT NULL,
    byte_size   INTEGER NOT NULL,
    sha256      TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    width       INTEGER NOT NULL DEFAULT 0,
    height      INTEGER NOT NULL DEFAULT 0,
    filename    TEXT NOT NULL DEFAULT '',
    media_ref   TEXT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE (tenant_id, sha256)
);

CREATE INDEX IF NOT EXISTS idx_content_media_store ON content_media(tenant_id, store_id, created_at);
