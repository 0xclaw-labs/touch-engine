"use client";

// HUI-3562 核销卡:扫券码(或从记录进入)后的单张权益卡。
// 纪律:券码默认掩码,店员点开才看;核销是独立动作(1671 单事务);
// 另一端已处理时显示处理人+时间;更正撤销仅 org_owner,原因必填,保留原记录。

import { useState } from "react";
import { benefitStatusText, evidenceText, maskCode } from "@/lib/reward-ops";
import { redeemRecordText } from "@/lib/service-desk";

export interface RedemptionBenefit {
  id: string;
  campaign_id?: string;
  status: string;
  evidence: string;
  code?: string;
  store_id?: string;
  participation_id: string;
  issued_at?: string;
  issued_by?: string;
  redeemed_at?: string;
  redeemed_by?: string;
  reverse_reason?: string;
}

export function RedemptionCardView(props: {
  benefit: RedemptionBenefit;
  reverseAllowed: boolean;
  busy: boolean;
  onRedeem: (storeId: string) => void;
  onReverse: (reason: string) => void;
  onClose: () => void;
}) {
  const { benefit, reverseAllowed, busy, onRedeem, onReverse, onClose } = props;
  const [revealed, setRevealed] = useState(false);
  const [reason, setReason] = useState("");
  const [reverseOpen, setReverseOpen] = useState(false);

  const redeemable = benefit.status === "issued";
  const record = redeemRecordText(benefit);

  return (
    <section aria-label="核销卡" className="tk-card" data-field="redemption-card" data-benefit-id={benefit.id}>
      <h2 className="tk-subtitle">核销卡</h2>
      <p className="tk-note" data-field="redemption-status">
        {benefitStatusText(benefit.status)} · {evidenceText(benefit.evidence)} · 参与 {benefit.participation_id}
        {benefit.store_id ? ` · 门店 ${benefit.store_id}` : ""}
      </p>
      <p className="tk-note" data-field="redemption-code">
        券码 {revealed ? (benefit.code || "（无码：现场赠品/折扣）") : maskCode(benefit.code ?? "")}
      </p>
      {benefit.code && !revealed ? (
        <button className="tk-quiet" type="button" data-action="reveal-code" onClick={() => setRevealed(true)}>
          显示券码
        </button>
      ) : null}
      {benefit.issued_at ? (
        <p className="tk-note" data-field="redemption-issued">
          发放时间 {benefit.issued_at} · 发放人 {benefit.issued_by || "未知"}
        </p>
      ) : null}
      <p className="tk-note" data-field="redemption-record">{record}</p>

      {redeemable ? (
        <button
          className="tk-button"
          type="button"
          data-action="redeem"
          disabled={busy}
          onClick={() => onRedeem(benefit.store_id ?? "")}
        >
          {busy ? "提交中…" : "确认核销（顾客已使用）"}
        </button>
      ) : null}

      {reverseAllowed && benefit.status !== "reversed" ? (
        reverseOpen ? (
          <div data-field="reverse-form">
            <label className="tk-label">
              更正撤销原因（必填；保留原发放事实，不删账）
              <input className="tk-input" data-field="reverse-reason" value={reason} onChange={(event) => setReason(event.target.value)} />
            </label>
            <button
              className="tk-quiet"
              type="button"
              data-action="reverse"
              disabled={busy}
              onClick={() => {
                const trimmed = reason.trim();
                if (!trimmed) return;
                onReverse(trimmed);
                setReverseOpen(false);
              }}
            >
              提交更正撤销
            </button>
            <p className="tk-note">没有原因就不撤销。</p>
          </div>
        ) : (
          <button className="tk-quiet" type="button" data-action="reverse-open" onClick={() => setReverseOpen(true)}>
            更正撤销（仅总部）
          </button>
        )
      ) : null}
      {benefit.reverse_reason ? (
        <p className="tk-note" data-field="redemption-reversed">撤销原因：{benefit.reverse_reason}（原发放事实保留）</p>
      ) : null}

      <p className="tk-gap">
        <button className="tk-quiet" type="button" data-action="close-redemption" onClick={onClose}>关闭核销卡</button>
      </p>
    </section>
  );
}
