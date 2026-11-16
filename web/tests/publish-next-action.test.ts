import {describe,it,expect} from "vitest";
import {publishNextAction} from "../src/lib/publish-next-action";
describe("当前发布主动作",()=>{
 it("未知投稿先查询，不能重复授权提交",()=>{expect(publishNextAction({kind:"oauth_publish",status:"unknown",phase:"confirm_submit",hasHandoff:true})).toBe("query");});
 it("当前禁止再执行不阻断已有未知或在途投稿的原记录查询",()=>{for(const status of ["unknown","submitting","awaiting_review"])expect(publishNextAction({kind:"refused",status,phase:"",hasHandoff:true})).toBe("query");});
 it("授权回跳参数不能代替服务端账号事实",()=>{expect(publishNextAction({kind:"oauth_publish",status:"handoff_ready",phase:"authorize",hasHandoff:true})).toBe("authorize");expect(publishNextAction({kind:"oauth_publish",status:"handoff_ready",phase:"confirm_submit",hasHandoff:true})).toBe("submit");});
 it("受限路线不产生手动后门",()=>{expect(publishNextAction({kind:"refused",status:"",phase:"",hasHandoff:false})).toBe("refused");});
 it("终态不能再投稿",()=>{for(const status of ["canceled","failed","platform_confirmed"])expect(publishNextAction({kind:"oauth_publish",status,phase:"confirm_submit",hasHandoff:true})).toBe("finished");});
});
