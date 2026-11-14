import { describe,it,expect } from "vitest";
import { todayObligations } from "@/lib/work/today-obligations";
describe("今天只列受权办理事项",()=>{
  it("正常停用、已发未核销库存及空内容引用不冒充待办",()=>expect(todayObligations({pending_confirm:0,todos:{unavailable_actions:4,missing_content:7,pending_redeem:10}},{serviceDesk:true,owner:true})).toEqual([]));
  it("真实待确认有影响、状态、主操作和返回位置",()=>expect(todayObligations({pending_confirm:2},{serviceDesk:true,owner:false})[0]).toMatchObject({id:"confirm",href:"/work/service?return=/",status:expect.any(String),impact:expect.any(String),action:expect.any(String)}));
  it("未知摘要不变成零，未授权用户没有可点击确认任务",()=>{expect(todayObligations(null,{serviceDesk:true,owner:true})).toEqual([]);expect(todayObligations({pending_confirm:3},{serviceDesk:false,owner:false})).toEqual([]);});
});
