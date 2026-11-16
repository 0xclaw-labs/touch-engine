// HUI-4664 G1 正显示面(ADV-9 FIX-C)/work/billing「可续付」区块两态验收:
//   - 正显示态:存在可续付 handoff(available + checkout_ref + checkout_url
//     三件套齐全)→ 出块,引用/金额/续付按钮(跳 checkout_url)在场;
//   - 诚实态:无引用/门不成立 → 不出块,不虚假承诺(绝不发明跳转地址,也
//     不制造「可续付」存在性暗示);页面既有未接通文案保持在场。

import { readFileSync } from "node:fs";
import path from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  CheckoutContinuationBlock,
  collectResumableCheckouts,
  type CheckoutContinuationEntry,
} from "../src/components/work/billing/checkout-continuation";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = (p: string) => readFileSync(path.join(webRoot, p), "utf8");

// build an entry with checkout overrides applied (default = fully wired truth)
function entry(over: Partial<CheckoutContinuationEntry["checkout"]> = {}): CheckoutContinuationEntry {
  return {
    storeId: "st_1",
    handoffId: "cph_1",
    checkout: {
      available: true,
      app_id: "touch-engine",
      launch_target_id: "ti-touch-engine-billing",
      checkout_ref: "ck_int5_001",
      checkout_url: "https://billing.test/billing/touch-checkout/ck_int5_001",
      state: "checkout_pending",
      amount_minor: 990000,
      currency: "CNY",
      ...over,
    },
  };
}

describe("可续付正显示态(真值三件套齐全)", () => {
  it("出块:可续付标记 + checkout_ref + 金额 + 续付按钮跳 checkout_url", () => {
    const html = renderToStaticMarkup(createElement(CheckoutContinuationBlock, { entries: [entry()] }));
    expect(html).toContain('data-testid="billing-continuations"');
    expect(html).toContain("可续付");
    expect(html).toContain("ck_int5_001");
    expect(html).toContain("9900.00 CNY");
    expect(html).toContain('data-action="continue-checkout"');
    expect(html).toContain('href="https://billing.test/billing/touch-checkout/ck_int5_001"');
    // 纪律重申:续付是原单接续,不是新购;回查只读。
    expect(html).toContain("不是新购");
  });

  it("多条可续付引用逐条在场", () => {
    const second = entry({ checkout_ref: "ck_int5_002", checkout_url: "https://billing.test/billing/touch-checkout/ck_int5_002" });
    const html = renderToStaticMarkup(createElement(CheckoutContinuationBlock, { entries: [entry(), second] }));
    expect(html).toContain("ck_int5_001");
    expect(html).toContain("ck_int5_002");
    expect((html.match(/data-action="continue-checkout"/g) ?? []).length).toBe(2);
  });

  it("金额缺席=照实说以收银台结算页为准,不显示金额数字", () => {
    const noAmount = entry({ amount_minor: undefined, currency: undefined });
    const html = renderToStaticMarkup(createElement(CheckoutContinuationBlock, { entries: [noAmount] }));
    expect(html).toContain("金额以收银台结算页为准");
    expect(html).not.toContain("9900.00 CNY");
  });
});

describe("真值门裁决(collectResumableCheckouts)", () => {
  it("缺 checkout_url 不算可续付(绝不发明跳转地址)", () => {
    const gated = collectResumableCheckouts([entry({ checkout_url: undefined })]);
    expect(gated).toHaveLength(0);
  });

  it("缺 checkout_ref 不算可续付", () => {
    const gated = collectResumableCheckouts([entry({ checkout_ref: undefined })]);
    expect(gated).toHaveLength(0);
  });

  it("available=false(未接通/not_awaiting_payment)一律不出块", () => {
    expect(collectResumableCheckouts([entry({ available: false, reason: "checkout_unavailable" })])).toHaveLength(0);
    expect(collectResumableCheckouts([entry({ available: false, reason: "not_awaiting_payment", state: "paid" })])).toHaveLength(0);
  });
});

describe("诚实态(无引用)", () => {
  it("空条目=整块不渲染:无「可续付」字样,无编造的跳转地址", () => {
    const html = renderToStaticMarkup(createElement(CheckoutContinuationBlock, { entries: [] }));
    expect(html).toBe("");
    expect(html).not.toContain("可续付");
    expect(html).not.toContain("touch-checkout/");
  });

  it("只有门不成立的条目=同样不出块", () => {
    const html = renderToStaticMarkup(createElement(CheckoutContinuationBlock, { entries: [entry({ available: false, reason: "checkout_unavailable" })] }));
    expect(html).toBe("");
  });
});

describe("账单页接线(PR-3 前端消费面)", () => {
  it("billing-desk 挂正显示块并走只读发现链(stores→handoffs→checkout 投影)", () => {
    const desk = read("src/components/work/billing/billing-desk.tsx");
    expect(desk).toContain("CheckoutContinuationBlock");
    expect(desk).toContain('session.api("GET", "stores")');
    expect(desk).toContain("content-production-handoffs");
    expect(desk).toContain("/checkout`");
    // 诚实未接通文案保持在场(不因正显示块而消失)。
    expect(desk).toContain("支付接续尚未开通，暂不能确认下单");
  });
});
