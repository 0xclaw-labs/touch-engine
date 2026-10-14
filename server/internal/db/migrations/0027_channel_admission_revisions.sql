-- HUI-3535: channel admission revision trail. Every payload-changing seed
-- load appends one immutable row (create | update) inside the same
-- transaction as the upsert; identical re-loads append nothing. The git
-- versioned seed.json stays the truth source — this table is the in-database
-- audit of WHEN the effective record changed (票面: 登记实际读取日期/据实留痕).

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS channel_admission_revisions (
    id           TEXT PRIMARY KEY,
    platform     TEXT NOT NULL,
    entry_mode   TEXT NOT NULL,
    scenario     TEXT NOT NULL,
    tenant_scope TEXT NOT NULL,
    seed_version INTEGER NOT NULL CHECK (seed_version >= 1),
    action       TEXT NOT NULL CHECK (action IN ('create','update')),
    status       TEXT NOT NULL CHECK (status IN ('admitted','not_admitted','unknown')),
    payload_hash TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_channel_admission_revisions_key
    ON channel_admission_revisions(platform, entry_mode, scenario, tenant_scope, created_at);
