import { NextResponse } from "next/server";
import { bffConfigProblems, readBffEnv } from "@/lib/bff";
import { ecoNavEndpoint, ecoNavIdentityEnabled } from "@/lib/eco-nav/env";
import { loadEcoNavModel } from "@/lib/eco-nav/load";

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  // Forward the merchant session server-side: the authority decides brand,
  // tenant and launchable apps. Missing config/upstream returns a blocked
  // model (no fixture) from loadEcoNavModel.
  const headers = new Headers();
  const cookie = request.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);
  const tenant = request.headers.get("x-tenant-id");
  if (tenant) headers.set("x-tenant-id", tenant);
  const model = await loadEcoNavModel({
    endpoint: ecoNavEndpoint(),
    identityEnabled: ecoNavIdentityEnabled(),
    headers,
  });

  // HUI-4593 修复轮 5(F①):组织切换 = 身份级能力 —— principal 在 >1 租户有
  // enabled membership 即可切换,与当前租户内的角色/权限无关(nav authority 只看
  // 当前租户上下文,authority 不可用时更是硬编码 false)。membership 真相在
  // touch-engine Go 服务(/api/v1/session/memberships,身份级、无租户头);
  // 这里按它对能力做「只放宽、不收紧」的终裁。count 读取失败/无 BFF 配置时
  // 保持 authority 原值(fail-closed,绝不凭空放宽)。
  const widened = await widenTenantSwitchCapability(model, cookie);
  return NextResponse.json(widened);
}

async function widenTenantSwitchCapability(
  model: Awaited<ReturnType<typeof loadEcoNavModel>>,
  cookie: string | null,
): Promise<unknown> {
  try {
    const env = readBffEnv(process.env);
    if (bffConfigProblems(env).length > 0) return model;
    const res = await fetch(env.serverUrl.replace(/\/+$/, "") + "/api/v1/session/memberships", {
      headers: {
        "x-internal-token": env.internalToken,
        ...(cookie ? { cookie } : {}),
      },
      cache: "no-store",
    });
    if (!res.ok) return model;
    const data = (await res.json().catch(() => ({}))) as { items?: { enabled?: boolean }[] };
    const enabled = (data.items ?? []).filter((m) => m?.enabled === true).length;
    if (enabled < 2) return model;
    return {
      ...model,
      capabilities: { ...model.capabilities, can_switch_tenant: true },
    };
  } catch {
    return model; // membership 真相读不到 = 不放宽(fail-closed)
  }
}
