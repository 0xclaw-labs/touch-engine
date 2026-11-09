// HUI-4591 M12 账单域视图模型与文案收敛。
//
// 纪律(裁定书):
//   - 本仓只有投影:订阅/订单/退款都是公共订单权威的诚实投影;文案永远不说
//     "已扣款/已到账"这类本仓无权断言的话,支付状态只复述权威事实。
//   - 四态开通(processing/opening/active/failed)文案可区分;浏览器返回不当
//     支付证明(页面只有读面回查)。
//   - 硬件五状态独立展示;AI 用量浏览/复制不计费;退款发票逐字段可查。

export interface SubscriptionPane {
  subscription_ref: string;
  plan_code: string;
  pricing_version: string;
  status: string;
  stored_status: string;
  paid_until: string;
  retain_until: string;
  store_limit: number;
  entitlement_projection: string;
  restricts_new_premium: boolean;
  locks_existing: boolean;
}

export interface StoreUsage {
  limit: number;
  live: number;
}

export interface CheckoutRef {
  available: boolean;
  app_id: string;
  launch_target_id: string;
}

export interface SubscriptionView {
  subscription: SubscriptionPane;
  store_usage: StoreUsage;
  checkout: CheckoutRef;
}

export interface CatalogEntryView {
  id: string;
  scope_tenant: string;
  plan_code: string;
  pricing_version: string;
  price_ref: string;
  currency: string;
  price_minor: number;
  service_days: number;
  included_store_count: number;
  extra_store_price_minor: number;
  grace_days: number;
  effective_from: string;
  effective_to: string;
}

export interface BillingOrderView {
  id: string;
  kind: string; // subscription | hardware
  action: string; // new | renew | expand | downgrade | purchase
  plan_code: string;
  amount_minor: number;
  currency: string;
  status: string; // processing | opening | active | failed | cancelled
  order_ref: string;
  payment_ref: string;
  payer_grant_ref?: string;
  agency_relation_ref?: string;
  payer_principal?: string;
  actor_principal: string;
  actor_role: string;
  fail_reason?: string;
  created_at: string;
  updated_at: string;
}

export interface HardwareStepView {
  state: string;
  operator: string;
  occurred_at: string;
  device_ref: string;
}

export interface HardwareFulfillmentView {
  payment: HardwareStepView;
  shipment: HardwareStepView;
  write_card: HardwareStepView;
  readback: HardwareStepView;
  acceptance: HardwareStepView;
}

export interface HardwareItemView {
  id: string;
  order_id: string;
  sku: string;
  name: string;
  kind: string; // goods | service
  qty: number;
  unit_amount_minor: number;
}

export interface AIUsageView {
  id: string;
  quote_ref: string;
  capability: string;
  quantity: number;
  amount_minor: number;
  currency: string;
  state: string; // confirmed | consumed | refunded | failed
  business_ref?: string;
  consumed_by_ref?: string;
  occurred_at: string;
}

export interface RefundView {
  id: string;
  order_id: string;
  refund_ref: string;
  amount_minor: number;
  currency: string;
  reason: string;
  invoice_title: string;
  invoice_status: string; // none | pending | issued | void
  invoice_ref?: string;
  auditor: string;
  occurred_at: string;
}

// ---- 文案(四态/五状态/退款发票/AI 用量状态) ----------------------------------

// 四态开通 + 诚实未知(F16):文案可区分,不给 processing 涂成功色。
export function subscriptionStatusText(status: string): string {
  switch (status) {
    case "processing":
      return "处理中（等待支付结果）";
    case "opening":
      return "开通中（已确认支付，等待开通）";
    case "active":
      return "已开通";
    case "expired":
      return "已到期（旧权益仍可查询核销，新增收费能力暂停）";
    case "failed":
      return "开通失败";
    case "cancelled":
      return "已撤单";
    default:
      return "未确认（无有效订阅事实）";
  }
}

export function orderStatusText(status: string): string {
  switch (status) {
    case "processing":
      return "处理中";
    case "opening":
      return "开通中";
    case "active":
      return "已开通";
    case "failed":
      return "失败";
    case "cancelled":
      return "已撤单";
    default:
      return status || "未知";
  }
}

export function orderActionText(action: string): string {
  switch (action) {
    case "new":
      return "新购";
    case "renew":
      return "续费";
    case "expand":
      return "扩店";
    case "downgrade":
      return "降档";
    case "purchase":
      return "购买";
    default:
      return action || "—";
  }
}

// 硬件五状态(每步独立事实;pending/done 两态)。
export const HARDWARE_STEPS: { key: keyof HardwareFulfillmentView; label: string }[] = [
  { key: "payment", label: "付款" },
  { key: "shipment", label: "出库" },
  { key: "write_card", label: "写卡" },
  { key: "readback", label: "回读" },
  { key: "acceptance", label: "现场验收" },
];

export function stepText(step: HardwareStepView | undefined): string {
  if (!step || step.state !== "done") return "未发生";
  const who = step.operator ? `（${step.operator}）` : "";
  return `已完成${who}`;
}

export function aiUsageStateText(state: string): string {
  switch (state) {
    case "confirmed":
      return "已确认未消费";
    case "consumed":
      return "已消费";
    case "refunded":
      return "已退款";
    case "failed":
      return "失败";
    default:
      return state || "未知";
  }
}

export function invoiceStatusText(status: string): string {
  switch (status) {
    case "none":
      return "未开票";
    case "pending":
      return "开票中";
    case "issued":
      return "已开票";
    case "void":
      return "已作废";
    default:
      return status || "未知";
  }
}

export function planCodeText(plan: string): string {
  if (plan === "store.plan.month") return "门店版·月付";
  if (plan === "store.plan.year") return "门店版·年付";
  return plan || "—";
}

export function formatAmount(minor: number, currency: string): string {
  const yuan = (minor / 100).toFixed(2);
  return currency ? `${yuan} ${currency}` : yuan;
}

// 门店额度文案(limit 0 = 无档位事实,诚实未知)。
export function storeQuotaText(limit: number, live: number): string {
  if (limit <= 0) return `已启用门店 ${live} 家（当前订阅无门店档位事实）`;
  if (live > limit) return `已启用门店 ${live} 家 / 档位 ${limit} 家（超出档位，扩店订单处理中或需扩店）`;
  return `已启用门店 ${live} 家 / 档位 ${limit} 家`;
}

// 下单资格判定:org_owner 恒有 billing:manage_payer(4593 冻结矩阵);
// agent 需 renewal grant 且 payer_grant_ref 非空(access/effective 已折算成
// billing:manage_payer);FEATURE_AUTHZ off 时回退角色判定(owner)。
export function canManageBilling(role: string, permissions: string[] | null): boolean {
  if (Array.isArray(permissions)) {
    return permissions.includes("billing:manage_payer");
  }
  return role === "org_owner";
}
