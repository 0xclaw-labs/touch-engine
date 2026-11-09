-- HUI-4591:硬件订单履约五状态与退款发票对账(0112)。
-- 纪律(裁定书):
--   - 硬件订单(F14)=一次性商品/服务明细(billing_hardware_items);
--     付款/出库/写卡/回读/现场验收五状态独立记录,每步带操作者/时间/设备
--     标识,不可跳跃(写卡前必须已出库,回读前必须已写卡,验收前必须已回读);
--     交付状态机应用侧归 HUI-1663,本表是订单侧可重建投影(受信事实写入)。
--   - 退款发票对账(F19):原订单/退款金额/原因/开票主体/发票状态/审核人逐
--     字段落库;UNIQUE(tenant_id, source, event_id) 重复回调不重复退款;
--     金额不超原订单未退余额(store 层守卫);核销(活动账)与现金账分开,
--     本表永不触碰 activity_benefit_facts。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS billing_hardware_items (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    order_id          TEXT NOT NULL REFERENCES billing_orders(id),
    sku               TEXT NOT NULL DEFAULT '',
    name              TEXT NOT NULL,
    -- goods = 一次性商品 | service = 一次性服务
    kind              TEXT NOT NULL CHECK (kind IN ('goods', 'service')),
    qty               INTEGER NOT NULL CHECK (qty > 0),
    unit_amount_minor INTEGER NOT NULL CHECK (unit_amount_minor >= 0),
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_billing_hardware_items_order
    ON billing_hardware_items(tenant_id, order_id);

CREATE TABLE IF NOT EXISTS billing_hardware_fulfillment (
    order_id              TEXT PRIMARY KEY REFERENCES billing_orders(id),
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    -- 五状态:pending(未发生)|done(已完成);每步独立事实(操作者/时间/设备)。
    payment_state         TEXT NOT NULL DEFAULT 'pending' CHECK (payment_state IN ('pending', 'done')),
    shipment_state        TEXT NOT NULL DEFAULT 'pending' CHECK (shipment_state IN ('pending', 'done')),
    write_card_state      TEXT NOT NULL DEFAULT 'pending' CHECK (write_card_state IN ('pending', 'done')),
    readback_state        TEXT NOT NULL DEFAULT 'pending' CHECK (readback_state IN ('pending', 'done')),
    acceptance_state      TEXT NOT NULL DEFAULT 'pending' CHECK (acceptance_state IN ('pending', 'done')),
    payment_operator      TEXT NOT NULL DEFAULT '',
    payment_at            TEXT NOT NULL DEFAULT '',
    payment_device_ref    TEXT NOT NULL DEFAULT '',
    shipment_operator     TEXT NOT NULL DEFAULT '',
    shipment_at           TEXT NOT NULL DEFAULT '',
    shipment_device_ref   TEXT NOT NULL DEFAULT '',
    write_card_operator   TEXT NOT NULL DEFAULT '',
    write_card_at         TEXT NOT NULL DEFAULT '',
    write_card_device_ref TEXT NOT NULL DEFAULT '',
    readback_operator     TEXT NOT NULL DEFAULT '',
    readback_at           TEXT NOT NULL DEFAULT '',
    readback_device_ref   TEXT NOT NULL DEFAULT '',
    acceptance_operator   TEXT NOT NULL DEFAULT '',
    acceptance_at         TEXT NOT NULL DEFAULT '',
    acceptance_device_ref TEXT NOT NULL DEFAULT '',
    updated_at            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS billing_refunds (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    order_id       TEXT NOT NULL REFERENCES billing_orders(id),
    refund_ref     TEXT NOT NULL,
    amount_minor   INTEGER NOT NULL CHECK (amount_minor > 0),
    currency       TEXT NOT NULL DEFAULT '',
    reason         TEXT NOT NULL,
    invoice_title  TEXT NOT NULL DEFAULT '',
    -- none | pending | issued | void(发票状态,权威事实投影)
    invoice_status TEXT NOT NULL DEFAULT 'none' CHECK (invoice_status IN ('none', 'pending', 'issued', 'void')),
    invoice_ref    TEXT NOT NULL DEFAULT '',
    auditor        TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL,
    event_id       TEXT NOT NULL,
    payload        TEXT NOT NULL DEFAULT '{}',
    occurred_at    TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    UNIQUE (tenant_id, source, event_id)
);

CREATE INDEX IF NOT EXISTS idx_billing_refunds_order
    ON billing_refunds(tenant_id, order_id);
