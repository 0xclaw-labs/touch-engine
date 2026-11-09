-- HUI-4589 企微客户事件回调日志：验签骨架的证据面。只落摘要与诚实状态——
-- 不落回调明文（隐私），无「客户已添加」类成功列；decrypt_state 的
-- pending_qualification 是资质未配置时的诚实态，绝不伪造验证结果。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS wecom_callback_log (
    id               TEXT PRIMARY KEY,
    occurred_at      TEXT NOT NULL,
    msg_signature    TEXT NOT NULL DEFAULT '',
    signature_valid  INTEGER NOT NULL CHECK (signature_valid IN (0,1)),
    decrypt_state    TEXT NOT NULL CHECK (decrypt_state IN
        ('pending_qualification','signature_mismatch','decrypt_failed','verified')),
    payload_digest   TEXT NOT NULL DEFAULT '',
    source_ip        TEXT NOT NULL DEFAULT '',
    note             TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_wecom_callback_log_time ON wecom_callback_log(occurred_at);
