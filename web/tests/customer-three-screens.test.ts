import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 T6 三屏接线:public-campaign.tsx 重构守卫。
// 既有基线(PublishHandoff 挂载、CustomerPublish 挂载、留资语义、五态降级)
// 必须原样保留;新增三屏按 journeyStage 渐进呈现;浏览不建参与。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

const page = src("app/c/[code]/public-campaign.tsx");
// 去注释后断言可执行代码(注释纪律说明不算实现)。
const code = page
  .replace(/\/\*[\s\S]*?\*\//g, "")
  .split("\n")
  .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
  .join("\n");

describe("三屏组件接线", () => {
  it("引入并渲染 ActivityIntro / ContentPublish / RewardStatus", () => {
    expect(code.includes('from "@/components/customer/ActivityIntro"')).toBe(true);
    expect(code.includes('from "@/components/customer/ContentPublish"')).toBe(true);
    expect(code.includes('from "@/components/customer/RewardStatus"')).toBe(true);
    expect(code.includes("<ActivityIntro")).toBe(true);
    expect(code.includes("<ContentPublish")).toBe(true);
    expect(code.includes("<RewardStatus")).toBe(true);
  });

  it("journeyStage 决定渐进呈现(阶段≠堆任务大厅)", () => {
    expect(code.includes("journeyStage(")).toBe(true);
    expect(code.includes('const stage = journeyStage(')).toBe(true);
    // 屏2/屏3 只在参与开始后呈现(participationOpen = stage !== browse)
    expect(code.includes('stage !== "browse"')).toBe(true);
    expect(code.includes('participationOpen && (')).toBe(true);
  });

  it("恢复:挂载时一次性 lookup;404=静默全新开始", () => {
    expect(code.includes("lookupParticipation(")).toBe(true);
  });

  it("开始参与只在显式点击:handleStart 调 startParticipation(浏览路径不建参与)", () => {
    expect(code.includes("startParticipation(")).toBe(true);
    // ActivityIntro 的开始回调是唯一入口
    expect(code.includes("handleParticipationStart")).toBe(true);
  });

  it("换选走 canSwitchDraft + switchDraft(冻结判定在页面)", () => {
    expect(code.includes("canSwitchDraft(")).toBe(true);
    expect(code.includes("switchDraft(")).toBe(true);
  });

  it("取消入口存在(cancelParticipation)", () => {
    expect(code.includes("cancelParticipation(")).toBe(true);
  });
});

describe("基线保留(1670/PR#15/留资/五态)", () => {
  it("PublishHandoff 挂载窄补丁保留(import+挂载,≤10 行提及)", () => {
    expect(page.includes('import { PublishHandoff } from "@/components/publish/PublishHandoff";')).toBe(true);
    expect(page.includes("<PublishHandoff code={code} />")).toBe(true);
    const lines = page.split("\n").filter((l) => l.includes("PublishHandoff"));
    expect(lines.length).toBeLessThanOrEqual(10);
  });

  it("CustomerPublish 基线挂载保留", () => {
    expect(page.includes('<CustomerPublish code={code} defaultCopy={view?.public_content ?? ""} />')).toBe(true);
  });

  it("留资语义保留:leadOutcomeState 同源委托与撤销态", () => {
    expect(page).toMatch(/leadOutcomeState\(revokeDone\s*\?\s*LEAD_REVOKED_COPY\s*:\s*outcome\)/);
    expect(page).toContain('data-state="revoked"');
  });

  it("五态降级与 view-events 保留", () => {
    expect(code.includes("degraded-state")).toBe(true);
    expect(code.includes("view-events")).toBe(true);
  });

  it("留资不成为领奖门槛(留资区块独立于参与状态)", () => {
    expect(code.includes('data-testid="lead-disclosure"')).toBe(true);
    // 留资渲染条件不得依赖参与/奖励状态
    expect(code.includes("disclosureReady ? (")).toBe(true);
  });
});

describe("公共页红线(重构后仍然成立)", () => {
  it("无内联样式;无 EcoTopNav;无生态推销字样", () => {
    expect(page).not.toMatch(/style=\{\{/);
    for (const banned of ["EcoTopNav", "派诺工作台", "随身搭子"]) {
      expect(page.includes(banned)).toBe(false);
    }
  });

  it("请求全部走同源 /api BFF", () => {
    const fetches = [...code.matchAll(/fetch\(\s*["'`]([^"'`]+)/g)].map((m) => m[1]);
    for (const f of fetches) {
      expect(f.startsWith("/api/")).toBe(true);
    }
  });

  it("不把 tenant 参数发进任何请求(短码不能换租户;tenant 只作来源统计读取)", () => {
    const fetches = [...code.matchAll(/fetch\(\s*["'`]([^"'`]+)/g)].map((m) => m[1]);
    for (const f of fetches) {
      expect(f.includes("tenant")).toBe(false);
    }
    // 请求体也不携带 tenant 覆盖(参与门面 body 固定)
    expect(code.includes("tenant_id:")).toBe(false);
  });
});
