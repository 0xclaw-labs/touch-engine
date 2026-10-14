// HUI-1670 碰一碰门店交付 B1: 顾客发布交接的视图语义(自足目录)。
//
// 服务端(Go publishhandoff + /api/v1/public/links/{code}/publish-handoff*)
// 是唯一权威;本模块只承载四件事:
//   1. 交接视图/路线裁决/媒体引用的客户端类型;
//   2. 文案纪律:九态之外 fail-closed;只有 platform_confirmed 允许说
//      「已发布」;awaiting_review 只是待核验;「我已完成」只是核验请求;
//      拒绝面绝不渲染手动指引(不把手工路径当被禁玩法的后门);
//   3. 入口/媒体 URL 安全形状:入口只接受 https 直达或小程序站内路径;
//      媒体地址只允许站内 /api/ 相对路径或 https;javascript:/data: 一律拒;
//   4. 纪律:恢复令牌只在响应里出现一次,绝不写 localStorage;浏览器侧
//      不出现内部 token / 平台 secret;验签只在服务端。
//
// 换稿 = 服务端指纹变化 → 422 confirmation_mismatch → 必须重新确认,
// 本模块提供 confirmMismatchCopy 说明这一点。

export type HandoffStatus =
  | "previewed"
  | "handoff_preparing"
  | "handoff_ready"
  | "editor_opened"
  | "submitting"
  | "awaiting_review"
  | "platform_confirmed"
  | "canceled"
  | "failed"
  | "unknown";

const HANDOFF_STATUSES: ReadonlySet<string> = new Set<HandoffStatus>([
  "previewed",
  "handoff_preparing",
  "handoff_ready",
  "editor_opened",
  "submitting",
  "awaiting_review",
  "platform_confirmed",
  "canceled",
  "failed",
  "unknown",
]);

/** 未知状态 fail-closed:不认识就是不认识,绝不抹平成"进行中"。 */
export function isKnownHandoffStatus(status: string | undefined): boolean {
  return HANDOFF_STATUSES.has(status ?? "");
}

/** 仍可操作(取消/完成申报)的活动状态;终态与 awaiting_review 除外。 */
const ACTIVE_STATUSES: ReadonlySet<string> = new Set([
  "previewed",
  "handoff_preparing",
  "handoff_ready",
  "editor_opened",
  "submitting",
  "unknown",
]);

export function canCancel(status: string | undefined): boolean {
  return ACTIVE_STATUSES.has(status ?? "");
}

/** 「我已完成」只是核验请求:活动态都可申报,终态不可。 */
export function canRequestDone(status: string | undefined): boolean {
  return ACTIVE_STATUSES.has(status ?? "");
}

// ---- 视图类型(与服务端 JSON 投影一一对应) --------------------------------

