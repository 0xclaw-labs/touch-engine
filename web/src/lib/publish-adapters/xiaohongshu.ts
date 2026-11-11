// xiaohongshu.ts: HUI-4586 小红书视频/图文适配的顾客面模型。
//
// 票面红线:
//   - SDK 载荷不发送 title/content/topics——参考文案只在独立 copy_view 呈现,
//     由用户主动点击复制(复制动作只记录 copy 事实,不是发布成功);
//   - 官方 JS 为媒体交接:SDK 失败只是交接错误;本系统没有 JS 发布回执契约,
//     唤起/失败/关闭页面都不升级平台确认;
//   - 官方 JS SDK 真身(平台脚本)未接入:本模块不加载、不模拟任何 SDK——
//     只有当部署方注册了官方 SDK 装载器时 invoke 才可用(诚实缺位)。

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

// XhsJsSdkLoader: 部署方接入官方 JS SDK 时注册的装载器缝。默认无注册 =
// invoke 诚实不可用(本模块绝不内嵌/伪装平台脚本)。
export type XhsJsSdkLoader = () => Promise<unknown>;

let xhsSdkLoader: XhsJsSdkLoader | null = null;

export function registerXhsJsSdkLoader(loader: XhsJsSdkLoader | null): void {
  xhsSdkLoader = loader;
}

export function xhsSdkReady(): boolean {
  return xhsSdkLoader !== null;
}

// 用户主动调用:装载并调用官方 JS SDK。没有装载器=诚实错误;SDK 失败原样
// 上抛,调用面只能把它当「交接错误」呈现——没有回执语义。
export async function invokeXhsJs(payload: XhsMediaPayload): Promise<unknown> {
  if (!xhsSdkLoader) {
    throw new Error("xiaohongshu official JS SDK is not wired in this deployment; the handoff stays manual");
  }
  return xhsSdkLoader()(payload);
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
  return {
    kind: "xhs_handoff",
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
