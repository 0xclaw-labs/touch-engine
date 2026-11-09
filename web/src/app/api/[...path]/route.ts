import { proxyToServer, readBffEnv } from "@/lib/bff";

// Catch-all BFF route: /api/* -> Go server /api/v1/*.
// e.g. /api/auth/login -> /api/v1/auth/login, /api/public/links/X -> /api/v1/public/links/X.

type Ctx = { params: Promise<{ path: string[] }> };

async function handle(req: Request, ctx: Ctx): Promise<Response> {
  const { path } = await ctx.params;
  return proxyToServer(req, readBffEnv(process.env), path);
}

export const GET = handle;
// HUI-3553 D3: controlled media advertises itself via HEAD (real file exists,
// size/Content-Type probeable). Relay it like GET — the Go server answers.
export const HEAD = handle;
export const POST = handle;
export const PUT = handle;
export const PATCH = handle;
export const DELETE = handle;