export interface HandoffView {
  id?: string;
  status?: string;
  evidence_source?: string;
  platform?: string;
  entry_mode?: string;
  scenario?: string;
  participation_id?: string;
  attempt_id?: string;
  content_version?: string;
  query_count?: number;
  timed_out?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface PlanEntryView {
  method?: string;
  url?: string;
  miniapp_path?: string;
  params?: Record<string, string>;
  unsupported?: string[];
}

export interface ManualGuideView {
  steps?: string[];
  export_caption?: string;
  media_note?: string;
}

export interface PlanView {
  kind?: string; // automated | manual_only | refused
  allowed?: boolean;
  reason?: string;
  reason_text?: string;
  notice?: string;
  missing?: string[];
  entry?: PlanEntryView;
  manual?: ManualGuideView;
}

export interface MediaRefView {
  kind?: string;
  asset_id?: string;
  url?: string;
  sha256?: string;
  content_type?: string;
  filename?: string;
  expires_at?: string;
}

export interface SnapshotView {
  pack_id?: string;
  version_id?: string;
  content_kind?: string;
  title?: string;
  body?: string;
  topics?: string[];
  store_name?: string;
  content_version?: string;
}

export interface RoutesResponse {
  platform?: string;
  scenario?: string;
  routes?: { entry_mode?: string; scenario?: string; plan?: PlanView }[];
  notice?: string;
  snapshot?: SnapshotView;
}

export interface StartResponse {
  handoff?: HandoffView;
  plan?: PlanView;
  media?: MediaRefView[];
  recovery?: { token?: string; expires_at?: string; qr_payload?: string };
}

// ---- 文案纪律(唯一出口) ----------------------------------------------------

/**
 * 状态文案的唯一出口。红线:九态里只有 platform_confirmed 的文案出现
 * 「已发布」;其余任何状态(包括未知/超时/待核验)都不得表述为成功。
 */
export function statusCopy(status: string | undefined): string {
  switch (status) {
    case "previewed":
      return "已创建发布交接，正在准备素材。还不是发布成功。";
    case "handoff_preparing":
      return "素材准备中，还没有可以打开的平台入口。还不是发布成功。";
    case "handoff_ready":
      return "素材就绪，可以进入平台发布。进入平台不等于发布成功。";
    case "editor_opened":
      return "已用可验证证据确认平台编辑器打开。打开编辑器不等于发布成功。";
    case "submitting":
      return "内容已交给平台处理，正在等待结果。结果出来前，这不是发布成功。";
    case "awaiting_review":
      return "等待店员核对或官方确认。这不是发布成功。";
    case "platform_confirmed":
      return "平台官方确认已发布。";
    case "canceled":
      return "交接已取消，没有发布，也没有触发任何付费生成。";
    case "failed":
      return "交接失败。失败不会触发任何付费生成，可以重新开始或改用合规导出。";
    case "unknown":
      return "平台结果未知。系统会继续查询；未知不是发布成功，也不会自动重发。";
    default:
      return "状态未知。不会把未知当成成功。";
  }
}

/** 证据来源说明:让顾客知道这条事实是谁给出的。 */
export function evidenceCopy(source: string | undefined): string {
  switch (source) {
    case "customer_action":
      return "事实来源：你的操作申报（不等于官方结果）。";
    case "staff_observation":
      return "事实来源：店员人工观察（不等于官方确认）。";
    case "staff_verification":
      return "事实来源：店员核对作品（不等于官方确认）。";
    case "official_receipt":
      return "事实来源：平台官方回执（服务端验签）。";
    case "system_adapter":
      return "事实来源：系统适配记录。";
    case "system_recovery":
      return "事实来源：系统恢复流程。";
    case "verifier_proof":
      return "事实来源：可验证的打开证据。";
    default:
      return "";
  }
}

/** 顾客「我已完成」的固定回应:申报只是核验请求,不是事实,不发奖。 */
export const DONE_NOTICE =
  "已记录你的完成申报。申报只是核验请求，等待店员核对或官方确认；申报本身不算发布成功，也不触发奖励。";

/** 确认不符(换稿)的固定说明:换稿=新指纹,必须重新确认,不静默更新。 */
export const CONFIRM_MISMATCH_COPY =
  "内容版本与当前快照不符。商家换了稿，需要你重新确认后才能交接；不会静默更新你要发出的内容。";

/** 超时后的固定说明:只查询,不重发(防重复提交)。 */
export function timeoutCopy(timedOut: boolean | undefined, queryCount: number | undefined): string {
  if (!timedOut) return "";
  return `平台处理超时，已自动查询 ${queryCount ?? 0} 次。结果未明前不会重发，也不会算作发布成功。`;
}

// ---- 拒绝面与手动指引 ------------------------------------------------------

/**
 * 手动指引的唯一出口。红线:refused(官方规范排除)一律返回空数组——
 * 即使服务端异常返回了 manual,这里也丢掉,不给被禁玩法留手动后门;
 * manual_only(承接能力未知)才透出合规导出+手动步骤。
 */
export function manualStepsFor(plan: PlanView | undefined | null): string[] {
  if (!plan || plan.kind === "refused") return [];
  if (plan.kind === "manual_only") return plan.manual?.steps ?? [];
  return [];
}

/** 合规导出文案(仅 manual_only;refused 不给)。 */
export function exportCaptionFor(plan: PlanView | undefined | null): string {
  if (!plan || plan.kind === "refused" || plan.kind !== "manual_only") return "";
  return plan.manual?.export_caption ?? "";
}

/** 拒绝面提示(诚实降级:说明原因与官方来源,不承诺手动绕行)。 */
export function refusalNotice(plan: PlanView | undefined | null): string {
  if (!plan || plan.kind !== "refused") return "";
  return plan.notice || plan.reason_text || "该平台此场景不能通过本服务交接。";
}

/** 能力缺项的诚实说明(自动带入做不到的字段)。 */
export function unsupportedLines(entry: PlanEntryView | undefined | null): string[] {
  return entry?.unsupported ?? [];
}

// ---- URL 安全形状 ----------------------------------------------------------

/**
 * 平台入口是否可以打开:h5 只接受 https 直达链接(拒绝 javascript:/data:
 * 与协议相对地址);miniapp 只接受站内路径(以 / 开头,不带 scheme)。
 * 这是客户端预检;权威裁决在服务端。
 */
export function entryOpenable(entry: PlanEntryView | undefined | null): boolean {
  if (!entry) return false;
  if (entry.method === "h5") {
    try {
      const parsed = new URL(entry.url ?? "");
      return parsed.protocol === "https:" && parsed.hostname !== "";
    } catch {
      return false;
    }
  }
  if (entry.method === "miniapp") {
    const p = entry.miniapp_path ?? "";
    return p.startsWith("/") && !p.includes("://") && !p.includes("?") && !p.includes("#");
  }
  return false;
}

/**
 * 受控媒体地址只允许:站内 /api/ 相对路径(同源 BFF)或 https 绝对地址。
 * 拒绝协议相对(//)、data:、javascript: 等;媒体字节不经本服务,这里
 * 只是引用。
 */
export function mediaUrlSafe(url: string | undefined): boolean {
  if (!url) return false;
  if (url.startsWith("/api/")) return true;
  try {
    return new URL(url).protocol === "https:";
  } catch {
    return false;
  }
}
