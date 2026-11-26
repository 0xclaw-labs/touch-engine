"use client";

import type { ReactNode } from "react";
import { usePathname } from "next/navigation";
import { SERVICE_DESK_HREF, SERVICE_DESK_LABEL } from "@/lib/work/service-desk";
import {
  isNavAllowedForRole,
  roleDisplayLabel,
  switchableTenants,
  type WorkNavItem,
} from "@/lib/work/workbench-shell";
import type { MembershipScope } from "@/lib/workspace-tenant";

const LINKS: readonly WorkNavItem[] = [
  { href: "/", label: "今天" },
  { href: "/work/stores", label: "门店" },
  { href: "/work/campaigns", label: "推广活动" },
  { href: "/work/materials", label: "内容中心" },
  { href: "/work/analytics", label: "经营结果" },
];

const MORE_LINKS: readonly WorkNavItem[] = [
  { href: "/agency", label: "代理工作台", roles: ["agent"] },
  { href: "/work/leads", label: "线索" },
  { href: "/work/rewards", label: "权益与团购" },
  { href: "/work/touchpoints", label: "跨店入口批量管理", roles: ["org_owner", "store_manager", "agent"] },
  { href: "/work/billing", label: "账单", roles: ["org_owner", "agent"] },
  { href: "/work/settings", label: "团队、权限与代理协作", roles: ["org_owner", "store_manager", "agent"] },
];

export function WorkFrame({
  children,
  tenantName,
  serviceDesk,
  role,
  tenantId = "",
  options,
  onSwitchTenant,
}: {
  children: ReactNode;
  tenantName?: string;
  serviceDesk?: boolean;
  role?: string;
  tenantId?: string;
  options?: MembershipScope[];
  onSwitchTenant?: (tenantId: string) => void;
}) {
  const path = usePathname();
  // HUI-2222：「确认与核销」是登记制入口（FEATURE_SERVICE_DESK + 核销角色），
  // 关不渲染；on 时 /work/service 页面本体由 HUI-3562 交付。
  const links = serviceDesk ? [...LINKS, { href: SERVICE_DESK_HREF, label: SERVICE_DESK_LABEL }] : LINKS;
  const moreLinks = MORE_LINKS.filter((item) => isNavAllowedForRole(item.roles, role));
  const roleLabel = roleDisplayLabel(role);
  const tenantChoices = switchableTenants(options, tenantId);

  return (
    <div className="tk-page">
      <div className="tk-row tk-gap" data-section="work-context-bar">
        <p className="tk-kicker">
          {tenantName || "当前组织"}
          {roleLabel ? ` · ${roleLabel}` : ""}
        </p>
        {tenantChoices.length > 1 && onSwitchTenant ? (
          <label className="tk-note" data-field="tenant-switcher">
            切换组织
            <select
              className="tk-select"
              aria-label="切换当前组织"
              value={tenantId}
              onChange={(event) => {
                const next = event.target.value;
                if (next && next !== tenantId) onSwitchTenant(next);
              }}
            >
              {tenantChoices.map((scope) => (
                <option key={scope.tenant_id} value={scope.tenant_id}>
                  {scope.display_name}
                </option>
              ))}
            </select>
          </label>
        ) : null}
      </div>
      <nav aria-label="商家主导航">
        <ul className="tk-nav">
          {links.map((item) => {
            const current = item.href === "/" ? path === "/" : path === item.href || path.startsWith(item.href + "/");
            return (
              <li key={item.href}>
                <a href={item.href} aria-current={current ? "page" : undefined}>
                  {item.label}
                </a>
              </li>
            );
          })}
        </ul>
      </nav>
      {moreLinks.length > 0 ? (
        <details className="tk-section" data-section="work-more-nav">
          <summary>管理与更多</summary>
          <ul className="tk-nav">
            {moreLinks.map((item) => {
              const current = path === item.href || path.startsWith(item.href + "/");
              return (
                <li key={item.href}>
                  <a href={item.href} aria-current={current ? "page" : undefined}>
                    {item.label}
                  </a>
                </li>
              );
            })}
          </ul>
        </details>
      ) : null}
      {children}
    </div>
  );
}
