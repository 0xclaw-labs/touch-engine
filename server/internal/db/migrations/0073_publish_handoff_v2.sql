-- HUI-1670 touch-handoff/v2: publish_handoffs / publish_handoff_events 两表重建。
--
-- 为什么重建(而不是 ALTER 加列):
--   1. v2 动作交接合法地无参与(link_share 纯链接:不伪建内容包/不建参与)——
--      旧表 campaign_id/participation_id/attempt_id 三列 NOT NULL+FK 挡死
--      无参与行;重建改为 nullable(SQL NULL 不要求父行存在,FK 语义保留);
--   2. 事件表 kind CHECK 白名单收死在 v1 八种,v2 六层事实
--      (点击/唤起/媒体交接/自报/店员确认/平台证实)插不进;
--   3. v2 需要 source+event_id 去重唯一索引(SHARED-BRIEF 事件通用语义)。
--
-- 重建顺序(外键每连接强制;PRAGMA foreign_keys 在事务内是 no-op,不能用
-- OFF 技巧,必须子先父后):
--   a. 重建 publish_handoff_events(先做,且 handoff_id 不再带 FK——这是
--      publish_handoffs 之后可再重建的前提;行级引用由本包唯一写面纪律保证,
--      与 handoffs 表的其余 FK 一致由应用层维护);
--   b. 重建 publish_handoffs(此时已无任何子表引用它);
--   旧行逐列原样搬运,幂等键/回执/参与索引照旧,新索引补 v2 语义。

-- ---- a. publish_handoff_events v2 --------------------------------------

CREATE TABLE publish_handoff_events_v2 (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    -- v2:不再外键引用 publish_handoffs(id)(父表重建前提;唯一写面纪律
    -- 保证行级引用有效,应用层拒绝悬空 handoff_id)。
    handoff_id      TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN
        -- v1 八种(/c 参与面兼容保留,语义不变)
        ('state_changed','customer_done_requested','staff_saw_submit',
         'staff_verified_work','official_confirmed','editor_open_proved',
         'receipt_rejected','canceled',
        -- v2 六层事实(HUI-1670 touch-handoff/v2 冻结命名,4585-4588 消费)
         'customer_click','wake_attempt','media_handoff',
         'customer_self_report','staff_confirm','platform_confirm',
        -- v2 回查留痕(adapter Query 结果;照录不确认)
         'platform_query')),
    actor           TEXT NOT NULL DEFAULT '',
    evidence_source TEXT NOT NULL DEFAULT '',
    receipt_id      TEXT NOT NULL DEFAULT '',
    -- v2 事件通用语义:source(写入方标识)+event_id(写入方侧幂等 ID),
    -- (tenant,handoff,source,event_id) 唯一;同键重放去重,同键不同 kind
    -- = IDEMPOTENCY_CONFLICT(应用层判定)。
    source          TEXT NOT NULL DEFAULT '',
    event_id        TEXT NOT NULL DEFAULT '',
    payload         TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL
);

INSERT INTO publish_handoff_events_v2
    (id, tenant_id, handoff_id, kind, actor, evidence_source, receipt_id,
     source, event_id, payload, created_at)
SELECT id, tenant_id, handoff_id, kind, actor, evidence_source, receipt_id,
       '', '', payload, created_at
FROM publish_handoff_events;

DROP TABLE publish_handoff_events;
ALTER TABLE publish_handoff_events_v2 RENAME TO publish_handoff_events;

-- 官方回执部分唯一(v1 official_confirmed + v2 platform_confirm 同待遇:
-- 同一 receipt_id 只落一条确认;被拒回执照录不占 ID)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_phe_receipt
    ON publish_handoff_events(tenant_id, receipt_id)
    WHERE receipt_id != '' AND kind IN ('official_confirmed', 'platform_confirm');
CREATE INDEX IF NOT EXISTS idx_phe_handoff
    ON publish_handoff_events(tenant_id, handoff_id, created_at);
-- v2 source+event_id 去重(只约束携带 event_id 的事件;v1 行 event_id='' 不参与)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_phe_dedup
    ON publish_handoff_events(tenant_id, handoff_id, source, event_id)
    WHERE event_id != '';

-- ---- b. publish_handoffs v2 ---------------------------------------------

