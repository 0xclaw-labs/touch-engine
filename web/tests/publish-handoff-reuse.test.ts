import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896×1670 集成修复:三屏流里顾客先经屏1「选择内容并参与」建参与
// (3546 幂等键 cj-web-*),再进入 PublishHandoff 开始交接。3546 只按幂等键
// 去重,不按访客去重——ensureParticipation 若直接创建,会为同一访客开出
// 第二条参与(双行、双预留)。修复:先 lookup 复用自身未完参与,查不到再
// 按原路径创建;独立使用 1670 组件(无屏1)行为不变。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const component = codeOf(src("components/publish/PublishHandoff.tsx"));

describe("ensureParticipation 先恢复后创建(防同访客双参与)", () => {
  it("创建前先请求 participations/lookup 复用未完参与", () => {
    const ensure = component.slice(component.indexOf("ensureParticipation"));
    expect(ensure.includes("/api/participations/lookup")).toBe(true);
    // lookup 先于创建调用(源码顺序:lookup 块在 POST /api/participations 之前)
    const lookupAt = ensure.indexOf("/api/participations/lookup");
    const createAt = ensure.indexOf('"/api/participations"');
    expect(lookupAt).toBeGreaterThan(-1);
    expect(createAt).toBeGreaterThan(lookupAt);
  });

  it("lookup 命中未完参与即复用,不再创建", () => {
    const ensure = component.slice(component.indexOf("ensureParticipation"), component.indexOf("const start = useCallback"));
    expect(ensure.includes("participation.current =")).toBe(true);
  });

  it("1670 挂载窄补丁不变(mount guard 同源)", () => {
    const page = src("app/c/[code]/public-campaign.tsx");
    expect(page.includes("<PublishHandoff code={code} />")).toBe(true);
  });
});
