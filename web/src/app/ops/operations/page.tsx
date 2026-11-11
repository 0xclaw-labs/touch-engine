"use client";

// HUI-4592 O6 运行与交付页(T20 核心验收面)。薄壳:门与导航由 /ops layout
// 提供,聚合状态与修复入口在 components/ops/operations-desk。

import { OperationsDesk } from "@/components/ops/operations-desk";

export default function OpsOperationsPage() {
  return <OperationsDesk />;
}
