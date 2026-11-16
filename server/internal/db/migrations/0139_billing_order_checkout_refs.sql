-- HUI-4664:原单收银接续的 checkout 引用与受控返回上下文(0139)。
-- 纪律(checkout-implementation-feasibility.md 三/四节):
--   - 本迁移只落 Touch 侧持久化骨架:保存公共收银台签发的 checkout 引用、
--     状态、期限、请求者与受控返回上下文。字段的**写入来源**只能是公共
--     权威侧 checkout producer 合同(第四节 1-5 项)落地后的受信 adapter;
--     浏览器返回、success query、本地推算都不得写这些列。
--   - checkout_ref 语义=公共权威的支付引用(可恢复、可查单);在 producer
--     合同建立前恒为空,收银保持 checkout_unwired(fail-closed)。
--   - return_context 是受控返回上下文的存储位:绑定原 tenant/order/允许
--     目标;返回只触发权威回查,不因返回参数推进 paid/active。
--   - 同 (tenant_id, checkout_ref) 可查回原单:请求已成但响应丢失后,
--     同 key 重试查回同一支付意图,不二次购买。

ALTER TABLE billing_orders ADD COLUMN checkout_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_orders ADD COLUMN checkout_status TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_orders ADD COLUMN checkout_expires_at TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_orders ADD COLUMN checkout_requested_by TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_orders ADD COLUMN return_context TEXT NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_billing_orders_checkout_ref
    ON billing_orders(tenant_id, checkout_ref) WHERE checkout_ref != '';
