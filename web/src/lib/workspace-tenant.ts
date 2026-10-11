// 正式商家与后台的当前组织只从受控成员列表解析。
// 手填的编号、停用成员和非成员来源都不能成为当前组织。

export type MembershipScope = {
  tenant_id: string;
  display_name: string;
  role: string;
  enabled: boolean;
  source: string;
};

export type WorkspaceTenant =
  | { status: "selected"; tenantId: string; displayName: string; role: string }
  | { status: "choose"; options: MembershipScope[] }
  | { status: "none" };

function usable(scopes: MembershipScope[] | null | undefined): MembershipScope[] {
  const seen = new Set<string>();
  const out: MembershipScope[] = [];
  for (const scope of scopes ?? []) {
    const id = scope?.tenant_id?.trim() ?? "";
    if (!scope?.enabled || scope.source !== "membership" || id === "" || seen.has(id)) continue;
    seen.add(id);
    out.push({ ...scope, tenant_id: id, display_name: scope.display_name?.trim() || "当前组织" });
  }
  return out;
}

export const TENANT_STORAGE_KEY = "touch_admin_tenant";

export type TenantMemory = {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
};

export type TenantConfirm = "stale" | "accept" | "reject";

let tenantOp = 0;

export function beginTenantOp(): number {
  tenantOp += 1;
  return tenantOp;
}

export function currentTenantOp(): number {
  return tenantOp;
}

export function decideTenantConfirm(input: {
  seq: number;
  whoamiOk: boolean;
  enabled: unknown;
  role: unknown;
  tenantId: string;
  items: MembershipScope[] | null | undefined;
}): TenantConfirm {
  if (input.seq !== tenantOp) return "stale";
  const role = typeof input.role === "string" ? input.role.trim() : "";
  const id = input.tenantId.trim();
  const listed = (input.items ?? []).some((item) => item.tenant_id === id && item.enabled === true && item.source === "membership");
  if (!input.whoamiOk || input.enabled !== true || role === "" || !listed) return "reject";
  return "accept";
}

export function writeTenantMemory(decision: TenantConfirm, tenantId: string, memory: TenantMemory): void {
  if (decision === "stale") return;
  if (decision === "accept") {
    memory.setItem(TENANT_STORAGE_KEY, tenantId);
    return;
  }
  if (memory.getItem(TENANT_STORAGE_KEY) === tenantId) memory.removeItem(TENANT_STORAGE_KEY);
}

export function resolveWorkspaceTenant(
  scopes: MembershipScope[] | null | undefined,
  remembered?: string | null,
): WorkspaceTenant {
  const options = usable(scopes);
  const rememberedID = remembered?.trim() ?? "";
  const restored = options.find((item) => item.tenant_id === rememberedID);
  const picked = restored ?? (options.length === 1 ? options[0] : undefined);
  if (picked) {
    return {
      status: "selected",
      tenantId: picked.tenant_id,
      displayName: picked.display_name,
      role: picked.role,
    };
  }
  if (options.length === 0) return { status: "none" };
  return { status: "choose", options };
}
