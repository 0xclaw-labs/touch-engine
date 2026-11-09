import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-4594 M11 /work/leads 线索页:源码级断言(node 环境无 DOM)。
// 锁住:口径诚实(待同步不计入已获客的注记必渲染)、来源两分、撤回显著、
// 三态+重试、feature off 未开通文案、台账恰好一条、页面零交互标记、
// 移动优先(无固定像素宽)。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const pageRel = "app/work/leads/page.tsx";
const viewRel = "components/work/leads/leads-view.tsx";

describe("census 账本同步", () => {
  it("/work/leads 记入账本恰好一条,交互下沉组件后页面零标记", () => {
    const ledger = pageLedger().map((p) => p.route);
    expect(ledger.filter((r) => r === "/work/leads")).toHaveLength(1);
    const page = read(pageRel);
    expect(page).not.toMatch(/<button/);
    expect(page).not.toMatch(/<input/);
  });
});

describe("工作台导航", () => {
  it("WorkFrame 常驻「线索」入口,入口恒在与 settings 同一口径", () => {
    const frame = read("components/work/work-frame.tsx");
    expect(frame).toContain('href: "/work/leads"');
    expect(frame).toContain('label: "线索"');
  });
});

describe("口径诚实(移动优先)", () => {
  const view = read(viewRel);

  it("「待同步不计入已获客」口径注记必渲染", () => {
    expect(view).toContain("data-field=\"pending-note\"");
    expect(view).toContain("不计入已获客");
  });

  it("四个口径分桶分开渲染:已获客/待同步/已撤销/合计", () => {
    expect(view).toContain('data-field="stat-received"');
    expect(view).toContain('data-field="stat-pending"');
    expect(view).toContain('data-field="stat-revoked"');
    expect(view).toContain('data-field="stat-total"');
  });

  it("来源两分:活动与门店留资分开标注,未知来源如实标注", () => {
    expect(view).toContain('capture_source === "store"');
    expect(view).toContain("门店留资");
    expect(view).toContain("来源未知");
  });

  it("撤回是终态:revoked 行显著标注且不再显示为可跟进状态", () => {
    expect(view).toContain("data-revoked");
    expect(view).toContain("已撤销");
  });

  it("接收回执有则显示,无则不冒充", () => {
    expect(view).toContain("has_receipt");
    expect(view).toContain("有接收回执");
  });

  it("loading/error+重试/disabled/empty 四态齐备", () => {
    expect(view).toContain('data-state="loading"');
    expect(view).toContain('data-state="error"');
    expect(view).toContain('data-state="disabled"');
    expect(view).toContain('data-state="empty"');
    expect(view).toContain('data-action="retry"');
    expect(view).toContain("线索功能未开通");
    expect(view).toContain("还没有授权线索");
  });

  it("404 = 未开通,不是空数据", () => {
    expect(view).toContain("res.status === 404");
  });

  it("移动优先:无固定像素宽", () => {
    expect(view).not.toMatch(/width:\s*\d{3,}px/);
    expect(view).not.toMatch(/min-width:\s*\d{3,}px/);
  });
});
