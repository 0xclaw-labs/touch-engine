-- HUI-4594: 撤回水位。撤销发生时把当时的源版本水位(source_version+1,与 revoke
-- 事实的 source_version 一致)冻结在本列:
--   - 取数面: 水位 >0 的线索只回 tombstone(零联系方式),取数即重新核同意;
--   - ACK 面: 已撤销线索收到 stored-ACK(晚到重放的提交事实)一律拒绝,
--     晚到重放不能复活已撤回联系人;
--   - 0 = 从未撤销。

ALTER TABLE lead_submissions ADD COLUMN revoke_source_revision INTEGER NOT NULL DEFAULT 0;
