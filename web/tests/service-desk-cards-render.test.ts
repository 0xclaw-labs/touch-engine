import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ParticipationCardView } from "../src/components/work/service-desk/participation-card";

const props = {
  card: { id: "pt_original", campaign_id: "cmp_original", store_id: "sto_original", rule_version: 1, confirmation_allowed: true },
  campaignTitle: "本店活动",
  batches: [{ id: "rinv_original", name: "原赠品", kind: "gift_discount", benefit_slot: "gift", rule_version: 1, allowed_evidence: ["staff_observed_submission"], status: "active", available_qty: 5, total_qty: 5, cost_minor: 0, cost_recorded: true }],
  canConfirm: true,
  busy: false,
  onConfirm: () => {}, onNotFit: () => {}, onClose: () => {},
};

describe("真实参与卡渲染保持独立事实与原操作", () => {
  it("预留仍显示明确确认入口，已发放只显示已有处理", () => {
    const reserved = renderToStaticMarkup(createElement(ParticipationCardView, { ...props, benefit: { id: "rben_original", inventory_id: "rinv_original", status: "reserved", evidence: "" } }));
    expect(reserved).toContain('data-action="confirm-issue"');
    expect(reserved).toContain('data-action="confirm-fulfill"');
    expect(reserved).not.toContain('data-field="card-done"');
    expect(reserved).toContain("实际成本 0.00 元");
    const issued = renderToStaticMarkup(createElement(ParticipationCardView, { ...props, benefit: { id: "rben_original", status: "issued", evidence: "staff_observed_submission" } }));
    expect(issued).toContain('data-field="card-done"');
    expect(issued).not.toContain('data-action="confirm-issue"');
  });
  it("丢响应时只允许重试同一次确认，不能换证据或改变是否现场兑现", () => {
    const pending = renderToStaticMarkup(createElement(ParticipationCardView, { ...props, pendingConfirmation: { participation_id: "pt_original", event_id: "original_operation", evidence: "staff_observed_submission", fulfill_now: false } }));
    expect(pending).toContain('data-action="retry-confirmation"');
    expect(pending).not.toContain('data-action="confirm-issue"');
    expect(pending).not.toContain('data-action="confirm-fulfill"');
    expect(pending).not.toContain('data-field="evidence-group"');
  });
  it("服务端明确禁止新发放时只展示原因", () => {
    const html = renderToStaticMarkup(createElement(ParticipationCardView, { ...props, canConfirm: false, card: { ...props.card, confirmation_allowed: false, confirmation_blocked_reason: "活动已暂停，停止新发放" } }));
    expect(html).toContain("活动已暂停，停止新发放");
    expect(html).not.toContain('data-action="confirm-issue"');
  });
});
