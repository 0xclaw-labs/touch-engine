import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { etaText, legacyQueue, presentQueue, publicQueueView, readJobQueueView } from "@/lib/mx-ops/queue";

const fixtures = path.join(process.cwd(), "tests", "fixtures", "mx-ops");

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(path.join(fixtures, name), "utf8"));
}

function mutated(base: Record<string, unknown>, patch: Record<string, unknown>): Record<string, unknown> {
  return { ...base, ...patch };
}

describe("job_queue_view 严格读取", () => {
  const waiting = fixture("job-queue-view.waiting.json") as Record<string, unknown>;

  it("合法正例保留 null 与 0 的不同语义", () => {
    const view = readJobQueueView(waiting);
    expect(view).not.toBeNull();
    expect(view?.jobs_ahead).toBe(3);
    expect(view?.waiting_jobs_ahead).toBe(2);
    expect(view?.running_jobs).toBe(1);
    expect(view?.eta_seconds_min).toBeNull();
    expect(view?.eta_seconds_max).toBeNull();
  });

  it("错合同版本、错 object_kind、缺字段一律 null，不猜", () => {
    expect(readJobQueueView(mutated(waiting, { contract_version: "mx-ops-c0/v2" }))).toBeNull();
    expect(readJobQueueView(mutated(waiting, { object_kind: "legacy_queue" }))).toBeNull();
    const missing = { ...waiting };
    delete missing.waiting_jobs_ahead;
    expect(readJobQueueView(missing)).toBeNull();
    expect(readJobQueueView(null)).toBeNull();
    expect(readJobQueueView("<xml/>")).toBeNull();
  });

  it("ETA 半未知/倒置/负数按合同判非法", () => {
    expect(readJobQueueView(mutated(waiting, { eta_seconds_min: null, eta_seconds_max: 0 }))).toBeNull();
    expect(readJobQueueView(mutated(waiting, { eta_seconds_min: 10, eta_seconds_max: 5 }))).toBeNull();
    expect(readJobQueueView(mutated(waiting, { eta_seconds_min: -1, eta_seconds_max: 0 }))).toBeNull();
    expect(readJobQueueView(mutated(waiting, { jobs_ahead: "3" }))).toBeNull();
  });
});

describe("公开投影删除 internal_debug", () => {
  it("internal_debug 非空时公开视图整键删除", () => {
    const base = fixture("job-queue-view.waiting.json") as Record<string, unknown>;
    const withDebug = mutated(base, {
      internal_debug: { ready_workers: 2, busy_workers: 1, starting_workers: 0 },
    });
    const pub = publicQueueView(withDebug);
    expect(pub).not.toBeNull();
    expect("internal_debug" in (pub as object)).toBe(false);
    expect(JSON.stringify(pub)).not.toContain("ready_workers");
  });
});

describe("商家等待视图", () => {
  it("无 ETA 样本显示暂不可估，等待原因与任务计数分开", () => {
    const view = presentQueue(readJobQueueView(fixture("job-queue-view.waiting.json")));
    expect(view).not.toBeNull();
    expect(view?.phaseLabel).toBe("排队等待");
    expect(view?.phase).toBe("waiting");
    expect(view?.waitKind).toBe("capacity");
    expect(view?.waitLabel).toBe("等待云端算力");
    expect(view?.stageLabel).toBe("生成");
    expect(view?.queueKindLabel).toBe("付费弹性");
    expect(view?.etaText).toBe("暂不可估");
    expect(view?.waitingJobsAheadText).toBe("2 个");
    expect(view?.runningJobsText).toBe("1 个");
    expect(view?.jobsAheadText).toContain("含运行中");
    expect(view?.nextEligibleText).toBeNull();
  });

  it("实测 0 是有效值：ETA 与等待计数都显示 0，不折叠为未知", () => {
    const view = presentQueue(readJobQueueView(fixture("job-queue-view.measured.json")));
    expect(view?.etaText).toBe("预计 0 分钟");
    expect(view?.waitingJobsAhead).toBe(0);
    expect(view?.waitingJobsAheadText).toBe("0 个");
    expect(view?.runningJobs).toBe(0);
    expect(view?.runningJobsText).toBe("0 个");
  });

  it("null 视图不产生伪进度", () => {
    expect(presentQueue(null)).toBeNull();
    expect(presentQueue(readJobQueueView({}))).toBeNull();
  });

  it("下一可执行窗口按上海时区展示，未知保持 null", () => {
    const base = fixture("job-queue-view.waiting.json") as Record<string, unknown>;
    const view = presentQueue(readJobQueueView(mutated(base, { next_eligible_at_unix: 1780003600 })));
    expect(view?.nextEligibleText).toBe("2026-05-29 05:26");
    expect(presentQueue(readJobQueueView(base))?.nextEligibleText).toBeNull();
  });
});

describe("ETA 文案边界", () => {
  it("同值与区间分别给约数，未知不给数", () => {
    expect(etaText(null, null)).toBe("暂不可估");
    expect(etaText(300, 300)).toBe("约 5 分钟");
    expect(etaText(300, 480)).toBe("约 5 分钟–8 分钟");
    expect(etaText(3600, 3600)).toBe("约 1 小时");
    expect(etaText(3600, 3900)).toBe("约 1 小时–1 小时 5 分钟");
  });
});

describe("旧 queue 冻结语义", () => {
  it("legacy jobs_ahead 保持含运行中，不改写", () => {
    const legacy = legacyQueue({
      position: 2,
      jobs_ahead: 3,
      estimated_wait_seconds: 600,
      capacity_state: "hot",
      ready_workers: 1,
      busy_workers: 1,
      starting_workers: 0,
    });
    expect(legacy?.jobs_ahead).toBe(3);
    expect(legacyQueue({ position: 2, jobs_ahead: 3 })).toBeNull();
  });
});
