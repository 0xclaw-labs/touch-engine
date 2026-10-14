import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 T4 屏2「内容与发布」:源码级断言。
// 红线:默认推荐一个内容包、少量换选;平台只显示 3535 已放行路线,off/unknown
// 诚实显示不可用原因;发布主体=顾客自己账号;不建手动发布后门。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const component = codeOf(src("components/customer/ContentPublish.tsx"));

describe("屏2 内容面:默认推荐+少量换选", () => {
  it("有 data-testid=content-publish 分区", () => {
    expect(component.includes('data-testid="content-publish"')).toBe(true);
  });

  it("拉取 content-choices 与 content-snapshot(同源 BFF)", () => {
    const fetches = [...component.matchAll(/fetch\(\s*["'`]([^"'`]+)/g)].map((m) => m[1]);
    const all = fetches.join("\n");
    expect(all).toContain("/api/public/links/");
    expect(all).toContain("content-choices");
    expect(all).toContain("content-snapshot");
  });

  it("choices 为空/404 → 诚实降级,不编造内容", () => {
    expect(component.includes("choicesUnavailable")).toBe(true);
    expect(component.includes("还没有准备好")).toBe(true);
  });

  it("换选走 onSwitchVersion 回调(冻结判定在页面)", () => {
    expect(component.includes("onSwitchVersion")).toBe(true);
    expect(component.includes("canSwitch")).toBe(true);
  });

  it("快照展示文案/标题/说明;媒体不渲染原始签名 URL", () => {
    expect(component.includes("snapshotBody")).toBe(true);
    expect(component.includes("asset_ref")).toBe(false);
  });
});

describe("屏2 平台路线:3535 放行面", () => {
  it("查询 channel-admission 并经 admissionLines 出文案", () => {
    expect(component.includes("channel-admission")).toBe(true);
    expect(component.includes("admissionLines")).toBe(true);
    expect(component.includes("entry_mode=h5")).toBe(true);
    expect(component.includes("scenario=store_promo")).toBe(true);
  });

  it("未放行不出现手动发布后门指引", () => {
    expect(component.includes("手动发布")).toBe(false);
    expect(component.includes("自己手动发")).toBe(false);
  });

  it("发布主体声明:顾客自己账号、可取消、不暗发", () => {
    expect(component.includes("自己的账号")).toBe(true);
    expect(component.includes("不会替你发布")).toBe(true);
  });
});

describe("屏2 契约红线", () => {
  it("不发参与类请求(建参与是屏1主动作的唯一路径)", () => {
    expect(component.includes("/api/participations")).toBe(false);
    expect(component.includes("startParticipation")).toBe(false);
  });

  it("不触碰奖励面(与 1670 同一纪律)", () => {
    for (const banned of ["publish-reward", "reward-proof", "reward-summary"]) {
      expect(component.includes(banned)).toBe(false);
    }
  });
});