CREATE TABLE publish_handoffs_v2 (
    id                       TEXT PRIMARY KEY,
    tenant_id                TEXT NOT NULL REFERENCES tenants(id),
    store_id                 TEXT NOT NULL REFERENCES stores(id),
    -- v2 nullable:link_share 纯链接无活动/无参与合法(NULL 不要求父行);
    -- v1 /c 行三列照常带值。
    campaign_id              TEXT REFERENCES campaigns(id),
    link_code                TEXT NOT NULL DEFAULT '',
    participation_id         TEXT REFERENCES participations(id),
    attempt_id               TEXT REFERENCES participation_attempts(id),
    platform                 TEXT NOT NULL,
    entry_mode               TEXT NOT NULL DEFAULT '',
    scenario                 TEXT NOT NULL,
    idempotency_key          TEXT NOT NULL,
    status                   TEXT NOT NULL CHECK (status IN
        ('previewed','handoff_preparing','handoff_ready','editor_opened',
         'submitting','awaiting_review','platform_confirmed','canceled',
         'failed','unknown')),
    evidence_source          TEXT NOT NULL CHECK (evidence_source != ''),
    content_version          TEXT NOT NULL DEFAULT '',
    content_pack_version_id  TEXT NOT NULL DEFAULT '',
    media_json               TEXT NOT NULL DEFAULT '[]',
    text_json                TEXT NOT NULL DEFAULT '{}',
    visitor_id               TEXT NOT NULL DEFAULT '',
    query_count              INTEGER NOT NULL DEFAULT 0,
    timed_out                INTEGER NOT NULL DEFAULT 0,
    cancel_reason            TEXT NOT NULL DEFAULT '',
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    -- v2 动作交接(touch-handoff/v2)
    action_id                TEXT NOT NULL DEFAULT '',   -- 4584 动作实例 id(sa_*)
    catalog_code             TEXT NOT NULL DEFAULT '',   -- A01-A30
    action_session_id        TEXT NOT NULL DEFAULT '',   -- 3546 会话权威
    action_revision          INTEGER NOT NULL DEFAULT 0, -- 开交接时校验过会话冻结值
    payload_kind             TEXT NOT NULL DEFAULT '' CHECK (payload_kind IN ('', 'media', 'link_share')),
    method                   TEXT NOT NULL DEFAULT '',   -- 3535 方法枚举
    environment              TEXT NOT NULL DEFAULT '',   -- 六环境枚举
    plan_kind                TEXT NOT NULL DEFAULT '',   -- v2 计划(kind 冻结在行)
    evidence_level           TEXT NOT NULL DEFAULT '',   -- official_receipt|none|unknown
    client_action_json       TEXT NOT NULL DEFAULT '{}', -- 结构化 client_action(类型化,非 JS)
    unsupported_fields_json  TEXT NOT NULL DEFAULT '[]',
    share_url                TEXT NOT NULL DEFAULT '',   -- link_share:服务端生成的合法分享目标
    request_hash             TEXT NOT NULL DEFAULT '',   -- 幂等键负载指纹(同键不同负载=冲突)
    gate_json                TEXT NOT NULL DEFAULT '{}'  -- 冻结的 Gate 上下文(审计;服务端派生事实)
);

INSERT INTO publish_handoffs_v2
    (id, tenant_id, store_id, campaign_id, link_code, participation_id, attempt_id,
     platform, entry_mode, scenario, idempotency_key, status, evidence_source,
     content_version, content_pack_version_id, media_json, text_json, visitor_id,
     query_count, timed_out, cancel_reason, created_at, updated_at)
SELECT id, tenant_id, store_id, campaign_id, link_code, participation_id, attempt_id,
     platform, entry_mode, scenario, idempotency_key, status, evidence_source,
     content_version, content_pack_version_id, media_json, text_json, visitor_id,
     query_count, timed_out, cancel_reason, created_at, updated_at
FROM publish_handoffs;

DROP TABLE publish_handoffs;
ALTER TABLE publish_handoffs_v2 RENAME TO publish_handoffs;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ph_idem
    ON publish_handoffs(tenant_id, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_ph_participation
    ON publish_handoffs(tenant_id, participation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ph_attempt
    ON publish_handoffs(tenant_id, attempt_id);
-- v2:同会话交接回查(一个会话可有多次交接:换 payload/换平台=新行)。
CREATE INDEX IF NOT EXISTS idx_ph_action_session
    ON publish_handoffs(tenant_id, action_session_id, created_at);
