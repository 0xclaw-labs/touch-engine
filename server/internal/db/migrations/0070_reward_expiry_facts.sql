-- HUI-1671: 权益到期事实快照(六种到期字段落库)。
-- 发放/预留时把当时的六个期限分别落库;此后订阅/活动/参与配置再变化,
-- 已发权益仍按快照判定并逐项给出原因(不复用单一 expired 字段)。
-- benefit 未过期 = benefit_until 为空或未过;订阅/活动/参与/渠道到期只停止
-- 新承诺,不拦已发权益的查询/重签/核销(T16)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS reward_expiry_facts (
    benefit_id           TEXT PRIMARY KEY REFERENCES reward_benefits(id),
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    subscription_until   TEXT NOT NULL DEFAULT '',
    campaign_until       TEXT NOT NULL DEFAULT '',
    channel_checked      INTEGER NOT NULL DEFAULT 0,
    channel_open         INTEGER NOT NULL DEFAULT 0,
    participation_until  TEXT NOT NULL DEFAULT '',
    reservation_until    TEXT NOT NULL DEFAULT '',
    benefit_until        TEXT NOT NULL DEFAULT '',
    judged_at            TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reward_expiry_facts_tenant
    ON reward_expiry_facts(tenant_id, benefit_until);
