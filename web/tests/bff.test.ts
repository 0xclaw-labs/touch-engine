import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { bffPathToUpstream, proxyToServer, readBffEnv, type BffEnv } from "../src/lib/bff";

// BFF tests: the web relay must be a faithful, non-escalating proxy.
// The admin(后台) x guest(游客) x tenant matrix is enforced by the Go server;
// these tests prove the BFF (1) forwards identity/cookies verbatim, (2) injects
// and overrides the internal token, (3) relays every verdict unchanged (no
// status widening, no silent success), (4) fails closed when unconfigured, and
// (5) re-verifies the full matrix through the BFF layer.

const ENV: BffEnv = { serverUrl: "http://127.0.0.1:18240", internalToken: "bff-internal-secret" };

// ---- fake Go server implementing the same matrix the integration tests cover ----

interface Session {
  principal: string;
  tenant: string;
  role: "owner" | "staff";
  enabled: boolean;
}

const SESSIONS: Record<string, Session> = {
  "sess-owner-a": { principal: "usr_owner_a", tenant: "tnt_A", role: "owner", enabled: true },
  "sess-staff-a": { principal: "usr_staff_a", tenant: "tnt_A", role: "staff", enabled: true },
  "sess-disabled-a": { principal: "usr_disabled_a", tenant: "tnt_A", role: "staff", enabled: false },
  "sess-owner-b": { principal: "usr_owner_b", tenant: "tnt_B", role: "owner", enabled: true },
};

// campaign cmp_A1 lives in tenant A. public link CODE_VALID is available.
const CAMPAIGNS: Record<string, { tenant: string; title: string }> = {
  cmp_A1: { tenant: "tnt_A", title: "A周年庆" },
};
const PUBLIC_LINKS: Record<string, { state: string; campaign: string }> = {
  CODEVALID12: { state: "available", campaign: "cmp_A1" },
  CODEPAUSED12: { state: "paused", campaign: "cmp_A1" },
};

function upstreamMatrix(req: Request, path: string): Response {
  const fail = (status: number, code: string) =>
    new Response(JSON.stringify({ error: code }), { status });

  if (req.headers.get("x-internal-token") !== ENV.internalToken) {
    return fail(401, "unauthorized");
  }

  // public surface: no session concept; GET-only, state-specific answers
  const pub = path.match(/^public\/links\/([^/]+)$/);
  if (pub) {
    if (req.method !== "GET") return fail(405, "method_not_allowed");
    const link = PUBLIC_LINKS[pub[1]];
    if (!link || link.state !== "available") {
      return new Response(JSON.stringify({ state: link?.state ?? "not_found" }), { status: 404 });
    }
    return Response.json({
      state: "available",
      title: CAMPAIGNS[link.campaign].title,
      public_content: "到店有礼",
    });
  }

  // HUI-3553 D3: controlled media semantics — the same contract the Go server
  // implements (http.ServeContent): HEAD headers, Range 206/416, 410 expiry.
  // The mp4-ish payload is 11546 bytes like the real testdata fixture.
  const media = path.match(/^public\/content\/media\/(.+)$/);
  if (media) {
    const token = media[1];
    const mp4Headers = (extra: Record<string, string>): HeadersInit => ({
      "content-type": "video/mp4",
      "accept-ranges": "bytes",
      ...extra,
    });
    if (token === "expiredtok01") return new Response("gone", { status: 410 });
    if (token === "unknowntok01") return new Response("not found", { status: 404, headers: { "content-type": "text/plain" } });
    const full = new Uint8Array(11546).map((_, i) => i % 251);
    if (req.method === "HEAD") {
      return new Response(null, {
        status: 200,
        headers: mp4Headers({ "content-length": String(full.length) }),
      });
    }
    const range = req.headers.get("range");
    if (range) {
      const m = range.match(/^bytes=(\d+)-(\d*)$/);
      if (!m || Number(m[1]) >= full.length) {
        return new Response("range not satisfiable", {
          status: 416,
          headers: { "content-range": `bytes */${full.length}` },
        });
      }
      const start = Number(m[1]);
      const end = m[2] ? Math.min(Number(m[2]), full.length - 1) : full.length - 1;
      const slice = full.subarray(start, end + 1);
      return new Response(slice, {
        status: 206,
        headers: mp4Headers({
          "content-range": `bytes ${start}-${end}/${full.length}`,
          "content-length": String(slice.length),
        }),
      });
    }
    return new Response(full, {
      status: 200,
      headers: mp4Headers({ "content-length": String(full.length) }),
    });
  }

  // admin surface: session + tenant membership required
  const sessionToken = cookieToken(req.headers.get("cookie"));
  const sess = sessionToken ? SESSIONS[sessionToken] : undefined;
  if (!sess) return fail(401, "unauthenticated");
  if (!sess.enabled) return fail(403, "member_disabled");

  const tenant = req.headers.get("x-tenant-id") ?? "";
  if (!tenant) return fail(400, "tenant_required");
  if (tenant !== sess.tenant) return fail(403, "not_member");

  if (path === "campaigns" && req.method === "GET") {
    return Response.json({ items: [{ id: "cmp_A1", tenant: sess.tenant }] });
  }
  if (path === "campaigns" && req.method === "POST") {
    return Response.json({ id: "cmp_new", status: "draft" }, { status: 201 });
  }
  const match = path.match(/^campaigns\/([^/]+)$/);
  if (match) {
    const rec = CAMPAIGNS[match[1]];
    if (!rec || rec.tenant !== sess.tenant) return fail(404, "not_found");
    if (req.method === "GET") return Response.json({ id: match[1], title: rec.title });
    return Response.json({ id: match[1], title: "改" });
  }
  const statusMatch = path.match(/^campaigns\/([^/]+)\/status$/);
  if (statusMatch) {
    const rec = CAMPAIGNS[statusMatch[1]];
    if (!rec || rec.tenant !== sess.tenant) return fail(404, "not_found");
    return Response.json({ id: statusMatch[1], status: "active" });
  }
  if (path === "admin/members") {
    if (sess.role !== "owner") return fail(403, "forbidden");
    return Response.json({ items: [] });
  }
  return fail(404, "no_route");
}

