// HUI-1896 顾客三屏:阶段状态机与文案唯一出口(纯函数,无 React,便于 vitest)。
//
// 纪律(与 3546 participation / 1671 reward-ops / 1670 publish 语义对齐):
//   - 阶段只由「自身参与」推进;普通浏览=browse,不占奖励、不建参与;
//   - 未知参与状态 fail-closed(不认识=不推进,canceled 回 browse 可重开);
//   - 奖励卡文案:本系统已确认发放/redeemed 的表述优先,外部发布 unknown
//     不能把「已发放」拖成「奖励未知」;只有预留说预留,不冒充可领;
//   - 核验三态文案与 1671 evidenceText 对齐:店员见到提交/店员核对作品/
//     官方确认发布;本票不写奖励判定,只如实转述状态;
//   - 未准入渠道不出现「先手动发完再领」类手动后门指引;
//   - 异常矩阵每项都给真实恢复或联系门店动作,不编造成功/失败。

export interface CustomerParticipation {
  id?: string;
  status?: string;
  benefit_reservation_state?: string;
  content_pack_version?: string;
  rule_version?: number;
  starts_at?: string;
  expires_at?: string;
}

export interface RewardBenefitPublic {
  status?: string;
  code?: string;
  store_id?: string;
  valid_until?: string;
}

export interface AdmissionView {
  allowed?: boolean;
  reason?: string;
  reason_text?: string;
  missing?: string[];
}

export interface AttemptRef {
  id?: string;
  status?: string;
}

// 参与阶段:三屏按此渐进呈现(屏1 常驻;started 起显示屏2;发布后屏3 突出)。
export type JourneyStage =
  | "browse"
  | "started"
  | "publishing"
  | "awaiting_review"
  | "reward_ready"
  | "redeemed";

const KNOWN_STATUSES: ReadonlySet<string> = new Set([
  "pending",
  "in_progress",
  "awaiting_review",
  "fulfilled",
  "canceled",
]);

export function journeyStage(
  participation: CustomerParticipation | null | undefined,
  opts: { rewardStatus?: string } = {},
): JourneyStage {
  const status = participation?.status ?? "";
  if (!KNOWN_STATUSES.has(status)) return "browse";
  if (status === "canceled") return "browse";
  if (status === "pending") return "started";
  if (status === "in_progress") return "publishing";
  if (status === "awaiting_review") return "awaiting_review";
  // fulfilled:只有本系统真实发放/核销才进入领奖两态。
  if (opts.rewardStatus === "redeemed") return "redeemed";
  if (opts.rewardStatus === "issued") return "reward_ready";
  return "awaiting_review";
}

// 掩码与 1671 maskCode 同型:只露尾 4 位;无码给现场说明。
function maskCode(code: string): string {
  const c = (code ?? "").trim();
  if (c === "") return "（无码：现场出示参与记录即可）";
  if (c.length <= 4) return "••••";
  return "••••" + c.slice(-4);
}

export function rewardCardCopy(
  benefit: RewardBenefitPublic | null | undefined,
  _external: { externalPublish?: string },
): string {
  const status = (benefit?.status ?? "").trim();
  if (status === "redeemed") {
    return "奖励已兑现。这一单到此完成，不用再领一次。";
  }
  if (status === "issued") {
    const parts = [`奖励已发放，可以直接使用（券码 ${maskCode(benefit?.code ?? "")}）。`];
    if (benefit?.store_id) parts.push(`适用门店 ${benefit.store_id}。`);
    if (benefit?.valid_until) parts.push(`有效期至 ${benefit.valid_until}。`);
    return parts.join("");
  }
  if (status === "reserved") {
    return "奖励还在预留中（未发放）。完成店员确认后才发放。";
  }
  // 无奖励记录/未知状态:不伪报,给人工路径。
  return "还没有记录到可用的奖励。完成后请店员确认，由门店现场发放。";
}

export function evidenceStepCopy(evidence: string | undefined): string {
  switch ((evidence ?? "").trim()) {
    case "staff_observed_submission":
      return "店员见到提交：店员在现场看到你提交，确认后可发优惠。";
    case "staff_verified_post":
      return "店员核对作品：店员查看你的作品后确认，可发优惠。";
    case "official_publish_confirmed":
      return "官方确认发布：平台确认后自动认定，可发优惠。";
    default:
      return "核验方式未认定（还没确认）。请保留参与记录并请店员确认。";
  }
}

