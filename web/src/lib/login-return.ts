// HUI-2222 受控回跳白名单(商家门闸登录面)。
//
// 红线:登录后的落点只允许本应用路由;外域、协议相对(//)、反斜杠、百分号
// 编码、查询串一律丢弃——任何参数都不可能把登录商家带离本应用,门店/订单/
// 运营上下文也不可能经 return 参数走私(白名单只认纯路径,查询串整体拒绝)。
// 路径红线复用 HUI-1664 safe-redirect 的 sanitizeInSitePath;本文件只追加
// 商家门闸语境的两条规则:/api 不是页面;/admin 与 /agency 是本应用路由。

import { REDIRECT_KEYS, sanitizeInSitePath } from "./safe-redirect";

export function loginReturnTarget(get: (key: string) => string | null | undefined): string | null {
  for (const key of REDIRECT_KEYS) {
    const raw = get(key);
    if (typeof raw === "string" && raw !== "") {
      const target = sanitizeInSitePath(raw);
      if (target === null) return null;
      if (target === "/api" || target.startsWith("/api/")) return null;
      return target;
    }
  }
  return null;
}

/** 从 window.location.search 读回跳目标(SSR/无 window 环境安全返回 null)。 */
export function loginReturnTargetFromSearch(search: string): string | null {
  if (typeof search !== "string" || search === "") return null;
  const params = new URLSearchParams(search);
  return loginReturnTarget((key) => params.get(key));
}
