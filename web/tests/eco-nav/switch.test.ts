import { afterEach, describe, expect, it, vi } from "vitest";
import { validateLaunchUrl } from "@/lib/eco-nav/allowlist";
import { POST } from "@/app/api/eco-nav/switch/route";

const originalFetch = global.fetch;

afterEach(() => {
  global.fetch = originalFetch;
  delete process.env.TOUCH_SERVER_URL;
  delete process.env.TOUCH_INTERNAL_TOKEN;
});

function request(body: unknown, headers: Record<string, string> = {}) {
  return new Request("http://127.0.0.1:18462/api/eco-nav/switch", {
    method: "POST",
    headers: { "content-type": "application/json", cookie: "touch_session=sess", "x-tenant-id": "tnt_a", ...headers },
    body: JSON.stringify(body),
  });
}

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

describe("POST /api/eco-nav/switch", () => {
  it("forwards only the two ids with session headers and validates the answer", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    const calls: { url: string; init: RequestInit }[] = [];
    global.fetch = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), init: init ?? {} });
      return new Response(JSON.stringify({ launch_url: "https://goboost.modelxing.com/projects?code=one" }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }) as typeof fetch;

    const res = await POST(request({ app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ launch_url: "https://goboost.modelxing.com/projects?code=one" });
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
    const res = await POST(request({ app_id: "goboost", launch_target_id: "ti-goboost-web", campaign_id: "cmp_1" }));
    expect(res.status).toBe(400);
    await expect(res.json()).resolves.toMatchObject({ error: "bad_request" });
  });

  it("fails closed when the upstream is missing or the URL is not the registered target", async () => {
    delete process.env.TOUCH_SERVER_URL;
    const missing = await POST(request({ app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(missing.status).toBe(503);

    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    global.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ launch_url: "https://evil.example/phish" }), {
        status: 200,
        headers: { "content-type": "application/json" },
      })) as typeof fetch;
    const mismatch = await POST(request({ app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(mismatch.status).toBe(502);
    await expect(mismatch.json()).resolves.toMatchObject({ error: "launch_target_mismatch" });
  });

  it("relays a denied upstream as an explicit failure, not a fake URL", async () => {
    process.env.TOUCH_SERVER_URL = "http://127.0.0.1:18240";
    process.env.TOUCH_INTERNAL_TOKEN = "internal";
    global.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ error: "shell_launch_denied" }), {
        status: 403,
        headers: { "content-type": "application/json" },
      })) as typeof fetch;
    const res = await POST(request({ app_id: "goboost", launch_target_id: "ti-goboost-web" }));
    expect(res.status).toBe(403);
    await expect(res.json()).resolves.toMatchObject({ error: "launch_denied" });
  });
});
