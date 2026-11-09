"use client";

// HUI-4593 A1 代理工作台入口。薄壳:会话门与商户列表本体在
// components/work/agency-console。代理在这里看授权商户、受控进入并跳 M1。

import { AgencyConsole } from "@/components/work/agency-console/agency-console";

export default function AgencyPage() {
  return <AgencyConsole />;
}
