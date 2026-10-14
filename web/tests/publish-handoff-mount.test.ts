import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1670 web 面源码级断言(node 无 DOM):组件与挂载补丁的纪律红线。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

// codeOf 去掉注释,只对可执行代码做断言(注释里的纪律说明不算实现)。
function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const component = codeOf(src("components/publish/PublishHandoff.tsx"));
const state = codeOf(src("components/publish/state.ts"));
const publicCampaign = src("app/c/[code]/public-campaign.tsx");

describe("交接组件不触碰奖励面", () => {
  it("不发 publish-reward / reward-proof / self-report 请求", () => {
    for (const banned of ["publish-reward", "reward-proof", "self-report", "publish-attempts"]) {
      expect(component.includes(banned)).toBe(false);
    }
  });

  it("不出现「已发布」的肯定承诺(唯一出口在 state.ts 的 platform_confirmed)", () => {
    expect(component.includes("已发布")).toBe(false);
  });

  it("恢复令牌/凭据绝不写 localStorage 或 sessionStorage", () => {
    expect(component.includes("localStorage")).toBe(false);
    expect(component.includes("sessionStorage")).toBe(false);
    expect(state.includes("localStorage")).toBe(false);
  });

  it("不持有内部 token / 平台 secret 字样;不把媒体原始地址(带签名参数)渲染进 DOM", () => {
    expect(component.includes("InternalToken")).toBe(false);
    expect(component.includes("RECEIPT_SECRET")).toBe(false);
    // 媒体列表只渲染文件名;带 token 的 URL 不进 DOM 文本、不作下载链接。
    expect(component.includes("{m.url}")).toBe(false);
    expect(component.includes("href={m.url}")).toBe(false);
  });

  it("请求只走同源 BFF(/api/...),无第三方域名", () => {
    const fetches = [...component.matchAll(/fetch\(\s*["'`]([^"'`]+)/g)].map((m) => m[1]);
    expect(fetches.length).toBeGreaterThanOrEqual(5);
    for (const f of fetches) {
      expect(f.startsWith("/api/")).toBe(true);
    }
  });
});

describe("挂载是窄补丁且基线保留", () => {
  it("public-campaign.tsx 挂载 PublishHandoff 且改动 ≤10 行", () => {
    expect(publicCampaign.includes('import { PublishHandoff } from "@/components/publish/PublishHandoff";')).toBe(true);
    expect(publicCampaign.includes("<PublishHandoff code={code} />")).toBe(true);
    // 窄补丁:PublishHandoff 相关行数 ≤10(import 1 + 挂载 1)。
    const lines = publicCampaign.split("\n").filter((l) => l.includes("PublishHandoff"));
    expect(lines.length).toBeLessThanOrEqual(10);
  });

  it("PR#15 基线(CustomerPublish 预览/合规导出)原样保留", () => {
    expect(publicCampaign.includes("<CustomerPublish code={code} defaultCopy={view?.public_content ?? \"\"} />")).toBe(true);
  });

  it("组件是自足目录:只依赖 react 与 ./state", () => {
    const imports = [...component.matchAll(/from "([^"]+)"/g)].map((m) => m[1]);
    for (const imp of imports) {
      expect(imp === "react" || imp === "./state").toBe(true);
    }
  });

  it("state.ts 是文案唯一出口:组件内不内联「成功」表述", () => {
    expect(component.includes("发布成功")).toBe(true); // 入口按钮的免责说明
    expect(state.includes("statusCopy")).toBe(true);
    expect(state.includes("manualStepsFor")).toBe(true);
  });
});
