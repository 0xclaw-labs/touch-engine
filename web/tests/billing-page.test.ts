// HUI-4591 M12 账单页与文案收敛测试。
// 纪律:薄壳页面零 census 标记;四态/五状态/发票/AI 状态文案可区分;
// 响应永不出现 balance/wallet 文案;权限判定对齐 4593 冻结矩阵。

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  aiUsageStateText,
  canManageBilling,
  formatAmount,
  HARDWARE_STEPS,
  invoiceStatusText,
  orderActionText,
  orderStatusText,
  planCodeText,
  stepText,
  storeQuotaText,
  subscriptionStatusText,
} from "../src/lib/billing";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = (p: string) => readFileSync(path.join(webRoot, p), "utf8");

describe("/work/billing 页面结构(薄壳)", () => {
  it("页面是薄壳,本体在 billing-desk 组件", () => {
    const page = read("src/app/work/billing/page.tsx");
    expect(page).toContain('from "@/components/work/billing/billing-desk"');
    expect(page).toContain("BillingDesk />");
    expect(page).not.toMatch(/<button|<input|<table|style=/);
  });

  it("工作台导航含账单入口", () => {
    const frame = read("src/components/work/work-frame.tsx");
    expect(frame).toContain('href: "/work/billing"');
    expect(frame).toContain('label: "账单"');
  });

  it("四页签齐全且订单回查是只读刷新(不当支付证明)", () => {
    const desk = read("src/components/work/billing/billing-desk.tsx");
    for (const label of ["套餐与门店额度", "订单与支付", "AI 用量", "发票与退款"]) {
      expect(desk).toContain(label);
    }
    expect(desk).toContain('data-action="retry"');
    expect(desk).toContain("回查");
    // 收银台跳转复用 eco-nav switch,不自造 URL
    expect(desk).toContain('"eco-nav/switch"');
    expect(desk).toContain("生态收银台未接入");
    // 到期语义(F18/T16)
    expect(desk).toContain("订阅到期只暂停新增收费能力");
  });

  it("账单响应不出现余额字段文案", () => {
    const lib = read("src/lib/billing.ts");
    expect(lib).not.toMatch(/balance/);
    expect(lib).not.toMatch(/wallet/);
  });
});

describe("文案收敛", () => {
  it("四态开通可区分(F16)", () => {
    expect(subscriptionStatusText("processing")).toContain("处理中");
    expect(subscriptionStatusText("opening")).toContain("开通中");
    expect(subscriptionStatusText("active")).toContain("已开通");
    expect(subscriptionStatusText("failed")).toContain("失败");
    expect(subscriptionStatusText("expired")).toContain("已到期");
    expect(subscriptionStatusText("expired")).toContain("核销");
    expect(subscriptionStatusText("unconfirmed")).toContain("未确认");
  });

  it("订单四态+撤单与动作文案", () => {
    expect(orderStatusText("processing")).toBe("处理中");
    expect(orderStatusText("active")).toBe("已开通");
    expect(orderStatusText("failed")).toBe("失败");
    expect(orderStatusText("cancelled")).toBe("已撤单");
    expect(orderActionText("new")).toBe("新购");
    expect(orderActionText("renew")).toBe("续费");
    expect(orderActionText("expand")).toBe("扩店");
    expect(orderActionText("purchase")).toBe("购买");
    expect(planCodeText("store.plan.month")).toContain("月付");
    expect(planCodeText("store.plan.year")).toContain("年付");
  });

  it("硬件五状态步骤齐全且未发生不冒充完成", () => {
    expect(HARDWARE_STEPS.map((s) => s.label)).toEqual(["付款", "出库", "写卡", "回读", "现场验收"]);
    expect(stepText(undefined)).toBe("未发生");
    expect(stepText({ state: "pending", operator: "", occurred_at: "", device_ref: "" })).toBe("未发生");
    expect(stepText({ state: "done", operator: "仓管老周", occurred_at: "2026-06-03T00:00:00Z", device_ref: "dev_1" })).toContain("仓管老周");
  });

  it("AI 用量与发票状态文案", () => {
    expect(aiUsageStateText("confirmed")).toContain("未消费");
    expect(aiUsageStateText("consumed")).toBe("已消费");
    expect(aiUsageStateText("refunded")).toBe("已退款");
    expect(aiUsageStateText("failed")).toBe("失败");
    expect(invoiceStatusText("none")).toBe("未开票");
    expect(invoiceStatusText("pending")).toBe("开票中");
    expect(invoiceStatusText("issued")).toBe("已开票");
    expect(invoiceStatusText("void")).toBe("已作废");
  });

  it("金额与门店额度展示", () => {
    expect(formatAmount(990000, "CNY")).toBe("9900.00 CNY");
    expect(storeQuotaText(3, 2)).toContain("2 家 / 档位 3 家");
    expect(storeQuotaText(0, 4)).toContain("无门店档位事实");
    expect(storeQuotaText(3, 5)).toContain("超出档位");
  });
});

describe("下单资格判定(4593 冻结矩阵)", () => {
  it("owner 恒可;staff/manager 不可;agent 按 billing:manage_payer", () => {
    expect(canManageBilling("org_owner", null)).toBe(true);
    expect(canManageBilling("staff", null)).toBe(false);
    expect(canManageBilling("store_manager", null)).toBe(false);
    // access/effective 可用时以权限为准(agent renewal+payer grant 折算成
    // billing:manage_payer;无 grant 的 agent 是空数组 → 不可)
    expect(canManageBilling("agent", ["billing:view", "billing:manage_payer"])).toBe(true);
    expect(canManageBilling("agent", ["billing:view"])).toBe(false);
    expect(canManageBilling("agent", [])).toBe(false);
    // 权限面可用时角色不再放大
    expect(canManageBilling("org_owner", [])).toBe(false);
  });
});
