-- HUI-4593 连锁、代理与权限:代管关系 additive 增量(裁定书冻结契约)。
-- 纪律:
--   - 纯 ADD COLUMN(SQLite 支持),0008 的行/列/墓碑/唯一激活约束逐列原样保留,
--     零数据丢失;
--   - allowed_actions:JSON 数组文本;NULL 语义由 NOT NULL DEFAULT '' 承接 ——
--     空串 = 旧语义(名下商家只读 + 开子账号),旧代理行绝不静默获得新增写权;
--     白名单值: storefront / action / renewal(应用层校验,库层不设 CHECK 以免
--     未来命令集扩展再重建表);
--   - store_scope_set:JSON 数组文本(门店 id 集);空 = 全门店;
--   - valid_until:RFC3339;空串 = 不过期;到期 = 关系整体失效(fail-closed,
--     与撤销同待遇 —— 到期即撤销数据与动作权限);
--   - grant_revision:scope 每次显式变更 +1(乐观并发,STALE_VERSION 拒绝);
--   - payer_grant_ref:代付授权 = 独立公共权威引用(资金裁决归 HUI-4591 公共
--     payer 权威),缺省空 = 不可代付;本层只存不透明引用并参与资格判定。

ALTER TABLE agency_relations ADD COLUMN allowed_actions TEXT NOT NULL DEFAULT '';
ALTER TABLE agency_relations ADD COLUMN store_scope_set TEXT NOT NULL DEFAULT '';
ALTER TABLE agency_relations ADD COLUMN valid_until TEXT NOT NULL DEFAULT '';
ALTER TABLE agency_relations ADD COLUMN grant_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agency_relations ADD COLUMN payer_grant_ref TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_agency_relations_active_expiry
    ON agency_relations(agent_principal, valid_until) WHERE revoked_at IS NULL;
