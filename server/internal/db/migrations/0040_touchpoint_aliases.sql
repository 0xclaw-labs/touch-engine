-- HUI-1665 增量(2026-10-14): 稳定触点别名 —— 一张实体卡一个固定 /t/{code} 入口。
-- 纪律:
--   - 别名是既有标签/短码域之上的独立入口层,不是第二套链接模型:
--     campaign_links 与五态解析零改动;别名的 code 与短码同一熵源与形状闸
--     (campaign.NewShortcode / ValidShortcode),不可枚举、不嵌结构。
--   - campaign_id 是"当前目标活动"(换绑=改此列,乐观并发由应用层把守);
--     URL(别名码)从不因换绑变化 —— 票面「同卡换活动 URL 不变」。
--   - status 是该卡自己的开关:停用只关别名,绝不级联 campaign_links
--     (票面「单卡停用不误停共享的其他卡」)。
--   - tag_id ON DELETE SET NULL:实体卡入口比管理面记录活得久,删管理记录
--     不下线已经印出/写卡的入口。
--   - deployment_stage 五态部署进度只经显式接口推进;CSV/二维码/检查零推进
--     (「写入未知状态不冒充已部署」)。
--   - 无任何 URL/凭据/租户外部标识字段:卡上载荷只有不透明码 URL。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS touchpoint_aliases (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    code             TEXT NOT NULL UNIQUE,
    tag_id           TEXT REFERENCES nfc_tags(id) ON DELETE SET NULL,
    campaign_id      TEXT NOT NULL REFERENCES campaigns(id),
    store_id         TEXT REFERENCES stores(id),
    label            TEXT NOT NULL DEFAULT '',
    position         TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    deployment_stage TEXT NOT NULL DEFAULT 'registered' CHECK (deployment_stage IN
                     ('registered','awaiting_write','written_awaiting_readback','readback_done','device_verified')),
    stage_note       TEXT NOT NULL DEFAULT '',
    stage_by         TEXT NOT NULL DEFAULT '',
    stage_at         TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

-- 一张实体卡至多一个稳定入口。
CREATE UNIQUE INDEX IF NOT EXISTS idx_touchpoint_aliases_tag
    ON touchpoint_aliases(tag_id) WHERE tag_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_touchpoint_aliases_tenant ON touchpoint_aliases(tenant_id);
CREATE INDEX IF NOT EXISTS idx_touchpoint_aliases_campaign ON touchpoint_aliases(campaign_id);
