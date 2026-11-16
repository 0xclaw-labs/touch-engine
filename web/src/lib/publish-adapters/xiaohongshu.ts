// xiaohongshu.ts: HUI-4586 小红书视频/图文适配的顾客面模型。
//
// 票面红线:
//   - SDK 载荷不发送 title/content/topics——参考文案只在独立 copy_view 呈现,
//     由用户主动点击复制(复制动作只记录 copy 事实,不是发布成功);
//   - 官方 JS 为媒体交接:SDK 失败只是交接错误;本系统没有 JS 发布回执契约,
//     唤起/失败/关闭页面都不升级平台确认;
//   - 默认加载官方固定版本脚本；服务端签名缺失/过期拒绝调用。
//     SDK 调用只表示发起交接，异步 fail 交回原页面展示，不冒充发布结果。

import { isHttpsUrl, parseClientAction, type ClientActionView } from "./client-action";

export interface XhsMediaPayload {
  type: "video" | "normal";
  video: string;
  cover: string;
  images: string[];
}

export interface XhsHandoffModel {
  kind: "xhs_handoff";
  payload: XhsMediaPayload;
  verifyConfig: XhsVerifyConfig;
  copyText: string; // 独立 copy_view 的参考文案(用户主动复制)
  steps: string[];
  mediaNote: string;
  note: string;
  sdkReady: boolean; // 官方 SDK 装载器已注册才为 true
}

export interface XhsUnavailable {
  kind: "unavailable";
  reason: string;
}

export type XhsModel = XhsHandoffModel | XhsUnavailable;

const BANNED_TEXT_KEYS = ["title", "content", "topics", "desc", "text"] as const;

// XhsJsSdkLoader: 部署方接入官方 JS SDK 时注册的装载器缝(接收已校验的媒体
// 载荷)。默认无注册 = invoke 诚实不可用(本模块绝不内嵌/伪装平台脚本)。
export type XhsJsSdkLoader = (payload: XhsMediaPayload) => Promise<unknown>;

export interface XhsVerifyConfig { appKey: string; nonce: string; timestamp: string; signature: string }
interface XhsSdk { share(input: { shareInfo: XhsMediaPayload; verifyConfig: XhsVerifyConfig; fail: (error: unknown) => void }): unknown }
declare global { interface Window { xhs?: XhsSdk } }
let xhsSdkLoader: XhsJsSdkLoader | null | undefined;
let loading: Promise<void> | null = null;
export function registerXhsJsSdkLoader(loader: XhsJsSdkLoader | null | undefined): void { xhsSdkLoader = loader; }
export function xhsSdkReady(): boolean { return typeof xhsSdkLoader === "function" || (xhsSdkLoader !== null && typeof window !== "undefined" && !!window.xhs); }
export function loadOfficialXhsSdk(): Promise<void> {
  if (typeof xhsSdkLoader === "function") return Promise.resolve();
  if (xhsSdkLoader === null || typeof document === "undefined") return Promise.reject(new Error("当前环境无法加载小红书交接，请回原页面重试。"));
  if (window.xhs) return Promise.resolve();
  if (loading) return loading;
  loading = new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    const timeout = setTimeout(() => { script.remove(); loading = null; reject(new Error("小红书组件加载超时，请重试。")); }, 15000);
    script.src = "https://fe-static.xhscdn.com/biz-static/goten/xhs-1.0.1.js";
    script.async = true;
    script.onload = () => { clearTimeout(timeout); if (window.xhs) resolve(); else { loading = null; reject(new Error("小红书组件未就绪，请重试。")); } };
    script.onerror = () => { clearTimeout(timeout); script.remove(); loading = null; reject(new Error("小红书组件加载失败，请检查网络后重试。")); };
    document.head.appendChild(script);
  });
  return loading;
}
export async function invokeXhsJs(payload: XhsMediaPayload, verify?: XhsVerifyConfig, onFailure?: (message: string) => void): Promise<unknown> {
  if (typeof xhsSdkLoader === "function") return xhsSdkLoader(payload);
  if (!verify) throw new Error("小红书交接签名缺失，请重新获取。");
  await loadOfficialXhsSdk();
  if (!window.xhs) throw new Error("小红书组件未就绪，请重试。");
  // SDK has a fail callback, no official publish-success callback. Async
  // failures go back to the owning view; returning only means invocation.
  let synchronousFailure = false;
  window.xhs.share({ shareInfo: payload, verifyConfig: verify, fail: () => { synchronousFailure = true; onFailure?.("小红书素材交接失败，请在原页面重新获取素材和签名后重试。"); } });
  if (synchronousFailure) throw new Error("小红书素材交接失败，请重试。");
  return { evidence: "handoff_only", officialResult: "unknown" };
}
function parseVerifyConfig(raw: string): XhsVerifyConfig | null {
  try { const obj = JSON.parse(raw) as Record<string, unknown>;
    if (!obj || ["appKey", "nonce", "timestamp", "signature"].some(k => typeof obj[k] !== "string" || !obj[k])) return null;
    const stamp = Number(obj.timestamp);
    if (!Number.isFinite(stamp) || Math.abs(Date.now() - stamp) > 10 * 60 * 1000) return null;
    return { appKey: obj.appKey as string, nonce: obj.nonce as string, timestamp: obj.timestamp as string, signature: obj.signature as string };
  } catch { return null; }
}

