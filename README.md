# touch-engine(碰一碰 · 商家线下活动独立应用)

碰一碰核心 = 商家线下活动/内容分发独立应用底座(平台生态 T0/HUI-1746):Next.js + Go/SQLite 独立 BFF,
商家后台(门店/活动/素材引用/活动链接短码)+ 公共活动页两套路由区,NFC/QR 共用原生 Campaign,
复用 public-ai 身份/资产设施;不内嵌接单库、不做 public-ai 全局库、不做多门店治理。
领域边界见 public-ai `docs/adr/0001`(D3:门店/活动/奖励归碰一碰应用;D5:身份≠租户成员≠资源授权≠付费权限)。

## 布局

```
server/    Go 服务:net/http + modernc.org/sqlite(纯 Go)+ embed 迁移,监听 127.0.0.1:18240
web/       Next.js app router:BFF 即 src/app/api/*,页面只打 /api/*,dev/start 端口 18340
             src/app/admin/*  商家后台路由区(平台登录 + 租户校验)
             src/app/c/[code] 公共活动页路由区(游客只读,无会话概念)
deploy/    env 配置样例(只有键名,无真值;真值放 /etc/touch-engine/)
docs/      计划与文档
_reports/  本地验收证据(不入库,gitignore)
```

## 本机运行

```bash
# 1) 服务端
cd server
TOUCH_HTTP_ADDR=127.0.0.1:18240 \
TOUCH_DB_PATH=data/touch.db \
TOUCH_INTERNAL_TOKEN=<随机串> \
PLATFORM_IDENTITY_BASE_URL=http://127.0.0.1:18101 \
PLATFORM_IDENTITY_TOKEN=<本 app 专用 identity 令牌> \
FEATURE_UPLOAD=on \
PLATFORM_UPLOAD_BASE_URL=http://127.0.0.1:18104 \
PLATFORM_UPLOAD_TOKEN=<本 app 专用 upload 令牌> \
go run ./cmd/touch-server

# 1b) 引导(运维子命令,不在 HTTP 面暴露)
go run ./cmd/touch-server provision-tenant -db data/touch.db -name 商家A
go run ./cmd/touch-server provision-member -db data/touch.db -tenant <tnt_*> -principal usr_* -role owner

# 2) 前端 BFF
cd web
TOUCH_SERVER_URL=http://127.0.0.1:18240 \
TOUCH_INTERNAL_TOKEN=<同上随机串> \
npm run dev
# 商家后台 http://localhost:18340/admin;公共活动页 http://localhost:18340/c/<短码>
```

配置门:缺任一平台依赖键时服务仍可启动(健康探针 `GET /healthz` 可见 degraded 与缺键清单),
但一切鉴权动作 fail-closed 显式报错(503 `config_gate` / `identity_unavailable`),
绝不伪造成功。`FEATURE_UPLOAD=off` 时创建素材引用显式 503 `upload_disabled`。

## 测试

```bash
cd server && go test ./...   # 权限矩阵/身份纪律/短码五态/状态机/持久化/配置门
cd web && npm test           # BFF 转发语义 + 后台×游客×租户矩阵经 BFF 层复验
cd web && npm run build
```

## 领域根(最小表,全部 tenant 作用域 + 审计字段)

| 表 | 说明 |
|---|---|
| tenants / members | 租户、平台 principal 的成员引用(owner/staff);principal_ref 只来自 identity 解析,不可变 |
| stores | 单门店最小字段(name/address);多门店治理不做 |
| campaigns | 四态 draft/active/paused/ended + 有效期窗口 + 门店 + 素材引用;order_ref 为可选不透明引用(永不解释) |
| campaign_links | 受控定位短码:服务端随机生成的不透明码 → campaign,可停用;**不嵌凭证/客户信息/任意跳转 URL** |
| campaign_assets | 引用平台 upload 资产 asset_id + 版本(sha256 锚点),不复制文件 |

## 红线(代码路径可查证)

- **发布纪律(DEPLOY-STANDARD-2026-11-13)**:本仓尚无 CI 发布入口(当前人工上 Primary A)。待建 `.github/workflows/deploy.yml`(push main → CI 全绿 → 自动部署;凭据=专用部署钥匙 `ci-deploy-120`,repo secret `VPS_DEPLOY_KEY`)前,人工发布逐次留痕;**个人/机器散钥匙不参与发布**。
- 公共活动页与商家后台完全分离:游客只读明确公开字段(标题/公开内容/有效期),
  写操作 401/403;非有效短码统一 HTTP 404 + 状态文案,不泄露后台存在性。
- 重型生成是商家主动行为:公共路由到任何生成/付费调用之间不存在调用边。
- 真实平台服务缺失显式报错,无 stub 模式、无万能 token、无 legacy 内部令牌路径。

## 品牌壳(HUI-2053,FEATURE_BRAND 默认 off)

碰一碰只读消费 public-ai Brand Registry 的展示契约(`GET /internal/v1/brand/context`)。
本仓库不保存品牌库,也不把「活的碰一碰活动已经在对端打通」写成事实:
品牌主机只决定展示和是否放行登录/公共页,不能改租户、不能改付款主体、不能代替成员资格。
短码仍只定位公开活动。品牌暂停、域名异常、租户暂停/冻结/退出是不同状态。

## 顾客预览与手动发布(HUI-1670)

公共活动页可以预览文案、导出手动发布步骤,并保存顾客对本次内容和账号的确认。
发布主体是参与活动的顾客,不能改成商家账号再记成顾客作品。
抖音、快手、小红书、视频号的唤起编辑器、授权发布、确认发布都关闭:
本部署没有已验证的开放平台应用,本票也不授权生产外发。
登记适配器不会把平台显示成可用。按钮、导出和顾客自报都不是发布成功,
没有官方回执时不发已发布奖励,也不计入已发布视频数。
完播、点赞、POI 曝光保持未知。
