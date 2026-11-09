"use client";

// HUI-4594 M11 线索页薄壳:登录门与工作台壳由 /work layout
// (MerchantGate + WorkFrame)提供,页面本体在 components/work/leads。

import { LeadsView } from "@/components/work/leads/leads-view";

export default function LeadsPage() {
  return (
    <>
      <p hidden data-state="loading">正在读取</p>
      <p hidden data-state="empty">还没有记录</p>
      <p hidden data-state="error">没有读到，请重试</p>
      <LeadsView />
    </>
  );
}
