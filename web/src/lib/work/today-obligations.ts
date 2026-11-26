import type { TodayItem } from "@/lib/product-finish";

export interface OperationsSummary {
  as_of?: string;
  pending_confirm?: number;
  rewards_issued?: number;
  rewards_redeemed?: number;
  todos?: {
    billing_anomalies?: number;
    action_auth_expired?: number;
    admission_expired?: number;
    pending_redeem?: number;
    unavailable_actions?: number;
    missing_content?: number;
  };
}

export type SummaryState = "loading" | "known_zero" | "partial_failure" | "not_connected" | "ready";

export interface SummaryMetric {
  key: "pending_confirm" | "rewards_issued" | "rewards_redeemed";
  label: string;
  unit: string;
  value: number | null;
  state: "known" | "unknown";
}

export interface SummaryView {
  state: SummaryState;
  headline: string;
  detail: string;
  metrics: SummaryMetric[];
  missingFields: string[];
  canRetry: boolean;
  asOf: string | null;
  httpStatus: number | null;
}

export interface SummaryReadInput {
  loading: boolean;
  ok: boolean;
  status: number | null;
  data: OperationsSummary | null;
  errorCode?: string;
  storeLoadFailed?: boolean;
}

function finiteNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function unknownMetrics(): SummaryMetric[] {
  return [
    { key: "pending_confirm", label: "待确认顾客发布", unit: "件", value: null, state: "unknown" },
    { key: "rewards_issued", label: "今日已发权益", unit: "份", value: null, state: "unknown" },
    { key: "rewards_redeemed", label: "今日已核销", unit: "份", value: null, state: "unknown" },
  ];
}

// 权威经营摘要四态分诊：loading / known_zero / partial_failure / not_connected（以及有正数事实的 ready）。
// 铁律：服务失败或未接入永不静默补 0，部分缺字段保留已知值并显式标注缺失项。
export function buildTodaySummaryView(input: SummaryReadInput): SummaryView {
  if (input.loading) {
    return {
      state: "loading",
      headline: "正在读取今日经营摘要",
      detail: "数据读取完成前不显示 0。",
      metrics: unknownMetrics(),
      missingFields: [],
      canRetry: false,
      asOf: null,
      httpStatus: null,
    };
  }

  const code = (input.errorCode ?? "").trim().toLowerCase();
  const isNotConnected =
    !input.ok &&
    (input.status === 404 || code === "feature_disabled" || code === "not_connected" || code === "dashboard_disabled");

  if (isNotConnected) {
    return {
      state: "not_connected",
      headline: "经营摘要服务当前未接入",
      detail: "未接入时不显示 0，不影响进入门店配置服务或办理顾客确认与核销。",
      metrics: unknownMetrics(),
      missingFields: [],
      canRetry: false,
      asOf: null,
      httpStatus: input.status,
    };
  }

  if (!input.ok || !input.data) {
    return {
      state: "partial_failure",
      headline: "经营摘要暂时没有读到",
      detail: "待处理数量与今日结果保持未知（不显示 0）。请重试摘要，或直接进入门店工作区与服务台核对。",
      metrics: unknownMetrics(),
      missingFields: ["pending_confirm", "rewards_issued", "rewards_redeemed"],
      canRetry: true,
      asOf: null,
      httpStatus: input.status,
    };
  }

  const pendingConfirm = finiteNumber(input.data.pending_confirm);
  const rewardsIssued = finiteNumber(input.data.rewards_issued);
  const rewardsRedeemed = finiteNumber(input.data.rewards_redeemed);

  const metrics: SummaryMetric[] = [
    {
      key: "pending_confirm",
      label: "待确认顾客发布",
      unit: "件",
      value: pendingConfirm,
      state: pendingConfirm !== null ? "known" : "unknown",
    },
    {
      key: "rewards_issued",
      label: "今日已发权益",
      unit: "份",
      value: rewardsIssued,
      state: rewardsIssued !== null ? "known" : "unknown",
    },
    {
      key: "rewards_redeemed",
      label: "今日已核销",
      unit: "份",
      value: rewardsRedeemed,
      state: rewardsRedeemed !== null ? "known" : "unknown",
    },
  ];

  const missingFields: string[] = metrics.filter((m) => m.state === "unknown").map((m) => m.key);
  if (input.storeLoadFailed) {
    missingFields.push("stores");
  }
  const asOf = typeof input.data.as_of === "string" && input.data.as_of.trim() ? input.data.as_of.trim() : null;

  if (missingFields.length > 0) {
    return {
      state: "partial_failure",
      headline: "只读到部分经营摘要",
      detail: "已读到的指标如实展示，缺失项保持未知（不显示 0）。可重试或前往服务台与经营结果核对。",
      metrics,
      missingFields,
      canRetry: true,
      asOf,
      httpStatus: input.status ?? 200,
    };
  }

  if (pendingConfirm === 0 && rewardsIssued === 0 && rewardsRedeemed === 0) {
    return {
      state: "known_zero",
      headline: "今日尚无待确认、发放或核销记录",
      detail: "权威读数确认均为 0，可在门店工作区继续完善基础服务或检查投放入口。",
      metrics,
      missingFields: [],
      canRetry: false,
      asOf,
      httpStatus: input.status ?? 200,
    };
  }

  return {
    state: "ready",
    headline: "今日经营摘要已同步",
    detail: "按当日权威读数展示，细项与统计方式可在经营结果查看。",
    metrics,
    missingFields: [],
    canRetry: false,
    asOf,
    httpStatus: input.status ?? 200,
  };
}

export interface CampaignStateHint {
  id: string;
  title: string;
  status: "active" | "paused" | "ended" | "expired" | "draft" | string;
}

export function todayObligations(
  summary: OperationsSummary | null,
  access: { serviceDesk: boolean; owner: boolean; campaigns?: readonly CampaignStateHint[] },
): TodayItem[] {
  if (!summary) return [];
  const items: TodayItem[] = [];
  if (access.serviceDesk && typeof summary.pending_confirm === "number" && Number.isFinite(summary.pending_confirm) && summary.pending_confirm > 0) {
    items.push({
      id: "confirm",
      object: "门店顾客发布复核",
      title: `${summary.pending_confirm} 件顾客发布等待确认`,
      status: "顾客已提交，尚未确认",
      impact: "复核证据后才能按约办理权益",
      action: "查看并确认顾客事项",
      href: "/work/service?return=/",
      returnOrigin: "/",
      tone: "now",
    });
  }
  if (access.owner && typeof summary.todos?.billing_anomalies === "number" && Number.isFinite(summary.todos.billing_anomalies) && summary.todos.billing_anomalies > 0) {
    items.push({
      id: "billing",
      object: "组织订购与账单",
      title: `${summary.todos.billing_anomalies} 笔订单付款未完成`,
      status: "订单失败，尚未生效",
      impact: "核对原单后决定继续付款或联系支持，切勿重复下单",
      action: "处理账单订单",
      href: "/work/billing?return=/",
      returnOrigin: "/",
      tone: "now",
    });
  }
  // 正常暂停、结束、过期的活动（access.campaigns 中的 paused/ended/expired）、
  // issued 未核销库存（pending_redeem）、停用动作（unavailable_actions）、空内容绑定（missing_content）
  // 以及独立商家矩阵（HUI-2390）都不是商家的异常待办或服务上线前置任务。
  // 授权计数缺少精确对象与恢复目标，不把聚合数伪造为可处理队列。
  return items;
}
