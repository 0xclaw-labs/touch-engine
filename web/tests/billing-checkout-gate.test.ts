// HUI-4664 收银接续门语义(ADV-9 PR12):available 门现在为真值语义——
// 可续付当且仅当 available 且 checkout_ref 与 checkout_url 都在场;
// available=false 按 reason 诚实分诊,文案只复述权威事实。

import { describe, expect, it } from "vitest";
import { canContinueCheckout, checkoutUnavailableText, type CheckoutRef } from "@/lib/billing";

const wired = (over: Partial<CheckoutRef> = {}): CheckoutRef => ({
  available: true,
  app_id: "",
  launch_target_id: "",
  checkout_ref: "ck_abc",
  checkout_url: "https://billing.test/billing/touch-checkout/ck_abc",
  state: "checkout_pending",
  ...over,
});

describe("canContinueCheckout 真值门", () => {
  it("available + 引用 + 续付 URL = 可续付", () => {
    expect(canContinueCheckout(wired())).toBe(true);
  });

  it("缺续付 URL 不算可续付(绝不发明跳转地址)", () => {
    expect(canContinueCheckout(wired({ checkout_url: undefined }))).toBe(false);
  });

  it("缺引用不算可续付", () => {
    expect(canContinueCheckout(wired({ checkout_ref: undefined }))).toBe(false);
  });

  it("available=false 一律不可续付", () => {
    expect(canContinueCheckout(wired({ available: false, reason: "checkout_unwired" }))).toBe(false);
    expect(canContinueCheckout(undefined)).toBe(false);
    expect(canContinueCheckout(null)).toBe(false);
  });
});

describe("checkoutUnavailableText 诚实分诊", () => {
  it("无引用=未接通,原单保留,不诱导重新下单", () => {
    const text = checkoutUnavailableText({ available: false, app_id: "", launch_target_id: "", reason: "checkout_unwired" });
    expect(text).toContain("支付接续尚未开通");
    expect(text).toContain("原单已保留");
    expect(text).toContain("无需重新下单");
  });

  it("seam 未配置=报价与原单保留(503 诚实态)", () => {
    const text = checkoutUnavailableText({ available: false, app_id: "", launch_target_id: "", reason: "checkout_unavailable" });
    expect(text).toContain("报价与原单都已保留");
  });

  it("付款授权变化=交回原付款人,不提付款权限", () => {
    const text = checkoutUnavailableText({ available: false, app_id: "", launch_target_id: "", reason: "original_payer_required" });
    expect(text).toContain("原付款人");
  });

  it("paid=事实已回流,回查原单,不重复付款", () => {
    const text = checkoutUnavailableText(wired({ available: false, reason: "not_awaiting_payment", state: "paid" }));
    expect(text).toContain("款项事实已回流");
    expect(text).toContain("无需重复付款");
  });

  it("failed=如实留因,交平台处理,不重复付款", () => {
    const text = checkoutUnavailableText(wired({ available: false, reason: "not_awaiting_payment", state: "failed", fail_reason: "provider_timeout" }));
    expect(text).toContain("provider_timeout");
    expect(text).toContain("无需重复付款");
  });
});
