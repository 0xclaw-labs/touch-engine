// manual-material.ts: HUI-4588 手动家族(朋友圈/视频号/微博/B站/QQ/QQ空间)
// 顾客面模型——素材预览/逐张保存/用户主动复制/系统分享(按实机能力降级)。
//
// 票面红线:
//   - W3C Web Share 成功语义仅为"数据交给目标/操作系统"——系统分享成功只
//     记录素材交接(handoff_only),不是发布,更不是 platform_confirmed;
//   - 「已完成」只存顾客声明(customer_self_report),不制造 post_id/奖励;
//   - 不支持 files 分享/没有目标 App/拒绝权限/复制失败都要给出真实状态与
//     可执行恢复,不用空按钮/假成功交差;
//   - 复制必须用户主动点击(绝不后台改剪贴板/模拟粘贴);
//   - 分享按钮永不记为关注/加群/发消息(A23/A26/A29/A30 归服务票)。

import {
  isHttpsUrl,
  parseClientAction,
  parseOrderedMedia,
  type ClientActionView,
  type HandoffEnvironment,
  type OrderedMediaEntry,
} from "./client-action";

export interface ManualMaterialModel {
  kind: "manual_material";
  platform: string;
  orderedMedia: OrderedMediaEntry[];
  copyText: string;
  steps: string[];
  mediaNote: string;
  note: string;
  suggested: boolean; // 【建议补充】平台标注透传
}

export interface ManualLinkModel {
  kind: "link_share";
  platform: string;
  shareUrl: string;
  copyText: string;
  steps: string[];
  note: string;
}

export interface ManualUnavailable {
  kind: "unavailable";
  reason: string;
}

export type ManualModel = ManualMaterialModel | ManualLinkModel | ManualUnavailable;

export type ShareSupport = "system_share" | "share_url_only" | "unavailable";

// probeShareSupport reads the runtime Web Share facts. 检测顺序照 W3C:
// navigator.share 存在 → 再看 navigator.canShare({files}) 是否支持文件分享;
// 都没有 = 只能复制链接/保存素材(诚实降级,不伪造分享按钮)。
export function probeShareSupport(nav?: {
  share?: unknown;
  canShare?: (data?: ShareData) => boolean;
}): ShareSupport {
  const n = nav ?? (typeof navigator !== "undefined" ? navigator : undefined);
  if (!n || typeof n.share !== "function") return "unavailable";
  let filesOk = false;
  try {
    filesOk = typeof n.canShare === "function" ? n.canShare({ files: [new File([""], "probe", { type: "image/jpeg" })] }) : true;
  } catch {
    filesOk = false;
  }
  return filesOk ? "system_share" : "share_url_only";
}

// shareFailureCopy maps concrete failures to honest recoverable copy.
export function shareFailureCopy(reason: "aborted" | "permission" | "unsupported" | "network" | "unknown"): string {
  switch (reason) {
    case "aborted":
      return "你取消了分享；素材与文案都还在，可随时重试或改用保存/复制。";
    case "permission":
      return "分享所需权限未授予；请检查系统权限后重试，或改用保存素材+手动发布。";
    case "unsupported":
      return "当前环境不支持直接调起分享面板；请保存素材到相册后到目标 App 内发布。";
    case "network":
      return "网络异常，分享没有完成；素材未发出，可稍后重试。";
    default:
      return "分享没有完成；素材未发出，可重试或改用保存/复制。";
  }
}

// buildManualModel turns the manual-family client_action into the typed model.
export function buildManualModel(raw: unknown, environment: HandoffEnvironment | null): ManualModel {
  const action: ClientActionView | null = parseClientAction(raw);
  if (!action) return { kind: "unavailable", reason: "交接参数形状校验未通过；请回到交接页重试" };
  const platform = action.params["platform"] ?? "";
  if (action.type === "open_url") {
    // A27/A28 纯链接(link_share):打开服务端已验证目标+复制回退。
    if (!isHttpsUrl(action.url)) return { kind: "unavailable", reason: "链接形状不合法；本页不跳转" };
    return {
      kind: "link_share",
      platform,
      shareUrl: action.url,
      copyText: action.url,
      steps: action.steps,
      note: action.note,
    };
  }
  if (action.type !== "manual_export") {
    return { kind: "unavailable", reason: "当前路线不是手动素材交接形态" };
  }
  const orderedMedia = parseOrderedMedia(action.params);
  const suggested = action.note.includes("建议补充");
  const steps = [...action.steps];
  if (environment === "ios_wechat" || environment === "android_wechat") {
    // 微信容器限制:不声称自动绕开;给真实可执行的恢复步骤。
    steps.push("若系统分享/保存被微信拦截：点右上角菜单选择「在浏览器打开」后重试，素材与进度不变。");
  }
  if (orderedMedia.length > 0) {
    steps.push("下载落「文件」而非相册时：请在文件管理中手动转存相册后再到平台内选择。");
  }
  return {
    kind: "manual_material",
    platform,
    orderedMedia,
    copyText: action.caption,
    steps,
    mediaNote: action.mediaNote,
    note: action.note,
    suggested,
  };
}

// wechatContainerNote: 六环境说明的顾客可读缺省(能力判定真源在服务端 3535;
// 这里只给操作语义)。
export function wechatContainerNote(environment: HandoffEnvironment | null): string {
  if (environment === "ios_wechat" || environment === "android_wechat") {
    return "微信内浏览器限制系统能力：本页如实说明可用操作，不自动绕开限制。";
  }
  return "";
}