// parseXhsPayload validates the server-issued media payload. 红线自检:载荷
// 出现任何文字字段=拒绝渲染(服务端结构性保证,前端再核一道)。
export function parseXhsPayload(rawJson: string): XhsMediaPayload | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawJson);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return null;
  const obj = parsed as Record<string, unknown>;
  for (const banned of BANNED_TEXT_KEYS) {
    if (banned in obj) return null;
  }
  const type = obj["type"];
  if (type !== "video" && type !== "normal") return null;
  const httpsOrEmpty = (v: unknown): string => (isHttpsUrl(v) ? v : "");
  const video = httpsOrEmpty(obj["video"]);
  const cover = httpsOrEmpty(obj["cover"]);
  let images: string[] = [];
  if (Array.isArray(obj["images"])) {
    for (const img of obj["images"]) {
      if (!isHttpsUrl(img)) return null;
      images.push(img);
    }
  }
  if (type === "video" && !video) return null;
  if (type === "normal" && images.length === 0) return null;
  return { type, video, cover, images };
}

// buildXhsHandoff turns the prepared client_action into the typed model.
export function buildXhsHandoff(raw: unknown): XhsModel {
  const action: ClientActionView | null = parseClientAction(raw);
  if (!action) return { kind: "unavailable", reason: "交接参数形状校验未通过；请回到交接页重试" };
  if (action.type !== "js_media_handoff" || action.params["sdk"] !== "xhs_js") {
    return { kind: "unavailable", reason: "当前路线不是小红书官方 JS 媒体交接形态" };
  }
  const payload = parseXhsPayload(action.params["payload"] ?? "");
  if (!payload) return { kind: "unavailable", reason: "媒体载荷缺失或携带了文字字段；本页拒绝渲染" };
  const verifyConfig = parseVerifyConfig(action.params["verify_config"] ?? "");
  if (!verifyConfig || verifyConfig.appKey !== action.params["app_id"]) return { kind: "unavailable", reason: "交接签名缺失或已过期，请刷新原交接重新获取。" };
  return {
    kind: "xhs_handoff",
    verifyConfig,
    payload,
    copyText: action.caption,
    steps: action.steps,
    mediaNote: action.mediaNote,
    note: action.note,
    sdkReady: xhsSdkReady(),
  };
}

// xhsQualificationNotice: 官网快照显示暂停新接入=资格未获准的诚实说明
// (服务端 pending_external 时页面透传该文案;本模块只提供缺省措辞)。
export function xhsQualificationNotice(notice: string): string {
  return notice || "该平台的承接资格未确认（等待平台批复），暂不能发起交接，也不提供替代的手动发布入口。";
}
