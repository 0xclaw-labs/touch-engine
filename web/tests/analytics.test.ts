import { describe, expect, it } from "vitest";
import {
  ANALYTICS_NOT_ENABLED_COPY,
  EVIDENCE_LABELS,
  FILTER_LABELS,
  buildAnalyticsQuery,
  emptyWindowCopy,
  evidenceRowList,
  formatMinor,
  localDayWindow,
  stageNotAppliedLabels,
  type EvidencePanel,
  type FunnelStage,
} from "../src/lib/analytics";

// HUI-1677 经营看板纯函数:源码级断言(node 环境无 DOM)。
// 红线:未知/未接入不冒充 0;筛选不适用级照实披露;nfc 只是入口标记;
// 匿名参与不冒充自然人数;面额不是支出。

describe("localDayWindow", () => {
  it("今天窗口是本地日 [00:00, now],RFC3339 带时区", () => {
    const now = new Date(2026, 9, 14, 15, 30, 45);
    const win = localDayWindow(now);
    expect(win.window_start).toMatch(/^2026-10-14T00:00:00[+-]\d{2}:\d{2}$/);
    expect(win.window_end).toMatch(/^2026-10-14T15:30:45[+-]\d{2}:\d{2}$/);
  });
});

describe("buildAnalyticsQuery", () => {
  it("空筛选只带窗口", () => {
    const q = buildAnalyticsQuery({ window_start: "s", window_end: "e" }, {});
    expect(q).toBe("window_start=s&window_end=e");
  });

  it("带筛选时逐项进查询串,空值不带", () => {
    const q = buildAnalyticsQuery({ window_start: "s", window_end: "e" }, {
      campaignId: "c1",
      touchpoint: "nfc",
      contentPackVersion: "",
    });
    expect(q).toContain("window_start=s");
    expect(q).toContain("campaign_id=c1");
    expect(q).toContain("touchpoint=nfc");
    expect(q).not.toContain("content_pack_version");
  });
});

describe("formatMinor", () => {
  it("分转元,保留两位;面额带「面额」语义由调用方给", () => {
    expect(formatMinor(500)).toBe("5.00");
    expect(formatMinor(0)).toBe("0.00");
    expect(formatMinor(1234)).toBe("12.34");
  });
});

describe("evidenceRowList", () => {
  it("三类证据单列,固定顺序,计数来自 by_class", () => {
    const ev: EvidencePanel = {
      by_class: { staff_observed_submission: 3, staff_verified_post: 0, official_publish_confirmed: 4 },
      distinct_participations: 5,
      official_attempts: 4,
      note: "不能相加",
    };
    const rows = evidenceRowList(ev);
    expect(rows.map((r) => r.key)).toEqual([
      "staff_observed_submission",
      "staff_verified_post",
      "official_publish_confirmed",
    ]);
    expect(rows.map((r) => r.count)).toEqual([3, 0, 4]);
    // 3+0+4=7 ≠ 5:商家语言点明不能相加。
    expect(EVIDENCE_LABELS.staff_observed_submission).toContain("店员");
    expect(EVIDENCE_LABELS.official_publish_confirmed).toContain("官方");
  });
});

describe("stageNotAppliedLabels", () => {
  const stage: FunnelStage = {
    key: "funnel_visits",
    title: "访问",
    available: true,
    value: 17,
    definition: { source: "s", dedup_key: "d", denominator: "den", event_time: "t", window: "w" },
    applied_filters: ["campaign_id", "store_id", "touchpoint"],
  };
  it("请求了但该级无法落实的筛选,照实列出(不假装过滤)", () => {
    const labels = stageNotAppliedLabels(stage, { content_pack_version: "v3", platform: "douyin" });
    expect(labels).toEqual([FILTER_LABELS.content_pack_version, FILTER_LABELS.platform]);
  });
  it("该级全部落实或未筛选时为空", () => {
    expect(stageNotAppliedLabels(stage, { touchpoint: "nfc" })).toEqual([]);
    expect(stageNotAppliedLabels(stage, {})).toEqual([]);
  });
});

describe("诚实文案常量", () => {
  it("未开通:缺失保持未知,不显示 0", () => {
    expect(ANALYTICS_NOT_ENABLED_COPY).toContain("未开通");
    expect(ANALYTICS_NOT_ENABLED_COPY).toContain("未知");
  });
  it("空窗口是真实 0,文案不冒充「暂无数据=失败」", () => {
    expect(emptyWindowCopy()).toContain("这个窗口");
  });
});
