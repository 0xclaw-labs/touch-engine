-- 旧服务保持原对客效果；首次草稿编辑时事务捕获baseline，发布后沿版本快照。
ALTER TABLE store_service_configs ADD COLUMN public_baseline TEXT DEFAULT NULL;
ALTER TABLE storefront_versions ADD COLUMN services TEXT DEFAULT NULL;
ALTER TABLE storefront_versions ADD COLUMN services_revision INTEGER NOT NULL DEFAULT 0;
