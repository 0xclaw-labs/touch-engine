import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 T3 屏1「活动与优惠」结构化面板:源码级断言(node 无 DOM)。
// 红线:面板只承载结构化权益/动作/核验/限额/领取条件与主动作,不复制
// 既有门店头与活动说明区(基线区块保持原样);主动作按实际写
// 「选择内容并参与」;未达标不写已领取;无生态导航/推销/搭子/商家账号;
// 浏览不建参与。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const component = codeOf(src("components/customer/ActivityIntro.tsx"));

describe("屏1 面板:不复制基线区块", () => {
  it("有 data-testid=activity-intro 分区", () => {
    expect(component.includes('data-testid="activity-intro"')).toBe(true);
  });

  it("不接收 merchantName/title/summary 等基线字段(门店头与活动说明只渲染一次)", () => {
    for (const dup of ["merchantName", "storeName", "summary", "startsAt", "endsAt", "storeNotice"]) {
      expect(component.includes(dup)).toBe(false);
    }
  });

  it("核验方式三态走 evidenceStepCopy 唯一出口", () => {
    expect(component.includes("evidenceStepCopy")).toBe(true);
  });

  it("限额走 stockLine,不编造数字", () => {
    expect(component.includes("stockLine")).toBe(true);
  });

  it("领取条件文案:店员确认后才发放", () => {
    expect(component.includes("店员确认")).toBe(true);
  });
});

describe("屏1 主动作:按实际写,未达标不冒充", () => {
  it("按钮文案是「选择内容并参与」,不是「立即领取」", () => {
    expect(component.includes("选择内容并参与")).toBe(true);
    expect(component.includes("立即领取")).toBe(false);
    expect(component.includes("一键领取")).toBe(false);
  });

  it("开始按钮只在 onParticipationStart 存在且 participation 门开时渲染", () => {
    expect(component.includes("onParticipationStart")).toBe(true);
    expect(component.includes("canStart")).toBe(true);
  });

  it("开始路径不直接调用 fetch 建参与(建参与只经 T2 门面)", () => {
    expect(component.includes("fetch(")).toBe(false);
    expect(component.includes("startParticipation")).toBe(false);
  });

  it("预留文案只经 benefitCopy 出口(pending/failed 不冒充已保留)", () => {
    expect(component.includes("benefitCopy")).toBe(true);
  });
});

describe("屏1 红线:公共页纪律", () => {
  it("不出现 EcoTopNav / 派诺工具推销 / 随身搭子 / 商家账号注册", () => {
    for (const banned of ["EcoTopNav", "派诺工作台", "随身搭子", "注册商家账号", "开通商家账号"]) {
      expect(component.includes(banned)).toBe(false);
    }
  });

  it("库存用尽时按钮不可用并给异常文案", () => {
    expect(component.includes("stockExhausted")).toBe(true);
    expect(component.includes("exceptionCopy")).toBe(true);
  });
});
