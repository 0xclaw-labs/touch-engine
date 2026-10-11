import { randomUUID } from "node:crypto";

const SOURCE_APP = "touch-engine";
const TARGET_APP = "product-image-engine";
const TENANT = "tnt_hui2222";
const PURPOSE = "活动主图";

export type CampaignImageResult = {
  resolution: "created" | "restored";
  project_id: string;
  charges_customer: false;
};

type FetchLike = (url: string, init: RequestInit) => Promise<Response>;

export type CampaignImageEnv = {
  TOUCH_ECO_NAV_PREVIEW?: string;
  TOUCH_ECO_NAV_TEST_BRAND?: string;
  TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL?: string;
  PRODUCT_INTERNAL_TOKEN?: string;
  TEST_IDENTITY_MINT_URL?: string;
};

function stamp(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function campaignHandoffDocument(campaignId: string, now = new Date()): Record<string, unknown> {
  const issued = new Date(now.getTime() - 60_000);
  const expires = new Date(now.getTime() + 8 * 60_000);
  return {
    schema_version: "order-handoff/v1",
    handoff_id: `h-${randomUUID()}`,
    source_app: SOURCE_APP,
    target_app: TARGET_APP,
    principal_id: "usr_hui2222",
    brief_version: "brief-1",
    source_project_ref: `touch-campaign:${campaignId}`,
    source_revision: "rev-1",
    actor: {
      issuer: "https://identity.example.invalid",
      app_id: SOURCE_APP,
      subject: "subject-hui2222",
    },
    binding: {
      binding_ref: `bind-${randomUUID()}`,
      proof_digest: "b".repeat(64),
    },
    gating: { policy_version: "order-gating/v1", open_gates: [] },
    assets: [
      {
        asset_ref: `asset_${campaignId}`,
        sha256: "c".repeat(64),
        size_bytes: 2048,
        media_type: "image/png",
      },
    ],
    delivery_spec: { media_type: "image/png", description: `活动 ${campaignId} 主图` },
    scopes: ["project.resume", "asset.import", "receipt.write"],
    issued_at: stamp(issued),
    expires_at: stamp(expires),
    source_profile: {
      profile_version: "source-profile/v1",
      source_kind: "campaign",
      tenant_scope: TENANT,
      capabilities: ["image.generate"],
      constraints: [],
      return_target_id: "ti-touch-engine-web",
      campaign_ref: campaignId,
    },
  };
}

function loopbackOrigin(raw: string | undefined): string | null {
  if (!raw) return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  const host = url.hostname.toLowerCase();
  if (url.protocol !== "http:" || (host !== "127.0.0.1" && host !== "localhost")) return null;
  return url.origin;
}

export async function acceptCampaignImage(
  campaignId: string,
  env: CampaignImageEnv,
  fetchImpl: FetchLike = fetch,
): Promise<CampaignImageResult> {
  if (env.TOUCH_ECO_NAV_PREVIEW !== "1" || env.TOUCH_ECO_NAV_TEST_BRAND !== "1") {
    throw new Error("测试品牌未打开，不会向产品图提交交接");
  }
  const origin = loopbackOrigin(env.TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL);
  const token = env.PRODUCT_INTERNAL_TOKEN?.trim() ?? "";
  const mintUrl = env.TEST_IDENTITY_MINT_URL?.trim() ?? "";
  if (!origin || !token || !mintUrl) {
    throw new Error("产品图测试地址未配齐，不会伪造工程");
  }
  let mint: URL;
  try {
    mint = new URL(mintUrl);
  } catch {
    throw new Error("测试身份地址无效");
  }
  if (mint.hostname !== "127.0.0.1" && mint.hostname !== "localhost") {
    throw new Error("测试身份只能在本机");
  }
  const minted = await fetchImpl(mint.toString(), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ aud: "product-image-engine", tenant_id: TENANT }),
  });
  if (!minted.ok) throw new Error("测试身份没有签发访问令牌");
  const mintedBody = (await minted.json()) as { access_token?: string };
  if (!mintedBody.access_token) throw new Error("测试身份没有签发访问令牌");

  const accepted = await fetchImpl(`${origin}/api/v1/handoffs/accept`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-product-internal-token": token,
      authorization: `Bearer ${mintedBody.access_token}`,
    },
    body: JSON.stringify({
      handoff: campaignHandoffDocument(campaignId),
      purpose: PURPOSE,
    }),
  });
  const payload = (await accepted.json().catch(() => null)) as {
    resolution?: string;
    project?: { id?: string };
    error?: { message?: string };
  } | null;
  if (!accepted.ok || (payload?.resolution !== "created" && payload?.resolution !== "restored") || !payload.project?.id) {
    throw new Error(payload?.error?.message || "产品图没有创建或恢复工程");
  }
  return {
    resolution: payload.resolution,
    project_id: payload.project.id,
    charges_customer: false,
  };
}

export type CampaignImageLaunchInput = {
  traceId: string;
  campaignId: string;
  campaignVersion: string;
  assetRef: string;
  sha256: string;
  grantRef: string;
  returnTargetId: string;
};

export type CampaignImageLaunch = {
  trace_id: string;
  campaign_id: string;
  campaign_version: string;
  asset_ref: string;
  sha256: string;
  grant_ref: string;
  return_target_id: string;
  candidate: true;
  generated: false;
  charges_customer: false;
};

// launchCampaignImage builds the campaign-to-product-image entry.
// It does not call product-image and does not carry asset bytes.
export function launchCampaignImage(input: CampaignImageLaunchInput): CampaignImageLaunch {
  const sha = input.sha256.trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(sha)) throw new Error("bad_sha256");
  const traceId = input.traceId.trim();
  const campaignId = input.campaignId.trim();
  const assetRef = input.assetRef.trim();
  const grantRef = input.grantRef.trim();
  const returnTargetId = input.returnTargetId.trim();
  if (!traceId || !campaignId || !assetRef || !grantRef || !returnTargetId) throw new Error("missing_trace");
  return {
    trace_id: traceId,
    campaign_id: campaignId,
    campaign_version: input.campaignVersion.trim() || "1",
    asset_ref: assetRef,
    sha256: sha,
    grant_ref: grantRef,
    return_target_id: returnTargetId,
    candidate: true,
    generated: false,
    charges_customer: false,
  };
}

// receiveCandidate keeps a returned reference beside the official row.
// An upstream claim of generation or publish is refused.
export function receiveCandidate(upstream: Record<string, unknown>, launch: CampaignImageLaunch): CampaignImageLaunch {
  if (upstream.executed === true || upstream.generated === true || upstream.status === "published" || upstream.status === "executed" || upstream.status === "generated") {
    throw new Error("upstream_claimed_generation");
  }
  return { ...launch, candidate: true, generated: false, charges_customer: false };
}
