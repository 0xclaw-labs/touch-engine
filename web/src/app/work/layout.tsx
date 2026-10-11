"use client";

import type { ReactNode } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { MerchantGate, useSession } from "@/components/work/merchant-session";
import { WorkFrame } from "@/components/work/work-frame";

export default function WorkLayout({ children }: { children: ReactNode }) {
  return (
    <div data-pn-surface="work.light" data-pn-theme="light">
      <MerchantGate>
        <WorkShell>{children}</WorkShell>
      </MerchantGate>
    </div>
  );
}

function WorkShell({ children }: { children: ReactNode }) {
  const session = useSession();
  return (
    <AdminShell
      nickname={session.email || "商家"}
      sessionRole={session.role}
      onLogout={() => void session.logout()}
      onTenantChange={session.switchTenant}
    >
      <WorkFrame tenantName={session.tenantName}>{children}</WorkFrame>
    </AdminShell>
  );
}