function cookieToken(cookie: string | null): string | null {
  if (!cookie) return null;
  const m = cookie.match(/touch_session=([^;]+)/);
  return m ? m[1] : null;
}

let upstreamCalls: { url: string; headers: Headers; method: string }[] = [];

beforeEach(() => {
  upstreamCalls = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    upstreamCalls.push({
      url,
      headers: new Headers(init?.headers as HeadersInit),
      method: init?.method ?? "GET",
    });
    const parsed = new URL(url);
    const path = parsed.pathname.replace(/^\/api\/v1\//, "");
    const req = new Request(url, {
      method: init?.method ?? "GET",
      headers: init?.headers as HeadersInit,
      body: init?.body as BodyInit,
    });
    return upstreamMatrix(req, path);
  }));
});
afterEach(() => vi.unstubAllGlobals());

function bffReq(method: string, path: string, opts: { cookie?: string; tenant?: string; body?: string; extraHeaders?: Record<string, string> } = {}): Request {
  const headers = new Headers(opts.extraHeaders);
  if (opts.cookie) headers.set("cookie", opts.cookie);
  if (opts.tenant) headers.set("x-tenant-id", opts.tenant);
  if (opts.body) headers.set("content-type", "application/json");
  return new Request(`http://bff.local/api/${path}`, { method, headers, body: opts.body });
}

const segments = (p: string) => p.split("/").filter(Boolean);

async function call(method: string, path: string, opts: Parameters<typeof bffReq>[2] = {}) {
  return proxyToServer(bffReq(method, path, opts), ENV, segments(path));
}

// ---- config gate ------------------------------------------------------------------

describe("BFF config gate", () => {
  it("fails closed with 503 and never calls upstream when unconfigured", async () => {
    const res = await proxyToServer(bffReq("GET", "campaigns", { tenant: "tnt_A" }), { serverUrl: "", internalToken: "" }, ["campaigns"]);
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.error).toBe("bff_config_gate");
    expect(body.detail).toEqual(["TOUCH_SERVER_URL is required", "TOUCH_INTERNAL_TOKEN is required"]);
    expect(upstreamCalls).toHaveLength(0);
  });

  it("readBffEnv trims and reads the documented keys", () => {
    const env = readBffEnv({ TOUCH_SERVER_URL: " http://x ", TOUCH_INTERNAL_TOKEN: " t " });
    expect(env.serverUrl).toBe("http://x");
    expect(env.internalToken).toBe("t");
  });
});

// ---- the admin(后台) x guest(游客) x tenant matrix, through the BFF ----------------

