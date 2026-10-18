// Vendored copy of the MX-OPS C0 contract v1 TypeScript wire types.
// Source: 0xclaw-labs/public-ai docs/contracts/mx-ops-c0/v1/mx-ops-c0.v1.ts @ 6ffc90ca
// Rule: readers ignore unknown fields; do not edit contract semantics locally.
// Consumers record the adopted sha in docs/mx-ops/<ticket>/contract-version.md.

// MX-OPS C0 contract v1 — TypeScript wire types (copyable, zero runtime deps).
//
// Source of truth: docs/contracts/mx-ops-c0/v1/ (mx-ops-c0.v1.schema.json,
// error-taxonomy.json, vectors.json) at one published commit sha. Copy this
// file into a product repo; do not edit the contract semantics locally.
//
// Rules: additive-only inside v1; readers MUST ignore unknown fields; unknown
// is null (never 0/""/false); money is integer minor units (CNY). This file is
// types only: no imports, no functions that decide policy server-side.

export const CONTRACT_VERSION = "mx-ops-c0/v1" as const;
export const DIGEST_DOMAIN = "painuo.mx-ops.c0/v1\n" as const;

export type ContractVersion = typeof CONTRACT_VERSION;
export type Sha256Digest = `sha256:${string}`;
export type UnixSeconds = number;
export type Ref = string;

export interface Money {
  currency: "CNY";
  amount_minor: number;
}

export type PolicyTemplate = "fixed_card" | "paid_elastic" | "night_batch" | "free_idle";

// ---- model offering: references only, never an inlined price ----
export interface ModelOffering {
  contract_version: ContractVersion;
  object_kind: "model_offering";
  offering_id: Ref;
  offering_version: number;
  model_id: Ref;
  display_name?: string;
  capability: Ref;
  capability_ref: { capability_id: Ref; descriptor_version: Ref };
  price_ref: { price_publication_ref: Ref; pricing_version: Ref; currency: "CNY" };
  spec: {
    profile: string | null;
    resolution: string | null;
    duration_steps_seconds: number[] | null;
    output_kind: "video" | "image" | "audio" | "unknown";
  };
  allowed_policy_templates: PolicyTemplate[];
  runtime_ref: { runtime_contract: Ref; model_id: Ref };
  availability: {
    state: "available" | "degraded" | "unavailable" | "unknown";
    as_of_unix: UnixSeconds;
    reason_code: Ref | null;
  };
  source: { owner_service: Ref; owner_contract: Ref };
  published_at_unix: UnixSeconds;
  supersedes_offering_version?: number | null;
  digest: Sha256Digest;
}

// ---- execution policy: intersection down the precedence chain ----
export type PolicyScopeLevel = "platform" | "pool" | "model" | "tenant" | "project" | "user";

export interface PolicyScope {
  level: PolicyScopeLevel;
  tenant_id: Ref | null;
  project_id: Ref | null;
  app_id: Ref | null;
  user_id: Ref | null;
  model_id: Ref | null;
  pool_ref: Ref | null;
}

export interface TimeWindowRule {
  days_of_week: number[];
  start_minute_of_day: number;
  end_minute_of_day: number;
}

export interface TimeWindow {
  timezone: string;
  windows: TimeWindowRule[];
  not_before_unix: UnixSeconds | null;
  start_before_unix: UnixSeconds | null;
  queue_ttl_seconds: number | null;
}

export interface ExecutionPolicy {
  contract_version: ContractVersion;
  object_kind: "execution_policy";
  policy_id: Ref;
  policy_version: number;
  policy_scope: PolicyScope;
  template: PolicyTemplate;
  applicable: { capabilities: Ref[]; model_ids: Ref[]; pool_refs: Ref[] };
  time_window: TimeWindow | null;
  dispatch: {
    allow_create: boolean;
    allow_resume: boolean;
    allow_warm_keepalive: boolean;
    max_starting: number | null;
  };
  compute_permission: {
    allowed_pool_refs: Ref[];
    allow_borrow_idle: boolean;
    max_borrow_workers: number | null;
  };
  cost_bearing: {
    payer: "user" | "platform_subsidy" | "internal" | "campaign";
    entitlement_ref: Ref | null;
  };
  service_level: { tier: "standard" | "priority" | "batch"; weight: number };
  conflict: { mode: "intersect"; on_conflict: "reject" };
  enabled: boolean;
  published_at_unix: UnixSeconds;
  digest: Sha256Digest;
}

