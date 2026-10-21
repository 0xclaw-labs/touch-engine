"use client";

import { useEffect, useMemo, useRef, useState, type CSSProperties, type FormEvent } from "react";

import { ApiFailure } from "./apiError.ts";
import { createBuiltInAuth } from "./builtInAuth.ts";
import { Button } from "./internal/Button.tsx";
import { Segmented } from "./internal/Segmented.tsx";
import { TextField } from "./internal/TextField.tsx";
import type { AuthAdapter, LoginPageProps } from "./types.ts";

import styles from "./login.module.css";

// ——— 错误文案映射（与 goboost-fd lib/errors.ts 的 loginErrorMessage 对齐的自持等价表）———
//
// 后端的 code 是给机器看的；登录面只保留登录语境会用到的码，机制逐条对齐：
// 未知码不假装知道原因（只给「没能完成」+ 原始码，绝不透传上游原文——
// 那可能是 JSON、内部操作名或时间戳，不是给人看的话），404 文案里绝不出现「权限」。

const LOGIN_SESSION_UNAVAILABLE_TEXT =
  "身份已验证，但暂时无法连接创作账号。请重试登录；如果持续失败，请联系管理员。";
const LOGIN_UNREACHABLE = "连不上服务器，确认后端已启动后重试。";

const MESSAGES: Record<string, string> = {
  not_found: "没有找到这个内容。它可能已被删除，或者不在当前项目里。",
  conflict: "已经有同名的记录了，换个名字再试。",
  bad_request: "提交的内容不符合要求，检查一下必填项再试。",
  internal: "服务端出错了，这次操作没有生效。稍后重试；如果一直失败，把发生时间告诉管理员。",
  unauthorized: "登录状态已失效，请重新登录。",
  invalid_credentials: "用户名或密码不正确。",
  rate_limited: "操作太频繁了，请稍后再试。",
  business_session_unavailable: LOGIN_SESSION_UNAVAILABLE_TEXT,
  forbidden: "这个操作需要项目的「可管理」权限。",
  cloud_only: "这项能力要等云端部署后才开放，现在还不能用——不是故障。",
};

/** 这些码的后端 message 里带具体数值，丢掉就等于让用户去猜。 */
const DETAILED_CODES = new Set(["bad_request"]);

type ErrorInfo = {
  code: string;
  status: number;
  message: string;
  detail: string;
  retryAt?: string;
  /** 是否命中已知错误码。false 表示我们并不知道发生了什么，文案里也不要假装知道。 */
  known: boolean;
};

