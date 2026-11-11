-- HUI-4587 快手适配:OAuth state 与服务端 token(0122)。
--
-- 票面:OAuth 回调 state 绑定 action_session/handoff/目标 App/一次流程;token
-- 仅服务器保存。state 一次性(used_at 非空即拒重放);过期拒绝;错 App/错会话
-- 拒绝。token 表服务端保管(access/refresh 不出服务器;客户端永无 secret)。
--
-- 残差声明:access_token 以明文存于本部署 SQLite——生产部署应换托管密钥库
-- (KMS);当前单机部署面如实挂账,不在本票伪造加密。

CREATE TABLE kuaishou_oauth_states (
    state             TEXT PRIMARY KEY,               -- 128-bit 随机(adapter 铸造)
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    handoff_id        TEXT NOT NULL,
    action_session_id TEXT NOT NULL,
    app_ref           TEXT NOT NULL,                  -- 目标 App(错 App 拒绝)
    scope             TEXT NOT NULL DEFAULT '',       -- 已申请且适用的授权范围
    return_to         TEXT NOT NULL DEFAULT '',       -- 同源 https 回跳地址(Prepare 校验过形状)
    created_at        TEXT NOT NULL,
    expires_at        TEXT NOT NULL,
    used_at           TEXT NOT NULL DEFAULT ''        -- 一次性:非空即拒重放
);

CREATE INDEX idx_kuaishou_oauth_states_handoff ON kuaishou_oauth_states(tenant_id, handoff_id);

CREATE TABLE kuaishou_oauth_tokens (
    id                 TEXT PRIMARY KEY,              -- phkt_* (adapter 铸造)
    tenant_id          TEXT NOT NULL REFERENCES tenants(id),
    handoff_id         TEXT NOT NULL UNIQUE,
    state              TEXT NOT NULL,                 -- 换发所用的一次性 state
    open_id            TEXT NOT NULL DEFAULT '',      -- 平台侧账号(服务端确定账号,不信客户端)
    access_token       TEXT NOT NULL,                 -- 仅服务器保存
    refresh_token      TEXT NOT NULL DEFAULT '',
    expires_at         TEXT NOT NULL,
    scope              TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    revoked_at         TEXT NOT NULL DEFAULT ''       -- 顾客取消授权/换账号时作废
);

CREATE INDEX idx_kuaishou_oauth_tokens_tenant ON kuaishou_oauth_tokens(tenant_id, created_at);
