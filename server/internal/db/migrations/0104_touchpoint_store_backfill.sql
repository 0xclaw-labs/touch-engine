-- HUI-1665 Wave2: 现有触点行由 Campaign 反查门店补 store_id(文档 B02 迁移方向)。
-- 纪律:
--   - 只补空:store_id 已非空的行(标签派生/人工绑定)绝不覆盖 —— 幂等守卫,重放零改动。
--   - 同租户 join(campaign 与触点同 tenant_id);campaign 无店(store_id 空)的触点
--     保持空:归属不能唯一确定就保留 legacy 解析并列管理异常(M8 如实展示目标类型),
--     禁止任选门店或让旧卡失效;商家可经换绑显式升级为门店根。
--   - target_type 不改:反查结果只作为归属事实与换绑升级候选,不静默改写顾客目标
--     (旧卡继续进原活动,原 /c 深链语义保留)。

PRAGMA foreign_keys = ON;

UPDATE touchpoint_aliases
SET store_id = (
    SELECT c.store_id FROM campaigns c
    WHERE c.id = touchpoint_aliases.campaign_id
      AND c.tenant_id = touchpoint_aliases.tenant_id
)
WHERE (touchpoint_aliases.store_id IS NULL OR touchpoint_aliases.store_id = '')
  AND touchpoint_aliases.campaign_id IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM campaigns c
      WHERE c.id = touchpoint_aliases.campaign_id
        AND c.tenant_id = touchpoint_aliases.tenant_id
        AND c.store_id IS NOT NULL AND c.store_id <> ''
  );
