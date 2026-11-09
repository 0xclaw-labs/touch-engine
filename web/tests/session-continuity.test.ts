import { describe, expect, it, vi } from "vitest";
import { createScopeReader } from "@/lib/session-continuity";

// HUI-2222 平台身份复用:access token 过期(401)先用 refresh cookie 静默续期
// 一次再重试 memberships;续期仍被拒才落匿名(登录表单)。单飞保证并发 401
// 只触发一次 refresh;网络错误不当已登录也不当匿名(保持 error 态诚实)。

type FetchMock = (url: string, init?: RequestInit) => Promise<Response>;

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function scopeReaderWith(fetchImpl: FetchMock) {
  return createScopeReader(fetchImpl as typeof fetch);
}

describe("createScopeReader", () => {
  it("passes memberships through when the session is alive", async () => {
    const calls: string[] = [];
    const read = scopeReaderWith(async (url) => {
      calls.push(String(url));
      return jsonResponse(200, { items: [{ tenant_id: "tnt_a" }] });
    });
    const items = await read();
    expect(items).toEqual([{ tenant_id: "tnt_a" }]);
    expect(calls).toEqual(["/api/session/memberships"]);
  });

  it("refreshes once on 401 and retries the memberships read", async () => {
    const calls: string[] = [];
    let membershipsHit = 0;
    const read = scopeReaderWith(async (url) => {
      calls.push(String(url));
      if (String(url) === "/api/session/memberships") {
        membershipsHit += 1;
        if (membershipsHit === 1) return jsonResponse(401, { error: "unauthenticated" });
        return jsonResponse(200, { items: [{ tenant_id: "tnt_a" }] });
      }
      return jsonResponse(200, { refreshed: true });
    });
    const items = await read();
    expect(items).toEqual([{ tenant_id: "tnt_a" }]);
    expect(calls).toEqual(["/api/session/memberships", "/api/auth/refresh", "/api/session/memberships"]);
  });

  it("answers null (anonymous) when the refresh is also rejected", async () => {
    const read = scopeReaderWith(async (url) => {
      if (String(url) === "/api/session/memberships") return jsonResponse(401, { error: "unauthenticated" });
      return jsonResponse(401, { error: "unauthenticated" });
    });
    expect(await read()).toBeNull();
  });

  it("answers null when the retried memberships read is still 401", async () => {
    const read = scopeReaderWith(async (url) => jsonResponse(401, { error: "unauthenticated" }));
    expect(await read()).toBeNull();
  });

  it("never fires the refresh more than once per reader (single-flight)", async () => {
    let refreshHits = 0;
    let membershipsHit = 0;
    const read = scopeReaderWith(async (url) => {
      if (String(url) === "/api/session/memberships") {
        membershipsHit += 1;
        if (membershipsHit === 1) return jsonResponse(401, { error: "unauthenticated" });
        return jsonResponse(200, { items: [] });
      }
      refreshHits += 1;
      // 慢续期:并发调用都会撞上同一个 in-flight promise。
      await new Promise((r) => setTimeout(r, 10));
      return jsonResponse(200, { refreshed: true });
    });
    const [a, b, c] = await Promise.all([read(), read(), read()]);
    expect(a).toEqual([]);
    expect(b).toEqual([]);
    expect(c).toEqual([]);
    expect(refreshHits).toBe(1);
  });

  it("throws on network failure (neither logged-in nor anonymous)", async () => {
    const read = scopeReaderWith(async () => {
      throw new TypeError("network down");
    });
    await expect(read()).rejects.toThrow();
  });

  it("throws when the refresh itself fails on transport (honest error state)", async () => {
    let membershipsHit = 0;
    const read = scopeReaderWith(async (url) => {
      if (String(url) === "/api/session/memberships") {
        membershipsHit += 1;
        if (membershipsHit === 1) return jsonResponse(401, { error: "unauthenticated" });
        throw new TypeError("network down");
      }
      return jsonResponse(200, { refreshed: true });
    });
    await expect(read()).rejects.toThrow();
  });
});
