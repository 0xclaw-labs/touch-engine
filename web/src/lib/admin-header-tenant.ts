// 后台页眉的组织名。whoami 的 tenant_name 有字就用它；
// 没有就用本次成员列表里这个租户的 display_name。
// 两边都空时保留已经得到的非空名称，仍空才交给页眉写成「当前组织」。
// 不回退到租户编号。

type HeaderTenantMember = {
  tenant_id?: string | null;
  display_name?: string | null;
};

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function memberDisplayName(members: readonly HeaderTenantMember[] | null | undefined, tenantId: string): string {
  const id = text(tenantId);
  if (!id) return "";
  for (const item of members ?? []) {
    if (text(item?.tenant_id) !== id) continue;
    const name = text(item?.display_name);
    if (name) return name;
  }
  return "";
}

export function adminHeaderTenantName(input: {
  whoamiName: unknown;
  members?: readonly HeaderTenantMember[] | null;
  tenantId: string;
  kept?: unknown;
}): string {
  const fromWhoami = text(input.whoamiName);
  if (fromWhoami) return fromWhoami;
  const fromMember = memberDisplayName(input.members, input.tenantId);
  if (fromMember) return fromMember;
  return text(input.kept);
}
