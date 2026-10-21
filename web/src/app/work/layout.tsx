"use client";

import type { ReactNode } from "react";
import { AdminShell } from "@/components/admin/AdminShell";
import { MerchantGate, useSession } from "@/components/work/merchant-session";
import { WorkFrame } from "@/components/work/work-frame";
import { serviceDeskEntryVisible } from "@/lib/work/service-desk";

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
      tenantId={session.tenantId}
      onLogout={() => void session.logout()}
      onTenantChange={session.switchTenant}
    >
      {/* HUI-2222：确认与核销入口由开关+角色决定，判定收口在 lib/work/service-desk */}
      <WorkFrame
        tenantName={session.tenantName}
        serviceDesk={serviceDeskEntryVisible(session.features, session.role)}
      >
        {children}
      </WorkFrame>
    </AdminShell>
  );
}
