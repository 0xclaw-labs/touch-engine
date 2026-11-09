-- HUI-1671: 奖励规则的动作绑定字段(兼容增量,全部带缺省值,旧行不受影响)。
-- action_id / action_revision 是规则冻结引用的动作(A01-A30 目录,由 3535/4584
-- 维护);reward_eligibility_ref 记动作在准入域(3535)的奖励资格投影引用。
-- 点评类动作 reward_eligibility=false 是准入权威;本域在创建与发放两侧同样
-- 硬拒(双保险),拒绝码 reward_not_eligible。
-- rule_freeze_ref 是被告知的规则冻结版本引用(G3 不编辑规则,只存审计锚)。

ALTER TABLE reward_inventory ADD COLUMN action_id            TEXT NOT NULL DEFAULT '';
ALTER TABLE reward_inventory ADD COLUMN action_revision      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE reward_inventory ADD COLUMN action_kind          TEXT NOT NULL DEFAULT '';
ALTER TABLE reward_inventory ADD COLUMN reward_eligibility_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE reward_inventory ADD COLUMN reward_eligible      TEXT NOT NULL DEFAULT ''
    CHECK (reward_eligible IN ('', 'yes', 'no'));
ALTER TABLE reward_inventory ADD COLUMN rule_freeze_ref      TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_reward_inventory_action
    ON reward_inventory(tenant_id, action_id, action_revision);
