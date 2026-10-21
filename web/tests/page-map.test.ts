import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
  MissingRegionError,
  UnknownPatternError,
  pageLedger,
  patternIds,
  requiredRegions,
  validateDeclaration,
} from "../src/lib/page-map";

const appDir = path.resolve(process.cwd(), "src/app");
const pageFile = /^page\.(tsx|ts|jsx|js)$/;

function discoverRoutes(dir = appDir): string[] {
  const routes: string[] = [];
  const walk = (current: string) => {
    for (const name of readdirSync(current)) {
      const abs = path.join(current, name);
      if (statSync(abs).isDirectory()) {
        walk(abs);
        continue;
      }
      if (!pageFile.test(name)) continue;
      const rel = path.relative(appDir, abs).split(path.sep).join("/");
      const segments = rel.split("/").slice(0, -1).filter((segment) => !/^\(.+\)$/.test(segment));
      routes.push(segments.length === 0 ? "/" : `/${segments.join("/")}`);
    }
  };
  walk(dir);
  return routes;
}

function mismatch(discovered: readonly string[], recorded: readonly string[]): { missing: string[]; extra: string[] } {
  const found = new Set(discovered);
  const booked = new Set(recorded);
  return {
    missing: [...booked].filter((route) => !found.has(route)).sort(),
    extra: [...found].filter((route) => !booked.has(route)).sort(),
  };
}

function without(regions: readonly string[], drop: string): string[] {
  return regions.filter((region) => region !== drop);
}

describe("冻结的模式编号", () => {
  it("14 个编号一个不能增删，顺序固定", () => {
    const want = [
      "portal",
      "login",
      "workspace",
      "list",
      "detail",
      "creation",
      "media_result",
      "compare",
      "review",
      "billing",
      "settings",
      "recovery",
      "public_consumer",
      "editor_shell",
    ];
    const got = patternIds();
    expect(got).toEqual(want);
    expect(got).toHaveLength(14);
    got[0] = "mutated";
    got.push("extra");
    expect(patternIds()).toEqual(want);
  });

  it("必带区域是 primary_action、status、empty", () => {
    const got = requiredRegions();
    expect(got).toEqual(["primary_action", "status", "empty"]);
    got[0] = "mutated";
    expect(requiredRegions()[0]).toBe("primary_action");
  });
});

describe("声明校验", () => {
  it("未知编号失败，且不报成漏区域", () => {
    const regions = requiredRegions();
    for (const id of ["", "Portal", "portal ", "login-entry", "editor-shell", "media-result", "public-consumer", "goboost"]) {
      expect(() => validateDeclaration({ id, regions }), id).toThrow(UnknownPatternError);
      try {
        validateDeclaration({ id, regions });
      } catch (err) {
        expect(err).toBeInstanceOf(UnknownPatternError);
        expect(err).not.toBeInstanceOf(MissingRegionError);
        expect((err as UnknownPatternError).id).toBe(id);
      }
    }
  });

  it("漏区域失败，缺失按必带顺序", () => {
    const required = requiredRegions();
    for (const id of patternIds()) {
      for (const drop of required) {
        expect(() => validateDeclaration({ id, regions: without(required, drop) })).toThrow(MissingRegionError);
        try {
          validateDeclaration({ id, regions: without(required, drop) });
        } catch (err) {
          expect(err).toBeInstanceOf(MissingRegionError);
          const miss = err as MissingRegionError;
          expect(miss.id).toBe(id);
          expect(miss.regions).toEqual([drop]);
        }
      }
      try {
        validateDeclaration({ id });
      } catch (err) {
        expect(err).toBeInstanceOf(MissingRegionError);
        expect((err as MissingRegionError).regions).toEqual(required);
      }
    }
  });

  it("比较精确，空白和大小写不算同一个区域", () => {
    expect(() => validateDeclaration({
      id: "portal",
      regions: [" primary_action", "Status", "empty"],
    })).toThrow(MissingRegionError);
    try {
      validateDeclaration({
        id: "portal",
        regions: [" primary_action", "Status", "empty"],
      });
    } catch (err) {
      expect((err as MissingRegionError).regions).toEqual(["primary_action", "status"]);
    }
  });

  it("多余区域名忽略", () => {
    expect(() => validateDeclaration({
      id: "detail",
      regions: ["header", "primary_action", "status", "empty", "secondary_actions"],
    })).not.toThrow();
    expect(() => validateDeclaration({
      id: "detail",
      regions: ["header", "primary_action", "status"],
    })).toThrow(MissingRegionError);
  });
});

