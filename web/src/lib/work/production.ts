// HUI-3553 MX-OPS 增量：商家云端制作等待视图。
//
// 纪律：
//   - 队列/phase/ETA 只来自权威 job_queue_view；没有投影就如实 UNKNOWN，
//     不给伪进度；
//   - 夜间窗口与活动发布日期分开比较：赶不上只提示，不自动升级付费/改活动；
//   - 商家工具费 / 活动优惠预算 / 模型实际成本分开记录，不合并不建钱包；
//   - 恢复只回同一 job/handoff，不新建制作工程；
//   - 回流只进候选，排除自动采用/自动外发；
//   - 顾客触碰只读已采用预制内容，零新生成。

import { presentQueue, readJobQueueView, type MerchantQueue } from "@/lib/mx-ops/queue";

export interface ProductionJob {
  id?: string;
  handoff_id?: string;
  campaign_id?: string;
  purpose?: string;
  desired_form?: string;
  execution_preference?: string;
  policy_ref?: string;
  policy_version?: number;
  job_ref?: string;
  created_at?: string;
  job_queue_view?: unknown;
  candidate_version_id?: string | null;
}

export interface ProductionContext {
  campaignPublishAtUnix?: number | null;
  entitlementRefs?: string[];
  quoteConfirmed?: boolean;
  merchantToolFeeMinor?: number | null;
  campaignBudgetMinor?: number | null;
  modelCostMinor?: number | null;
  currency?: "CNY";
}

export type ProductionWaitKind = "none" | "cloud" | "window" | "human" | "unknown";

export interface ProductionView {
  handoffId: string | null;
  jobRef: string | null;
  preferenceLabel: string;
  queue: MerchantQueue | null;
  waitingKind: ProductionWaitKind;
  waitingText: string;
  nextEligibleText: string | null;
  windowAfterPublish: boolean;
  publishWarning: string | null;
  feeLines: {
    merchantToolFee: string;
    campaignBudget: string;
    modelCost: string;
  };
  feeNote: string;
  candidate: {
    ref: string | null;
    state: "none" | "candidate";
    autoAdopted: false;
    adoptHint: string;
  };
  recovery: {
    kind: "same_job" | "new";
    jobRef: string | null;
    text: string;
  };
  notice: string;
  autoUpgrade: false;
  autoAdopt: false;
}

const PREFERENCE_LABEL: Record<string, string> = {
  immediate: "立即（固定卡排队）",
  paid_elastic: "付费优先（弹性扩容）",
  night_batch: "夜间批量",
  free_idle: "免费闲时",
};

function preferenceLabel(value: string | undefined): string {
  const preference = (value ?? "").trim();
  if (preference === "") return "未指定（按服务器策略）";
  return PREFERENCE_LABEL[preference] ?? "未指定的执行偏好";
}

function formatMinor(minor: number | null | undefined): string {
  if (minor === null || minor === undefined || !Number.isFinite(minor)) return "未提供";
  const sign = minor < 0 ? "-" : "";
  const absolute = Math.abs(Math.round(minor));
  const yuan = Math.floor(absolute / 100);
  const cents = String(absolute % 100).padStart(2, "0");
  return `${sign}¥${yuan}.${cents}`;
}

function waitKindOf(queue: MerchantQueue | null, hasCandidate: boolean): ProductionWaitKind {
  // A returned candidate means production has landed; the remaining wait is
  // the merchant's explicit preview/adopt decision, not a cloud slot.
  if (hasCandidate) return "human";
  if (queue) {
    switch (queue.waitKind) {
      case "none":
        return "none";
      case "capacity":
      case "cloud":
      case "budget":
      case "authorization":
      case "eligibility":
      case "material":
        return "cloud";
      case "window":
        return "window";
      case "unknown":
        return "unknown";
    }
  }
  return "unknown";
}

function waitingTextOf(queue: MerchantQueue | null, hasCandidate: boolean): string {
  if (hasCandidate) {
    return "云端已回流候选，等待商家预览并决定是否采用。";
  }
  if (queue) {
    if (queue.phase === "waiting") {
      return `${queue.waitLabel}；预估 ${queue.etaText}`;
    }
    return `${queue.phaseLabel}；${queue.waitLabel}`;
  }
  return "云端排队状态未知：没有拿到权威队列投影，不提供估算时间。";
}

export function presentProduction(job: ProductionJob, ctx: ProductionContext = {}): ProductionView {
  const queue = presentQueue(readJobQueueView(job.job_queue_view));
  const candidateRef = (job.candidate_version_id ?? "").trim();
  const hasCandidate = candidateRef !== "";
  const publishAt =
    typeof ctx.campaignPublishAtUnix === "number" && Number.isFinite(ctx.campaignPublishAtUnix)
      ? ctx.campaignPublishAtUnix
      : null;
  const nextEligible = queue?.nextEligibleAtUnix ?? null;
  const windowAfterPublish = nextEligible !== null && publishAt !== null && nextEligible > publishAt;
  const jobRef = (job.job_ref ?? "").trim();
  const handoffId = (job.handoff_id ?? job.id ?? "").trim();
  const night = (job.execution_preference ?? "").trim() === "night_batch";

  return {
    handoffId: handoffId === "" ? null : handoffId,
    jobRef: jobRef === "" ? null : jobRef,
    preferenceLabel: preferenceLabel(job.execution_preference),
    queue,
    waitingKind: waitKindOf(queue, hasCandidate),
    waitingText: waitingTextOf(queue, hasCandidate),
    nextEligibleText: queue?.nextEligibleText ?? null,
    windowAfterPublish,
    publishWarning: windowAfterPublish
      ? "下一可执行窗口晚于活动发布日期，预计赶不上发布。可以先上传已有内容或与运营确认；不会自动升级付费，也不会自动改活动日期。"
      : null,
    feeLines: {
      merchantToolFee: formatMinor(ctx.merchantToolFeeMinor),
      campaignBudget: formatMinor(ctx.campaignBudgetMinor),
      modelCost: formatMinor(ctx.modelCostMinor),
    },
    feeNote:
      "商家工具费、活动优惠预算、模型实际成本分开记录。唯一模型收费由星模发起、公共 Billing 记账，不建立第二个钱包。",
    candidate: {
      ref: hasCandidate ? candidateRef : null,
      state: hasCandidate ? "candidate" : "none",
      autoAdopted: false,
      adoptHint: hasCandidate
        ? "候选不会自动替换当前采用内容；请在「活动内容」里预览后显式采用。"
        : "还没有回流候选。",
    },
    recovery:
      jobRef === ""
        ? {
            kind: "new",
            jobRef: null,
            text: "还没有可恢复的制作任务；先安排一次制作。",
          }
        : {
            kind: "same_job",
            jobRef,
            text: "刷新或离场后回到同一制作任务，不重新提交、不重复扣费。",
          },
    notice: night
      ? "夜间批量只在新派单时使用窗口；队列完成只回到候选，不会自动外发、不会自动替换当前启用内容。"
      : "队列完成只回到候选，不会自动外发、不会自动替换当前启用内容。",
    autoUpgrade: false,
    autoAdopt: false,
  };
}

export function visitorContentGuard(): { generationAllowed: false; reason: string } {
  return {
    generationAllowed: false,
    reason: "顾客触碰只读取已采用的预制内容；不会新开 GPU、重新生成或等待云端队列。",
  };
}
