-- HUI-4594: 无活动留资(门店维度)。顾客从门店首页/联系入口自愿留资,无需活动存在。
-- SQLite 无法直接去列/去 FK,按标准流程重建两表(数据全量搬迁,含 0088/0089 新列):
--   - lead_submissions: campaign_id 允许 ''(门店来源线索),campaign_id 的外键解除
--     (活动归属完整性由应用层维护);新增 capture_source('campaign'|'store')与
--     session_id(3546 动作会话引用,可空串=未挂会话);
--   - lead_forms: campaign_id 允许 ''(门店级表单行,store_id 定位);原 inline
--     UNIQUE(campaign_id) 改为 partial unique index——活动行仍一活动一表单,
--     门店行一门店一表单,互不挤占。
-- 纪律: 不建第二线索库;门店留资复用同一张 lead_submissions/leads_outbox/审计链。

PRAGMA foreign_keys = ON;

CREATE TABLE lead_submissions_v2 (
    id                     TEXT PRIMARY KEY,
    tenant_id              TEXT NOT NULL REFERENCES tenants(id),
    campaign_id            TEXT NOT NULL DEFAULT '',
    store_id               TEXT NOT NULL DEFAULT '',
    link_id                TEXT NOT NULL DEFAULT '',
    form_id                TEXT NOT NULL DEFAULT '',
    session_id             TEXT NOT NULL DEFAULT '',
    capture_source         TEXT NOT NULL DEFAULT 'campaign' CHECK (capture_source IN ('campaign','store')),
    submission_ref         TEXT NOT NULL UNIQUE,
    dedup_key              TEXT NOT NULL UNIQUE,
    name                   TEXT NOT NULL DEFAULT '',
    phone                  TEXT NOT NULL DEFAULT '',
    wechat                 TEXT NOT NULL DEFAULT '',
    marketing_optin        INTEGER NOT NULL DEFAULT 0 CHECK (marketing_optin IN (0,1)),
    consent_notice_version TEXT NOT NULL DEFAULT '',
    consent_at             TEXT NOT NULL,
    consent_ip_fp          TEXT NOT NULL DEFAULT '',
    sync_state             TEXT NOT NULL DEFAULT 'accepted'
        CHECK (sync_state IN ('accepted','pending_sync','crm_received','rejected','revoked')),
    sync_error             TEXT NOT NULL DEFAULT '',
    attempts               INTEGER NOT NULL DEFAULT 0,
    source_version         INTEGER NOT NULL DEFAULT 1,
    revoked_at             TEXT,
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL,
    receiver_state         TEXT NOT NULL DEFAULT '',
    receipt_id             TEXT NOT NULL DEFAULT '',
    revoke_source_revision INTEGER NOT NULL DEFAULT 0
);

INSERT INTO lead_submissions_v2
    (id,tenant_id,campaign_id,store_id,link_id,form_id,session_id,capture_source,
     submission_ref,dedup_key,name,phone,wechat,marketing_optin,consent_notice_version,
     consent_at,consent_ip_fp,sync_state,sync_error,attempts,source_version,revoked_at,
     created_at,updated_at,receiver_state,receipt_id,revoke_source_revision)
SELECT
    id,tenant_id,campaign_id,store_id,link_id,form_id,'','campaign',
    submission_ref,dedup_key,name,phone,wechat,marketing_optin,consent_notice_version,
    consent_at,consent_ip_fp,sync_state,sync_error,attempts,source_version,revoked_at,
    created_at,updated_at,receiver_state,receipt_id,revoke_source_revision
FROM lead_submissions;

DROP TABLE lead_submissions;
ALTER TABLE lead_submissions_v2 RENAME TO lead_submissions;

CREATE INDEX IF NOT EXISTS idx_lead_submissions_tenant ON lead_submissions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_lead_submissions_campaign ON lead_submissions(campaign_id);

CREATE TABLE lead_forms_v2 (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id),
    campaign_id             TEXT NOT NULL DEFAULT '',
    store_id                TEXT NOT NULL DEFAULT '',
    notice_version          TEXT NOT NULL DEFAULT 'v1',
    marketing_optin_enabled INTEGER NOT NULL DEFAULT 1 CHECK (marketing_optin_enabled IN (0,1)),
    enabled                 INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_by              TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL
);

INSERT INTO lead_forms_v2
    (id,tenant_id,campaign_id,store_id,notice_version,marketing_optin_enabled,enabled,created_by,created_at,updated_at)
SELECT
    id,tenant_id,campaign_id,'',notice_version,marketing_optin_enabled,enabled,created_by,created_at,updated_at
FROM lead_forms;

DROP TABLE lead_forms;
ALTER TABLE lead_forms_v2 RENAME TO lead_forms;

CREATE UNIQUE INDEX IF NOT EXISTS idx_lead_forms_campaign_u ON lead_forms(campaign_id) WHERE campaign_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_lead_forms_store_u ON lead_forms(store_id) WHERE campaign_id = '' AND store_id <> '';
CREATE INDEX IF NOT EXISTS idx_lead_forms_campaign ON lead_forms(campaign_id);
