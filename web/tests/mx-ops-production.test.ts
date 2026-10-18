import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { presentProduction, visitorContentGuard, type ProductionJob } from "@/lib/work/production";

const fixtures = path.join(process.cwd(), "tests", "fixtures", "mx-ops");

function queueFixture(name: string): unknown {
  return JSON.parse(readFileSync(path.join(fixtures, name), "utf8"));
}

function job(patch: Partial<ProductionJob> = {}): ProductionJob {
  return {
    id: "cho_1",
    handoff_id: "cho_1",
    campaign_id: "camp_1",
    purpose: "给新品做一条夜间视频",
    desired_form: "video",
    execution_preference: "night_batch",
    policy_ref: "pol_night-batch",
    policy_version: 7,
    job_ref: "mx_job_1",
    job_queue_view: queueFixture("job-queue-view.waiting.json"),
    candidate_version_id: null,
    ...patch,
  };
}

const ctx = {
  campaignPublishAtUnix: 1790000000, // 2026-09-21 前后，晚于下一窗口
  entitlementRefs: ["ent_subsidy_v1"],
  quoteConfirmed: true,
  merchantToolFeeMinor: 1200,
  campaignBudgetMinor: 5000,
  modelCostMinor: 980,
  currency: "CNY" as const,
};

describe("商家云端制作视图", () => {
  it("夜间任务展示偏好、窗口、队列等待与可空 ETA", () => {
    const view = presentProduction(job(), ctx);
    expect(view.preferenceLabel).toContain("夜间");
    expect(view.queue?.waitLabel).toBe("等待云端算力");
    expect(view.queue?.etaText).toBe("暂不可估");
    expect(view.waitingKind).toBe("cloud");
    expect(view.waitingText).toContain("暂不可估");
    expect(view.nextEligibleText).toBeNull();
    expect(view.windowAfterPublish).toBe(false);
    expect(view.publishWarning).toBeNull();
  });

  it("下一窗口晚于活动发布日：提示赶不上，但不自动升级付费/改活动", () => {
    const base = queueFixture("job-queue-view.waiting.json") as Record<string, unknown>;
    const withWindow = { ...base, next_eligible_at_unix: 1780003600 };
    const view = presentProduction(job({ job_queue_view: withWindow }), {
      ...ctx,
      campaignPublishAtUnix: 1779990000, // 窗口之前
    });
    expect(view.nextEligibleText).toBe("2026-05-29 05:26");
    expect(view.windowAfterPublish).toBe(true);
    expect(view.publishWarning).toContain("赶不上");
    expect(view.autoUpgrade).toBe(false);
    expect(view.autoAdopt).toBe(false);
  });

  it("没有权威队列投影时如实 UNKNOWN，不给伪进度", () => {
    const view = presentProduction(job({ job_queue_view: undefined }), ctx);
    expect(view.queue).toBeNull();
    expect(view.waitingKind).toBe("unknown");
    expect(view.waitingText).toContain("未知");
    expect(view.waitingText).not.toContain("马上");
    expect(view.waitingText).not.toContain("预计");
  });

  it("候选回流进入等待商家决定，不自动采用/外发", () => {
    const view = presentProduction(job({ candidate_version_id: "cpv_9" }), ctx);
    expect(view.candidate.state).toBe("candidate");
    expect(view.candidate.ref).toBe("cpv_9");
    expect(view.candidate.autoAdopted).toBe(false);
    expect(view.candidate.adoptHint).toContain("显式采用");
    expect(view.waitingKind).toBe("human");
    expect(view.notice).toContain("不会自动外发");
  });

  it("三层费用分开记录，不退化成一条总额", () => {
    const view = presentProduction(job(), ctx);
    expect(view.feeLines.merchantToolFee).toBe("¥12.00");
    expect(view.feeLines.campaignBudget).toBe("¥50.00");
    expect(view.feeLines.modelCost).toBe("¥9.80");
    expect(view.feeNote).toContain("唯一模型收费");
    expect(view.feeNote).toContain("公共 Billing");
    expect(view.feeNote).not.toContain("钱包余额");
  });

  it("费用缺失显示未提供，不当成 0", () => {
    const view = presentProduction(job(), { ...ctx, merchantToolFeeMinor: null, campaignBudgetMinor: null, modelCostMinor: null });
    expect(view.feeLines.merchantToolFee).toBe("未提供");
    expect(view.feeLines.campaignBudget).toBe("未提供");
    expect(view.feeLines.modelCost).toBe("未提供");
  });

  it("恢复入口永远回到同一 job，不新建制作工程", () => {
    const same = presentProduction(job(), ctx);
    expect(same.recovery.kind).toBe("same_job");
    expect(same.recovery.jobRef).toBe("mx_job_1");
    expect(same.recovery.text).toContain("不重新提交");
    const none = presentProduction(job({ job_ref: "" }), ctx);
    expect(none.recovery.kind).toBe("new");
  });

  it("偏好缺失时不猜，标成未指定", () => {
    const view = presentProduction(job({ execution_preference: "" }), ctx);
    expect(view.preferenceLabel).toContain("未指定");
  });
});

describe("顾客路径零新生成", () => {
  it("访客守卫恒不生成，只读已采用预制内容", () => {
    const guard = visitorContentGuard();
    expect(guard.generationAllowed).toBe(false);
    expect(guard.reason).toContain("不会新开 GPU");
    expect(guard.reason).toContain("已采用");
  });
});
