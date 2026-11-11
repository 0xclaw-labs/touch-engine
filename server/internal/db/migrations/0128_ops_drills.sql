-- HUI-4592:备份/恢复演练记录(恢复校验报告必带——"已备份"不算演练)。
-- 纪律:
--   - kind=backup|restore;restore 行 verification_report NOT NULL:
--     没有恢复校验报告的"演练"在存储层就落不进去;
--   - result=ok|failed,失败也是演练证据(诚实记录,不粉饰);
--   - executed_by 由已验证会话解析;notes 是人工补充,不替代校验报告。

CREATE TABLE IF NOT EXISTS ops_drills (
    id                  TEXT PRIMARY KEY,
    kind                TEXT NOT NULL,
    target              TEXT NOT NULL,
    result              TEXT NOT NULL,
    verification_report TEXT NOT NULL DEFAULT '',
    executed_by         TEXT NOT NULL,
    executed_at         TEXT NOT NULL,
    notes               TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ops_drills_time ON ops_drills(executed_at);
