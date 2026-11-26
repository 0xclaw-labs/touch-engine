// 碰一碰统一门户 SSO 入口策略（零 Node 依赖，SSR 安全）。
//
// 边界约束：
// 1. 只在官方生态域（https://*.modelxing.com）或显式配置启用（且非第三方白标品牌域）
//    时展示「使用生态账号登录」，保护白标自定义品牌域的隔离与准入边界；
// 2. next 落点只允许站内合法路径（复用 safe-redirect 白名单）拼接生态域 origin，
//    与门户 safeReturnTarget（只认站内或 *.modelxing.com）双重对齐，防开放重定向。

import { sanitizeInSitePath } from "@/lib/safe-redirect";

export const AUTH_PORTAL_LOGIN_URL = "https://auth.modelxing.com/login";
export const TOUCH_APP_ID = "touch-engine";

export function isPortalSsoHost(hostname: string | null | undefined): boolean {
  if (typeof hostname !== "string") return false;
  const host = hostname.trim().toLowerCase();
  return host === "modelxing.com" || host.endsWith(".modelxing.com");
}

function isLoopbackHost(hostname: string | null | undefined): boolean {
  if (typeof hostname !== "string") return false;
  const host = hostname.trim().toLowerCase();
  return host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "[::1]";
}

/**
 * 判断当前浏览宿主是否可安全启用生态门户 SSO：
 * - 显式关闭（"0" / "false"）一律关闭；
 * - https 协议下的 modelxing.com / *.modelxing.com 自动开启；
 * - 显式开启（"1" / "true"）时允许生态域或本地 loopback，但仍拒绝第三方白标品牌域。
 */
export function canUsePortalSso(
  locationLike?: { protocol?: string; hostname?: string } | null,
  explicitFlag?: string | undefined,
): boolean {
  const flag = (explicitFlag ?? "").trim().toLowerCase();
  if (flag === "0" || flag === "false") return false;
  if (!locationLike) return false;
  const host = (locationLike.hostname ?? "").trim().toLowerCase();
  const protocol = (locationLike.protocol ?? "").trim().toLowerCase();
  if (protocol === "https:" && isPortalSsoHost(host)) return true;
  if ((flag === "1" || flag === "true") && (isPortalSsoHost(host) || isLoopbackHost(host))) {
    return true;
  }
  return false;
}

function safeNextOrigin(rawOrigin: string | null | undefined): string | null {
  if (typeof rawOrigin !== "string" || rawOrigin.trim() === "") return null;
  try {
    const parsed = new URL(rawOrigin);
    if (parsed.protocol !== "https:") return null;
    if (!isPortalSsoHost(parsed.hostname)) return null;
    return parsed.origin;
  } catch {
    return null;
  }
}

function safeInAppPath(rawPath: string | null | undefined): string | null {
  if (typeof rawPath !== "string" || rawPath.trim() === "") return null;
  const clean = sanitizeInSitePath(rawPath);
  if (!clean) return null;
  if (clean === "/api" || clean.startsWith("/api/")) return null;
  return clean;
}

export function buildPortalLoginUrl(options?: {
  next?: string | null;
  promptLogin?: boolean;
  currentOrigin?: string | null;
  currentPathname?: string | null;
}): string {
  const url = new URL(AUTH_PORTAL_LOGIN_URL);
  url.searchParams.set("app", TOUCH_APP_ID);
  if (options?.promptLogin) {
    url.searchParams.set("prompt", "login");
  }
  const origin = safeNextOrigin(options?.currentOrigin);
  if (origin) {
    const path = safeInAppPath(options?.next) ?? safeInAppPath(options?.currentPathname) ?? "/";
    url.searchParams.set("next", `${origin}${path}`);
  }
  return url.toString();
}

/** 浏览器侧跳往统一门户；SSR/无 window 环境下安全无副作用。 */
export function startPortalSso(options?: { promptLogin?: boolean; next?: string | null }): void {
  if (typeof window === "undefined") return;
  const target = buildPortalLoginUrl({
    promptLogin: options?.promptLogin,
    next: options?.next,
    currentOrigin: window.location.origin,
    currentPathname: window.location.pathname,
  });
  window.location.assign(target);
}
