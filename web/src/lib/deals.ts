// HUI-4590: 团购套餐（美团/大众点评/抖音）展示助手。
// 诚实纪律：点击不是成交；价格只在核对未过期时可信（show），否则「到平台查看」；
// 外部券核销事实只来自授权适配器，未接通/未授权/未知都不写成已核销。

export interface StoreDealOfferView {
  id: string;
  platform: string;
  provider_offer_id?: string;
  product_url: string;
  title: string;
  image_ref?: string;
  price_minor: number;
  currency?: string;
  price_source?: string;
  price_verified_at?: string;
  price_state?: string; // show | platform
  valid_from?: string;
  valid_until?: string;
  status: string;
  authorization_ref?: string;
  note?: string;
  created_at?: string;
  updated_at?: string;
  store_ids?: string[];
  click_total?: number;
}

export interface DealCouponResultView {
  attempt_id?: string;
  outcome?: string;
  state?: string;
  detail?: string;
  valid?: boolean;
}

export function platformText(platform: string): string {
  switch (platform) {
    case "meituan":
      return "美团";
    case "dianping":
      return "大众点评";
    case "douyin":
      return "抖音";
    default:
      return "未知平台";
  }
}

export function offerStatusText(status: string): string {
  switch (status) {
    case "active":
      return "在售";
    case "paused":
      return "已下架";
    case "retired":
      return "已退役";
    default:
      return "状态未知";
  }
}

export function priceStateText(state: string | undefined): string {
  return state === "show" ? "价格已核对（顾客可见）" : "到平台查看（未核对或已超过 72 小时）";
}

export function verifiedAtText(verifiedAt: string | undefined): string {
  if (!verifiedAt) return "未核对";
  return `核对于 ${verifiedAt}`;
}

// 核销结果文案：已核销/已核销过（回读）是两条事实；failed 是平台的真实答案
// （不是本地伪造）；unknown 是待确认（结果已留痕，稍后对同一券码再核销会先
// 回查同一请求，绝不重发核销）。inspect 响应（valid/detail）同样只复述平台事实。
export function couponOutcomeText(res: DealCouponResultView): string {
  if (res.outcome === undefined && typeof res.valid === "boolean") {
    return res.valid
      ? "外部平台确认：券当前可用"
      : `外部平台确认：券当前不可用${res.detail ? `（${res.detail}）` : ""}`;
  }
  switch (res.outcome) {
    case "redeemed":
      return "外部平台确认已核销（一条平台事实）";
    case "already_redeemed":
      return "该券已核销过，回读已处理结果，没有第二次核销";
    case "failed":
      return `外部平台返回失败${res.detail ? `：${res.detail}` : ""}（已留痕，可重试）`;
    case "unknown":
      return `平台没有给出结果（待确认，不是已核销）。结果已留痕${res.attempt_id ? `（${res.attempt_id}）` : ""}；稍后对同一券码再次核销会自动回查同一请求，不会重复核销。`;
    default:
      return res.detail || "结果未知（未知不是已核销）";
  }
}
