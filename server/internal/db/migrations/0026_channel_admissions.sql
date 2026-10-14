-- HUI-3535: channel admission records — the versioned truth of which
-- platform + entry_mode + scenario routes are admitted for 碰一碰门店交付.
-- additive only. Loaded at startup from docs/contracts/channel-admission/v1/
-- seed.json (vendored copy embedded in internal/channeladmission); this table
-- is the read model, migrations stay pure DDL per repo discipline.
-- unknown never equals allowed: CHECK admits only the three real statuses.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS channel_admissions (
    id               TEXT PRIMARY KEY,
    platform         TEXT NOT NULL,
    entry_mode       TEXT NOT NULL CHECK (entry_mode IN ('h5','miniapp','native','manual')),
    scenario         TEXT NOT NULL,
    app_binding      TEXT NOT NULL CHECK (app_binding IN ('platform_app','merchant_authorized','none')),
    tenant_scope     TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('admitted','not_admitted','unknown')),
    reward_admitted  INTEGER NOT NULL CHECK (reward_admitted IN (0,1)),
    reason_code      TEXT NOT NULL,
    reason_text      TEXT NOT NULL,
    cap_media        TEXT NOT NULL CHECK (cap_media   IN ('supported','unsupported','unknown')),
    cap_title        TEXT NOT NULL CHECK (cap_title   IN ('supported','unsupported','unknown')),
    cap_body         TEXT NOT NULL CHECK (cap_body    IN ('supported','unsupported','unknown')),
    cap_topic        TEXT NOT NULL CHECK (cap_topic   IN ('supported','unsupported','unknown')),
    cap_poi          TEXT NOT NULL CHECK (cap_poi     IN ('supported','unsupported','unknown')),
    cap_receipt      TEXT NOT NULL CHECK (cap_receipt IN ('supported','unsupported','unknown')),
    cap_callback     TEXT NOT NULL CHECK (cap_callback IN ('supported','unsupported','unknown')),
    missing          TEXT NOT NULL DEFAULT '[]',
    official_sources TEXT NOT NULL DEFAULT '[]',
    last_checked_at  TEXT NOT NULL,
    valid_until      TEXT NOT NULL,
    close_reason     TEXT NOT NULL DEFAULT '',
    seed_version     INTEGER NOT NULL CHECK (seed_version >= 1),
    payload_hash     TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE (platform, entry_mode, scenario, tenant_scope)
);

CREATE INDEX IF NOT EXISTS idx_channel_admissions_scope
    ON channel_admissions(tenant_scope, platform, entry_mode, scenario);
