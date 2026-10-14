// HUI-1671 B3: presentation helpers for the real /work/rewards management
// page. Honest wording discipline: an unknown or pending state is never drawn
// as issued or redeemed; face value and cost stay two separate numbers; the
// coupon code is masked until staff deliberately reveals it.

export interface RewardInventoryView {
  id: string;
  kind: string;
  name: string;
  benefit_slot: string;
  rule_version: number;
  allowed_evidence: string[];
  rule_ref?: string;
  total_qty: number;
  available_qty: number;
  face_value_minor: number;
  cost_minor: number;
  currency?: string;
  store_id?: string;
  valid_from?: string;
  valid_until?: string;
  status: string;
  note?: string;
}

export interface RewardBenefitView {
  id: string;
  participation_id: string;
  benefit_slot: string;
  rule_version: number;
  evidence: string;
  code?: string;
  status: string;
  store_id?: string;
  face_value_minor: number;
  cost_minor: number;
  currency?: string;
  reserved_until?: string;
  issued_at?: string;
  redeemed_at?: string;
  reverse_reason?: string;
}

export interface RewardSummaryView {
  campaign_id: string;
  as_of?: string;
  counts?: Record<string, number>;
  evidence_granted?: Record<string, number>;
  pending_review?: number;
  committed_face_minor?: number;
  committed_cost_minor?: number;
  pending_face_minor?: number;
  pending_cost_minor?: number;
}

export interface RewardReviewRequestView {
  attempt_id: string;
  participation_ref: string;
  platform: string;
  created_at: string;
}

const STATUS_TEXT: Record<string, string> = {
  reserved: "预留中（未发放）",
  issued: "已发放（可核销）",
  redeemed: "已核销",
  reversed: "已更正撤销（终态）",
  expired: "预留已过期（未发放）",
};

export function benefitStatusText(status: string): string {
  const text = STATUS_TEXT[(status ?? "").trim()];
  return text ?? `未知状态（${status || "空"}），不是已核销。`;
}

const EVIDENCE_TEXT: Record<string, string> = {
  staff_observed_submission: "店员见到提交",
  staff_verified_post: "店员核对作品",
  official_publish_confirmed: "官方确认发布",
};

export function evidenceText(evidence: string): string {
  const text = EVIDENCE_TEXT[(evidence ?? "").trim()];
  return text ?? (evidence ? `未知证据（${evidence}）` : "未认定（还没确认）");
}

export function maskCode(code: string): string {
  const c = (code ?? "").trim();
  if (c === "") return "（无码：现场赠品/折扣）";
  if (c.length <= 4) return "••••";
  return "••••" + c.slice(-4);
}

const KIND_TEXT: Record<string, string> = {
  store_coupon: "门店券",
  imported_code: "导入券码池",
  gift_discount: "现场赠品/折扣",
};

export function inventoryKindText(kind: string): string {
  return KIND_TEXT[(kind ?? "").trim()] ?? `未知类型（${kind}）`;
}

const SLOT_TEXT: Record<string, string> = {
  first_publish: "首发发布",
};

export function slotText(slot: string): string {
  return SLOT_TEXT[(slot ?? "").trim()] ?? slot;
}

// 面额是营销口径,成本是实际口径,两个数永远分开披露。
export function faceAndCostText(faceMinor: number, costMinor: number, currency?: string): string {
  const unit = (currency ?? "").trim() || "分";
  return `面额 ${faceMinor} ${unit}（不是成本），成本 ${costMinor} ${unit}。`;
}

export function inventoryLine(inv: RewardInventoryView): string {
  const parts = [
    inv.name,
    `${inventoryKindText(inv.kind)}`,
    `slot ${slotText(inv.benefit_slot)}（规则 v${inv.rule_version}）`,
    `余量 ${inv.available_qty}/${inv.total_qty}`,
    `状态 ${inv.status}`,
  ];
  if (inv.store_id) parts.push(`适用门店 ${inv.store_id}`);
  if (inv.valid_until) parts.push(`有效期至 ${inv.valid_until}`);
  return parts.join(" · ");
}

export function summaryLines(summary: RewardSummaryView): string[] {
  const counts = summary.counts ?? {};
  const evidence = summary.evidence_granted ?? {};
  const lines = [
    `预留 ${counts.reserved ?? 0}`,
    `已发放 ${counts.issued ?? 0}`,
    `已核销 ${counts.redeemed ?? 0}`,
    `已更正撤销 ${counts.reversed ?? 0}`,
    `预留过期 ${counts.expired ?? 0}`,
    `—— 证据（发放+核销口径）：`,
    `店员见到提交 ${evidence.staff_observed_submission ?? 0}`,
    `店员核对作品 ${evidence.staff_verified_post ?? 0}`,
    `官方确认 ${evidence.official_publish_confirmed ?? 0}`,
    `待核验（顾客自报，未处理） ${summary.pending_review ?? 0}`,
  ];
  return lines;
}

// 店员不因核销获得预算权:批次与更正撤销是 org_owner 专属;agent 一律不可。
export function canManageInventory(role: string): boolean {
  return (role ?? "").trim() === "org_owner";
}

export function canReverseRewards(role: string): boolean {
  return (role ?? "").trim() === "org_owner";
}

export function canConfirmRewards(role: string): boolean {
  const r = (role ?? "").trim();
  return r === "org_owner" || r === "staff" || r === "store_manager";
}

export interface ConfirmDraft {
  campaignId: string;
  participationId: string;
  benefitSlot: string;
  ruleVersion: number;
  evidence: string;
  storeId: string;
  fulfillNow: boolean;
  note?: string;
}

// 事件键在浏览器生成一次:同一确认重放由后端账本去重,不同确认拿新键。
export function confirmRequestBody(draft: ConfirmDraft): Record<string, unknown> {
  const eventId = `cfm-${draft.participationId}-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`;
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
    event_id: `rdm-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`,
    store_id: storeId,
  };
}

export function reverseRequestBody(reason: string): Record<string, unknown> {
  return {
    reason,
    event_id: `rvs-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`,
  };
}
