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
  // HUI-4591 M12:订购与账单(套餐/门店额度/续费扩店/支付状态/AI 用量/发票退款)。
  { href: "/work/billing", label: "账单" },
  { href: "/work/analytics", label: "统计" },
  // HUI-4594 M11:授权线索页(接收/待同步/撤回;待同步不计入已获客)。
  // 页面本体按 FEATURE_LEADS_CAPTURE 诚实降级,入口恒在。
  { href: "/work/leads", label: "线索" },
  // HUI-4593 M13:成员与权限(授权/撤回/代理协作/模板下发)。页面本体按
  // FEATURE_AUTHZ 与角色诚实降级,入口恒在。
  { href: "/work/settings", label: "设置" },
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
