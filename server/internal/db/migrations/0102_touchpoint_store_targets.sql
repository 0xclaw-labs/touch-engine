-- HUI-1665 Wave2: 触点 store 根目标 —— 新增 target_type 与 binding_revision 列。
-- 纪律:
--   - 旧行零语义变化:target_type DEFAULT 'legacy_campaign',既有行的解析/停用/换绑
--     兼容路径逐字节保留;只有显式 target_type='store' 的行才解析为门店根(→/s/{store_code})。
--   - campaign_id 的 NOT NULL 放宽在本号段 0103 的表重建中完成;本号先落纯增量列,
--     使重建(0103)的回拷可以带上新列的现值(重放安全)。
--   - binding_revision 是换绑乐观并发闸:旧行统一 1,每次成功换绑应用层 +1,
--     陈旧 expected 在应用层 409;URL(别名码)永不因换绑变化。
--   - store_id 检索索引:门店筛选与 0104 反查迁移的检索路径(小表,纯索引无回填)。

PRAGMA foreign_keys = ON;

ALTER TABLE touchpoint_aliases ADD COLUMN target_type
    TEXT NOT NULL DEFAULT 'legacy_campaign' CHECK (target_type IN ('store','legacy_campaign'));

ALTER TABLE touchpoint_aliases ADD COLUMN binding_revision
    INTEGER NOT NULL DEFAULT 1 CHECK (binding_revision >= 1);

CREATE INDEX IF NOT EXISTS idx_touchpoint_aliases_store ON touchpoint_aliases(store_id);
