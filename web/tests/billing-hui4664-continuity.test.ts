import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  agencyTodoCategoryText,
  canContinueCheckout,
  checkoutUnavailableText,
  lifecycleStageText,
  milestoneStatusText,
} from "../src/lib/billing";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = (p: string) => readFileSync(path.join(webRoot, p), "utf8");

describe("HUI-4664 生命周期分态与里程碑文案", () => {
  it("清晰区分待支付、已付待开动、开通同步中、已生效、硬件待验收与同单退款", () => {
    expect(lifecycleStageText("pending_payment")).toContain("待支付");
    expect(lifecycleStageText("payment_paid")).toContain("已确认支付");
    expect(lifecycleStageText("service_activating")).toContain("切勿重复购买");
    expect(lifecycleStageText("service_active")).toContain("已生效");
    expect(lifecycleStageText("hardware_pending")).toContain("已付款不等于实卡已交付");
    expect(lifecycleStageText("refund_in_progress")).toContain("禁止再次付款");
  });

  it("里程碑与代理待办分类文案准确无歧义", () => {
    expect(milestoneStatusText("done")).toBe("已完成");
    expect(milestoneStatusText("in_progress")).toBe("进行中");
    expect(milestoneStatusText("stale")).toContain("重新核对");
    expect(agencyTodoCategoryText("pending_onboarding")).toBe("待开通");
    expect(agencyTodoCategoryText("missing_config")).toBe("补配置");
    expect(agencyTodoCategoryText("hardware_acceptance")).toBe("实卡验收");
    expect(agencyTodoCategoryText("renewal")).toBe("续费");
    expect(agencyTodoCategoryText("exception")).toBe("异常");
  });

  it("权威 code_url 满足真值门; sync_pending / price_changed / order_already_paid 诚实分诊", () => {
    expect(
      canContinueCheckout({
        available: true,
        app_id: "",
        launch_target_id: "ti-touch-engine-billing",
        checkout_ref: "ck_live_1",
        code_url: "weixin://wxpay/bizpayurl?pr=live1",
      }),
    ).toBe(true);
    expect(
      checkoutUnavailableText({
        available: false,
        app_id: "",
        launch_target_id: "",
        sync_pending: true,
        reason: "not_awaiting_payment",
      }),
    ).toContain("无需重复付款");
    expect(
      checkoutUnavailableText({
        available: false,
        app_id: "",
        launch_target_id: "",
        reason: "price_changed",
      }),
    ).toContain("价格目录");
    expect(
      checkoutUnavailableText({
        available: false,
        app_id: "",
        launch_target_id: "",
        reason: "order_already_paid",
      }),
    ).toContain("禁止再次付款");
  });
});

describe("HUI-4664 A1 / M12 前端消费接线", () => {
  it("M12 消费 billing/continuity、订单 milestones 与 orders/{id}/checkout Mint 端点", () => {
    const desk = read("src/components/work/billing/billing-desk.tsx");
    expect(desk).toContain('session.api("GET", "billing/continuity")');
    expect(desk).toContain("billing/orders/${id}/checkout");
    expect(desk).toContain('data-testid="billing-readiness-gaps"');
    expect(desk).toContain('data-testid="order-milestones"');
  });

  it("A1 消费 /api/agency/todos 且零示例任务", () => {
    const agency = read("src/components/work/agency-console/agency-console.tsx");
    expect(agency).toContain("/api/agency/todos");
    expect(agency).toContain('data-section="agency-pending-tasks"');
    expect(agency).toContain("不生成示例任务");
    expect(agency).toContain("session.clearWorkspace");
  });
});
