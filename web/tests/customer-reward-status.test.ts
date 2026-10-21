import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 T5 屏3「领取或请店员确认」:源码级断言。
// 红线:无参与不渲染;「我已完成」只请求核验;已发放才显示券/码且掩码;
// 已兑现不再引导重复领券;本系统已确认发放不被外部 unknown 拖成「奖励未知」。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const component = codeOf(src("components/customer/RewardStatus.tsx"));

describe("屏3 渲染门:无参与不渲染", () => {
  it("participation 为空返回 null(区块隐藏)", () => {
    expect(component.includes("if (!participation)")).toBe(true);
    expect(component.includes("return null")).toBe(true);
  });

  it("有 data-testid=reward-status 分区", () => {
    expect(component.includes('data-testid="reward-status"')).toBe(true);
  });
});

describe("屏3 人工路径:参与码与请店员确认", () => {
  it("展示参与编号;核验码以文本出示，不再渲染不存在的 /p 链接", () => {
    expect(component.includes("参与编号")).toBe(true);
    expect(component.includes("resumeCredential")).toBe(true);
    expect(component.includes("credentialIssueNotice")).toBe(true);
    expect(component).not.toContain("reward-qr");
    expect(component).not.toContain("/p/ptx_");
    expect(component.includes("reward-token")).toBe(true);
    expect(component.includes("核验码")).toBe(true);
  });

  it("「请店员确认」人工路径文案存在", () => {
    expect(component.includes("请店员确认")).toBe(true);
  });

  it("「我已完成」只请求核验:文案说明确认由店员做出", () => {
    expect(component.includes("我已完成")).toBe(true);
    expect(component.includes("不会自己让奖励通过")).toBe(true);
  });
});

describe("屏3 奖励状态:真实进度,不冒充", () => {
  it("状态标签走 statusLabel(不抹平成成功)", () => {
    expect(component.includes("statusLabel")).toBe(true);
  });

  it("奖励文案走 rewardCardCopy(已发放优先,掩码)", () => {
    expect(component.includes("rewardCardCopy")).toBe(true);
  });

  it("店员已确认完成但无券码记录 → 现场出示为准,不编造券码", () => {
    expect(component.includes("staffFulfilledCopy")).toBe(true);
  });

  it("reward-summary 可选(off 时仍显示领取条件,不显示编造库存)", () => {
    expect(component.includes("reward-summary")).toBe(true);
    expect(component.includes("stockLine")).toBe(true);
  });

  it("已兑现不再引导重复领券(动作按钮以 redeemed 门控收起;文案在 lib 唯一出口)", () => {
    expect(component.includes("const redeemed")).toBe(true);
    expect(component.includes("!redeemed && (")).toBe(true);
  });
});

describe("屏3 安全红线", () => {
  it("凭据不落 localStorage;媒体/凭据原始 URL 不进 DOM 文本", () => {
    expect(component.includes("localStorage")).toBe(false);
    expect(component.includes("sessionStorage")).toBe(false);
    expect(component.includes("{cred.token}")).toBe(false);
  });

  it("只读自身参与(reward-summary 公共面),不发店员/商家会话请求", () => {
    expect(component.includes("/api/participations/")).toBe(true);
    expect(component.includes("staff/")).toBe(false);
    expect(component.includes("reward-confirm")).toBe(false);
  });
});
