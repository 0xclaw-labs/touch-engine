import { NextRequest, NextResponse } from "next/server";

import {
  exchangeWithIdentity,
  hostAllowed,
  opaqueExchangeCode,
  readSsoBridgeConfig,
  safeReturnPath,
  sameOriginPost,
} from "@/lib/sso-bridge";
import { buildPortalLoginUrl } from "@/lib/portal-sso";

export const dynamic = "force-dynamic";

const MAX_BODY_BYTES = 16 * 1024;

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (ch) =>
    ch === "&" ? "&amp;" : ch === "<" ? "&lt;" : ch === ">" ? "&gt;" : ch === '"' ? "&quot;" : "&#39;",
  );
}

function failure(reason: string): NextResponse {
  return new NextResponse(`登录接续未完成（${reason}），请回原应用重试。`, {status:400,headers:{"cache-control":"no-store","content-type":"text/plain; charset=utf-8"}});
}

function membershipRequiredResponse(appHost: string, returnPath: string): NextResponse {
  const switchUrl = buildPortalLoginUrl({
    promptLogin: true,
    currentOrigin: `https://${appHost}`,
    next: returnPath,
  });
  const html = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>账号尚未开通团队席位 · 碰一碰</title></head>
<body style="font-family:system-ui,sans-serif;max-width:36rem;margin:12vh auto;padding:0 1rem;color:#101828;line-height:1.6">
<main>
<h1 style="font-size:1.25rem;margin-bottom:0.75rem">当前账号尚未开通团队席位</h1>
<p>你的生态账号已验证，但尚未加入碰一碰组织（无团队席位）。请联系管理员为你开通席位，或切换其他已开通账号。</p>
<p style="margin-top:1.25rem;display:flex;gap:0.75rem;flex-wrap:wrap">
<a href="${escapeHTML(switchUrl)}" style="display:inline-block;padding:0.5rem 1rem;border-radius:0.5rem;background:#101828;color:#fff;text-decoration:none">切换其他账号</a>
<a href="/" style="display:inline-block;padding:0.5rem 1rem;border-radius:0.5rem;border:1px solid #d0d5dd;color:#344054;text-decoration:none">返回登录页</a>
</p>
</main></body></html>`;
  return new NextResponse(html, {
    status: 403,
    headers: {
      "cache-control": "no-store",
      "content-type": "text/html; charset=utf-8",
      "x-content-type-options": "nosniff",
      "x-frame-options": "DENY",
      "content-security-policy": "frame-ancestors 'none'",
    },
  });
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
  if (result.status === 403) return membershipRequiredResponse(cfg.appHost, safeReturnPath(returnTarget));
  if (result.status !== 200) return failure("failed");
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
