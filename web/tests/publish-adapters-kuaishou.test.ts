import { describe, expect, it } from "vitest";
import {
  buildKuaishouModel,
  kuaishouAsyncCopy,
  parseKuaishouOAuthReturn,
} from "../src/lib/publish-adapters/kuaishou";

const authorizeUrl =
  "https://open.kuaishou.com/oauth2/authorize?client_id=cid&response_type=code&scope=user_video_publish&state=st1&redirect_uri=https%3A%2F%2Fpub.example%2Foauth%2Fkuaishou%2Fcallback";

describe("kuaishou oauth model (HUI-4587)", () => {
  it("renders the authorize phase with state and no secret", () => {
    const model = buildKuaishouModel({ type: "oauth_consent", url: authorizeUrl, params: { phase: "authorize", state: "st1" } });
    expect(model.kind).toBe("authorize");
    if (model.kind === "authorize") {
      expect(model.state).toBe("st1");
      expect(model.authorizeUrl).not.toContain("client_secret");
    }
  });

  it("refuses authorize urls that carry secret-class params (自检)", () => {
    const leaky = authorizeUrl + "&client_secret=sec";
    expect(buildKuaishouModel({ type: "oauth_consent", url: leaky, params: { phase: "authorize" } }).kind).toBe(
      "unavailable",
    );
  });

  it("refuses non-https or param-missing authorize urls", () => {
    expect(
      buildKuaishouModel({ type: "oauth_consent", url: "http://open.example/auth", params: { phase: "authorize" } }).kind,
    ).toBe("unavailable");
    expect(
      buildKuaishouModel({ type: "oauth_consent", url: "https://open.kuaishou.com/oauth2/authorize", params: { phase: "authorize" } }).kind,
    ).toBe("unavailable");
  });

  it("renders the confirm_submit phase after authorization", () => {
    const model = buildKuaishouModel({
      type: "oauth_consent",
      params: { phase: "confirm_submit", account_ref: "open_1" },
      note: "请确认本次内容后提交",
    });
    expect(model.kind).toBe("confirm_submit");
    if (model.kind === "confirm_submit") {
      expect(model.accountRef).toBe("open_1");
      expect(model.note).toContain("确认");
    }
  });

  it("answers unavailable for unknown phases/shapes (诚实)", () => {
    expect(buildKuaishouModel({ type: "manual_export" }).kind).toBe("unavailable");
    expect(buildKuaishouModel({ type: "oauth_consent", params: { phase: "???" } }).kind).toBe("unavailable");
    expect(buildKuaishouModel("bad").kind).toBe("unavailable");
  });

  it("parses the oauth redirect-back flags", () => {
    expect(parseKuaishouOAuthReturn("?oauth=ok&handoff_id=pha_1")).toEqual({
      outcome: "ok",
      handoffId: "pha_1",
      reason: "",
    });
    expect(parseKuaishouOAuthReturn("?oauth=denied&reason=authorization_failed")).toEqual({
      outcome: "denied",
      handoffId: "",
      reason: "authorization_failed",
    });
    expect(parseKuaishouOAuthReturn("").outcome).toBe("none");
  });

  it("keeps the async honesty copy (异步接受≠最终公开)", () => {
    expect(kuaishouAsyncCopy()).toContain("不等于最终公开");
  });
});
