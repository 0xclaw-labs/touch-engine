-- HUI-4587 快手适配:服务端投稿任务关联与阶段(0123)。
--
-- 票面:一次提交键对应一次平台任务;持久化发布关联与阶段,查询异步结果并
-- 处理审核/处理中/失败;超时或重启先查询既有 photo_id,不因轮询失败重复创建
-- 作品。publish 返回 result=1 或 pending=true 不计为平台确认——本表 state
-- 只照录平台观察词,确认只走验签回执管线(Query 铸 Receipt 的显式 published)。

CREATE TABLE kuaishou_publish_jobs (
    id               TEXT PRIMARY KEY,               -- phkj_* (adapter 铸造)
    tenant_id        TEXT NOT NULL REFERENCES tenants(id),
    handoff_id       TEXT NOT NULL UNIQUE,           -- 一个交接至多一个平台任务(不重复创建)
    open_id          TEXT NOT NULL DEFAULT '',       -- 提交账号(服务端确定)
    stage            TEXT NOT NULL DEFAULT '' CHECK (stage IN
        ('','uploading','uploaded','creating','accepted')),
        -- uploading:字节上传中;uploaded:平台收妥字节(upload_token 在手);
        -- creating:创建作品调用 in-flight(此阶段重启=结果未知,拒重复创建);
        -- accepted:平台已接受任务(photo_id 在手,异步终态待查)。
    upload_token     TEXT NOT NULL DEFAULT '',       -- 平台上传凭据(服务器暂存)
    photo_id         TEXT NOT NULL DEFAULT '',       -- 平台作品 id(provider_ref)
    submit_state     TEXT NOT NULL DEFAULT '',       -- 最近一次提交观察词(照录)
    photo_state      TEXT NOT NULL DEFAULT '',       -- 最近一次作品查询观察词(照录,不确认)
    last_query_at    TEXT NOT NULL DEFAULT '',
    detail_json      TEXT NOT NULL DEFAULT '{}',     -- 最近一次平台响应的可观察字段
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX idx_kuaishou_publish_jobs_tenant ON kuaishou_publish_jobs(tenant_id, updated_at);
CREATE INDEX idx_kuaishou_publish_jobs_photo ON kuaishou_publish_jobs(photo_id) WHERE photo_id != '';
