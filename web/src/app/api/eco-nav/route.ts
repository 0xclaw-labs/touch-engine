import { NextResponse } from "next/server";
import { ecoNavEndpoint, ecoNavIdentityEnabled } from "@/lib/eco-nav/env";
import { loadEcoNavModel } from "@/lib/eco-nav/load";

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  // Forward the merchant session server-side: the authority decides brand,
  // tenant and launchable apps. Missing config/upstream returns a blocked
  // model (no fixture) from loadEcoNavModel.
  const headers = new Headers();
  const cookie = request.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);
  const tenant = request.headers.get("x-tenant-id");
  if (tenant) headers.set("x-tenant-id", tenant);
  const model = await loadEcoNavModel({
    endpoint: ecoNavEndpoint(),
    identityEnabled: ecoNavIdentityEnabled(),
    headers,
  });
  return NextResponse.json(model);
}