// ---- compute pool ----
export interface ComputePool {
  contract_version: ContractVersion;
  object_kind: "compute_pool";
  pool_ref: `compute://${string}`;
  pool_version: number;
  capacity_mode: "resident_only" | "reuse_only" | "elastic";
  min_hot: number;
  max_workers: number;
  max_starting: number;
  idle_timeout_seconds: number;
  startup_timeout_seconds: number;
  cooldown_seconds: number;
  budget: {
    currency: "CNY";
    hourly_minor: number | null;
    daily_minor: number | null;
    committed_minor: number | null;
    unbilled_minor: number | null;
  };
  zero_capacity_action: "queue" | "reject";
  on_budget_exhausted: "queue" | "reject_new" | "stop_free_only";
  borrow: { borrowable: boolean; max_borrow_workers: number | null; scope: "market_idle_only" };
  compatibility: {
    gpu_model: Ref;
    runtime_contract: Ref;
    worker_protocol_version: Ref;
    capabilities: Ref[];
  };
  reuse_fallback: "unsupported" | "controlled_resume_only" | null;
  observed_capacity: {
    ready: number;
    busy: number;
    starting: number;
    desired: number;
    observed_at_unix: UnixSeconds;
  } | null;
  published_at_unix: UnixSeconds;
  digest: Sha256Digest;
}

// ---- management command + response: four states, partial is explicit ----
export type ConfigCommandName = "read" | "validate" | "preview" | "publish" | "apply-status" | "rollback";
export type ConfigObjectKind =
  | "model_offering"
  | "execution_policy"
  | "compute_pool"
  | "config_command"
  | "config_command_response"
  | "execution_grant"
  | "job_queue_view";

export interface ConfigCommand {
  contract_version: ContractVersion;
  object_kind: "config_command";
  command_id: Ref;
  command: ConfigCommandName;
  revision?: number | null;
  expected_revision?: number | null;
  idempotency_key: Ref;
  actor: { kind: "admin" | "service" | "automation"; id: Ref; roles: Ref[] };
  target: {
    service: Ref;
    object_kind: ConfigObjectKind;
    object_ref: Ref;
    environment: "dev" | "staging" | "production";
    tenant_id: Ref | null;
    project_id: Ref | null;
  };
  reason: string;
  payload?: Record<string, unknown> | null;
  requested_at_unix: UnixSeconds;
  digest: Sha256Digest;
}

export type ErrorClass =
  | "validation"
  | "conflict"
  | "idempotency"
  | "permission"
  | "budget"
  | "authorization"
  | "unknown"
  | "unsupported"
  | "unavailable"
  | "partial";

export interface ErrorBody {
  class: ErrorClass;
  code: string;
  retryable: boolean;
  retry_after_seconds: number | null;
  field: string | null;
  detail: string | null;
  as_of_unix: UnixSeconds | null;
}

export interface ErrorEnvelope {
  ok: false;
  error: ErrorBody;
}

export interface ObjectState {
  object_kind: ConfigObjectKind;
  object_ref: Ref;
  desired_state: "saved" | "published";
  adoption_state: "not_adopted" | "adopted";
  convergence_state: "converging" | "converged" | "failed" | "unknown";
  as_of_unix: UnixSeconds;
  error: ErrorBody | null;
}

