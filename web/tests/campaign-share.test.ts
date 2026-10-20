import { describe, expect, it } from "vitest";
import { campaignShareText, campaignShareUrl, pickShareLink, shareAvailability } from "../src/lib/campaign-share";

const campaign = {
  title: "周末五折",
  store_name: "湖滨店",
  starts_at: "2026-10-21T02:00:00.000Z",
  ends_at: "2026-10-22T02:00:00.000Z",
};

describe("campaign share", () => {
  it("builds a same-origin public URL without hardcoding a domain", () => {
    expect(campaignShareUrl("https://touch.example.com", "ABCDEFGHJKLM")).toBe("https://touch.example.com/c/ABCDEFGHJKLM");
    expect(campaignShareUrl("https://touch.example.com/", "ABCDEFGHJKLM")).toBe("https://touch.example.com/c/ABCDEFGHJKLM");
    expect(campaignShareUrl("http://127.0.0.1:18462", "ABCDEFGHJKLM")).toBe("http://127.0.0.1:18462/c/ABCDEFGHJKLM");
  });

  it("includes title, store, window and URL in the share template", () => {
    const text = campaignShareText({ ...campaign, url: "https://example.com/c/ABCDEFGHJKLM" });
    expect(text).toContain("周末五折");
    expect(text).toContain("湖滨店");
    expect(text).toContain("https://example.com/c/ABCDEFGHJKLM");
    expect(text).not.toContain("2026-10-21T02:00:00");
  });

  it("only shares an enabled link", () => {
    expect(pickShareLink([{ code: "AAA", enabled: false }, { code: "BBB", enabled: true }])?.code).toBe("BBB");
    expect(pickShareLink([])).toBeNull();
    expect(pickShareLink([{ code: "AAA", enabled: false }])).toBeNull();
  });

  it("tells the truth when the campaign is ended or the link is disabled", () => {
    const live = shareAvailability({ status: "active", ends_at: null, link: { code: "AAA", enabled: true } });
    expect(live).toEqual({ canShare: true, note: "" });
    const noLink = shareAvailability({ status: "active", ends_at: null, link: null });
    expect(noLink.canShare).toBe(false);
    expect(noLink.note).toContain("短码");
    const ended = shareAvailability({ status: "ended", ends_at: null, link: { code: "AAA", enabled: true } });
    expect(ended.canShare).toBe(true);
    expect(ended.note).toContain("已结束");
    const expired = shareAvailability({ status: "active", ends_at: "2026-10-01T00:00:00Z", link: { code: "AAA", enabled: true } });
    expect(expired.note).toContain("窗口");
  });
});
