import { NextResponse } from "next/server";
import { bffConfigProblems, readBffEnv } from "@/lib/bff";
import { validateLaunchUrl } from "@/lib/eco-nav/allowlist";

// HUI-2628: thin browser-facing app-switch BFF.
//   browser {app_id, launch_target_id}
//     -> Go /api/v1/eco-nav/switch (session + tenant)
//     -> public-ai POST /internal/v1/identity/shell/launch
//     -> validated launch_url
// The response is never assembled here and carries no campaign/asset data.

export const dynamic = "force-dynamic";

const APP_ID = /^[a-z0-9][a-z0-9-]{2,63}$/;
const TARGET_ID = /^ti-[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$/;

function bad(message: string, status = 400) {
  return NextResponse.json({ error: "bad_request", message }, { status });
}

export async function POST(request: Request) {
  const env = readBffEnv(process.env);
  const problems = bffConfigProblems(env);
  if (problems.length > 0) {
    return NextResponse.json(
      { error: "bff_config_gate", message: "eco-nav switch is not configured; no launch was minted" },
      { status: 503 },
    );
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return bad("JSON body is required");
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) return bad("JSON object is required");
  const keys = Object.keys(body as Record<string, unknown>);
  if (keys.length !== 2 || !keys.includes("app_id") || !keys.includes("launch_target_id")) {
    return bad("only app_id and launch_target_id are accepted");
  }
  const appId = typeof (body as Record<string, unknown>).app_id === "string" ? ((body as Record<string, unknown>).app_id as string).trim() : "";
  const targetId = typeof (body as Record<string, unknown>).launch_target_id === "string" ? ((body as Record<string, unknown>).launch_target_id as string).trim() : "";
  if (!APP_ID.test(appId) || !TARGET_ID.test(targetId)) return bad("app_id or launch_target_id is invalid");

  const headers = new Headers({ "content-type": "application/json", "x-internal-token": env.internalToken });
  const cookie = request.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);
  const tenant = request.headers.get("x-tenant-id");
  if (tenant) headers.set("x-tenant-id", tenant);

  let upstream: Response;
  try {
    upstream = await fetch(`${env.serverUrl.replace(/\/+$/, "")}/api/v1/eco-nav/switch`, {
      method: "POST",
      headers,
      body: JSON.stringify({ app_id: appId, launch_target_id: targetId }),
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return NextResponse.json({ error: "server_unavailable", message: "touch server could not be reached" }, { status: 503 });
  }

  const data = (await upstream.json().catch(() => ({}))) as Record<string, unknown>;
  if (!upstream.ok) {
    if (upstream.status === 403) return NextResponse.json({ error: "launch_denied", message: "the platform refused this app switch" }, { status: 403 });
    if (upstream.status === 429) return NextResponse.json({ error: "rate_limited", message: "too many switches; try again later" }, { status: 429 });
    if (upstream.status === 400) return NextResponse.json({ error: "bad_request", message: "the platform rejected the switch request" }, { status: 400 });
    if (upstream.status === 503) return NextResponse.json({ error: "identity_unavailable", message: "identity is unavailable; no launch was minted" }, { status: 503 });
    return NextResponse.json({ error: "launch_unavailable", message: "no launch_url was returned" }, { status: 502 });
  }

  const raw = typeof data.launch_url === "string" ? data.launch_url : "";
  const launchUrl = validateLaunchUrl(targetId, raw);
  if (!launchUrl) {
    return NextResponse.json(
      { error: "launch_target_mismatch", message: "the returned launch_url is not the registered target" },
      { status: 502 },
    );
  }
  return NextResponse.json({ launch_url: launchUrl });
}
