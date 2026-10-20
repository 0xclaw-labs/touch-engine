import { describe, expect, it } from "vitest";
import { formatCampaignWindow } from "../src/lib/public-time";

describe("guest campaign window", () => {
  it("formats a real window in Chinese local time, never ISO", () => {
    const text = formatCampaignWindow("2026-10-21T02:00:00.000Z", "2026-10-22T02:00:00.000Z", {
      locale: "zh-CN",
      timeZone: "Asia/Shanghai",
    });
    expect(text).toContain("2026年10月21日");
    expect(text).toContain("10:00");
    expect(text).not.toContain("T02:00:00");
    expect(text).toContain("至");
  });

  it("handles one-sided and invalid windows honestly", () => {
    expect(formatCampaignWindow("2026-10-21T02:00:00.000Z", null, { locale: "zh-CN", timeZone: "Asia/Shanghai" })).toContain("起");
    expect(formatCampaignWindow(null, null)).toBe("活动时间以门店现场说明为准");
    expect(formatCampaignWindow("bad", "also-bad")).toBe("活动时间以门店现场说明为准");
  });
});
