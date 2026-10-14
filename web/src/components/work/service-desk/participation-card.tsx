"use client";

// HUI-3562 参与卡:店员扫/手输凭据后打开的「本次参与」卡。
// 纪律(票面):卡面只有门店/活动、内容预览、此次规则、奖励及期限、
// 已有处理;证据选项只来自活动已冻结规则(批次快照),official 永远只读;
// 「暂不符合」零写入(不造第二本账),顾客补做后可再次出示。

import { useMemo, useState } from "react";
import { benefitCopy, statusLabel } from "@/lib/participation";
import { evidenceText, faceAndCostText, inventoryKindText, slotText } from "@/lib/reward-ops";
import {
  confirmDisabledReason,
  confirmRequestBody,
  evidenceChoices,
  matchBatchesForCard,
  type ParticipationCard as Card,
  type ServiceBatch,
} from "@/lib/service-desk";

const NOT_FIT_REASONS = [
  "顾客还没有完成发布操作",
  "发布内容不符合活动要求",
  "顾客暂时放弃，之后再来",
];

export interface ParticipationBenefit {
  id: string;
  status: string;
  evidence: string;
  code?: string;
  store_id?: string;
  redeemed_at?: string;
  redeemed_by?: string;
}

export function ParticipationCardView(props: {
  card: Card;
  campaignTitle: string;
  batches: ServiceBatch[];
  benefit?: ParticipationBenefit;
  canConfirm: boolean;
  busy: boolean;
  onConfirm: (body: Record<string, unknown>, evidence: string) => void;
  onNotFit: (reason: string) => void;
  onClose: () => void;
}) {
  const { card, campaignTitle, batches, benefit, canConfirm, busy, onConfirm, onNotFit, onClose } = props;
  const candidates = useMemo(
    () => matchBatchesForCard(batches, { rule_version: card.rule_version, store_id: card.store_id }),
    [batches, card.rule_version, card.store_id],
  );
  const [batchId, setBatchId] = useState(candidates[0]?.id ?? "");
  const [evidence, setEvidence] = useState("");
  const [note, setNote] = useState("");
  const [notFitOpen, setNotFitOpen] = useState(false);
  const [notFitReason, setNotFitReason] = useState(NOT_FIT_REASONS[0]);

  const batch = candidates.find((b) => b.id === batchId) ?? candidates[0];
  const choices = evidenceChoices(batch?.allowed_evidence ?? []);
  const disabledReason = confirmDisabledReason(choices, evidence);
  const reservation = benefitCopy(card.benefit_reservation_state);

  const shortId = card.id.length > 12 ? card.id.slice(0, 12) + "…" : card.id;
  const evidenceLabel = choices.selectable.find((c) => c.value === evidence)?.label ?? "（未选证据）";

  const submit = (fulfillNow: boolean) => {
    if (!batch || !evidence || disabledReason) return;
    onConfirm(
      confirmRequestBody({
        participationId: card.id,
        benefitSlot: batch.benefit_slot,
        ruleVersion: batch.rule_version,
        evidence,
        storeId: card.store_id,
        fulfillNow,
        note: note.trim(),
      }),
      evidence,
    );
  };

  return (
    <section aria-label="参与卡" className="tk-card" data-field="participation-card" data-participation-id={card.id}>
      <h2 className="tk-subtitle">本次参与</h2>
      <p className="tk-note" data-field="card-context">
        {campaignTitle} · 门店 {card.store_id} · 状态 {statusLabel(card.status)} · 规则 v{card.rule_version ?? "?"}
        {card.link_code ? ` · 入口码 ${card.link_code}` : ""}
      </p>
      {card.content_pack_version ? (
        <p className="tk-note" data-field="card-content">内容版本 {card.content_pack_version}</p>
      ) : null}
      {reservation ? (
        <p className="tk-note" data-field="card-reservation">{reservation}</p>
      ) : null}
      {card.expires_at ? (
        <p className="tk-note" data-field="card-window">参与有效期至 {card.expires_at}</p>
      ) : null}

      <section aria-label="已有处理">
        <h3 className="tk-subtitle">已有处理</h3>
        {(card.attempts ?? []).length === 0 ? (
          <p className="tk-note" data-field="attempts-empty">还没有发布尝试记录。</p>
        ) : (
          <ul className="tk-list" data-field="attempts-list">
            {(card.attempts ?? []).map((raw) => {
              const a = raw as { id?: string; platform?: string; handoff_method?: string; status?: string; rule_version?: number };
              return (
                <li key={a.id}>
                  尝试 {a.platform || "（平台未记录）"} · {a.handoff_method} · {a.status} · 规则 v{a.rule_version}
                </li>
              );
            })}
          </ul>
        )}
        {benefit ? (
          <p className="tk-note" data-field="card-benefit">
            已有关联权益：{benefit.status} · {evidenceText(benefit.evidence)}
          </p>
        ) : (
          <p className="tk-note" data-field="card-benefit-empty">还没有权益记录。没有事实不写已发放。</p>
        )}
      </section>

      {benefit ? (
        <p className="tk-note" data-field="card-done">这张卡已经有处理结果，请回到「已发放/已核销记录」继续核销或查看。</p>
      ) : canConfirm ? (
        <section aria-label="确认与发放">
          <h3 className="tk-subtitle">确认符合条件</h3>
          {candidates.length === 0 ? (
            <p className="tk-note" data-field="no-batch">
              这场活动在规则 v{card.rule_version ?? "?"} 下没有可用奖励批次。空不是不限量，不能确认发放。
            </p>
          ) : (
            <>
              <label className="tk-label">
                奖励批次（按活动冻结规则）
                <select
                  className="tk-select"
                  data-field="card-batch"
                  value={batch?.id ?? ""}
                  onChange={(event) => {
                    setBatchId(event.target.value);
                    setEvidence("");
                  }}
                >
                  {candidates.map((b) => (
                    <option key={b.id} value={b.id}>
                      {b.name} · {inventoryKindText(b.kind)} · slot {slotText(b.benefit_slot)} · 余量 {b.available_qty}/{b.total_qty}
                      {b.valid_until ? ` · 有效期至 ${b.valid_until}` : ""}
                    </option>
                  ))}
                </select>
              </label>
              {batch ? (
                <p className="tk-note" data-field="batch-rule">
                  此批次规则允许的证据：{choices.allowed.map(evidenceText).join("、")}。
                  {batch.kind === "gift_discount" ? " 现场赠品/折扣可现场兑现。" : ""}
                  {" "}{faceAndCostText(batch.face_value_minor ?? 0, batch.cost_minor ?? 0, batch.currency)}
                </p>
              ) : null}
              <div role="group" aria-label="选择证据" data-field="evidence-group">
                {choices.selectable.map((choice) => (
                  <label key={choice.value} className="tk-label">
                    <input
                      type="radio"
                      name="card-evidence"
                      value={choice.value}
                      checked={evidence === choice.value}
                      onChange={() => setEvidence(choice.value)}
                    />
                    {choice.label}
                  </label>
                ))}
                {choices.officialReadOnly ? (
                  <p className="tk-note" data-field="official-note">
                    系统已有平台官方确认：这是系统事实，店员不能代为确认，也不提供点选。
                  </p>
                ) : null}
              </div>
              <p className="tk-note" data-field="confirming-what">
                正在确认：{campaignTitle} 的参与 {shortId}，以「{evidenceLabel}」作为证据。
              </p>
              <label className="tk-label">
                说明（可空）
                <input className="tk-input" data-field="card-note" value={note} onChange={(event) => setNote(event.target.value)} />
              </label>
              <button
                className="tk-button"
                type="button"
                data-action="confirm-issue"
                disabled={busy || Boolean(disabledReason)}
                onClick={() => submit(false)}
              >
                {busy ? "提交中…" : "确认符合条件并发奖"}
              </button>
              {batch?.kind === "gift_discount" ? (
                <button
                  className="tk-button"
                  type="button"
                  data-action="confirm-fulfill"
                  disabled={busy || Boolean(disabledReason)}
                  onClick={() => submit(true)}
                >
                  {busy ? "提交中…" : "确认并现场兑现（发放+核销一次完成）"}
                </button>
              ) : null}
              {disabledReason ? (
                <p className="tk-note tk-danger" data-field="confirm-disabled">{disabledReason}</p>
              ) : null}
              {notFitOpen ? (
                <div data-field="not-fit-form">
                  <label className="tk-label">
                    暂不符合的原因
                    <select className="tk-select" data-field="not-fit-reason" value={notFitReason} onChange={(event) => setNotFitReason(event.target.value)}>
                      {NOT_FIT_REASONS.map((reason) => (
                        <option key={reason} value={reason}>{reason}</option>
                      ))}
                    </select>
                  </label>
                  <p className="tk-note" data-field="not-fit-note">
                    选「暂不符合」= 未发放任何奖励；顾客补做后可再次出示参与码，再扫即可回到这张卡。
                  </p>
                  <button
                    className="tk-quiet"
                    type="button"
                    data-action="not-fit"
                    onClick={() => {
                      onNotFit(notFitReason);
                      setNotFitOpen(false);
                    }}
                  >
                    记为暂不符合，关闭这张卡
                  </button>
                </div>
              ) : (
                <button className="tk-quiet" type="button" data-action="not-fit-open" onClick={() => setNotFitOpen(true)}>
                  暂不符合
                </button>
              )}
              <p className="tk-note">确认即按上方所选证据发放；发放与核销都记录在奖励账本，页面不写成功状态。</p>
            </>
          )}
        </section>
      ) : (
        <p className="tk-note" data-field="confirm-forbidden">当前角色不能确认发放，只能查看这张卡。</p>
      )}

      <p className="tk-gap">
        <button className="tk-quiet" type="button" data-action="close-card" onClick={onClose}>关闭这张卡</button>
      </p>
    </section>
  );
}
