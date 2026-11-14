import { NextRequest, NextResponse } from "next/server";

import {
  exchangeWithIdentity,
  hostAllowed,
  opaqueExchangeCode,
  readSsoBridgeConfig,
  safeReturnPath,
  sameOriginPost,
} from "@/lib/sso-bridge";

export const dynamic = "force-dynamic";

const MAX_BODY_BYTES = 16 * 1024;

function failure(reason: string): NextResponse {
  return new NextResponse(`登录接续未完成（${reason}），请回原应用重试。`, {status:400,headers:{"cache-control":"no-store","content-type":"text/plain; charset=utf-8"}});
}

async function readCode(req: NextRequest): Promise<{ code: string | null; returnTarget: unknown }> {
  const contentType = (req.headers.get("content-type") ?? "").toLowerCase();
  let text: string;
  try {
    text = await req.text();
  } catch {
    return { code: null, returnTarget: null };
  }
  if (text.length > MAX_BODY_BYTES) return { code: null, returnTarget: null };
  if (contentType.includes("application/x-www-form-urlencoded") || contentType.trim() === "") {
    const params = new URLSearchParams(text);
    return { code: params.get("code"), returnTarget: params.get("return_target") };
  }
  if (contentType.includes("application/json")) {
    try {
      const parsed = JSON.parse(text) as Record<string, unknown>;
      return { code: typeof parsed.code === "string" ? parsed.code : null, returnTarget: parsed.return_target };
    } catch {
      return { code: null, returnTarget: null };
    }
  }
  return { code: null, returnTarget: null };
}

export async function POST(req: NextRequest): Promise<NextResponse> {
  const cfg = readSsoBridgeConfig(process.env);
  if (!hostAllowed(req.headers.get("host"), req.headers.get("x-forwarded-host"), cfg.appHost)) {
    return failure("audience_mismatch");
  }
  if (!sameOriginPost({ origin: req.headers.get("origin"), secFetchSite: req.headers.get("sec-fetch-site") }, cfg.appHost)) {
    return failure("audience_mismatch");
  }
  const { code, returnTarget } = await readCode(req);
  const opaque = opaqueExchangeCode(code);
  if (!opaque) return failure("invalid_request");

  let result;
  try {
    const clientIP = (req.headers.get("x-forwarded-for") ?? req.headers.get("x-real-ip") ?? "").split(",")[0]?.trim() || null;
    result = await exchangeWithIdentity(cfg, opaque, clientIP);
  } catch {
    return failure("identity_unavailable");
  }
  if (result.status !== 200) return failure(result.status === 403 ? "membership_required" : "failed");
  // identity 200 但没有本应用会话 cookie 不能算成功（Q2 缺口 1）。
  if (!result.setCookie.some((cookie) => cookie.trim().toLowerCase().startsWith("touch-engine_session="))) {
    return failure("failed");
  }

  const res = NextResponse.redirect(new URL(safeReturnPath(returnTarget), `https://${cfg.appHost}`), 303);
  res.headers.set("cache-control", "no-store");
  for (const cookie of result.setCookie) res.headers.append("set-cookie", cookie);
  // 换账号进入时必须让旧租户选择失效（Q2 缺口 5），由壳层读取后清理。
  res.headers.append("set-cookie", "touch_sso_reset=1; Path=/; Max-Age=60; SameSite=Lax; Secure");
  return res;
}
