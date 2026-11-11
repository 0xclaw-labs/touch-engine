// HUI-4592 平台运营控制台视图模型与文案收敛。
//
// 纪律(裁定书):
//   - 权限只来自受信 ops grant(/ops 六视图门 = access/effective 的
//     ops_permissions 非空;无授权诚实 403,不换标签冒充平台管理员);
//   - 运营写全部走统一操作信封(POST /ops/operations):原因必填、
//     revision 守卫、幂等重放——页面不发明第二套写路径;
//   - 数据不可得显示 unavailable/unknown,与真实零记录区分;
//   - 文案只复述权威事实(订单/退款是 4591 投影;渠道判定是 3535 真值),
//     页面永不说"已恢复/已生效"之外本仓无权断言的话。

export interface OpsTenantRow {
  id: string;
  name: string;
  created_at: string;
  brand_id?: string;
  lifecycle?: string;
  charge_hold?: boolean;
  store_count: number;
  member_count: number;
  agency_count: number;
  subscription_status: string;
  paid_until: string;
  retain_until: string;
  ops_revision: number;
}

export interface OpsTenantDetail {
  tenant: OpsTenantRow;
  ops_revision: number;
  subscription: {
    available: boolean;
    status?: string;
    paid_until?: string;
    retain_until?: string;
    effective_status?: string;
    state?: string;
  };
  business_data: {
    available: boolean;
    state: string;
    message?: string;
    support_access_ref?: string;
    store_count?: number;
    member_count?: number;
    stores?: Array<{ id: string; name: string; status?: string }>;
    members?: Array<{ id: string; display_name: string; role: string; enabled: number }>;
  };
}

export interface OpsSupportAccess {
  id: string;
  tenant_id: string;
  principal_ref: string;
  reason: string;
  opened_at: string;
  expires_at: string;
  closed_at: string;
  close_reason: string;
}

export interface OpsPlanView {
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
  effective_from: string;
  effective_to: string;
  effective: boolean;
  retired: boolean;
  window_state: string;
  order_price_snapshot_immutable: boolean;
}

export interface OpsOrderView {
  id: string;
  tenant_id: string;
  kind: string;
  action: string;
  plan_code: string;
  price_ref: string;
  amount_minor: number;
  currency: string;
  status: string;
  payment_ref: string;
  fail_reason?: string;
  created_at: string;
  updated_at: string;
  refund_total_minor: number;
}

export interface OpsRefundView {
  id: string;
  tenant_id: string;
  order_id: string;
  refund_ref: string;
  amount_minor: number;
  currency: string;
  reason: string;
  invoice_status: string;
  auditor: string;
  occurred_at: string;
}

export interface OpsAIUsageView {
  id: string;
  tenant_id: string;
  quote_ref: string;
  capability: string;
  quantity: number;
  amount_minor: number;
  currency: string;
  state: string;
}

export interface OpsChannelRecord {
  id: string;
  platform: string;
  method: string;
  scenario: string;
  environment: string;
  status: string;
  allowed: boolean;
  promised: boolean;
  pending_external: boolean;
  reason: string;
  reason_text: string;
  navigation: string;
  media_handoff: string;
  platform_receipt: string;
  reward_eligibility: string;
  evidence_grade: string;
  checked_at: string;
  valid_until: string;
  emergency_disabled: boolean;
  close_reason: string;
  tenant_scope: string;
}

