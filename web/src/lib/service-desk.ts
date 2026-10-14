// HUI-3562 店员确认与核销台(/work/service)的纯函数语义层。
//
// 服务端(1671 账本 + 3546 参与)是唯一权威;本模块只承载:
//   1. 参与码/凭据的提取与形状预检(校验位段在打网前先本地拒);
//   2. 证据选项派生——只来自活动冻结规则(批次快照 allowed_evidence);
//      official_publish_confirmed 永远不是店员可点选项(只读展示);
//   3. 结果文案纪律——未知/重放不是新发放;断网未知先回查,
//      永不提示顾客"重新领";
//   4. 本店作用域读取(店经理只见本店权益记录)。
//
// 与 lib/reward-ops.ts 的分工:那边是 1671 管理台(/work/rewards)的
// 展示助手;这边只服务店员台的窄交互,不重复定义状态机。

export interface ServiceBatch {
  id: string;
  kind: string;
  name: string;
  benefit_slot: string;
  rule_version: number;
  allowed_evidence: string[];
  total_qty: number;
  available_qty: number;
  status: string;
  store_id?: string;
  valid_from?: string;
  valid_until?: string;
  face_value_minor?: number;
  cost_minor?: number;
  currency?: string;
}

export interface ParticipationCard {
  id: string;
  campaign_id: string;
  store_id: string;
  status?: string;
  rule_version?: number;
  link_code?: string;
  content_pack_version?: string;
  benefit_reservation_state?: string;
  starts_at?: string;
  expires_at?: string;
  created_at?: string;
  attempts?: Array<Record<string, unknown>>;
}

