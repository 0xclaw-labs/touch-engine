// HUI-1896 C2 发布交接展示面:1670 handoff v2 契约的顾客侧解析与文案
// (纯函数,零 fetch)。请求执行(POST /api/v1/public/actions/{actionId}/handoffs
// / GET /api/v1/public/handoffs/{handoffId})归 C2 视图组件;本模块只管
// 「契约形状 → 顾客可读状态」。
//
// 契约(票面;1670 并行实施中,基线尚无该端点):
//   请求: action_session_id, action_revision, content_pack_version(payload_kind
//         =media 必填;link_share 可空), method, confirmation, idempotency_key,
//         participation_id 可空。
//   响应: plan.kind = refused | manual_only | official_handoff | oauth_publish
//         + client_action + unsupported_fields + evidence_level;
//         续查 GET handoffs/{handoffId} 的 handoff/attempt 状态。
//
// 纪律(硬红线):
//   - 未知 kind/状态 → unknown 语气,绝不渲染成功绿态;
//   - refused 不给「先手动发完再领」类手动后门指引(未准入渠道不绕过);
//   - 点击/唤起/媒体交接 ≠ 平台成功:evidence_level 只如实转述;
//   - unsupported_fields 逐项列出(不让顾客以为字段都带过去了);
//   - 本模块契约 fixture 只进 tests/(本地契约验证),待 1670 真实提供者联验。

export type PlanKind = "refused" | "manual_only" | "official_handoff" | "oauth_publish" | "unknown";

export interface HandoffClientAction {
  type: string;
  url: string;
  note: string;
}

export interface HandoffPlanView {
  handoffId: string;
  kind: PlanKind;
  reason: string;
  reasonText: string;
  instructions: string;
  clientAction: HandoffClientAction | null;
  unsupportedFields: string[];
  evidenceLevel: string;
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function parseClientAction(raw: unknown): HandoffClientAction | null {
  if (raw === null || typeof raw !== "object") return null;
  const rec = raw as Record<string, unknown>;
  return {
    type: str(rec.type),
    url: /^https:\/\//.test(str(rec.url)) ? str(rec.url) : "",
    note: str(rec.note),
  };
}

/** 解析 1670 plan;非对象/缺 plan → null;未知 kind → "unknown"(fail-closed)。 */
export function parseHandoffPlan(payload: unknown): HandoffPlanView | null {
  if (payload === null || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  if (rec.plan === null || typeof rec.plan !== "object") return null;
  const plan = rec.plan as Record<string, unknown>;
  const rawKind = str(plan.kind);
  const kind: PlanKind = ["refused", "manual_only", "official_handoff", "oauth_publish"].includes(rawKind)
    ? (rawKind as PlanKind)
    : "unknown";
  const fields = Array.isArray(plan.unsupported_fields)
    ? plan.unsupported_fields.filter((f): f is string => typeof f === "string" && f.trim() !== "").map((f) => f.trim())
    : [];
  return {
    handoffId: str(rec.handoff_id),
    kind,
    reason: str(plan.reason),
    reasonText: str(plan.reason_text),
    instructions: str(plan.instructions),
    clientAction: parseClientAction(plan.client_action),
    unsupportedFields: fields,
    evidenceLevel: str(plan.evidence_level),
  };
}

const EVIDENCE_FACE: Record<string, string> = {
  click_only: "记录到的只是点击",
  manual_evidence: "以顾客手动操作与门店核对为准",
  staff_verified: "需要店员核对确认",
  platform_receipt: "以平台官方回执为准",
};

/** plan.kind → 顾客文案(unknown 永远未知语气,不渲染成功)。 */
export function planCopy(kind: PlanKind): string {
  switch (kind) {
    case "official_handoff":
      return "可以把内容交到平台发布页。发布是否完成以平台为准,这一步不会替你发布。";
    case "oauth_publish":
      return "需要你先授权平台,之后由服务端投稿。授权可以取消,发布结果以平台为准。";
    case "manual_only":
      return "这个平台暂时没有在线发布路线。可以保存素材后手动发布,或请店员协助确认。";
    case "refused":
      return "这一步在当前渠道未放行。可以先看门店内容,发布方式请听门店现场安排。";
    default:
      return "发布路线状态未知。这一步还没有被确认,请稍后重试或请店员协助。";
  }
}

/** evidence_level → 证据口径转述(不升级为平台成功)。 */
export function evidenceFace(level: string): string {
  return EVIDENCE_FACE[(level ?? "").trim()] ?? "完成口径未认定:请保留记录并请店员确认。";
}

// ---- 交接状态(续查 GET /api/v1/public/handoffs/{handoffId}) --------------------

export interface HandoffStatusView {
  handoffId: string;
  status: string;
  attemptStatus: string;
}

export function parseHandoffStatus(payload: unknown): HandoffStatusView | null {
  if (payload === null || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  const attempt = (rec.attempt ?? null);
  const attemptRec = attempt && typeof attempt === "object" ? (attempt as Record<string, unknown>) : {};
  return {
    handoffId: str(rec.handoff_id),
    status: str(rec.status),
    attemptStatus: str(attemptRec.status),
  };
}

/**
 * 交接状态 → 顾客文案。等待/完成/失败/未知四档:
 *   - 只有平台官方回执态才算「平台确认」;顾客自报/导出/页面隐藏永不升级;
 *   - unknown 状态未知语气(绝不渲染成功绿态)。
 */
export function statusFace(status: string): string {
  switch ((status ?? "").trim()) {
    case "pending":
    case "pending_external":
      return "发布结果等待平台确认。可以先返回门店页,稍后回来查看,或请店员协助。";
    case "platform_confirmed":
    case "confirmed":
      return "平台已确认发布。可以请店员核对,领取条件以门店活动说明为准。";
    case "manual_completed":
      return "按手动路线完成。是否达到活动条件以店员核对为准。";
    case "failed":
    case "refused":
      return "这一步没有完成。可以重试或换一个路线;已开始的参与记录保留。";
    case "canceled":
      return "已取消。已发生的记录保留,可以重新开始或请店员协助。";
    default:
      return "交接状态未知。这一步还没有被确认,不算发布成功;请稍后刷新或请店员协助。";
  }
}
