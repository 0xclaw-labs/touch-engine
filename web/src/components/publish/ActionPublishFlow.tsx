"use client";

import { useCallback, useEffect, useState } from "react";
import { parseClientAction } from "@/lib/publish-adapters/client-action";
import { executePlatformHandoff, handoffFailureMessage, preparePlatformHandoff, savePreparedFile, sharePreparedFiles, sharePublicLink } from "@/lib/publish-adapters/execution";
import { statusFace, planCopy, evidenceFace } from "@/lib/store-handoff";
import { publishNextAction } from "@/lib/publish-next-action";

type Payload = { handoff?: { handoff_id: string; status: string; client_action?: unknown; share_url?: string }; plan?: {kind: string; allowed: boolean; client_action?: unknown; reason_text?: string; unsupported_fields?:string[]; evidence_level?:string}; confirmation_required?: string };
export function ActionPublishFlow({actionId,sessionId,actionRevision,versionId,method,environment,storeCode,payloadKind="media",legacy}: {actionId:string;sessionId:string;actionRevision:number;versionId:string;method:string;environment:string;storeCode:string;payloadKind?:"media"|"link_share";legacy?:{code:string;participationId:string;platform:string}}) {
 const [data,setData]=useState<Payload|null>(null);
 const [confirmed,setConfirmed]=useState(false);
 const [busy,setBusy]=useState(false);
 const [notice,setNotice]=useState("");
 const [files,setFiles]=useState<File[]>([]);
 const [prepared,setPrepared]=useState(false);
 const [accountConfirmed,setAccountConfirmed]=useState(false);
 const [nativeLinkShare,setNativeLinkShare]=useState(false);
 const isLink=payloadKind==="link_share";
 useEffect(()=>{setNativeLinkShare(typeof navigator.share==="function");},[]);
 const code=legacy?.code??"",participationId=legacy?.participationId??"",platform=legacy?.platform??"";
 const base=code?`/api/public/links/${encodeURIComponent(code)}/publish-handoff`:`/api/public/actions/${encodeURIComponent(actionId)}/handoffs`;
 const accept=useCallback((next:Payload)=>{setData(next);},[]);
 const unavailable=useCallback((message:string)=>{
  setNotice(message);
  // Keep the original record for recovery, but never retain executable exports
  // after a failed refresh or a current admission refusal.
  setData(current=>({...current,plan:{kind:"unknown",allowed:false,reason_text:message}}));
 },[]);
 const load=useCallback(async()=>{
  if(!sessionId&&!code)return;
  try {
   if(code){
    const q=new URLSearchParams({platform,with_snapshot:"1",adapter_mode:"1",environment,participation_id:participationId});
    const response=await fetch(`${base}/routes?${q}`);const result=await response.json().catch(()=>null);
    if(response.ok&&result?.plan){accept(result);setNotice("");}else unavailable("当前发布方式尚未放行或原交接暂不可读取，请刷新原进度或请门店协助。");
    return;
   }
   const existing=await fetch(`/api/public/action-sessions/${encodeURIComponent(sessionId)}/handoff`);
   if(existing.ok){accept(await existing.json());setNotice("");return;}
   if(existing.status!==404){unavailable("原交接暂时读不到，请刷新原进度；请勿重复提交。");return;}
   if(!method){unavailable("当前路线未放行，不能开始新的交接。");return;}
   const query=new URLSearchParams({action_session_id:sessionId,method,environment,payload_kind:payloadKind});
   const response=await fetch(`${base}/plan?${query}`);
   if(response.ok){accept(await response.json());setNotice("");}else unavailable("发布路线或素材暂不可用。请稍后刷新，或请门店协助。");
  }catch{unavailable("网络没有给出明确结果。请回到原进度刷新。");}
 },[accept,base,code,environment,method,participationId,payloadKind,platform,sessionId,unavailable]);
 useEffect(()=>{setData(null);setConfirmed(false);setAccountConfirmed(false);void load();},[load,versionId]);
 const raw=data?.handoff?.client_action??data?.plan?.client_action;
 const action=parseClientAction(raw);
 const linkUrl=isLink&&action?.type==="open_url"?action.url:"";
 const signature=JSON.stringify(raw);
 useEffect(()=>{
  let alive=true;setPrepared(false);setFiles([]);setAccountConfirmed(false);
  if(!data?.handoff || !raw)return;
  preparePlatformHandoff(raw).then(result=>{if(alive){setFiles(result.files);setPrepared(true);}}).catch(error=>{if(alive)setNotice(handoffFailureMessage(error));});
  return()=>{alive=false;};
 // The exact action/expiry/account defines the user's next confirmation.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[signature,data?.handoff?.handoff_id]);
 const start=async()=>{
  if((!isLink&&!confirmed)||!data?.confirmation_required)return;
  setBusy(true);setNotice("");
  try {
   const response=await fetch(base,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(code?{participation_id:participationId,platform,adapter_mode:true,environment,confirmed_content_version:data.confirmation_required}:{action_session_id:sessionId,action_revision:actionRevision,payload_kind:payloadKind,method,environment,confirmation:data.confirmation_required,idempotency_key:`handoff-${sessionId}-${versionId}-${method}`})});
   const result=await response.json().catch(()=>null);
   if(response.ok&&result?.handoff){accept(result);return;}
   setNotice(result?.error==="confirmation_mismatch"?"内容刚发生变化，请刷新预览并重新确认。":"交接没有继续。当前路线或素材不可用，请刷新原进度。");
  }catch{setNotice("网络结果不明。请刷新原进度核对，重试会沿用本次记录。");}
  finally{setBusy(false);}
 };
 const mutate=async(operation:"submit"|"query"|"cancel")=>{
  if(!data?.handoff)return;
  setBusy(true);setNotice("");
  try{
   const response=await fetch(`/api/public/handoffs/${encodeURIComponent(data.handoff.handoff_id)}/${operation}`,{method:"POST",headers:{"Content-Type":"application/json"},body:"{}"});
   const result=await response.json().catch(()=>null);
   if(response.ok&&result?.handoff){setData(current=>({...current,handoff:result.handoff}));}
   else{setNotice(operation==="submit"?"投稿结果尚未确认，请先查询原投稿；授权或素材失效时请刷新原进度。":"这一步没有完成，请保留原进度后重试。");await load();}
  }catch{setNotice("网络结果不明，请刷新原进度并先查询原投稿。");await load();}
  finally{setBusy(false);}
 };
 const recordFact=(operation:string)=>{
  if(!data?.handoff)return;
  void fetch(`/api/public/handoffs/${encodeURIComponent(data.handoff.handoff_id)}/facts`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({kind:"media_handoff",source:"customer-browser",event_id:crypto.randomUUID(),payload:{operation,evidence:"handoff_only"}})}).catch(()=>undefined);
 };
 const execute=()=>{
  // Invoke native sharing synchronously inside this click, using preloaded Files.
  const promise=payloadKind==="link_share"&&action?.url?sharePublicLink(action.url):action?.type==="manual_export"?(data?.handoff?.share_url?sharePublicLink(data.handoff.share_url):sharePreparedFiles(files)):executePlatformHandoff(raw,{onFailure:setNotice});
  promise.then(fact=>{recordFact(fact.operation);setNotice(isLink?"链接已交给系统分享；是否发送请在目标应用确认。本页不会把交接记为发布成功。":"已将操作交给平台或系统；请在目标应用确认。返回本页可继续原进度，发布结果仍以平台为准。");}).catch(error=>setNotice(handoffFailureMessage(error,payloadKind)));
 };
 const next=publishNextAction({kind:data?.plan?.kind??"unknown",status:data?.handoff?.status??"",phase:action?.params.phase??"",hasHandoff:!!data?.handoff});
 const canExport=!!data?.handoff&&data.plan?.allowed===true&&["handoff_ready","editor_opened"].includes(data.handoff.status);
 return <section className="tk-section" data-testid="action-publish-flow">
  <h2 className="tk-section-title">{isLink?"分享链接":"发布交接"}</h2>
  {!data&&<p className="tk-note">{isLink?"正在核对分享链接与可用方式…":"正在核对本次内容与可用路线…"}</p>}
  {data?.plan&&!isLink&&<p className="tk-note">{planCopy(data.plan.kind as Parameters<typeof planCopy>[0])}</p>}
  {linkUrl&&<p className="tk-note">将分享的链接：<code style={{overflowWrap:"anywhere"}}>{linkUrl}</code></p>}
  {data?.handoff&&<p className="tk-note" data-testid="handoff-status">{statusFace(data.handoff.status)}</p>}
  {next==="confirm_content"&&(isLink?<button type="button" className="tk-primary" disabled={busy||!linkUrl||!data?.confirmation_required} onClick={start}>确认链接，准备分享</button>:<><label className="tk-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>我确认以上内容，并选择用自己的账号继续这次交接</label><button className="tk-primary" disabled={busy||!confirmed||!data?.confirmation_required} onClick={start}>确认内容，准备交接</button></>)}
  {next==="authorize"&&action?.url&&<a className="tk-primary" href={action.url}>前往平台授权</a>}
  {next==="submit"&&<><p className="tk-note">已授权账号：{action?.params.account_ref}。请核对账号及上方内容后确认投稿。</p><label className="tk-check"><input type="checkbox" checked={accountConfirmed} onChange={e=>setAccountConfirmed(e.target.checked)}/>我确认用这个平台账号发布本次内容</label><button className="tk-primary" disabled={busy||!accountConfirmed||!action?.params.account_ref} onClick={()=>void mutate("submit")}>确认账号与内容，提交平台</button></>}
  {next==="handoff"&&(isLink?<>
    {nativeLinkShare&&<button type="button" className="tk-primary" disabled={busy||!prepared||!linkUrl} onClick={execute}>分享此链接</button>}
    {!nativeLinkShare&&<p className="tk-note">当前浏览器不支持系统分享。可以复制下方链接，再到目标应用中粘贴发送。</p>}
    {linkUrl&&<button type="button" className="tk-quiet" disabled={busy||!prepared} onClick={()=>{
      if(!navigator.clipboard?.writeText){setNotice("无法自动复制，请长按上方链接手动复制。");return;}
      void navigator.clipboard.writeText(linkUrl).then(()=>setNotice("链接已复制，请在目标应用中粘贴并确认发送。复制不会记录为分享成功。"),()=>setNotice("无法自动复制，请长按上方链接手动复制。"));
    }}>复制链接</button>}
  </>:<button type="button" className="tk-primary" disabled={busy||!prepared} onClick={execute}>{prepared? action?.type==="manual_export"?"选择分享目标":"打开平台继续":"正在准备素材…"}</button>)}
  {next==="query"&&<button type="button" className="tk-primary" disabled={busy} onClick={()=>void mutate("query")}>查询原投稿结果</button>}
  {data&&next==="refused"&&<p className="tk-warn">{data?.plan?.reason_text||"当前路线不可用。请联系门店确认可用服务。"}</p>}
  {canExport&&files.length>0&&<details className="tk-gap"><summary>逐个保存素材</summary>{files.map((file,i)=><button key={i} type="button" className="tk-quiet" onClick={()=>{try{savePreparedFile(file);recordFact("download_requested");setNotice("已请求保存，请在手机中核对文件。保存不会确认发布或领取奖励。");}catch(e){setNotice(handoffFailureMessage(e));}}}>保存{file.type.startsWith("video/")?"视频":"图片"} {i+1}</button>)}</details>}
  {canExport&&action?.caption&&<details className="tk-gap"><summary>参考文案与操作说明</summary><p className="tk-note">{action.caption}</p><button className="tk-quiet" onClick={()=>{void navigator.clipboard.writeText(action.caption).then(()=>setNotice("参考文案已复制，请在平台核对实际内容。"),()=>setNotice("无法自动复制，请长按文案手动复制。"));}}>复制参考文案</button>{action.steps.map(step=><p className="tk-note" key={step}>{step}</p>)}</details>}
  {data?.plan?.evidence_level&&<p className="tk-note">{evidenceFace(data.plan.evidence_level)}</p>}
  {data?.plan?.unsupported_fields?.length ? <p className="tk-note">此方式不会自动带入：{data.plan.unsupported_fields.join("、")}。参考文案可单独复制，请在平台核对。</p> : null}
  <div className="tk-row tk-gap"><button type="button" className="tk-quiet" disabled={busy} onClick={()=>void load()}>刷新原进度</button>{data?.handoff&&!['platform_confirmed','canceled','failed'].includes(data.handoff.status)&&<button className="tk-quiet" disabled={busy} onClick={()=>void mutate("cancel")}>取消交接</button>}<a className="tk-quiet" href={code?"#my-activity-benefits":`/s/${encodeURIComponent(storeCode)}/benefits`}>我的活动与优惠</a></div>
  <p className="tk-note">{isLink?"系统分享和复制链接都不代表已发送、已发布或已加入社群。返回本页可继续原分享进度。":"交接、保存、返回和自报均不表示平台发布成功。权益发放与使用状态请查看“我的活动与优惠”。若平台没有自动返回，请回到这个浏览器页面继续原进度。"}</p>
  {notice&&<p className="tk-warn" role="status">{notice}</p>}
 </section>;
}
