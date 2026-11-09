-- HUI-3546 增量轮(2026-11-09, B08): 跨浏览器恢复凭据。
-- 纪律:
--   - purpose 结构性只允许 customer_resume:店员查验码(staff_verify)与
--     参与交接凭据(handoff_credentials)物理上进不了会话兑换面——
--     店员码不能接管顾客会话;
--   - 短时(TTL ≤ 15 分钟,域层默认 10 分钟)、单用途(used_at 事务置位,
--     并发同兑只有一方成功)、可撤销(revoked_at);
--   - 高熵随机串(128-bit + 校验位),库内只存 SHA-256 全 token 哈希,
--     明文只在签发响应出现一次,永不落库、永不进 URL 查询参数/日志;
--   - 换绑语义:兑换把会话 visitor_id 换绑到出示凭据的新浏览器,
--     会话/参与/冻结快照原样保留。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS action_session_credentials (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    session_id   TEXT NOT NULL REFERENCES action_sessions(id),
    purpose      TEXT NOT NULL CHECK (purpose IN ('customer_resume')),
    target_scope TEXT NOT NULL DEFAULT 'h5',
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   TEXT NOT NULL,
    used_at      TEXT NOT NULL DEFAULT '',
    revoked_at   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_action_session_credentials_session
    ON action_session_credentials(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_action_session_credentials_expiry
    ON action_session_credentials(expires_at);
