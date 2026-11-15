import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { AnalyticsReport } from "../src/components/work/analytics/analytics-view";
import { parseOperationsResponse } from "../src/lib/analytics";
import paymentFacts from "../../server/internal/httpapi/testdata/analytics-payment-facts.json";

const wire = () => ({
  window: { start: "2026-11-14T00:00:00Z", end: "2026-11-14T12:00:00Z" },
  scope: { tenant_id: "empty" }, filter_echo: null, funnel: [], inventory: null,
  evidence: { by_class: {} }, cost: {}, anomalies: {}, notes: {}, dimensions: {},
});
function render(value: unknown) {
  return renderToStaticMarkup(createElement(AnalyticsReport, {
    data: parseOperationsResponse(value, "empty"), catalog: null, catalogNote: "目录读取失败，请重试。", draft: {},
    onDraft: vi.fn(), onApply: vi.fn(), onReset: vi.fn(), onRefresh: vi.fn(),
  }));
}

describe("HUI-4665 真实渲染", () => {
  it("逐条说明内容版本筛选，不把权益和发布快照混成已收窄总数", () => {
    const html = render({ ...wire(), filter_echo: { content_pack_version: "v1" }, evidence_layers: { layers: [{ key: "staff_confirmed", available: true, counts: [
      { key: "staff_confirm", label: "交接核验", value: 1, applied_filters: ["content_pack_version"] },
      { key: "staff_benefit_participations", label: "权益核验", value: 2, applied_filters: [] },
    ] }] } });
    expect(html).toContain('data-field="line-applied">本项按 内容包版本 收窄');
    expect(html).toContain('data-field="line-not-applied">本项未生效：内容包版本');
    expect(html).toContain('data-field="evidence-filter-limit"');
  });
  it("受限门店不凭本店占用推算共享批次可用量，也不把空门店称全店共享", () => {
    const html = render({ ...wire(), inventory: [{ inventory_id: "i1", name: "券", total_qty: 100, reserved: 1, issued: 0, redeemed: 0, available: 99, available_unknown: true }] });
    expect(html).toContain("可用 未知");
    expect(html).toContain("当前门店范围无法证明全批次占用");
    expect(html).not.toContain("全店共享");
    expect(html).not.toContain("可用 99");
  });
  it("旧空库 JSON 能完整渲染，缺增量事实显示未知", () => {
    const html = render(wire());
    expect(html).toContain("没有库存批次");
    expect(html).toContain("门店服务记录");
    expect(html).toContain("未知");
    expect(html).not.toContain("NaN");
    expect(html).toContain("真实成交收入、完整费用和因果归因证据尚未接入");
  });
  it("未录成本批次不把部分和显示为完整的零成本", () => {
    const html = render({ ...wire(), cost: { reward_cost_minor: 0, reward_redeemed_cost_minor: 0, unrecorded_batches: 1 } });
    expect(html).toContain("未知（本期已发权益包含未录成本，或完整性尚未读到）");
    expect(html).not.toContain("奖励实际兑现成本(已发):0.00");
  });
  it("费用完整性由窗口内冻结权益判断，当前库存未知不污染已知权益成本", () => {
    const html=render({...wire(),cost:{reward_cost_minor:0,reward_redeemed_cost_minor:0,unrecorded_batches:2,reward_cost_unrecorded:0,redeemed_cost_unrecorded:0}});
    expect(html).toContain("奖励实际兑现成本(已发):0.00");
    expect(html).toContain("未录成本的批次:2");
  });
  it("账单无权时只呈现授权下一步，不展示金额", () => {
    const html = render({ ...wire(), cost_reconciliation: { availability: "forbidden", note: "请联系组织管理员查看费用。" } });
    expect(html).toContain("请联系组织管理员查看费用");
    expect(html).not.toContain('data-field="recon-ai"');
    expect(html).toContain('data-section="leads"');
  });
  it("已支付但开通失败仍保留付款事实，订阅退款与硬件退款分列并显示币种", () => {
    // 与真实 HTTP 工程测试共用手工复算期望，不从被测聚合生成期望。
    const html = render({ ...wire(), cost_reconciliation: paymentFacts });
    expect(html).toContain('data-field="recon-subscription">');
    expect(html).toContain('data-field="recon-subscription-refunds">');
    expect(html).toContain('data-field="recon-hardware-refunds">');
    expect(html).toContain("币种：CNY");
    expect(html).toContain("订阅实付(支付事实订单投影):12.00");
    expect(html).toContain("订阅退款:3.00");
    expect(html).toContain("硬件支出(支付事实订单投影):20.00");
    expect(html).toContain("硬件退款:5.00");
    expect(html).not.toContain("失败不是支出");
  });
  it("付款或退款缺币种时不把裸金额呈现为完整费用", () => {
    const html = render({ ...wire(), cost_reconciliation: {
      subscription_available: true, subscription_paid_minor: 12345, subscription_paid_orders: 1,
      hardware_available: true, hardware_paid_minor: 54321, hardware_paid_orders: 1,
      hardware_refund_minor: 9999, hardware_refunds: 1,
    } });
    expect(html).not.toContain("123.45");
    expect(html).not.toContain("543.21");
    expect(html).not.toContain("99.99");
    expect(html).toContain('data-field="recon-subscription-unknown"');
    expect(html).toContain('data-field="recon-hardware-unknown"');
  });
  it("同会话多份证据分列，不把记录相加为成功人数", () => {
    const html = render({ ...wire(), evidence: { by_class: { staff_observed_submission: 3, staff_verified_post: 2, official_publish_confirmed: 1 }, distinct_participations: 3, official_attempts: 1 } });
    expect(html).toContain('data-evidence="staff_observed_submission"');
    expect(html).toContain('data-field="evidence-distinct">3');
    expect(html).toContain("各项不相加为“成功总数”");
    expect(html).toContain("0 条回执不证明平台已授权或实际零发布");
  });
  it("无法确定退款发生时间时仍展示问题，不用零笔或部分金额代替合计", () => {
    const html = render({ ...wire(), cost_reconciliation: {
      subscription_refunds: 0, subscription_refund_time_unknown: 1,
      hardware_refunds: 2, hardware_refund_time_unknown: 1,
    } });
    expect(html).toContain('data-field="recon-subscription-refund-time-unknown"');
    expect(html).toContain('data-field="recon-hardware-refund-time-unknown"');
    expect(html).toContain("有 1 笔退款无法确认发生时间，本窗口退款合计未知");
    expect(html).toContain("已确认落在本窗口的退款为 2 笔");
    expect(html).not.toContain("订阅退款:0.00");
  });
  it("付款时间不完整时说明未知合计及已确认单数", () => {
    const html = render({ ...wire(), cost_reconciliation: {
      subscription_paid_orders: 1, subscription_paid_time_unknown: 2,
      hardware_paid_orders: 0, hardware_paid_time_unknown: 1,
    } });
    expect(html).toContain('data-field="recon-subscription-paid-time-unknown"');
    expect(html).toContain('data-field="recon-hardware-paid-time-unknown"');
    expect(html).toContain("有 2 单付款无法确认发生时间，本窗口付款合计未知");
    expect(html).toContain("已确认落在本窗口的付款为 1 单");
  });
});

describe("不可用事实与真实零", () => {
  it("不可用的证据层即使带残留数值，也不当成门店事实", () => {
    const html = render({ ...wire(), evidence_layers: { layers: [{ key: "action_clicked", available: false, counts: [{ key: "service_opens", value: 99 }] }] } });
    expect(html).not.toContain('data-field="service-opens">99');
    expect(html).toContain('data-field="service-opens">未知');
  });
  it("活动七项为零不否定门店服务、线索或费用事实", () => {
    const html = render({ ...wire(), funnel: [{ key: "visits", available: true, value: 0 }], evidence_layers: { layers: [{ key: "action_clicked", available: true, counts: [{ key: "service_opens", value: 2 }] }] } });
    expect(html).not.toContain("这个窗口还没有发生可统计的事实");
    expect(html).toContain('data-field="service-opens">2');
  });
});

it("平台筛选不能假装收窄无平台维度的授权线索", () => {
  const html = render({ ...wire(), filter_echo: { platform: "douyin", store_id: "store-1" } });
  expect(html).toContain('data-field="leads-filter-limit">本区块未生效：平台');
  expect(html).not.toContain('data-field="leads-filter-limit">本区块未生效：门店');
});
