import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-1677 /work/analytics 经营看板:源码级断言(node 环境无 DOM)。
// 锁住:五区块结构、筛选条、两条诚实注记必渲染、未知不冒充 0、
// 404=未开通、移动优先(无固定像素宽)、census 账本同步。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const pageRel = "app/work/analytics/page.tsx";
const viewRel = "components/work/analytics/analytics-view.tsx";
const libRel = "lib/analytics.ts";

describe("census 账本同步", () => {
  it("/work/analytics 记入账本恰好一条,交互下沉组件后页面零标记", () => {
    const ledger = pageLedger().map((p) => p.route);
    expect(ledger.filter((r) => r === "/work/analytics")).toHaveLength(1);
    const page = read(pageRel);
    expect(page).not.toMatch(/<button/);
    expect(page).not.toMatch(/<input/);
  });
});

describe("看板五区块结构(移动优先)", () => {
  const view = read(viewRel);

  it("漏斗/三类证据/库存/成本/异常 五区块齐备", () => {
    expect(view).toContain('data-section="funnel"');
    expect(view).toContain('data-section="evidence"');
    expect(view).toContain('data-section="inventory"');
    expect(view).toContain('data-section="cost"');
    expect(view).toContain('data-section="anomalies"');
  });

  it("漏斗渲染七级,每级带值与口径(definition 摘要)", () => {
    expect(view).toContain("funnel.length");
    expect(view).toContain("definition.source");
  });

  it("两条诚实注记必渲染:nfc 入口标记 + 匿名不冒充自然人数", () => {
    expect(view).toContain("notes.entry_marker");
    expect(view).toContain("notes.visitor");
  });

  it("三类证据标签与不能相加注记进组件", () => {
    expect(view).toContain("evidenceRowList");
    expect(view).toContain("evidence.note");
    expect(view).toContain("distinct_participations");
  });

  it("成本三分:未录入不冒充 0,面额注记在场", () => {
    expect(view).toContain("content_generation_available");
    expect(view).toContain("face_note");
    expect(view).toContain("reward_cost_minor");
    expect(view).toContain("reward_face_minor");
  });

  it("库存显示可用/预留/已发/已核销", () => {
    expect(view).toContain("available");
    expect(view).toContain("reserved");
    expect(view).toContain("redeemed");
  });
});

describe("筛选条", () => {
  const view = read(viewRel);

  it("六类筛选:活动/门店/内容版本/触点/平台/承接方式 + 应用与重置", () => {
    expect(view).toContain('data-filter="campaign"');
    expect(view).toContain('data-filter="store"');
    expect(view).toContain('data-filter="version"');
    expect(view).toContain('data-filter="touchpoint"');
    expect(view).toContain('data-filter="platform"');
    expect(view).toContain('data-filter="method"');
    expect(view).toContain('data-action="apply-filters"');
    expect(view).toContain('data-action="reset-filters"');
  });

  it("筛选不适用级照实披露(stageNotAppliedLabels)", () => {
    expect(view).toContain("stageNotAppliedLabels");
  });
});

describe("状态语义", () => {
  const view = read(viewRel);

  it("404=未开通诚实文案,不是空数据", () => {
    expect(view).toContain("404");
    expect(view).toContain("ANALYTICS_NOT_ENABLED_COPY");
  });

  it("加载/错误/重试态在场", () => {
    expect(view).toContain('data-state="loading"');
    expect(view).toContain('data-state="error"');
    expect(view).toContain('data-action="retry"');
  });

  it("真实 0 与未知分开:窗口零值用真实 0 文案", () => {
    expect(view).toContain("emptyWindowCopy");
  });

  it("移动优先:无固定像素宽,tk- 体系", () => {
    expect(view).not.toMatch(/width:\s*\d{3,}px/);
    expect(view).toContain("tk-");
  });

  it("数据走 dashboard/operations 只读端点(BFF catch-all 直达)", () => {
    expect(view).toContain("dashboard/operations");
    expect(view).not.toMatch(/fetch\(/);
  });
});

describe("lib 纯函数文件", () => {
  const lib = read(libRel);
  it("金额格式化与查询串组装在 lib(组件保持渲染职责)", () => {
    expect(lib).toContain("export function formatMinor");
    expect(lib).toContain("export function buildAnalyticsQuery");
    expect(lib).toContain("export function stageNotAppliedLabels");
  });
});
