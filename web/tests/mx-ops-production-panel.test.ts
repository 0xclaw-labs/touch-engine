import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const panelRel = "components/work/production-panel.tsx";
const pageRel = "app/work/campaigns/[id]/page.tsx";

describe("活动页云端制作区（MX-OPS 增量）", () => {
  const panel = read(panelRel);
  const page = read(pageRel);

  it("挂载在活动详情，与内容包采用区分开", () => {
    expect(page).toContain("ProductionPanel");
    expect(page).toContain("<ProductionPanel campaignId={id}");
    expect(panel).toContain('id="production"');
    expect(panel).toContain("云端制作");
  });

  it("读取真实制作交接列表，不伪造队列", () => {
    expect(panel).toMatch(/session\.api\("GET", `campaigns\/\$\{campaignId\}\/content-handoffs`\)/);
    expect(panel).toContain("presentProduction");
    expect(panel).toContain("没有拿到权威队列投影");
  });

  it("展示 phase/等待原因/可空 ETA/下一窗口/策略版本", () => {
    expect(panel).toContain("waitingKindLabel");
    expect(panel).toContain("queue.etaText");
    expect(panel).toContain("下一可执行窗口");
    expect(panel).toContain("policyVersion");
    expect(panel).toContain("waitingJobsAheadText");
    expect(panel).toContain("runningJobsText");
  });

  it("三层费用分开，且说明唯一收费归星模与公共 Billing", () => {
    expect(panel).toContain("merchantToolFee");
    expect(panel).toContain("campaignBudget");
    expect(panel).toContain("modelCost");
    expect(panel).toContain("feeNote");
    expect(panel).not.toMatch(/钱包余额/);
  });

  it("候选只进候选区，显式采用；不自动替换/外发", () => {
    expect(panel).toContain("回流产物只进候选");
    expect(panel).toContain("显式采用");
    expect(panel).toContain("不会自动替换当前启用内容");
    expect(panel).not.toMatch(/data-action="auto-adopt"/);
  });

  it("恢复入口回同一任务；夜间按钮在引擎未接通时禁用并说明原因", () => {
    expect(panel).toContain("recovery.text");
    expect(panel).toMatch(/data-action="arrange-night"[\s\S]{0,200}disabled/);
    expect(panel).toContain("制作引擎尚未接通");
  });

  it("不出现 internal_debug/worker 内部计数", () => {
    expect(panel).not.toContain("internal_debug");
    expect(panel).not.toContain("ready_workers");
    expect(panel).not.toContain("busy_workers");
    expect(panel).not.toContain("starting_workers");
  });

  it("顾客触碰提示零新生成", () => {
    expect(panel).toContain("顾客触碰只读取已采用的预制内容");
    expect(panel).toContain("零新生成");
  });
});
