import { describe, expect, it } from "vitest";

import {
  detectEnvironment,
  methodLabel,
  parseActionCapabilities,
  pickRoute,
  type CapabilityRoute,
} from "@/lib/env-capability";

// HUI-1896 环境差异化渲染:UA → 3535 六环境枚举;能力 routes → 诚实路线选择。
// 纪律(3535 合同):pending_external=true 永不自动当 manual 许可;unknown 不
// 冒充可完成;refused 给 reason_text;emergency_disabled 独立停用;allowed+
// promised 才是「可以走」的路线。reward 判定不进公开面(服务端权威)。

const UA_IOS_SAFARI =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1";
const UA_IOS_WECHAT =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.49 NetType/WIFI Language/zh_CN";
const UA_ANDROID_BROWSER =
  "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36";
const UA_ANDROID_WECHAT =
  "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36 MicroMessenger/8.0.49.2560(0x28003135) XWEB";
const UA_DESKTOP =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36";

function route(partial: Partial<CapabilityRoute>): CapabilityRoute {
  return {
    platform: "douyin", method: "direct_open", environment: "ios_safari", scenario: "review",
    app_binding: "", status: "admitted", allowed: true, promised: true, pending_external: false,
    reason: "", reason_text: "", navigation: "supported", media_handoff: "unsupported",
    platform_receipt: "unsupported", reward_eligibility: "unsupported",
    field_support: {}, required_inputs: [], missing: [], official_sources: [],
    evidence_grade: "", checked_at: "", valid_until: "", emergency_disabled: false, close_reason: "",
    ...partial,
  };
}

describe("detectEnvironment:UA → 六环境", () => {
  it("iPhone Safari → ios_safari;iPhone 微信 → ios_wechat", () => {
    expect(detectEnvironment(UA_IOS_SAFARI)).toBe("ios_safari");
    expect(detectEnvironment(UA_IOS_WECHAT)).toBe("ios_wechat");
  });

  it("Android 浏览器/微信分得开", () => {
    expect(detectEnvironment(UA_ANDROID_BROWSER)).toBe("android_browser");
    expect(detectEnvironment(UA_ANDROID_WECHAT)).toBe("android_wechat");
  });

  it("桌面 → desktop_browser;空/未知 UA → desktop_browser(保守基线)", () => {
    expect(detectEnvironment(UA_DESKTOP)).toBe("desktop_browser");
    expect(detectEnvironment("")).toBe("desktop_browser");
    expect(detectEnvironment("some-weird-agent")).toBe("desktop_browser");
  });

  it("target_app 不由 UA 判定(应用内 webview 无可靠指纹,不猜)", () => {
    expect(detectEnvironment(UA_IOS_WECHAT, true)).toBe("target_app");
    expect(detectEnvironment(UA_IOS_SAFARI, true)).toBe("target_app");
  });
});

describe("parseActionCapabilities:白名单解析 3535 公开投影", () => {
  it("解析 routes;缺 routes/非对象 → null", () => {
    const ok = parseActionCapabilities({ action_id: "sa_a8", catalog_code: "A08", action_kind: "review", label: "大众点评", routes: [route({})] });
    expect(ok).not.toBeNull();
    expect(ok?.actionId).toBe("sa_a8");
    expect(ok?.routes).toHaveLength(1);
    expect(parseActionCapabilities(null)).toBeNull();
    expect(parseActionCapabilities({ action_id: "x" })).toBeNull();
    expect(parseActionCapabilities({ action_id: "x", routes: "no" })).toBeNull();
  });
});

describe("pickRoute:环境过滤与诚实分级", () => {
  const routes: CapabilityRoute[] = [
    route({ environment: "ios_safari", allowed: true, promised: true, method: "direct_open" }),
    route({ environment: "desktop_browser", allowed: true, promised: true, method: "h5_share" }),
  ];

  it("按当前环境过滤;目标环境无记录 → unknown_kind=pending(不冒充可用)", () => {
    const caps = parseActionCapabilities({ action_id: "sa_a8", routes })!;
    expect(pickRoute(caps.routes, "ios_safari")?.kind).toBe("ok");
    expect(pickRoute(caps.routes, "android_wechat")?.kind).toBe("pending");
    expect(pickRoute(caps.routes, "android_wechat")?.face).toContain("暂未");
  });

  it("allowed+promised 优先于 allowed-only", () => {
    const caps = parseActionCapabilities({
      action_id: "sa_a1",
      routes: [
        route({ environment: "ios_safari", method: "manual_material", promised: false, allowed: true }),
        route({ environment: "ios_safari", method: "h5_share", promised: true, allowed: true }),
      ],
    })!;
    const pick = pickRoute(caps.routes, "ios_safari");
    expect(pick?.kind).toBe("ok");
    expect(pick?.route.method).toBe("h5_share");
  });

  it("pending_external=true → pending 文案,绝不写「可以发布/完成」", () => {
    const caps = parseActionCapabilities({
      action_id: "sa_a1",
      routes: [route({ environment: "ios_safari", allowed: true, promised: false, pending_external: true, reason: "pending_platform" })],
    })!;
    const pick = pickRoute(caps.routes, "ios_safari");
    expect(pick?.kind).toBe("pending");
    expect(pick?.face).toContain("平台");
    expect(pick?.face).not.toMatch(/可以发布|可以完成|一键/);
  });

  it("allowed=false → refused + reason_text;空 reason 有兜底文案", () => {
    const caps = parseActionCapabilities({
      action_id: "sa_a1",
      routes: [route({ environment: "ios_safari", allowed: false, reason: "not_admitted", reason_text: "该路线暂未准入" })],
    })!;
    const pick = pickRoute(caps.routes, "ios_safari");
    expect(pick?.kind).toBe("refused");
    expect(pick?.face).toContain("暂未准入");
    const caps2 = parseActionCapabilities({
      action_id: "sa_a1",
      routes: [route({ environment: "ios_safari", allowed: false, reason: "not_admitted", reason_text: "" })],
    })!;
    expect(pickRoute(caps2.routes, "ios_safari")?.face).toContain("未放行");
  });

  it("emergency_disabled → disabled 文案(独立否决)", () => {
    const caps = parseActionCapabilities({
      action_id: "sa_a1",
      routes: [route({ environment: "ios_safari", emergency_disabled: true, close_reason: "平台紧急维护" })],
    })!;
    const pick = pickRoute(caps.routes, "ios_safari");
    expect(pick?.kind).toBe("disabled");
    expect(pick?.face).toContain("停用");
    expect(pick?.face).toContain("紧急维护");
  });
});

describe("methodLabel:方法→真实状态文案(不编造成功)", () => {
  it("八种方法各有文案;未知方法给保守文案", () => {
    for (const method of ["h5_share", "miniapp", "js_media_handoff", "oauth_server_push", "native_sdk", "link_share", "manual_material", "direct_open"]) {
      expect(methodLabel(method)).not.toBe("");
    }
    expect(methodLabel("mystery")).toContain("现场");
    expect(methodLabel("manual_material")).toContain("手动");
    expect(methodLabel("oauth_server_push")).toContain("授权");
    expect(methodLabel("direct_open")).toContain("打开");
  });
});
