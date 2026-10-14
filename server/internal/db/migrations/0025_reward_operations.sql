-- HUI-1671 B3: 审计流水,append-only。reserve/issue/redeem/reverse/expire
-- 各自成行,永不删除、永不改写历史;更正走 reverse 追加行。
-- dedup_key 是调用方事件键(证据事件/确认事件),部分唯一索引保证同租户内
-- 同一事件只处理一次;簿记行(fulfill 同事务第二条、系统过期)留空不占键。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS reward_operations (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    benefit_id TEXT NOT NULL REFERENCES reward_benefits(id),
    op         TEXT NOT NULL CHECK (op IN ('reserve', 'issue', 'redeem', 'reverse', 'expire')),
    dedup_key  TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL DEFAULT '',
    store_id   TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_reward_operations_dedup
    ON reward_operations(tenant_id, dedup_key) WHERE dedup_key <> '';
CREATE INDEX IF NOT EXISTS idx_reward_operations_benefit
    ON reward_operations(benefit_id, created_at);

-- 官方发布事实(仅官方模式奖励的锚点)。0010 把 customer_publish_attempts 的
-- 回执字段锁死为空(本部署渠道无法证明发布成功),所以官方事实只能落在
-- reward 域自己的审计表里:由受信官方查询集成写入(今天不存在,故为零行,
-- 仅官方活动诚实地等待);客户端口头的回执永不入表。attempt_id 只引用
-- 本租户本活动真实存在的发布尝试行。UNIQUE 保证一个尝试最多被采信一次。
CREATE TABLE IF NOT EXISTS reward_official_facts (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    campaign_id      TEXT NOT NULL REFERENCES campaigns(id),
    attempt_id       TEXT NOT NULL REFERENCES customer_publish_attempts(id),
    platform         TEXT NOT NULL,
    platform_post_id TEXT NOT NULL,
    receipt_source   TEXT NOT NULL CHECK (receipt_source = 'official_query'),
    consumed_by      TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, attempt_id)
);
