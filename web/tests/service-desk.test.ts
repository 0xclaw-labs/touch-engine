import { describe, expect, it } from "vitest";
import {
  SERVICE_DESK_HREF,
  SERVICE_DESK_LABEL,
  serviceDeskEntryVisible,
} from "@/lib/work/service-desk";
import {
  confirmDisabledReason,
  confirmRequestBody,
  evidenceChoices,
  extractHandoffToken,
  looksLikeHandoffToken,
  matchBatchesForCard,
  networkUnknownCopy,
  outcomeNotice,
  ownStoreBenefits,
  redeemRecordText,
  type ServiceBatch,
} from "../src/lib/service-desk";

// HUI-2222：「确认与核销」入口可见性是纯判定。可见集对齐服务端 authz
// ActionUpdate 矩阵（org_owner / store_manager / staff；agent 只读不进），
// 开关缺省或 off 一律不渲染（HUI-3562 页面本体，未开通不冒充可用）。

describe("service desk entry visibility", () => {
  it("label and href are merchant language, pointing at the future page", () => {
    expect(SERVICE_DESK_LABEL).toBe("确认与核销");
    expect(SERVICE_DESK_HREF).toBe("/work/service");
  });

  it("feature off or missing hides the entry for every role", () => {
    for (const features of [undefined, { serviceDesk: false }] as const) {
      for (const role of ["org_owner", "store_manager", "staff", "agent", "", "unknown"]) {
        expect(serviceDeskEntryVisible(features, role)).toBe(false);
      }
    }
  });

  it("feature on shows the entry only for service-capable roles", () => {
    expect(serviceDeskEntryVisible({ serviceDesk: true }, "org_owner")).toBe(true);
    expect(serviceDeskEntryVisible({ serviceDesk: true }, "store_manager")).toBe(true);
    expect(serviceDeskEntryVisible({ serviceDesk: true }, "staff")).toBe(true);
  });

  it("feature on still hides read-only agent and unknown roles", () => {
    for (const role of ["agent", "", "unknown", "owner"]) {
      expect(serviceDeskEntryVisible({ serviceDesk: true }, role)).toBe(false);
    }
  });

  it("does not mutate its inputs", () => {
    const features = { serviceDesk: true };
    serviceDeskEntryVisible(features, "staff");
    expect(features).toEqual({ serviceDesk: true });
  });
});

// HUI-3562 店员台纯函数语义。红线:
//   - 证据选项只来自活动已冻结规则(批次快照的 allowed_evidence);
//   - official_publish_confirmed 永远不是店员可点选项(只读展示);
//   - 未知/重放不是新发放;断网未知先回查,永不提示顾客"重新领";
//   - 店经理只见本店;现场赠品才给「确认并现场兑现」。

const batch: ServiceBatch = {
  id: "rinv_1",
  kind: "store_coupon",
  name: "开业券",
  benefit_slot: "first_publish",
  rule_version: 1,
  allowed_evidence: ["staff_observed_submission", "staff_verified_post"],
  total_qty: 10,
  available_qty: 9,
  status: "active",
  store_id: "sto_a",
};

describe("参与码:提取与形状预检", () => {
  it("从规范二维码载荷提取 ptx_ 凭据", () => {
    expect(extractHandoffToken("https://h5.example.com/p/ptx_aaaaaaaaaaaaaaaaaaaaaa.A")).toBe(
      "ptx_aaaaaaaaaaaaaaaaaaaaaa.A",
    );
    expect(extractHandoffToken("ptx_aaaaaaaaaaaaaaaaaaaaaa.A")).toBe("ptx_aaaaaaaaaaaaaaaaaaaaaa.A");
    expect(extractHandoffToken("  ptx_aaaaaaaaaaaaaaaaaaaaaa.A ")).toBe("ptx_aaaaaaaaaaaaaaaaaaaaaa.A");
  });

  it("非规范载荷/垃圾输入返回 null(不猜)", () => {
    expect(extractHandoffToken("https://h5.example.com/other/ptx_xxx.A")).toBeNull();
    expect(extractHandoffToken("https://h5.example.com/p/")).toBeNull();
    expect(extractHandoffToken("")).toBeNull();
    expect(extractHandoffToken("random text")).toBeNull();
  });

  it("形状预检:校验位段缺失/长度不对先本地拒,不打网", () => {
    expect(looksLikeHandoffToken("ptx_aaaaaaaaaaaaaaaaaaaaaa.A")).toBe(true);
    expect(looksLikeHandoffToken("ptx_short.A")).toBe(false);
    expect(looksLikeHandoffToken("ptx_aaaaaaaaaaaaaaaaaaaaaa")).toBe(false);
    expect(looksLikeHandoffToken("ptx_aaaaaaaaaaaaaaaaaaaaaa.AB")).toBe(false);
    expect(looksLikeHandoffToken("")).toBe(false);
  });
});

