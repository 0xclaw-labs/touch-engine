-- HUI-4595 (TOUCH-ALL-20261109): 门店真实内容制作交接(公共任务与计费的发起面)。
-- Additive only:既有表零改动(0030 content_handoffs 旧活动域 brief 与 0042
-- 队列投影继续原样服务,互不搬动)。纪律:
--   - 制作走公共任务权威(派诺生态 Task),本仓不自建制作队列:本表只持久化
--     商家「已确认报价后明确提交」的制作请求与真实引用投影(task_ref 来自
--     平台任务权威的提交回执;未接通时如实为空,绝不伪造任务号);
--   - 内容归 tenant/store,campaign 可选(经 store_content_packs.campaign_id
--     间接表达,本表不存 campaign 列,不为无活动门店伪建活动);
--   - 首次制作合法:pack 可以是零候选/零采用的新包(空包→制作是合法路径,
--     T05),content_binding_ref 可空(采用回流成片后由 4584 写侧建立绑定);
--   - 反复恢复同一制作请求不创建新任务或重复扣费:(tenant_id, idempotency_key)
--     唯一,同键重放返回同一行;
--   - 状态独立可辨(票面「处理中/失败/取消/已产出独立」):recorded=已登记未
--     提交(任务权威未接通的诚实态)/submitted=已提交平台任务(处理中)/
--     produced=已产出(至少一次 produced 回流)/failed/canceled=生产方受信回执;
--   - quote_ref/charge_ref 是 4591 AI 用量投影的引用来源:本表只存引用与
--     核验事实,不存金额、不建钱包、不自增余额(账单权威=生态统一账单)。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS store_content_production_handoffs (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    store_id            TEXT NOT NULL REFERENCES stores(id),
    pack_id             TEXT NOT NULL REFERENCES store_content_packs(id),
    content_binding_ref TEXT NOT NULL DEFAULT '',
    quote_ref           TEXT NOT NULL,
    quote_verified      INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'recorded'
                        CHECK (status IN ('recorded','submitted','produced','failed','canceled')),
    task_ref            TEXT NOT NULL DEFAULT '',
    last_charge_ref     TEXT NOT NULL DEFAULT '',
    last_result_revision INTEGER NOT NULL DEFAULT 0,
    note                TEXT NOT NULL DEFAULT '',
    idempotency_key     TEXT,
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_store_cph_store ON store_content_production_handoffs(tenant_id, store_id, created_at);