/** retryAt（ISO 8601）→ 今天的 HH:MM；解析不出就放弃时间提示。 */
function formatRetryAt(retryAt?: string): string {
  if (!retryAt) return "";
  const at = new Date(retryAt);
  if (Number.isNaN(at.getTime())) return "";
  return at.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

function describeError(err: unknown): ErrorInfo {
  if (err instanceof ApiFailure) {
    const retryAt = typeof err.retryAt === "string" ? err.retryAt : undefined;
    const base = MESSAGES[err.code];
    if (base) {
      return { code: err.code, status: err.status, message: base, detail: err.message, retryAt, known: true };
    }
    // 按状态码兜底，别把一句英文原文甩给用户。
    const byStatus =
      err.status === 404
        ? MESSAGES.not_found
        : err.status === 429
          ? MESSAGES.rate_limited
          : err.status === 501
            ? MESSAGES.cloud_only
            : undefined;
    if (byStatus) {
      return { code: err.code, status: err.status, message: byStatus, detail: err.message, retryAt, known: true };
    }
    return {
      code: err.code,
      status: err.status,
      message: "这个操作没能完成。",
      detail: err.message,
      retryAt,
      known: false,
    };
  }
  // 非 ApiFailure（网络中断等）没有 status，与 5xx 同落「连不上」文案。
  return {
    code: err instanceof TypeError ? "network" : "unknown",
    status: 0,
    message: "这个操作没能完成。",
    detail: err instanceof Error ? err.message : "",
    known: false,
  };
}

function errorMessage(info: ErrorInfo): string {
  if (info.code === "rate_limited") {
    const when = formatRetryAt(info.retryAt);
    return when ? `请求太频繁了，${when} 后可重试。` : MESSAGES.rate_limited;
  }
  if (!info.known) {
    return `${info.message}（错误码 ${info.code}）`;
  }
  if (DETAILED_CODES.has(info.code) && info.detail && info.detail !== info.message) {
    return `${info.message}（${info.detail}）`;
  }
  return info.message;
}

/** 登录页专用一行式文案；代理 500/504 不复用生成中断文案。 */
function loginErrorMessage(err: unknown): string {
  const info = describeError(err);
  if (info.code === "business_session_unavailable") return LOGIN_SESSION_UNAVAILABLE_TEXT;
  if (info.status === 401) return MESSAGES.invalid_credentials;
  if (info.status === 0 || info.status >= 500) return LOGIN_UNREACHABLE;
  return errorMessage(info);
}

/** 验证码错误单独归一句话；其余走 generic（覆写口或缺省表）。 */
function otpErrorMessage(err: unknown, messages: LoginPageProps["messages"]): string {
  if (err instanceof ApiFailure && (err.code === "invalid_otp" || err.status === 401)) {
    return messages?.otpInvalid ?? "验证码不正确或已过期";
  }
  return messages?.generic ? messages.generic(err) : loginErrorMessage(err);
}

function genericErrorMessage(err: unknown, messages: LoginPageProps["messages"]): string {
  return messages?.generic ? messages.generic(err) : loginErrorMessage(err);
}

/** 缺省 nextResolver：只接受站内以 / 开头且不以 // 开头的 ?next=，否则回 /（开放重定向红线）。 */
function defaultNextResolver(search: string): string {
  const raw = new URLSearchParams(search).get("next");
  if (!raw || !raw.startsWith("/") || raw.startsWith("//")) return "/";
  return raw;
}

function isOnSitePath(path: string): boolean {
  return path.startsWith("/") && !path.startsWith("//");
}

/**
 * 统一登录组件：三态状态机（验证码请求 → 验证码校验 / 密码）+ 分屏版式。
 * 版式刚性、皮肤柔性——版式只在 split/compact 之间切换，其余视觉经 brand props。
 */
export function LoginPage({
  brand,
  auth,
  identifierTransform,
  modes,
  messages,
  links,
  sso,
  onAuthenticated,
  navigate,
  linkNavigate,
  nextResolver,
  nextOverride,
  layout = "split",
  className,
}: LoginPageProps) {
  // 覆写口：Partial<AuthAdapter> 叠在内建 platform-identity 客户端之上。
  const client = useMemo(() => ({ ...createBuiltInAuth(), ...auth }) as AuthAdapter, [auth]);

  // 站内链接（忘记密码/立即注册）与登录落点分道：navigate 可能被宿主挂「会话已认证」
  // 闸门（登录成功落点防护），登录页用户永远未认证，链接复用它就是死链——
  // 链接跳转走 linkNavigate（宿主注 router.push），缺省回退 navigate。
  const go = linkNavigate ?? navigate;

  // modes 语义（钉死）：modes.otp===false 且 password 未关 → 无分段固定密码态；
  // modes.password===false → 无分段固定 otp 态；双边关与缺省同待（回退双开显分段，缺省 otp 态）。
  const otpEnabled = modes?.otp !== false;
  const pwEnabled = modes?.password !== false;
  const showTabs = otpEnabled === pwEnabled;
  const otpOnly = !otpEnabled && pwEnabled;

  const [tab, setTab] = useState<"otp" | "password">(otpOnly ? "password" : "otp");
  const [otpStep, setOtpStep] = useState<"request" | "verify">("request");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [sentAt, setSentAt] = useState<number | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const verifying = useRef(false);

  useEffect(() => {
    if (otpStep !== "verify" || sentAt == null) return;
    // node 测试 harness 无 window/定时器；浏览器侧 250ms tick 驱动重发倒计时
    if (typeof window === "undefined") return;
    const id = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(id);
  }, [otpStep, sentAt]);

  // reduced-motion 下右栏媒体退为静帧海报（对照门户 hero 的处理）
  const [reducedMotion, setReducedMotion] = useState(false);
  useEffect(() => {
    // node 测试 harness 无 matchMedia（goboost-fd 已踩过）
    if (typeof window === "undefined") return;
    setReducedMotion(window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches ?? false);
  }, []);

  const remain = sentAt == null ? 0 : Math.max(0, 60 - Math.ceil((now - sentAt) / 1000));

  /** 登录后的落点。override 优先（仍须站内）；否则交 nextResolver 读 `?next=`。 */
  function nextPath(): string {
    if (nextOverride != null) return isOnSitePath(nextOverride) ? nextOverride : "/";
    const search = typeof window !== "undefined" ? window.location.search : "";
    return (nextResolver ?? defaultNextResolver)(search);
  }

  function switchTab(next: string) {
    if (next !== "otp" && next !== "password") return;
    setError(null);
    setTab(next);
  }

  async function sendCode(addr: string) {
    await client.startEmailCode(addr);
    setSentAt(Date.now());
    setNow(Date.now());
    setCode("");
    setOtpStep("verify");
  }

  async function onRequest(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (busy) return;
    const addr = email.trim();
    if (!addr || !addr.includes("@")) {
      setError("请填写用来收信的邮箱。");
      return;
    }
    setError(null);
    setBusy(true);
    try {
      await sendCode(addr);
    } catch (err) {
      setError(genericErrorMessage(err, messages));
    } finally {
      setBusy(false);
    }
  }

  async function onResend() {
    if (busy || remain > 0) return;
    const addr = email.trim();
    if (!addr) return;
    setError(null);
    setBusy(true);
    try {
      await sendCode(addr);
    } catch (err) {
      setError(genericErrorMessage(err, messages));
    } finally {
      setBusy(false);
    }
  }

  async function submitCode(value: string) {
    if (verifying.current || busy) return;
    const digits = value.replace(/\D/g, "");
    if (digits.length !== 6) {
      setError("请输入 6 位验证码。");
      return;
    }
    verifying.current = true;
    setError(null);
    setBusy(true);
    try {
      const result = await client.verifyEmailCode(email.trim(), digits);
      // 会话语义（落 cookie/刷新）归接入仓；组件边界止于「拿到会话数据」
      onAuthenticated(result);
      navigate(nextPath());
    } catch (err) {
      verifying.current = false;
      setError(otpErrorMessage(err, messages));
      setBusy(false);
    }
  }

  async function onVerify(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    await submitCode(code);
  }

  async function onPassword(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (busy) return;
    const name = username.trim();
    if (!name || !password) {
      setError("请填写用户名和密码。");
      return;
    }
    setError(null);
    setBusy(true);
    try {
      // 变换只作用于内建 auth 路径：覆写方自带标识语义，不干预（types.ts 契约）。
      const identifier = !auth && identifierTransform ? identifierTransform(name) : name;
      const result = await client.signIn(identifier, password);
      onAuthenticated(result);
      navigate(nextPath());
    } catch (err) {
      setError(genericErrorMessage(err, messages));
      setBusy(false);
    }
  }

  const onVerifyScreen = tab === "otp" && otpStep === "verify";
  // 嵌入语义：无右栏媒体、无页面衬底（compact 卡）
  const compact = layout === "compact" || nextOverride != null;
  // 注册开放时 tagline 缺省改走开放口径（brand.tagline 覆写仍优先）；
  // 否则维持封闭缺省——绝不能在挂注册入口的同时宣称「不能自助注册」。
  const defaultTagline = links?.register
    ? "使用邮箱验证码登录；未开通的邮箱验证通过后自动创建账号。"
    : "账号由主管理员开通，不能自助注册。";

  // accent 注入：覆写时四值必须齐全（types.ts 契约），根元素 inline 变量整体换肤
  const accentVars = brand.accent
    ? ({
        "--lpg-accent": brand.accent.base,
        "--lpg-accent-hover": brand.accent.hover,
        "--lpg-accent-weak": brand.accent.weak,
        "--lpg-fg-on-accent": brand.accent.onAccent,
      } as CSSProperties)
    : undefined;
  const rootClass = [compact ? styles.compact : styles.page, className].filter(Boolean).join(" ");

  // 副标文案：输码屏固定发码提示；常态 brand.tagline 覆写（可传空串显式关闭，
  // 宿主注册页用），未覆写才走缺省口径。空文案不渲染空 <p>。
  const subText = onVerifyScreen
    ? "如果这个邮箱已开通，验证码已发送，10 分钟内有效。"
    : (brand.tagline ?? defaultTagline);

  const head = (
    <div className={styles.head}>
      <h1 className={styles.brand}>{onVerifyScreen ? "输入验证码" : brand.name}</h1>
      {subText ? <p className={styles.sub}>{subText}</p> : null}
    </div>
  );

  const body = (
    <>
      {showTabs && !onVerifyScreen ? (
        <Segmented
          items={[
            { key: "otp", label: "邮箱" },
            { key: "password", label: "账号密码" },
          ]}
          value={tab}
          onChange={switchTab}
          ariaLabel="登录方式"
          className={styles.modeTabs}
        />
      ) : null}

      {error ? (
        <p className={styles.error} role="alert">
          {error}
        </p>
      ) : null}

      {tab === "otp" && otpStep === "request" ? (
        <form onSubmit={onRequest} noValidate>
          <TextField
            label="邮箱"
            type="email"
            value={email}
            onChange={setEmail}
            autoComplete="email"
            autoFocus
            disabled={busy}
          />
          {/* 每屏一枚品牌胶囊 CTA：发码步即发码钮，输码/密码步即「登录」 */}
          <Button type="submit" variant="primary" block loading={busy}>
            发送验证码
          </Button>
        </form>
      ) : null}

      {onVerifyScreen ? (
        <form onSubmit={onVerify} noValidate>
          <div className={styles.echo}>
            <span className={styles.echoAddr}>{email}</span>
            <button
              type="button"
              className={styles.linkBtn}
              onClick={() => {
                setError(null);
                setCode("");
                setOtpStep("request");
              }}
              disabled={busy}
            >
              改邮箱
            </button>
          </div>
          <TextField
            label="验证码"
            value={code}
            onChange={(value) => {
              const digits = value.replace(/\D/g, "").slice(0, 6);
              setCode(digits);
              if (digits.length === 6 && code.length !== 6) void submitCode(digits);
            }}
            inputMode="numeric"
            autoComplete="one-time-code"
            autoFocus
            maxLength={6}
            disabled={busy}
          />
          <Button type="submit" variant="primary" block loading={busy}>
            登录
          </Button>
          {/* 重发是补救动作，降为文字链，不与「登录」并列抢权重 */}
          <p className={styles.resend}>
            {remain > 0 ? (
              <span>{remain} 秒后可重发</span>
            ) : (
              <button
                type="button"
                className={styles.linkBtn}
                onClick={() => void onResend()}
                disabled={busy}
              >
                重发验证码
              </button>
            )}
          </p>
        </form>
      ) : null}

      {tab === "password" ? (
        <>
          <form onSubmit={onPassword} noValidate>
            <TextField
              label="用户名"
              value={username}
              onChange={setUsername}
              autoComplete="username"
              autoFocus
              disabled={busy}
            />
            <TextField
              label="密码"
              type="password"
              value={password}
              onChange={setPassword}
              autoComplete="current-password"
              disabled={busy}
            />
            <Button type="submit" variant="primary" block loading={busy}>
              登录
            </Button>
          </form>
          <p className={styles.foot}>
            {showTabs ? (
              <button type="button" className={styles.linkBtn} onClick={() => switchTab("otp")}>
                邮箱验证码登录
              </button>
            ) : null}
            {links?.forgotPassword ? (
              <button
                type="button"
                className={styles.linkBtn}
                onClick={() => go(links.forgotPassword!)}
              >
                忘记密码？
              </button>
            ) : (
              <span>只有用户名时找主管理员重置。</span>
            )}
          </p>
        </>
      ) : null}

      {/* SSO 是另一条入口，不是本表单的一步：次级变体常驻表单下方（文案与
          shuhai-guanlan 现网一致）；start 的会话语义（PKCE 跳转等）归接入仓 */}
      {sso?.available ? (
        <Button type="button" variant="secondary" block onClick={() => sso.start()}>
          使用生态账号登录
        </Button>
      ) : null}

      {/* 注册入口同 SSO 逻辑：另一条路，不是表单一步；verify 中间态不渲染，不打断输码焦点 */}
      {links?.register && !onVerifyScreen ? (
        <p className={styles.foot}>
          <span>
            还没有账号？
            <button
              type="button"
              className={styles.linkBtn}
              onClick={() => go(links.register!)}
            >
              立即注册
            </button>
          </span>
        </p>
      ) : null}
    </>
  );

  if (compact) {
    return (
      <div className={rootClass} style={accentVars}>
        <div className={styles.card}>
          {head}
          {body}
        </div>
      </div>
    );
  }

  // 右栏媒体：无 media 时以底色垫（types.ts 契约）；reduced-motion 退静帧海报
  const media = brand.media;
  return (
    <main className={rootClass} style={accentVars}>
      <div className={styles.split}>
        <div className={styles.left}>
          {head}
          <div className={styles.card}>{body}</div>
        </div>
        <div className={styles.mediaPanel} aria-hidden="true">
          {media && !reducedMotion && media.kind !== "image" ? (
            <video
              className={styles.media}
              src={media.src}
              poster={media.poster}
              muted
              loop
              autoPlay
              playsInline
              preload="auto"
            />
          ) : media ? (
            <img className={styles.media} src={media.kind === "image" ? media.src : media.poster} alt="" />
          ) : null}
        </div>
      </div>
    </main>
  );
}

export default LoginPage;
