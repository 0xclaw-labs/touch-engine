import { it,expect } from "vitest";
import { moneyInputMinor,formatMoney,inventoryPreparationBody } from "@/lib/reward-ops";
import { toRFC3339,fromRFC3339 } from "@/lib/campaign-config";
it("门店日期固定用亚洲上海时区，不随操作员设备漂移",()=>{
 expect(toRFC3339("2026-11-14T09:30")).toBe("2026-11-14T01:30:00.000Z");
 expect(fromRFC3339("2026-11-14T01:30:00Z")).toBe("2026-11-14T09:30");
});
it("元输入精确转换分，未知成本保持省略，真零保留",()=>{
 expect(moneyInputMinor("19.99")).toBe(1999); expect(moneyInputMinor("0")).toBe(0);
 expect(moneyInputMinor("")).toBeUndefined(); expect(moneyInputMinor("1.999")).toBeNull();
 expect(formatMoney(1999,"FEN")).toBe("19.99 元");
 const draft={requestKey:"same",name:"券",quantity:"2",codes:"",face:"5",cost:""};
 expect(inventoryPreparationBody(draft)).toEqual({request_key:"same",name:"券",quantity:2,codes:[],face_value_minor:500});
 expect(inventoryPreparationBody({...draft,cost:"0"})).toHaveProperty("cost_minor",0);
 expect(inventoryPreparationBody({...draft,cost:"-1"})).toBeNull();
});
