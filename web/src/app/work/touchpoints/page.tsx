"use client";

// HUI-1665 触点管理台薄壳:登录门与工作台壳由 /work layout
// (MerchantGate + WorkFrame)提供,页面本体在 components/work/touchpoints。

import { TouchpointsPanel } from "@/components/work/touchpoints/touchpoints-panel";

export default function TouchpointsPage() {
  return <TouchpointsPanel />;
}
