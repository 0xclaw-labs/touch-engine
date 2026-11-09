-- HUI-4584 G1: 旧 extra_jump_actions (campaign_id,kind) 行的一次性迁移。
-- 规则（与 internal/actioncatalog.LegacyMapping 同语义，两侧改一处须同步另一处）：
--   wifi   -> A17（确定）
--   wecom  -> A18，variant=wecom（确定）
--   review -> A08-A12 按 href 域名推断；推不出 -> catalog_code='' 待商家采用
--   follow -> A13-A16 按 href 域名推断；推不出 -> catalog_code='' 待商家采用
--   navigate / return 不迁移：兼容辅助能力保留旧路径，不因迁移删除。
-- 每行生成新 action_id（不合并、不猜选）；campaign_id 落 legacy_campaign_id
-- 保留来源与唯一映射；旧行不删除（旧 /c 与 /admin 路径字节级兼容）。
-- 无店活动（campaigns.store_id 为空）的行无法落门店，留旧表。
-- 幂等守卫：NOT EXISTS (legacy_campaign_id, legacy_kind)——旧表
-- UNIQUE(campaign_id,kind) 保证该键唯一，重放零新增。

PRAGMA foreign_keys = ON;

INSERT INTO store_actions(
    id, tenant_id, store_id, catalog_code, platform, kind, variant, label, target_ref,
    content_binding_ref, capability_ref, presentation, sort_order, enabled, revision, revoked,
    expires_at, legacy_campaign_id, legacy_kind, created_at, updated_at
)
SELECT
    'sa_' || lower(hex(randomblob(12))),
    j.tenant_id,
    c.store_id,
    CASE
        WHEN j.kind = 'wifi' THEN 'A17'
        WHEN j.kind = 'wecom' THEN 'A18'
        WHEN j.kind = 'review' AND j.href LIKE '%dianping.com%'    THEN 'A08'
        WHEN j.kind = 'review' AND j.href LIKE '%douyin.com%'      THEN 'A09'
        WHEN j.kind = 'review' AND j.href LIKE '%amap.com%'        THEN 'A10'
        WHEN j.kind = 'review' AND j.href LIKE '%baidu.com%'       THEN 'A11'
        WHEN j.kind = 'review' AND j.href LIKE '%meituan.com%'     THEN 'A12'
        WHEN j.kind = 'follow' AND j.href LIKE '%douyin.com%'      THEN 'A13'
        WHEN j.kind = 'follow' AND j.href LIKE '%kuaishou.com%'    THEN 'A14'
        WHEN j.kind = 'follow' AND j.href LIKE '%xiaohongshu.com%' THEN 'A15'
        WHEN j.kind = 'follow' AND j.href LIKE '%weixin.qq.com%'   THEN 'A16'
        ELSE ''
    END,
    CASE
        WHEN j.kind = 'wifi' THEN ''
        WHEN j.kind = 'wecom' THEN 'wechat'
        WHEN j.kind = 'review' AND j.href LIKE '%dianping.com%'    THEN 'dianping'
        WHEN j.kind = 'review' AND j.href LIKE '%douyin.com%'      THEN 'douyin'
        WHEN j.kind = 'review' AND j.href LIKE '%amap.com%'        THEN 'amap'
        WHEN j.kind = 'review' AND j.href LIKE '%baidu.com%'       THEN 'baidu'
        WHEN j.kind = 'review' AND j.href LIKE '%meituan.com%'     THEN 'meituan'
        WHEN j.kind = 'follow' AND j.href LIKE '%douyin.com%'      THEN 'douyin'
        WHEN j.kind = 'follow' AND j.href LIKE '%kuaishou.com%'    THEN 'kuaishou'
        WHEN j.kind = 'follow' AND j.href LIKE '%xiaohongshu.com%' THEN 'xiaohongshu'
        WHEN j.kind = 'follow' AND j.href LIKE '%weixin.qq.com%'   THEN 'wechat'
        ELSE ''
    END,
    CASE
        WHEN j.kind = 'wifi' THEN 'wifi'
        WHEN j.kind = 'wecom' THEN 'contact'
        ELSE j.kind
    END,
    CASE WHEN j.kind = 'wecom' THEN 'wecom' ELSE '' END,
    CASE
        WHEN j.kind = 'wifi' THEN '门店 Wi-Fi'
        WHEN j.kind = 'review' THEN '写点评'
        WHEN j.kind = 'wecom' THEN '加企微'
        WHEN j.kind = 'follow' THEN '关注账号'
        ELSE j.kind
    END,
    j.href,
    '', '', 'link',
    0,
    j.enabled,
    1,
    j.revoked,
    j.expires_at,
    j.campaign_id,
    j.kind,
    '2026-01-01T00:00:00Z',
    '2026-01-01T00:00:00Z'
FROM extra_jump_actions j
JOIN campaigns c ON c.id = j.campaign_id
WHERE j.kind IN ('wifi', 'review', 'wecom', 'follow')
  AND c.store_id IS NOT NULL AND c.store_id <> ''
  AND c.tenant_id = j.tenant_id
  AND NOT EXISTS (
      SELECT 1 FROM store_actions s
      WHERE s.legacy_campaign_id = j.campaign_id AND s.legacy_kind = j.kind
  );
