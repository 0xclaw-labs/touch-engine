-- 真实零成本与尚未录入分开，旧0无法证明已录；权益冻结发放来源事实。
ALTER TABLE reward_inventory ADD COLUMN cost_recorded INTEGER NOT NULL DEFAULT 0 CHECK(cost_recorded IN (0,1));
ALTER TABLE reward_benefits ADD COLUMN cost_recorded INTEGER NOT NULL DEFAULT 0 CHECK(cost_recorded IN (0,1));
UPDATE reward_inventory SET cost_recorded=1 WHERE cost_minor>0;
UPDATE reward_benefits SET cost_recorded=1 WHERE cost_minor>0;
