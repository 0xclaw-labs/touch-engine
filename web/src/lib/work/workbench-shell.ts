import { SERVICE_DESK_HREF, SERVICE_DESK_LABEL } from "@/lib/work/service-desk";
import { usable, type MembershipScope } from "@/lib/workspace-tenant";

export interface WorkNavItem {
  href: string;
  label: string;
  roles?: readonly string[];
}

export const WORK_PRIMARY_NAV: readonly WorkNavItem[] = [
  { href: "/", label: "今天" },
  { href: "/work/stores", label: "门店" },
  { href: "/work/campaigns", label: "推广活动" },
  { href: "/work/materials", label: "内容中心" },
  { href: "/work/analytics", label: "经营结果" },
];

export const WORK_SECONDARY_NAV: readonly WorkNavItem[] = [
  { href: "/agency", label: "代理工作台", roles: ["agent"] },
  { href: "/work/leads", label: "线索" },
  { href: "/work/rewards", label: "权益与团购" },
  { href: "/work/touchpoints", label: "跨店入口批量管理", roles: ["org_owner", "store_manager", "agent"] },
  { href: "/work/billing", label: "账单", roles: ["org_owner", "agent"] },
  { href: "/work/settings", label: "团队、权限与代理协作", roles: ["org_owner", "store_manager", "agent"] },
];

export function isNavAllowedForRole(roles: readonly string[] | undefined, role: string | undefined): boolean {
  const normalized = (role ?? "").trim();
  if (!roles || roles.length === 0) return true;
  if (!normalized) return !roles.includes("agent") || roles.length > 1;
  return roles.includes(normalized);
}

export function visibleWorkNav(input: { role?: string; serviceDesk?: boolean }): {
  primary: WorkNavItem[];
  secondary: WorkNavItem[];
} {
  const primary: WorkNavItem[] = [...WORK_PRIMARY_NAV];
  if (input.serviceDesk) {
    primary.push({ href: SERVICE_DESK_HREF, label: SERVICE_DESK_LABEL });
  }
  const secondary = WORK_SECONDARY_NAV.filter((item) => isNavAllowedForRole(item.roles, input.role));
  return { primary, secondary };
}

const ROLE_LABELS: Record<string, string> = {
  org_owner: "组织负责人",
  store_manager: "门店经理",
  staff: "店员",
  agent: "代管代理",
};

export function roleDisplayLabel(role: string | undefined): string {
  const key = (role ?? "").trim();
  return ROLE_LABELS[key] ?? "";
}

// 受权诊断只对负责人与受托代理开放；普通店员与日常操作流不暴露内部状态码与排障字段。
export function canViewDiagnostics(role: string | undefined): boolean {
  const key = (role ?? "").trim();
  return key === "org_owner" || key === "agent";
}

export interface StoreRef {
  id: string;
  name: string;
  status?: string;
}

export type StoreEntryResolution =
  | { mode: "empty"; href: string; label: string }
  | {
      mode: "auto_single";
      store: StoreRef;
      href: string;
      channelsHref: string;
      entrancesHref: string;
      contentHref: string;
      label: string;
    }
  | { mode: "multi_store"; stores: StoreRef[]; manageHref: string; label: string };

export function storeWorkspaceEntryHref(
  storeId: string,
  opts?: { panel?: "page" | "channels" | "services" | "content" | "entrances"; returnOrigin?: string },
): string {
  const encodedId = encodeURIComponent(storeId);
  const panel = opts?.panel ?? "page";
  const params = new URLSearchParams();
  if (panel === "entrances") {
    params.set("tab", "entrances");
  } else {
    params.set("tab", "page");
    if (panel !== "page") params.set("panel", panel);
  }
  const safeReturn = sanitizeWorkReturnPath(opts?.returnOrigin);
  if (safeReturn) params.set("return", safeReturn);
  return `/work/stores/${encodedId}?${params.toString()}`;
}

export function resolveStoreEntry(
  stores: readonly StoreRef[],
  opts?: { manage?: boolean; returnOrigin?: string },
): StoreEntryResolution {
  const valid = stores.filter((s) => typeof s?.id === "string" && s.id.trim() !== "");
  if (valid.length === 0) {
    return { mode: "empty", href: "/work/stores", label: "登记第一家门店" };
  }
  if (valid.length === 1 && !opts?.manage) {
    const store = valid[0];
    const safeReturn = sanitizeWorkReturnPath(opts?.returnOrigin) ?? undefined;
    return {
      mode: "auto_single",
      store,
      href: storeWorkspaceEntryHref(store.id, { panel: "page", returnOrigin: safeReturn }),
      channelsHref: storeWorkspaceEntryHref(store.id, { panel: "channels", returnOrigin: safeReturn }),
      entrancesHref: storeWorkspaceEntryHref(store.id, { panel: "entrances", returnOrigin: safeReturn }),
      contentHref: buildWorkContextHref("/work/materials", { store: store.id, returnOrigin: safeReturn }),
      label: `进入「${store.name}」工作区`,
    };
  }
  return {
    mode: "multi_store",
    stores: [...valid],
    manageHref: "/work/stores?manage=1",
    label: "选择或管理门店",
  };
}

