-- HUI-4664 / ADV-9 PR12: 原单 checkout 引用映射(0139;R3 §2.1 草案的任务适配版)。
-- 纪律:
--   - checkout_ref 语义上由公共结算侧生成(上游 PR-4 到位后透传);本表是
--     可重建投影,不是支付权威。上游未接入期间 Touch 按 R3 §6.1 draft 先行
--     本地铸造并标注对齐点,上游到位后零表结构改动换源。
--   - UNIQUE(tenant_id, order_ref) = 同单恢复复用同一引用(同单重试只读回
--     同一行,不产生第二个支付意图);UNIQUE(checkout_ref) 兜住公共侧引用被
--     错误复用到他单/他租户的情形。
--   - order_ref 是原单引用(order 语义键):制作接续 = handoff id(cph_…);
--     未来订阅/硬件原单接续 = billing_orders id(bo_…)——同一恢复域。
--   - launch_url 永不落库:URL 只由 seam 配置派生或在响应中即时给出;
--     落库即制造可重放凭据。
--   - 本表 state 是工程投影:checkout_pending → paid → activated;
--     paid 而 activation 失败 = failed(如实留因,可重试激活)。
--     支付/开通事实推进唯一入口仍是受信权威缝(billing_authority 服务身份)。
--   - 幂等键贯穿:同 payment_ref 重放零新增零改写(payment_ref 列比对)。

CREATE TABLE IF NOT EXISTS billing_checkout_refs (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    handoff_id   TEXT NOT NULL DEFAULT '',
    quote_ref    TEXT NOT NULL DEFAULT '',
    checkout_ref TEXT NOT NULL,
    order_ref    TEXT NOT NULL,
    state        TEXT NOT NULL DEFAULT 'checkout_pending' CHECK (state IN
        ('checkout_pending','paid','activated','failed')),
    amount_minor INTEGER NOT NULL CHECK (amount_minor >= 0),
    currency     TEXT NOT NULL DEFAULT '',
    payer_ref    TEXT NOT NULL DEFAULT '',
    payment_ref  TEXT NOT NULL DEFAULT '',
    fail_reason  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (tenant_id, order_ref),
    UNIQUE (checkout_ref)
);

CREATE INDEX IF NOT EXISTS idx_checkout_refs_handoff
    ON billing_checkout_refs(tenant_id, handoff_id, state);
