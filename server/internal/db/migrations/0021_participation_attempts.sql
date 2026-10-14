-- HUI-3546 碰一碰门店交付 A2: 参与尝试(attempt)。
-- 纪律:
--   - 内容预览可换选;真正交接时把该 attempt 的 content_pack_version 与
--     当时的 rule_version 冻结在本行,后续改稿不改写已冻结行;
--   - 换稿/换平台 = 新 attempt;新 attempt 不新增 benefit_slot
--     (槽位在 participations 上,一参与一槽,只在调用 G3 预留时传递);
--   - 发布证据/官方回执/奖励账归 HUI-1670/1671 域,本表不收 post_id、
--     不收回执、不写发放结果。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS participation_attempts (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    participation_id     TEXT NOT NULL REFERENCES participations(id),
    platform             TEXT NOT NULL DEFAULT '',
    handoff_method       TEXT NOT NULL CHECK (handoff_method IN ('h5', 'platform_miniprogram', 'staff_view')),
    content_pack_version TEXT NOT NULL DEFAULT '',
    rule_version         INTEGER NOT NULL,
    status               TEXT NOT NULL CHECK (status IN ('prepared', 'handed_off', 'completed', 'canceled')),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_participation_attempts_participation
    ON participation_attempts(participation_id, created_at);
