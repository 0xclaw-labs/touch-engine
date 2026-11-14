import { describe,it,expect } from "vitest";
import { nextOrderStep, blocksNewSubscription } from "../src/lib/billing";
describe("原单接续与付款/交付分离",()=>{
 const order={kind:"subscription",status:"opening",payment_ref:"paid-original"} as Parameters<typeof nextOrderStep>[0];
 it("已支付待开通要求回查原单，阻止新买",()=>{
  expect(nextOrderStep(order)).toContain("已确认支付");
  expect(nextOrderStep(order)).toContain("回查原订单");
  expect(blocksNewSubscription(order)).toBe(true);
 });
 it("待支付和失败不同，硬件付款不能当作交付",()=>{
  expect(nextOrderStep({...order,status:"processing",payment_ref:""})).toContain("等待支付结果");
  expect(blocksNewSubscription({...order,status:"failed",payment_ref:""})).toBe(false);
  expect(blocksNewSubscription({...order,status:"failed"})).toBe(true);
  expect(nextOrderStep({...order,kind:"hardware",status:"active"})).toContain("实卡验收");
 });
});
