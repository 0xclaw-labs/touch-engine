-- HUI-4593:授权域审计(authz/members 域全部特权写面留痕)。
-- 纪律:
--   - append-only:只有 INSERT,无 UPDATE/DELETE 面;解除/撤销本身也是新行;
--   - 每行必备:操作者(principal+成员行+角色)/时间/动作/目标;前后值用 JSON
--     文本快照;before_json 的字面量 "null" = 新建无前值(显式 null 语义,
--     与空对象 {} = 前值为空集 相区分);原因 reason 由写面强制(新特权面必填);
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

-- HUI-4593 修复轮 2(F2):店员动作级显式授权(授权→访问→撤回→同会话 403 的存储半边)。
-- 纪律:
--   - 只授店员(role=staff,应用层强制);组织负责人/门店经理/代理走各自既有模型
--     (owner=治理面全集、manager=本店维度、agent=代管 grant),不在此表混用;
--   - 白名单 = storefront:*/action:* 冻结权限串(store 层最后一道闸);
--     billing/ops 永不经此面(ops 只认受信服务端 grant,pay 走公共 payer 权威);
--   - 现行态 = revoked_at IS NULL(部分唯一索引:同成员同权限至多一条现行);
--     撤销 = 墓碑,再授权 = 新行(全程可回查);每次变更同事务落审计;
--   - 撤回即时生效:access/effective 每请求实时读取,同会话下一请求即失权;
--   - 门店范围沿用成员行 store_scope 语义(带 scope 店员 = 授权仅本店)。

CREATE TABLE IF NOT EXISTS member_permission_grant (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    member_id  TEXT NOT NULL REFERENCES members(id),
    permission TEXT NOT NULL,
    granted_by TEXT NOT NULL DEFAULT '',
    granted_at TEXT NOT NULL,
    revoked_by TEXT NOT NULL DEFAULT '',
    revoked_at TEXT,
    reason     TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_member_perm_grant_active
    ON member_permission_grant(member_id, permission) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_member_perm_grant_tenant ON member_permission_grant(tenant_id, member_id);
