import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// HUI-4593 修复轮 5(F①):组织切换 = 身份级能力。
// /api/eco-nav 的 can_switch_tenant 终裁 —— nav authority(外部 public-ai)只看
// 当前租户上下文,authority 不可用时更是硬编码 false;BFF 按 principal 的全部
// enabled memberships(跨租户,真相在 touch-engine Go 服务)做「只放宽、不收紧」
// 的终裁:>=2 → true;读不到/无 BFF 配置 → 保持 authority 原值(fail-closed)。

const AUTHORITY_MODEL = {
  schema_version: "eco-nav/v1",
  status: { state: "ready", reasons: [] },
  brand: { display_name: "碰一碰" },
  app: { app_id: "touch-engine", display_name: "碰一碰" },
  visible_apps: [],
  work_context: { tenant_id: "tnt_A", tenant_name: "商家A" },
  role: { role_key: "staff", display_name: "店员" },
  billing: null,
  return_context: null,
  capabilities: {
    can_switch_app: false,
    can_switch_tenant: false, // authority 原判:false(只看当前租户)
    can_manage_members: false,
    can_open_billing: false,
  },
};

const env = { TOUCH_SERVER_URL: "http://go.internal:18240", TOUCH_INTERNAL_TOKEN: "bff-internal-secret" };

let membershipCount = 2;
let membershipFails = false;
let membershipsCalled = 0;
// 特殊形态用(如 disabled 计数用例):覆盖 memberships 载荷
let membershipPayload: unknown[] | null = null;

async function loadModelWith(cookie: string | null): Promise<Record<string, unknown>> {
  vi.resetModules();
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/api/v1/session/memberships")) {
      membershipsCalled += 1;
      if (membershipFails) throw new Error("membership truth unreachable");
      const items = membershipPayload ?? Array.from({ length: membershipCount }, (_, i) => ({
        tenant_id: `tnt_${i}`, display_name: `商家${i}`, role: "staff", enabled: true, source: "membership",
      }));
      return new Response(JSON.stringify({ items }), { status: 200 });
    }
    // nav authority(外部 public-ai)
    return new Response(JSON.stringify(AUTHORITY_MODEL), { status: 200 });
  }));
  const { GET } = await import("../src/app/api/eco-nav/route");
  const headers = new Headers();
  if (cookie) headers.set("cookie", cookie);
  const res = await GET(new Request("http://web.local/api/eco-nav", { headers }));
  return (await res.json()) as Record<string, unknown>;
}

beforeEach(() => {
  membershipCount = 2;
  membershipFails = false;
  membershipsCalled = 0;
  membershipPayload = null;
  vi.stubEnv("TOUCH_SERVER_URL", env.TOUCH_SERVER_URL);
  vi.stubEnv("TOUCH_INTERNAL_TOKEN", env.TOUCH_INTERNAL_TOKEN);
  vi.stubEnv("PUBLIC_AI_ECO_NAV_URL", "https://public-ai.example/eco-nav");
  vi.stubEnv("PUBLIC_AI_ECO_NAV_IDENTITY", "1");
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

describe("eco-nav can_switch_tenant 身份级终裁(修复轮 5)", () => {
  it("principal 有 2 个 enabled membership → authority 判 false 也放宽为 true", async () => {
    const model = (await loadModelWith("touch_session=tok-multi")) as {
      capabilities: { can_switch_tenant: boolean };
    };
    expect(model.capabilities.can_switch_tenant).toBe(true);
    expect(membershipsCalled).toBe(1);
  });

  it("单租户 principal → 保持 authority 原值 false(不凭空放宽)", async () => {
    membershipCount = 1;
    const model = (await loadModelWith("touch_session=tok-single")) as {
      capabilities: { can_switch_tenant: boolean };
    };
    expect(model.capabilities.can_switch_tenant).toBe(false);
  });

  it("membership 真相读不到 → 保持 authority 原值(fail-closed,绝不放宽)", async () => {
    membershipFails = true;
    const model = (await loadModelWith("touch_session=tok-multi")) as {
      capabilities: { can_switch_tenant: boolean };
    };
    expect(model.capabilities.can_switch_tenant).toBe(false);
  });

  it("disabled membership 不计入(必须 enabled)", async () => {
    membershipPayload = [
      { tenant_id: "tnt_A", enabled: true, source: "membership" },
      { tenant_id: "tnt_B", enabled: false, source: "membership" },
    ];
    const model = (await loadModelWith("touch_session=tok-x")) as {
      capabilities: { can_switch_tenant: boolean };
    };
    expect(model.capabilities.can_switch_tenant).toBe(false);
  });

  it("authority 已判 true 时保持 true(只放宽不收紧)", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/v1/session/memberships")) {
        return new Response(JSON.stringify({ items: [{ tenant_id: "tnt_A", enabled: true }] }), { status: 200 });
      }
      return new Response(JSON.stringify({
        ...AUTHORITY_MODEL,
        capabilities: { ...AUTHORITY_MODEL.capabilities, can_switch_tenant: true },
      }), { status: 200 });
    }));
    const model = (await loadModelWith("touch_session=tok-x")) as {
      capabilities: { can_switch_tenant: boolean };
    };
    expect(model.capabilities.can_switch_tenant).toBe(true);
  });
});
