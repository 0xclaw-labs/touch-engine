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
  value: number | undefined;
  definition: FunnelDefinition;
  applied_filters?: string[];
  note?: string;
}

export interface EvidencePanel {
  by_class: Record<string, number>;
  distinct_participations: number | undefined;
  official_attempts: number | undefined;
  note: string;
}

export interface InventoryLine {
  inventory_id: string;
  campaign_id: string;
  store_id?: string;
  name: string;
  kind: string;
  benefit_slot: string;
  rule_version: number | undefined;
  currency?: string;
  total_qty: number | undefined;
  reserved: number | undefined;
  issued: number | undefined;
  redeemed: number | undefined;
  available: number | undefined;
  available_unknown?: boolean;
  reversed: number | undefined;
  expired: number | undefined;
  face_value_minor: number | undefined;
  cost_minor: number | undefined;
}

export interface CostPanel {
  content_generation_available: boolean;
  content_generation_amount_minor?: number | null;
  content_generation_runs: number | undefined;
  content_generation_note?: string;
  reward_cost_minor: number | undefined;
  reward_redeemed_cost_minor: number | undefined;
  reward_face_minor: number | undefined;
  unrecorded_batches: number | undefined;
  reward_cost_unrecorded?: number;
  redeemed_cost_unrecorded?: number;
  face_note: string;
}

export interface AnomalyPanel {
  pending_review: number | undefined;
  reservation_failed: number | undefined;
  reversed_in_window: number | undefined;
  expired_in_window: number | undefined;
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
  applied_filters?: string[];
  key: string;
  label: string;
  value: number | undefined;
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
  campaign_submitted: number | undefined;
  store_submitted: number | undefined;
  pending_sync: number | undefined;
  receiver_confirmed: number | undefined;
  revoked: number | undefined;
  receiver_declared_revoked: number | undefined;
  unique_contacts: number | undefined;
  note: string;
  definition: FunnelDefinition;
}

