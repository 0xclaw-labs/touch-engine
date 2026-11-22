"use client";

// shadcn-admin 结构母版的分组侧栏：品牌/组织切换 Command + 分组导航 + 待办 Badge。
// 只做壳层导航，不改变权限判断与业务规则；链接均指向既有路由。

import type { ComponentType, ReactNode } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  BarChart3,
  Gift,
  Headset,
  Image as ImageIcon,
  Layers,
  LayoutDashboard,
  Megaphone,
  Store,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { MembershipScope } from "@/lib/workspace-tenant";
import { OrgSwitchCommand } from "./OrgSwitchCommand";

type NavIcon = ComponentType<{ className?: string }>;

interface NavItem {
  title: string;
  href: string;
  icon: NavIcon;
  badge?: number;
}

interface NavGroup {
  label: string;
  items: NavItem[];
}

function NavLink({
  item,
  active,
}: {
  item: NavItem;
  active: boolean;
}) {
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors hover:bg-accent hover:text-accent-foreground",
        active ? "bg-accent font-medium" : "text-foreground/80",
      )}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="flex-1 truncate">{item.title}</span>
      {typeof item.badge === "number" && item.badge > 0 ? (
        <Badge variant="secondary" className="px-1.5">
          {item.badge}
        </Badge>
      ) : null}
    </Link>
  );
}

export function AdminSidebar({
  scopes,
  currentTenantId,
  tenantName,
  todoCount,
  onTenantChange,
  children,
}: {
  scopes: MembershipScope[];
  currentTenantId: string;
  tenantName?: string;
  todoCount: number;
  onTenantChange: (tenantId: string) => void;
  children: ReactNode;
}) {
  const path = usePathname();

  const groups: NavGroup[] = [
    {
      label: "概览",
      items: [
        {
          title: "经营工作台",
          href: "/admin",
          icon: LayoutDashboard,
          badge: todoCount,
        },
      ],
    },
    {
      label: "经营",
      items: [
        { title: "门店", href: "/work/stores", icon: Store },
        { title: "活动", href: "/work/campaigns", icon: Megaphone },
        { title: "碰触点", href: "/work/touchpoints", icon: Layers },
        { title: "素材", href: "/work/materials", icon: ImageIcon },
        { title: "奖励与核销", href: "/work/rewards", icon: Gift },
      ],
    },
    {
      label: "分析与服务",
      items: [
        { title: "经营分析", href: "/work/analytics", icon: BarChart3 },
        { title: "服务与开通", href: "/work/service", icon: Headset },
      ],
    },
  ];

  const isActive = (href: string) =>
    href === "/admin" ? path === "/admin" : path === href || path.startsWith(href + "/");

  const allItems = groups.flatMap((group) => group.items);

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-64 shrink-0 flex-col gap-4 border-r p-4 lg:flex">
        <div className="flex flex-col gap-2">
          <p className="text-sm font-semibold">碰一碰 · 商家后台</p>
          <OrgSwitchCommand
            scopes={scopes}
            currentTenantId={currentTenantId}
            onSelect={onTenantChange}
          />
        </div>
        <nav className="flex flex-col gap-4">
          {groups.map((group) => (
            <div key={group.label} className="flex flex-col gap-1">
              <p className="px-3 text-xs font-medium text-muted-foreground">
                {group.label}
              </p>
              {group.items.map((item) => (
                <NavLink
                  key={item.href}
                  item={item}
                  active={isActive(item.href)}
                />
              ))}
            </div>
          ))}
        </nav>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-col gap-2 border-b p-3 lg:hidden">
          <OrgSwitchCommand
            scopes={scopes}
            currentTenantId={currentTenantId}
            onSelect={onTenantChange}
          />
          <nav className="flex gap-2 overflow-x-auto">
            {allItems.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                aria-current={isActive(item.href) ? "page" : undefined}
                className={cn(
                  "flex shrink-0 items-center gap-1 rounded-md px-2.5 py-1.5 text-xs",
                  isActive(item.href)
                    ? "bg-accent font-medium"
                    : "text-foreground/80",
                )}
              >
                {item.title}
                {typeof item.badge === "number" && item.badge > 0 ? (
                  <Badge variant="secondary" className="px-1.5">
                    {item.badge}
                  </Badge>
                ) : null}
              </Link>
            ))}
          </nav>
        </div>
        <main className="min-w-0 flex-1">{children}</main>
      </div>
    </div>
  );
}
