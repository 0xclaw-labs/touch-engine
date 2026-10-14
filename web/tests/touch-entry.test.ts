import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src/app");

function read(rel: string): string {
  const abs = path.join(root, rel);
  expect(existsSync(abs), abs).toBe(true);
  return readFileSync(abs, "utf8");
}

const page = read("t/[code]/page.tsx");
const entry = read("t/[code]/touch-entry.tsx");

describe("/t/[code] server 壳:FEATURE 双保险的 web 半边", () => {
  it("读 FEATURE_TOUCHPOINT_ENTRY,off(默认)走 notFound", () => {
    expect(page).toContain("FEATURE_TOUCHPOINT_ENTRY");
    expect(page).toContain("notFound");
  });

  it("动态渲染:不在构建期把开关值定死", () => {
    expect(page).toContain("force-dynamic");
  });

  it("页面本体在 client 组件,server 壳只做闸门", () => {
    expect(page).toContain("touch-entry");
    expect(page.length).toBeLessThan(1200);
  });
});

describe("touch-entry client:临时跳转,绝不 301/308,白名单之外不跳", () => {
  it("用 location.replace 临时跳转(不写 301/308,不赋值 location.href)", () => {
    expect(entry).toContain("location.replace");
    expect(entry).not.toContain("location.href");
    expect(entry).not.toContain("301");
    expect(entry).not.toContain("308");
    expect(entry).not.toContain("permanentRedirect");
    expect(entry).not.toContain("redirect(");
  });

  it("走公共端点 + 白名单守卫(touchEntryRedirect),不自行拼跳转路径", () => {
    expect(entry).toContain("/api/public/touchpoints/");
    expect(entry).toContain("encodeURIComponent");
    expect(entry).toContain("touchEntryRedirect");
    expect(entry).not.toMatch(/\/c\/\$\{/); // 跳转路径只能来自 lib 白名单
  });

  it("404/未知态给本地文案(touchEntryFace),不冒充成功", () => {
    expect(entry).toContain("touchEntryFace");
  });

  it("公开页不出现 uid_hint 等后台字段名", () => {
    expect(entry).not.toContain("uid_hint");
    expect(entry).not.toContain("x-tenant-id");
    expect(entry).not.toContain("org_owner");
  });
});