// 参与已 fulfilled 但顾客面读不到券码记录(1671 无按访客的权益读面):
// 如实说店员已完成、以现场出示为准;不编造券码,也不伪报「已发放到账」。
export function staffFulfilledCopy(): string {
  return "门店已完成确认。券码或赠品以店员现场出示为准。";
}

// 未准入/未知路线:诚实说明不可用原因;绝不出「先手动发布完再来领」的
// 手动后门(平台限制不藏到领取最后一步)。
export function admissionLines(admission: AdmissionView | null | undefined): string[] {
  if (!admission) {
    return ["渠道放行状态暂时读不到。请以门店现场说明为准，或请店员协助确认。"];
  }
  if (admission.allowed) return [];
  const text = (admission.reason_text ?? "").trim() || "该渠道路线暂未放行，这一步不能通过本服务开始。";
  return [`${text}。可以先看活动和内容，发布方式请听门店现场安排。`];
}

export function stockLine(availableQty: number | undefined): string {
  if (availableQty == null || !Number.isFinite(availableQty)) return "";
  if (availableQty <= 0) return "本档奖励已用完。可以先参与，发放以门店现场安排为准。";
  if (availableQty === 1) return "本档奖励只剩最后一份，先到先得，领完即止。";
  return `本档奖励余量 ${availableQty} 份，领完即止。`;
}

// 异常矩阵:每项给真实恢复动作或联系门店动作;网络未知不判成功/失败。
const EXCEPTION_COPY: Record<string, string> = {
  app_missing: "手机上还没有这个平台的应用。可以换一个已放行的平台，或把内容保存后按门店现场方式参与。",
  version_unsupported: "当前应用版本不支持这一步。可以升级应用后重试，或请店员协助现场确认。",
  auth_denied: "你没有允许平台授权。这一步不会替你发布；可以取消，换一个平台，或请店员确认其他参与方式。",
  download_failed: "内容获取没有完成。可以重新试一次或换选一个内容；不影响你已开始的参与。",
  user_canceled: "你已取消这一步。已发生的参与记录保留，可以重新开始或请店员协助。",
  reviewing: "还在核对中。可以稍后回来刷新状态，或出示参与码请店员确认。",
  page_lost: "页面找不到了。用保存的恢复入口重新打开，或请店员按你的参与码恢复。",
  network_unknown: "网络没有给出明确结果。先当作还没完成；稍后重试即可，参与记录不会因此丢失。",
  reward_exhausted: "这一档奖励已经用完。可以把情况告诉店员，由门店现场决定其他安排。",
  campaign_unavailable: "活动暂停或已结束。已开始的参与仍可按原规则核验，请出示参与码联系店员。",
  last_stock: "这一档只剩最后一份，先到先得。是否还能领取以店员确认为准。",
  bad_code: "这个码无法识别或已过期。请核对二维码/标签，或请店员重新提供活动入口。",
};

export function exceptionCopy(kind: string): string {
  return EXCEPTION_COPY[(kind ?? "").trim()] ?? "";
}

// 换选规则:未交接草稿可换;存在 handed_off attempt 或参与已终态=冻结
//(attempt 内容版本已冻结,换选不改变已交接版本)。
export function canSwitchDraft(
  participation: CustomerParticipation | null | undefined,
  attempts: AttemptRef[],
): boolean {
  if (!participation) return false;
  const status = participation.status ?? "";
  if (status === "fulfilled" || status === "canceled") return false;
  if (!KNOWN_STATUSES.has(status)) return false;
  return !attempts.some((a) => (a.status ?? "") === "handed_off");
}

// ---- 参与门面(3546 消费面;只走同源 BFF /api/*) --------------------------------
//
// 纪律:
//   - lookup 404 = 这个浏览器还没有参与 = 全新开始,不是错误态;
//   - 开始是显式动作(按钮),浏览路径绝不调用 startParticipation;
//   - 幂等键按活动码在会话内稳定:同一浏览器重复开始重放回原记录,
//     不会开出第二条参与;键只在内存,不落 localStorage;
//   - 网络失败返回 {error:"network"}:不判成功也不判失败(异常矩阵纪律);
//   - qr_payload/token 只经返回值进内存,组件一次性出示。