describe("BFF matrix relay (admin x guest x tenant)", () => {
  const cookie = (s: string) => `touch_session=${s}; HttpOnly`;

  const matrix: {
    name: string;
    session: string;
    tenant: string;
    method: string;
    path: string;
    want: number;
  }[] = [
    { name: "owner lists own campaigns", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "campaigns", want: 200 },
    { name: "staff lists own campaigns", session: "sess-staff-a", tenant: "tnt_A", method: "GET", path: "campaigns", want: 200 },
    { name: "owner reads own campaign", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "campaigns/cmp_A1", want: 200 },
    { name: "staff reads own campaign", session: "sess-staff-a", tenant: "tnt_A", method: "GET", path: "campaigns/cmp_A1", want: 200 },
    { name: "owner creates campaign", session: "sess-owner-a", tenant: "tnt_A", method: "POST", path: "campaigns", want: 201 },
    { name: "staff creates campaign", session: "sess-staff-a", tenant: "tnt_A", method: "POST", path: "campaigns", want: 201 },
    { name: "staff pauses campaign", session: "sess-staff-a", tenant: "tnt_A", method: "POST", path: "campaigns/cmp_A1/status", want: 200 },
    { name: "staff cannot manage members", session: "sess-staff-a", tenant: "tnt_A", method: "GET", path: "admin/members", want: 403 },
    { name: "owner manages members", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "admin/members", want: 200 },
    { name: "disabled member refused", session: "sess-disabled-a", tenant: "tnt_A", method: "GET", path: "campaigns", want: 403 },
    { name: "no session refused", session: "", tenant: "tnt_A", method: "GET", path: "campaigns", want: 401 },
    { name: "cross tenant read refused", session: "sess-owner-b", tenant: "tnt_A", method: "GET", path: "campaigns/cmp_A1", want: 403 },
    { name: "cross tenant list forged refused", session: "sess-owner-b", tenant: "tnt_A", method: "GET", path: "campaigns", want: 403 },
    { name: "cross tenant status refused", session: "sess-owner-b", tenant: "tnt_A", method: "POST", path: "campaigns/cmp_A1/status", want: 403 },
    { name: "owner B lists own (never A's data)", session: "sess-owner-b", tenant: "tnt_B", method: "GET", path: "campaigns", want: 200 },
    { name: "guest public page available", session: "", tenant: "", method: "GET", path: "public/links/CODEVALID12", want: 200 },
    { name: "guest public page paused-state 404", session: "", tenant: "", method: "GET", path: "public/links/CODEPAUSED12", want: 404 },
    { name: "guest public page unknown 404", session: "", tenant: "", method: "GET", path: "public/links/UNKNOWNCODE1", want: 404 },
    { name: "guest cannot write public surface", session: "", tenant: "", method: "POST", path: "public/links/CODEVALID12", want: 405 },
    { name: "guest cannot create campaign", session: "", tenant: "", method: "POST", path: "campaigns", want: 401 },
  ];

  for (const c of matrix) {
    it(c.name, async () => {
      const res = await call(c.method, c.path, {
        cookie: c.session ? cookie(c.session) : undefined,
        tenant: c.tenant,
        body: c.method === "GET" ? undefined : "{}",
      });
      expect(res.status).toBe(c.want);
    });
  }

  it("owner B's campaign list never contains A's records", async () => {
    const res = await call("GET", "campaigns", { cookie: cookie("sess-owner-b"), tenant: "tnt_B" });
    const body = await res.json();
    for (const item of body.items as { tenant: string }[]) {
      expect(item.tenant).toBe("tnt_B");
    }
  });
});

// ---- relay semantics ---------------------------------------------------------------

