import { describe, expect, it } from "vitest";
import {
  buildPutBody,
  catalogMissingEntries,
  channelsSaveError,
  normalizeItems,
  reorder,
  targetSummary,
  validateSummary,
  type ChannelItem,
} from "../src/lib/store-channels";

// 会让这个测试失败的产品文案改动：把「原生唤起未验证/平台动作未知/准入未判定」
// 说成成功，或在保存负载里丢失 config_revision 乐观锁。

const CATALOG = [
  { code: "A08", label: "大众点评", platform: "dianping", kind: "review", variant: "" },
  { code: "A12", label: "美团点评", platform: "meituan", kind: "review", variant: "" },
  { code: "A17", label: "门店 Wi-Fi", platform: "", kind: "wifi", variant: "" },
];

function item(partial: Partial<ChannelItem>): ChannelItem {
  return {
    action_id: "sa_a",
    action_revision: 1,
    revision: 1,
    catalog_code: "A08",
    catalog_label: "大众点评",
    platform: "dianping",
    kind: "review",
    variant: "",
    label: "大众点评",
    target_ref: "https://www.dianping.com/shop/1",
    content_binding_ref: "",
    order: 1,
    enabled: true,
    source: "store",
    legacy_campaign_id: "",
    ...partial,
  };
}

describe("门店渠道动作页纯逻辑", () => {
  it("缺项提示按目录逐项算，已配置目录不再提示", () => {
    const items = [
      item({ catalog_code: "A08" }),
      item({ action_id: "sa_b", catalog_code: "A08" }), // 同目录多条并存是常态
      item({ action_id: "sa_c", catalog_code: "A17" }),
    ];
    const missing = catalogMissingEntries(CATALOG, items);
    expect(missing.map((m) => m.code)).toEqual(["A12"]);
    expect(missing[0].label).toBe("美团点评");
    // 待映射行（catalog_code=''）不算已配置。
    const withPending = [...items, item({ action_id: "sa_d", catalog_code: "", catalog_label: "" })];
    expect(catalogMissingEntries(CATALOG, withPending).map((m) => m.code)).toEqual(["A12"]);
  });

  it("目标摘要只给主机名和截断路径", () => {
    expect(targetSummary("https://www.dianping.com/shop/123456?x=1")).toBe("www.dianping.com/shop/123456");
    expect(targetSummary("https://shop.example.com/wifi")).toBe("shop.example.com/wifi");
    expect(targetSummary("http://127.0.0.1/x")).toBe("目标地址不是正式链接");
    expect(targetSummary("")).toBe("未配置目标");
  });

  it("排序交换只影响相邻两项的 order", () => {
    const items = [
      item({ action_id: "sa_a", order: 1 }),
      item({ action_id: "sa_b", order: 2 }),
      item({ action_id: "sa_c", order: 3 }),
    ];
    const moved = reorder(items, "sa_b", -1);
    expect(moved.find((m) => m.action_id === "sa_a")?.order).toBe(2);
    expect(moved.find((m) => m.action_id === "sa_b")?.order).toBe(1);
    expect(moved.find((m) => m.action_id === "sa_c")?.order).toBe(3);
    // 边界：第一项上移/最后一项下移不动。
    expect(reorder(items, "sa_a", -1).find((m) => m.action_id === "sa_a")?.order).toBe(1);
    expect(reorder(items, "sa_c", 1).find((m) => m.action_id === "sa_c")?.order).toBe(3);
  });

  it("保存负载带 expected_revision、规整 order、不回显服务端只读字段", () => {
    const items = [
      item({ action_id: "sa_a", order: 2, revision: 3, action_revision: 3 }),
      item({ action_id: "", catalog_code: "A12", label: "美团点评", target_ref: "https://www.meituan.com/x", order: 1 }),
    ];
    const body = buildPutBody(items, 4);
    expect(body.expected_revision).toBe(4);
    expect(body.items).toHaveLength(2);
    // order 规整为 1..n 连续（按展示序）。
    expect(body.items[0]).toEqual({
      action_id: "",
      catalog_code: "A12",
      label: "美团点评",
      target_ref: "https://www.meituan.com/x",
      content_binding_ref: "",
      order: 1,
      enabled: true,
    });
    expect(body.items[1]).toEqual({
      action_id: "sa_a",
      catalog_code: "A08",
      label: "大众点评",
      target_ref: "https://www.dianping.com/shop/1",
      content_binding_ref: "",
      order: 2,
      enabled: true,
    });
    expect(JSON.stringify(body.items)).not.toContain("revision");
    expect(JSON.stringify(body.items)).not.toContain("platform");
  });

  it("启用行必须有目标与目录，校验文案三分证据且不冒充成功", () => {
    const items = normalizeItems([
      item({ catalog_code: "", enabled: true }),
      item({ action_id: "sa_b", catalog_code: "A12", label: "美团点评", target_ref: "", enabled: true }),
      item({ action_id: "sa_c", catalog_code: "A17", label: "Wi-Fi", target_ref: "", enabled: false }),
    ]);
    expect(items.map((i) => i.blocker)).toEqual(["请先选择目录项", "启用前需要填写目标地址", ""]);
  });

  it("validate 摘要：三分证据 + 准入未判定，绝无成功话术", () => {
    const ok = validateSummary({
      action_id: "sa_a",
      action_revision: 2,
      field_errors: [],
      method_states: [
        { method: "web", state: "configured" },
        { method: "client_launch", state: "unverified", reason: "native_launch_unverified" },
        { method: "platform_action", state: "unknown" },
      ],
      reason_codes: [],
      reward_admission: { evaluated: false, capability_ref: "", reason: "admission_truth_not_wired" },
    });
    expect(ok.lines.join("\n")).toContain("网页配置可达");
    expect(ok.lines.join("\n")).toContain("原生唤起未验证");
    expect(ok.lines.join("\n")).toContain("平台动作未知");
    expect(ok.lines.join("\n")).toContain("营销奖励场景准入未判定");
    expect(ok.lines.join("\n")).not.toMatch(/已关注|已加群|点评成功|发布成功|加微信成功/);

    const bad = validateSummary({
      action_id: "sa_legacy",
      action_revision: 1,
      field_errors: [{ field: "target_ref", code: "target_not_official" }],
      method_states: [
        { method: "web", state: "closed", reason: "address_not_official" },
        { method: "client_launch", state: "unverified", reason: "native_launch_unverified" },
        { method: "platform_action", state: "unknown" },
      ],
      reason_codes: ["target_not_official"],
      reward_admission: { evaluated: false, capability_ref: "", reason: "admission_truth_not_wired" },
    });
    expect(bad.problem).toContain("目标地址不是公网正式链接");
    expect(bad.lines.join("\n")).toContain("网页未开放（地址不是正式链接）");
  });

  it("保存失败文案区分旧版本并发与逐字段拒绝", () => {
    expect(channelsSaveError(409, { error: "stale_version" })).toContain("已被他人更新");
    expect(channelsSaveError(400, { error: "unknown_catalog_code" })).toContain("目录项");
    expect(channelsSaveError(400, { error: "duplicate_action_id" })).toContain("同一个动作");
    expect(channelsSaveError(400, { error: "bad_target_ref" })).toContain("公网 https");
    expect(channelsSaveError(400, { error: "target_required" })).toContain("启用前需要填写目标地址");
    expect(channelsSaveError(403, { error: "out_of_scope" })).toContain("只能配置自己管辖的门店");
    expect(channelsSaveError(500, {})).toContain("没有保存成功");
  });
});
