"use client";
import {useEffect,useState} from "react";
import {ActionPublishFlow} from "./ActionPublishFlow";
import {detectEnvironment} from "@/lib/env-capability";
import {platformLabel} from "@/lib/customer-publish";
import {Select,SelectContent,SelectItem,SelectTrigger,SelectValue} from "@/components/ui/select";
export function CampaignPublishFlow({code,participationId,versionId}: {code:string;participationId:string;versionId:string}){
 const [platforms,setPlatforms]=useState<string[]>([]),[platform,setPlatform]=useState("");
 useEffect(()=>{let alive=true;Promise.all([fetch(`/api/public/links/${encodeURIComponent(code)}/content-choices`).then(r=>r.ok?r.json():null),fetch(`/api/participations/${encodeURIComponent(participationId)}`).then(r=>r.ok?r.json():null)]).then(([data,original])=>{if(!alive)return;const items=Array.isArray(data?.items)?data.items:[];const attempts=Array.isArray(original?.attempts)?original.attempts:[];const names=[...new Set([...attempts,...items].map((item:{platform?:string})=>item.platform).filter((name:unknown):name is string=>typeof name==="string"&&!!name))] as string[];setPlatforms(names);setPlatform(current=>names.includes(current)?current:names[0]??"");}).catch(()=>undefined);return()=>{alive=false;};},[code,participationId]);
 return <section data-testid="campaign-publish-flow">
 {platforms.length>1&&<label className="tk-label">本次发布到<Select value={platform} onValueChange={setPlatform}><SelectTrigger className="tk-select"><SelectValue/></SelectTrigger><SelectContent>{platforms.map(name=><SelectItem value={name} key={name}>{platformLabel(name)}</SelectItem>)}</SelectContent></Select></label>}
 {platform?<ActionPublishFlow key={platform} actionId="" sessionId="" actionRevision={0} versionId={versionId} method="" environment={detectEnvironment(typeof navigator==="undefined"?"":navigator.userAgent)} storeCode="" legacy={{code,participationId,platform}}/>:<p className="tk-note">当前没有可交接的内容或平台，请先向门店核对。已取得的权益仍可在原记录查看。</p>}
 </section>;
}
