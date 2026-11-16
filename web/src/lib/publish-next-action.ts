/** Only server plan and original persisted state choose the main action. */
export function publishNextAction(input:{kind:string;status:string;phase:string;hasHandoff:boolean}):"confirm_content"|"authorize"|"submit"|"handoff"|"query"|"refused"|"finished" {
 if(["platform_confirmed","canceled","failed"].includes(input.status))return "finished";
 if(input.hasHandoff&&["submitting","awaiting_review","unknown"].includes(input.status))return "query";
 if(input.kind==="refused"||input.kind==="pending_external"||input.kind==="unknown")return "refused";
 if(!input.hasHandoff)return "confirm_content";
 if(input.kind==="oauth_publish")return input.phase==="confirm_submit"?"submit":"authorize";
 return "handoff";
}
