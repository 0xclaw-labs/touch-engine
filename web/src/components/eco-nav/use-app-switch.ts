"use client";

import { useCallback, useState } from "react";

export type AppSwitchResult = { ok: true; launchUrl: string } | { ok: false; message: string };

/**
 * HUI-2628: the one client seam that calls the app-switch BFF. The renderer
 * stays presentational; the browser sends only the two contract ids.
 */
export function useAppSwitch(tenantId: string, enabled = true) {
  const [launching, setLaunching] = useState<Record<string, boolean>>({});

  const switchApp = useCallback(async (appId: string, targetId: string): Promise<AppSwitchResult> => {
    if (!enabled) return { ok: false, message: "预览页不发起应用切换。" };
    setLaunching((current) => ({ ...current, [appId]: true }));
    try {
      const res = await fetch("/api/eco-nav/switch", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          ...(tenantId ? { "x-tenant-id": tenantId } : {}),
        },
        body: JSON.stringify({ app_id: appId, launch_target_id: targetId }),
      });
      const data = (await res.json().catch(() => ({}))) as { launch_url?: unknown; message?: unknown };
      if (!res.ok || typeof data.launch_url !== "string") {
        return { ok: false, message: typeof data.message === "string" ? data.message : "应用切换没有完成，也没有跳转。" };
      }
      return { ok: true, launchUrl: data.launch_url };
    } catch {
      return { ok: false, message: "网络异常。应用切换没有完成，也没有跳转。" };
    } finally {
      setLaunching((current) => ({ ...current, [appId]: false }));
    }
  }, [enabled, tenantId]);

  return { switchApp, launching };
}
