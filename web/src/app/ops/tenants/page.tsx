"use client";

// HUI-4592 O1 商户与开通页。薄壳:门与导航由 /ops layout 提供,页面本体在
// components/ops/tenants-desk。

import { TenantsDesk } from "@/components/ops/tenants-desk";

export default function OpsTenantsPage() {
  return <TenantsDesk />;
}
