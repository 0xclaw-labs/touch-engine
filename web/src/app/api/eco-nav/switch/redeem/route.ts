import { NextResponse } from "next/server";
import { bffConfigProblems, readBffEnv } from "@/lib/bff";
import { validateLaunchUrl } from "@/lib/eco-nav/allowlist";

// HUI-2222: thin browser-facing app-switch redeem BFF.
//   browser {jump_token}
//     -> Go /api/v1/eco-nav/switch/redeem (session + tenant)
//     -> Go claims the grant transactionally (single use, TTL bound) and
//        releases the shell's launch_url with the contract ids
//     -> validateLaunchUrl(targetId, url) before anything reaches the browser
// The launch_url exists in a browser response only here — once per grant.

export const dynamic = "force-dynamic";

function bad(message: string, status = 400) {
  return NextResponse.json({ error: "bad_request", message }, { status });
}

export async function POST(request: Request) {
  const env = readBffEnv(process.env);
  const problems = bffConfigProblems(env);
  if (problems.length > 0) {
    return NextResponse.json(
      { error: "bff_config_gate", message: "切换服务还没有配置好，暂时不可用" },
      { status: 503 },
    );
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return bad("切换请求格式不正确，请重试");
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) return bad("切换请求格式不正确，请重试");
  const keys = Object.keys(body as Record<string, unknown>);
  if (keys.length !== 1 || typeof (body as Record<string, unknown>).jump_token !== "string") {
    return bad("切换请求格式不正确，请重试");
  }
  const jumpToken = ((body as Record<string, unknown>).jump_token as string).trim();
  if (jumpToken === "") return bad("切换请求格式不正确，请重试");

  const headers = new Headers({ "content-type": "application/json", "x-internal-token": env.internalToken });
  const cookie = request.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);
  const tenant = request.headers.get("x-tenant-id");
  if (tenant) headers.set("x-tenant-id", tenant);

  let upstream: Response;
  try {
    upstream = await fetch(`${env.serverUrl.replace(/\/+$/, "")}/api/v1/eco-nav/switch/redeem`, {
      method: "POST",
      headers,
      body: JSON.stringify({ jump_token: jumpToken }),
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return NextResponse.json({ error: "server_unavailable", message: "暂时连不上服务，请稍后重试" }, { status: 503 });
  }

  const data = (await upstream.json().catch(() => ({}))) as Record<string, unknown>;
  if (!upstream.ok) {
    // 诚实映射:过期/失效/不可用各自如实带状态码,绝不伪造一个可跳的 URL。
    if (upstream.status === 410) {
      return NextResponse.json({ error: "jump_expired", message: "这次切换已过期，请重新发起" }, { status: 410 });
    }
    if (upstream.status === 404) {
      return NextResponse.json({ error: "jump_invalid", message: "这次切换已失效，请重新发起" }, { status: 404 });
    }
    if (upstream.status === 403) {
      return NextResponse.json({ error: "launch_denied", message: "平台暂时拒绝了这次切换，请稍后重试" }, { status: 403 });
    }
    if (upstream.status === 503) {
      return NextResponse.json({ error: "identity_unavailable", message: "暂时连不上服务，请稍后重试" }, { status: 503 });
    }
    return NextResponse.json({ error: "launch_unavailable", message: "这次切换没有成功，请稍后重试" }, { status: 502 });
  }

  const raw = typeof data.launch_url === "string" ? data.launch_url : "";
  const targetId = typeof data.launch_target_id === "string" ? data.launch_target_id : "";
  const launchUrl = validateLaunchUrl(targetId, raw);
  if (!launchUrl) {
    return NextResponse.json(
      { error: "launch_target_mismatch", message: "这次切换没有成功，请稍后重试" },
      { status: 502 },
    );
  }
  return NextResponse.json({ launch_url: launchUrl });
}
