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

// HUI-4664 判定升级后的收银门契约:available=true 只在「订单/原单绑定
// checkout 引用 + 等待支付 + 公共结算侧 seam 已接通」时成立,此时必须携带
// checkout_ref 与 checkout_url(续付 URL;只在本响应即时给出,永不落库)。
// available=false 时 reason 诚实分诊:checkout_unwired(无引用/未接通)、
// checkout_unavailable(seam 未配置,报价与原单保留)、original_payer_required、
// not_awaiting_payment(已付/已开通/失败——没有可续付的款项)。
export interface CheckoutRef {
  available: boolean;
  app_id: string;
  launch_target_id: string;
  checkout_ref?: string;
  checkout_url?: string;
  state?: string; // checkout_pending | paid | activated | failed
  quote_ref?: string;
  order_ref?: string;
  amount_minor?: number;
  currency?: string;
  fail_reason?: string;
  reason?: string;
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

// A pending original subscription intent must be resumed before another purchase.
export function blocksNewSubscription(order: Pick<BillingOrderView, "kind" | "status"> & Partial<Pick<BillingOrderView, "payment_ref">>): boolean {
  return order.kind === "subscription" && (["processing", "opening"].includes(order.status) || (order.status === "failed" && Boolean(order.payment_ref)));
}

// ---- HUI-4664 收银接续门(ADV-9 PR12;语义现在为真) ------------------------------

// 收银门判定:available=true 当且仅当引用与续付 URL 都在场(缺一即视为未
// 接通,绝不发明跳转地址;续付 URL 只由服务端响应即时给出)。PR-3 前端接续
// 面挂此门;既有 checkout.available 门的语义由本函数钉死为真值语义。
export function canContinueCheckout(checkout: CheckoutRef | undefined | null): boolean {
  return Boolean(checkout?.available && checkout.checkout_ref && checkout.checkout_url);
}

// available=false 的诚实文案矩阵(reason 分诊;页面只复述权威事实,永不说
// 「已扣款/已到账」这类本仓无权断言的话)。
export function checkoutUnavailableText(checkout: CheckoutRef | undefined | null): string {
  if (!checkout) return "支付接续尚未开通。原单已保留，可继续回查；请联系平台完成接入，无需重新下单。";
  switch (checkout.reason) {
    case "original_payer_required":
      return "原付款授权已变化，请由原付款人或平台核对这笔原单。查看原单不会授予付款权限。";
    case "not_awaiting_payment":
      if (checkout.state === "failed") {
        return `开通未完成（${checkout.fail_reason || "原因以权威事实为准"}）；款项事实以账单投影为准，请联系平台处理，无需重复付款。`;
      }
      if (checkout.state === "paid" || checkout.state === "activated") {
        return "款项事实已回流，服务状态以服务端事实为准推进；请回查原单，无需重复付款。";
      }
      return "这笔原单已不在待支付状态，请按回查结果继续办理，无需再次付款。";
    case "checkout_unavailable":
      return "公共结算侧收银尚未接通：这次没有生成可续付引用；报价与原单都已保留，可继续回查，无需重新报价或重新发起。";
    default:
      return "支付接续尚未开通。原单已保留，可继续回查；请联系平台完成接入，无需重新下单。";
  }
}

export function nextOrderStep(order: Pick<BillingOrderView, "kind" | "status" | "payment_ref">): string {
  if (order.kind === "hardware") return "付款与实卡验收分别记录；回查原订单查看出库、写卡、回读和现场验收。";
  if (order.status === "opening" || (order.payment_ref && order.status === "processing")) return "已确认支付，正在同步开通；请回查原订单，无需重复购买。";
  if (order.status === "processing") return "等待支付结果；请回查原订单确认付款进度，无需重复下单。";
  if (order.status === "failed") return "办理失败，请回查原订单定位原因；已付款的异常交由平台处理。";
  if (order.status === "active") return "服务已生效；实卡交付以独立验收记录为准。";
  return "请回查原订单，核对取消或后续处理结果。";
}
