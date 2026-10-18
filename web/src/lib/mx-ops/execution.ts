// MX-OPS C0 execution-policy consumption (options and validation only).
//
// The client never decides policy. It shows the intersection of:
//   allowed policy templates ∩ caller entitlements ∩ time window ∩ quote state.
// A free-idle option is never an internal-research permission; a numeric
// priority is never constructed (forged priority is a server-side reject).

import type { ExecutionPolicy, PolicyTemplate } from "./contract";

export type PayerKind = ExecutionPolicy["cost_bearing"]["payer"];
export type PreferenceId = "immediate" | "night_batch" | "free_idle" | "paid_elastic";

export interface ExecutionOption {
  id: PreferenceId;
  template: PolicyTemplate;
  label: string;
  detail: string;
  available: boolean;
  reason: string | null;
  costBearing: string;
  policyId: string;
  policyVersion: number;
}

export interface ExecutionContext {
  policies: ExecutionPolicy[];
  entitlements: string[];
  quoteConfirmed: boolean;
  nowUnix: number;
}

export type PreferenceResult =
  | { ok: true; template: PolicyTemplate; option: ExecutionOption }
  | { ok: false; code: "mx_ops_c0_unsupported" | "mx_ops_c0_permission_denied"; message: string };

export const FEE_DISCLOSURE =
  "这里只展示和发起；唯一收费由星模发起并由公共 Billing 记账，不建立第二个钱包。";

const PREFERENCE_BY_TEMPLATE: Record<PolicyTemplate, PreferenceId> = {
  fixed_card: "immediate",
  paid_elastic: "paid_elastic",
  night_batch: "night_batch",
  free_idle: "free_idle",
};

const TEMPLATE_LABEL: Record<PolicyTemplate, string> = {
  fixed_card: "立即（固定卡排队）",
  paid_elastic: "付费优先（弹性扩容）",
  night_batch: "夜间批量",
  free_idle: "免费闲时",
};

const DAY_LABEL = ["", "周一", "周二", "周三", "周四", "周五", "周六", "周日"];

function minuteLabel(minuteOfDay: number): string {
  const hours = Math.floor(minuteOfDay / 60);
  const minutes = minuteOfDay % 60;
  return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}`;
}

function windowDetail(policy: ExecutionPolicy): string {
  const window = policy.time_window;
  if (!window) return "没有可执行时间窗";
  const rules = window.windows
    .map((rule) => {
      const days =
        rule.days_of_week.length === 5 && rule.days_of_week.every((day, index) => day === index + 1)
          ? "周一至周五"
          : rule.days_of_week.map((day) => DAY_LABEL[day] ?? "").join("、");
      return `${days} ${minuteLabel(rule.start_minute_of_day)}–${minuteLabel(rule.end_minute_of_day)}`;
    })
    .join("；");
  return `时区 ${window.timezone}；窗口 ${rules}`;
}

export function costBearingLabel(payer: PayerKind): string {
  switch (payer) {
    case "user":
      return "由你承担（按确认报价）";
    case "platform_subsidy":
      return "平台补贴承担（只借明确可出借的空闲槽位，不等于内部研发卡）";
    case "campaign":
      return "活动预算承担";
    case "internal":
      return "内部研发承担，不对商家开放";
  }
}

function unavailability(policy: ExecutionPolicy, ctx: ExecutionContext): string | null {
  if (!policy.enabled) return "策略未启用";
  if (policy.cost_bearing.payer === "internal") return "内部研发算力不对商家开放";
  switch (policy.template) {
    case "fixed_card":
      return null;
    case "paid_elastic":
      return ctx.quoteConfirmed ? null : "报价未确认，不能选择付费优先";
    case "night_batch":
      return policy.time_window ? null : "策略缺少可执行时间窗，不能安排夜间批量";
    case "free_idle": {
      if (!policy.compute_permission.allow_borrow_idle) {
        return "没有可借用空闲算力的资格";
      }
      if (policy.cost_bearing.payer !== "platform_subsidy" && policy.cost_bearing.payer !== "campaign") {
        return "免费资格没有绑定平台补贴";
      }
      const entitlement = policy.cost_bearing.entitlement_ref;
      if (!entitlement || !ctx.entitlements.includes(entitlement)) {
        return "没有免费闲时资格";
      }
      return null;
    }
  }
}

function detailOf(policy: ExecutionPolicy): string {
  switch (policy.template) {
    case "fixed_card":
      return "不新建、不恢复 GPU；按固定卡队列等待，不占内部研发卡";
    case "paid_elastic":
      return "可扩弹性算力；不默认抢占运行中的任务";
    case "night_batch":
      return windowDetail(policy);
    case "free_idle":
      return "只借明确可出借的市场空闲槽位；不续命、不开卡";
  }
}

export function executionOptions(ctx: ExecutionContext): ExecutionOption[] {
  const options: ExecutionOption[] = [];
  const seen = new Set<PolicyTemplate>();
  for (const policy of ctx.policies) {
    if (seen.has(policy.template)) continue;
    seen.add(policy.template);
    const reason = unavailability(policy, ctx);
    options.push({
      id: PREFERENCE_BY_TEMPLATE[policy.template],
      template: policy.template,
      label: TEMPLATE_LABEL[policy.template],
      detail: detailOf(policy),
      available: reason === null,
      reason,
      costBearing: costBearingLabel(policy.cost_bearing.payer),
      policyId: policy.policy_id,
      policyVersion: policy.policy_version,
    });
  }
  return options;
}

export function validatePreference(input: {
  preference: string;
  options: ExecutionOption[];
}): PreferenceResult {
  const option = input.options.find((item) => item.id === input.preference);
  if (!option) {
    return {
      ok: false,
      code: "mx_ops_c0_unsupported",
      message: "这个执行偏好不在服务器允许的范围内。",
    };
  }
  if (!option.available) {
    return {
      ok: false,
      code: option.template === "free_idle" ? "mx_ops_c0_permission_denied" : "mx_ops_c0_unsupported",
      message: option.reason ?? "这个执行偏好当前不可用。",
    };
  }
  return { ok: true, template: option.template, option };
}

// A client may only send a named preference. Any numeric priority (at any
// depth) is a forged grant and must be refused before it reaches the wire.
export function assertNoPriority(value: unknown): void {
  const stack: unknown[] = [value];
  while (stack.length > 0) {
    const current = stack.pop();
    if (Array.isArray(current)) {
      stack.push(...current);
      continue;
    }
    if (typeof current !== "object" || current === null) continue;
    for (const [key, child] of Object.entries(current)) {
      if (/priority/i.test(key)) {
        throw new Error("mx_ops_c0_priority_forged: 客户端不能声明数字优先级");
      }
      stack.push(child);
    }
  }
}
