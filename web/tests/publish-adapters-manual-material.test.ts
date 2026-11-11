import { describe, expect, it } from "vitest";
import {
  buildManualModel,
  probeShareSupport,
  shareFailureCopy,
  wechatContainerNote,
} from "../src/lib/publish-adapters/manual-material";

describe("manual family model (HUI-4588)", () => {
  const manualAction = {
    type: "manual_export",
    params: {
      platform: "wechat",
      share_mode: "system_share_or_copy",
      media_order: "media_00,media_01",
      media_00: "image:https://m.example/i1",
      media_01: "image:https://m.example/i2",
    },
    caption: "店庆\n#探店",
    steps: ["把素材保存到手机相册", "在朋友圈按顺序选择素材"],
    media_note: "短时效受控链接",
    note: "微信朋友圈普通 H5 原生发布契约未核实：系统分享成功只记录素材交接；「已完成」只存你本人的声明——都不生成平台确认，也不触发奖励。",
  };

  it("builds ordered material with the copy view", () => {
    const model = buildManualModel(manualAction, "ios_safari");
    expect(model.kind).toBe("manual_material");
    if (model.kind === "manual_material") {
      expect(model.orderedMedia.map((m) => m.url)).toEqual(["https://m.example/i1", "https://m.example/i2"]);
      expect(model.copyText).toContain("#探店");
      expect(model.suggested).toBe(false);
    }
  });

  it("marks suggested platforms via the note label", () => {
    const model = buildManualModel(
      { ...manualAction, params: { platform: "weibo" }, note: "【建议补充】微博素材交接（非获批自动发布）。" },
      null,
    );
    expect(model.kind).toBe("manual_material");
    if (model.kind === "manual_material") expect(model.suggested).toBe(true);
  });

  it("adds the honest wechat-container recovery without claiming bypass", () => {
    const model = buildManualModel(manualAction, "ios_wechat");
    if (model.kind !== "manual_material") throw new Error(String(model));
    const joined = model.steps.join("\n");
    expect(joined).toContain("在浏览器打开");
    expect(joined).not.toContain("自动绕开");
    expect(wechatContainerNote("ios_wechat")).toContain("不自动绕开");
  });

  it("adds the downloads-to-files honest hint when media exists", () => {
    const model = buildManualModel(manualAction, "desktop_browser");
    if (model.kind !== "manual_material") throw new Error(String(model));
    expect(model.steps.join("\n")).toContain("转存相册");
  });

  it("builds the A27 pure-link model with copy fallback", () => {
    const model = buildManualModel(
      {
        type: "open_url",
        url: "https://pub.example.com/s/store",
        params: { platform: "qq" },
        steps: ["复制下方门店链接"],
        note: "系统不会自动发消息给任何联系人",
      },
      null,
    );
    expect(model.kind).toBe("link_share");
    if (model.kind === "link_share") {
      expect(model.shareUrl).toBe("https://pub.example.com/s/store");
      expect(model.copyText).toBe("https://pub.example.com/s/store");
    }
  });

  it("answers unavailable for broken shapes (不伪造入口)", () => {
    expect(buildManualModel("bad", null).kind).toBe("unavailable");
    expect(buildManualModel({ type: "open_url", url: "http://plain" }, null).kind).toBe("unavailable");
    expect(buildManualModel({ type: "oauth_consent" }, null).kind).toBe("unavailable");
  });
});

describe("system share capability (实机三态降级)", () => {
  it("reports full system_share only when files are shareable", () => {
    const filesNav = {
      share: () => Promise.resolve(),
      canShare: (data?: { files?: unknown }) => Array.isArray(data?.files),
    };
    expect(probeShareSupport(filesNav)).toBe("system_share");
  });

  it("degrades to url-only or unavailable honestly", () => {
    expect(probeShareSupport({ share: () => Promise.resolve() })).toBe("share_url_only");
    expect(probeShareSupport({})).toBe("unavailable");
    expect(probeShareSupport(undefined)).toBe("unavailable");
  });

  it("maps concrete failures to recoverable copy (无假成功)", () => {
    expect(shareFailureCopy("aborted")).toContain("可随时重试");
    expect(shareFailureCopy("unsupported")).toContain("保存素材");
    expect(shareFailureCopy("permission")).toContain("权限");
    expect(shareFailureCopy("network")).toContain("没有完成");
    expect(shareFailureCopy("unknown")).toContain("没有完成");
  });
});
