-- HUI-1663:补换审计(0131)。
--
-- 纪律(裁定书「补换原因与审计」+ 票面补换反例):
--   - reason_category 二值区分可回收损坏与遗失失控:
--       damaged_recoverable  = 换载体保留逻辑入口(同触点换物理卡,URL 不变);
--       lost_uncontrolled    = 遗失失控,必须撤销旧入口(停用旧触点+新卡新触点新码)。
--   - old_entry_revoked = 旧 /t 入口是否已停用(lost 分支恒 1);「遗失卡不能在
--     补换后继续成为有效入口」由此列审计兜底。
--   - 旧顾客参与/已发权益不随补换丢失:本表与补换流程零触碰 participations
--     (1665 语义:参与挂活动不挂卡)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS touchpoint_delivery_replacements (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    old_delivery_id   TEXT NOT NULL REFERENCES touchpoint_deliveries(id),
    new_delivery_id   TEXT NOT NULL REFERENCES touchpoint_deliveries(id),
    old_touchpoint_id TEXT NOT NULL,
    new_touchpoint_id TEXT NOT NULL,
    reason_category   TEXT NOT NULL CHECK (reason_category IN
        ('damaged_recoverable', 'lost_uncontrolled')),
    reason_detail     TEXT NOT NULL DEFAULT '',
    old_entry_revoked INTEGER NOT NULL DEFAULT 0 CHECK (old_entry_revoked IN (0, 1)),
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_touchpoint_delivery_replacements_old
    ON touchpoint_delivery_replacements(tenant_id, old_delivery_id);
