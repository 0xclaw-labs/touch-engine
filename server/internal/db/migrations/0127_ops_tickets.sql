-- HUI-4592:运行异常工单(定位/责任人/处理操作/结果复查/关闭理由)。
-- 纪律:
--   - 状态机 open→confirmed→resolved→closed,迁移规则由 ops 域包裁决
--     (纯函数),终态不可再改;每步留 actor/时间;
--   - source=probe|manual:探针告警可开单,人工也可开单;
--   - 关闭必带理由(closed_reason 写面强制)——"确认处理"不是静默关单;
--   - tenant_id 可空(''):平台级异常(域名/证书/媒体链)不归属单一商户。

CREATE TABLE IF NOT EXISTS ops_tickets (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL DEFAULT '',
    severity       TEXT NOT NULL DEFAULT 'normal',
    title          TEXT NOT NULL,
    detail         TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'open',
    owner_ref      TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT 'manual',
    opened_at      TEXT NOT NULL,
    opened_by      TEXT NOT NULL,
    confirmed_by   TEXT NOT NULL DEFAULT '',
    confirmed_at   TEXT NOT NULL DEFAULT '',
    resolved_by    TEXT NOT NULL DEFAULT '',
    resolved_at    TEXT NOT NULL DEFAULT '',
    resolution     TEXT NOT NULL DEFAULT '',
    closed_by      TEXT NOT NULL DEFAULT '',
    closed_at      TEXT NOT NULL DEFAULT '',
    closed_reason  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ops_tickets_status ON ops_tickets(status, opened_at);
CREATE INDEX IF NOT EXISTS idx_ops_tickets_tenant ON ops_tickets(tenant_id, opened_at);