export interface OpsAssignmentRow {
  id: string;
  tenant_id: string;
  store_id: string;
  template_ref: string;
  source_version: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface OpsTemplateRow {
  tenant_id: string;
  id: string;
  name: string;
  updated_at: string;
  version_count: number;
  published_count: number;
  latest_version: number;
  assignment_count: number;
}

export interface OpsContentVersionRow {
  tenant_id: string;
  pack_id: string;
  version_id: string;
  version: number;
  status: string;
  blocked_reason: string;
  updated_at: string;
}

export interface OpsTicket {
  id: string;
  tenant_id: string;
  kind: string;
  severity: string;
  title: string;
  detail: string;
  status: string;
  owner_ref: string;
  source: string;
  opened_at: string;
  resolution: string;
  closed_reason: string;
}

export interface OpsDrill {
  id: string;
  kind: string;
  target: string;
  result: string;
  verification_report: string;
  executed_by: string;
  executed_at: string;
  notes: string;
}

export interface OpsOperation {
  id: string;
  action: string;
  resource_ref: string;
  tenant_id: string;
  state: string;
  error_code: string;
  request_json: string;
  result_json: string;
  created_at: string;
  finished_at: string;
}

export interface OpsAuditEntry {
  id: string;
  occurred_at: string;
  actor_ref: string;
  actor_role: string;
  authority_source: string;
  command: string;
  action: string;
  target_type: string;
  target_id: string;
  tenant_id: string;
  before_json: string;
  after_json: string;
  reason: string;
  operation_id: string;
}

export interface OperationsOverview {
  domain_health: {
    status: string;
    targets: Array<{
      target_kind: string;
      config_key: string;
      configured?: boolean;
      status: string;
      latency_ms?: number;
      http_status?: number;
      error?: string;
      detail?: string;
    }>;
  };
  hardware_fulfillment: {
    state: string;
    source: string;
    note: string;
    // Go 端内嵌结构体序列化展开:订单字段在顶层,履约投影在 fulfillment。
    orders: Array<
      OpsOrderView & {
        fulfillment?: {
          steps?: Array<{ step: string; state: string; operator: string; occurred_at: string }>;
        };
      }
    >;
  };
  touchpoint_inventory: Record<string, number>;
  tickets: OpsTicket[];
  drills: OpsDrill[];
  recent_operations: OpsOperation[];
}

// ---- 文案 -----------------------------------------------------------------------

export function lifecycleText(v: string | undefined): string {
  switch (v) {
    case "active": return "正常";
    case "suspended": return "已暂停";
    case "security_freeze": return "安全冻结";
    case "offboarding": return "退出中";
    case "retired": return "已退役";
    default: return v ? v : "未知";
  }
}

export function ticketStatusText(v: string): string {
  switch (v) {
    case "open": return "待认领";
    case "confirmed": return "处理中";
    case "resolved": return "已修复待确认";
    case "closed": return "已关闭";
    default: return v;
  }
}

export function orderKindText(v: string): string {
  switch (v) {
    case "subscription": return "订阅";
    case "hardware": return "硬件";
    default: return v;
  }
}

export function orderStatusText(v: string): string {
  switch (v) {
    case "processing": return "处理中";
    case "opening": return "开通中";
    case "active": return "已开通";
    case "failed": return "失败";
    case "cancelled": return "已撤单";
    default: return v;
  }
}

export function invoiceStatusText(v: string): string {
  switch (v) {
    case "none": return "未开票";
    case "pending": return "开票中";
    case "issued": return "已开票";
    case "void": return "已作废";
    default: return v;
  }
}

export function evidenceGradeText(v: string): string {
  switch (v) {
    case "official_spec": return "官方规范";
    case "official_doc": return "官方文档";
    case "official_snapshot": return "官方快照";
    case "device_test": return "真机实测";
    case "unverified": return "未核实";
    default: return v;
  }
}

export function drillKindText(v: string): string {
  switch (v) {
    case "backup": return "备份演练";
    case "restore": return "恢复演练";
    default: return v;
  }
}

export function formatAmount(minor: number, currency: string): string {
  const yuan = (minor / 100).toFixed(2).replace(/\.00$/, "");
  return currency ? `${yuan} ${currency}` : yuan;
}

// 冻结操作白名单(与 Go ops 包一致,只增不改;页面按钮按此渲染)。
export const OPS_ACTION_LABELS: Record<string, string> = {
  "tenant.suspend": "暂停商户",
  "tenant.resume": "恢复商户",
  "tenant.charge_hold.set": "收费保持",
  "tenant.charge_hold.clear": "解除收费保持",
  "plan.publish": "发布生效",
  "plan.retire": "下架停售",
  "order.recheck": "重查对账",
  "channel.disable": "紧急停用",
  "channel.restore": "恢复放行",
  "content.retract": "风险下架",
  "template.assignment.cancel": "撤回分发",
  "ticket.open": "开单",
  "ticket.confirm": "认领",
  "ticket.resolve": "标记已修复",
  "ticket.close": "关闭工单",
  "drill.record": "记录演练",
};