export interface WorkReturnContext {
  store?: string;
  campaign?: string;
  draft?: string;
  returnOrigin?: string;
}

const ALLOWED_RETURN_PREFIXES = ["/", "/work/", "/agency"];

// 受控站内工作台回跳校验：允许 `/`、`/work/...`、`/agency...` 及其安全查询串和片段，
// 拒绝外域 URL、协议相对路径、反斜杠、路径穿越、`/api` 和伪编码绕过。
export function sanitizeWorkReturnPath(raw: string | null | undefined): string | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  if (!trimmed || !trimmed.startsWith("/")) return null;
  if (trimmed.startsWith("//") || trimmed.includes("\\") || /[\r\n\0]/.test(trimmed)) return null;
  const lower = trimmed.toLowerCase();
  if (lower.includes("javascript:") || lower.includes("data:") || lower.includes("%2f%2f") || lower.includes("%5c") || lower.includes("%2e%2e")) {
    return null;
  }
  const hashIndex = trimmed.indexOf("#");
  const withoutHash = hashIndex >= 0 ? trimmed.slice(0, hashIndex) : trimmed;
  const hashPart = hashIndex >= 0 ? trimmed.slice(hashIndex + 1) : "";
  if (hashPart && !/^[A-Za-z0-9_-]+$/.test(hashPart)) return null;

  const queryIndex = withoutHash.indexOf("?");
  const pathname = queryIndex >= 0 ? withoutHash.slice(0, queryIndex) : withoutHash;
  if (pathname === "/api" || pathname.startsWith("/api/")) return null;
  if (pathname.includes("..") || pathname.includes("//")) return null;
  if (!/^\/[A-Za-z0-9/_%-]*$/.test(pathname)) return null;
  try {
    const decodedPath = decodeURIComponent(pathname);
    if (decodedPath.includes("..") || decodedPath.includes("\\") || decodedPath.includes("//")) return null;
  } catch {
    return null;
  }

  const allowedPrefix =
    pathname === "/" ||
    pathname === "/work" ||
    pathname === "/agency" ||
    ALLOWED_RETURN_PREFIXES.some((prefix) => prefix !== "/" && pathname.startsWith(prefix));
  if (!allowedPrefix) return null;

  return trimmed;
}

export function buildWorkContextHref(targetPath: string, ctx: WorkReturnContext): string {
  const params = new URLSearchParams();
  if (ctx.store?.trim()) params.set("store", ctx.store.trim());
  if (ctx.campaign?.trim()) params.set("campaign", ctx.campaign.trim());
  if (ctx.draft?.trim()) params.set("draft", ctx.draft.trim());
  const safeReturn = sanitizeWorkReturnPath(ctx.returnOrigin);
  if (safeReturn) params.set("return", safeReturn);
  const query = params.toString();
  return query ? `${targetPath}?${query}` : targetPath;
}

export function readWorkReturnContext(
  search: string,
  fallbackHref: string,
): {
  storeId: string;
  campaignId: string;
  draftId: string;
  returnHref: string;
  hasExplicitReturn: boolean;
} {
  const params = new URLSearchParams(search);
  const storeId = (params.get("store") ?? "").trim();
  const campaignId = (params.get("campaign") ?? "").trim();
  const draftId = (params.get("draft") ?? "").trim();
  const safeReturn = sanitizeWorkReturnPath(params.get("return"));
  return {
    storeId,
    campaignId,
    draftId,
    returnHref: safeReturn ?? fallbackHref,
    hasExplicitReturn: safeReturn !== null,
  };
}

export function switchableTenants(
  options: readonly MembershipScope[] | null | undefined,
  currentTenantId: string,
): MembershipScope[] {
  const enabled = usable(options ? [...options] : []);
  if (enabled.length <= 1) return [];
  const current = currentTenantId.trim();
  if (!current) return enabled;
  return enabled;
}

export function isCurrentTenantPayload(requestTenantId: string, activeTenantId: string): boolean {
  const req = requestTenantId.trim();
  const active = activeTenantId.trim();
  return req !== "" && req === active;
}
