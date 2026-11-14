import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pickHandoff, planToday, rewardPresentation, surfaceLabel } from "@/lib/product-finish";

describe("奖励未知不是成功", () => {
  it("status unknown 不成功，也不用成功语气", () => {
    const view = rewardPresentation({ status: "unknown" });
    expect(view.success).toBe(false);
    expect(view.tone).toBe("unknown");
    expect(view.className).toBe("tk-unknown");
    expect(view.className).not.toBe("tk-ok");
  });

  it("待核实和空状态都不是已发放", () => {
    for (const body of [{ status: "pending_verification", grant_enabled: false }, {}, { status: "" }]) {
      const view = rewardPresentation(body);
      expect(view.success).toBe(false);
      expect(view.className).not.toBe("tk-ok");
    }
  });
});

describe("商家今天的下一步", () => {
  it("没有门店时先登记，不把未知摘要列为操作任务", () => {
    const items = planToday({
      stores: 0,
      active: [],
      paused: [],
      ended: [],
      drafts: [],
      materialGap: "unknown",
      pendingLeads: null,
      rewardKnown: false,
      redemptionKnown: false,
    });
    expect(items[0]).toMatchObject({ id: "stores", href: "/work/stores", tone: "empty" });
    expect(items.some((item) => item.tone === "unknown")).toBe(false);
    expect(items.some((item) => item.tone === "confirmed")).toBe(false);
  });

  it("草稿和确需补材可操作，正常活动终态不变成待办", () => {
    const items = planToday({
      stores: 1,
      active: [{ id: "cmp_live", title: "到店送一杯" }],
      paused: [{ id: "cmp_pause", title: "暂停中" }],
      ended: [{ id: "cmp_end", title: "已结束场" }],
      drafts: [{ id: "cmp_draft", title: "草稿场" }],
      materialGap: "needs_material",
      pendingLeads: 2,
      rewardKnown: false,
      redemptionKnown: false,
    });
    expect(items.map((item) => item.href)).toEqual(expect.arrayContaining([
      "/work/campaigns/cmp_draft", "/work/materials", "/work/leads?return=/",
    ]));
    expect(items.some((item) => ["paused", "ended", "active", "rewards"].includes(item.id))).toBe(false);
  });

  it("活动正常过期不冒充今天待办", () => {
    const items = planToday({
      stores: 1,
      active: [{ id: "cmp_late", title: "窗口已过", ends_at: "2020-01-01T00:00:00Z" }],
      paused: [],
      ended: [],
      drafts: [],
      materialGap: "has_material",
      pendingLeads: 0,
      rewardKnown: true,
      redemptionKnown: true,
    });
    expect(items.some(item => item.tone === "expired" || item.tone === "now")).toBe(false);
    expect(items[0].id).toBe("quiet");
  });
});

describe("页面状态文案", () => {
  it("空、加载、错误、过期、暂停、结束各自不同", () => {
    const labels = ["empty", "loading", "error", "expired", "paused", "ended"].map((state) => surfaceLabel(state as "empty"));
    expect(new Set(labels).size).toBe(labels.length);
    expect(surfaceLabel("expired")).toContain("过期");
    expect(surfaceLabel("paused")).toContain("暂停");
    expect(surfaceLabel("ended")).toContain("结束");
    expect(surfaceLabel("error")).not.toContain("成功");
    expect(surfaceLabel("empty")).not.toContain("0");
  });
});

describe("pickHandoff 交接选择", () => {
  const now = "2026-10-04T12:00:00.000Z";

  it("第一个未过期 active → live（跳过前面已过期的 active）", () => {
    expect(
      pickHandoff(
        [
          { id: "c1", status: "active", ends_at: "2026-10-04T11:00:00.000Z" },
          { id: "c2", status: "active", ends_at: "2026-10-05T12:00:00.000Z" },
        ],
        now,
      ),
    ).toEqual({ id: "c2", state: "live" });
  });

  it("无可交接 active → 第一个 draft → draft", () => {
    expect(
      pickHandoff(
        [
          { id: "c1", status: "active", ends_at: "2026-10-04T11:00:00.000Z" },
          { id: "d1", status: "draft" },
        ],
        now,
      ),
    ).toEqual({ id: "d1", state: "draft" });
  });

  it("全过期且无 draft → none（不从过期场交接；过期 paused 不进 fallback）", () => {
    expect(
      pickHandoff(
        [
          { id: "c1", status: "active", ends_at: "2026-10-04T11:00:00.000Z" },
          { id: "e1", status: "ended", ends_at: "2026-10-04T10:00:00.000Z" },
          { id: "p1", status: "paused", ends_at: "2026-10-04T09:00:00.000Z" },
        ],
        now,
      ),
    ).toEqual({ state: "none" });
  });

  it("无 active 无 draft 时，未过期 paused 作交接目标（行为保持：第三 fallback）", () => {
    expect(pickHandoff([{ id: "p1", status: "paused", ends_at: "2026-10-05T12:00:00.000Z" }], now)).toEqual({
      id: "p1",
      state: "paused_fallback",
    });
  });

  it("fallback 排除已过期：过期 paused 不接交接", () => {
    expect(pickHandoff([{ id: "p1", status: "paused", ends_at: "2026-10-04T11:00:00.000Z" }], now)).toEqual({
      state: "none",
    });
  });

  it("fallback 排除 ended：ended 即使 ends_at 在未来也不接交接", () => {
    expect(pickHandoff([{ id: "e1", status: "ended", ends_at: "2026-10-06T12:00:00.000Z" }], now)).toEqual({
      state: "none",
    });
  });

  it("空清单 → none", () => {
    expect(pickHandoff([], now)).toEqual({ state: "none" });
  });

  it("nowISO 注入决定过期判断", () => {
    const rows = [{ id: "c1", status: "active", ends_at: "2026-10-04T13:00:00.000Z" }];
    expect(pickHandoff(rows, now)).toEqual({ id: "c1", state: "live" });
    expect(pickHandoff(rows, "2026-10-04T14:00:00.000Z")).toEqual({ state: "none" });
  });
});