describe("已有用户页面账本", () => {
  it("扫实际 page 文件：缺页、多页、重复路由都失败", () => {
    expect(mismatch(["/a", "/c"], ["/a", "/b"])).toEqual({ missing: ["/b"], extra: ["/c"] });

    const discovered = discoverRoutes();
    const recorded = pageLedger().map((page) => page.route);
    expect(recorded).toHaveLength(new Set(recorded).size);
    expect(discovered).toHaveLength(new Set(discovered).size);
    const diff = mismatch(discovered, recorded);
    expect(diff).toEqual({ missing: [], extra: [] });
    expect(discovered.some((route) => route === "/api" || route.startsWith("/api/"))).toBe(false);
  });

  it("每条路由恰好一条，编号已知，三个区域都在", () => {
    const pages = pageLedger();
    expect(pages.length).toBeGreaterThan(0);
    const again = pageLedger();
    again[0].regions[0] = "mutated";
    again[0].route = "/mutated";
    expect(pageLedger()[0].regions).toEqual(["primary_action", "status", "empty"]);
    expect(pageLedger()[0].route).not.toBe("/mutated");

    for (const page of pages) {
      expect(page.route.startsWith("/")).toBe(true);
      expect(page.regions).toEqual(["primary_action", "status", "empty"]);
      expect(() => validateDeclaration(page)).not.toThrow();
    }
  });

  it("公开顾客页用 public_consumer，不记成 workspace 或 login", () => {
    const publics = pageLedger().filter((page) => page.route === "/c" || page.route.startsWith("/c/"));
    expect(publics.map((page) => page.route).sort()).toEqual(["/c/[code]", "/c/[code]/contact"]);
    for (const page of publics) {
      expect(page.id).toBe("public_consumer");
      expect(page.id).not.toBe("workspace");
      expect(page.id).not.toBe("login");
    }
  });

  it("商家列表用 list，商家详情用 detail", () => {
    const byRoute = new Map(pageLedger().map((page) => [page.route, page.id]));
    expect(byRoute.get("/work/stores")).toBe("list");
    expect(byRoute.get("/work/campaigns")).toBe("list");
    expect(byRoute.get("/work/materials")).toBe("list");
    expect(byRoute.get("/work/campaigns/new")).toBe("creation");
    expect(byRoute.get("/work/campaigns/new")).not.toBe("list");
    expect(byRoute.get("/work/campaigns/[id]")).toBe("detail");
    expect(byRoute.get("/work/campaigns/[id]")).not.toBe("list");
    expect(byRoute.get("/work/campaigns/[id]")).not.toBe("workspace");
  });

  it("今天是 workspace；没有独立登录页，不另造 /login", () => {
    const byRoute = new Map(pageLedger().map((page) => [page.route, page.id]));
    expect(byRoute.get("/")).toBe("workspace");
    expect(byRoute.get("/")).not.toBe("login");
    expect(byRoute.get("/work/analytics")).toBe("workspace");
    expect(discoverRoutes()).not.toContain("/login");
    expect(pageLedger().some((page) => page.route === "/login")).toBe(false);
    expect(patternIds()).toContain("login");
  });

  it("旧后台和奖励页不记成公开页", () => {
    const byRoute = new Map(pageLedger().map((page) => [page.route, page.id]));
    expect(byRoute.get("/admin")).toBe("workspace");
    expect(byRoute.get("/admin/preview")).toBe("billing");
    expect(byRoute.get("/work/rewards")).toBe("billing");
    for (const route of ["/admin", "/admin/preview", "/work/rewards", "/"]) {
      expect(byRoute.get(route)).not.toBe("public_consumer");
    }
  });
});

describe("这一片不接外部能力", () => {
  it("生产源码不引用 public-ai，也不写已生成或已送出", () => {
    const src = readFileSync(new URL("../src/lib/page-map.ts", import.meta.url), "utf8");
    expect(src).not.toMatch(/^import /m);
    expect(src).not.toContain("public-ai");
    expect(src).not.toContain("已生成");
    expect(src).not.toContain("已送出");
  });
});
