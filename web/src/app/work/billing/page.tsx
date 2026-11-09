"use client";

// HUI-4591 M12 订购与账单页。薄壳:登录门与工作台壳由 /work layout
// (MerchantGate + WorkFrame)提供,页面本体在 components/work/billing。

import { BillingDesk } from "@/components/work/billing/billing-desk";

export default function BillingPage() {
  return <BillingDesk />;
}
