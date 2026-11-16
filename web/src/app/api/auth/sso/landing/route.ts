import { NextRequest, NextResponse } from "next/server";

import { hostAllowed, opaqueExchangeCode, readSsoBridgeConfig } from "@/lib/sso-bridge";

export const dynamic = "force-dynamic";

// GET /api/auth/sso/landing —— 门户一次性 code 的到达桥：渲染同源自动 POST 表单，
// GET 不换码、不写 cookie、不落日志。
function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (ch) =>
    ch === "&" ? "&amp;" : ch === "<" ? "&lt;" : ch === ">" ? "&gt;" : ch === '"' ? "&quot;" : "&#39;",
  );
}

export async function GET(req: NextRequest): Promise<NextResponse> {
  const cfg = readSsoBridgeConfig(process.env);
  if (!hostAllowed(req.headers.get("host"), req.headers.get("x-forwarded-host"), cfg.appHost)) {
    return new NextResponse("登录请求来源不正确。", {
      status: 400,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });
  }
  const code = opaqueExchangeCode(req.nextUrl.searchParams.get("code"));
  if (!code) {
    return new NextResponse("登录链接不完整，请回到门户重新进入碰一碰。", {
      status: 400,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });
  }
  const h = opaqueExchangeCode(req.nextUrl.searchParams.get("h"));
  const continuation = h && /^hnd_[a-f0-9]{32}$/.test(h) ? `/lead-follow-up-return/${h}` : "/";
  const html = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>正在进入碰一碰…</title></head>
<body style="color-scheme:light;background:#f7f8fa;color:#101828;font-family:system-ui,sans-serif;max-width:36rem;margin:12vh auto;padding:0 1rem">
<main><h1 style="font-size:1.25rem">正在进入碰一碰…</h1>
<p>请稍候。此页只在你的浏览器与本应用之间传递一次性登录凭证。</p>
<form id="sso-exchange" method="post" action="/api/auth/sso/exchange">
<input type="hidden" name="code" value="${escapeHTML(code)}">
<input type="hidden" name="return_target" value="${escapeHTML(continuation)}">
<noscript><p>浏览器未启用脚本，请点按继续。</p><button type="submit">继续</button></noscript>
</form>
<script>document.getElementById("sso-exchange").submit();</script>
</main></body></html>`;
  return new NextResponse(html, {
    status: 200,
    headers: {
      "content-type": "text/html; charset=utf-8",
      "cache-control": "no-store",
      "referrer-policy": "same-origin",
      "x-content-type-options": "nosniff",
      "x-frame-options": "DENY",
    },
  });
}
