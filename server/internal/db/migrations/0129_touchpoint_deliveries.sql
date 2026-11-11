-- HUI-1663:实体 NFC 交付物理履约记录(0129)。
--
-- 纪律(票面 + briefs/hui-1663.md 裁定书):
--   - 每行 = 一张实体卡的一次完整交付生命周期;补换产生新行(replacement_of 链),
--     旧行封存为 superseded —— 交付事实只增不改,历史证据永不覆盖。
--   - status 只是七步状态机(付款→出库→绑定→写卡→回读→现场验收→交付)的当前步
--     投影;独立事实在 0130 事件账本;「不可跳跃(写卡前必须已出库+绑定)」由
--     internal/delivery 纯状态机裁决,本表 CHECK 只守白名单。
--   - 付款事实归 HUI-4591 订单履约投影(billing_hardware_fulfillment),本表只存
--     订单引用,不造余额、不建 paid 真值(投影纪律)。
--   - 卡片不写任何凭证(B15 红线):本表零 secret;写卡资料只有公开 /t/{code} URL。
--   - bound_binding_revision / verified_binding_revision = 绑定确认与最后实测时
--     触点的绑定版本;与 touchpoint_aliases.binding_revision 漂移(换绑/换域)时
--     展示面如实暴露 stale,绝不沿用旧「已验证」标识冒充本次验证。
--   - 部分唯一索引 = 一个触点同时至多一条活交付;补换让位(superseded 行不在闸内)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS touchpoint_deliveries (
    id                        TEXT PRIMARY KEY,
    tenant_id                 TEXT NOT NULL REFERENCES tenants(id),
    store_id                  TEXT NOT NULL REFERENCES stores(id),
    touchpoint_id             TEXT NOT NULL REFERENCES touchpoint_aliases(id),
    -- 硬件订单引用(HUI-4591 billing_orders.id):付款步事实的只读前置;
    -- 补换行可继承原单(售后换卡)或引用新单。
    hardware_order_id         TEXT NOT NULL REFERENCES billing_orders(id),
    delivery_ref              TEXT NOT NULL DEFAULT '',
    physical_unit_ref         TEXT NOT NULL DEFAULT '',
    tag_batch                 TEXT NOT NULL DEFAULT '',
    -- awaiting_dispatch → dispatched → bound → written → readback → accepted
    -- → delivered;superseded = 已被补换新行取代(封存,只读)。
    status                    TEXT NOT NULL DEFAULT 'awaiting_dispatch' CHECK (status IN
        ('awaiting_dispatch', 'dispatched', 'bound', 'written', 'readback', 'accepted', 'delivered', 'superseded')),
    bound_binding_revision    INTEGER NOT NULL DEFAULT 0 CHECK (bound_binding_revision >= 0),
    verified_binding_revision INTEGER NOT NULL DEFAULT 0 CHECK (verified_binding_revision >= 0),
    write_evidence_ref        TEXT NOT NULL DEFAULT '',
    readback_evidence_ref     TEXT NOT NULL DEFAULT '',
    device_evidence_ref       TEXT NOT NULL DEFAULT '',
    replacement_of            TEXT REFERENCES touchpoint_deliveries(id),
    created_by                TEXT NOT NULL DEFAULT '',
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_touchpoint_deliveries_tenant
    ON touchpoint_deliveries(tenant_id, store_id, touchpoint_id);

-- 一个触点同时至多一条活交付(应用层创建闸的库层兜底;补换 superseded 让位)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_touchpoint_deliveries_live
    ON touchpoint_deliveries(tenant_id, touchpoint_id) WHERE status != 'superseded';

CREATE INDEX IF NOT EXISTS idx_touchpoint_deliveries_replacement
    ON touchpoint_deliveries(replacement_of);
