/**
 * eco-nav/v1 消费端。
 *
 * parseEcoNavDocument 按 public-ai #62 @ be6d7d3 的 eco-nav.v1.ts /
 * eco-nav-model.schema.json 收口：Blocked / Selection / Resolved。
 * 未知键、未知枚举、错误版本一律 schema_incompatible。
 * toViewModel 只映射顶栏。身份来源由加载器标注，文档里没有 provenance。
 */

export const NAV_INTENT_MANUAL_SWITCH = "manual_switch" as const;

export const ECO_NAV_SCHEMA_VERSION = "eco-nav/v1" as const;
export const TOUCH_APP_ID = "touch" as const;
export const BILLING_UNCONFIRMED = "额度需确认";
export const PROVISIONAL_STATUS_SUMMARY = "预览上下文 · 未接公共身份";
export const REGISTRY_WITHOUT_IDENTITY_SUMMARY = "目录已读取，身份上下文仍为预览";
export const LAUNCH_UNRESOLVED_RESIDUAL =
  "启动目标尚未解析：允许列表里没有可安全跳转的地址，不会伪造跳转地址。";

const BLOCKED_QUERY_KEYS = ["order_id", "handoff_id", "claim_code", "stage_id", "brief_version", "campaign_id"] as const;

const SLUG = /^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$/;
const LABEL = /^[^\u0000-\u001f\u007f-\u009f]*[^\s\u0000-\u001f\u007f-\u009f][^\u0000-\u001f\u007f-\u009f]*$/;
const REF = /^[^\s\u0000-\u001f\u007f-\u009f]+$/;
const ICON_REF = /^[a-z0-9][a-z0-9:._-]{0,63}$/;
const HTTPS_URL = /^https:\/\/[^\s/?#@]+(\/[^\s?#]*)?(\?[^\s#]+)?$/;
const RFC3339 = /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$/;
const CONFIG_VERSION = /^[A-Za-z0-9._-]{1,64}$/;
const ACCENT = /^#[0-9a-f]{6}$/;

const NAV_STATES = ["ready", "degraded", "selection_required", "hidden", "denied", "unavailable"] as const;
const STATUS_REASONS = [
  "public_surface",
  "brand_suspended",
  "brand_unknown",
  "unauthenticated",
  "membership_denied",
  "authority_unavailable",
  "tenant_selection_required",
  "billing_unconfigured",
  "billing_account_unknown",
  "billing_unavailable",
  "entitlement_unavailable",
] as const;
const BRAND_KINDS = ["first_party", "white_label"] as const;
const APP_STATES = ["current", "launchable", "entitlement_required", "temporarily_unavailable"] as const;
const LAUNCH_MODES = ["sso_launch", "none"] as const;
const SCOPE_STATES = ["resolved", "selection_required"] as const;
const SEAT_SOURCES = ["membership", "delegation"] as const;
const DELEGATION_TYPES = ["maker_service", "ops_collab"] as const;
const BILLING_KINDS = ["wallet", "customer_quota"] as const;
const BILLING_REASONS = ["unconfigured", "account_unknown", "unavailable"] as const;
const PAYER_SOURCES = ["personal", "delegation"] as const;
const AVAILABILITIES = ["available", "exhausted", "unknown"] as const;
const AMOUNT_UNITS = ["cny_fen", "quota_credit"] as const;
const BILLING_ENTRIES = ["billing_center", "brand_quota_page"] as const;
const ACCOUNT_ROLES = ["owner", "payer", "viewer", "none"] as const;

const TOP_KEYS = [
  "schema_version",
  "status",
  "brand",
  "app",
  "visible_apps",
  "work_context",
  "role",
  "billing",
  "return_context",
  "capabilities",
] as const;

export type EcoNavProvenance = "public_ai_context" | "provisional_fixture";
export type NavState = (typeof NAV_STATES)[number];
export type StatusReason = (typeof STATUS_REASONS)[number];
export type PayerSource = (typeof PAYER_SOURCES)[number];
export type EcoNavViewport = "wide" | "narrow";

export interface Brand {
  brand_id: string;
  kind: (typeof BRAND_KINDS)[number];
  status: "active";
  config_version: string;
  display_name: string;
  logo: { url: string; alt: string } | null;
  theme: { token_set_ref: string; accent: string | null };
  support: { label: string; url: string | null } | null;
}

export interface CurrentApp {
  app_id: string;
  display_name: string;
  icon_ref: string | null;
  home_target_id: string;
}

export type VisibleApp = {
  app_id: string;
  display_name: string;
  icon_ref: string | null;
} & (
  | { state: "current"; launch_mode: "none"; launch_target_id: null; unavailable_reason: null }
  | { state: "launchable"; launch_mode: "sso_launch"; launch_target_id: string; unavailable_reason: null }
  | {
      state: "entitlement_required";
      launch_mode: "none";
      launch_target_id: null;
      unavailable_reason: "entitlement_missing";
    }
  | {
      state: "temporarily_unavailable";
      launch_mode: "none";
      launch_target_id: null;
      unavailable_reason: "entitlement_unavailable";
    }
);

export interface Scope {
  tenant_id: string;
  display_name: string | null;
  source: (typeof SEAT_SOURCES)[number];
}

export interface WorkContext {
  state: (typeof SCOPE_STATES)[number];
  current_scope: Scope | null;
  switchable_scopes: Scope[];
  restored: boolean;
}

export interface Delegation {
  delegation_id: string;
  type: (typeof DELEGATION_TYPES)[number];
  expires_at: string;
}

export interface Role {
  label: string;
  source: (typeof SEAT_SOURCES)[number];
  delegations: Delegation[];
}

export interface BillingAmount {
  value_minor: number;
  unit: (typeof AMOUNT_UNITS)[number];
}

export interface Billing {
  kind: (typeof BILLING_KINDS)[number];
  known: boolean;
  reason: (typeof BILLING_REASONS)[number] | null;
  payer_source: PayerSource | null;
  viewer_account_role: (typeof ACCOUNT_ROLES)[number] | null;
  availability: (typeof AVAILABILITIES)[number];
  amount: BillingAmount | null;
  entry: (typeof BILLING_ENTRIES)[number] | null;
}

export interface ReturnContext {
  source_app_id: string;
  return_target_id: string;
  label: string;
  source_ref: string | null;
}

export interface Capabilities {
  can_switch_app: boolean;
  can_switch_tenant: boolean;
  can_manage_members: boolean;
  can_open_billing: boolean;
}

interface ModelBase {
  schema_version: typeof ECO_NAV_SCHEMA_VERSION;
  capabilities: Capabilities;
}

export interface BlockedModel extends ModelBase {
  status: { state: "hidden" | "denied" | "unavailable"; reasons: StatusReason[] };
  brand: null;
  app: null;
  visible_apps: [];
  work_context: null;
  role: null;
  billing: null;
  return_context: null;
}

export interface SelectionModel extends ModelBase {
  status: { state: "selection_required"; reasons: ["tenant_selection_required"] };
  brand: Brand;
  app: CurrentApp;
  visible_apps: VisibleApp[];
  work_context: WorkContext;
  role: null;
  billing: null;
  return_context: null;
}

export interface ResolvedModel extends ModelBase {
  status: { state: "ready" | "degraded"; reasons: StatusReason[] };
  brand: Brand;
  app: CurrentApp;
  visible_apps: VisibleApp[];
  work_context: WorkContext;
  role: Role;
  billing: Billing;
  return_context: ReturnContext | null;
}

export type EcoNavDocument = BlockedModel | SelectionModel | ResolvedModel;

export function isRenderable(model: EcoNavDocument): model is SelectionModel | ResolvedModel {
  return (
    model.status.state === "ready" ||
    model.status.state === "degraded" ||
    model.status.state === "selection_required"
  );
}

export interface EcoNavModel {
  schema_version: typeof ECO_NAV_SCHEMA_VERSION;
  provenance: EcoNavProvenance;
  renderable: boolean;
  brand: Brand | null;
  current_app: CurrentApp;
  document_app_id: string | null;
  active_tenant_id: string | null;
  source_tenant_id: string | null;
  scopes: Scope[];
  apps: VisibleApp[];
  role_label: string;
  source_role_label: string;
  seat_source: "membership" | "delegation" | null;
  source_seat_source: "membership" | "delegation" | null;
  delegations: Delegation[];
  source_delegations: Delegation[];
  payer_source: PayerSource | null;
  source_payer_source: PayerSource | null;
  billing: Billing | null;
  billing_follows_document: boolean;
  source_billing_follows: boolean;
  return_context: ReturnContext | null;
  capabilities: Capabilities;
  status: { state: NavState; reasons: StatusReason[]; summary: string };
}

export interface ManualSwitchPlan {
  nav_intent: typeof NAV_INTENT_MANUAL_SWITCH;
  app_id: string;
  launch_target_id: string | null;
  href: string | null;
  creates_handoff: false;
  residual: string | null;
}

export type LaunchResolver = (launchTargetId: string) => string | null;
export type LaunchAvailability = (launchTargetId: string) => boolean;

export type ParseEcoNavResult =
  | { ok: true; document: EcoNavDocument }
  | { ok: false; code: "schema_incompatible" };

export interface ViewModelOptions {
  provenance: EcoNavProvenance;
  statusSummary: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function keysExact(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const own = Object.keys(value);
  if (own.length !== keys.length) return false;
  return keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
}

function asExact(value: unknown, keys: readonly string[]): Record<string, unknown> | null {
  if (!isRecord(value) || !keysExact(value, keys)) return null;
  return value;
}

function inList<T extends string>(value: unknown, allowed: readonly T[]): value is T {
  return typeof value === "string" && (allowed as readonly string[]).includes(value);
}

function isSlug(value: unknown): value is string {
  return typeof value === "string" && SLUG.test(value);
}

function isLabel(value: unknown): value is string {
  return typeof value === "string" && value.length >= 1 && value.length <= 64 && LABEL.test(value);
}

function isRef(value: unknown): value is string {
  return typeof value === "string" && value.length >= 1 && value.length <= 128 && REF.test(value);
}

function isHttpsUrl(value: unknown): value is string {
  return typeof value === "string" && value.length <= 2048 && HTTPS_URL.test(value);
}

function isIconRef(value: unknown): value is string | null {
  if (value === null) return true;
  return typeof value === "string" && ICON_REF.test(value);
}

function isMinor(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function parseLogo(value: unknown): Brand["logo"] | "bad" {
  if (value === null) return null;
  const obj = asExact(value, ["url", "alt"]);
  if (!obj || !isHttpsUrl(obj.url) || !isLabel(obj.alt)) return "bad";
  return { url: obj.url, alt: obj.alt };
}

function parseSupport(value: unknown): Brand["support"] | "bad" {
  if (value === null) return null;
  const obj = asExact(value, ["label", "url"]);
  if (!obj || !isLabel(obj.label)) return "bad";
  if (obj.url !== null && !isHttpsUrl(obj.url)) return "bad";
  return { label: obj.label, url: obj.url === null ? null : obj.url };
}

function parseBrand(value: unknown): Brand | null {
  const obj = asExact(value, [
    "brand_id",
    "kind",
    "status",
    "config_version",
    "display_name",
    "logo",
    "theme",
    "support",
  ]);
  if (!obj || !isSlug(obj.brand_id) || !inList(obj.kind, BRAND_KINDS) || obj.status !== "active") return null;
  if (typeof obj.config_version !== "string" || !CONFIG_VERSION.test(obj.config_version)) return null;
  if (!isLabel(obj.display_name)) return null;
  const theme = asExact(obj.theme, ["token_set_ref", "accent"]);
  if (!theme || !isSlug(theme.token_set_ref)) return null;
  if (theme.accent !== null && (typeof theme.accent !== "string" || !ACCENT.test(theme.accent))) return null;
  const logo = parseLogo(obj.logo);
  const support = parseSupport(obj.support);
  if (logo === "bad" || support === "bad") return null;
  return {
    brand_id: obj.brand_id,
    kind: obj.kind,
    status: "active",
    config_version: obj.config_version,
    display_name: obj.display_name,
    logo,
    theme: { token_set_ref: theme.token_set_ref, accent: theme.accent === null ? null : theme.accent },
    support,
  };
}

function parseCurrentApp(value: unknown): CurrentApp | null {
  const obj = asExact(value, ["app_id", "display_name", "icon_ref", "home_target_id"]);
  if (!obj || !isSlug(obj.app_id) || !isLabel(obj.display_name) || !isIconRef(obj.icon_ref)) return null;
  if (!isSlug(obj.home_target_id)) return null;
  return {
    app_id: obj.app_id,
    display_name: obj.display_name,
    icon_ref: obj.icon_ref,
    home_target_id: obj.home_target_id,
  };
}

function parseVisibleApp(value: unknown): VisibleApp | null {
  const obj = asExact(value, [
    "app_id",
    "display_name",
    "icon_ref",
    "state",
    "launch_mode",
    "launch_target_id",
    "unavailable_reason",
  ]);
  if (!obj || !isSlug(obj.app_id) || !isLabel(obj.display_name) || !isIconRef(obj.icon_ref)) return null;
  if (!inList(obj.state, APP_STATES) || !inList(obj.launch_mode, LAUNCH_MODES)) return null;
  const base = { app_id: obj.app_id, display_name: obj.display_name, icon_ref: obj.icon_ref };
  if (
    obj.state === "current" &&
    obj.launch_mode === "none" &&
    obj.launch_target_id === null &&
    obj.unavailable_reason === null
  ) {
    return { ...base, state: "current", launch_mode: "none", launch_target_id: null, unavailable_reason: null };
  }
  if (
    obj.state === "launchable" &&
    obj.launch_mode === "sso_launch" &&
    isSlug(obj.launch_target_id) &&
    obj.unavailable_reason === null
  ) {
    return {
      ...base,
      state: "launchable",
      launch_mode: "sso_launch",
      launch_target_id: obj.launch_target_id,
      unavailable_reason: null,
    };
  }
  if (
    obj.state === "entitlement_required" &&
    obj.launch_mode === "none" &&
    obj.launch_target_id === null &&
    obj.unavailable_reason === "entitlement_missing"
  ) {
    return {
      ...base,
      state: "entitlement_required",
      launch_mode: "none",
      launch_target_id: null,
      unavailable_reason: "entitlement_missing",
    };
  }
  if (
    obj.state === "temporarily_unavailable" &&
    obj.launch_mode === "none" &&
    obj.launch_target_id === null &&
    obj.unavailable_reason === "entitlement_unavailable"
  ) {
    return {
      ...base,
      state: "temporarily_unavailable",
      launch_mode: "none",
      launch_target_id: null,
      unavailable_reason: "entitlement_unavailable",
    };
  }
  return null;
}

function parseScope(value: unknown, source: "any" | "membership"): Scope | null {
  const obj = asExact(value, ["tenant_id", "display_name", "source"]);
  if (!obj || !isRef(obj.tenant_id) || !inList(obj.source, SEAT_SOURCES)) return null;
  if (source === "membership" && obj.source !== "membership") return null;
  if (obj.display_name !== null && !isLabel(obj.display_name)) return null;
  return {
    tenant_id: obj.tenant_id,
    display_name: obj.display_name === null ? null : obj.display_name,
    source: obj.source,
  };
}

function parseWorkContext(value: unknown): WorkContext | null {
  const obj = asExact(value, ["state", "current_scope", "switchable_scopes", "restored"]);
  if (!obj || !inList(obj.state, SCOPE_STATES) || typeof obj.restored !== "boolean") return null;
  if (!Array.isArray(obj.switchable_scopes) || obj.switchable_scopes.length > 64) return null;
  let current: Scope | null = null;
  if (obj.current_scope !== null) {
    current = parseScope(obj.current_scope, "any");
    if (!current) return null;
  }
  const scopes: Scope[] = [];
  for (const item of obj.switchable_scopes) {
    const scope = parseScope(item, "membership");
    if (!scope) return null;
    scopes.push(scope);
  }
  return { state: obj.state, current_scope: current, switchable_scopes: scopes, restored: obj.restored };
}

function parseRole(value: unknown): Role | null {
  const obj = asExact(value, ["label", "source", "delegations"]);
  if (!obj || !isLabel(obj.label) || !inList(obj.source, SEAT_SOURCES)) return null;
  if (!Array.isArray(obj.delegations) || obj.delegations.length > 16) return null;
  const delegations: Delegation[] = [];
  for (const item of obj.delegations) {
    const row = asExact(item, ["delegation_id", "type", "expires_at"]);
    if (!row || !isRef(row.delegation_id) || !inList(row.type, DELEGATION_TYPES)) return null;
    if (typeof row.expires_at !== "string" || !RFC3339.test(row.expires_at)) return null;
    delegations.push({ delegation_id: row.delegation_id, type: row.type, expires_at: row.expires_at });
  }
  return { label: obj.label, source: obj.source, delegations };
}

function parseBilling(value: unknown): Billing | null {
  const obj = asExact(value, [
    "kind",
    "known",
    "reason",
    "payer_source",
    "viewer_account_role",
    "availability",
    "amount",
    "entry",
  ]);
  if (!obj || !inList(obj.kind, BILLING_KINDS) || typeof obj.known !== "boolean") return null;
  if (obj.reason !== null && !inList(obj.reason, BILLING_REASONS)) return null;
  if (obj.payer_source !== null && !inList(obj.payer_source, PAYER_SOURCES)) return null;
  if (obj.viewer_account_role !== null && !inList(obj.viewer_account_role, ACCOUNT_ROLES)) return null;
  if (!inList(obj.availability, AVAILABILITIES)) return null;
  if (obj.entry !== null && !inList(obj.entry, BILLING_ENTRIES)) return null;
  let amount: BillingAmount | null = null;
  if (obj.amount !== null) {
    const row = asExact(obj.amount, ["value_minor", "unit"]);
    if (!row || !isMinor(row.value_minor) || !inList(row.unit, AMOUNT_UNITS)) return null;
    amount = { value_minor: row.value_minor, unit: row.unit };
  }
  if (!obj.known) {
    if (typeof obj.reason !== "string" || obj.availability !== "unknown" || amount !== null) return null;
  } else if (obj.reason !== null || obj.payer_source === null || (obj.availability !== "available" && obj.availability !== "exhausted")) {
    return null;
  }
  return {
    kind: obj.kind,
    known: obj.known,
    reason: obj.reason,
    payer_source: obj.payer_source,
    viewer_account_role: obj.viewer_account_role,
    availability: obj.availability,
    amount,
    entry: obj.entry,
  };
}

function parseReturn(value: unknown): ReturnContext | null {
  const obj = asExact(value, ["source_app_id", "return_target_id", "label", "source_ref"]);
  if (!obj || !isSlug(obj.source_app_id) || !isSlug(obj.return_target_id) || !isLabel(obj.label)) return null;
  if (obj.source_ref !== null) {
    if (typeof obj.source_ref !== "string" || obj.source_ref.length < 1 || obj.source_ref.length > 256 || !REF.test(obj.source_ref)) {
      return null;
    }
  }
  return {
    source_app_id: obj.source_app_id,
    return_target_id: obj.return_target_id,
    label: obj.label,
    source_ref: obj.source_ref === null ? null : obj.source_ref,
  };
}

function parseCapabilities(value: unknown): Capabilities | null {
  const obj = asExact(value, ["can_switch_app", "can_switch_tenant", "can_manage_members", "can_open_billing"]);
  if (!obj) return null;
  if (
    typeof obj.can_switch_app !== "boolean" ||
    typeof obj.can_switch_tenant !== "boolean" ||
    typeof obj.can_manage_members !== "boolean" ||
    typeof obj.can_open_billing !== "boolean"
  ) {
    return null;
  }
  return {
    can_switch_app: obj.can_switch_app,
    can_switch_tenant: obj.can_switch_tenant,
    can_manage_members: obj.can_manage_members,
    can_open_billing: obj.can_open_billing,
  };
}

function parseStatus(value: unknown): { state: NavState; reasons: StatusReason[] } | null {
  const obj = asExact(value, ["state", "reasons"]);
  if (!obj || !inList(obj.state, NAV_STATES) || !Array.isArray(obj.reasons) || obj.reasons.length > 8) return null;
  const reasons: StatusReason[] = [];
  const seen = new Set<string>();
  for (const reason of obj.reasons) {
    if (!inList(reason, STATUS_REASONS) || seen.has(reason)) return null;
    seen.add(reason);
    reasons.push(reason);
  }
  if (obj.state === "ready") {
    if (reasons.length !== 0) return null;
  } else if (reasons.length < 1) {
    return null;
  }
  if (obj.state === "selection_required" && (reasons.length !== 1 || reasons[0] !== "tenant_selection_required")) {
    return null;
  }
  return { state: obj.state, reasons };
}

const fail = { ok: false, code: "schema_incompatible" } as const;

export function parseEcoNavDocument(input: unknown): ParseEcoNavResult {
  if (!isRecord(input) || !keysExact(input, TOP_KEYS) || input.schema_version !== ECO_NAV_SCHEMA_VERSION) return fail;
  const status = parseStatus(input.status);
  const capabilities = parseCapabilities(input.capabilities);
  if (!status || !capabilities || !Array.isArray(input.visible_apps) || input.visible_apps.length > 32) return fail;

  const visible: VisibleApp[] = [];
  for (const item of input.visible_apps) {
    const app = parseVisibleApp(item);
    if (!app) return fail;
    visible.push(app);
  }

  const state = status.state;
  if (state === "hidden" || state === "denied" || state === "unavailable") {
    if (
      input.brand !== null ||
      input.app !== null ||
      input.work_context !== null ||
      input.role !== null ||
      input.billing !== null ||
      input.return_context !== null ||
      visible.length !== 0
    ) {
      return fail;
    }
    if (
      capabilities.can_switch_app ||
      capabilities.can_switch_tenant ||
      capabilities.can_manage_members ||
      capabilities.can_open_billing
    ) {
      return fail;
    }
    const document: BlockedModel = {
      schema_version: ECO_NAV_SCHEMA_VERSION,
      status: { state, reasons: status.reasons },
      brand: null,
      app: null,
      visible_apps: [],
      work_context: null,
      role: null,
      billing: null,
      return_context: null,
      capabilities,
    };
    return { ok: true, document };
  }

  const brand = parseBrand(input.brand);
  const app = parseCurrentApp(input.app);
  const work = parseWorkContext(input.work_context);
  if (!brand || !app || !work || visible.length < 1) return fail;

  if (state === "selection_required") {
    if (input.role !== null || input.billing !== null || input.return_context !== null) return fail;
    const document: SelectionModel = {
      schema_version: ECO_NAV_SCHEMA_VERSION,
      status: { state: "selection_required", reasons: ["tenant_selection_required"] },
      brand,
      app,
      visible_apps: visible,
      work_context: work,
      role: null,
      billing: null,
      return_context: null,
      capabilities,
    };
    return { ok: true, document };
  }

  if (state !== "ready" && state !== "degraded") return fail;
  const role = parseRole(input.role);
  const billing = parseBilling(input.billing);
  if (!role || !billing) return fail;
  let returnContext: ReturnContext | null = null;
  if (input.return_context !== null) {
    returnContext = parseReturn(input.return_context);
    if (!returnContext) return fail;
  }
  const document: ResolvedModel = {
    schema_version: ECO_NAV_SCHEMA_VERSION,
    status: { state, reasons: status.reasons },
    brand,
    app,
    visible_apps: visible,
    work_context: work,
    role,
    billing,
    return_context: returnContext,
    capabilities,
  };
  return { ok: true, document };
}

function scopeList(work: WorkContext): Scope[] {
  const scopes = [...work.switchable_scopes];
  if (work.current_scope && !scopes.some((scope) => scope.tenant_id === work.current_scope?.tenant_id)) {
    scopes.unshift(work.current_scope);
  }
  return scopes;
}

function emptyCurrentApp(): CurrentApp {
  return { app_id: TOUCH_APP_ID, display_name: "碰一碰", icon_ref: null, home_target_id: "ti-touch-web" };
}

export function toViewModel(document: EcoNavDocument, options: ViewModelOptions): EcoNavModel {
  if (!isRenderable(document)) {
    return {
      schema_version: ECO_NAV_SCHEMA_VERSION,
      provenance: options.provenance,
      renderable: false,
      brand: null,
      current_app: emptyCurrentApp(),
      document_app_id: null,
      active_tenant_id: null,
      source_tenant_id: null,
      scopes: [],
      apps: [],
      role_label: "",
      source_role_label: "",
      seat_source: null,
      source_seat_source: null,
      delegations: [],
      source_delegations: [],
      payer_source: null,
      source_payer_source: null,
      billing: null,
      billing_follows_document: false,
      source_billing_follows: false,
      return_context: null,
      capabilities: document.capabilities,
      status: { state: document.status.state, reasons: document.status.reasons, summary: options.statusSummary },
    };
  }

  const current = document.work_context.current_scope;
  const billing = document.billing;
  const follows = current !== null && billing !== null;
  const payer = follows ? billing.payer_source : null;
  const roleLabel = document.role?.label ?? "";
  const seatSource = document.work_context.current_scope?.source ?? null;
  const delegations = document.role?.delegations ?? [];
  return {
    schema_version: ECO_NAV_SCHEMA_VERSION,
    provenance: options.provenance,
    renderable: true,
    brand: document.brand,
    current_app: {
      app_id: TOUCH_APP_ID,
      display_name: document.app.display_name.trim() || "碰一碰",
      icon_ref: document.app.icon_ref,
      home_target_id: document.app.home_target_id,
    },
    document_app_id: document.app.app_id,
    active_tenant_id: current?.tenant_id ?? null,
    source_tenant_id: current?.tenant_id ?? null,
    scopes: scopeList(document.work_context),
    apps: document.visible_apps,
    role_label: roleLabel,
    source_role_label: roleLabel,
    seat_source: seatSource,
    source_seat_source: seatSource,
    delegations,
    source_delegations: delegations,
    payer_source: payer,
    source_payer_source: payer,
    billing,
    billing_follows_document: follows,
    source_billing_follows: follows,
    return_context: document.return_context,
    capabilities: document.capabilities,
    status: { state: document.status.state, reasons: document.status.reasons, summary: options.statusSummary },
  };
}

/**
 * HUI-2628: the only fallback when the nav authority is missing/unreachable is
 * a contract-valid blocked model. It renders nothing and never invents a brand,
 * tenant, payer or app list. Preview fixtures are reserved for /admin/preview.
 */
export function blockedUnavailableEcoNav(): EcoNavModel {
  const parsed = parseEcoNavDocument({
    schema_version: ECO_NAV_SCHEMA_VERSION,
    status: { state: "unavailable", reasons: ["authority_unavailable"] },
    brand: null,
    app: null,
    visible_apps: [],
    work_context: null,
    role: null,
    billing: null,
    return_context: null,
    capabilities: {
      can_switch_app: false,
      can_switch_tenant: false,
      can_manage_members: false,
      can_open_billing: false,
    },
  });
  if (!parsed.ok) throw new Error("blocked eco-nav document is not eco-nav/v1");
  return toViewModel(parsed.document, {
    provenance: "public_ai_context",
    statusSummary: "生态导航暂不可用；页面其余部分可用。",
  });
}

export function pinTouchApp(model: EcoNavModel): EcoNavModel {
  const displayName = model.current_app.display_name.trim() || "碰一碰";
  return {
    ...model,
    current_app: { ...model.current_app, app_id: TOUCH_APP_ID, display_name: displayName },
  };
}

export function selectTenant(model: EcoNavModel, tenantId: string): EcoNavModel {
  if (!model.renderable || !model.capabilities.can_switch_tenant) return model;
  const next = model.scopes.find((scope) => scope.tenant_id === tenantId);
  if (!next || next.tenant_id === model.active_tenant_id) return model;
  const back = next.tenant_id === model.source_tenant_id;
  return {
    ...model,
    active_tenant_id: next.tenant_id,
    payer_source: back ? model.source_payer_source : null,
    role_label: back ? model.source_role_label : "",
    seat_source: back ? model.source_seat_source : null,
    delegations: back ? model.source_delegations : [],
    billing_follows_document: back ? model.source_billing_follows : false,
    return_context: null,
  };
}

export function safeAllowListedUrl(raw: string | null | undefined): string | null {
  if (!raw) return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return null;
  for (const key of BLOCKED_QUERY_KEYS) {
    if (url.searchParams.has(key)) return null;
  }
  return url.toString();
}

/**
 * HUI-2628: the renderer may only enable an app when the target is registered
 * and the model says launchable. The actual switch always goes through the BFF
 * (SSO code minting); no URL is constructed in the browser.
 */
export function canLaunchManually(
  app: VisibleApp,
  canSwitch: boolean,
  available: LaunchAvailability,
): boolean {
  if (!canSwitch || app.state !== "launchable" || app.launch_mode !== "sso_launch" || !app.launch_target_id) return false;
  return available(app.launch_target_id) === true;
}

export function planManualSwitch(
  app: VisibleApp,
  canSwitch: boolean,
  resolve: LaunchResolver = () => null,
): ManualSwitchPlan {
  const targetId = app.launch_target_id;
  const resolved =
    canSwitch && app.state === "launchable" && app.launch_mode === "sso_launch" && targetId
      ? safeAllowListedUrl(resolve(targetId))
      : null;
  return {
    nav_intent: NAV_INTENT_MANUAL_SWITCH,
    app_id: app.app_id,
    launch_target_id: targetId,
    href: resolved,
    creates_handoff: false,
    residual: resolved ? null : LAUNCH_UNRESOLVED_RESIDUAL,
  };
}

function formatBillingMinor(minor: number): string {
  const yuan = Math.trunc(minor / 100);
  const frac = String(Math.abs(minor) % 100).padStart(2, "0");
  return `${yuan}.${frac}`;
}

export function billingBadgeText(model: EcoNavModel): string {
  if (model.provenance !== "public_ai_context") return BILLING_UNCONFIRMED;
  const billing = model.billing;
  if (!model.billing_follows_document || !billing?.known || !billing.amount) return BILLING_UNCONFIRMED;
  const { value_minor, unit } = billing.amount;
  if (unit === "quota_credit") return `客户额度 ${value_minor}`;
  const yuan = formatBillingMinor(value_minor);
  return billing.kind === "wallet" ? `钱包 CNY ${yuan}` : `客户额度 CNY ${yuan}`;
}

export function payerText(model: EcoNavModel): string {
  if (!model.billing_follows_document || model.payer_source === null) return "付款主体需确认";
  return model.payer_source === "personal" ? "个人付款" : "委托付款";
}

export function switchableApps(apps: VisibleApp[], currentAppId: string): VisibleApp[] {
  return apps.filter((app) => app.state !== "current" && app.app_id !== currentAppId);
}

export function partitionApps(
  apps: VisibleApp[],
  viewport: EcoNavViewport,
): { pinned: VisibleApp[]; overflow: VisibleApp[] } {
  if (viewport === "narrow") return { pinned: [], overflow: apps };
  return { pinned: apps.slice(0, 4), overflow: apps.slice(4) };
}

export function resolveTargetHref(enabled: boolean, targetId: string | null, resolve: LaunchResolver): string | null {
  if (!enabled || !targetId) return null;
  return safeAllowListedUrl(resolve(targetId));
}
