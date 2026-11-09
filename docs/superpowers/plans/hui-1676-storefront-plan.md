# HUI-1676 实施计划｜门店首页与活动配置：版本发布、逐动作预检、预览回滚与旧参与保护

分支 touch/hui-1676-storefront-version（worktree wt-touch-1676），基线 1460146。
迁移号段 0095-0098（台账预分配）。端口 web 18352 / go 18252。报告 reports/hui-1676-report.md。

## 裁定书核对（brainstorm 以裁定书为答，无新增阻塞）

- B01 storefront_versions：draft/published、revision、sections[] ✓
- 四端点 + expected_revision 并发控制（STALE_VERSION）✓
- 发布=冻结引用原子切换；回滚重查实时撤权（不复活撤权内容，也不静默剔除）✓
- 缓存按 revision 失效：复用 readcache（epoch 边界=发布/回滚 bumpEpoch，0 请求过期）✓
- 逐动作预检：失败只影响相关动作；页级 fatal（结构非法/跨店引用）整体拒绝 ✓
- 发布需用户确认变更摘要：preflight 计算 diff + summary_fingerprint，publish 必须回带 ✓
- M3 页面 + 手机预览用真实公共数据面（会话约束预览端点，同一白名单投影）✓
- 活动可选语义（活动编辑不动门店基础服务）；点评禁奖（3535 准入联动硬拒）✓
- 旧参与快照不随换版改写：storefront 版本行只存引用，永不写参与/权益表 ✓
- 4593 接线：模板应用回调 PATCH /api/v1/template-assignments/{id} {status:"applied",applied_revision,reason}

关键预裁定补充（裁定书+票面推得，记录备查）：
1. 发布指针复用 4584 已建 store_storefronts.storefront_revision（其票恒 0，本票接管写）——不新建第二指针。
2. 版本行内冻结 actions 白名单载荷（label/platform/kind/variant/presentation/capability_ref/catalog_code/action_revision）：回滚显示对应版本必须是版本时的真值，live 行只保留实时否决权（撤权/过期/停用）。
3. publish 幂等：指针已等于候选 revision → 原样返回同一发布结果；否则 expected_published_revision 不匹配 → 409 stale_version。
4. publish 的 summary_fingerprint 校验：与当前 published↔候选 draft 的实时重算 diff 指纹比对，不一致 → 409 confirm_required（草稿在确认后变过）。
5. rollback 目标必须是已发布版本；refs 中任一动作已撤销/过期/消失 → 409 rollback_blocked 带动作清单（明确确认形成新修订，不改写历史）。
6. 迁移只用 0095（版本表）+ 0096（发布/回滚事件审计）；0097/0098 保留不用（无投机 DDL，报告中说明）。
7. 套餐区 v1 = group_buy 类动作（真实 store_actions）+ offer_refs（deal_offers 引用，只读 ListStoreDealOffers 新增 SELECT）；活动卡片 campaign_refs 引用 campaigns。

## T1 迁移（server/internal/db/migrations/）

- 0095_storefront_versions.sql：
  storefront_versions(id PK, tenant_id FK, store_id FK, store_code, revision>=1,
  status CHECK draft|published, sections JSON, actions JSON(冻结白名单载荷),
  action_revision_refs JSON, offer_refs JSON, campaign_refs JSON, brand_ref,
  summary_fingerprint, created_by, created_at, updated_at, published_at,
  UNIQUE(store_id, revision))。
- 0096_storefront_publish_events.sql：
  storefront_publish_events(id PK, tenant_id, store_id, action CHECK publish|rollback,
  revision, previous_revision, actor, summary JSON, created_at)——append-only 审计。
- db_test.go 表清单窄 +2（共享文件窄补丁）。

## T2 storefrontversion 叶子包（纯函数，TDD 先红后绿）

server/internal/storefrontversion/：
- Section{ID,Type(actions|offers),Title,ActionIDs,OfferRefs}、ActionSnapshot（冻结白名单载荷）、Document{Sections,ActionRefs,OfferRefs,CampaignRefs,BrandRef}。
- ValidateDocument：section id 非空唯一、type 白名单、actions 段才有 action_ids、offers 段才有 offer_refs、跨段 action_id 唯一、长度/数量上限。结构非法=页级 fatal（发布整体拒绝）。
- ChangeSummary(prev, next) []string：段增删/更名/排序、动作增删/移动、品牌/套餐/活动引用变化；首版=全量新增。Fingerprint(summary)=sha256。
- Preflight：输入 ActionFact{ActionID,Found,Enabled,Revoked,Expired,TargetOfficial,ContentState} 输出逐动作 Verdict{State ready|restricted|missing_config,ReasonCodes[]}；missing_config=目标/内容缺失；restricted=撤权/过期/内容停发（可见原因）；跨动作零传染。无奖励库存检查（首页版本不挂奖励）。
- FreezeRefs：保存/发布时按 live 行冻结 action_revision_refs + actions 载荷。

## T3 store 层仓储（store_storefront_versions.go，TDD）

