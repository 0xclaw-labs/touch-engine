-- HUI-4593:受信服务端平台运营 grant(ops 维度的唯一权威来源)。
-- 纪律:
--   - 平台 ops grant 必须来自可信服务端身份/授权源(受信服务生产者缝,
--     X-Service-ID + TOUCH_SERVICE_TOKENS),并验证签发/有效期/撤销;
--     商家 /admin/members、代理接口、用户输入永不自授(应用层强制);
--   - 不创建第二套账号/密码:principal_ref 是既有 identity 主体,行只是 grant;
--   - 不把 org_owner 当全平台管理员:商家角色对 ops 维度恒为零;
--   - expires_at 空 = 不过期;撤销 = 墓碑(revoked_at/revoked_by),绝不物理删除;
--   - commands:JSON 数组的 ops 命令串(命令集由 HUI-4592 契约冻结,本层只
--     校验命令是否在有效 grant 内,不解释命令语义)。

CREATE TABLE IF NOT EXISTS ops_grants (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    principal_ref    TEXT NOT NULL,
    store_scope_set  TEXT NOT NULL DEFAULT '',
    commands         TEXT NOT NULL DEFAULT '[]',
    issued_by_service TEXT NOT NULL,
    issued_at        TEXT NOT NULL,
    expires_at       TEXT NOT NULL DEFAULT '',
    revoked_by       TEXT NOT NULL DEFAULT '',
    revoked_at       TEXT
);

CREATE INDEX IF NOT EXISTS idx_ops_grants_principal ON ops_grants(principal_ref, tenant_id);
CREATE INDEX IF NOT EXISTS idx_ops_grants_tenant ON ops_grants(tenant_id, issued_at);
