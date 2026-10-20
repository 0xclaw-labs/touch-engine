import {
  blockedUnavailableEcoNav,
  isRenderable,
  pinTouchApp,
  parseEcoNavDocument,
  REGISTRY_WITHOUT_IDENTITY_SUMMARY,
  toViewModel,
  type EcoNavModel,
  type EcoNavProvenance,
} from "@/lib/eco-nav/model";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";

export { provisionalEcoNav };

export interface LoadEcoNavInput {
  endpoint: string | null;
  identityEnabled: boolean;
  fetchImpl?: typeof fetch;
  fixture?: EcoNavModel;
  /** Session cookie + selected tenant, forwarded server-side only. */
  headers?: HeadersInit;
}

/**
 * 读取生态导航。
 * 没有目录地址、网络失败或契约不符时，回到预览视图。
 * 解析成功但不可渲染（hidden / denied / unavailable）时不换成预览夹具。
 * 冻结文档没有 provenance。只有解析成功且 PUBLIC_AI_ECO_NAV_IDENTITY=1，
 * 才把视图标成 public-ai 身份。
 */
export async function loadEcoNavModel(input: LoadEcoNavInput): Promise<EcoNavModel> {
  // A fixture is still accepted by tests/preview callers, but the production
  // route never passes one. No config -> no nav, not someone else's context.
  const fixture = input.fixture ? pinTouchApp(input.fixture) : null;
  if (!input.endpoint) return fixture ?? blockedUnavailableEcoNav();

  const fetchImpl = input.fetchImpl ?? fetch;
  let response: Response;
  try {
    response = await fetchImpl(input.endpoint, {
      method: "GET",
      headers: { accept: "application/json", ...(input.headers ?? {}) },
      cache: "no-store",
    });
  } catch {
    return fixture ?? blockedUnavailableEcoNav();
  }
  if (!response.ok) {
    return fixture ?? blockedUnavailableEcoNav();
  }

  let json: unknown;
  try {
    json = await response.json();
  } catch {
    return fixture ?? blockedUnavailableEcoNav();
  }
  const parsed = parseEcoNavDocument(json);
  if (!parsed.ok) {
    return fixture ?? blockedUnavailableEcoNav();
  }

  const provenance: EcoNavProvenance = input.identityEnabled ? "public_ai_context" : "provisional_fixture";
  const statusSummary =
    !input.identityEnabled && isRenderable(parsed.document) ? REGISTRY_WITHOUT_IDENTITY_SUMMARY : "";
  return pinTouchApp(toViewModel(parsed.document, { provenance, statusSummary }));
}
