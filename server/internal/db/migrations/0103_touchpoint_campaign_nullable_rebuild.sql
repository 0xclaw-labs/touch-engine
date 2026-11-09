-- HUI-1665 Wave2: campaign_id 放宽为可空 —— store 根触点允许不带活动注记。
-- 纪律:
--   - SQLite 不能 ALTER 放宽 NOT NULL,按官方流程重建表。
--   - Migrate 在事务内逐文件执行,连接级 foreign_keys=ON 且事务内 PRAGMA foreign_keys
--     是 no-op。被 touchpoint_events.alias_id 引用的表不能直接 DROP:隐式 DELETE 会给
--     延迟/立即外键计数器留下无法由后续 RENAME 消解的违规(COMMIT 处失败,探针已证)。
--     因此先把审计行搬到无约束暂存表、清空子表、重建父表、再按新父回插(此时外键
--     立即检查通过)、丢弃暂存 —— 每一步要么零违规要么当场可过,不需要 defer。
--   - 回拷带 target_type/binding_revision 现值(0102 已落列,首次应用与重放同一 SQL,
--     重放零丢失);旧行缺省时 COALESCE 回 legacy_campaign/1,双保险。
--   - 全部旧列/旧约束(一卡一别名唯一索引、status/stage CHECK、SET NULL)原样保留;
--     events 表本体与其索引不动(kind CHECK 在回插时继续生效)。

PRAGMA foreign_keys = ON;

CREATE TABLE touchpoint_aliases_new (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    code             TEXT NOT NULL UNIQUE,
    tag_id           TEXT REFERENCES nfc_tags(id) ON DELETE SET NULL,
    campaign_id      TEXT REFERENCES campaigns(id),
    store_id         TEXT REFERENCES stores(id),
    target_type      TEXT NOT NULL DEFAULT 'legacy_campaign' CHECK (target_type IN ('store','legacy_campaign')),
    binding_revision INTEGER NOT NULL DEFAULT 1 CHECK (binding_revision >= 1),
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

INSERT INTO touchpoint_aliases_new(
    id,tenant_id,code,tag_id,campaign_id,store_id,target_type,binding_revision,label,position,
    status,deployment_stage,stage_note,stage_by,stage_at,created_by,created_at,updated_at)
SELECT
    id,tenant_id,code,tag_id,campaign_id,store_id,
    COALESCE(target_type,'legacy_campaign'),COALESCE(binding_revision,1),label,position,
    status,deployment_stage,stage_note,stage_by,stage_at,created_by,created_at,updated_at
FROM touchpoint_aliases;

-- 审计行先出暂存、清空子表:DROP 父表时无引用行,外键零违规。
CREATE TABLE touchpoint_events_rebuild_stage AS SELECT * FROM touchpoint_events;
DELETE FROM touchpoint_events;

DROP TABLE touchpoint_aliases;
ALTER TABLE touchpoint_aliases_new RENAME TO touchpoint_aliases;

-- 回插审计行:父表已就位,alias_id 外键立即检查通过;kind CHECK 继续生效。
INSERT INTO touchpoint_events(id,tenant_id,alias_id,kind,detail,created_by,created_at)
    SELECT id,tenant_id,alias_id,kind,detail,created_by,created_at FROM touchpoint_events_rebuild_stage;
DROP TABLE touchpoint_events_rebuild_stage;

CREATE UNIQUE INDEX IF NOT EXISTS idx_touchpoint_aliases_tag
    ON touchpoint_aliases(tag_id) WHERE tag_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_touchpoint_aliases_tenant ON touchpoint_aliases(tenant_id);
CREATE INDEX IF NOT EXISTS idx_touchpoint_aliases_campaign ON touchpoint_aliases(campaign_id);
CREATE INDEX IF NOT EXISTS idx_touchpoint_aliases_store ON touchpoint_aliases(store_id);
