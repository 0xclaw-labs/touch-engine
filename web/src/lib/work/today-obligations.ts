import type { TodayItem } from "@/lib/product-finish";
export interface OperationsSummary {
  as_of?: string;
  pending_confirm?: number;
  rewards_issued?: number;
  rewards_redeemed?: number;
  todos?: { billing_anomalies?: number; action_auth_expired?: number; admission_expired?: number; pending_redeem?: number; unavailable_actions?: number; missing_content?: number };
}
export function todayObligations(summary:OperationsSummary|null, access:{serviceDesk:boolean;owner:boolean}):TodayItem[] {
  if(!summary) return [];
  const items:TodayItem[]=[];
  if(access.serviceDesk && typeof summary.pending_confirm==="number" && summary.pending_confirm>0) items.push({id:"confirm",title:`${summary.pending_confirm} 件顾客发布等待确认`,status:"顾客已提交，尚未确认",impact:"复核证据后才能按约办理权益",action:"查看并确认顾客事项",href:"/work/service?return=/",tone:"now"});
  if(access.owner && typeof summary.todos?.billing_anomalies==="number" && summary.todos.billing_anomalies>0) items.push({id:"billing",title:`${summary.todos.billing_anomalies} 笔订单付款未完成`,status:"订单失败，尚未生效",impact:"核对订单后决定继续付款或联系支持",action:"处理账单订单",href:"/work/billing?return=/",tone:"now"});
  // issued库存、停用动作、空内容绑定和正常终态不是商家的待办义务。
  // 授权计数缺少对象/恢复动作的精确目标，不把聚合数伪造为可处理队列。
  return items;
}
