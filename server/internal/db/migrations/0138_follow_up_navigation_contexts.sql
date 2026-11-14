-- Opaque, principal-bound navigation contexts; no customer fields or CRM copy.
CREATE TABLE follow_up_navigation_contexts (
 handoff_id TEXT PRIMARY KEY, kind TEXT NOT NULL, principal_id TEXT NOT NULL,
 source_tenant_id TEXT NOT NULL, source_ref TEXT NOT NULL, source_revision INTEGER NOT NULL,
 store_id TEXT NOT NULL, crm_tenant_id TEXT NOT NULL, lead_id TEXT NOT NULL,
 origin_handoff_id TEXT NOT NULL DEFAULT '', visit_ref TEXT UNIQUE,
 expires_at INTEGER NOT NULL, used_at INTEGER NOT NULL DEFAULT 0
);
