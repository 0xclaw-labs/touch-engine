# 实施计划｜HUI-3553 门店内容包与可视化选材（真实媒体、生态回流、商家采用与版本冻结）

- worktree: /Users/jimfu/works/派诺生态/wt-touch-3553 ｜ 分支: touch/hui-3553-content-packs ｜ 基线: 161fadc
- 迁移号段: 0050-0055 ｜ 端口: web 18342 / go 18242
- 需求权威: Linear HUI-3553（2026-11-09 修订）+ SHARED-BRIEF.md + briefs/hui-3553.md（裁定书）
- 唯一写面: server/internal/contentpack、server/internal/upload、server/internal/store（内容包增量）、
  server/internal/httpapi（内容包新增 handler + server.go 窄注册补丁）、server/internal/db/migrations/0050-0055、
  web/src/app/work/materials、web/src/components/work/content-pack-panel.tsx（及新增抽屉组件）、web/tests 内容包测试。

## 范围裁定复述（照办，不再讨论）

1. 内容包脱离活动成为门店根资产：`POST /api/v1/stores/{id}/content-packs`（campaign 可空）。旧活动端点保留不动；
   内部归一到 store 域存储——新迁移建 store 维度表，旧表用查询适配（UNION/映射）表达「已有活动包↔门店域」的明确映射，不重编号。
2. 采用权威：候选(draft/candidate) → 商家明确采用（(tenant, store, platform) 指针，记录 adopted_by/时间）→ 采用版内容列零 UPDATE（版本冻结）。
   换版 = 新候选 → 再采用 → 指针切换；旧引用不受影响。
3. B05 读面：`GET /api/v1/public/actions/{actionId}/content` 按服务器已采用引用返回；无采用 = 404 语义（不伪装空数组成功）。
   `POST /api/v1/public/content/{version}/media-access`（purpose=preview/download/platform_fetch）签发短 TTL 凭据；
   GET 受控媒体支持 HEAD/Range/正确 MIME/大小限制/重试续签；不依赖商家 cookie；凭据非一次性但绑定版本+媒体+用途+TTL。
4. 生态回流：本票只提供「唯一候选登记事务」exported 方法（HTTP 内部端点归 4595）；同幂等键重放=同一行；旧回流绝不覆盖新采用。
5. content_binding_ref 写端点归 4584；本票提供「采用后建立绑定」的内部调用（exported store 方法），自测用直接函数调用。
6. M5（/work/materials）：补「内容包/候选/采用/换版」抽屉与真实上传下载接线；上传走既有 upload 包（在 upload 包内新增本地
   受控媒体存储）；浏览/换稿/下载零收费调用。

## 边界（不做的）

- 不建 store_actions 表（4584 域，迁移 0043-0049）；不写 actions PUT 端点。
- 不写 POST /api/v1/internal/content-production-handoffs/{id}/results（4595 域）。
- 不动 0028-0030/0042 既有表与旧 campaign 端点行为（已有测试必须全绿）。
- 不接真实制作引擎（capabilities 如实 production_available=false 保持现状）。
- 生产路径不放任何假媒体文件；测试 fixture 只进 testdata。

## 设计

### 存储模型（迁移 0050-0055）

- 0050 `store_content_packs`：门店根内容包（tenant_id, store_id NOT NULL, campaign_id NULL 可选, title…，
  UNIQUE(tenant_id, store_id, title)）。注释写明与旧 content_packs 的映射规则（campaign 包经 campaigns.store_id 映射进门店域）。
- 0051 `content_media`：真实受控媒体元数据（media_id, tenant_id, store_id, kind(video|image), mime_type=服务器探测,
  byte_size, sha256, duration_ms, width, height, filename, media_ref=内容寻址地址; UNIQUE(tenant_id, sha256)）。
  文件字节存磁盘（TOUCH_MEDIA_DIR，默认 data/media），库中零字节。
- 0052 `store_content_pack_versions` + `store_content_pack_version_events`：不可变版本（快照列 = media_id+sha256+mime+size+
  duration+宽高 冻结；文案/门店事实/平台适配/授权/标识同 0029 词汇）；事件表只追加（created|submitted|adopted|superseded|
  blocked|returned）。
- 0053 `store_content_pack_adoptions`：(tenant, store, platform) 当前指针（pack_id, version_id, adopted_by, 时间戳）；
  换版覆盖指针，旧版内容行零改动。
