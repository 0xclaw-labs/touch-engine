import { describe, expect, it } from "vitest";
import {
  TEMPLATE_PRESETS,
  applyTemplate,
  buildPublishBody,
  buildRollbackBody,
  canPublish,
  previewRows,
  publishFailureText,
  reasonCopy,
  summaryLineCopy,
  templateById,
  verdictCopy,
  type ChannelItem,
  type PreflightResult,
} from "../src/lib/storefront-page";

const items: ChannelItem[] = [
  { action_id: "sa_dp", catalog_code: "A08", label: "大众点评", enabled: true, order: 0 },
  { action_id: "sa_dy", catalog_code: "A13", label: "抖音关注", enabled: true, order: 1 },
  { action_id: "sa_ga", catalog_code: "A19", label: "美团团购", enabled: true, order: 2 },
  { action_id: "sa_off", catalog_code: "A15", label: "停用的入口", enabled: false, order: 3 },
];

describe("行业模板", () => {
  it("三个预设都在,且段落类型合法", () => {
    expect(TEMPLATE_PRESETS.map((p) => p.id)).toEqual(["basic", "dining", "retail"]);
    for (const preset of TEMPLATE_PRESETS) {
      for (const group of preset.groups) {
        expect(["actions", "offers"]).toContain(group.type);
      }
    }
  });

  it("应用模板 = 目录码归组到本店真实动作;停用动作不进;空段落整段省略并说明", () => {
    const dining = templateById("dining");
    expect(dining).toBeTruthy();
    const { sections, skipped } = applyTemplate(dining!, items);
    const s1 = sections.find((s) => s.id === "s1");
    expect(s1?.action_ids).toEqual(["sa_dp"]);
    const s3 = sections.find((s) => s.id === "s3");
    expect(s3?.type).toBe("offers");
    // A10/A11/A12/A13/A14/A16 里本店只配了 A13(抖音关注在 s2),s1 缺的段不虚构。
    const s2 = sections.find((s) => s.id === "s2");
    expect(s2?.action_ids).toEqual(["sa_dy"]);
    // 没有任何实例的段(A10 等没有)不影响;offers 段永远保留骨架。
    expect(skipped).toEqual([]);
    // 全停用的店:动作段全部省略并给出说明,不虚构入口。
    const disabled = items.map((i) => ({ ...i, enabled: false }));
    const empty = applyTemplate(dining!, disabled);
    expect(empty.sections.every((s) => s.type === "offers")).toBe(true);
    expect(empty.skipped.length).toBeGreaterThan(0);
  });
});

describe("预检与发布确认的产品语言", () => {
  it("三种状态各有说法,受限/缺配置给修复入口,ready 不给", () => {
    const ready = verdictCopy({ action_id: "a", section_id: "s", state: "ready", reason_codes: [] });
    expect(ready.stateText).toContain("可以发布");
    expect(ready.fixEntry).toBe("");
    const blocked = verdictCopy({ action_id: "a", section_id: "s", state: "restricted", reason_codes: ["action_revoked"] });
    expect(blocked.stateText).toContain("受限");
    expect(blocked.lines[0]).toContain("撤销");
    expect(blocked.fixEntry).toContain("渠道动作");
    const missing = verdictCopy({ action_id: "a", section_id: "s", state: "missing_config", reason_codes: ["target_missing"] });
    expect(missing.stateText).toContain("缺配置");
    expect(missing.lines[0]).toContain("目标地址");
  });

  it("diff 摘要行翻成人话;未知行原样保留", () => {
    expect(summaryLineCopy("section_added:s3")).toContain("新增段落");
    expect(summaryLineCopy("action_removed:sa_1")).toContain("移除动作");
    expect(summaryLineCopy("brand_changed")).toContain("品牌");
    expect(summaryLineCopy("custom_code:x")).toBe("custom_code:x");
  });

  it("发布三条件:预检可发布 + 用户勾选确认 + 指纹匹配,缺一不可", () => {
    const pre: PreflightResult = {
      draft_revision: 2,
      published_revision: 1,
      fatal: [],
      actions: [],
      change_summary: ["action_added:x"],
      summary_fingerprint: "fp-1",
      publishable: true,
    };
    expect(canPublish(pre, true, "fp-1")).toBe(true);
    expect(canPublish(pre, false, "fp-1")).toBe(false);
    expect(canPublish(pre, true, "fp-stale")).toBe(false);
    expect(canPublish({ ...pre, publishable: false }, true, "fp-1")).toBe(false);
    expect(canPublish(null, true, "fp-1")).toBe(false);
  });

  it("发布/回滚失败文案按机器原因码翻成下一步,通用失败兜底", () => {
    expect(publishFailureText(409, { error: "stale_version" })).toContain("重新预检");
    expect(publishFailureText(422, { error: "actions_not_ready" })).toContain("移除");
    expect(publishFailureText(409, { error: "confirm_required" })).toContain("确认最新差异");
    expect(publishFailureText(500, {})).toContain("稍后重试");
  });

  it("reason code 全表覆盖(机器码不出现在界面)", () => {
    for (const code of [
      "action_not_found", "action_disabled", "action_revoked", "action_expired",
      "target_missing", "target_not_official", "content_not_adopted", "content_blocked", "content_grant_expired",
    ]) {
      expect(reasonCopy(code)).not.toEqual(code);
    }
  });
});

describe("请求体组装", () => {
  it("发布体带候选版本+期望线上版本+指纹+确认;回滚体带目标版本+期望线上版本", () => {
    expect(buildPublishBody(2, 1, "fp")).toEqual({
      candidate_revision: 2,
      expected_published_revision: 1,
      summary_fingerprint: "fp",
      confirm_changes: true,
    });
    expect(buildRollbackBody(1, 2)).toEqual({ target_revision: 1, expected_published_revision: 2 });
  });
});

describe("手机预览投影", () => {
  it("段落 → 手机壳行;offers 段显示套餐数;下线动作标注不冒充可用", () => {
    const rows = previewRows({
      sections: [
        { section_id: "s1", type: "actions", title: "常用服务", action_ids: ["a", "gone"] },
        { section_id: "s2", type: "offers", title: "到店优惠", offer_refs: ["o1", "o2"] },
      ],
      actions: [{ action_id: "a", label: "大众点评" } as never],
    });
    expect(rows[0].title).toBe("常用服务");
    expect(rows[0].lines).toEqual(["大众点评", "已下线的入口"]);
    expect(rows[1].lines[0]).toContain("2 个套餐");
  });
});
