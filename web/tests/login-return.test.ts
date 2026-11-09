import { describe, expect, it } from "vitest";
import { loginReturnTarget } from "@/lib/login-return";

// HUI-2222 受控回跳白名单:登录后的落点只允许本应用路由,防开放重定向。
// 底层路径红线复用 HUI-1664 safe-redirect(sanitizeInSitePath);本层追加
// 商家门闸语境的追加规则:/api 不是页面;门店/订单/运营上下文参数不可经
// return 传递(查询串整体拒绝)。

describe("loginReturnTarget", () => {
  it("accepts in-app routes only", () => {
    expect(loginReturnTarget((k) => (k === "return" ? "/work/stores" : null))).toBe("/work/stores");
    expect(loginReturnTarget((k) => (k === "return" ? "/work/campaigns/abc-1" : null))).toBe("/work/campaigns/abc-1");
    expect(loginReturnTarget((k) => (k === "return" ? "/" : null))).toBe("/");
    expect(loginReturnTarget((k) => (k === "return" ? "/agency" : null))).toBe("/agency");
    expect(loginReturnTarget((k) => (k === "return" ? "/admin" : null))).toBe("/admin");
  });

  it("reads the first non-empty key among next/redirect/return", () => {
    expect(loginReturnTarget((k) => (k === "redirect" ? "/work/touchpoints" : null))).toBe("/work/touchpoints");
    expect(loginReturnTarget((k) => (k === "next" ? "/work/rewards" : null))).toBe("/work/rewards");
    // 空串的 return 不截胡,交给下一个键。
    expect(
      loginReturnTarget((k) => (k === "return" ? "" : k === "next" ? "/work/materials" : null)),
    ).toBe("/work/materials");
  });

  it("rejects external and protocol-relative targets outright", () => {
    expect(loginReturnTarget((k) => (k === "return" ? "https://evil.example/x" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "//evil.example" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "http://evil.example" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "javascript:alert(1)" : null))).toBeNull();
  });

  it("rejects encoded, backslash, uppercase and query-bearing bypasses", () => {
    expect(loginReturnTarget((k) => (k === "return" ? "%2F%2Fevil.example" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "/%2F%2Fevil.example" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "/\\evil" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "/Work/Stores" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "/work/stores?store_id=x" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "/work/stores#order" : null))).toBeNull();
  });

  it("never returns an API path (APIs are not pages)", () => {
    expect(loginReturnTarget((k) => (k === "return" ? "/api/v1/whoami" : null))).toBeNull();
    expect(loginReturnTarget((k) => (k === "return" ? "/api" : null))).toBeNull();
  });

  it("answers null without any parameter or with only blanks", () => {
    expect(loginReturnTarget(() => null)).toBeNull();
    expect(loginReturnTarget(() => "   ")).toBeNull();
  });
});
