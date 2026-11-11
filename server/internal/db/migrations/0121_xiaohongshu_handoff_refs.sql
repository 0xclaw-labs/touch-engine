-- HUI-4586 小红书适配:JS 媒体交接载荷签发记录(0121)。
--
-- 票面:Prepare 返回 client_action.type=invoke_xhs_js 及 type、images 或
-- video/cover、verifyConfig 中的公开鉴权参数,不返回 AppSecret;自动 SDK 载荷
-- 不发送 title/content/topics。本表记录每次服务器签发的 JS 交接载荷(载荷哈希
-- +媒体引用+公开参数),使弱网重试取回同一载荷、审计可核"载荷里没有文字字段"。
--
-- 无回执契约:本表只有签发记录,没有任何"成功"语义列——JS fail 不等于发布
-- 回执,本地伪 success 不存在入库通道。

CREATE TABLE xiaohongshu_handoff_refs (
    id                TEXT PRIMARY KEY,               -- phxr_* (adapter 铸造)
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    handoff_id        TEXT NOT NULL UNIQUE,
    media_type        TEXT NOT NULL DEFAULT '',       -- video | normal(平台 JS 语义)
    payload_sha256    TEXT NOT NULL DEFAULT '',       -- 签发给客户端的载荷指纹(审计:不含文字字段)
    media_refs_json   TEXT NOT NULL DEFAULT '[]',     -- 有序受控媒体引用(铸造时快照,不含内部 token 明文)
    public_params_json TEXT NOT NULL DEFAULT '{}',    -- verifyConfig 公开参数(AppID 级,永无 secret)
    created_at        TEXT NOT NULL,
    expires_at        TEXT NOT NULL                   -- 受控引用有效期(过期由读面重铸,不新建交接)
);

CREATE INDEX idx_xiaohongshu_handoff_refs_tenant ON xiaohongshu_handoff_refs(tenant_id, created_at);