export interface ConfigCommandResponse {
  contract_version: ContractVersion;
  object_kind: "config_command_response";
  command_id: Ref;
  status: "accepted" | "rejected" | "conflict" | "unsupported" | "unknown" | "partial";
  revision: number | null;
  result_digest: Sha256Digest | null;
  objects: ObjectState[];
  error?: ErrorBody | null;
  as_of_unix: UnixSeconds;
  digest: Sha256Digest;
}

// ---- execution grant: runtime verifies, never creates a wallet ----
export interface ExecutionGrant {
  contract_version: ContractVersion;
  object_kind: "execution_grant";
  grant_id: Ref;
  grant_version: number;
  issuer: { service: Ref; contract_version: Ref };
  subject: {
    tenant_id: Ref;
    app_id: Ref;
    user_id: Ref;
    payer_account_id: Ref;
    payer_user_id: Ref | null;
  };
  entitlement: {
    entitlement_ref: Ref;
    kind: "paid" | "subsidy" | "internal" | "campaign" | "personal_credit";
    max_charge_minor: number | null;
    currency: "CNY";
  };
  cost_bearing: {
    payer_kind: "user" | "platform_subsidy" | "internal" | "campaign";
    payer_ref: Ref;
  };
  meter_only: true;
  cost_authorization: {
    authorization_ref: Ref;
    amount_minor: number;
    currency: "CNY";
    account_id: Ref;
    expires_at_unix: UnixSeconds;
  };
  policy_ref: { policy_id: Ref; policy_version: number };
  price_ref: {
    price_publication_ref: Ref;
    pricing_version: Ref;
    quoted_amount_minor: number;
    quote_expires_at_unix: UnixSeconds;
  };
  input_digest: Sha256Digest;
  validity: {
    issued_at_unix: UnixSeconds;
    not_before_unix: UnixSeconds | null;
    expires_at_unix: UnixSeconds;
    revoked_at_unix: UnixSeconds | null;
  };
  idempotency: {
    key: Ref;
    logical_request_id: Ref;
    scope: ("tenant_id" | "app_id" | "user_id" | "payer_account_id")[];
  };
  verification_profile: ContractVersion;
  digest: Sha256Digest;
}

// ---- queue view: additive sibling of the frozen legacy "queue" ----
export type WaitReason =
  | "eligibility"
  | "capacity"
  | "budget"
  | "authorization"
  | "time_window"
  | "borrowed_slot"
  | "concurrency_limit"
  | "material_unavailable"
  | "unknown";

export interface JobQueueView {
  contract_version: ContractVersion;
  object_kind: "job_queue_view";
  job_ref: Ref;
  queue_ref: Ref;
  as_of_unix: UnixSeconds;
  queue_kind: PolicyTemplate;
  phase: "admitted" | "waiting" | "dispatching" | "running" | "finishing";
  stage: "batch" | "video" | "generate" | "enhance";
  wait_reason: WaitReason | null;
  position: number | null;
  /** legacy inclusive meaning: running jobs ahead are counted too */
  jobs_ahead: number;
  /** queued ahead, running excluded */
  waiting_jobs_ahead: number | null;
  running_jobs: number | null;
  running_jobs_ahead: number | null;
  /** unknown is null; a measured 0 stays 0 */
  eta_seconds_min: number | null;
  eta_seconds_max: number | null;
  next_eligible_at_unix: UnixSeconds | null;
  policy_source: Ref | null;
  policy_version: number | null;
  capacity_state: "hot" | "busy" | "warming" | "waiting_capacity" | "unavailable" | null;
  borrowed: boolean | null;
  /** internal-only: public projection must delete this key entirely */
  internal_debug: { ready_workers: number; busy_workers: number; starting_workers: number } | null;
}

/** Frozen legacy queue object. Semantics are not redefined by C0. */
export interface LegacyQueue {
  position: number;
  jobs_ahead: number;
  estimated_wait_seconds?: number;
  capacity_state: "hot" | "busy" | "warming" | "waiting_capacity" | "unavailable";
  ready_workers?: number;
  busy_workers?: number;
  starting_workers?: number;
}
