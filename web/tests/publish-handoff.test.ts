import { describe, expect, it } from "vitest";
import {
  CONFIRM_MISMATCH_COPY,
  DONE_NOTICE,
  canCancel,
  canRequestDone,
  entryOpenable,
  evidenceCopy,
  exportCaptionFor,
  isKnownHandoffStatus,
  manualStepsFor,
  mediaUrlSafe,
  refusalNotice,
  statusCopy,
  timeoutCopy,
  unsupportedLines,
  type PlanView,
} from "../src/components/publish/state";

// HUI-1670 顾客发布交接的文案与安全纪律(源码级,node 无 DOM)。
// 红线:只有 platform_confirmed 可以说「已发布」;拒绝面无手动后门;
// 申报只是核验请求;超时只查询不重发。

const ALL_STATUSES = [
  "previewed",
  "handoff_preparing",
  "handoff_ready",
  "editor_opened",
  "submitting",
  "awaiting_review",
  "platform_confirmed",
  "canceled",
  "failed",
  "unknown",
];

describe("发布成功只认官方确认", () => {
  it("九态中只有 platform_confirmed 的文案出现「已发布」", () => {
    for (const s of ALL_STATUSES) {
      const copy = statusCopy(s);
      if (s === "platform_confirmed") {
        expect(copy).toContain("已发布");
        expect(copy).toContain("官方确认");
      } else {
        expect(copy.includes("已发布")).toBe(false);
      }
    }
  });

  it("非确认态都不出现「发布成功」的肯定表述", () => {
    for (const s of ["previewed", "handoff_ready", "editor_opened", "submitting", "awaiting_review", "unknown"]) {
      expect(statusCopy(s).includes("等于发布成功") || statusCopy(s).includes("不是发布成功")).toBe(true);
    }
  });

  it("未知状态 fail-closed:不认识就是不认识,且绝不表述成功", () => {
    expect(isKnownHandoffStatus("previewed")).toBe(true);
    expect(isKnownHandoffStatus("publish_confirmed")).toBe(false);
    expect(isKnownHandoffStatus(undefined)).toBe(false);
    expect(statusCopy("publish_confirmed" as string)).toContain("未知");
    const fallback = statusCopy(undefined);
    expect(fallback).toContain("未知");
    expect(fallback.includes("已发布")).toBe(false);
    expect(fallback.includes("发布成功")).toBe(false);
  });

  it("failed/canceled 明确不触发付费生成", () => {
    expect(statusCopy("failed")).toContain("付费生成");
    expect(statusCopy("canceled")).toContain("付费生成");
  });

  it("超时只查询不重发,且不把未知当成功", () => {
    expect(timeoutCopy(false, 0)).toBe("");
    const copy = timeoutCopy(true, 2);
    expect(copy).toContain("查询");
    expect(copy).toContain("不会重发");
    expect(copy).toContain("发布成功");
  });
});

describe("三事实与证据来源", () => {
  it("人工事实永远标注不等于官方确认;官方回执标注服务端验签", () => {
    expect(evidenceCopy("staff_observation")).toContain("不等于官方确认");
    expect(evidenceCopy("staff_verification")).toContain("不等于官方确认");
    expect(evidenceCopy("customer_action")).toContain("不等于官方结果");
    expect(evidenceCopy("official_receipt")).toContain("服务端验签");
  });

  it("我已完成只是核验请求,不发奖", () => {
    expect(DONE_NOTICE).toContain("核验请求");
    expect(DONE_NOTICE).toContain("不算发布成功");
    expect(DONE_NOTICE).toContain("不触发奖励");
  });

  it("换稿必须重新确认,不静默更新", () => {
    expect(CONFIRM_MISMATCH_COPY).toContain("重新确认");
    expect(CONFIRM_MISMATCH_COPY).toContain("不会静默更新");
  });
});

describe("拒绝面没有手动后门", () => {
  const refused: PlanView = {
    kind: "refused",
    allowed: false,
    reason: "not_admitted",
    notice: "官方规范明确排除该场景",
    manual: { steps: ["手动步骤1"], export_caption: "导出文案" },
  };
  const manualOnly: PlanView = {
    kind: "manual_only",
    allowed: false,
    notice: "承接能力未确认",
    manual: { steps: ["导出素材", "打开平台手动发布"], export_caption: "合规导出:文案与素材清单" },
  };

  it("refused 即使服务端异常带回 manual,客户端也丢弃", () => {
    expect(manualStepsFor(refused)).toEqual([]);
    expect(exportCaptionFor(refused)).toBe("");
  });

  it("manual_only 透出合规导出与手动步骤", () => {
    expect(manualStepsFor(manualOnly).length).toBe(2);
    expect(exportCaptionFor(manualOnly)).toContain("合规导出");
  });

  it("automated 不渲染手动步骤;拒绝提示来自服务端 notice", () => {
    expect(manualStepsFor({ kind: "automated", allowed: true })).toEqual([]);
    expect(refusalNotice(refused)).toContain("明确排除");
    expect(refusalNotice(manualOnly)).toBe("");
  });
});

describe("操作窗口", () => {
  it("取消/申报只在活动态可用;终态与待核验不可", () => {
    for (const s of ["previewed", "handoff_preparing", "handoff_ready", "editor_opened", "submitting", "unknown"]) {
      expect(canCancel(s)).toBe(true);
      expect(canRequestDone(s)).toBe(true);
    }
    for (const s of ["awaiting_review", "platform_confirmed", "canceled", "failed", "weird", undefined]) {
      expect(canCancel(s)).toBe(false);
      expect(canRequestDone(s)).toBe(false);
    }
  });
});

describe("入口与媒体地址安全形状", () => {
  it("h5 入口只接受 https 直达,拒绝 javascript:/data:/http", () => {
    expect(entryOpenable({ method: "h5", url: "https://to.example.com/publish?media=x" })).toBe(true);
    expect(entryOpenable({ method: "h5", url: "javascript:alert(1)" })).toBe(false);
    expect(entryOpenable({ method: "h5", url: "data:text/html,hi" })).toBe(false);
    expect(entryOpenable({ method: "h5", url: "http://to.example.com/publish" })).toBe(false);
    expect(entryOpenable({ method: "h5" })).toBe(false);
    expect(entryOpenable(null)).toBe(false);
  });

  it("miniapp 入口只接受站内路径", () => {
    expect(entryOpenable({ method: "miniapp", miniapp_path: "/pages/publish/index" })).toBe(true);
    expect(entryOpenable({ method: "miniapp", miniapp_path: "https://evil.example/x" })).toBe(false);
    expect(entryOpenable({ method: "miniapp", miniapp_path: "pages/publish/index" })).toBe(false);
    expect(entryOpenable({ method: "miniapp", miniapp_path: "/pages/x?token=1" })).toBe(false);
  });

  it("受控媒体地址只允许 /api/ 相对路径或 https", () => {
    expect(mediaUrlSafe("/api/v1/media-handoffs/xxx?sig=1")).toBe(true);
    expect(mediaUrlSafe("https://media.example.com/x")).toBe(true);
    expect(mediaUrlSafe("//evil.example/x")).toBe(false);
    expect(mediaUrlSafe("javascript:alert(1)")).toBe(false);
    expect(mediaUrlSafe("data:application/octet-stream,AAAA")).toBe(false);
    expect(mediaUrlSafe(undefined)).toBe(false);
  });

  it("能力缺项按服务端给出的诚实说明透出", () => {
    expect(unsupportedLines({ unsupported: ["话题：该入口不支持自动带话题"] })).toHaveLength(1);
    expect(unsupportedLines(undefined)).toEqual([]);
  });
});