const HANDOFF_TOKEN = /^ptx_[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{1}$/;
const HANDOFF_PATH = /\/p\/(ptx_[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{1})/;

// 店员两类可点证据;official 只读(系统事实,人工不能代为确认)。
const STAFF_EVIDENCE = ["staff_observed_submission", "staff_verified_post"] as const;
export const EVIDENCE_OFFICIAL = "official_publish_confirmed";

/** 从扫到的二维码载荷或手输文本提取核验凭据;非规范输入返回 null(不猜)。 */
export function extractHandoffToken(input: string): string | null {
  const raw = (input ?? "").trim();
  if (raw === "") return null;
  if (HANDOFF_TOKEN.test(raw)) return raw;
  const hit = raw.match(HANDOFF_PATH);
  return hit ? hit[1] : null;
}

/** 打网前的形状预检:22 位随机体 + 1 位校验位。预检只是预检,权威在服务端。 */
export function looksLikeHandoffToken(token: string): boolean {
  return HANDOFF_TOKEN.test((token ?? "").trim());
}

export interface EvidenceChoices {
  /** 店员可点选项(只来自批次冻结规则的店员两类)。 */
  selectable: Array<{ value: string; label: string }>;
  /** 规则是否声明官方确认(只读展示一行)。 */
  officialReadOnly: boolean;
  allowed: string[];
}

const EVIDENCE_LABEL: Record<string, string> = {
  staff_observed_submission: "店员见到顾客提交",
  staff_verified_post: "店员核对顾客作品页面",
  official_publish_confirmed: "系统已有平台官方确认",
};

/** 证据选项白名单:规则没给的证据不可选(越界不可选),official 永不可点。 */
export function evidenceChoices(allowedEvidence: string[]): EvidenceChoices {
  const allowed = (allowedEvidence ?? []).filter(Boolean);
  const selectable = STAFF_EVIDENCE.filter((value) => allowed.includes(value)).map((value) => ({
    value,
    label: EVIDENCE_LABEL[value] ?? value,
  }));
  return { selectable, officialReadOnly: allowed.includes(EVIDENCE_OFFICIAL), allowed };
}

/**
 * 主动作禁用原因。null = 可以确认。官方专属规则:人工不能代为确认;
 * 未选证据:先核对再确认。
 */
export function confirmDisabledReason(choices: EvidenceChoices, selected?: string): string | null {
  if (choices.selectable.length === 0 && choices.officialReadOnly) {
    return "这场活动只认系统已有的平台官方确认，人工不能代为确认；官方回执到达后由系统处理。";
  }
  if (!selected) {
    return "先当面核对顾客的操作，再选择以哪一种证据确认。";
  }
  return null;
}

/** 参与卡可用的批次:冻结规则版本一致 + 门店作用域相符 + 批次可用。 */
export function matchBatchesForCard(batches: ServiceBatch[], card: { rule_version?: number; store_id: string }): ServiceBatch[] {
  return (batches ?? []).filter(
    (b) =>
      b.status === "active" &&
      b.rule_version === card.rule_version &&
      (!b.store_id || b.store_id === card.store_id),
  );
}

/** 结果文案:诚实回读。replay/already_* 都不是"又发了一份"。 */
export function outcomeNotice(outcome: string, applied: boolean, replay: boolean): { text: string; ok: boolean } {
  if (outcome === "issued_and_redeemed" && applied) {
    return { text: "确认并现场兑现完成：同一事务记了发放与核销两条事实。", ok: true };
  }
  if (outcome === "issued" && applied && !replay) {
    return { text: "已发放真实权益（一条发放事实）。", ok: true };
  }
  if (outcome === "already_issued" || replay) {
    return { text: "这个参与已经处理过：回读已处理结果，没有第二份。", ok: true };
  }
  return { text: "这次没有完成。没有事实不写成功，可以再试一次。", ok: false };
}

/** 断网/未知后的回查文案。查无事实才提示可安全重试——绝不提示"重新领"。 */
export function networkUnknownCopy(found: boolean): string {
  if (found) {
    return "网络恢复后已回读到处理结果，按已处理显示。";
  }
  return "刚才那次操作结果未知，回查没有查到记录。可以安全重试：同一次确认用同一个回执号，不会发两份。";
}

export interface RedeemRecordLike {
  status?: string;
  redeemed_by?: string;
  redeemed_at?: string;
}

/** 核销事实展示:处理人+时间;未核销时不说已核销。 */
export function redeemRecordText(ben: RedeemRecordLike): string {
  if (ben.status !== "redeemed") {
    return "还没有核销记录。";
  }
  return `已核销 · 处理人 ${ben.redeemed_by || "未知"} · 时间 ${ben.redeemed_at || "未知"}`;
}

export interface BenefitStoreLike {
  id: string;
  store_id?: string;
}

/** 本店作用域读取:店经理只见本店;总部级未绑定门店的记录对店经理不可见
 * (与 authz 矩阵同口径:StoreID="" = 总部级,门店经理不可见);总部看全量。 */
export function ownStoreBenefits<T extends BenefitStoreLike>(items: T[], storeScope: string): T[] {
  if (!storeScope) return items ?? [];
  return (items ?? []).filter((b) => b.store_id === storeScope);
}

export interface ConfirmDraft {
  participationId: string;
  benefitSlot: string;
  ruleVersion: number;
  evidence: string;
  storeId: string;
  fulfillNow: boolean;
  note?: string;
}

// 事件键在浏览器生成一次:同一确认重放由后端账本去重;现场兑现另配核销键。
export function confirmRequestBody(draft: ConfirmDraft): Record<string, unknown> {
  const eventId = `svc-${draft.participationId}-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`;
  return {
    participation_id: draft.participationId,
    benefit_slot: draft.benefitSlot,
    rule_version: draft.ruleVersion,
    event_id: eventId,
    evidence: draft.evidence,
    store_id: draft.storeId,
    note: draft.note ?? "",
    fulfill_now: draft.fulfillNow,
    redeem_event_id: eventId + "-redeem",
  };
}

export function redeemRequestBody(storeId: string): Record<string, unknown> {
  return {
    event_id: `svr-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`,
    store_id: storeId,
  };
}

export function reverseRequestBody(reason: string): Record<string, unknown> {
  return {
    reason,
    event_id: `svx-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`,
  };
}