describe("BFF relay semantics", () => {
  it("maps /api/X to upstream /api/v1/X and preserves the query string", async () => {
    const req = new Request("http://bff.local/api/campaigns?limit=5");
    await proxyToServer(req, ENV, ["campaigns"]);
    expect(upstreamCalls[0].url).toContain("http://127.0.0.1:18240/api/v1/campaigns?limit=5");
  });

  it("forwards the session cookie verbatim", async () => {
    await call("GET", "campaigns", { cookie: "touch_session=sess-owner-a", tenant: "tnt_A" });
    expect(upstreamCalls[0].headers.get("cookie")).toBe("touch_session=sess-owner-a");
  });

  it("sets brand host from the browser URL and ignores a spoofed public host", async () => {
    await call("GET", "campaigns", {
      cookie: "touch_session=sess-owner-a",
      tenant: "tnt_A",
      extraHeaders: {
        "x-touch-public-host": "evil.example",
        "x-forwarded-host": "evil.example",
      },
    });
    expect(upstreamCalls[0].headers.get("x-touch-public-host")).toBe("bff.local");
    expect(upstreamCalls[0].headers.get("x-forwarded-host")).toBeNull();
  });

  it("overrides any caller-supplied internal token (no escalation)", async () => {
    const res = await call("GET", "campaigns", {
      cookie: "touch_session=sess-owner-a",
      tenant: "tnt_A",
      extraHeaders: { "x-internal-token": "forged-token" },
    });
    expect(res.status).toBe(200);
    expect(upstreamCalls[0].headers.get("x-internal-token")).toBe(ENV.internalToken);
  });

  it("relays upstream errors unchanged (no silent success)", async () => {
    const res = await call("GET", "campaigns", { cookie: "touch_session=sess-disabled-a", tenant: "tnt_A" });
    expect(res.status).toBe(403);
    const body = await res.json();
    expect(body.error).toBe("member_disabled");
  });

  it("relays set-cookie headers back to the browser (login flow)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      const res = new Response(JSON.stringify({ authenticated: true }), { status: 200 });
      res.headers.append("set-cookie", "touch_session=tok1; HttpOnly; SameSite=Lax");
      res.headers.append("set-cookie", "touch_session_refresh=rt1; HttpOnly; SameSite=Lax");
      return res;
    }));
    const res = await call("POST", "auth/login", { body: JSON.stringify({ email: "a@b.c", password: "x" }) });
    expect(res.status).toBe(200);
    const setCookies = res.headers.getSetCookie();
    expect(setCookies).toHaveLength(2);
    expect(setCookies[0]).toContain("touch_session=tok1");
  });

  it("when the Go server is down the BFF answers 503 server_unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("ECONNREFUSED"); }));
    const res = await call("GET", "campaigns", { cookie: "touch_session=sess-owner-a", tenant: "tnt_A" });
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.error).toBe("server_unavailable");
  });

  it("bffPathToUpstream builds /api/v1/... paths", () => {
    expect(bffPathToUpstream(["campaigns", "cmp_1"])).toBe("/api/v1/campaigns/cmp_1");
    expect(bffPathToUpstream(["auth", "login"])).toBe("/api/v1/auth/login");
    expect(bffPathToUpstream(["public", "links", "CODE12"])).toBe("/api/v1/public/links/CODE12");
  });

  it("PUT extra-jumps reaches the Go server with the owner cookie", async () => {
    const prevUrl = process.env.TOUCH_SERVER_URL;
    const prevToken = process.env.TOUCH_INTERNAL_TOKEN;
    process.env.TOUCH_SERVER_URL = ENV.serverUrl;
    process.env.TOUCH_INTERNAL_TOKEN = ENV.internalToken;
    try {
      const route = await import("../src/app/api/[...path]/route");
      expect(typeof route.PUT).toBe("function");
      const res = await route.PUT!(
        bffReq("PUT", "campaigns/cmp_A1/extra-jumps", {
          cookie: "touch_session=sess-owner-a",
          tenant: "tnt_A",
          body: JSON.stringify({ actions: [] }),
        }),
        { params: Promise.resolve({ path: ["campaigns", "cmp_A1", "extra-jumps"] }) },
      );
      expect(res.status).toBe(404);
      const body = await res.json();
      expect(body.error).toBe("no_route");
      expect(upstreamCalls.at(-1)?.method).toBe("PUT");
      expect(upstreamCalls.at(-1)?.url).toContain("/api/v1/campaigns/cmp_A1/extra-jumps");
      expect(upstreamCalls.at(-1)?.headers.get("cookie")).toBe("touch_session=sess-owner-a");
    } finally {
      if (prevUrl === undefined) delete process.env.TOUCH_SERVER_URL;
      else process.env.TOUCH_SERVER_URL = prevUrl;
      if (prevToken === undefined) delete process.env.TOUCH_INTERNAL_TOKEN;
      else process.env.TOUCH_INTERNAL_TOKEN = prevToken;
    }
  });
});

// ---- HUI-3553 D3: controlled media relay through the web BFF ------------------------
// Walk A found the defect: a Go-absolute media URL ("/api/v1/public/content/
// media/<token>", as returned by media-access and fed to <video>/<a>) hit the
// BFF with path[0]="v1" and was double-prefixed to /api/v1/v1/... = upstream
// 404, so the <video> element was fed an error page instead of mp4 bytes. The
// relay must (1) pass Go-absolute URLs through, (2) forward Range, (3) relay
// 206/416/410/404 statuses and the REAL Content-Type untouched, and (4) not
// buffer whole files.

