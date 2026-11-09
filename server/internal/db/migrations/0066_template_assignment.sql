-- HUI-4593:连锁模板分发引用(组织级 storefront 模板 → 成员门店继承+局部覆盖)。
-- 纪律:
--   - 本表只记录「分发与授权结果」:谁把哪个模板版本授给哪家店、允许覆盖哪些
--     字段;不写 storefront 版本表,不承载模板内容(应用委托 HUI-1676 领域服务
--     写草稿;发布仍走 1676 预检/确认/回滚);
--   - 仅源模板变更不静默覆盖门店已发布版本 —— 分发行不触达任何已发布状态,
--     status=applied 只在 1676 应用后由 PATCH 回填 applied_revision;
--   - 幂等重放:带 request_key 的同一分发请求(同租户+同店+同模板+同版本+同 key)
--     唯一索引兜底,重试不重复创建;无 key 的重复分发合法(部分唯一索引);
--   - 门店目标/账号/二维码/Wi-Fi 密码永不跨店盲目复制:allowed_override_fields
--     的保留字段拒绝在应用层(store_target/account/qr_code/wifi_password);
--   - status 生命周期:distributed(已下发)→ applied(门店已应用) / cancelled(总部撤回)。

CREATE TABLE IF NOT EXISTS template_assignment (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id),
    store_id                TEXT NOT NULL REFERENCES stores(id),
    template_ref            TEXT NOT NULL,
    source_version          TEXT NOT NULL,
    allowed_override_fields TEXT NOT NULL DEFAULT '[]',
    request_key             TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'distributed'
                                CHECK (status IN ('distributed','applied','cancelled')),
    applied_revision        TEXT NOT NULL DEFAULT '',
    created_by              TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    UNIQUE (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_template_assignment_replay
    ON template_assignment(tenant_id, store_id, template_ref, source_version, request_key)
    WHERE request_key <> '';

CREATE INDEX IF NOT EXISTS idx_template_assignment_tenant ON template_assignment(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_template_assignment_store ON template_assignment(store_id, status);
