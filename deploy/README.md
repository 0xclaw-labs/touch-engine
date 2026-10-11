# deploy 配置样例

两个样例文件只含键名与说明,**永不写入真实 token**;真值按 public-ai checklist 惯例放服务器
`/etc/touch-engine/*.env`(0640,不进 git)。

- `touch-server.env.example`:Go 服务。含 app 配置、platform identity(必配)、
  upload(素材引用校验,默认 off)、notify/task(仅脚手架,默认 off)。
  `TOUCH_PUBLIC_PERMISSION_URL` 与 `TOUCH_MATRIX_DRAFT_URL` 默认空,空则不拨号。
  本地验收可在 127.0.0.1:18741 在听时写成 `http://127.0.0.1:18741`,这不是生产默认值。
- `touch-web.env.example`:Next.js BFF。只指向 loopback 的 Go 服务。

键名约定:

- 平台服务键名对齐 `PLATFORM_*_BASE_URL` / `PLATFORM_*_TOKEN`
  (HUI-1723 inventory §4);每 app × 每服务一把专用令牌,由平台侧写入
  `IDENTITY_APP_TOKENS` / `UPLOAD_APP_TOKENS` / `NOTIFY_APP_TOKENS` / `TASK_APP_TOKENS`,
  禁止万能 token,禁止 legacy `PLATFORM_INTERNAL_TOKEN` 路径。
- 本应用自有键以 `TOUCH_` 前缀;`FEATURE_UPLOAD`/`FEATURE_NOTIFY`/`FEATURE_TASK` 默认 off。
