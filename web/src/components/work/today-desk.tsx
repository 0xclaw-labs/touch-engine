"use client";
import { useEffect, useState } from "react";
import { MerchantGate, useSession } from "@/components/work/merchant-session";
import { WorkFrame } from "@/components/work/work-frame";
import { serviceDeskEntryVisible, SERVICE_DESK_HREF, SERVICE_DESK_LABEL } from "@/lib/work/service-desk";
import { todayObligations, type OperationsSummary } from "@/lib/work/today-obligations";
import { surfaceLabel, type TodayItem } from "@/lib/product-finish";
import { Card, CardContent } from "@/components/ui/card";

export function TodayDesk() { return <MerchantGate><TodayBody /></MerchantGate>; }
function TodayBody() {
  const session = useSession();
  const desk = serviceDeskEntryVisible(session.features, session.role);
  const [phase,setPhase] = useState<"loading"|"error"|"ready">("loading");
  const [summary,setSummary] = useState<OperationsSummary|null>(null);
  const [storeCount,setStoreCount] = useState<number|null>(null);
  const [attempt,setAttempt] = useState(0);
  useEffect(()=>{
    let alive=true; setPhase("loading"); setSummary(null); setStoreCount(null);
    void Promise.allSettled([session.api("GET","dashboard/operations-summary"),session.api("GET","stores")]).then(([ops,stores])=>{
      if(!alive) return;
      if(stores.status==="fulfilled" && stores.value.ok && Array.isArray(stores.value.data.items)) setStoreCount(stores.value.data.items.length);
      if(ops.status==="fulfilled" && ops.value.ok) {setSummary(ops.value.data as OperationsSummary);setPhase("ready");}
      else setPhase("error");
    });
    return ()=>{alive=false;};
  },[session,attempt]);
  const items=todayObligations(summary,{serviceDesk:desk,owner:session.role==="org_owner"});
  return <WorkFrame tenantName={session.tenantName || "当前组织"} serviceDesk={desk}>
    <main><h1 className="tk-title">今天</h1><p className="tk-lead">处理已经发生、需要你继续办理的顾客事项。配置本店服务可以直接进入门店工作区。</p>
      {storeCount===0 ? <p className="tk-state">还没有门店。<a href="/work/stores">登记第一家门店</a></p> : null}
      {phase==="loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase==="error" ? <p className="tk-warn" data-state="error" role="alert">经营摘要暂时没有读到，待处理数量和今日结果未知。<button className="tk-quiet" data-action="retry" onClick={()=>setAttempt(x=>x+1)}>重试摘要</button> 仍可<a href="/work/stores">进入门店工作区</a>{desk ? <>或<a href={SERVICE_DESK_HREF}>{SERVICE_DESK_LABEL}</a></> : null}。</p> : null}
      {phase==="ready" ? <>
        <h2 className="tk-section-title">需要办理</h2>
        {typeof summary?.pending_confirm!=="number" || typeof summary?.rewards_issued!=="number" || typeof summary?.rewards_redeemed!=="number" ? <p className="tk-warn">摘要只读到部分数据，缺失项保持未知。可前往服务台或经营结果核对。</p> : null}
        {items.length===0 ? <p className="tk-state" data-state="empty">当前没有已知的待处理事项。正常暂停、结束的活动和未选择的服务不会列为异常。</p> : <div className="tk-stack">{items.map((item:TodayItem)=><Card key={item.id}><CardContent className="tk-gap"><strong>{item.title}</strong><p className="tk-note">{item.status} · {item.impact}</p><a className="tk-button" href={item.href}>{item.action}</a></CardContent></Card>)}</div>}
        <Card className="tk-section"><CardContent className="tk-gap"><h2 className="tk-section-title">今天的实际结果</h2><p className="tk-note">{typeof summary?.rewards_issued==="number" ? `已发权益 ${summary.rewards_issued} 份` : "发放结果未知"} · {typeof summary?.rewards_redeemed==="number" ? `已核销 ${summary.rewards_redeemed} 份` : "核销结果未知"}。按 UTC 当天统计。</p><a href="/work/analytics">查看经营结果与统计方式</a></CardContent></Card>
      </> : null}
    </main>
  </WorkFrame>;
}