describe("证据选项只来自活动冻结规则", () => {
  it("只把店员两类列为可点选项,official 永不可点", () => {
    const choices = evidenceChoices(["staff_observed_submission", "official_publish_confirmed", "staff_verified_post"]);
    expect(choices.selectable.map((c) => c.value)).toEqual(["staff_observed_submission", "staff_verified_post"]);
    expect(choices.officialReadOnly).toBe(true);
  });

  it("规则没给的证据不出现(越界不可选)", () => {
    const choices = evidenceChoices(["staff_verified_post"]);
    expect(choices.selectable.map((c) => c.value)).toEqual(["staff_verified_post"]);
    expect(choices.officialReadOnly).toBe(false);
  });

  it("只认官方的规则:主动作禁用并说明人工不能代为确认", () => {
    const choices = evidenceChoices(["official_publish_confirmed"]);
    expect(choices.selectable).toEqual([]);
    expect(choices.officialReadOnly).toBe(true);
    const reason = confirmDisabledReason(choices);
    expect(reason).toContain("官方");
    expect(reason).toContain("不能");
  });

  it("未选证据时按钮禁用并提示先核对", () => {
    const reason = confirmDisabledReason(evidenceChoices(["staff_observed_submission"]));
    expect(reason).toContain("核对");
  });

  it("选了允许的证据:不禁用(null)", () => {
    const reason = confirmDisabledReason(evidenceChoices(["staff_observed_submission"]), "staff_observed_submission");
    expect(reason).toBeNull();
  });
});

describe("批次匹配:参与卡只认冻结规则版本", () => {
  it("按规则版本+门店作用域过滤可用批次", () => {
    const hit = matchBatchesForCard(
      [batch, { ...batch, id: "rinv_2", rule_version: 2 }, { ...batch, id: "rinv_3", store_id: "sto_b" }, { ...batch, id: "rinv_4", status: "paused" }],
      { rule_version: 1, store_id: "sto_a" },
    );
    expect(hit.map((b) => b.id)).toEqual(["rinv_1"]);
  });

  it("租户级批次(无门店绑定)对本店参与可用", () => {
    const hit = matchBatchesForCard([{ ...batch, store_id: undefined }], { rule_version: 1, store_id: "sto_a" });
    expect(hit.map((b) => b.id)).toEqual(["rinv_1"]);
  });
});

describe("结果文案:未知不是已发放,重放没有第二份", () => {
  it("发放/现场兑现/已处理回读各有诚实文案", () => {
    expect(outcomeNotice("issued", true, false).text).toContain("已发放");
    expect(outcomeNotice("issued_and_redeemed", true, false).text).toContain("现场兑现");
    expect(outcomeNotice("already_issued", false, false).text).toContain("没有第二份");
    expect(outcomeNotice("issued", true, true).text).toContain("没有第二份");
    expect(outcomeNotice("unknown", false, false).text).not.toContain("已发放");
  });

  it("核销卡显示处理人与时间;另一端显示已处理", () => {
    const text = redeemRecordText({ status: "redeemed", redeemed_by: "usr_mgr_a", redeemed_at: "2026-10-14T10:00:00Z" });
    expect(text).toContain("已核销");
    expect(text).toContain("usr_mgr_a");
    expect(text).toContain("2026-10-14T10:00:00Z");
    expect(redeemRecordText({ status: "issued", redeemed_by: "", redeemed_at: "" })).not.toContain("已核销");
  });
});

describe("断网未知:先回查,不提示顾客重新领", () => {
  it("回查到事实→已处理文案;查无事实→可安全重试且说明不会发两份", () => {
    const found = networkUnknownCopy(true);
    expect(found).toContain("已处理");
    expect(found).not.toContain("重新领");
    const missing = networkUnknownCopy(false);
    expect(missing).toContain("重试");
    expect(missing).toContain("不会发两份");
    expect(missing).not.toContain("重新领");
  });

  it("确认请求体:同一次确认一个幂等键,现场兑现带核销键", () => {
    const body = confirmRequestBody({
      participationId: "pt_1",
      benefitSlot: "first_publish",
      ruleVersion: 1,
      evidence: "staff_observed_submission",
      storeId: "sto_a",
      fulfillNow: true,
      note: "现场给过",
    });
    expect(body["participation_id"]).toBe("pt_1");
    expect(body["fulfill_now"]).toBe(true);
    expect(String(body["redeem_event_id"])).not.toBe(body["event_id"]);
    const again = confirmRequestBody({
      participationId: "pt_1",
      benefitSlot: "first_publish",
      ruleVersion: 1,
      evidence: "staff_observed_submission",
      storeId: "sto_a",
      fulfillNow: false,
    });
    expect(again["event_id"]).not.toBe(body["event_id"]);
    expect(again["fulfill_now"]).toBe(false);
  });
});

describe("本店作用域:店经理只见本店权益记录", () => {
  it("有门店作用域时只看本店(总部级未绑定记录不可见);总部看全量", () => {
    const items = [
      { id: "b1", store_id: "sto_a" },
      { id: "b2", store_id: "sto_b" },
      { id: "b3", store_id: undefined },
    ];
    expect(ownStoreBenefits(items, "sto_a").map((b) => b.id)).toEqual(["b1"]);
    expect(ownStoreBenefits(items, "")).toHaveLength(3);
  });
});
