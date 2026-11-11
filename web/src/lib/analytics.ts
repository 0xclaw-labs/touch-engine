// HUI-1677 经营看板纯函数层(浏览器侧):窗口/查询串组装、金额格式化、
// 三类证据单列、筛选不适用级照实披露、诚实文案常量与响应类型。
// 纪律同服务端 internal/dashboard 包:未知/未接入不冒充 0;筛选不适用级
// 照实披露(不假装过滤);nfc 只是入口标记;匿名参与不冒充自然人数;
// 券面额不是支出。本文件不发起请求(组件保持渲染职责)。

export interface FunnelDefinition {
  source: string;
  dedup_key: string;
  denominator: string;
  event_time: string;
  window: string;
}

export interface FunnelStage {
  key: string;
  title: string;
  available: boolean;
  value: number;
  definition: FunnelDefinition;
  applied_filters?: string[];
  note?: string;
}

export interface EvidencePanel {
  by_class: Record<string, number>;
  distinct_participations: number;
  official_attempts: number;
  note: string;
}

export interface InventoryLine {
  inventory_id: string;
  campaign_id: string;
  store_id?: string;
  name: string;
  kind: string;
  benefit_slot: string;
  rule_version: number;
  currency?: string;
  total_qty: number;
  reserved: number;
  issued: number;
  redeemed: number;
  available: number;
  reversed: number;
  expired: number;
  face_value_minor: number;
  cost_minor: number;
}

export interface CostPanel {
  content_generation_available: boolean;
  content_generation_amount_minor?: number | null;
  content_generation_runs: number;
  content_generation_note?: string;
  reward_cost_minor: number;
  reward_redeemed_cost_minor: number;
  reward_face_minor: number;
  unrecorded_batches: number;
  face_note: string;
}

export interface AnomalyPanel {
  pending_review: number;
  reservation_failed: number;
  reversed_in_window: number;
  expired_in_window: number;
}

export interface OperationsNotes {
  entry_marker: string;
  visitor: string;
}

export interface DimensionCampaign {
  id: string;
  title: string;
  store_id?: string;
}

export interface DimensionStore {
  id: string;
  name: string;
}

export interface OperationsResponse {
  window: { start: string; end: string };
  scope: { tenant_id: string; store_scope?: string };
  filter_echo: Record<string, string>;
  funnel: FunnelStage[];
  evidence: EvidencePanel;
  evidence_layers: EvidenceLayersPanel;
  leads: LeadsPanel;
  inventory: InventoryLine[];
  cost: CostPanel;
  cost_reconciliation: CostReconciliation;
  anomalies: AnomalyPanel;
  notes: OperationsNotes;
  dimensions: {
    campaigns?: DimensionCampaign[];
    stores?: DimensionStore[];
    tags?: unknown[];
    actions?: DimensionAction[];
  };
}

export interface AnalyticsFilters {
  campaignId?: string;
  storeId?: string;
  contentPackVersion?: string;
  touchpoint?: string;
  platform?: string;
  handoffMethod?: string;
  actionId?: string;
  environment?: string;
}

// 六层证据(TOUCH-ALL-20261109-v1):点击/唤起尝试/媒体交接/顾客自报/店员确认/
// 平台证实 分开计数,层与层之间不相加。
export interface LayerCount {
  key: string;
  label: string;
  value: number;
}

export interface EvidenceLayer {
  key: string;
  title: string;
  available: boolean;
  counts: LayerCount[];
  definition: FunnelDefinition;
  applied_filters?: string[];
  note?: string;
}

export interface EvidenceLayersPanel {
  layers: EvidenceLayer[];
  note: string;
}

export interface LeadsPanel {
  campaign_submitted: number;
  store_submitted: number;
  pending_sync: number;
  receiver_confirmed: number;
  revoked: number;
  receiver_declared_revoked: number;
  unique_contacts: number;
  note: string;
  definition: FunnelDefinition;
}

export interface CostReconciliation {
  subscription_available: boolean;
  subscription_paid_minor?: number | null;
  subscription_paid_orders: number;
  subscription_failed_orders: number;
  subscription_note?: string;
  ai_confirmed_minor: number;
  ai_confirmed_count: number;
  ai_consumed_minor: number;
  ai_consumed_count: number;
  ai_refunded_minor: number;
  ai_refunded_count: number;
  ai_failed_minor: number;
  ai_failed_count: number;
  hardware_available: boolean;
  hardware_paid_minor?: number | null;
  hardware_paid_orders: number;
  hardware_refund_minor: number;
  hardware_refunds: number;
  definition: FunnelDefinition;
  face_note: string;
}

export interface DimensionAction {
  id: string;
  label: string;
  platform?: string;
  kind?: string;
  enabled: boolean;
}

// 环境白名单(与 3535 六环境同口径;服务端拒绝未知值 400)。
export const ENVIRONMENT_OPTIONS = [
  "desktop_browser",
  "ios_safari",
  "android_browser",
  "ios_wechat",
  "android_wechat",
  "target_app",
] as const;

// 筛选键的固定顺序(与服务端 filter_echo 一致)与商家标签。
export const FILTER_LABELS: Record<string, string> = {
  campaign_id: "活动",
  store_id: "门店",
  content_pack_version: "内容包版本",
  touchpoint: "触点",
  platform: "平台",
  handoff_method: "承接方式",
  action_id: "动作",
  environment: "环境",
};

const FILTER_ORDER = [
  "campaign_id",
  "store_id",
  "content_pack_version",
  "touchpoint",
  "platform",
  "handoff_method",
  "action_id",
  "environment",
] as const;

