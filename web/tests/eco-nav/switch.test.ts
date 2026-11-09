import { afterEach, describe, expect, it, vi } from "vitest";
import { validateLaunchUrl } from "@/lib/eco-nav/allowlist";
import { POST as SWITCH_POST } from "@/app/api/eco-nav/switch/route";
import { POST as REDEEM_POST } from "@/app/api/eco-nav/switch/redeem/route";

const originalFetch = global.fetch;

afterEach(() => {
  global.fetch = originalFetch;
  delete process.env.TOUCH_SERVER_URL;
  delete process.env.TOUCH_INTERNAL_TOKEN;
});

function request(url: string, body: unknown, headers: Record<string, string> = {}) {
  return new Request(url, {
    method: "POST",
    headers: { "content-type": "application/json", cookie: "touch_session=sess", "x-tenant-id": "tnt_a", ...headers },
    body: JSON.stringify(body),
  });
}

const mintUrl = "http://127.0.0.1:18462/api/eco-nav/switch";
const redeemUrl = "http://127.0.0.1:18462/api/eco-nav/switch/redeem";

describe("validated launch_url", () => {
  it("accepts only the registered target's origin and path prefix", () => {
    expect(validateLaunchUrl("ti-goboost-web", "https://goboost.modelxing.com/projects?code=one")).toBe(
      "https://goboost.modelxing.com/projects?code=one",
    );
    expect(validateLaunchUrl("ti-goboost-web", "https://evil.example/projects?code=one")).toBeNull();
    expect(validateLaunchUrl("ti-goboost-web", "https://goboost.modelxing.com/elsewhere")).toBeNull();
    expect(validateLaunchUrl("ti-goboost-web", "http://goboost.modelxing.com/projects")).toBeNull();
    expect(validateLaunchUrl("ti-goboost-web", "https://goboost.modelxing.com/projects?campaign_id=cmp_1")).toBeNull();
    expect(validateLaunchUrl("ti-unregistered", "https://goboost.modelxing.com/projects")).toBeNull();
  });
});

describe("POST /api/eco-nav/switch (mint)", () => {
  it("answers a jump_token and never a launch_url", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    const calls: { url: string; init: RequestInit }[] = [];
    global.fetch = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), init: init ?? {} });
      return new Response(
        JSON.stringify({ jump_token: "jtg_abc.def", grant_id: "jgr_1", expires_at: "2026-11-10T00:01:00Z" }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }) as typeof fetch;

    const res = await SWITCH_POST(request(mintUrl, { app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(res.status).toBe(200);
    const body = (await res.json()) as Record<string, unknown>;
    expect(body.jump_token).toBe("jtg_abc.def");
    expect(body).not.toHaveProperty("launch_url");
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("http://127.0.0.1:18240/api/v1/eco-nav/switch");
    const sent = JSON.parse(String(calls[0].init.body)) as Record<string, unknown>;
    expect(sent).toEqual({ app_id: "goboost", launch_target_id: "ti-goboost-web" });
    const headers = new Headers(calls[0].init.headers);
    expect(headers.get("cookie")).toBe("touch_session=sess");
    expect(headers.get("x-tenant-id")).toBe("tnt_a");
    expect(headers.get("x-internal-token")).toBe("internal");
  });

  it("rejects extra payload fields (no campaign/asset/payer on a nav switch)", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    const res = await SWITCH_POST(
      request(mintUrl, { app_id: "goboost", launch_target_id: "ti-goboost-web", campaign_id: "cmp_1" }),
    );
    expect(res.status).toBe(400);
    await expect(res.json()).resolves.toMatchObject({ error: "bad_request" });
  });

  it("fails closed when the upstream is missing", async () => {
    delete process.env.TOUCH_SERVER_URL;
    const missing = await SWITCH_POST(request(mintUrl, { app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(missing.status).toBe(503);
  });

  it("relays a denied upstream as an explicit failure, not a fake URL", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    global.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ error: "shell_launch_denied" }), {
        status: 403,
        headers: { "content-type": "application/json" },
      })) as typeof fetch;
    const res = await SWITCH_POST(request(mintUrl, { app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(res.status).toBe(403);
    await expect(res.json()).resolves.toMatchObject({ error: "launch_denied" });
  });

  it("rejects a mint upstream that still tries to hand out a launch_url", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    global.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ launch_url: "https://goboost.modelxing.com/projects?code=one" }), {
        status: 200,
        headers: { "content-type": "application/json" },
      })) as typeof fetch;
    const res = await SWITCH_POST(request(mintUrl, { app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(res.status).toBe(502);
    const body = (await res.json()) as Record<string, unknown>;
    expect(body).not.toHaveProperty("launch_url");
  });
});

describe("POST /api/eco-nav/switch/redeem", () => {
  it("forwards only jump_token and releases the validated launch_url once", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    const calls: { url: string; init: RequestInit }[] = [];
    global.fetch = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), init: init ?? {} });
      return new Response(
        JSON.stringify({
          launch_url: "https://goboost.modelxing.com/projects?code=one-time",
          app_id: "goboost",
          launch_target_id: "ti-goboost-web",
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }) as typeof fetch;

    const res = await REDEEM_POST(request(redeemUrl, { jump_token: "jtg_abc.def" }));
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({
      launch_url: "https://goboost.modelxing.com/projects?code=one-time",
    });
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("http://127.0.0.1:18240/api/v1/eco-nav/switch/redeem");
    const sent = JSON.parse(String(calls[0].init.body)) as Record<string, unknown>;
    expect(sent).toEqual({ jump_token: "jtg_abc.def" });
    expect(new Headers(calls[0].init.headers).get("cookie")).toBe("touch_session=sess");
  });

  it("fails closed when the redeemed URL is not the registered target", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    global.fetch = vi.fn(async () =>
      new Response(
        JSON.stringify({
          launch_url: "https://evil.example/phish",
          app_id: "goboost",
          launch_target_id: "ti-goboost-web",
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      )) as typeof fetch;
    const res = await REDEEM_POST(request(redeemUrl, { jump_token: "jtg_abc.def" }));
    expect(res.status).toBe(502);
    await expect(res.json()).resolves.toMatchObject({ error: "launch_target_mismatch" });
  });

  it("relays honest expiry/invalid/availability failures without inventing a URL", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    const upstream = (status: number, error: string) =>
      vi.fn(async () =>
        new Response(JSON.stringify({ error }), { status, headers: { "content-type": "application/json" } }),
      ) as typeof fetch;

    global.fetch = upstream(410, "jump_expired");
    const gone = await REDEEM_POST(request(redeemUrl, { jump_token: "jtg_x.y" }));
    expect(gone.status).toBe(410);
    const goneBody = (await gone.json()) as Record<string, unknown>;
    expect(goneBody).not.toHaveProperty("launch_url");

    global.fetch = upstream(404, "not_found");
    const invalid = await REDEEM_POST(request(redeemUrl, { jump_token: "jtg_x.y" }));
    expect(invalid.status).toBe(404);
    const invalidBody = (await invalid.json()) as Record<string, unknown>;
    expect(invalidBody).not.toHaveProperty("launch_url");

    global.fetch = upstream(503, "jump_unavailable");
    const down = await REDEEM_POST(request(redeemUrl, { jump_token: "jtg_x.y" }));
    expect(down.status).toBe(503);
    const downBody = (await down.json()) as Record<string, unknown>;
    expect(downBody).not.toHaveProperty("launch_url");
  });
});
