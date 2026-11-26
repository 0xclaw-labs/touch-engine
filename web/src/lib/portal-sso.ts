// 碰一碰统一门户 SSO 入口策略（零 Node 依赖，SSR 安全）。
//
// 边界约束：
// 1. 仅当显式配置 NEXT_PUBLIC_TOUCH_SSO_ENABLED === "1" 且处于官方生态域
//    （https://modelxing.com 或 https://*.modelxing.com）时启用门户 SSO，
//    既避免门户未登记 touch-engine 前默认触发原地死循环，也保护第三方白标自定义品牌域隔离；
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

/**
 * 判断当前浏览宿主是否可安全启用生态门户 SSO：
 * - 仅当显式配置 NEXT_PUBLIC_TOUCH_SSO_ENABLED === "1" 且处于官方生态域
 *   （https://modelxing.com 或 https://*.modelxing.com）时返回 true；
 * - 未显式开启时默认关闭，第三方白标品牌域与非 HTTPS 来源一律返回 false。
 */
export function canUsePortalSso(
  locationLike?: { protocol?: string; hostname?: string } | null,
  explicitFlag?: string | undefined,
): boolean {
  if ((explicitFlag ?? "").trim() !== "1") return false;
  if (!locationLike) return false;
  const host = (locationLike.hostname ?? "").trim().toLowerCase();
  const protocol = (locationLike.protocol ?? "").trim().toLowerCase();
  return protocol === "https:" && isPortalSsoHost(host);
}

/** 浏览器位置与导航出口（生产直读 window.location，单测可安全拦截白标域/生态域与跳转顺序）。 */
export const portalBrowser = {
  location(): { protocol: string; hostname: string; origin: string; pathname: string } | null {
    return typeof window === "undefined" ? null : window.location;
  },
  assign(url: string): void {
    if (typeof window !== "undefined") {
      window.location.assign(url);
    }
  },
};

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
  const loc = portalBrowser.location();
  if (!loc) return;
  const target = buildPortalLoginUrl({
    promptLogin: options?.promptLogin,
    next: options?.next,
    currentOrigin: loc.origin,
    currentPathname: loc.pathname,
  });
  portalBrowser.assign(target);
}

/**
 * 无席位态切号：先 await 本地退出接口清掉本域 touch_session Cookie，
 * 成功后再触发 onCleared 并跳转门户 ?prompt=login，避免旧 Cookie 死锁。
 */
export async function switchPortalAccount(options?: {
  next?: string | null;
  currentOrigin?: string | null;
  currentPathname?: string | null;
  onCleared?: () => void;
}): Promise<boolean> {
  const loc = portalBrowser.location();
  if (!loc) return false;
  let result: Response;
  try {
    result = await fetch("/api/auth/logout", { method: "POST" });
  } catch {
    return false;
  }
  if (!result.ok) return false;
  options?.onCleared?.();
  const target = buildPortalLoginUrl({
    promptLogin: true,
    next: options?.next,
    currentOrigin: options?.currentOrigin ?? loc.origin,
    currentPathname: options?.currentPathname ?? loc.pathname,
  });
  portalBrowser.assign(target);
  return true;
}