const FILTER_GETTERS: Record<string, (f: AnalyticsFilters) => string> = {
  campaign_id: (f) => f.campaignId ?? "",
  store_id: (f) => f.storeId ?? "",
  content_pack_version: (f) => f.contentPackVersion ?? "",
  touchpoint: (f) => f.touchpoint ?? "",
  platform: (f) => f.platform ?? "",
  handoff_method: (f) => f.handoffMethod ?? "",
  action_id: (f) => f.actionId ?? "",
  environment: (f) => f.environment ?? "",
};

// FEATURE_DASHBOARD 未开通时,端点答统一 404:缺失保持未知,不显示 0,
// 也不假装「暂无数据」。这张页面把这行话原样给商家。
export const ANALYTICS_NOT_ENABLED_COPY =
  "经营统计未开通。开通之前缺失保持未知，不显示 0，也不假装有数据。";

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

export function toRfc3339Local(date: Date): string {
  const offset = -date.getTimezoneOffset();
  const sign = offset >= 0 ? "+" : "-";
  const abs = Math.abs(offset);
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    `T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}` +
    `${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`
  );
}

// 「今天」窗口 = 本地日历日 [00:00, now],RFC3339 带本地时区偏移;
// 服务端按时刻落窗(秒粒度)与日粒度访问事实各自解析。
export function localDayWindow(now: Date): { window_start: string; window_end: string } {
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0);
  return { window_start: toRfc3339Local(start), window_end: toRfc3339Local(now) };
}

// 查询串:窗口必带;筛选逐项进串,空值不带(不出现空参数)。
export function buildAnalyticsQuery(
  win: { window_start: string; window_end: string },
  filters: AnalyticsFilters,
): string {
  const params = new URLSearchParams();
  params.set("window_start", win.window_start);
  params.set("window_end", win.window_end);
  for (const key of FILTER_ORDER) {
    const value = (FILTER_GETTERS[key](filters) ?? "").trim();
    if (value !== "") params.set(key, value);
  }
  return params.toString();
}

// 分 → 元,固定两位;面额/成本的「是面额不是支出」语义由调用方文案承担。
export function formatMinor(minor: number): string {
  return (minor / 100).toFixed(2);
}

export const EVIDENCE_LABELS: Record<string, string> = {
  staff_observed_submission: "店员目击提交（顾客当面展示）",
  staff_verified_post: "店员核实发布（店员亲眼查到已发内容）",
  official_publish_confirmed: "官方发布确认（平台查询回执）",
};

const EVIDENCE_ORDER = [
  "staff_observed_submission",
  "staff_verified_post",
  "official_publish_confirmed",
] as const;

// 三类证据单列、固定顺序。绝不在这里相加——相加会冒充「发布总人数」;
// 总体覆盖看 distinct_participations,官方发布看 official_attempts。
export function evidenceRowList(
  evidence: EvidencePanel,
): { key: string; label: string; count: number }[] {
  return EVIDENCE_ORDER.map((key) => ({
    key,
    label: EVIDENCE_LABELS[key],
    count: evidence.by_class[key] ?? 0,
  }));
}

// 请求了、但该级事实没有这个维度 → 照实列出「本级未生效」的筛选,
// 绝不假装过滤。filter_echo 是服务端回显的已应用筛选。
export function stageNotAppliedLabels(
  stage: FunnelStage,
  filterEcho: Record<string, string>,
): string[] {
  const applied = new Set(stage.applied_filters ?? []);
  return FILTER_ORDER.filter((key) => (filterEcho[key] ?? "") !== "" && !applied.has(key)).map(
    (key) => FILTER_LABELS[key],
  );
}

// 窗口内全是真实 0(账本在、作用域为空)时的文案:0 不是未知,不是失败。
export function emptyWindowCopy(): string {
  return "这个窗口还没有发生可统计的事实。0 是真实的 0，不是未知；数据会随运营累积。";
}

// PR#7 汇总指标(GET /dashboard,9 指标固定目录)单行。value 是指针语义:
// 未接入/未知序列化为缺席或 null,绝不冒充 0。授权留资提交/CRM 接收在
// 目录里单列,碰触或领券不自动算成获客联系人(留资 unique_contacts 另给)。
export interface CatalogMetric {
  key: string;
  title: string;
  available: boolean;
  value?: number | null;
  unique_contacts?: number | null;
  reason?: string;
}

export function metricDisplay(metric: CatalogMetric): string {
  return metric.available && typeof metric.value === "number" ? String(metric.value) : "未接入";
}

// 六层标题(固定顺序,服务端 layers 数组同序;缺失行保持未知,不拼 0)。
export const LAYER_TITLES: { key: string; label: string }[] = [
  { key: "action_clicked", label: "点击" },
  { key: "invoke_attempt", label: "唤起尝试" },
  { key: "handoff_media", label: "媒体交接" },
  { key: "user_reported", label: "顾客自报" },
  { key: "staff_confirmed", label: "店员确认" },
  { key: "platform_confirmed", label: "平台证实" },
];

// 线索面板行(固定顺序):待同步与已获客是两个桶,注记必渲染。
export function leadsPanelRows(
  lp: LeadsPanel,
): { key: string; label: string; value: number }[] {
  return [
    { key: "campaign_submitted", label: "活动来源提交", value: lp.campaign_submitted },
    { key: "store_submitted", label: "门店留资提交", value: lp.store_submitted },
    { key: "pending_sync", label: "待同步(Notify 已投递,未获客)", value: lp.pending_sync },
    { key: "receiver_confirmed", label: "已获客(接收方确认入库)", value: lp.receiver_confirmed },
    { key: "revoked", label: "已撤销", value: lp.revoked },
    { key: "receiver_declared_revoked", label: "接收方申报撤销", value: lp.receiver_declared_revoked },
    { key: "unique_contacts", label: "唯一联系人(去重)", value: lp.unique_contacts },
  ];
}
