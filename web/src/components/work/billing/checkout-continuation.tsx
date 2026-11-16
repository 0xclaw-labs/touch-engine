"use client";

// HUI-4664 原单接续正显示面(ADV-9 FIX-C/G1,PR-3 前端消费范围)。
//
// 纪律(R3 PR-3 / INT5 G1 定谳):
//   - 只消费续付 API 真值:条目来自 handoff checkout 投影(服务端装配),
//     浏览器声明一律不参与;可续付当且仅当 canContinueCheckout 真值门成立
//     (available + checkout_ref + checkout_url 三件套,缺一即视为未接通);
//   - 续付按钮跳 checkout_url(公共结算侧 URL,服务端响应即时给出;本仓
//     永不发明跳转地址,也永不把跳转当支付证明——状态只由 authority 事实
//     驱动,回来仍走只读回查);
//   - 无引用/未接通=不出块:页面保持既有诚实文案(不虚假承诺,也不制造
//     「可续付」存在性 oracle)。

import React from "react";
import { canContinueCheckout, formatAmount, type CheckoutRef } from "@/lib/billing";

// One resumable production continuation as discovered from the read faces:
// the checkout projection of one content-production handoff.
export interface CheckoutContinuationEntry {
  storeId: string;
  handoffId: string;
  checkout: CheckoutRef;
}

// collectResumableCheckouts applies the truth gate client-side (mirror of the
// server's own available semantics; the gate is the only adjudicator — the
// page never re-adjudicates money facts).
export function collectResumableCheckouts(entries: CheckoutContinuationEntry[]): CheckoutContinuationEntry[] {
  return entries.filter((entry) => canContinueCheckout(entry.checkout));
}

export function CheckoutContinuationBlock({ entries }: { entries: CheckoutContinuationEntry[] }) {
  const resumable = collectResumableCheckouts(entries);
  if (resumable.length === 0) return null;
  return (
    <section className="tk-section" data-testid="billing-continuations" aria-label="可续付订单">
      <h3>可续付</h3>
      <ul data-testid="continuation-list">
        {resumable.map((entry) => (
          <li key={entry.checkout.checkout_ref} data-testid="continuation-row">
            <span>制作接续待支付</span>
            {entry.checkout.checkout_ref ? <span> · 引用 {entry.checkout.checkout_ref}</span> : null}
            {typeof entry.checkout.amount_minor === "number"
              ? <span> · {formatAmount(entry.checkout.amount_minor, entry.checkout.currency || "CNY")}</span>
              : <span> · 金额以收银台结算页为准</span>}
            <a className="tk-button" data-action="continue-checkout" href={entry.checkout.checkout_url}>
              去续付
            </a>
          </li>
        ))}
      </ul>
      <p>续付是原单接续,不是新购;付款状态以服务端事实为准,页面只读回查。</p>
    </section>
  );
}