- 0054 `store_action_content_bindings`：(tenant, action_id) → store/pack/version 引用 + action_revision；
  只能由「采用后建立绑定」内部调用写入（UNIQUE(tenant_id, action_id)）。
- 0055 `media_access_grants`：media-access 签发审计（只追加：version/media/purpose/session_ref/expires_at/时间）。

### upload 包扩展（真实媒体存储，server/internal/upload/mediastore.go）

- `MediaStore{Dir, MaxBytes}`：Save(reader) → 临时文件流式写 + 同时算 sha256 + 魔数字节探测 MIME（jpeg/png/webp/mp4，
  拒绝白名单外）+ mp4 盒解析 mvhd/tkhd 取 duration_ms/width/height（尽力而为，缺失记 0）+ 超限失败；
  落盘内容寻址 `<dir>/<sha[:2]>/<sha><ext>`；同租户同 sha256 去重复用（replayed=true）。
- Open(sha) 供受控读取；MIME 白名单与 contentpack 现有 allowedContentTypes 对齐。
- 服务器以真实字节定 MIME，客户端伪填一律不采信。

### contentpack 域扩展（server/internal/contentpack/media_access.go）

- `MediaAccessClaim{VersionID, MediaID, Purpose, SessionRef, ExpiresAt}` + Mint/Verify（独立 HMAC domain
  `contentpack/media-access/v1`）；TTL 沿用 10 分钟默认/1 小时上限；非一次性（无 jti），绑定版本+媒体+用途+TTL。
- `ValidMediaPurpose`：preview|download|platform_fetch；download → Content-Disposition attachment，其余 inline。
- 版本冻结/分发裁决沿用 Distributable 单点。

### store 层（server/internal/store/store_content_packs_store.go）

- CreateStoreContentPack / Get / ListStoreContentPacks（新表 + 旧 campaign 包查询适配 UNION，legacy 标记）。
- CreateStoreContentPackVersion（media_id 解析→同租户 content_media 行，跨租户/缺失一律 media_missing 不泄露存在性；
  快照冻结；幂等键重复 = ErrDuplicatePackVersion 同旧契约）。
- AddStoreReturnCandidate（exported：4595 唯一候选登记事务；同幂等键重放=同一行 replayed=true；只插 candidate，
  绝不动采用指针）。
- AdoptStoreContentPackVersion（Distributable 拒 blocked/grant_expired；前指针 retired+superseded 事件；记录 adopted_by+时间）。
- GetStoreAdoptedVersion / ListStoreAdoptedVersions（「换一个」候选池 = 已采用且当前可分发，排除当前）。
- BlockStoreContentPackVersion（同旧语义：指针保留、新分发拒绝、事件表记录）。
- BindActionContent（「采用后建立绑定」内部调用：version 必须 adopted 且属于本店 pack；upsert (tenant, action_id)）；
  GetActionContentBinding。
- RecordMediaAccessGrant（审计）。

### httpapi（server/internal/httpapi/handlers_store_content.go 新文件 + server.go 窄注册补丁）

管理面（session + authz.manage_content_packs，沿用 packTenantAction 纪律）：
- POST /api/v1/stores/{id}/content-packs {title, campaign_id?}（campaign 可空）
- GET  /api/v1/stores/{id}/content-packs（包+版本+采用指针一次读）
- POST /api/v1/stores/{id}/content-media（multipart 字段 file；真实上传，完成=字节已验证落盘；返回服务器实测元数据）
- POST /api/v1/stores/{id}/content-packs/{packId}/versions（media_id 语义字段，商家不填技术引用）
- POST /api/v1/stores/{id}/content-packs/{packId}/versions/{versionId}/adopt {platform}
- POST /api/v1/stores/{id}/content-packs/{packId}/versions/{versionId}/block {reason}

公共面（FEATURE_CONTENT_PACKS gate，off=统一 404）：
- GET  /api/v1/public/actions/{actionId}/content：绑定→包→服务器已采用版；返回 action_revision、content_binding_ref、
  selected_content_pack_version、candidates 摘要（换一个池）、content_kind、title/body/topics、公开 marking、
  media[{media_id,role,order,mime_type,byte_size,sha256,duration_ms,width,height,availability}]；无采用 404；
  blocked/grant 过期 409；不暴露授权原文/内部凭据。
