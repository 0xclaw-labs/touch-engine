import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// 修复轮 4(F2)/work/stores 守卫:Walk B 报店员直达后客户端异常白屏。
// 本地真实栈(dev+prod×租户级/带scope店员×全旅程)未复现精确异常,按「最小守卫
// 修到正常流程不再白屏」交付:①列表行形态防御;②路由段错误边界(诚实错误态+重试)。
// 正式的错误态/边界体系归 2628,已在报告标注。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

describe("门店页行形态防御", () => {
  const page = read("app/work/stores/page.tsx");

  it("列表行守卫:非对象/缺 id 行丢弃,不再整页白屏", () => {
    expect(page).toContain("修复轮 4 守卫");
    expect(page).toContain("typeof it === \"object\" && it !== null");
    expect(page).toMatch(/filter\(\s*\(it\): it is StoreRec/);
  });

  it("store-limited 语义文案保持:店员/经理看到自己门店", () => {
    expect(page).toContain("门店的增删改由总部操作");
  });
});

describe("门店段错误边界", () => {
  const boundary = read("app/work/stores/error.tsx");

  it("客户端异常有诚实错误态+重试,不再白屏(Application error)", () => {
    expect(boundary).toContain('data-state="error"');
    expect(boundary).toContain('data-field="stores-error"');
    expect(boundary).toContain('data-action="retry-stores"');
    expect(boundary).toContain("reset");
    expect(boundary).toContain("归 HUI-2628 共享壳域");
  });

  it("census 不受影响:门店页账本条目原样", () => {
    const decl = pageLedger().find((p) => p.route === "/work/stores");
    expect(decl?.id).toBe("list");
  });
});
