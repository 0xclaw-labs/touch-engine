-- HUI-4593:授权域审计(authz/members 域全部特权写面留痕)。
-- 纪律:
--   - append-only:只有 INSERT,无 UPDATE/DELETE 面;解除/撤销本身也是新行;
--   - 每行必备:操作者(principal+成员行+角色)/时间/动作/目标;前后值用 JSON
--     文本快照(空对象 = 无前值);原因 reason 由写面强制(新特权面必填);
--   - 本表是成员授权/代理协作/模板分发/ops grant 的统一审计汇,不是业务事件流
--     (业务事件归 0041 touchpoint_events 一线)。

CREATE TABLE IF NOT EXISTS authz_audit_log (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    actor_principal TEXT NOT NULL,
    actor_member_id TEXT NOT NULL DEFAULT '',
    actor_role      TEXT NOT NULL DEFAULT '',
    action          TEXT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    before_json     TEXT NOT NULL DEFAULT '{}',
    after_json      TEXT NOT NULL DEFAULT '{}',
    reason          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_authz_audit_tenant_time ON authz_audit_log(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_authz_audit_target ON authz_audit_log(tenant_id, target_type, target_id);
