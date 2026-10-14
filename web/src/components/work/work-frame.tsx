"use client";

import type { ReactNode } from "react";
import { usePathname } from "next/navigation";
import { SERVICE_DESK_HREF, SERVICE_DESK_LABEL } from "@/lib/work/service-desk";

const LINKS = [
  { href: "/", label: "今天" },
  { href: "/work/stores", label: "门店" },
  { href: "/work/campaigns", label: "活动" },
  { href: "/work/materials", label: "素材" },
  { href: "/work/rewards", label: "奖励" },
  { href: "/work/analytics", label: "统计" },
];

export function WorkFrame({
  children,
  tenantName,
  serviceDesk,
}: {
  children: ReactNode;
  tenantName?: string;
  serviceDesk?: boolean;
}) {
  const path = usePathname();
  // HUI-2222：「确认与核销」是登记制入口（FEATURE_SERVICE_DESK + 核销角色），
  // 关不渲染；on 时 /work/service 页面本体由 HUI-3562 交付。
  const links = serviceDesk ? [...LINKS, { href: SERVICE_DESK_HREF, label: SERVICE_DESK_LABEL }] : LINKS;
  return (
    <div className="tk-page">
      <p className="tk-kicker">{tenantName || "当前组织"}</p>
      <ul className="tk-nav">
        {links.map((item) => {
          const current = item.href === "/" ? path === "/" : path === item.href || path.startsWith(item.href + "/");
          return (
            <li key={item.href}>
              <a href={item.href} aria-current={current ? "page" : undefined}>{item.label}</a>
            </li>
          );
        })}
      </ul>
      {children}
    </div>
  );
}
