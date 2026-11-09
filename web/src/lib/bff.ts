// BFF proxy core. The web tier is a dumb, safe relay:
//   browser -> /api/* (this BFF) -> Go server /api/v1/* (loopback)
// Rules:
//   - the browser NEVER talks to the Go server or platform services directly
//   - the internal token is injected here and always overrides anything the
//     caller sends (no escalation)
//   - missing BFF config is a fail-closed 503; nothing is forwarded
//   - all authorization happens on the Go server; the BFF only relays verdicts
//   - the public activity area (/c/[code]) and the admin area (/admin) both go
//     through this same relay; the Go server enforces that a guest can only
//     ever read the public whitelist surface

export interface BffEnv {
  serverUrl: string;
  internalToken: string;
}

export function readBffEnv(env: Record<string, string | undefined>): BffEnv {
  return {
    serverUrl: (env.TOUCH_SERVER_URL ?? "").trim(),
    internalToken: (env.TOUCH_INTERNAL_TOKEN ?? "").trim(),
  };
}

export function bffConfigProblems(env: BffEnv): string[] {
  const problems: string[] = [];
  if (!env.serverUrl) problems.push("TOUCH_SERVER_URL is required");
  if (!env.internalToken) problems.push("TOUCH_INTERNAL_TOKEN is required");
  return problems;
}

const HOP_BY_HOP = new Set([
  "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
  "te", "trailer", "transfer-encoding", "upgrade", "host",
]);

export function bffPathToUpstream(path: string[]): string {
  // HUI-3553 D3: Go API responses carry Go-absolute URLs (e.g. media-access
  // returns "/api/v1/public/content/media/<token>" as the <video>/<a> src).
  // A browser fetching such a URL from the web origin arrives here with
  // path[0] === "v1" — pass it through instead of double-prefixing
  // ("/api/v1/v1/..." 404s upstream). Classic BFF paths are unchanged.
  if (path.length > 0 && path[0] === "v1") {
    return "/api/" + path.join("/");
  }
  return "/api/v1/" + path.join("/");
}

export async function proxyToServer(
  req: Request,
  env: BffEnv,
  path: string[],
): Promise<Response> {
  const problems = bffConfigProblems(env);
  if (problems.length > 0) {
    return Response.json(
      {
        error: "bff_config_gate",
        message: "web BFF is not configured; refusing to forward (fail-closed)",
        detail: problems,
      },
      { status: 503 },
    );
  }

  const incoming = new URL(req.url);
  const upstreamUrl =
    env.serverUrl.replace(/\/+$/, "") +
    bffPathToUpstream(path) +
    incoming.search;

  const headers = new Headers();
  // forward the identity session cookie verbatim; never the caller's token
  const cookie = req.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);
  const contentType = req.headers.get("content-type");
  if (contentType) headers.set("content-type", contentType);
  // HUI-3553 D3: controlled media needs Range semantics end to end (video
  // seeking, weak-network resume). Relay the byte-interval headers verbatim;
  // the Go server (http.ServeContent) answers 206/416.
  const range = req.headers.get("range");
  if (range) headers.set("range", range);
  const ifRange = req.headers.get("if-range");
  if (ifRange) headers.set("if-range", ifRange);
  // workspace selection is caller input; the Go server re-validates it
  // against membership (an unknown/foreign tenant can only ever yield 403)
  const tenant = req.headers.get("x-tenant-id");
  if (tenant) headers.set("x-tenant-id", tenant);
  headers.set("x-internal-token", env.internalToken);
  headers.set("x-forwarded-for-origin", incoming.origin);
  // Brand host is the host the browser actually used. A caller-supplied
  // X-Forwarded-Host or X-Touch-Public-Host cannot retarget the brand.
  if (incoming.host) headers.set("x-touch-public-host", incoming.host);

  const init: RequestInit = {
    method: req.method,
    headers,
    redirect: "manual",
  };
  if (!["GET", "HEAD"].includes(req.method)) {
    init.body = await req.arrayBuffer();
  }

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, init);
  } catch {
    return Response.json(
      {
        error: "server_unavailable",
        message: "touch server could not be reached; refusing to fake a result",
      },
      { status: 503 },
    );
  }

  // relay status + safe headers (incl. every set-cookie)
  const outHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) outHeaders.append(key, value);
  });
  // HUI-3553 D3: relay the body as a stream — never buffer the whole file.
  // Controlled media answers 206 partial content; buffering would defeat
  // Range seeking on large videos and hold every byte in memory per request.
  // 204/304 must carry a null body (a zero-length buffer is still a body and
  // makes the Response constructor throw — e.g. the view-event beacon).
  const nullBody = upstream.status === 204 || upstream.status === 304;
  return new Response(nullBody ? null : upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: outHeaders,
  });
}
