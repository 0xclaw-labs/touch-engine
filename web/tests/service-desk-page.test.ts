import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-3562 /work/service 店员台:源码级断言(node 环境无 DOM)。
// 锁住:移动优先结构、证据白名单、official 只读、暂不符合零写入、
// 断网回查文案、本店作用域、撤销 owner 专属、census 账本同步。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const pageRel = "app/work/service/page.tsx";
const deskRel = "components/work/service-desk/service-desk.tsx";
const partRel = "components/work/service-desk/participation-card.tsx";
const redeemRel = "components/work/service-desk/redemption-card.tsx";

describe("census 账本同步", () => {
  it("/work/service 记入账本(review 模式),恰好一条", () => {
    const ledger = pageLedger().map((p) => p.route);
    expect(ledger.filter((r) => r === "/work/service")).toHaveLength(1);
    const decl = pageLedger().find((p) => p.route === "/work/service");
    expect(decl?.id).toBe("review");
  });

  it("页面壳是薄壳:零 raw 标记(census 干净)", () => {
    const page = read(pageRel);
    expect(page).toContain("<ServiceDesk />");
    expect(page).not.toMatch(/<button/);
    expect(page).not.toMatch(/<input/);
    expect(page).not.toMatch(/\bstyle\s*=/);
    expect(page).not.toMatch(/#[0-9A-Fa-f]{6}\b/);
  });
});

describe("店员台三区结构(移动优先)", () => {
  const desk = read(deskRel);

  it("只放高频动作:扫参与码、待确认、已发放/已核销记录", () => {
    expect(desk).toContain('data-section="scan"');
    expect(desk).toContain('data-section="review"');
    expect(desk).toContain('data-section="records"');
    expect(desk).toContain('data-field="scan-input"');
    expect(desk).toContain('data-action="open-participation"');
    expect(desk).toContain('data-action="open-coupon"');
  });

  it("相机不可用降级为手输,不装死", () => {
    expect(desk).toContain("BarcodeDetector");
    expect(desk).toContain('data-field="manual-entry"');
    expect(desk).toContain("extractHandoffToken");
    expect(desk).toContain("looksLikeHandoffToken");
  });

  it("店员默认自己门店:whoami 取 store_scope,读取按本店过滤", () => {
    expect(desk).toMatch(/session\.api\(\s*"GET",\s*"whoami"/);
    expect(desk).toContain("store_scope");
    expect(desk).toContain("ownStoreBenefits");
  });

  it("待确认走 1671 待核验队列;记录走权益列表", () => {
    expect(desk).toMatch(/reward-review-requests/);
    expect(desk).toMatch(/reward-benefits/);
    expect(desk).toContain("reward-inventory");
  });

  it("断网未知先回查;全程不出现「重新领」", () => {
    expect(desk).toContain("networkUnknownCopy");
    for (const rel of [deskRel, partRel, redeemRel]) {
      expect(read(rel)).not.toContain("重新领");
      expect(read(rel)).not.toContain("重新领一次");
    }
  });

  it("更正撤销入口仅 org_owner 可见", () => {
    expect(desk).toContain('data-section="records"');
    expect(desk).toMatch(/org_owner/);
  });

  it("移动优先:无固定像素宽,不写桌面专用布局", () => {
    for (const rel of [pageRel, deskRel, partRel, redeemRel]) {
      expect(read(rel)).not.toMatch(/width:\s*\d{3,}px/);
      expect(read(rel)).not.toMatch(/min-width:\s*\d{3,}px/);
    }
  });

  it("不展示经营报表/顾客手机号/visitor 键", () => {
    expect(desk).not.toMatch(/analytics|dashboard/);
    for (const rel of [deskRel, partRel, redeemRel]) {
      expect(read(rel)).not.toContain("phone");
      expect(read(rel)).not.toContain("visitor_id");
    }
  });
});

describe("参与卡:确认什么、以什么证据", () => {
  const card = read(partRel);

  it("证据选项只来自批次冻结规则(白名单派生),不自报", () => {
    expect(card).toContain("evidenceChoices");
    expect(card).toContain("matchBatchesForCard");
    expect(card).not.toContain('value="self_report');
  });

  it("official 只读展示一行,不可点选", () => {
    expect(card).toContain('data-field="official-note"');
    expect(card).toContain("officialReadOnly");
    expect(card).toContain("confirmDisabledReason");
  });

  it("三个主动作:确认并发奖/确认并现场兑现(仅现场赠品)/暂不符合", () => {
    expect(card).toContain('data-action="confirm-issue"');
    expect(card).toContain('data-action="confirm-fulfill"');
    expect(card).toContain('data-action="not-fit"');
    expect(card).toContain("gift_discount");
  });

  it("按钮旁明示正在确认什么", () => {
    expect(card).toContain('data-field="confirming-what"');
  });

  it("暂不符合:零写入,顾客可补做后再来", () => {
    expect(card).toContain("未发放任何奖励");
    expect(card).toContain("补");
    // 暂不符合处理器不调用任何写端点(文件里唯一的 reward-confirm 调用在确认提交里)。
    const calls = [...card.matchAll(/session\.api\(\s*"(POST|PATCH|DELETE)",\s*`?([^`')]+)/g)].map((m) => m[2]);
    for (const path of calls) {
      expect(path).not.toContain("cancel");
    }
  });

  it("预留状态文案走白名单(benefitCopy),只有 confirmed 才说已保留", () => {
    expect(card).toContain("benefitCopy");
  });
});

describe("核销卡:券码、独立核销入口、已处理回读", () => {
  const card = read(redeemRel);

  it("券码默认掩码,点开才看全码", () => {
    expect(card).toContain("maskCode");
    expect(card).toContain('data-action="reveal-code"');
  });

  it("核销是独立入口;另一端已处理时显示处理人+时间", () => {
    expect(card).toContain('data-action="redeem"');
    expect(card).toContain("redeemRecordText");
  });

  it("撤销入口由 owner 权限 prop 控制,原因必填", () => {
    expect(card).toContain('data-action="reverse"');
    expect(card).toContain("reverseAllowed");
    expect(card).toContain("没有原因就不撤销");
  });
});