export interface CostReconciliation {
  availability?: "forbidden";
  note?: string;
  subscription_available: boolean;
  subscription_paid_minor?: number | null;
  subscription_paid_orders: number | undefined;
  subscription_paid_currency?: string;
  subscription_paid_time_unknown?: number;
  subscription_failed_orders: number | undefined;
  subscription_refund_minor?: number | null;
  subscription_refunds?: number;
  subscription_refund_currency?: string;
  subscription_refund_time_unknown?: number;
  subscription_note?: string;
  ai_confirmed_minor: number | undefined;
  ai_confirmed_count: number | undefined;
  ai_consumed_minor: number | undefined;
  ai_consumed_count: number | undefined;
  ai_refunded_minor: number | undefined;
  ai_refunded_count: number | undefined;
  ai_failed_minor: number | undefined;
  ai_failed_count: number | undefined;
  hardware_available: boolean;
  hardware_paid_minor?: number | null;
  hardware_paid_orders: number | undefined;
  hardware_paid_currency?: string;
  hardware_paid_time_unknown?: number;
  hardware_refund_minor: number | undefined;
  hardware_refunds: number | undefined;
  hardware_refund_currency?: string;
  hardware_refund_time_unknown?: number;
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
export function formatMinor(minor: unknown): string {
  return typeof minor === "number" && Number.isFinite(minor) ? (minor / 100).toFixed(2) : "未知";
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
): { key: string; label: string; count: number | string }[] {
  return EVIDENCE_ORDER.map((key) => ({
    key,
    label: EVIDENCE_LABELS[key],
    count: countDisplay(evidence.by_class?.[key]),
  }));
}

// 请求了、但该级事实没有这个维度 → 照实列出「本级未生效」的筛选,
// 绝不假装过滤。filter_echo 是服务端回显的已应用筛选。
export function stageNotAppliedLabels(
  stage: Pick<FunnelStage, "applied_filters">,
  filterEcho: Record<string, string> | null | undefined,
): string[] {
  const applied = new Set(stage.applied_filters ?? []);
  return FILTER_ORDER.filter((key) => (filterEcho?.[key] ?? "") !== "" && !applied.has(key)).map(
    (key) => FILTER_LABELS[key],
  );
}

// 窗口内全是真实 0(账本在、作用域为空)时的文案:0 不是未知,不是失败。
export function emptyWindowCopy(): string {
  return "这个窗口的活动参与与权益已读事实均为 0。门店服务、线索与费用分别看各区块；未知仍保持未知。";
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
  definition?: FunnelDefinition;
}

export function metricDisplay(metric: CatalogMetric): string {
  return metric.available && typeof metric.value === "number" && Number.isFinite(metric.value) ? String(metric.value) : "未接入";
}

// 六层标题(固定顺序,服务端 layers 数组同序;缺失行保持未知,不拼 0)。
export const LAYER_TITLES: { key: string; label: string }[] = [
  { key: "action_clicked", label: "点击" },
  { key: "invoke_attempt", label: "唤起尝试" },
  { key: "handoff_media", label: "交接准备与操作" },
  { key: "user_reported", label: "顾客自报" },
  { key: "staff_confirmed", label: "店员确认" },
  { key: "platform_confirmed", label: "平台证实" },
];

// 线索面板行(固定顺序):待同步与已获客是两个桶,注记必渲染。
export function leadsPanelRows(
  lp: LeadsPanel,
): { key: string; label: string; value: number | string }[] {
  return [
    { key: "campaign_submitted", label: "活动来源提交", value: lp.campaign_submitted },
    { key: "store_submitted", label: "门店留资提交", value: lp.store_submitted },
    { key: "pending_sync", label: "待同步(Notify 已投递,未获客)", value: lp.pending_sync },
    { key: "receiver_confirmed", label: "已获客(接收方确认入库)", value: lp.receiver_confirmed },
    { key: "revoked", label: "已撤销", value: lp.revoked },
    { key: "receiver_declared_revoked", label: "接收方申报撤销", value: lp.receiver_declared_revoked },
    { key: "unique_contacts", label: "唯一联系人(去重)", value: lp.unique_contacts },
  ].map(row => ({ ...row, value: countDisplay(row.value) }));
}

// 接口是外部数据边界。只兼容旧 Go nil 集合，不给缺失数值补零。
// 历史增量面板缺席可继续查看其他事实，缺席项目逐项为未知。
export function countDisplay(value: unknown): number | string {
  return typeof value === "number" && Number.isFinite(value) ? value : "未知";
}

function object(value: unknown, required = false): Record<string, unknown> {
  if (value == null && !required) return {};
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid statistics object");
  return value as Record<string, unknown>;
}
function list(value: unknown): unknown[] {
  if (value == null) return [];
  if (!Array.isArray(value)) throw new Error("invalid statistics list");
  return value;
}
function text(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}
function numberFields(source: Record<string, unknown>, keys: string): Record<string, number | undefined> {
  return Object.fromEntries(keys.split(" ").map(key => [key,
    typeof source[key] === "number" && Number.isFinite(source[key]) ? source[key] : undefined,
  ]));
}
function definition(value: unknown): FunnelDefinition {
  const source = object(value);
  return Object.fromEntries(["source", "dedup_key", "denominator", "event_time", "window"].map(key => [key, text(source[key], "未提供，不能推算转化率")])) as unknown as FunnelDefinition;
}
function filterList(value: unknown): string[] {
  return list(value).filter((item): item is string => typeof item === "string");
}

export function parseOperationsResponse(value: unknown, tenantId: string): OperationsResponse {
  const source = object(value, true);
  const win = object(source.window, true);
  const scope = object(source.scope, true);
  if (!text(win.start) || !text(win.end) || scope.tenant_id !== tenantId) throw new Error("invalid statistics scope");
  if (!("funnel" in source) || !("inventory" in source)) throw new Error("missing statistics collection");
  const evidence = object(source.evidence);
  const layers = object(source.evidence_layers);
  const leads = object(source.leads);
  const cost = object(source.cost);
  const recon = object(source.cost_reconciliation);
  const anomalies = object(source.anomalies);
  const notes = object(source.notes);
  const dims = object(source.dimensions);
  const byClass = object(evidence.by_class);
  const note = "没有读到这部分事实，缺失保持未知。可刷新重试。";
  const parsed = {
    window: { start: win.start, end: win.end },
    scope: { tenant_id: scope.tenant_id, store_scope: text(scope.store_scope) },
    filter_echo: Object.fromEntries(Object.entries(object(source.filter_echo)).filter(([, v]) => typeof v === "string")),
    funnel: list(source.funnel).map(value => {
      const row = object(value, true);
      return { key: text(row.key), title: text(row.title, "未命名事实"), available: row.available === true,
        ...numberFields(row, "value"), definition: definition(row.definition), applied_filters: filterList(row.applied_filters), note: text(row.note) };
    }),
    evidence: { by_class: numberFields(byClass, EVIDENCE_ORDER.join(" ")),
      ...numberFields(evidence, "distinct_participations official_attempts"), note: text(evidence.note, note) },
    evidence_layers: { layers: list(layers.layers).map(value => {
      const layer = object(value, true);
      return { key: text(layer.key), title: text(layer.title), available: layer.available === true,
        counts: list(layer.counts).map(value => { const row = object(value, true); return { key: text(row.key), label: text(row.label), ...numberFields(row, "value"), applied_filters: Array.isArray(row.applied_filters) ? filterList(row.applied_filters) : undefined }; }),
        definition: definition(layer.definition), applied_filters: filterList(layer.applied_filters), note: text(layer.note) };
    }), note: text(layers.note, note) },
    leads: { ...numberFields(leads, "campaign_submitted store_submitted pending_sync receiver_confirmed revoked receiver_declared_revoked unique_contacts"),
      definition: definition(leads.definition), note: text(leads.note, note) },
    inventory: list(source.inventory).map(value => {
      const row = object(value, true);
      return { ...Object.fromEntries("inventory_id campaign_id store_id name kind benefit_slot currency".split(" ").map(key => [key, text(row[key])])),
        ...numberFields(row, "rule_version total_qty reserved issued redeemed available reversed expired face_value_minor cost_minor"), available_unknown: row.available_unknown === true };
    }),
    cost: { ...numberFields(cost, "content_generation_amount_minor content_generation_runs reward_cost_minor reward_redeemed_cost_minor reward_face_minor unrecorded_batches reward_cost_unrecorded redeemed_cost_unrecorded"),
      content_generation_available: cost.content_generation_available === true, content_generation_note: text(cost.content_generation_note, note), face_note: text(cost.face_note, note) },
    cost_reconciliation: { ...numberFields(recon, "subscription_paid_minor subscription_paid_orders subscription_failed_orders subscription_refund_minor subscription_refunds ai_confirmed_minor ai_confirmed_count ai_consumed_minor ai_consumed_count ai_refunded_minor ai_refunded_count ai_failed_minor ai_failed_count hardware_paid_minor hardware_paid_orders hardware_refund_minor hardware_refunds subscription_paid_time_unknown subscription_refund_time_unknown hardware_paid_time_unknown hardware_refund_time_unknown"),
      subscription_paid_currency: text(recon.subscription_paid_currency), subscription_refund_currency: text(recon.subscription_refund_currency),
      hardware_paid_currency: text(recon.hardware_paid_currency), hardware_refund_currency: text(recon.hardware_refund_currency),
      availability: recon.availability === "forbidden" ? "forbidden" : undefined, note: text(recon.note),
      subscription_available: recon.subscription_available === true, hardware_available: recon.hardware_available === true,
      subscription_note: text(recon.subscription_note), definition: definition(recon.definition), face_note: text(recon.face_note, note) },
    anomalies: numberFields(anomalies, "pending_review reservation_failed reversed_in_window expired_in_window"),
    notes: { entry_marker: text(notes.entry_marker, note), visitor: text(notes.visitor, note) },
    dimensions: {
      campaigns: list(dims.campaigns).map(value => { const row = object(value, true); return { id: text(row.id), title: text(row.title), store_id: text(row.store_id) }; }),
      stores: list(dims.stores).map(value => { const row = object(value, true); return { id: text(row.id), name: text(row.name) }; }),
      actions: list(dims.actions).map(value => { const row = object(value, true); return { id: text(row.id), label: text(row.label), enabled: row.enabled === true }; }),
    },
  };
  return parsed as unknown as OperationsResponse;
}

type AnalyticsAPI = (method: string, path: string) => Promise<{ ok: boolean; status: number; data: unknown }>;
export type AnalyticsRead =
  | { phase: "ready"; data: OperationsResponse; catalog: CatalogMetric[] | null; catalogNote: string }
  | { phase: "error" | "forbidden" | "disabled"; message: string };

export async function readAnalytics(api: AnalyticsAPI, tenantId: string, query: string): Promise<AnalyticsRead> {
  // 两个独立只读结果分别判断，目录故障不能抹掉已经读到的经营事实。
  const [ops, cat] = await Promise.allSettled([api("GET", `dashboard/operations?${query}`), api("GET", `dashboard?${query}`)]);
  if (ops.status === "rejected") return { phase: "error", message: "没有连上统计服务。请检查网络后重试。" };
  if (ops.value.status === 401 || ops.value.status === 403 || (cat.status === "fulfilled" && [401, 403].includes(cat.value.status))) {
    return { phase: "forbidden", message: "当前账号没有此范围的统计查看权限。请返回工作台确认组织或联系管理员授权，再重试。" };
  }
  if (ops.value.status === 404) return { phase: "disabled", message: ANALYTICS_NOT_ENABLED_COPY };
  if (!ops.value.ok) return { phase: "error", message: "统计服务暂时不可用，没有读到经营事实。请稍后重试。" };
  let data: OperationsResponse;
  try { data = parseOperationsResponse(ops.value.data, tenantId); }
  catch { return { phase: "error", message: "统计响应不完整或范围已变化，没有沿用旧数据。请刷新重试。" }; }
  let catalog: CatalogMetric[] | null = null;
  let catalogNote = "汇总指标暂时没有读到，保持未知。请刷新重试。";
  if (cat.status === "fulfilled" && cat.value.ok) {
    try {
      const body = object(cat.value.data, true);
      const catScope = object(body.scope, true);
      if (catScope.tenant_id !== tenantId || text(catScope.store_scope) !== text(data.scope.store_scope) || !Array.isArray(body.metrics)) throw new Error("catalog scope mismatch");
      catalog = body.metrics.map(value => {
        const row = object(value, true);
        return { key: text(row.key), title: text(row.title), available: row.available === true,
          ...numberFields(row, "value unique_contacts"), reason: text(row.reason), definition: definition(row.definition) } as CatalogMetric;
      });
      catalogNote = "";
    } catch { /* 非权威目录缺失保持未知，不影响主事实。 */ }
  }
  return { phase: "ready", data, catalog, catalogNote };
}
