"use client";
import { useEffect, useState } from "react";
import type { MerchantSession } from "../merchant-session";
import { ACTION_LABELS, type AccessEffective } from "../settings/settings-desk";

// Kept at the existing merchant gate, so the delegation stays visible while
// moving between stores, content and bills without changing the shared nav.
export function AgencyWorkContext({ session }: { session: MerchantSession }) {
  const [access,setAccess] = useState<AccessEffective | null>(null);
  useEffect(() => {
    let live=true; setAccess(null);
    void session.api("GET","access/effective").then((res) => { if(live && res.ok) setAccess(res.data as unknown as AccessEffective); }).catch(() => {});
    return () => {live=false;};
  },[session]);
  return <aside className="tk-section" aria-label="当前代办商家">
    <p>正在为 <strong>{session.tenantName}</strong> 办事。</p>
    <p>可办任务：{access ? (access.relation?.allowed_actions ?? []).map((a) => ACTION_LABELS[a] ?? a).join("、") || "只读与开子账号" : "正在核对有效授权；每次办理由服务端重新确认"}。</p>
    <p>付款授权：{access?.relation?.payer_granted ? "已有独立代付授权，按公共付款权威裁决" : "未确认独立付款授权，收费请交由商家负责人办理"}。</p>
    <a className="tk-quiet" href="/agency" onClick={session.clearWorkspace}>返回代理商家列表</a>
  </aside>;
}
