"use client";
import { StoreMotionNote } from "@/components/work/store-motion-note";
import { MotionHandoffBrief } from "@/components/work/motion-handoff-brief";
import { ProductionPanel } from "@/components/work/production-panel";

// Historical anchors remain explicitly reachable for existing handoff records.
// New content work is performed through ContentEditor on the activity promise.
export function CampaignLegacyHistory({campaignId,endsAt}:{campaignId:string;endsAt?:string}) {
 return <section className="tk-section" aria-label="历史制作交接兼容记录">
 <h2 className="tk-section-title">历史制作交接记录</h2>
 <p className="tk-note">供核对早期工程交接记录。新增内容请回到活动承诺中的统一内容编辑器；这里不代表当前采用版本或已经发布。</p>
 <section id="store-motion"><StoreMotionNote campaignId={campaignId}/></section>
 <MotionHandoffBrief campaignId={campaignId}/>
 <ProductionPanel campaignId={campaignId} campaignEndsAt={endsAt??null}/>
 </section>;
}