export type ParticipationResult =
  | CustomerParticipation
  | { error: "network" }
  | { error: "start_failed" | "frozen"; message: string };

async function postJson(url: string, body: unknown, method = "POST"): Promise<{ status: number; data: Record<string, unknown> | null }> {
  try {
    const res = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = (await res.json().catch(() => null)) as Record<string, unknown> | null;
    return { status: res.status, data };
  } catch {
    return { status: 0, data: null };
  }
}

function asParticipation(data: Record<string, unknown> | null): CustomerParticipation | null {
  if (data && typeof data.id === "string" && typeof data.status === "string") {
    return data as CustomerParticipation;
  }
  return null;
}

function messageOf(data: Record<string, unknown> | null): string {
  const m = data && typeof data.message === "string" ? data.message : "";
  return m || "这一步没有完成。可以稍后再试，或请店员协助。";
}

/** 同设备重复碰触恢复:优先返回未完参与;404=全新开始(null);网络={error:"network"}。 */
export async function lookupParticipation(code: string): Promise<ParticipationResult | null> {
  const { status, data } = await postJson("/api/participations/lookup", { link_code: code });
  if (status === 0) return { error: "network" };
  if (status === 404) return null;
  return asParticipation(data) ?? { error: "network" };
}

// 会话级幂等键:Map<link_code, key>。组件生命周期内稳定,刷新即新会话
//(服务端另有 visitor cookie 兜底恢复,幂等键只防同会话双击/重试双开)。
const idempotencyKeys = new Map<string, string>();

function idempotencyKeyFor(code: string): string {
  let key = idempotencyKeys.get(code);
  if (!key) {
    key = "cj-web-" + code + "-" + Math.random().toString(36).slice(2, 12) + Date.now().toString(36);
    idempotencyKeys.set(code, key);
  }
  return key;
}

/** 显式开始参与(并申请预留;预留结果以 benefit_reservation_state 为准)。 */
export async function startParticipation(code: string, contentPackVersion: string): Promise<ParticipationResult | null> {
  const { status, data } = await postJson("/api/participations", {
    link_code: code,
    touchpoint_source: "manual",
    content_pack_version: contentPackVersion,
    idempotency_key: idempotencyKeyFor(code),
  });
  if (status === 0) return { error: "network" };
  if (status === 200 || status === 201) {
    return asParticipation(data) ?? { error: "start_failed", message: messageOf(data) };
  }
  return { error: "start_failed", message: messageOf(data) };
}

/** 未交接草稿换选;冻结(frozen 422)返回错误,不动已交接 attempt。 */
export async function switchDraft(id: string, contentPackVersion: string): Promise<ParticipationResult | null> {
  const { status, data } = await postJson(`/api/participations/${encodeURIComponent(id)}/content`, {
    content_pack_version: contentPackVersion,
  }, "PATCH");
  if (status === 0) return { error: "network" };
  if (status === 200) return asParticipation(data) ?? { error: "network" };
  return { error: "frozen", message: messageOf(data) };
}

/** 用户取消:参与置 canceled(终态),不抹已发生记录。 */
export async function cancelParticipation(id: string): Promise<ParticipationResult | null> {
  const { status, data } = await postJson(`/api/participations/${encodeURIComponent(id)}/cancel`, {});
  if (status === 0) return { error: "network" };
  if (status === 200) return asParticipation(data) ?? { error: "network" };
  return { error: "start_failed", message: messageOf(data) };
}

export interface ResumeCredential {
  credential_id?: string;
  token?: string;
  qr_payload?: string;
  expires_at?: string;
  participation?: CustomerParticipation;
}

/** 参与码/恢复凭据:响应只出现一次,由调用方当次出示;本模块不落盘。 */
export async function resumeCredential(id: string): Promise<ResumeCredential | null> {
  const { status, data } = await postJson(`/api/participations/${encodeURIComponent(id)}/handoff`, {
    purpose: "customer_resume",
  });
  if (status !== 201 || !data) return null;
  return data as ResumeCredential;
}
