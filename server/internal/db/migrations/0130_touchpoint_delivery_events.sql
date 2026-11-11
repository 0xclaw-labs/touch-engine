-- HUI-1663:交付事实事件账本(0130)——只追加,每步独立事实记录。
--
-- 纪律(票面「事件幂等/只追加」+ SHARED-BRIEF 事件通用语义):
--   - event_type 白名单 = 七步事实(出库/绑定/写入/回读/现场验收/交付)+补换;
--     付款步事实归 4591 订单投影,不在本账本重复建档(只读消费)。
--   - UNIQUE(tenant_id, source, event_id):同键同负载 = 幂等读回(不推进两次);
--     同键异负载 = IDEMPOTENCY_CONFLICT(应用层按 canonical payload 判等)。
--   - readback 事件 payload 携带 {read_url, expected_url, matches}:回读一致性
--     核对记录;matches=false 只记事实不推进状态(应用层裁决)。
--   - observed_binding_revision = 事实发生时实测到的触点绑定版本(stale 判据)。
--   - operator_ref/device_ref:操作者从受信会话解析,device_ref 是现场设备标识
--     (事实记录输入,不是授权凭据)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS touchpoint_delivery_events (
    id                        TEXT PRIMARY KEY,
    tenant_id                 TEXT NOT NULL REFERENCES tenants(id),
    delivery_id               TEXT NOT NULL REFERENCES touchpoint_deliveries(id),
    event_type                TEXT NOT NULL CHECK (event_type IN
        ('dispatched', 'bound', 'written', 'readback', 'accepted', 'delivered', 'replaced')),
    source                    TEXT NOT NULL DEFAULT 'merchant',
    event_id                  TEXT NOT NULL,
    operator_ref              TEXT NOT NULL DEFAULT '',
    device_ref                TEXT NOT NULL DEFAULT '',
    evidence_ref              TEXT NOT NULL DEFAULT '',
    observed_binding_revision INTEGER NOT NULL DEFAULT 0,
    payload                   TEXT NOT NULL DEFAULT '{}',
    occurred_at               TEXT NOT NULL,
    created_at                TEXT NOT NULL,
    UNIQUE (tenant_id, source, event_id)
);

CREATE INDEX IF NOT EXISTS idx_touchpoint_delivery_events_delivery
    ON touchpoint_delivery_events(delivery_id, occurred_at, id);
