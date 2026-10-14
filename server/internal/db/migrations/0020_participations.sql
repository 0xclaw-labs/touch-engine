-- HUI-3546 碰一碰门店交付 A2: 参与权威记录(G2 所有,只由本域修改)。
-- 纪律:
--   - 浏览不建参与:本表只在顾客显式开始后才有行;
--   - 同一 (tenant_id, campaign_id, idempotency_key) 重放回原行(幂等);
--   - benefit_slot 一参与一槽:换稿/换平台的新 attempt 不新增槽位;
--   - 未确认预留不得表述为已保留:benefit_reservation_state 只有 confirmed
--     才代表 G3 确认,failed/pending 都不是;
--   - 状态语义分开存,禁单一 success 布尔抹平 canceled/unknown/awaiting_review;
--   - visitor_id 是首方随机续接键(非手机号、非平台 open_id、非可逆 PII);
--   - reward/核销归 HUI-1671,本表只留受信预留引用,不写第二份库存。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS participations (
    id                        TEXT PRIMARY KEY,
    tenant_id                 TEXT NOT NULL REFERENCES tenants(id),
    store_id                  TEXT NOT NULL REFERENCES stores(id),
    campaign_id               TEXT NOT NULL REFERENCES campaigns(id),
    link_code                 TEXT NOT NULL,
    touchpoint_source         TEXT NOT NULL CHECK (touchpoint_source IN ('nfc', 'qr', 'shorturl', 'manual')),
    rule_version              INTEGER NOT NULL,
    content_pack_version      TEXT NOT NULL DEFAULT '',
    visitor_id                TEXT NOT NULL,
    status                    TEXT NOT NULL CHECK (status IN ('pending', 'in_progress', 'awaiting_review', 'fulfilled', 'canceled')),
    idempotency_key           TEXT NOT NULL,
    benefit_slot              TEXT NOT NULL DEFAULT '',
    benefit_reservation_ref   TEXT NOT NULL DEFAULT '',
    benefit_reservation_state TEXT NOT NULL DEFAULT 'not_requested' CHECK (benefit_reservation_state IN ('not_requested', 'pending', 'confirmed', 'failed')),
    fulfillment_ref           TEXT NOT NULL DEFAULT '',
    starts_at                 TEXT NOT NULL,
    expires_at                TEXT NOT NULL,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_participations_visitor
    ON participations(tenant_id, visitor_id, status);
CREATE INDEX IF NOT EXISTS idx_participations_campaign
    ON participations(tenant_id, campaign_id, status, created_at);
