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
  metricDisplay,
  stageNotAppliedLabels,
  type CatalogMetric,
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

describe("PR#7 目录指标(留资/CRM 单列,未接入非 0)", () => {
  it("有值显示值;未接入/未知显示「未接入」,绝不冒充 0", () => {
    const known: CatalogMetric = { key: "lead_submissions", title: "授权留资提交", available: true, value: 7 };
    const ungated: CatalogMetric = { key: "ugc_plays", title: "UGC 播放", available: false, value: null, reason: "未接入平台播放权限" };
    expect(metricDisplay(known)).toBe("7");
    expect(metricDisplay(ungated)).toBe("未接入");
    expect(metricDisplay({ key: "x", title: "x", available: true, value: null })).toBe("未接入");
  });
});

// ---- TOUCH-ALL-20261109-v1:action/environment 筛选 + 新块渲染数据 ---------------

import {
  ENVIRONMENT_OPTIONS,
  LAYER_TITLES,
  leadsPanelRows,
  type CostReconciliation,
  type LeadsPanel,
} from "../src/lib/analytics";

describe("v1 轮筛选:动作/环境", () => {
  it("actionId/environment 进查询串,空值不带", () => {
    const q = buildAnalyticsQuery({ window_start: "s", window_end: "e" }, {
      actionId: "sa_abc",
      environment: "ios_wechat",
    });
    expect(q).toContain("action_id=sa_abc");
    expect(q).toContain("environment=ios_wechat");
  });
  it("未选环境不带 environment 参数", () => {
    const q = buildAnalyticsQuery({ window_start: "s", window_end: "e" }, {});
    expect(q).not.toContain("action_id");
    expect(q).not.toContain("environment");
  });
  it("环境白名单=3535 六环境", () => {
    expect(ENVIRONMENT_OPTIONS).toEqual([
      "desktop_browser", "ios_safari", "android_browser", "ios_wechat", "android_wechat", "target_app",
    ]);
  });
  it("筛选标签含动作与环境(filter_echo 回显用)", () => {
    expect(FILTER_LABELS["action_id"]).toBe("动作");
    expect(FILTER_LABELS["environment"]).toBe("环境");
  });
});

describe("六层证据渲染数据", () => {
  it("LAYER_TITLES 固定六层顺序与商家措辞", () => {
    expect(LAYER_TITLES.map((l) => l.key)).toEqual([
      "action_clicked", "invoke_attempt", "handoff_media",
      "user_reported", "staff_confirmed", "platform_confirmed",
    ]);
    expect(LAYER_TITLES[5].label).toContain("平台");
  });
});

describe("线索口径渲染数据", () => {
  it("四桶分列+注记,待同步不并入已获客", () => {
    const lp: LeadsPanel = {
      campaign_submitted: 10, store_submitted: 4, pending_sync: 6, receiver_confirmed: 3,
      revoked: 1, receiver_declared_revoked: 1, unique_contacts: 9,
      note: "待同步不算已获客:...", definition: {
        source: "s", dedup_key: "d", denominator: "den", event_time: "e", window: "w",
      },
    };
    const rows = leadsPanelRows(lp);
    const byKey = Object.fromEntries(rows.map((r) => [r.key, r.value]));
    expect(byKey["campaign_submitted"]).toBe(10);
    expect(byKey["store_submitted"]).toBe(4);
    expect(byKey["pending_sync"]).toBe(6);
    expect(byKey["receiver_confirmed"]).toBe(3);
    expect(byKey["revoked"]).toBe(1);
    expect(rows.some((r) => r.label.includes("待同步"))).toBe(true);
    expect(rows.some((r) => r.label.includes("已获客"))).toBe(true);
  });
});

describe("费用对账渲染数据", () => {
  it("无支付事实=不可用,带未录入文案", () => {
    const cr: CostReconciliation = {
      subscription_available: false,
      subscription_paid_orders: 0,
      subscription_failed_orders: 1,
      subscription_note: "订阅实付是支付事实订单(active)的投影;...",
      ai_confirmed_minor: 0, ai_confirmed_count: 0,
      ai_consumed_minor: 0, ai_consumed_count: 0,
      ai_refunded_minor: 0, ai_refunded_count: 0,
      ai_failed_minor: 0, ai_failed_count: 0,
      hardware_available: false, hardware_paid_orders: 0,
      hardware_refund_minor: 0, hardware_refunds: 0,
      definition: { source: "s", dedup_key: "d", denominator: "den", event_time: "e", window: "w" },
      face_note: "f",
    };
    expect(cr.subscription_available).toBe(false);
    expect(cr.subscription_paid_minor).toBeUndefined();
  });
});