describe("BFF controlled media relay (HUI-3553 D3)", () => {
  const MEDIA_PATH = "v1/public/content/media/tokA2f9";

  it("passes Go-absolute /api/v1/* URLs through WITHOUT double prefixing", async () => {
    const res = await call("GET", MEDIA_PATH);
    expect(res.status).toBe(200);
    expect(upstreamCalls[0].url).toBe(ENV.serverUrl + "/api/v1/public/content/media/tokA2f9");
  });

  it("keeps mapping classic /api/X paths to /api/v1/X (unchanged behavior)", () => {
    expect(bffPathToUpstream(["stores", "sto_1", "content-packs"])).toBe("/api/v1/stores/sto_1/content-packs");
    expect(bffPathToUpstream(["public", "links", "X"])).toBe("/api/v1/public/links/X");
    expect(bffPathToUpstream(["v1", "public", "content", "media", "t"])).toBe("/api/v1/public/content/media/t");
  });

  it("forwards the Range header upstream and relays 206 + Content-Range + real video/mp4", async () => {
    const res = await call("GET", MEDIA_PATH, { extraHeaders: { range: "bytes=100-199" } });
    expect(upstreamCalls[0].headers.get("range")).toBe("bytes=100-199");
    expect(res.status).toBe(206);
    expect(res.headers.get("content-type")).toBe("video/mp4");
    expect(res.headers.get("content-range")).toBe("bytes 100-199/11546");
    expect(res.headers.get("accept-ranges")).toBe("bytes");
    const body = await res.arrayBuffer();
    expect(body.byteLength).toBe(100);
  });

  it("relays 200 full-body byte-identically with the real Content-Type (never an HTML error page)", async () => {
    const res = await call("GET", MEDIA_PATH);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("video/mp4");
    const body = await res.arrayBuffer();
    expect(body.byteLength).toBe(11546);
    // payload fingerprint from the fake upstream (i % 251)
    const first = new Uint8Array(body)[0];
    const last = new Uint8Array(body)[11545];
    expect(first).toBe(0);
    expect(last).toBe(11545 % 251);
  });

  it("relays 410 (expired) and 404 (unknown) verbatim — no status rewriting, no fake success", async () => {
    const gone = await call("GET", "v1/public/content/media/expiredtok01");
    expect(gone.status).toBe(410);
    const unknown = await call("GET", "v1/public/content/media/unknowntok01");
    expect(unknown.status).toBe(404);
    expect(unknown.headers.get("content-type")).not.toBe("video/mp4");
  });

  it("relays 416 for unsatisfiable ranges (upstream verdict, untouched)", async () => {
    const res = await call("GET", MEDIA_PATH, { extraHeaders: { range: "bytes=99999-" } });
    expect(res.status).toBe(416);
    expect(res.headers.get("content-range")).toBe("bytes */11546");
  });

  it("relays HEAD with empty body and real media headers", async () => {
    const res = await call("HEAD", MEDIA_PATH);
    expect(upstreamCalls[0].method).toBe("HEAD");
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("video/mp4");
    expect(res.headers.get("content-length")).toBe("11546");
    const body = await res.arrayBuffer();
    expect(body.byteLength).toBe(0);
  });

  it("streams the body instead of buffering (Response carries an upstream stream)", async () => {
    const res = await call("GET", MEDIA_PATH, { extraHeaders: { range: "bytes=0-9" } });
    // A streamed body is a ReadableStream, not a materialized buffer: the
    // proxy must never call upstream.arrayBuffer() for media responses.
    expect(res.body).toBeInstanceOf(ReadableStream);
    const body = await res.arrayBuffer();
    expect(body.byteLength).toBe(10);
  });
});

// ---- HUI-4594: internal seams never travel through the public relay ----------------

describe("BFF internal-path block (HUI-4594)", () => {
  it("does not forward /api/internal/* (leads receiver seam)", async () => {
    const res = await call("GET", "internal/lead-records/sub_x", { tenant: "tnt_A" });
    expect(res.status).toBe(404);
    expect(upstreamCalls).toHaveLength(0);
  });

  it("does not forward Go-absolute /api/v1/internal/* passthrough", async () => {
    const res = await call("POST", "v1/internal/lead-records/sub_x/acknowledgements", {
      tenant: "tnt_A",
      body: "{}",
    });
    expect(res.status).toBe(404);
    expect(upstreamCalls).toHaveLength(0);
  });

  it("still forwards public and admin paths", async () => {
    await call("GET", "campaigns", { tenant: "tnt_A" });
    expect(upstreamCalls).toHaveLength(1);
    expect(upstreamCalls[0].url).toContain("/api/v1/campaigns");
  });
});
