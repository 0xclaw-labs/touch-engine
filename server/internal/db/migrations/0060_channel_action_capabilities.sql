-- HUI-3535 (2026-11-09 增量): 动作级渠道能力真源。行键 catalog_code+platform+
-- method+scenario+tenant_scope；六环境四分能力(navigation/media_handoff/
-- platform_receipt/reward_eligibility)在 environments JSON 列按环境分别给值，
-- 四者独立存储、绝不互相推断（裁定书）。种子=迁移权威初值（0061/0062）；平台端
-- 更新走受控记录端点(internal token)，本迁移只落初值不承担后续维护。
-- unknown 永不开奖励由模型层校验兜底，CHECK 只拦状态枚举。
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS channel_action_capabilities (
    id                 TEXT PRIMARY KEY,
    catalog_code       TEXT NOT NULL,
    platform           TEXT NOT NULL,
    method             TEXT NOT NULL CHECK (method IN
        ('h5_share','miniapp','js_media_handoff','oauth_server_push','native_sdk',
         'link_share','manual_material','direct_open')),
    scenario           TEXT NOT NULL CHECK (scenario IN
        ('store_promo','reward_task','review','follow','service','group_buy')),
    action_kind        TEXT NOT NULL CHECK (action_kind IN
        ('publish','review','follow','service','group_buy')),
    label              TEXT NOT NULL,
    suggested_only     INTEGER NOT NULL CHECK (suggested_only IN (0,1)),
    app_binding        TEXT NOT NULL CHECK (app_binding IN ('platform_app','merchant_authorized','none')),
    tenant_scope       TEXT NOT NULL DEFAULT '*',
    status             TEXT NOT NULL CHECK (status IN ('admitted','not_admitted','unknown')),
    environments       TEXT NOT NULL,
    field_support      TEXT NOT NULL DEFAULT '{}',
    required_inputs    TEXT NOT NULL DEFAULT '[]',
    evidence_grade     TEXT NOT NULL CHECK (evidence_grade IN
        ('official_spec','official_doc','official_snapshot','device_test','unverified')),
    official_sources   TEXT NOT NULL DEFAULT '[]',
    checked_at         TEXT NOT NULL,
    valid_until        TEXT NOT NULL,
    emergency_disabled INTEGER NOT NULL DEFAULT 0 CHECK (emergency_disabled IN (0,1)),
    close_reason       TEXT NOT NULL DEFAULT '',
    missing            TEXT NOT NULL DEFAULT '[]',
    reason_code        TEXT NOT NULL,
    reason_text        TEXT NOT NULL,
    seed_version       INTEGER NOT NULL CHECK (seed_version >= 1),
    payload_hash       TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (catalog_code, platform, method, scenario, tenant_scope)
);
CREATE INDEX IF NOT EXISTS idx_channel_action_capabilities_code
    ON channel_action_capabilities(catalog_code, tenant_scope);
