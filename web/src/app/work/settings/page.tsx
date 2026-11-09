"use client";

// HUI-4593 M13 成员与权限页。薄壳:登录门与工作台壳由 /work layout
// (MerchantGate + WorkFrame)提供,页面本体在 components/work/settings。

import { SettingsDesk } from "@/components/work/settings/settings-desk";

export default function SettingsPage() {
  return <SettingsDesk />;
}
