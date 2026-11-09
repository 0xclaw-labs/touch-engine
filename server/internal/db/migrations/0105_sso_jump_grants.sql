-- HUI-2222 (wave2 2026-11-10, B15/M11): SSO 跨应用跳转短时单用途凭据。
-- 纪律(复用 HUI-3546 凭据纪律):
--   - shell launch(public-ai)仍是唯一 mint 权威;本表只保管「凭据交接环节」:
--     mint 一次 → 短时单用途兑换一次,launch_url 不再出现在第一跳响应;
--   - 短时(TTL 默认 60s)、单用途(used_at 事务条件置位,并发同兑恰一方成功)、
--     可撤销(revoked_at);
--   - 高熵随机串(128-bit + 校验位, jtg_ 前缀),库内只存 SHA-256 全 token 哈希,
--     明文只在 mint 响应出现一次,永不落库、永不进日志;
--   - 绑定 mint 时的 principal+tenant:换会话/换组织出示一律拒绝(防凭据搬运);
--   - launch_url 是 shell 返回的既定目标(已过 safeLaunchURL https 非环回校验),
--     服务端保管、兑换时刻原样交还,不在此表二次加工。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sso_jump_grants (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    principal_id     TEXT NOT NULL,
    app_id           TEXT NOT NULL,
    launch_target_id TEXT NOT NULL,
    launch_url       TEXT NOT NULL,
    token_hash       TEXT NOT NULL UNIQUE,
    expires_at       TEXT NOT NULL,
    used_at          TEXT NOT NULL DEFAULT '',
    revoked_at       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sso_jump_grants_expiry
    ON sso_jump_grants(expires_at);
CREATE INDEX IF NOT EXISTS idx_sso_jump_grants_principal
    ON sso_jump_grants(principal_id, created_at);
