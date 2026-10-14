import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const page = read("app/work/rewards/page.tsx");

describe("/work/rewards 真实管理页", () => {
  it("挂上真实账本端口,不再只是只读说明页", () => {
    for (const port of [
      "reward-inventory",
      "reward-benefits",
      "reward-summary",
      "reward-review-requests",
      "reward-confirm",
    ]) {
      expect(page).toContain(port);
    }
    expect(page).toContain("data-action=\"confirm-reward\"");
    expect(page).toContain("data-action=\"redeem\"");
    expect(page).toContain("data-action=\"reverse\"");
    expect(page).toContain("data-action=\"pause-batch\"");
    expect(page).toContain("data-action=\"create-batch\"");
  });

  it("敏感券码默认掩码,点开才显示;错误文案不回显券码", () => {
    expect(page).toContain("maskCode(");
    expect(page).toContain('data-action="reveal-code"');
    // 页面不出现把完整码直接铺出来的插值。
    expect(page).not.toMatch(/\{ben\.code\}/);
  });

  it("预算与更正归 org_owner;agent 只读;店员确认走授权角色判断", () => {
    expect(page).toContain("canManageInventory(session.role)");
    expect(page).toContain("canReverseRewards(session.role)");
    expect(page).toContain("canConfirmRewards(session.role)");
    expect(page).toContain("更正撤销必须注明原因");
  });

  it("诚实文案纪律保留:未知不是已发放、发放核销是两条事实", () => {
    expect(page).toContain("未知奖励不是已发放");
    expect(page).toContain("发放与核销是两条事实");
    expect(page).toContain("不删账");
    expect(page).toContain("没有事实不写成功");
    // 保留对访客侧只读状态的诚实提示。
    expect(page).toContain("rewardNotice(reward)");
  });

  it("确认表单只提供批次允许的证据;官方回执不靠手填", () => {
    expect(page).toContain("selected?.allowed_evidence");
    expect(page).toContain("官方证据需要服务端已有的官方事实引用");
  });

  it("导入券码池建批次时给码;码不进任何日志字段", () => {
    expect(page).toContain("batch-codes");
    expect(page).toContain("imported_code");
  });
});
