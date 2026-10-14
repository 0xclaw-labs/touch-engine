import { describe, expect, it } from "vitest";
import {
  benefitCopy,
  canHandoff,
  canResume,
  credentialLooksLive,
  isKnownStatus,
  isSafeQrPayload,
  statusLabel,
  type ParticipationView,
} from "../src/lib/participation";

// HUI-3546:参与视图语义。红线:未确认预留绝不表述为「已保留」;
// 终态不可恢复;凭据活性的客户端预检只是预检,权威在服务端。

const open: ParticipationView = {
  id: "pt_a",
  status: "in_progress",
  benefit_reservation_state: "pending",
  expires_at: "2026-10-21T00:00:00Z",
};

describe("未确认预留绝不伪报已保留", () => {
  it("pending/failed/not_requested/未知都不出现「已保留」", () => {
    for (const state of ["pending", "failed", "not_requested", undefined, "CONFIRMED", "reserved"]) {
      const copy = benefitCopy(state);
      expect(copy === null || !copy.includes("已保留")).toBe(true);
    }
  });

  it("只有 confirmed 才说已保留", () => {
    expect(benefitCopy("confirmed")).toContain("已保留");
    expect(benefitCopy("pending")).toContain("确认");
    expect(benefitCopy("failed")).toContain("没有确认");
    expect(benefitCopy("not_requested")).toBe("");
    expect(benefitCopy(undefined)).toBe("");
  });
});

describe("参与状态语义", () => {
  it("未知状态 fail-closed:不认识就是不认识", () => {
    expect(isKnownStatus("pending")).toBe(true);
    expect(isKnownStatus("success")).toBe(false);
    expect(isKnownStatus(undefined)).toBe(false);
  });

  it("未完参与可恢复,终态不可恢复", () => {
    expect(canResume(open)).toBe(true);
    expect(canResume({ status: "pending" })).toBe(true);
    expect(canResume({ status: "awaiting_review" })).toBe(true);
    expect(canResume({ status: "canceled" })).toBe(false);
    expect(canResume({ status: "fulfilled" })).toBe(false);
    expect(canResume(null)).toBe(false);
    expect(canResume({ status: "success" })).toBe(false);
  });

  it("过窗后不可再交接,无窗视为可交接", () => {
    expect(canHandoff(open, new Date("2026-10-14T00:00:00Z"))).toBe(true);
    expect(canHandoff(open, new Date("2026-10-22T00:00:00Z"))).toBe(false);
    expect(canHandoff({ status: "pending" })).toBe(true);
    expect(canHandoff({ status: "canceled" })).toBe(false);
    expect(canHandoff({ status: "pending", expires_at: "not-a-date" })).toBe(false);
  });

  it("状态标签不做 success 抹平", () => {
    expect(statusLabel("pending")).not.toContain("完成");
    expect(statusLabel("fulfilled")).toContain("完成");
    expect(statusLabel("awaiting_review")).toContain("店员");
    expect(statusLabel("weird")).toContain("未知");
  });
});

describe("交接凭据", () => {
  it("活性预检:缺 token/缺时间/已过期都算死", () => {
    const at = new Date("2026-10-14T12:05:00Z");
    expect(credentialLooksLive({ token: "ptx_x.y", expires_at: "2026-10-14T12:10:00Z" }, at)).toBe(true);
    expect(credentialLooksLive({ token: "ptx_x.y", expires_at: "2026-10-14T12:10:00Z" }, new Date("2026-10-14T12:11:00Z"))).toBe(false);
    expect(credentialLooksLive({ expires_at: "2026-10-14T12:10:00Z" }, at)).toBe(false);
    expect(credentialLooksLive({ token: "ptx_x.y" }, at)).toBe(false);
    expect(credentialLooksLive(null, at)).toBe(false);
  });

  it("二维码载荷只认服务端规范形(无查询串、带 /p/ptx_)", () => {
    expect(isSafeQrPayload("https://h5.example.com/p/ptx_ABC.DEF")).toBe(true);
    expect(isSafeQrPayload("https://h5.example.com/p/ptx_ABC.DEF?x=1")).toBe(false);
    expect(isSafeQrPayload("https://h5.example.com/c/abc123")).toBe(false);
    expect(isSafeQrPayload("javascript:alert(1)")).toBe(false);
    expect(isSafeQrPayload(undefined)).toBe(false);
  });
});
