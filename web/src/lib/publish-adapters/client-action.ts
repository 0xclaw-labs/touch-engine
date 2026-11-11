// client-action.ts: touch-handoff/v2 client_action 的前端形状校验与环境检测
// (HUI-4585..4588 四平台 publish-adapters 共享的唯一解析出口)。
//
// 纪律(1670 冻结契约/裁定书):
//   - client_action 是类型化描述——这里只渲染/校验,绝不执行任何载荷;
//   - URL 只接受 https(公开面无 http 明文);失败回 null 让页面走诚实态,
//     绝不静默降级成猜测入口;
//   - 环境六枚举(T07 口径)从 UA 实测推断,target_app 无法从网页检测——
//     诚实返回 null,由调用面按「未识别环境」给保守步骤。

export const HANDOFF_CLIENT_ACTION_TYPES = [
  "open_url",
  "open_miniapp",
  "js_media_handoff",
  "oauth_consent",
  "manual_export",
] as const;

export type ClientActionType = (typeof HANDOFF_CLIENT_ACTION_TYPES)[number];

export const SIX_ENVIRONMENTS = [
  "desktop_browser",
  "ios_safari",
  "android_browser",
  "ios_wechat",
  "android_wechat",
  "target_app",
] as const;

export type HandoffEnvironment = (typeof SIX_ENVIRONMENTS)[number];

export interface ClientActionView {
  type: ClientActionType;
  url: string;
  miniappPath: string;
  params: Record<string, string>;
  caption: string;
  steps: string[];
  mediaNote: string;
  note: string;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function isHttpsUrl(v: unknown): v is string {
  if (typeof v !== "string" || v.length === 0 || v.length > 2048) return false;
  try {
    const u = new URL(v);
    return u.protocol === "https:" && u.hostname.length > 0;
  } catch {
    return false;
  }
}

// parseClientAction validates one raw client_action (server JSON). null =
// shape violation — the page must answer honestly instead of guessing.
export function parseClientAction(raw: unknown): ClientActionView | null {
  if (!isRecord(raw)) return null;
  const type = raw["type"];
  if (typeof type !== "string" || !(HANDOFF_CLIENT_ACTION_TYPES as readonly string[]).includes(type)) {
    return null;
  }
  const params: Record<string, string> = {};
  const rawParams = raw["params"];
  if (rawParams !== undefined && rawParams !== null) {
    if (!isRecord(rawParams)) return null;
    for (const [k, v] of Object.entries(rawParams)) {
      if (typeof v !== "string") return null;
      params[k] = v;
    }
  }
  const steps: string[] = [];
  if (raw["steps"] !== undefined) {
    if (!Array.isArray(raw["steps"])) return null;
    for (const s of raw["steps"]) {
      if (typeof s !== "string") return null;
      steps.push(s);
    }
  }
  const url = typeof raw["url"] === "string" ? raw["url"] : "";
  if (url !== "" && !isHttpsUrl(url)) return null;
  return {
    type: type as ClientActionType,
    url,
    miniappPath: typeof raw["miniapp_path"] === "string" ? raw["miniapp_path"] : "",
    params,
    caption: typeof raw["caption"] === "string" ? raw["caption"] : "",
    steps,
    mediaNote: typeof raw["media_note"] === "string" ? raw["media_note"] : "",
    note: typeof raw["note"] === "string" ? raw["note"] : "",
  };
}

export interface DetectedEnvironment {
  environment: HandoffEnvironment | null;
  wechat: boolean;
  mobile: boolean;
}

// detectEnvironment classifies the runtime from UA facts (六环境检测的诚实
// 边界:target_app 由宿主声明,网页侧检不出 → null;无任何平台标记的可识别性
// 不足 → null,由调用面按保守步骤处理).
export function detectEnvironment(ua: string, opts?: { wechatOverride?: boolean }): DetectedEnvironment {
  const text = (ua ?? "").toLowerCase();
  const wechat = opts?.wechatOverride ?? text.includes("micromessenger");
  const ios = /iphone|ipad|ipod/.test(text);
  const android = text.includes("android");
  const desktop = /macintosh|windows|cros|x11/.test(text);
  const mobile = ios || android;
  let environment: HandoffEnvironment | null = null;
  if (wechat && ios) environment = "ios_wechat";
  else if (wechat && android) environment = "android_wechat";
  else if (ios) environment = "ios_safari";
  else if (android) environment = "android_browser";
  else if (desktop) environment = "desktop_browser";
  return { environment, wechat, mobile };
}

export interface OrderedMediaEntry {
  kind: string; // video | cover | image
  url: string;
  index: number;
}

// parseOrderedMedia reads the 4588 family's ordered params
// (media_order=media_00,media_01… / media_NN=kind:url). Broken shapes answer
// an empty list — the page keeps honest steps without fake media rows.
export function parseOrderedMedia(params: Record<string, string>): OrderedMediaEntry[] {
  const orderRaw = params["media_order"];
  if (!orderRaw) return [];
  const keys = orderRaw.split(",").map((k) => k.trim()).filter(Boolean);
  const out: OrderedMediaEntry[] = [];
  for (const key of keys) {
    const value = params[key];
    if (!value) continue;
    const sep = value.indexOf(":");
    if (sep <= 0) continue;
    const kind = value.slice(0, sep);
    const url = value.slice(sep + 1);
    if (!isHttpsUrl(url)) continue;
    out.push({ kind, url, index: out.length });
  }
  return out;
}
