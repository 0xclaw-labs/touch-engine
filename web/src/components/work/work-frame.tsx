"use client";

import type { ReactNode } from "react";
import { usePathname } from "next/navigation";

const LINKS = [
  { href: "/", label: "今天" },
  { href: "/work/stores", label: "门店" },
  { href: "/work/campaigns", label: "活动" },
  { href: "/work/materials", label: "素材" },
  { href: "/work/rewards", label: "奖励" },
  { href: "/work/analytics", label: "统计" },
];

export function WorkFrame({ children, tenantName }: { children: ReactNode; tenantName?: string }) {
  const path = usePathname();
  return (
    <div className="tk-page">
      <p className="tk-kicker">{tenantName || "当前组织"}</p>
      <ul className="tk-nav">
        {LINKS.map((item) => {
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
