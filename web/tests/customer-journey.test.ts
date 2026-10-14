import { describe, expect, it } from "vitest";
import {
  admissionLines,
  canSwitchDraft,
  evidenceStepCopy,
  exceptionCopy,
  journeyStage,
  rewardCardCopy,
  stockLine,
  type CustomerParticipation,
} from "../src/lib/customer-journey";

// HUI-1896 顾客三屏:阶段状态机与文案唯一出口。
// 红线:未发放不冒充可领;外部 unknown 不把本系统已发放拖成「奖励未知」;
// 未知状态 fail-closed;未准入渠道不出现手动发布后门。

const started: CustomerParticipation = {
  id: "pt_1",
  status: "pending",
  benefit_reservation_state: "confirmed",
  content_pack_version: "v1",
  expires_at: "2026-10-21T00:00:00Z",
};

describe("journeyStage 阶段状态机", () => {
  it("无参与=browse(浏览不占奖励)", () => {
    expect(journeyStage(null)).toBe("browse");
  });

  it("pending=已开始(未发布)", () => {
    expect(journeyStage(started)).toBe("started");
  });

  it("in_progress=发布进行中", () => {
    expect(journeyStage({ ...started, status: "in_progress" })).toBe("publishing");
  });

  it("awaiting_review=待店员确认", () => {
    expect(journeyStage({ ...started, status: "awaiting_review" })).toBe("awaiting_review");
  });

  it("fulfilled+已发放=可领;fulfilled+已兑现=已兑现", () => {
    expect(journeyStage({ ...started, status: "fulfilled" }, { rewardStatus: "issued" })).toBe("reward_ready");
    expect(journeyStage({ ...started, status: "fulfilled" }, { rewardStatus: "redeemed" })).toBe("redeemed");
  });

  it("未知状态 fail-closed:不认识就不推进(保持 browse 而不是猜)", () => {
    expect(journeyStage({ ...started, status: "success" })).toBe("browse");
    expect(journeyStage({ ...started, status: undefined })).toBe("browse");
  });

  it("canceled 终态回到 browse(可重新开始,不冒充进行中)", () => {
    expect(journeyStage({ ...started, status: "canceled" })).toBe("browse");
  });
});

describe("rewardCardCopy 已发放优先,不被外部 unknown 拖黑", () => {
  it("本系统已发放:外部发布 unknown 也说已发放可用", () => {
    const copy = rewardCardCopy({ status: "issued", code: "8888", store_id: "s1", valid_until: "2026-11-01" }, { externalPublish: "unknown" });
    expect(copy).toContain("已发放");
    expect(copy).not.toContain("奖励未知");
  });

  it("已兑现:标注已兑现,不再引导重复领券", () => {
    const copy = rewardCardCopy({ status: "redeemed" }, {});
    expect(copy).toContain("已兑现");
    expect(copy).not.toContain("请领取");
  });

  it("只有预留:说保留中(未发放),不出现可用的券", () => {
    const copy = rewardCardCopy({ status: "reserved" }, {});
    expect(copy).toContain("预留");
    expect(copy).not.toContain("已发放");
  });

  it("无奖励记录:不伪报,给店员确认路径", () => {
    const copy = rewardCardCopy(null, {});
    expect(copy).toContain("店员");
    expect(copy).not.toContain("已发放");
  });

  it("券码掩码显示,不整码抛出", () => {
    const copy = rewardCardCopy({ status: "issued", code: "A1234567890" }, {});
    expect(copy).not.toContain("A1234567890");
    expect(copy).toContain("••••");
  });
});

describe("evidenceStepCopy 核验三态准确文案", () => {
  it("店员见到提交/店员核对作品/官方确认各自准确", () => {
    expect(evidenceStepCopy("staff_observed_submission")).toContain("店员见到提交");
    expect(evidenceStepCopy("staff_verified_post")).toContain("店员核对作品");
    expect(evidenceStepCopy("official_publish_confirmed")).toContain("官方确认");
  });

  it("未知证据如实说未认定,不冒充已核验", () => {
    expect(evidenceStepCopy("weird")).toContain("未认定");
    expect(evidenceStepCopy("")).toContain("未认定");
  });
});

describe("admissionLines 未准入不引导先手动发完再领", () => {
  it("allowed→无提示行", () => {
    expect(admissionLines({ allowed: true, reason: "", reason_text: "", missing: [] })).toEqual([]);
  });

  it("unknown(未登记)→诚实说明,不给手动指引", () => {
    const lines = admissionLines({ allowed: false, reason: "admission_unknown", reason_text: "该渠道路线暂无准入记录", missing: ["x"] });
    expect(lines.join("\n")).toContain("暂无准入记录");
    expect(lines.join("\n")).not.toContain("手动发布");
    expect(lines.join("\n")).not.toContain("先发");
  });

  it("服务不可达→说明不可用原因并给联系门店动作", () => {
    const lines = admissionLines(null);
    expect(lines.join("\n")).toContain("门店");
  });
});

describe("stockLine 限额不伪报", () => {
  it("undefined→空串(不显示编造数字)", () => {
    expect(stockLine(undefined)).toBe("");
  });

  it("0→用尽文案,不承诺可领", () => {
    const line = stockLine(0);
    expect(line).toContain("已用完");
    expect(line).not.toContain("可领");
  });

  it("1→最后一份,先到先得,不承诺一定能拿到", () => {
    const line = stockLine(1);
    expect(line).toContain("最后");
    expect(line).not.toContain("一定能");
  });

  it("n>1→显示余量", () => {
    expect(stockLine(5)).toContain("5");
  });
});

describe("exceptionCopy 异常矩阵各有真实恢复或联系门店动作", () => {
  it("十二类异常都有文案且不空", () => {
    const kinds = [
      "app_missing",
      "version_unsupported",
      "auth_denied",
      "download_failed",
      "user_canceled",
      "reviewing",
      "page_lost",
      "network_unknown",
      "reward_exhausted",
      "campaign_unavailable",
      "last_stock",
      "bad_code",
    ] as const;
    for (const kind of kinds) {
      const copy = exceptionCopy(kind);
      expect(copy.length).toBeGreaterThan(4);
    }
  });

  it("网络未知不伪报失败也不伪报成功", () => {
    const copy = exceptionCopy("network_unknown");
    expect(copy).not.toContain("失败");
    expect(copy).not.toContain("成功");
  });

  it("未知种类不编造", () => {
    expect(exceptionCopy("whatever" as never)).toBe("");
  });
});

describe("canSwitchDraft 换选只改未交接草稿", () => {
  it("无参与=不可换(还没开始,谈不上草稿)", () => {
    expect(canSwitchDraft(null, [])).toBe(false);
  });

  it("未交接=可换", () => {
    expect(canSwitchDraft(started, [])).toBe(true);
  });

  it("存在已交接 attempt=冻结", () => {
    expect(canSwitchDraft(started, [{ id: "at_1", status: "handed_off" }])).toBe(false);
  });

  it("终态参与不可换", () => {
    expect(canSwitchDraft({ ...started, status: "fulfilled" }, [])).toBe(false);
  });
});
