import { describe, expect, it } from "vitest";
import {
  detectEnvironment,
  parseClientAction,
  parseOrderedMedia,
} from "../src/lib/publish-adapters/client-action";

describe("client-action shape gate", () => {
  it("parses a well-formed typed action", () => {
    const action = parseClientAction({
      type: "manual_export",
      caption: "标题\n正文",
      steps: ["第一步", "第二步"],
      params: { platform: "wechat", share_mode: "system_share_or_copy" },
      note: "说明",
    });
    expect(action).not.toBeNull();
    expect(action?.type).toBe("manual_export");
    expect(action?.params["platform"]).toBe("wechat");
  });

  it("rejects unknown types and executable-looking payloads", () => {
    expect(parseClientAction({ type: "eval_js" })).toBeNull();
    expect(parseClientAction({ type: "manual_export", params: { deep: { nested: 1 } } })).toBeNull();
    expect(parseClientAction({ type: "manual_export", steps: ["ok", 42] })).toBeNull();
    expect(parseClientAction("not-an-object")).toBeNull();
    expect(parseClientAction(null)).toBeNull();
  });

  it("rejects non-https urls (公开面无明文)", () => {
    expect(parseClientAction({ type: "open_url", url: "http://example.com/x" })).toBeNull();
    expect(parseClientAction({ type: "open_url", url: "javascript:alert(1)" })).toBeNull();
    expect(parseClientAction({ type: "open_url", url: "https://example.com/x" })).not.toBeNull();
  });
});

describe("environment detection (六枚举诚实边界)", () => {
  it("classifies wechat containers", () => {
    expect(
      detectEnvironment("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) MicroMessenger/8.0").environment,
    ).toBe("ios_wechat");
    expect(
      detectEnvironment("Mozilla/5.0 (Linux; Android 14) MicroMessenger/8.0").environment,
    ).toBe("android_wechat");
  });

  it("classifies mobile browsers and desktop", () => {
    expect(detectEnvironment("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) Safari").environment).toBe("ios_safari");
    expect(detectEnvironment("Mozilla/5.0 (Linux; Android 14) Chrome").environment).toBe("android_browser");
    expect(detectEnvironment("Mozilla/5.0 (Macintosh) Safari").environment).toBe("desktop_browser");
  });

  it("answers null for unclassifiable UAs instead of guessing target_app", () => {
    expect(detectEnvironment("").environment).toBeNull();
    expect(detectEnvironment("weird-agent").environment).toBeNull();
  });
});

describe("ordered media parsing (A05/A07 保序)", () => {
  it("keeps the server-issued order", () => {
    const media = parseOrderedMedia({
      media_order: "media_00,media_01",
      media_00: "image:https://m.example/i1",
      media_01: "image:https://m.example/i2",
    });
    expect(media.map((m) => m.url)).toEqual(["https://m.example/i1", "https://m.example/i2"]);
    expect(media[0].kind).toBe("image");
  });

  it("drops broken entries instead of faking rows", () => {
    expect(
      parseOrderedMedia({ media_order: "media_00", media_00: "image:http://plain" }),
    ).toEqual([]);
    expect(parseOrderedMedia({})).toEqual([]);
  });
});
