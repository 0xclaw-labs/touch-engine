import type { Metadata } from "next";
import type { ReactNode } from "react";

export const metadata: Metadata = {
  title: "门店服务",
  description: "看这家门店可以用的服务和活动",
};

// HUI-1896 公共门店面:与 /c 同一公共视觉合同(2628),无生态导航。
export default function StorefrontLayout({ children }: { children: ReactNode }) {
  return <div data-pn-surface="public.consumer" data-pn-theme="light">{children}</div>;
}
