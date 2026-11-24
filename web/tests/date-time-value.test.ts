import { describe, expect, it } from "vitest";
import {
  combineDateTime,
  formatDateLabel,
  formatDateTimeLabel,
  formatDateValue,
  parseDateValue,
  parseDateTimeValue,
  pad2,
} from "@/lib/date-time-value";

// 复合件纯函数收口：本地墙钟值解析/格式化往返；拒绝越界与进位回绕；不做 UTC 偏移。

describe("日期值解析与格式化", () => {
  it("round-trips a valid local date", () => {
    const date = parseDateValue("2026-11-24");
    expect(date).not.toBeNull();
    expect(formatDateValue(date!)).toBe("2026-11-24");
    expect(formatDateLabel("2026-11-24")).toBe("2026-11-24");
  });

  it("rejects malformed, out-of-range and rollover values", () => {
    expect(parseDateValue("")).toBeNull();
    expect(parseDateValue("2026/11/24")).toBeNull();
    expect(parseDateValue("2026-13-01")).toBeNull();
    expect(parseDateValue("2026-11-32")).toBeNull();
    // 13 月回绕到次年 1 月、32 日回绕到次月 1 日均必须被拒。
    expect(parseDateValue("2026-13-40")).toBeNull();
  });

  it("pad2 zero-pads", () => {
    expect(pad2(3)).toBe("03");
    expect(pad2(12)).toBe("12");
  });
});

describe("日期时间值解析与合并", () => {
  it("round-trips a valid local datetime", () => {
    const parsed = parseDateTimeValue("2026-11-24T08:30");
    expect(parsed).not.toBeNull();
    expect(parsed!.time).toBe("08:30");
    expect(combineDateTime(parsed!.date, parsed!.time)).toBe("2026-11-24T08:30");
    expect(formatDateTimeLabel("2026-11-24T08:30")).toBe("2026-11-24 08:30");
  });

  it("rejects malformed, out-of-range and rollover datetimes", () => {
    expect(parseDateTimeValue("2026-11-24")).toBeNull();
    expect(parseDateTimeValue("not-a-date")).toBeNull();
    expect(parseDateTimeValue("2026-13-01T25:61")).toBeNull();
    expect(parseDateTimeValue("2026-11-32T00:00")).toBeNull();
  });

  it("combineDateTime falls back to 00:00 on empty time", () => {
    const date = parseDateValue("2026-11-24");
    expect(combineDateTime(date!, "")).toBe("2026-11-24T00:00");
    expect(combineDateTime(new Date(NaN), "10:00")).toBe("");
  });
});
