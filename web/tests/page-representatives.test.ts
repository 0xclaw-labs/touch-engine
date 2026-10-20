import { describe, expect, it } from "vitest";
import { pageCensus } from "../src/lib/page-census";
import { pageRepresentatives, selectRepresentatives } from "../src/lib/page-representatives";

const absent = {
  route: "not_present",
  file: "not_present",
  id: "not_present",
  surface: "not_present",
  stack: "not_present",
  screenshot: "not_measured",
} as const;

// 与生产函数分开写的同一规则。只读普查行，不调用 selectRepresentatives。
function representativesByHand(
  rows: readonly { route: string; file: string; id: string; surface: string; stack: string }[],
) {
  let entry: (typeof rows)[number] | undefined;
  let home: (typeof rows)[number] | undefined;
  let list: (typeof rows)[number] | undefined;
  let detail: (typeof rows)[number] | undefined;
  let main: (typeof rows)[number] | undefined;
  let media: (typeof rows)[number] | undefined;
  let review: (typeof rows)[number] | undefined;
  for (const row of rows) {
    if (entry === undefined && (row.id === "portal" || row.id === "login")) entry = row;
    if (home === undefined && row.id === "workspace") home = row;
    if (list === undefined && row.id === "list") list = row;
    if (detail === undefined && row.id === "detail") detail = row;
    if (main === undefined && row.id === "creation") main = row;
    if (media === undefined && row.id === "media_result") media = row;
    if (review === undefined && row.id === "review") review = row;
  }
  const copied = (row: (typeof rows)[number] | undefined) => {
    if (row === undefined) {
      return {
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      };
    }
    return {
      route: row.route,
      file: row.file,
      id: row.id,
      surface: row.surface,
      stack: row.stack,
      screenshot: "not_measured",
    };
  };
  return [
    copied(entry),
    copied(home),
    copied(list),
    copied(detail),
    copied(main),
    copied(media ?? review),
  ];
}

