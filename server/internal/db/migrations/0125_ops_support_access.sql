-- HUI-4592:受控支持访问(限时+指定目标+原因,进入后仍按授权作用域)。
-- 纪律:
--   - expires_at NOT NULL:支持访问必须限时,无"永久支持"形态(TTL 上限
--     由 ops 域包裁决,本层结构拒绝无界行);
--   - 每请求实时校验(到期/closed 立即停止),关闭 = 墓碑(closed_*),
--     绝不物理删除——全程可回查、可审计;
--   - 不发明第二套身份:principal_ref 是既有 identity 主体,行只是
--     一个限时作用域授权;目标租户显式指定,不因同品牌跨商家放大。

CREATE TABLE IF NOT EXISTS ops_support_access (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    principal_ref TEXT NOT NULL,
    reason       TEXT NOT NULL,
    opened_at    TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    closed_by    TEXT NOT NULL DEFAULT '',
    closed_at    TEXT NOT NULL DEFAULT '',
    close_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ops_support_principal ON ops_support_access(principal_ref, tenant_id);
CREATE INDEX IF NOT EXISTS idx_ops_support_tenant ON ops_support_access(tenant_id, opened_at);
