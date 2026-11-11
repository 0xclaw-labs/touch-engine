import { describe, expect, it } from "vitest";
import {
  buildXhsHandoff,
  invokeXhsJs,
  parseXhsPayload,
  registerXhsJsSdkLoader,
  xhsSdkReady,
} from "../src/lib/publish-adapters/xiaohongshu";

describe("xiaohongshu handoff model (HUI-4586)", () => {
  it("parses a video payload without text fields", () => {
    const payload = parseXhsPayload(
      JSON.stringify({ type: "video", video: "https://m.example/v", cover: "https://m.example/c" }),
    );
    expect(payload).toEqual({ type: "video", video: "https://m.example/v", cover: "https://m.example/c", images: [] });
  });

  it("rejects payloads carrying title/content/topics (红线自检)", () => {
    for (const banned of ["title", "content", "topics", "desc", "text"]) {
      const raw = JSON.stringify({ type: "normal", images: ["https://m.example/i"], [banned]: "自动文案" });
      expect(parseXhsPayload(raw)).toBeNull();
    }
  });

  it("rejects broken shapes (非 https/空图片/坏 JSON/未知类型)", () => {
    expect(parseXhsPayload("not-json")).toBeNull();
    expect(parseXhsPayload(JSON.stringify({ type: "video" }))).toBeNull();
    expect(parseXhsPayload(JSON.stringify({ type: "normal", images: [] }))).toBeNull();
    expect(parseXhsPayload(JSON.stringify({ type: "normal", images: ["http://plain"] }))).toBeNull();
    expect(parseXhsPayload(JSON.stringify({ type: "poll", images: ["https://m.example/i"] }))).toBeNull();
  });

  it("keeps image order for normal notes", () => {
    const payload = parseXhsPayload(
      JSON.stringify({ type: "normal", images: ["https://m.example/i1", "https://m.example/i2"] }),
    );
    expect(payload?.images).toEqual(["https://m.example/i1", "https://m.example/i2"]);
  });

  it("builds the handoff model with an independent copy view", () => {
    const model = buildXhsHandoff({
      type: "js_media_handoff",
      params: { sdk: "xhs_js", app_id: "app", type: "video", payload: JSON.stringify({ type: "video", video: "https://m.example/v" }) },
      caption: "店庆\n#探店",
      steps: ["由你本人主动唤起小红书交接"],
      media_note: "短时效受控引用",
      note: "SDK 失败只是交接错误",
    });
    expect(model.kind).toBe("xhs_handoff");
    if (model.kind === "xhs_handoff") {
      expect(model.copyText).toContain("#探店");
      expect(model.sdkReady).toBe(false); // 官方 SDK 未接入=诚实不可用
    }
  });

  it("refuses non-xhs or text-carrying payloads at the model level", () => {
    expect(buildXhsHandoff({ type: "manual_export" }).kind).toBe("unavailable");
    expect(
      buildXhsHandoff({
        type: "js_media_handoff",
        params: { sdk: "xhs_js", payload: JSON.stringify({ type: "video", video: "https://m.example/v", title: "x" }) },
      }).kind,
    ).toBe("unavailable");
  });

  it("invoke without a registered loader throws honestly (绝不伪装 SDK)", async () => {
    registerXhsJsSdkLoader(null);
    expect(xhsSdkReady()).toBe(false);
    await expect(invokeXhsJs({ type: "video", video: "https://m.example/v", cover: "", images: [] })).rejects.toThrow(
      /not wired/,
    );
  });
});
