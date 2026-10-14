import { describe, expect, it } from "vitest";
import {
  admissionNote,
  channelLabel,
  defaultSpec,
  dedupBasisLabel,
  benefitKindLabel,
  fromRFC3339,
  modeExplanation,
  perOrderAllowed,
  refusalCopy,
  staffScopeLabel,
  toRFC3339,
  toServerSpec,
  verificationModeLabel,
  windowProblem,
  DEDUP_BASES,
  MODES,
} from "@/lib/campaign-config";

// HUI-1676 增量:发布兑奖配置纯函数验收(node 环境,无 DOM)。

describe("三种核验模式", () => {
  it("枚举与 rewardops 三个 Evidence 类逐字对齐", () => {
    expect(MODES).toEqual([
      "staff_observed_submission",
      "staff_verified_post",
      "official_publish_confirmed",
    ]);
  });

  it("每种模式有商家语言标签和说明,第三种不强制", () => {
    for (const mode of MODES) {
      expect(verificationModeLabel(mode)).not.toBe(mode);
      expect(modeExplanation(mode).length).toBeGreaterThan(4);
    }
    expect(verificationModeLabel("staff_observed_submission")).toContain("目测");
    expect(modeExplanation("official_publish_confirmed")).toContain("不是所有活动");
  });
});

describe("去重判定基础", () => {
  it("三个选项,cookie/IP 不在其中", () => {
    expect(DEDUP_BASES).toEqual([
      "verifiable_receipt",
      "platform_authorized_subject",
      "onsite_manual",
    ]);
    for (const basis of DEDUP_BASES) {
      expect(dedupBasisLabel(basis)).not.toMatch(/cookie|ip/i);
    }
  });

  it("按订单限领只在可验证消费凭据下可选", () => {
    expect(perOrderAllowed("verifiable_receipt")).toBe(true);
    expect(perOrderAllowed("platform_authorized_subject")).toBe(false);
    expect(perOrderAllowed("onsite_manual")).toBe(false);
  });
});

describe("奖励口径标签", () => {
  it("四种奖励与口径都有商家语言", () => {
    expect(benefitKindLabel("store_coupon")).toContain("优惠券");
    expect(benefitKindLabel("imported_code")).toContain("券码");
    expect(benefitKindLabel("cash")).toBe("cash");
    expect(staffScopeLabel("store_staff")).toContain("店员");
    expect(staffScopeLabel("org_owner")).toContain("老板");
  });
});

describe("启用拒绝映射到具体字段", () => {
  it("422 机器原因码 → 字段 + 下一步动作", () => {
    const cases: Array<[string, string, string]> = [
      ["customer_copy_missing", "customer_copy", "顾客文案"],
      ["content_not_ready", "content_version_id", "活动内容"],
      ["channel_not_admitted", "channels", "发布方式"],
      ["benefit_version_mismatch", "benefit_slot", "奖励"],
      ["benefit_stock_missing", "reward_budget", "库存"],
      ["window_conflict", "claim_window", "时间"],
      ["campaign_ended", "campaign_window", "结束"],
      ["store_mismatch", "store_ids", "门店"],
    ];
    for (const [reason, field, keyword] of cases) {
      const copy = refusalCopy(422, { error: reason, field });
      expect(copy.field).toBe(field);
      expect(copy.text).toContain(keyword);
    }
  });

  it("其他状态落到表单级兜底文案,不直出机器码", () => {
    const copy = refusalCopy(500, { error: "internal" });
    expect(copy.field).toBe("form");
    expect(copy.text).not.toMatch(/internal|422/);
  });
});

describe("窗口镜像校验", () => {
  const base = defaultSpec();

  it("没填完整不给问题(草稿允许)", () => {
    expect(windowProblem(base)).toBe("");
  });

  it("领取开始晚于结束要指出", () => {
    const spec = structuredClone(base);
    spec.reward.claim_from = "2026-10-02T00:00:00Z";
    spec.reward.claim_until = "2026-10-01T00:00:00Z";
    spec.reward.redeem_from = "2026-10-01T00:00:00Z";
    spec.reward.redeem_until = "2026-11-01T00:00:00Z";
    expect(windowProblem(spec)).toContain("领取开始");
  });

  it("兑换结束早于领取结束要指出", () => {
    const spec = structuredClone(base);
    spec.reward.claim_from = "2026-10-01T00:00:00Z";
    spec.reward.claim_until = "2026-10-31T00:00:00Z";
    spec.reward.redeem_from = "2026-10-01T00:00:00Z";
    spec.reward.redeem_until = "2026-10-15T00:00:00Z";
    expect(windowProblem(spec)).toContain("兑换结束");
  });
});

describe("时间格式转换", () => {
  it("datetime-local ↔ RFC3339 往返", () => {
    const rfc = toRFC3339("2026-10-01T08:30");
    expect(rfc).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    expect(fromRFC3339(rfc)).toBe("2026-10-01T08:30");
    expect(toRFC3339("")).toBe("");
    expect(fromRFC3339("")).toBe("");
    expect(toRFC3339("不是时间")).toBe("");
  });

  it("toServerSpec 只转换四个窗口值", () => {
    const spec = defaultSpec();
    spec.reward.claim_from = "2026-10-01T08:00";
    const out = toServerSpec(spec);
    expect(out.reward.claim_from).toMatch(/Z$/);
    expect(out.reward.claim_until).toBe("");
    expect(out.verification_mode).toBe(spec.verification_mode);
  });
});

describe("渠道展示", () => {
  it("可用/关闭/缺项各有诚实文案", () => {
    const allowed = admissionNote({
      platform: "douyin", entry_mode: "h5", scenario: "s",
      status: "admitted", allowed: true,
    });
    expect(allowed).toBe("可用");
    const closed = admissionNote({
      platform: "douyin", entry_mode: "h5", scenario: "s",
      status: "admitted", allowed: false, close_reason: "平台调整",
    });
    expect(closed).toContain("渠道已关闭");
    const missing = admissionNote({
      platform: "douyin", entry_mode: "h5", scenario: "s",
      status: "not_admitted", allowed: false, missing: ["企业认证", "POI 门店号"],
    });
    expect(missing).toContain("企业认证");
  });

  it("渠道标签是平台中文名", () => {
    expect(channelLabel({ platform: "douyin", entry_mode: "h5", scenario: "store_post" })).toContain("抖音");
  });
});

describe("默认草稿", () => {
  it("字段形状与服务端契约一致,没有技术 ID 字段", () => {
    const spec = defaultSpec();
    expect(spec.verification_mode).toBe("staff_observed_submission");
    expect(spec.reward.budget_mode).toBe("total_quantity");
    expect(spec.reward.staff_scope).toBe("store_staff");
    expect(spec.reward.out_of_stock_rule).toBe("stop_new");
    expect(spec.dedup_basis).toBe("platform_authorized_subject");
    expect(spec.channels).toEqual([]);
    expect(spec.customer_copy).toBe("");
    expect(Object.keys(spec)).not.toContain("order_id");
  });
});
