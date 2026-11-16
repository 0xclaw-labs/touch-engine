import { NextRequest, NextResponse } from "next/server";
import { hostAllowed, readSsoAppHost } from "@/lib/sso-host";

// A reverse proxy may normalize nextUrl to its internal localhost endpoint.
// Only the configured canonical host may receive the original code and h.
export function middleware(req: NextRequest) {
  if (req.nextUrl.pathname === "/" && req.nextUrl.searchParams.has("code")) {
    const host = readSsoAppHost();
    if (!hostAllowed(req.headers.get("host"), req.headers.get("x-forwarded-host"), host)) {
      return new NextResponse("登录请求来源不正确。", {
        status: 400,
        headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
      });
    }
    const dest = new URL("/api/auth/sso/landing", `https://${host}`);
    dest.search = req.nextUrl.search;
    const response = NextResponse.redirect(dest);
    response.headers.set("cache-control", "no-store");
    response.headers.set("referrer-policy", "same-origin");
    return response;
  }
  return NextResponse.next();
}
export const config = { matcher: ["/"] };