- GetStorefrontDraft / SaveStorefrontDraft(expectedRevision)：首存 rev1；最新行为 draft 时内容相同=幂等重放（不推版），内容变化=原行修订 +1；最新行已 published → 新建 rev+1 draft 行。ErrStaleRevision。
- PublishStorefrontVersion(candidateRevision, expectedPublished, fingerprint)：
  事务内重校验 refs（live revision 漂移→ErrActionRefChanged；已撤销→ErrActionRevoked），
  行置 published+published_at，指针 UPDATE store_storefronts.storefront_revision，
  事件落 storefront_publish_events，提交后 bumpEpoch。幂等重放/冲突见预裁定 3-4。
- RollbackStorefrontVersion(target, expectedPublished)：目标须已发布；refs 实时否决重查
  （撤销/过期/消失→ErrRollbackBlocked 带清单）；指针回拨+事件+bumpEpoch；已在该版本=重放。
- ListStorefrontVersions / ListStorefrontPublishEvents（只读审计）。
- GetStorefrontPublishedByCode（公共投影源：指针行 join）。
- ListStoreDealOffers(tenantID, storeID)（只读 SELECT join deal_offers×campaigns.store_id）。

## T4 httpapi（handlers_storefront_version.go，TDD）

- 开关：FEATURE_STOREFRONT（路由不注册 + gate 统一 404 双保险；一个开关管整个门店版本域）。
- 授权：storeForActions + requireScopedAction（GET=ReadRecord、写=Update，store_manager 本店/org_owner 全店，4593 维度天然生效）。
- GET/PUT /api/v1/stores/{id}/storefront-draft（PUT expected_revision→409 stale_version）。
- POST /api/v1/stores/{id}/storefront/preflight：逐动作事实集组装（store_actions 行+绑定表+contentpack 真值；复用 campaign 配置同款只读组装），输出页级 fatal[] + 逐动作 state/reason_codes/修复入口（channels 页锚点）。
- POST /api/v1/stores/{id}/storefront/publish：candidate+expected_published_revision+summary_fingerprint（确认变更摘要）；机器负例→409。
- POST /api/v1/stores/{id}/storefront/rollback：target+expected_published_revision。
- GET /api/v1/stores/{id}/storefront/revisions（版本与事件审计，回滚 UI 数据源）。
- GET /api/v1/stores/{id}/storefront-preview?revision=：会话约束预览数据面=公共白名单同形投影（+preview:true）。
- handlePublicStorefront 升级：指针>0 → 渲染 published 版本（sections/actions/activity_cards/policy_links 白名单，live 撤权/过期/停店否决仍生效）；指针=0 → 4584 现行为逐字节保留。
- 缓存：Server 增 StorefrontCache（readcache，Epoch=st.Epoch，FEATURE_PUBLIC_CACHE 才启用）；发布/回滚 bumpEpoch=0 请求过期。
- 点评禁奖：campaignenable.ValidateSpec 增渠道 scenario 白名单校验，"review"→硬拒（review_reward_prohibited）；panel 前置拦截+产品文案。
- 4593 接线缝：M3 模板应用完成后前端调 PATCH /api/v1/template-assignments/{id}（status=applied+applied_revision+reason）；后端无需新端点。

## T5 M3 页面（web）

- web/src/app/work/stores/[id]/page/page.tsx（薄壳）+ components/work/storefront/storefront-page-desk.tsx + lib/storefront-page.ts。
- 功能：门店资料摘要、行业模板（3 预设：基础/餐饮到店/零售货架——按目录码生成段落骨架）、分组/排序/常用按钮（真实 store_actions 行）、套餐区（group_buy 动作+deal offers 选择）、可选活动（campaign_refs，活动编辑反向零触达）、手机预览（真实预览数据面渲染+明显预览标识）、逐动作预检结果+修复入口、变更摘要 diff+确认、发布/回滚/版本历史、4593 模板 applied 回调。
- 共享文件窄补丁：page-map +1、page-census markerBook +1、tests/page-census.test.ts 计数 18→19（两处）、stores 列表行 +1「门店首页」链接、channels 页头 +1 返回链接（均为 +1 行级窄补丁）。
- web vitest：lib 纯函数测试 + 页面源级断言 + census 同步。

## T6 验证

- cd server && go test ./...（基线红 7 个 httpapi 例外）；cd web && npx vitest run。
- 真实起服冒烟 18252/18352（FEATURE_STOREFRONT=on，data/ 隔离），迁移加载、四端点正反例走一遍。
- code-review 自审 diff；不 push 不 merge。

## 验收对照（T04/T01 核心）

- 新店无活动可配置/发布基础首页（指针 0→1）。
- 保存第二版草稿时第一版继续服务（GET /s 公共面 revision 不变），发布才切换。
- 缺一个视频素材只限制该内容动作（preflight 单动作 missing_config，其余 ready 可发布）。
- 并发旧 expected_revision→409；重复发布同结果；指针漂移→409。
- 回滚不复活已撤权动作（rollback_blocked）；历史版本行不被改写。
- 换版/回滚零写参与/权益表（测试断言表行数不变）。
- 点评渠道进奖励配置→400 review_reward_prohibited。
