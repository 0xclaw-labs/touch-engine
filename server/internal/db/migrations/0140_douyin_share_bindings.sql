-- HUI-4585 抖音视频适配加固:补齐 share_id 服务器端权威绑定字段与
-- provider_refs 事实分层字段(审核/公开范围/后续删除不被 create 抹平)。
--
-- 所有新增列均带 NOT NULL DEFAULT '',完全向后兼容既有行与迁移 0119/0120。

ALTER TABLE douyin_share_ids ADD COLUMN store_id TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_share_ids ADD COLUMN attempt_id TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_share_ids ADD COLUMN content_pack_version_id TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_share_ids ADD COLUMN client_key TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_share_ids ADD COLUMN method TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_share_ids ADD COLUMN scenario TEXT NOT NULL DEFAULT '';

ALTER TABLE douyin_provider_refs ADD COLUMN moderation_state TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_provider_refs ADD COLUMN visibility_scope TEXT NOT NULL DEFAULT '';
ALTER TABLE douyin_provider_refs ADD COLUMN deleted_at TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_douyin_share_ids_share_tenant ON douyin_share_ids(share_id, tenant_id);
