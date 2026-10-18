import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import type { ExecutionPolicy } from "@/lib/mx-ops/contract";
import {
  assertNoPriority,
  costBearingLabel,
  executionOptions,
  FEE_DISCLOSURE,
  validatePreference,
} from "@/lib/mx-ops/execution";

const fixtures = path.join(process.cwd(), "tests", "fixtures", "mx-ops");

function policy(name: string): ExecutionPolicy {
  return JSON.parse(readFileSync(path.join(fixtures, name), "utf8")) as ExecutionPolicy;
}

describe("执行偏好只从允许模板与权益取交集", () => {
  it("夜间批量：有窗口才可选，并展示窗口与批处理等级", () => {
    const night = policy("execution-policy.night_batch.json");
    const options = executionOptions({
      policies: [night],
      entitlements: [],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    });
    const option = options.find((item) => item.template === "night_batch");
    expect(option).toBeDefined();
    expect(option?.available).toBe(true);
    expect(option?.label).toContain("夜间");
    expect(option?.detail).toContain("Asia/Shanghai");
    expect(option?.costBearing).toContain("你承担");
  });

  it("夜间策略缺时间窗时不可选，给商家语言原因", () => {
    const night = { ...policy("execution-policy.night_batch.json"), time_window: null };
    const option = executionOptions({
      policies: [night],
      entitlements: [],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    }).find((item) => item.template === "night_batch");
    expect(option?.available).toBe(false);
    expect(option?.reason).toContain("时间窗");
  });

  it("免费闲时：无权益禁用；有权益+平台补贴才可选，且明确不等于内部研发卡", () => {
    const free = policy("execution-policy.free_idle.json");
    const without = executionOptions({
      policies: [free],
      entitlements: [],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    }).find((item) => item.template === "free_idle");
    expect(without?.available).toBe(false);
    expect(without?.reason).toContain("资格");

    const withEntitlement = executionOptions({
      policies: [free],
      entitlements: ["ent_subsidy_v1"],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    }).find((item) => item.template === "free_idle");
    expect(withEntitlement?.available).toBe(true);
    expect(withEntitlement?.costBearing).toContain("平台补贴");
    expect(withEntitlement?.costBearing).toContain("不等于");
  });

  it("免费策略声明的权益引用不匹配时不可选", () => {
    const free = policy("execution-policy.free_idle.json");
    const option = executionOptions({
      policies: [free],
      entitlements: ["ent_other"],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    }).find((item) => item.template === "free_idle");
    expect(option?.available).toBe(false);
  });

  it("付费弹性：报价未确认不可选；确认后显示用户承担", () => {
    const paid = policy("execution-policy.paid_elastic.json");
    const before = executionOptions({
      policies: [paid],
      entitlements: [],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    }).find((item) => item.template === "paid_elastic");
    expect(before?.available).toBe(false);
    expect(before?.reason).toContain("报价");

    const after = executionOptions({
      policies: [paid],
      entitlements: [],
      quoteConfirmed: true,
      nowUnix: 1780000000,
    }).find((item) => item.template === "paid_elastic");
    expect(after?.available).toBe(true);
    expect(after?.costBearing).toContain("你承担");
  });

  it("internal 费用承担不对商家开放，免费不能借成 internal", () => {
    const free = policy("execution-policy.free_idle.json");
    const internal = {
      ...free,
      cost_bearing: { payer: "internal" as const, entitlement_ref: "ent_internal" },
    };
    const option = executionOptions({
      policies: [internal],
      entitlements: ["ent_internal"],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    }).find((item) => item.template === "free_idle");
    expect(option?.available).toBe(false);
    expect(option?.reason).toContain("内部");
  });
});

describe("偏好校验不发数字优先级", () => {
  const paid = policy("execution-policy.paid_elastic.json");
  const options = executionOptions({
    policies: [paid],
    entitlements: [],
    quoteConfirmed: true,
    nowUnix: 1780000000,
  });

  it("合法偏好返回模板；列表外模板拒绝", () => {
    expect(validatePreference({ preference: "paid_elastic", options })).toMatchObject({
      ok: true,
      template: "paid_elastic",
    });
    const outside = validatePreference({ preference: "night_batch", options });
    expect(outside.ok).toBe(false);
    if (!outside.ok) expect(outside.code).toBe("mx_ops_c0_unsupported");
  });

  it("不可用偏好拒绝并带原因，不静默降级", () => {
    const noQuote = executionOptions({
      policies: [paid],
      entitlements: [],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    });
    const result = validatePreference({ preference: "paid_elastic", options: noQuote });
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.code).toBe("mx_ops_c0_unsupported");
      expect(result.message).toContain("报价");
    }
  });

  it("出现 priority/requested_priority 数字直接拒绝（防伪造）", () => {
    expect(() => assertNoPriority({ execution_preference: "night_batch" })).not.toThrow();
    expect(() => assertNoPriority({ priority: 9 })).toThrow();
    expect(() => assertNoPriority({ requested_priority: "high" })).toThrow();
    expect(() => assertNoPriority({ nested: { priority: 1 } })).toThrow();
  });
});

describe("费用披露口径", () => {
  it("只展示/发起，唯一收费归星模与公共 Billing，不提第二钱包", () => {
    expect(FEE_DISCLOSURE).toContain("唯一收费");
    expect(FEE_DISCLOSURE).toContain("公共 Billing");
    expect(FEE_DISCLOSURE).not.toContain("钱包余额");
    expect(costBearingLabel("user")).toContain("你承担");
    expect(costBearingLabel("campaign")).toContain("活动预算");
    expect(costBearingLabel("platform_subsidy")).toContain("平台补贴");
    expect(costBearingLabel("internal")).toContain("内部");
  });
});
