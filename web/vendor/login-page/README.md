# @painuo/login-page

派诺生态统一登录组件：7 处 web 登录面共用的组件真源（spec：`docs/superpowers/specs/2026-10-12-login-page-unification-design.md`）。Claude 式分屏版式为刚性基准，视觉经 props 做品牌皮肤；纯 React 组件 + CSS Modules，零 Next 依赖（路由跳转与 `?next=` 校验经 props 注入），`peerDependencies: react >=18`。

认证：内建 platform-identity 标准客户端为缺省实现，非标后端经 `auth` 覆写口桥接。组件边界止于「拿到会话数据」——落 cookie / 刷新 / epoch 竞态归接入仓。

## 使用

```tsx
import { LoginPage } from "@painuo/login-page";

<LoginPage
  brand={{ name: "数海观澜AIGC" }}
  onAuthenticated={(result) => {}}
  navigate={(path) => router.replace(path)}
/>
```

## Props 速览

| prop | 类型 | 缺省 | 说明 |
|---|---|---|---|
| `brand` | `{ name; tagline?; accent?; media? }` | 必填 | `tagline` 缺省「账号由主管理员开通，不能自助注册。」（开放注册产品必须覆写）；`accent` 覆写时 `{ base, hover, weak, onAccent }` 四值必须齐全；`media` 为 split 右栏媒体，`null` = 底色+字标垫 |
| `modes` | `{ otp?; password? }` | 双开 | `otp: false` → 固定密码态；`password: false` → 固定验证码态；双边关回退双开显分段 |
| `auth` | `Partial<AuthAdapter>` | 内建客户端 | 叠加覆写 `startEmailCode` / `verifyEmailCode` / `signIn`，签名与 goboost-fd `lib/auth.ts` 对齐 |
| `identifierTransform` | `(identifier: string) => string` | 原样提交 | 密码登录标识变换：legacy 用户名 → identity 合成邮箱（如 `<external_key 小写>@users.dhuman.modelxing.com`）；只作用于内建 auth 路径，OTP 的 email 输入不经过它 |
| `messages` | `{ otpInvalid?; generic? }` | 包内等价表 | `otpInvalid` 覆写验证码错误文案；`generic(err)` 整体覆写其余错误文案 |
| `sso` | `{ available; start }` | 不渲染 | `available: true` 时表单下方渲染「使用生态账号登录」次级按钮，点击调 `start()`（PKCE 等会话语义归接入仓） |
| `onAuthenticated` | `(result: AuthResult) => void` | 必填 | 认证成功回调，`AuthResult` 形状归接入仓解释 |
| `navigate` | `(path: string) => void` | 必填 | 路由注入（如 `router.replace`） |
| `nextResolver` | `(search: string) => string` | 站内安全缺省 | 读 `?next=`；缺省只接受 `/` 开头且非 `//` 的站内路径，否则回 `/`（开放重定向红线） |
| `nextOverride` | `string \| null` | — | 嵌入场景直接给定落点（仍须站内），等价 embedded 语义：compact 卡、无右栏媒体 |
| `layout` | `"split" \| "compact"` | `"split"` | split=独立登录页（分屏+媒体）；compact=admin 内联/嵌入（单列卡） |
| `className` | `string` | — | 根元素附加类，宿主极少量定位用；禁止借此改版式 |

## 接入方式（vendor 同步）

npm git 依赖不支持仓内子目录，采用 vendor 拷贝分发（spec §5）。消费仓提交 `vendor/login-page/`，源版本记录在 `vendor/login-page/.source-ref`。

```bash
# 在 public-ai 检出内，把 packages/login-page 打包写入消费仓（整体替换）：
scripts/login-page-sync.sh --into <消费仓路径> [--ref <tag|sha>]

# 消费仓日常/CI 漂移自检（与打包规则逐文件比对）：
scripts/login-page-sync.sh --check <消费仓路径>
# goboost-fd 侧的快速自检（自洽 + 与已提交基线比对）：
bash scripts/check-login-vendor.sh
```

版本约定：交付版本在 public-ai 打 tag `login-page/vX.Y.Z`（semver）；升级 = 重跑 `--into` 出 PR，显式可审。勿手改 `vendor/login-page/`——改源后重新同步。

## 内建客户端端点面

缺省 `auth` 客户端（`createBuiltInAuth`，可经 `fetchImpl` 注入）覆盖 platform-identity 标准面：

| 方法 | 端点 | 请求体 |
|---|---|---|
| `startEmailCode` | `POST /api/auth/email/code/start` | `{ email }` |
| `verifyEmailCode` | `POST /api/auth/email/code/verify` | `{ email, code }` |
| `signIn` | `POST /api/auth/password` | `{ email, password }` |

会话语义（session 查询、epoch、acceptLogin）不在组件边界内，归接入仓（spec §2 非目标）。

## 开发

```bash
cd packages/login-page
npm install
npm test        # node:test 组件行为 + CSS 语法哨兵（postcss 解析 src 下全部 .module.css）
npx tsc --noEmit
```
