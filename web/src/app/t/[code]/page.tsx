import { notFound } from "next/navigation";

import { featureEnabled } from "@/lib/touchpoints";

import { TouchEntry } from "./touch-entry";

// HUI-1665 稳定入口 /t/{code}:FEATURE_TOUCHPOINT_ENTRY 默认 off。
// off 时整页 404,与 Go 端「路由不注册 + handler gate」并列的第三道闸。
// force-dynamic:开关按请求时的环境变量裁决,不在构建期定死。
export const dynamic = "force-dynamic";

export default function TouchEntryPage() {
  if (!featureEnabled(process.env.FEATURE_TOUCHPOINT_ENTRY)) {
    notFound();
  }
  return <TouchEntry />;
}
