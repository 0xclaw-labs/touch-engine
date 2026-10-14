"use client";

// HUI-1677 经营看板入口。薄壳:登录门与工作台壳由 /work layout
// (MerchantGate + WorkFrame)提供,页面本体在 components/work/analytics。

import { AnalyticsView } from "@/components/work/analytics/analytics-view";

export default function AnalyticsPage() {
  return <AnalyticsView />;
}
