// HUI-1896 C5 访客本人权益视图语义(纯函数,零 React,零 fetch)。
//
// 消费 1671 访客面 GET /api/v1/visitor/reward-benefits(读路径不经新参与
// Gate:活动结束/订阅到期后旧券仍可查,T16 由服务端保证)。纪律:
//   - 券码掩码只露尾 4 位(与 1671 maskCode 同型);无码给现场说明,不编造;
//   - external_state=pending_unknown 绝不把本系统已发放拖成「奖励未知」
//     (票面硬红线);failed 如实「外部发放没有完成」,不否认本地已发放事实;
//   - expiry_reasons 是六种独立到期的如实标注(query 判定永不拦读);
//   - redeem_blockers 只在确实被拦时给说明,不否定权益存在;
//   - 未知状态 unknown 语气,不伪报。

export interface VisitorBenefitInfo {
  id?: string;
  status?: string;
  code?: string;
  store_id?: string;
  face_value_minor?: number;
  currency?: string;
  issued_at?: string;
  redeemed_at?: string;
}

export interface VisitorBenefitRow {
  benefit: VisitorBenefitInfo;
  expiry_reasons?: string[];
  redeem_allowed?: boolean;
  redeem_blockers?: string[];
  is_external?: boolean;
  external_state?: string;
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** 解析访客权益列表;非对象/缺 items → null;残缺行(id/status 缺)丢弃。 */
export function parseVisitorBenefits(payload: unknown): VisitorBenefitRow[] | null {
  if (payload === null || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  if (!Array.isArray(rec.items)) return null;
  const out: VisitorBenefitRow[] = [];
  for (const item of rec.items) {
    if (item === null || typeof item !== "object") continue;
    const row = item as Record<string, unknown>;
    const benefit = (row.benefit !== null && typeof row.benefit === "object" ? row.benefit : {}) as Record<string, unknown>;
    const id = str(benefit.id);
    const status = str(benefit.status);
    if (id === "" || status === "") continue;
    out.push({
      benefit: {
        id,
        status,
        code: str(benefit.code),
        store_id: str(benefit.store_id),
        face_value_minor: typeof benefit.face_value_minor === "number" ? benefit.face_value_minor : undefined,
        currency: str(benefit.currency),
        issued_at: str(benefit.issued_at),
        redeemed_at: str(benefit.redeemed_at),
      },
      expiry_reasons: Array.isArray(row.expiry_reasons) ? row.expiry_reasons.filter((r): r is string => typeof r === "string") : [],
      redeem_allowed: row.redeem_allowed === true,
      redeem_blockers: Array.isArray(row.redeem_blockers) ? row.redeem_blockers.filter((r): r is string => typeof r === "string") : [],
      is_external: row.is_external === true,
      external_state: str(row.external_state),
    });
  }
  return out;
}

/** 券码掩码:只露尾 4 位;短码全掩;无码给现场说明(不编造)。 */
export function maskBenefitCode(code: string): string {
  const c = (code ?? "").trim();
  if (c === "") return "无码:现场出示本页给店员即可";
  if (c.length <= 4) return "••••";
  return "••••" + c.slice(-4);
}

/** 六种独立到期原因 → 顾客可读解释(逐因如实,不混成一句「已过期」)。 */
export function expiryReasonFace(reason: string): string {
  switch ((reason ?? "").trim()) {
    case "subscription_expired":
      return "商家订阅已到期";
    case "campaign_ended":
      return "活动已结束";
    case "channel_closed":
      return "发放渠道已关闭";
    case "participation_closed":
      return "参与通道已关闭";
    case "reservation_ttl_elapsed":
      return "预留保留期限已过";
    case "benefit_expired":
      return "权益已过有效期";
    default:
      return "到期状态未知";
  }
}

const BLOCKER_EXTRA_FACE: Record<string, string> = {
  external_redeem_local_forbidden: "外部平台券由平台/门店按平台规则核销,本店收银台不直接核销这张。",
};

function blockerFace(blocker: string): string {
  return BLOCKER_EXTRA_FACE[(blocker ?? "").trim()] ?? "";
}

export interface BenefitRowFace {
  status: "issued" | "redeemed" | "unknown";
  lines: string[];
}

/** 单条权益 → 顾客可读行(券码掩码/门店/到期/外部状态;未知状态 unknown)。 */
export function benefitRowFace(row: VisitorBenefitRow): BenefitRowFace {
  const status = (row.benefit.status ?? "").trim();
  const lines: string[] = [];
  const external = row.is_external === true;
  const externalState = (row.external_state ?? "").trim();

  if (status === "redeemed") {
    lines.push("奖励已兑现。这一单到此完成,不用再领一次。");
    return { status: "redeemed", lines };
  }
  if (status === "issued") {
    lines.push(`奖励已发放,可以直接使用(券码 ${maskBenefitCode(row.benefit.code ?? "")})。`);
    if (row.benefit.store_id) lines.push(`适用门店 ${row.benefit.store_id}。`);
    if (external && externalState === "pending_unknown") {
      // 外部确认中:不否认本地已发放事实(红线)。
      lines.push("外部平台发放状态确认中:本系统已记录发放,以平台最终状态与门店现场为准。");
    } else if (external && externalState === "failed") {
      lines.push("外部平台发放没有完成。请把这一页给店员,由门店协助处理;本系统已记录的发放仍然有效。");
    } else if (external) {
      lines.push("这是外部平台券:以平台页面与门店现场为准。");
    }
    if (row.redeem_allowed === false && Array.isArray(row.redeem_blockers)) {
      for (const blocker of row.redeem_blockers) {
        const extra = blockerFace(blocker);
        if (extra !== "") lines.push(extra);
        else lines.push(`暂时不能核销:${expiryReasonFace(blocker)}。权益仍按原记录保留,请把这一页给店员核对。`);
      }
    }
    return { status: "issued", lines };
  }
  lines.push("权益状态未知。请保留本页并请店员核对,这里不会冒充可用。");
  return { status: "unknown", lines };
}
