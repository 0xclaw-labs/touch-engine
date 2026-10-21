import { describe, expect, it } from "vitest";
import { failureText, loginFailureText } from "@/lib/failure-copy";

// HUI-2628 fix2（gate-r2 盲评 fail 项）：错误面不泄漏机器码——
// 状态码、机器错误 token、后端原文不得出现在产品文案里，每条都带下一步动作。
describe("failureText 产品语句", () => {
  it("任何输出都不含三位状态码、机器错误码或后端原文", () => {
    const cases: Array<[number, Record<string, unknown>]> = [
      [400, { error: "store_required", message: "活动还没有门店，不能记下门店参数。" }],
      [400, {}],
      [401, { error: "unauthorized" }],
      [403, { error: "forbidden" }],
      [404, { error: "not_found" }],
      [500, { error: "internal_error", message: "boom" }],
      [502, {}],
      [422, { message: "title too long" }],
    ];
    for (const [status, data] of cases) {
      const text = failureText(status, data);
      expect(text, String(status)).not.toMatch(/\b\d{3}\b/);
      expect(text, String(status)).not.toMatch(/store_required|unauthorized|forbidden|not_found|internal_error/);
      expect(text, String(status)).not.toContain("boom");
      expect(text, String(status)).not.toContain("title too long");
    }
  });

  it("store_required 给出关联门店的下一步动作", () => {
    const text = failureText(400, { error: "store_required" });
    expect(text).toContain("门店");
    expect(text).toMatch(/先|请/);
  });

  it("登录态/找不到/服务不可用/默认各有产品语句与下一步", () => {
    expect(failureText(401, {})).toMatch(/重新登录/);
    expect(failureText(403, {})).toMatch(/权限|重新登录/);
    expect(failureText(404, {})).toMatch(/删除|不存在/);
    expect(failureText(500, {})).toMatch(/稍(等|后)/);
    expect(failureText(422, {})).toMatch(/再试/);
  });
});

describe("loginFailureText（登录面专用口径）", () => {
  it("401 wrong-password 说邮箱或密码不对，不冒充会话过期", () => {
    const text = loginFailureText(401, { error: "login_rejected" });
    expect(text).toMatch(/邮箱或密码/);
    expect(text).toMatch(/管理员/);
    expect(text).not.toMatch(/过期/);
  });
  it("其余状态回落 failureText 收口，不开第二套映射", () => {
    expect(loginFailureText(401, { error: "user_disabled" })).toMatch(/邮箱或密码/);
    expect(loginFailureText(500, {})).toMatch(/稍(等|后)/);
    expect(loginFailureText(403, {})).toMatch(/权限|重新登录/);
  });
});
