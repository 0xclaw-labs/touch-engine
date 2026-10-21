"use client";

import { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { useLaunchTargets } from "@/components/eco-nav/use-launch-resolver";
import { useAppSwitch } from "@/components/eco-nav/use-app-switch";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { commitTenantSwitch } from "@/lib/eco-nav/touch-shell";

export function AdminShell({
  children,
  nickname,
  sessionRole,
  tenantId = "",
  onLogout,
  onTenantChange,
}: {
  children: ReactNode;
  nickname: string;
  sessionRole: string;
  tenantId?: string;
  onLogout: () => void;
  onTenantChange: (tenantId: string) => void;
}) {
  const [model, setModel] = useState<EcoNavModel | null>(null);
  const resolveLaunchTarget = useLaunchTargets();
  const { switchApp } = useAppSwitch(tenantId);

  useEffect(() => {
    let alive = true;
    fetch("/api/eco-nav")
      .then(async (res) => {
        if (!res.ok) throw new Error("eco-nav");
        return (await res.json()) as EcoNavModel;
      })
      .then((next) => {
        if (alive) setModel(next);
      })
      .catch(() => {
        // HUI-2628: a failed nav authority must not invent a brand/tenant.
        if (alive) setModel(null);
      });
    return () => {
      alive = false;
    };
  }, []);

  return (
    <>
      {model ? (
        <EcoTopNav
          model={model}
          nickname={nickname}
          sessionRole={sessionRole}
          resolveLaunchTarget={resolveLaunchTarget}
          onSwitchApp={switchApp}
          onLogout={onLogout}
          onTenantChange={(tenantId) => {
            const committed = commitTenantSwitch(model?.provenance ?? "", tenantId);
            if (committed) onTenantChange(committed);
          }}
        />
      ) : null}
      {children}
    </>
  );
}
