// MX-OPS C0 queue-view consumption (readers only).
//
// Rules copied from the contract (mx-ops-c0/v1 @ 6ffc90ca):
//   - unknown is null, a measured 0 stays 0;
//   - waiting_jobs_ahead excludes running jobs, running_jobs is separate;
//   - ETA is a range and has no sample when unknown (both fields null together);
//   - internal_debug is internal-only: the public projection deletes the key.
// This module never turns null into 0 and never invents a wait/ETA.

import {
  CONTRACT_VERSION,
  type JobQueueView,
  type LegacyQueue,
  type PolicyTemplate,
  type WaitReason,
} from "./contract";

export type PublicJobQueueView = Omit<JobQueueView, "internal_debug">;

export type WaitKind =
  | "none"
  | "cloud"
  | "capacity"
  | "window"
  | "eligibility"
  | "budget"
  | "authorization"
  | "material"
  | "unknown";

export interface MerchantQueue {
  jobRef: string;
  queueKind: PolicyTemplate;
  queueKindLabel: string;
  phase: JobQueueView["phase"];
  phaseLabel: string;
  stageLabel: string;
  waitReason: WaitReason | null;
  waitKind: WaitKind;
  waitLabel: string;
  position: number | null;
  jobsAhead: number;
  jobsAheadText: string;
  waitingJobsAhead: number | null;
  waitingJobsAheadText: string;
  runningJobs: number | null;
  runningJobsText: string;
  etaSecondsMin: number | null;
  etaSecondsMax: number | null;
  etaText: string;
  nextEligibleAtUnix: number | null;
  nextEligibleText: string | null;
  policySource: string | null;
  policyVersion: number | null;
  capacityState: JobQueueView["capacity_state"];
  capacityLabel: string;
  borrowed: boolean | null;
  asOfUnix: number;
  asOfText: string;
}

const PHASE_LABEL: Record<JobQueueView["phase"], string> = {
  admitted: "已受理",
  waiting: "排队等待",
  dispatching: "派单中",
  running: "生成中",
  finishing: "收尾中",
};

const STAGE_LABEL: Record<JobQueueView["stage"], string> = {
  batch: "批量",
  video: "视频",
  generate: "生成",
  enhance: "增强",
};

const TEMPLATE_LABEL: Record<PolicyTemplate, string> = {
  fixed_card: "固定卡",
  paid_elastic: "付费弹性",
  night_batch: "夜间批量",
  free_idle: "免费闲时",
};

const WAIT_KIND: Record<WaitReason, WaitKind> = {
  eligibility: "eligibility",
  capacity: "capacity",
  budget: "budget",
  authorization: "authorization",
  time_window: "window",
  borrowed_slot: "cloud",
  concurrency_limit: "cloud",
  material_unavailable: "material",
  unknown: "unknown",
};

const WAIT_LABEL: Record<WaitReason, string> = {
  eligibility: "等待资格确认",
  capacity: "等待云端算力",
  budget: "等待预算确认",
  authorization: "等待授权确认",
  time_window: "等待可执行时间窗",
  borrowed_slot: "等待借用的空闲算力",
  concurrency_limit: "等待并发空位",
  material_unavailable: "素材或参考图暂不可用",
  unknown: "等待原因未知",
};

const CAPACITY_LABEL: Record<NonNullable<JobQueueView["capacity_state"]>, string> = {
  hot: "算力已就绪",
  busy: "算力忙碌",
  warming: "算力预热中",
  waiting_capacity: "等待算力",
  unavailable: "算力不可用",
};

