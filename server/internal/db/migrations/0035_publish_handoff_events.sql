-- HUI-1670: 顾客发布交接——只追加证据事件表。
-- Additive only。纪律(design spec Q4/Q8):
--   - 只追加:created|状态迁移|核验请求|人工事实|官方回执全部是事件,
--     永不改写删除(处理记录是审计事实;人工永不转官方);
--   - kind 白名单 CHECK:
--       state_changed            状态迁移(带 from/to 于 payload)
--       customer_done_requested  顾客"我已完成"(仅核验请求,不是事实)
--       staff_saw_submit         店员目测提交(人工事实一)
--       staff_verified_work      店员核对作品(人工事实二)
--       official_confirmed       官方确认(事实三;仅验签通过到达)
--       editor_open_proved       可证实的编辑器打开(verifier 判定 OK)
--       receipt_rejected         回执被拒(错签名/错绑定/超时乱序回查)
--       canceled                 顾客/店员取消
--   - receipt_id 部分唯一索引:同一官方回执只允许一条 official_confirmed
--     (重放幂等、绝不双计);被拒回执(receipt_rejected)允许在状态推进后
--     重新评估(=回查语义),同 ID 多条拒绝留痕不冲突;
--   - payload JSON 记 from/to/原因等,白名单字段,无凭据明文。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS publish_handoff_events (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    handoff_id      TEXT NOT NULL REFERENCES publish_handoffs(id),
    kind            TEXT NOT NULL CHECK (kind IN
        ('state_changed','customer_done_requested','staff_saw_submit',
         'staff_verified_work','official_confirmed','editor_open_proved',
         'receipt_rejected','canceled')),
    actor           TEXT NOT NULL DEFAULT '',
    evidence_source TEXT NOT NULL DEFAULT '',
    receipt_id      TEXT NOT NULL DEFAULT '',
    payload         TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_phe_receipt
    ON publish_handoff_events(tenant_id, receipt_id)
    WHERE receipt_id != '' AND kind = 'official_confirmed';
CREATE INDEX IF NOT EXISTS idx_phe_handoff
    ON publish_handoff_events(tenant_id, handoff_id, created_at);