describe("代表页夹具", () => {
  it("两个 portal 取第一条", () => {
    const rows = [
      { route: "/enter-a", file: "src/app/enter-a/page.tsx", id: "portal", surface: "portal", stack: "next" },
      { route: "/enter-b", file: "src/app/enter-b/page.tsx", id: "portal", surface: "portal", stack: "next" },
      { route: "/", file: "src/app/page.tsx", id: "workspace", surface: "work", stack: "next" },
      { route: "/items", file: "src/app/items/page.tsx", id: "list", surface: "work", stack: "next" },
      { route: "/items/more", file: "src/app/items/more/page.tsx", id: "list", surface: "work", stack: "next" },
      { route: "/items/1", file: "src/app/items/1/page.tsx", id: "detail", surface: "work", stack: "next" },
      { route: "/new", file: "src/app/new/page.tsx", id: "creation", surface: "editor", stack: "next" },
      { route: "/film", file: "src/app/film/page.tsx", id: "media_result", surface: "public", stack: "next" },
    ];
    expect(selectRepresentatives(rows)).toEqual([
      { route: "/enter-a", file: "src/app/enter-a/page.tsx", id: "portal", surface: "portal", stack: "next", screenshot: "not_measured" },
      { route: "/", file: "src/app/page.tsx", id: "workspace", surface: "work", stack: "next", screenshot: "not_measured" },
      { route: "/items", file: "src/app/items/page.tsx", id: "list", surface: "work", stack: "next", screenshot: "not_measured" },
      { route: "/items/1", file: "src/app/items/1/page.tsx", id: "detail", surface: "work", stack: "next", screenshot: "not_measured" },
      { route: "/new", file: "src/app/new/page.tsx", id: "creation", surface: "editor", stack: "next", screenshot: "not_measured" },
      { route: "/film", file: "src/app/film/page.tsx", id: "media_result", surface: "public", stack: "next", screenshot: "not_measured" },
    ]);
  });

  it("review 在 media_result 前面时，Result 仍是第一条 media_result", () => {
    const rows = [
      { route: "/check", file: "src/app/check/page.tsx", id: "review", surface: "work", stack: "next" },
      { route: "/film-a", file: "src/app/film-a/page.tsx", id: "media_result", surface: "public", stack: "uni-app-x" },
      { route: "/film-b", file: "src/app/film-b/page.tsx", id: "media_result", surface: "public", stack: "next" },
    ];
    expect(selectRepresentatives(rows)).toEqual([
      absent,
      absent,
      absent,
      absent,
      absent,
      {
        route: "/film-a",
        file: "src/app/film-a/page.tsx",
        id: "media_result",
        surface: "public",
        stack: "uni-app-x",
        screenshot: "not_measured",
      },
    ]);
  });

  it("只有 review 时取第一条 review", () => {
    const rows = [
      { route: "/check-a", file: "src/app/check-a/page.tsx", id: "review", surface: "work", stack: "next" },
      { route: "/check-b", file: "src/app/check-b/page.tsx", id: "review", surface: "admin", stack: "next" },
    ];
    expect(selectRepresentatives(rows)).toEqual([
      absent,
      absent,
      absent,
      absent,
      absent,
      {
        route: "/check-a",
        file: "src/app/check-a/page.tsx",
        id: "review",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
    ]);
  });

  it("没有 creation 时 Main Action 为 not_present", () => {
    const rows = [
      { route: "/gate", file: "src/app/gate/page.tsx", id: "login", surface: "portal", stack: "next" },
      { route: "/", file: "src/app/page.tsx", id: "workspace", surface: "work", stack: "next" },
      { route: "/items", file: "src/app/items/page.tsx", id: "list", surface: "work", stack: "next" },
      { route: "/items/1", file: "src/app/items/1/page.tsx", id: "detail", surface: "work", stack: "next" },
      { route: "/check", file: "src/app/check/page.tsx", id: "review", surface: "work", stack: "next" },
    ];
    expect(selectRepresentatives(rows)).toEqual([
      { route: "/gate", file: "src/app/gate/page.tsx", id: "login", surface: "portal", stack: "next", screenshot: "not_measured" },
      { route: "/", file: "src/app/page.tsx", id: "workspace", surface: "work", stack: "next", screenshot: "not_measured" },
      { route: "/items", file: "src/app/items/page.tsx", id: "list", surface: "work", stack: "next", screenshot: "not_measured" },
      { route: "/items/1", file: "src/app/items/1/page.tsx", id: "detail", surface: "work", stack: "next", screenshot: "not_measured" },
      absent,
      { route: "/check", file: "src/app/check/page.tsx", id: "review", surface: "work", stack: "next", screenshot: "not_measured" },
    ]);
  });

  it("Portal 不等于 portal", () => {
    const rows = [
      { route: "/Gate", file: "src/app/Gate/page.tsx", id: "Portal", surface: "portal", stack: "next" },
      { route: "/sign-in", file: "src/app/sign-in/page.tsx", id: "Login", surface: "portal", stack: "next" },
      { route: "/gate", file: "src/app/gate/page.tsx", id: "portal", surface: "portal", stack: "next" },
    ];
    expect(selectRepresentatives(rows)[0]).toEqual({
      route: "/gate",
      file: "src/app/gate/page.tsx",
      id: "portal",
      surface: "portal",
      stack: "next",
      screenshot: "not_measured",
    });
    expect(selectRepresentatives([{ route: "/Gate", file: "src/app/Gate/page.tsx", id: "Portal", surface: "portal", stack: "next" }])[0]).toEqual(absent);
  });

  it("login 排在 portal 前面时，Entry 取 login", () => {
    const rows = [
      { route: "/sign-in", file: "src/app/sign-in/page.tsx", id: "login", surface: "portal", stack: "next" },
      { route: "/gate", file: "src/app/gate/page.tsx", id: "portal", surface: "portal", stack: "next" },
    ];
    expect(selectRepresentatives(rows)[0]).toEqual({
      route: "/sign-in",
      file: "src/app/sign-in/page.tsx",
      id: "login",
      surface: "portal",
      stack: "next",
      screenshot: "not_measured",
    });
  });

  it("只抄 route、file、id、surface、stack，screenshot 固定 not_measured", () => {
    const row = {
      route: "/admin",
      file: "src/app/admin/page.tsx",
      id: "workspace",
      surface: "admin",
      stack: "next",
      markers: ["TODO"],
      screenshot: "shot.png",
      token_source: "measured",
    };
    const got = selectRepresentatives([row]);
    expect(got).toEqual([
      absent,
      { route: "/admin", file: "src/app/admin/page.tsx", id: "workspace", surface: "admin", stack: "next", screenshot: "not_measured" },
      absent,
      absent,
      absent,
      absent,
    ]);
    expect(got[1]).not.toHaveProperty("markers");
    got[1].route = "/mutated";
    expect(row.route).toBe("/admin");
    expect(selectRepresentatives([row])[1].route).toBe("/admin");
  });
});

describe("真实普查上的 6 条", () => {
  it("手写规则走 pageCensus()，和导出的 6 条深比较", () => {
    const want = [
      absent,
      { route: "/", file: "src/app/page.tsx", id: "workspace", surface: "work", stack: "next", screenshot: "not_measured" },
      { route: "/work/stores", file: "src/app/work/stores/page.tsx", id: "list", surface: "work", stack: "next", screenshot: "not_measured" },
      {
        route: "/work/campaigns/[id]",
        file: "src/app/work/campaigns/[id]/page.tsx",
        id: "detail",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        route: "/work/campaigns/new",
        file: "src/app/work/campaigns/new/page.tsx",
        id: "creation",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      // HUI-3562：/work/service 入账后，第 6 槽(media_result ?? review)由它代表。
      {
        route: "/work/service",
        file: "src/app/work/service/page.tsx",
        id: "review",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
    ];
    const census = pageCensus();
    expect(representativesByHand(census)).toEqual(want);
    expect(pageRepresentatives()).toEqual(want);
    expect(pageRepresentatives()).toEqual(representativesByHand(census));
    expect(pageRepresentatives()).toHaveLength(6);
    expect(pageRepresentatives().map((page) => page.route)).toEqual([
      "not_present",
      "/",
      "/work/stores",
      "/work/campaigns/[id]",
      "/work/campaigns/new",
      "/work/service",
    ]);
    expect(census.some((page) => page.id === "portal" || page.id === "login")).toBe(false);
    // HUI-2628 起创建页(creation)有真实路由;media_result 仍空缺。
    expect(census.some((page) => page.id === "creation")).toBe(true);
    expect(census.some((page) => page.id === "media_result")).toBe(false);
    expect(census.some((page) => page.id === "review")).toBe(true);
    expect(census[0]?.id).toBe("workspace");
    expect(census[0]?.route).toBe("/");

    const again = pageRepresentatives();
    again[1].route = "/mutated";
    again[1].id = "portal";
    again.push({ ...absent, route: "/extra" });
    expect(pageRepresentatives()).toEqual(want);
  });
});