- POST /api/v1/public/content/{version}/media-access {action_session_id?, media_ids[], purpose}：版本必须已采用且可分发；
  media 必须属于该版本；session_ref 仅作审计引用（会话权威归 3546，本票不做会话验证、也不把它当授权源——约束 8：
  引用不是后台授权）；签发短 TTL url/expires_at/mime_type/byte_size/sha256/filename + 审计落库；续签=同版本重签，
  blocked 后拒绝新签发。
- GET|HEAD /api/v1/public/content/media/{token}：验签+TTL→版本仍可分发（fail-closed）→media 属于版本→文件存在
  （缺 410 media_missing）→ http.ServeContent（Content-Type 服务器实测值、ETag=sha256、Accept-Ranges、单/多 Range、
  206/416、HEAD 零体）；purpose=download 时 attachment。

### web（/work/materials + 抽屉组件）

- 门店选择器（GET /stores，默认第一家）；上传入口接真实文件 input（不填 asset_id），multipart 直传
  /api/stores/{id}/content-media，成功后展示服务器实测元数据（MIME/时长/大小/哈希）——「登记成功≠上传完成」在此闭环。
- 内容包/候选/采用/换版抽屉（content-pack-panel.tsx 扩展 + 新抽屉组件）：候选预览（media-access purpose=preview 的
  URL 进 video/img）、明确采用、新候选换版、下载（purpose=download）按真实结果表述（「已开始下载」而非「已保存」）。
- 零收费调用：页面只有读/上传/采用/受控媒体 URL。

### 测试矩阵（TDD 逐任务红→绿→commit）

1. 迁移加载 + 表存在。
2. upload.MediaStore：真实 jpeg/png/mp4 fixture 验 MIME/哈希/时长宽高/大小上限/超限拒绝/未知魔数拒绝/同 sha 复用。
3. contentpack：MediaAccessClaim mint/verify/过期/篡改/用途白名单/TTL 上限；download disposition。
4. store：建包（活动可空/跨租户 NotFound/重复标题 409）/建版本（media_missing/幂等重放/版本号单调）/回流候选
  （重放零新增/不覆盖采用指针）/采用（blocked 422、grant 过期 422、指针切换旧版 retired+事件、adopted_by 记录）/
  换版后旧引用不变/BindActionContent（未采用拒、跨店拒、upsert）。
5. httpapi 管理面：全套端点 + authz（staff 403）+ 上传（真实字节→元数据；伪 MIME 不采信）。
6. httpapi 公共面：gate off 404；content 无采用 404；blocked 409；两次读一致；candidates 池排除当前且全部已采用；
  media-access 正例（url/expires/mime/size/sha/filename）+ 负例（错版本 404、错 media 422、blocked 409、坏 purpose 400）；
  受控 GET：200 全量、Range 单段 206 + Content-Range 正确、后缀/开区间 Range、HEAD（零体+头齐）、MIME 正确
  （绝不返回 HTML 冒充 mp4）、过期 token 410、篡改 400、blocked 后缓存 token 也拒绝、下载 disposition。
7. web vitest：materials 源断言扩展（真实上传接线/store 抽屉/preview+download 用途/诚实文案）+ 既有测试保持绿。

## 任务序列（每任务一 commit）

- T1 迁移 0050-0055（含 fixture 生成：ffmpeg 产 testdata/tiny.mp4 真实小视频）。
- T2 upload.MediaStore（红→绿）。
- T3 contentpack MediaAccessClaim（红→绿）。
- T4 store 层（红→绿）。
- T5 httpapi 管理面（红→绿）。
- T6 httpapi 公共面 + 受控媒体 GET（红→绿；Range/MIME 是硬验收）。
- T7 web materials + 抽屉（vitest 红→绿）。
- T8 全量验证（go test ./...：基线红 7 个 httpapi 测试不属本域不修；npx vitest run）+ 自审 + 报告。

## 验收对照（票面 T05/T06 + 反例）

- T05：同一 adopted version——商家预览、匿名下载、平台抓取取到相同 sha256；Range 分段正确；中文文件名/多图顺序正确。
- T06：采用新版后旧会话引用不受影响（旧版本行零改动）；换一个只切已采用候选；provider/账单零变化（页面无任何生成调用）。
- 反例：无商家 cookie 的媒体请求成功；错 tenant/version/media、撤权、到期、缺失、伪 MIME 各自明确拒绝；
  过期重试不触发生成/扣费；拒绝只影响对应动作。
