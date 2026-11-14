import { describe, expect, it } from "vitest";
import { workspaceHref, workspacePanel, catalogChoices } from "@/lib/work/store-workspace";

describe("门店工作区上下文", () => {
  it("同店旧入口准确定位，未知页签安全返回顾客页", () => {
    expect(workspaceHref("store/a", "channels")).toBe("/work/stores/store%2Fa?tab=page&panel=channels");
    expect(workspaceHref("s1", "entrances")).toBe("/work/stores/s1?tab=entrances");
    expect(workspacePanel("unknown")).toBe("page");
    expect(workspacePanel("services")).toBe("services");
  });
  it("完整目录可搜索，不把未选项变成欠项，允许同类多实例", () => {
    const catalog = [{code:"A08",label:"大众点评",platform:"dianping",kind:"review",variant:""}, {code:"A17",label:"门店 Wi-Fi",platform:"",kind:"wifi",variant:""}];
    expect(catalogChoices(catalog,"点评").map(x=>x.code)).toEqual(["A08"]);
    expect(catalogChoices(catalog,"a17").map(x=>x.code)).toEqual(["A17"]);
    expect(catalogChoices(catalog,"")).toHaveLength(2);
  });
});
