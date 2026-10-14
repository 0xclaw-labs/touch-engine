import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

// HUI-2222：「确认与核销」入口接线 —— 开关随 session 下发，两个导航槽位
// （/work 共享 WorkFrame、根页 TodayDesk）共用同一判定，off 时不渲染。
// 源码级断言（仓内无组件测试库，沿 page/product-pages 的来源守卫口径）。

describe("session carries feature flags", () => {
  it("merchant-session 类型和解析都带 features，且登出/切换时清零", () => {
    const src = read("components/work/merchant-session.tsx");
    expect(src).toContain("features: SessionFeatures");
    expect(src).toContain("function parseFeatures");
    expect(src).toContain("setFeatures(parseFeatures(data))");
  });
});

describe("work frame nav slot", () => {
  it("/work 共享导航按布尔 prop 追加确认与核销项，href 收口到共享常量", () => {
    const src = read("components/work/work-frame.tsx");
    expect(src).toContain("serviceDesk?: boolean");
    expect(src).toContain("SERVICE_DESK_HREF");
    expect(src).toContain("SERVICE_DESK_LABEL");
    expect(src).not.toContain('"/work/service"'); // 不允许第二份硬编码
  });
});

describe("today desk nav slot", () => {
  it("根页今日台导航同样按判定渲染入口", () => {
    const src = read("components/work/today-desk.tsx");
    expect(src).toContain("serviceDeskEntryVisible");
    expect(src).toContain("SERVICE_DESK_HREF");
    expect(src).toContain("SERVICE_DESK_LABEL");
    expect(src).not.toContain('"/work/service"');
  });
});

describe("work layout wiring", () => {
  it("work/layout 把 session 的开关与角色交给 WorkFrame", () => {
    const src = read("app/work/layout.tsx");
    expect(src).toContain("serviceDeskEntryVisible(session.features, session.role)");
  });
});
