-- HUI-4664: 原单收银接续与公共 Mint/Inspect 收敛的安全就地升级(0141)。
--
-- 纪律(不破坏既有 0110/0139 数据):
--   - 纯追加 ALTER TABLE ADD COLUMN + 新索引;零 DROP TABLE、零清库、零改写旧行。
--   - billing_checkout_refs 保留既有本地稳定引用 checkout_ref(ck_*, 权威事件主路由键),
--     新增 remote_checkout_ref(公共侧 Mint 返回的 tco_*), 彻底消除 main 0139 与
--     旧候选 0140 的表结构冲突。
--   - code_url / launch_url / return_url 永不落库(仅由公共 Mint/Inspect 响应即时给出)。
--   - billing_orders 追加下单冻结的 pricing_version 与 payer_grant_revision 快照列,
--     老行保持默认空/0,绝不按新目录重算历史订单。

PRAGMA foreign_keys = ON;

ALTER TABLE billing_checkout_refs ADD COLUMN order_kind TEXT NOT NULL DEFAULT 'content_production';
ALTER TABLE billing_checkout_refs ADD COLUMN remote_checkout_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN out_trade_no TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN remote_status TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN return_target TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN price_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN intent_key TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN pricing_version TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN payer_grant_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_checkout_refs ADD COLUMN payer_grant_revision INTEGER NOT NULL DEFAULT 0;

ALTER TABLE billing_orders ADD COLUMN pricing_version TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_orders ADD COLUMN payer_grant_revision INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_checkout_refs_remote
    ON billing_checkout_refs(tenant_id, remote_checkout_ref)
    WHERE remote_checkout_ref != '';

CREATE INDEX IF NOT EXISTS idx_checkout_refs_order_kind
    ON billing_checkout_refs(tenant_id, order_kind, state);
