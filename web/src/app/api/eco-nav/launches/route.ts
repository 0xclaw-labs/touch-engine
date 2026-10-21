import { NextResponse } from "next/server";
import { registeredLaunchMap } from "@/lib/eco-nav/allowlist";

// HUI-2628: availability only. The browser never receives launch URLs; the
// switch route mints a one-time SSO launch after validating it server-side.
export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json({ targets: Object.keys(registeredLaunchMap()) });
}