const PHASES = new Set(Object.keys(PHASE_LABEL));
const STAGES = new Set(Object.keys(STAGE_LABEL));
const TEMPLATES = new Set(Object.keys(TEMPLATE_LABEL));
const WAIT_REASONS = new Set(Object.keys(WAIT_LABEL));
const CAPACITY = new Set(Object.keys(CAPACITY_LABEL));

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonNegativeInt(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

function isNullableNonNegativeInt(value: unknown): value is number | null {
  return value === null || isNonNegativeInt(value);
}

const REQUIRED_KEYS = [
  "contract_version",
  "object_kind",
  "job_ref",
  "queue_ref",
  "as_of_unix",
  "queue_kind",
  "phase",
  "stage",
  "wait_reason",
  "position",
  "jobs_ahead",
  "waiting_jobs_ahead",
  "running_jobs",
  "running_jobs_ahead",
  "eta_seconds_min",
  "eta_seconds_max",
  "next_eligible_at_unix",
  "policy_source",
  "policy_version",
  "capacity_state",
  "borrowed",
  "internal_debug",
] as const;

// readJobQueueView is strict: a document that violates the contract is not
// partially trusted, it is null (nothing is guessed and nothing is zero-filled).
export function readJobQueueView(raw: unknown): JobQueueView | null {
  if (!isRecord(raw)) return null;
  if (raw.contract_version !== CONTRACT_VERSION) return null;
  if (raw.object_kind !== "job_queue_view") return null;
  for (const key of REQUIRED_KEYS) {
    if (!(key in raw)) return null;
  }
  if (typeof raw.job_ref !== "string" || raw.job_ref.trim() === "") return null;
  if (typeof raw.queue_ref !== "string" || raw.queue_ref.trim() === "") return null;
  if (!isNonNegativeInt(raw.as_of_unix)) return null;
  if (typeof raw.queue_kind !== "string" || !TEMPLATES.has(raw.queue_kind)) return null;
  if (typeof raw.phase !== "string" || !PHASES.has(raw.phase)) return null;
  if (typeof raw.stage !== "string" || !STAGES.has(raw.stage)) return null;
  if (raw.wait_reason !== null && (typeof raw.wait_reason !== "string" || !WAIT_REASONS.has(raw.wait_reason))) {
    return null;
  }
  if (!isNullableNonNegativeInt(raw.position)) return null;
  if (!isNonNegativeInt(raw.jobs_ahead)) return null;
  if (!isNullableNonNegativeInt(raw.waiting_jobs_ahead)) return null;
  if (!isNullableNonNegativeInt(raw.running_jobs)) return null;
  if (!isNullableNonNegativeInt(raw.running_jobs_ahead)) return null;
  if (!isNullableNonNegativeInt(raw.eta_seconds_min)) return null;
  if (!isNullableNonNegativeInt(raw.eta_seconds_max)) return null;
  const min = raw.eta_seconds_min;
  const max = raw.eta_seconds_max;
  // Contract semantic: ETA is both-null (no sample) or a valid ordered range.
  if ((min === null) !== (max === null)) return null;
  if (typeof min === "number" && typeof max === "number" && min > max) return null;
  if (raw.next_eligible_at_unix !== null && !isNonNegativeInt(raw.next_eligible_at_unix)) return null;
  if (raw.policy_source !== null && typeof raw.policy_source !== "string") return null;
  if (raw.policy_version !== null && !isNonNegativeInt(raw.policy_version)) return null;
  if (raw.capacity_state !== null && (typeof raw.capacity_state !== "string" || !CAPACITY.has(raw.capacity_state))) {
    return null;
  }
  if (raw.borrowed !== null && typeof raw.borrowed !== "boolean") return null;
  if (raw.internal_debug !== null) {
    if (!isRecord(raw.internal_debug)) return null;
    if (
      !isNonNegativeInt(raw.internal_debug.ready_workers) ||
      !isNonNegativeInt(raw.internal_debug.busy_workers) ||
      !isNonNegativeInt(raw.internal_debug.starting_workers)
    ) {
      return null;
    }
  }
  return raw as unknown as JobQueueView;
}

// publicQueueView is the only shape allowed to leave the server/browser boundary:
// the internal_debug key is deleted entirely, never nulled in place.
export function publicQueueView(raw: unknown): PublicJobQueueView | null {
  const view = readJobQueueView(raw);
  if (!view) return null;
  const { internal_debug: _internalDebug, ...publicView } = view;
  return publicView;
}

function humanDuration(seconds: number): string {
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} 小时` : `${hours} 小时 ${rest} 分钟`;
}

export function etaText(min: number | null, max: number | null): string {
  if (min === null || max === null) return "暂不可估";
  if (min === 0 && max === 0) return "预计 0 分钟";
  if (min === max) return `约 ${humanDuration(min)}`;
  return `约 ${humanDuration(min)}–${humanDuration(max)}`;
}

const SHANGHAI = "Asia/Shanghai";
const WHEN_FORMAT = new Intl.DateTimeFormat("zh-CN", {
  timeZone: SHANGHAI,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

function whenText(unix: number): string {
  const parts = WHEN_FORMAT.formatToParts(new Date(unix * 1000));
  const pick = (type: string) => parts.find((part) => part.type === type)?.value ?? "";
  return `${pick("year")}-${pick("month")}-${pick("day")} ${pick("hour")}:${pick("minute")}`;
}

export function presentQueue(view: JobQueueView | null): MerchantQueue | null {
  if (!view) return null;
  const waiting = view.phase === "waiting";
  const reason = view.wait_reason;
  const waitKind: WaitKind = !waiting ? "none" : reason ? WAIT_KIND[reason] : "unknown";
  const waitLabel = !waiting ? PHASE_LABEL[view.phase] : reason ? WAIT_LABEL[reason] : "排队中，原因未知";
  return {
    jobRef: view.job_ref,
    queueKind: view.queue_kind,
    queueKindLabel: TEMPLATE_LABEL[view.queue_kind],
    phase: view.phase,
    phaseLabel: PHASE_LABEL[view.phase],
    stageLabel: STAGE_LABEL[view.stage],
    waitReason: reason,
    waitKind,
    waitLabel,
    position: view.position,
    jobsAhead: view.jobs_ahead,
    jobsAheadText: `前面共 ${view.jobs_ahead} 个任务（含运行中）`,
    waitingJobsAhead: view.waiting_jobs_ahead,
    waitingJobsAheadText: view.waiting_jobs_ahead === null ? "未知" : `${view.waiting_jobs_ahead} 个`,
    runningJobs: view.running_jobs,
    runningJobsText: view.running_jobs === null ? "未知" : `${view.running_jobs} 个`,
    etaSecondsMin: view.eta_seconds_min,
    etaSecondsMax: view.eta_seconds_max,
    etaText: etaText(view.eta_seconds_min, view.eta_seconds_max),
    nextEligibleAtUnix: view.next_eligible_at_unix,
    nextEligibleText: view.next_eligible_at_unix === null ? null : whenText(view.next_eligible_at_unix),
    policySource: view.policy_source,
    policyVersion: view.policy_version,
    capacityState: view.capacity_state,
    capacityLabel: view.capacity_state === null ? "未知" : CAPACITY_LABEL[view.capacity_state],
    borrowed: view.borrowed,
    asOfUnix: view.as_of_unix,
    asOfText: whenText(view.as_of_unix),
  };
}

// legacyQueue reads the frozen legacy object without redefining its inclusive
// jobs_ahead meaning. It is shown separately from queue_view.
export function legacyQueue(raw: unknown): LegacyQueue | null {
  if (!isRecord(raw)) return null;
  if (!isNonNegativeInt(raw.position) || !isNonNegativeInt(raw.jobs_ahead)) return null;
  if (typeof raw.capacity_state !== "string" || !CAPACITY.has(raw.capacity_state)) return null;
  if (raw.estimated_wait_seconds !== undefined && !isNonNegativeInt(raw.estimated_wait_seconds)) return null;
  return raw as unknown as LegacyQueue;
}
