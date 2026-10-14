"use client";

// HUI-3562 店员确认与核销台入口。薄壳:登录门与工作台壳由 /work layout
// (MerchantGate + WorkFrame)提供,页面本体在 components/work/service-desk。

import { ServiceDesk } from "@/components/work/service-desk/service-desk";

export default function ServicePage() {
  return <ServiceDesk />;
}
