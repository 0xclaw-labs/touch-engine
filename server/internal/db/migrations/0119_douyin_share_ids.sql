-- HUI-4585 抖音视频适配:官方 H5 share_id 服务器映射(0119)。
--
-- 票面:在服务器创建本次 share_id、绑定内容/动作/账号授权上下文,处理有效期。
-- share_id 是抖音官方回执关联的外部键:平台回执/查询只认 share_id,内部
-- handoff id 由本表的服务器映射还原(provider_ref 纪律——供应商回调中没有的
-- 内部标识由此可信映射,不能要求顾客补填后相信)。
--
-- 每个交接至多一个活跃 share_id(handoff_id 唯一):重试/刷新复用同一
-- share_id,过期作废后由 adapter 重铸新行(旧行保留作审计,不复活)。

CREATE TABLE douyin_share_ids (
    id                TEXT PRIMARY KEY,                 -- phdsi_* (adapter 铸造)
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    handoff_id        TEXT NOT NULL UNIQUE,             -- publish_handoffs.id(唯一写面纪律:应用层维护引用)
    share_id          TEXT NOT NULL,                    -- 抖音官方关联键(本服务器生成)
    content_version   TEXT NOT NULL DEFAULT '',         -- 交接冻结的快照指纹(换稿=旧 share_id 不认)
    action_id         TEXT NOT NULL DEFAULT '',
    action_session_id TEXT NOT NULL DEFAULT '',
    app_ref           TEXT NOT NULL DEFAULT '',         -- 本部署官方应用身份(错 app 拒绝)
    created_at        TEXT NOT NULL,
    expires_at        TEXT NOT NULL,                    -- 有效期(adapter 每次使用前核对)
    revoked_at        TEXT NOT NULL DEFAULT ''          -- 显式作废(换内容/取消交接)
);

CREATE INDEX idx_douyin_share_ids_tenant ON douyin_share_ids(tenant_id, created_at);
CREATE INDEX idx_douyin_share_ids_share ON douyin_share_ids(share_id);
