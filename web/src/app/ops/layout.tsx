"use client";

// HUI-4592 平台运营控制台布局:权限门(OpsGate)+ 六视图导航(共享壳)。
// /ops 是平台运营面,不是 /admin 商家后台换标签:门 = 受信 ops grant。

import { OpsGate, OpsNav } from "@/components/ops/ops-shell";

export default function OpsLayout({ children }: { children: React.ReactNode }) {
  return (
    <OpsGate>
      <main className="tk-page" data-surface="ops-console">
        <h1 className="tk-title">平台运营控制台</h1>
        <OpsNav />
        {children}
      </main>
    </OpsGate>
  );
}
