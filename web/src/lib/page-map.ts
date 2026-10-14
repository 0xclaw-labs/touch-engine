// HUI-2624 第一片：只给已经存在的用户页面记账。
// 14 个模式编号冻结，不能增删。每个声明必须带 primary_action、status、empty。
// 未知编号或漏区域失败。多余区域名忽略。比较精确，大小写和空白都算。
// 不渲染页面，不新增路由，不引用外部模式包。这一片不是页面成品。

export const patternIdList = [
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
] as const;

export type PatternId = (typeof patternIdList)[number];

export const requiredRegionList = ["primary_action", "status", "empty"] as const;

export interface PageDeclaration {
  route: string;
  id: string;
  regions: string[];
}

export class UnknownPatternError extends Error {
  readonly id: string;

  constructor(id: string) {
    super(`unknown pattern id: ${id}`);
    this.name = "UnknownPatternError";
    this.id = id;
  }
}

export class MissingRegionError extends Error {
  readonly id: string;
  readonly regions: string[];

  constructor(id: string, regions: string[]) {
    const label = id === "" ? "<empty>" : id;
    super(`pattern ${label}: missing required region ${regions.join(",")}`);
    this.name = "MissingRegionError";
    this.id = id;
    this.regions = regions;
  }
}

// 登录是商家门上的闸，没有单独的 page 文件，所以不造 /login。
// /admin 登录后仍是商家后台，不把整页记成 login。
// /c/ 是公开顾客页，不记成 workspace。
const recorded: readonly { route: string; id: PatternId; regions: readonly string[] }[] = [
  { route: "/", id: "workspace", regions: ["primary_action", "status", "empty"] },
  { route: "/admin", id: "workspace", regions: ["primary_action", "status", "empty"] },
  { route: "/admin/preview", id: "billing", regions: ["primary_action", "status", "empty"] },
  { route: "/work/stores", id: "list", regions: ["primary_action", "status", "empty"] },
  { route: "/work/campaigns", id: "list", regions: ["primary_action", "status", "empty"] },
  { route: "/work/campaigns/[id]", id: "detail", regions: ["primary_action", "status", "empty"] },
  { route: "/work/materials", id: "list", regions: ["primary_action", "status", "empty"] },
  { route: "/work/rewards", id: "billing", regions: ["primary_action", "status", "empty"] },
  // HUI-3562 店员确认与核销台:确认/核销是复核动作,记 review 模式。
  { route: "/work/service", id: "review", regions: ["primary_action", "status", "empty"] },
  { route: "/work/analytics", id: "workspace", regions: ["primary_action", "status", "empty"] },
  { route: "/c/[code]", id: "public_consumer", regions: ["primary_action", "status", "empty"] },
  { route: "/c/[code]/contact", id: "public_consumer", regions: ["primary_action", "status", "empty"] },
  // HUI-1665 稳定入口 /t/{code}:公开消费者面,off 时整页 404。
  { route: "/t/[code]", id: "public_consumer", regions: ["primary_action", "status", "empty"] },
  // HUI-1665 触点管理台:一卡一稳定链接的日常操作列表页。
  { route: "/work/touchpoints", id: "list", regions: ["primary_action", "status", "empty"] },
];

export function patternIds(): string[] {
  return [...patternIdList];
}

export function requiredRegions(): string[] {
  return [...requiredRegionList];
}

export function pageLedger(): PageDeclaration[] {
  return recorded.map((page) => ({
    route: page.route,
    id: page.id,
    regions: [...page.regions],
  }));
}

export function validateDeclaration(input: { id: string; regions?: readonly string[] }): void {
  if (!(patternIdList as readonly string[]).includes(input.id)) {
    throw new UnknownPatternError(input.id);
  }
  const have = new Set(input.regions ?? []);
  const missing = requiredRegionList.filter((region) => !have.has(region));
  if (missing.length > 0) {
    throw new MissingRegionError(input.id, [...missing]);
  }
}
