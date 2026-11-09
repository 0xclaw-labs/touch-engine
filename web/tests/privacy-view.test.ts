import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 C6 隐私与授权(/s/{storeCode}/privacy):复用 /contact 撤销链。
// 红线:告知(接收商家/用途/必填字段/告知版本)+独立营销许可;拒绝营销不阻断;
// 提交后状态诚实——待同步不说「销售已收到」;撤销即撤回授权;无 link code
// 或留资未开通时给诚实待开态,不伪建表单。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const view = codeOf(src("app/s/[storeCode]/privacy/privacy-view.tsx"));

describe("C6 链路:复用 /contact 端点族", () => {
  it("lead-form/lead-submissions/lead-status/lead-revocations 全链在场", () => {
    expect(view.includes("/lead-form")).toBe(true);
    expect(view.includes("/lead-submissions")).toBe(true);
    expect(view.includes("/lead-status")).toBe(true);
    expect(view.includes("/lead-revocations")).toBe(true);
  });

  it("文案出口复用 leadOutcomeCopy/LEAD_REVOKED_COPY(与 /c 同源)", () => {
    expect(view.includes("leadOutcomeCopy")).toBe(true);
    expect(view.includes("LEAD_REVOKED_COPY")).toBe(true);
  });
});

describe("C6 告知与许可", () => {
  it("告知四要素:接收商家/用途/必填字段/告知版本", () => {
    expect(view.includes("接收方")).toBe(true);
    expect(view.includes("用途")).toBe(true);
    expect(view.includes("必填")).toBe(true);
    expect(view.includes("告知版本")).toBe(true);
  });

  it("营销许可独立勾选;拒绝不阻断浏览", () => {
    expect(view.includes("marketing-optin")).toBe(true);
    expect(view.includes("不勾选")).toBe(true);
  });
});

describe("C6 状态诚实", () => {
  it("待同步语义:不伪报「销售已收到」(只允许否定句提及)", () => {
    expect(view.includes("待同步")).toBe(true);
    expect(view.includes("sales_received") || view.includes("salesReceived")).toBe(true);
    for (const line of view.split("\n")) {
      if (!line.includes("销售已收到")) continue;
      expect(line.includes("不会") || line.includes("观察不到")).toBe(true);
    }
  });

  it("撤销流程在场:撤销成功=已撤销;失败给核对手机号引导", () => {
    expect(view.includes("撤销我的授权")).toBe(true);
    expect(view.includes("已撤销")).toBe(true);
    expect(view.includes("核对手机号")).toBe(true);
  });

  it("无 link code / 留资未开通 → 诚实待开态(不伪建表单)", () => {
    expect(view.includes("privacy-not-open")).toBe(true);
    expect(view.includes("暂未开通在线联系与授权管理")).toBe(true);
  });
});

describe("C6 布局", () => {
  it("无生态导航;返回门店入口;390/430/桌面列模式", () => {
    expect(view.includes("EcoTopNav")).toBe(false);
    expect(view.includes("返回门店")).toBe(true);
    expect(view.includes("data-columns")).toBe(true);
    expect(view.includes("data-viewport")).toBe(true);
  });
});
