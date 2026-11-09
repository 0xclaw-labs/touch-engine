"use client";

// HUI-4593 走查收尾（r8）：受控进入落地页（/ M1）的「返回代理台」横幅挂载。
// 指挥集成裁定：2628 独占 TodayDesk 结构；本挂载是自有组件（AgentBanner）的
// 收尾，2628 重构时收编。不改 TodayDesk 内部结构。
//
// 实现纪律（修复轮收尾实测教训）：根页已有 TodayDesk 的 MerchantGate 会话实例，
// 这里**不再另起会话钩子** —— 两个实例会竞争模块级租户操作序号（beginTenantOp），
// 输家的 confirm 被判 stale 丢弃、role 永远为空，横幅永远不渲染。
// 改为直读记忆租户（switchTenant 落地时已写入）→ whoami / access/effective
// （BFF 同源 cookie 模式，与 merchant-session 同一取数路径）；租户名缺省时回退
// 成员列表 display_name。非代理渲染 null，商家视图零打扰。

import { useCallback, useEffect, useState } from "react";
import { AgentBanner, type AccessEffective } from "@/components/work/settings/settings-desk";
import { TENANT_STORAGE_KEY } from "@/lib/workspace-tenant";

interface ActingState {
  role: string;
  tenantId: string;
  tenantName: string;
}

export function AgentWorkBanner() {
  const [acting, setActing] = useState<ActingState | null>(null);
  const [access, setAccess] = useState<AccessEffective | null>(null);

  const load = useCallback(async () => {
    try {
      const tenantId = window.localStorage.getItem(TENANT_STORAGE_KEY) ?? "";
      if (!tenantId) {
        setActing(null);
        return;
      }
      const headers = { "x-tenant-id": tenantId };
      const res = await fetch("/api/whoami", { headers });
      if (!res.ok) {
        setActing(null);
        return;
      }
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      const role = typeof data.role === "string" ? data.role : "";
      let tenantName = typeof data.tenant_name === "string" ? data.tenant_name : "";
      if (!tenantName) {
        // 品牌上下文未开时 whoami 不带租户名:回退成员列表 display_name
        const m = await fetch("/api/session/memberships");
        if (m.ok) {
          const md = (await m.json().catch(() => ({}))) as {
            items?: { tenant_id?: string; display_name?: string }[];
          };
          tenantName =
            (md.items ?? []).find((i) => i.tenant_id === tenantId)?.display_name ?? "";
        }
      }
      if (role !== "agent") {
        setActing(null);
        return;
      }
      setActing({ role, tenantId, tenantName });
      const acc = await fetch("/api/access/effective", { headers });
      if (acc.ok) {
        setAccess((await acc.json().catch(() => ({}))) as AccessEffective);
      }
    } catch {
      setActing(null); // 网络失败不渲染横幅(诚实:不知道就不显示)
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!acting) return null;
  return <AgentBanner tenantName={acting.tenantName} access={access} backHref="/agency" />;
}
