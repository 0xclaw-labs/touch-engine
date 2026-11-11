import { describe, expect, it } from "vitest";
import { buildDouyinHandoff, douyinRefusalCopy } from "../src/lib/publish-adapters/douyin";

describe("douyin handoff model (HUI-4585)", () => {
  it("renders the official H5 invoke with server-issued params", () => {
    const model = buildDouyinHandoff(
      {
        type: "js_media_handoff",
        params: {
          sdk: "douyin_h5",
          client_key: "ck_1",
          share_id: "dshare_1",
          media_url: "https://media.example/v",
          share_expires_at: "2026-11-11T10:00:00Z",
        },
        steps: ["确认后由你本人主动唤起抖音"],
        note: "官方获准前提下放行",
      },
      "ios_safari",
    );
    expect(model.kind).toBe("js_invoke");
    if (model.kind === "js_invoke") {
      expect(model.shareId).toBe("dshare_1");
      expect(model.mediaUrl).toBe("https://media.example/v");
      expect(model.signingGap).toEqual([]);
    }
  });

  it("surfaces the honest signing gap when the server reports one", () => {
    const model = buildDouyinHandoff(
      {
        type: "js_media_handoff",
        params: { sdk: "douyin_h5", share_id: "dshare_1" },
        unsupported_fields: ["官方签名：抖音 H5 官方签名机制尚未接入"],
      },
      null,
    );
    expect(model.kind).toBe("js_invoke");
    if (model.kind === "js_invoke") {
      expect(model.signingGap).toHaveLength(1);
      expect(model.signingGap[0]).toContain("签名");
    }
  });

  it("refuses generic param shapes (不拼通用 media/title/body query)", () => {
    expect(buildDouyinHandoff({ type: "js_media_handoff", params: {} }, null).kind).toBe("unavailable");
    expect(
      buildDouyinHandoff({ type: "js_media_handoff", params: { media: "x", title: "t" } }, null).kind,
    ).toBe("unavailable");
    expect(
      buildDouyinHandoff(
        { type: "js_media_handoff", params: { sdk: "douyin_h5", share_id: "s", media_url: "http://plain" } },
        null,
      ).kind,
    ).toBe("unavailable");
  });

  it("renders the miniapp task route only with its sdk marker", () => {
    const ok = buildDouyinHandoff(
      { type: "open_miniapp", miniapp_path: "pages/task", params: { sdk: "douyin_miniapp_task", share_id: "d2" } },
      null,
    );
    expect(ok.kind).toBe("miniapp_task");
    const bad = buildDouyinHandoff({ type: "open_miniapp", miniapp_path: "pages/task", params: {} }, null);
    expect(bad.kind).toBe("unavailable");
  });

  it("passes the refused notice through verbatim (无手动后门文案)", () => {
    const copy = douyinRefusalCopy("官方规范明确排除该场景");
    expect(copy).toBe("官方规范明确排除该场景");
    expect(douyinRefusalCopy("")).toContain("没有可替代的手动发布入口");
  });
});
