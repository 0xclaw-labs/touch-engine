// HUI-3546 碰一碰门店交付 A2: 参与记录与跨端恢复的视图语义。
//
// 服务端(Go /api/v1/participations*)是唯一权威;本模块只承载两件事:
//   1. 参与视图的类型与状态判定(恢复/可续接/终态);
//   2. 文案纪律:benefit_reservation_state 只有 confirmed 才允许说
//      「已保留」;pending/failed/not_requested 一律不得伪报——这是
//      票面红线,由 benefitCopy 白名单化输出兜底。
//
// 交接凭据(token)只经内存传给 BFF 代理,永不写进 URL 查询串、
// 永不进 localStorage;参与二维码的 qr_payload 由服务端签发。

export interface ParticipationView {
  id?: string;
  status?: string;
  link_code?: string;
  content_pack_version?: string;
  rule_version?: number;
  benefit_reservation_state?: string;
  starts_at?: string;
  expires_at?: string;
  created_at?: string;
  attempts?: AttemptView[];
}

export interface AttemptView {
  id?: string;
  platform?: string;
  handoff_method?: string;
  content_pack_version?: string;
  rule_version?: number;
  status?: string;
  created_at?: string;
}

export interface HandoffCredentialView {
  credential_id?: string;
  purpose?: string;
  target_scope?: string;
  expires_at?: string;
  token?: string;
  qr_payload?: string;
  participation?: ParticipationView;
}

// 参与状态(与服务端五态一一对应;未知状态按不可续接处理,fail-closed)。
export type ParticipationStatus =
  | "pending"
  | "in_progress"
  | "awaiting_review"
  | "fulfilled"
  | "canceled";

const OPEN_STATUSES: ReadonlySet<string> = new Set(["pending", "in_progress", "awaiting_review"]);

export function isKnownStatus(status: string | undefined): boolean {
  return (
    status === "pending" ||
    status === "in_progress" ||
    status === "awaiting_review" ||
    status === "fulfilled" ||
    status === "canceled"
  );
}

/** 未完参与:同设备重复碰触时可恢复。终态(canceled/fulfilled)不可恢复。 */
export function canResume(view: ParticipationView | null | undefined): boolean {
  return view != null && OPEN_STATUSES.has(view.status ?? "");
}

/** 顾客此刻能否发起交接(未完 + 有效窗口内)。 */
export function canHandoff(view: ParticipationView | null | undefined, now = new Date()): boolean {
  if (!canResume(view)) return false;
  const endsAt = view?.expires_at;
  if (!endsAt) return true;
  const end = Date.parse(endsAt);
  return Number.isFinite(end) && now.getTime() <= end;
}

/** 交接凭据是否仍是"活"的(客户端预检;权威判定在服务端)。 */
export function credentialLooksLive(
  cred: HandoffCredentialView | null | undefined,
  now = new Date(),
): boolean {
  if (!cred?.expires_at || !cred.token) return false;
  const end = Date.parse(cred.expires_at);
  return Number.isFinite(end) && now.getTime() <= end;
}

const BENEFIT_COPY_CONFIRMED = "奖励名额已保留，请继续发布";
const BENEFIT_COPY_PENDING = "正在为你确认奖励名额…";
const BENEFIT_COPY_FAILED = "奖励名额暂时没有确认，稍后重试即可，不影响这次参与";
const BENEFIT_COPY_NONE = "";

/**
 * 权益预留文案的唯一出口。返回 null 表示「不可向顾客提及保留」:
 * 服务端未确认(not_requested/pending)或确认失败(failed)时,
 * 页面绝不出现「已保留」。
 */
export function benefitCopy(state: string | undefined): string | null {
  switch (state) {
    case "confirmed":
      return BENEFIT_COPY_CONFIRMED;
    case "pending":
      return BENEFIT_COPY_PENDING;
    case "failed":
      return BENEFIT_COPY_FAILED;
    default:
      return BENEFIT_COPY_NONE;
  }
}

/** 状态中文标签(公共页/店员侧共用;不做 success 布尔抹平)。 */
export function statusLabel(status: string | undefined): string {
  switch (status) {
    case "pending":
      return "已开始，待发布";
    case "in_progress":
      return "发布进行中";
    case "awaiting_review":
      return "待店员确认";
    case "fulfilled":
      return "已完成";
    case "canceled":
      return "已取消";
    default:
      return "状态未知";
  }
}

/** 参与二维码载荷必须是规范 URL(服务端签发);这里只做形状校验。 */
export function isSafeQrPayload(payload: string | undefined): boolean {
  if (!payload) return false;
  if (!payload.startsWith("https://") && !payload.startsWith("http://")) return false;
  if (payload.includes("?") || payload.includes("#")) return false;
  return payload.includes("/p/ptx_");
}
