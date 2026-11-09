import { describe, expect, it } from "vitest";

import {
  INTENT_TITLES,
  actionHref,
  actionIntent,
  activityCards,
  groupActions,
  offerEntry,
  offersView,
  parseStorefront,
  storeStateFace,
  type StorefrontActionView,
  type StorefrontPayload,
} from "@/lib/storefront-view";

// HUI-1896 C1 门店首页数据面:纯函数契约消费(1676/4584 storefronts 公共读)。
// 纪律:
//   - payload 白名单解析:内部字段(tenant_id/target_ref/legacy)绝不进视图;
//   - 未配置/空分组不渲染(不死按钮);disabled 门店动作区收起只给门店提示;
//   - activity_cards 容忍解析:无合法活动码不伪造深链(活动卡可选,基础服务
//     不依赖活动);
//   - offers(4590)契约 fixture 只在本测试做本地契约验证(隔离数据面),
//     生产路径未接真实提供者前页面整区不渲染、不伪建素材/价格。

// 基线 4584 数据面形状(schema storefront.v1,revision=0,单 section)。
const BASELINE_PAYLOAD: StorefrontPayload = {
  schema_version: "storefront.v1",
  store_code: "st_8HXK38MQR2VW",
  storefront_revision: 0,
  store: { name: "派诺咖啡天山店", address: "上海市长宁区天山西路 100 号", opening_hours: "09:00-21:00", state: "active" },
  sections: [{ section_id: "default", title: "门店服务", action_ids: ["sa_a1", "sa_a8"] }],
  actions: [
    { action_id: "sa_a1", action_revision: 3, catalog_code: "A01", platform: "douyin", kind: "publish", variant: "video", label: "抖音视频", presentation: "primary", capability_ref: "cap:douyin" },
    { action_id: "sa_a8", action_revision: 1, catalog_code: "A08", platform: "dianping", kind: "review", label: "大众点评", presentation: "", capability_ref: "" },
    // 内部字段:白名单解析必须丢弃,不得进视图。
    { action_id: "sa_x", action_revision: 1, catalog_code: "A17", platform: "", kind: "wifi", label: "门店 Wi-Fi", tenant_id: "t_1", target_ref: "secret", legacy_campaign_id: "c_1" } as never,
  ],
};

// 1676 合流后的契约样本(隔离 fixture:多分组 + revision>0 + 活动卡)。
const PUBLISHED_PAYLOAD: StorefrontPayload = {
  schema_version: "storefront.v1",
  store_code: "st_8HXK38MQR2VW",
  storefront_revision: 4,
  store: { name: "派诺咖啡天山店", address: "上海市长宁区天山西路 100 号", opening_hours: "09:00-21:00", state: "active" },
  sections: [
    { section_id: "popular", title: "常用", action_ids: ["sa_a17", "sa_a8"] },
    { section_id: "publish", title: "内容发布", action_ids: ["sa_a1"] },
  ],
  actions: BASELINE_PAYLOAD.actions,
  activity_cards: [
    { card_id: "ac_1", title: "发视频领优惠", state: "available", campaign_code: "8HXK38MQR2VW" },
    { card_id: "ac_2", title: "旧活动", state: "ended" },
  ],
};

describe("parseStorefront:白名单解析", () => {
  it("解析基线数据面:门店/节/动作齐全", () => {
    const view = parseStorefront(BASELINE_PAYLOAD);
    expect(view).not.toBeNull();
    expect(view?.store.name).toBe("派诺咖啡天山店");
    expect(view?.store.state).toBe("active");
    expect(view?.storefrontRevision).toBe(0);
    expect(view?.actions).toHaveLength(3);
    expect(view?.actions[0]).toEqual({
      actionId: "sa_a1", actionRevision: 3, catalogCode: "A01", platform: "douyin",
      kind: "publish", variant: "video", label: "抖音视频", presentation: "primary", capabilityRef: "cap:douyin",
    } satisfies StorefrontActionView);
  });

  it("内部字段(tenant_id/target_ref/legacy)绝不进视图", () => {
    const view = parseStorefront(BASELINE_PAYLOAD);
    const wifi = view?.actions.find((a) => a.actionId === "sa_x");
    expect(wifi).not.toBeUndefined();
    expect(JSON.stringify(wifi)).not.toContain("secret");
    expect(JSON.stringify(wifi)).not.toContain("t_1");
    expect(JSON.stringify(wifi)).not.toContain("c_1");
  });

  it("非对象/缺 schema/缺店名 → null(不渲染编造门店)", () => {
    expect(parseStorefront(null)).toBeNull();
    expect(parseStorefront("x")).toBeNull();
    expect(parseStorefront({ store: { name: "x" }, actions: [] })).toBeNull();
    expect(parseStorefront({ schema_version: "", store: { name: "x" }, actions: [] })).toBeNull();
    expect(parseStorefront({ schema_version: "storefront.v1", store: {}, actions: [] })).toBeNull();
  });

  it("1676 合流契约样本(隔离 fixture):多节 + revision>0 解析正确", () => {
    const view = parseStorefront(PUBLISHED_PAYLOAD);
    expect(view?.storefrontRevision).toBe(4);
    expect(view?.sections).toHaveLength(2);
    expect(view?.sections[0].actionIds).toEqual(["sa_a17", "sa_a8"]);
  });
});

describe("actionIntent/actionHref:意图分组与地址化", () => {
  it("kind→意图映射齐全", () => {
    expect(actionIntent("publish")).toBe("publish");
    expect(actionIntent("review")).toBe("review");
    expect(actionIntent("follow")).toBe("follow");
    expect(actionIntent("wifi")).toBe("wifi");
    expect(actionIntent("contact")).toBe("contact");
    expect(actionIntent("groupbuy")).toBe("groupbuy");
    expect(actionIntent("share")).toBe("share");
    expect(actionIntent("join_group")).toBe("share");
    expect(actionIntent("mystery")).toBe("other");
  });

  it("publish 动作去内容视图,其余去服务视图(地址化)", () => {
    const view = parseStorefront(BASELINE_PAYLOAD)!;
    expect(actionHref("st_8HXK38MQR2VW", view.actions[0])).toBe("/s/st_8HXK38MQR2VW/content/sa_a1");
    expect(actionHref("st_8HXK38MQR2VW", view.actions[1])).toBe("/s/st_8HXK38MQR2VW/service/sa_a8");
  });

  it("空 action_id 不给链接(死按钮零容忍)", () => {
    const view = parseStorefront(BASELINE_PAYLOAD)!;
    expect(actionHref("st_x", { ...view.actions[0], actionId: "" })).toBeNull();
  });
});

describe("groupActions:未配置动作不渲染", () => {
  it("按意图分组且空组不出现", () => {
    const view = parseStorefront(BASELINE_PAYLOAD)!;
    const groups = groupActions("st_8HXK38MQR2VW", view.actions);
    const intents = groups.map((g) => g.intent);
    expect(intents).toContain("publish");
    expect(intents).toContain("review");
    expect(intents).toContain("wifi");
    expect(intents).not.toContain("follow");
    expect(intents).not.toContain("groupbuy");
    // 组内顺序稳定:publish 第一。
    expect(intents[0]).toBe("publish");
  });

  it("全空动作列表 → 空数组(页面只给门店头,不编造入口)", () => {
    expect(groupActions("st_x", [])).toEqual([]);
  });

  it("每组都有中文标题(读屏可理解)", () => {
    const view = parseStorefront(BASELINE_PAYLOAD)!;
    for (const g of groupActions("st_8HXK38MQR2VW", view.actions)) {
      expect(INTENT_TITLES[g.intent]).toBeTruthy();
      expect(g.title).toBe(INTENT_TITLES[g.intent]);
    }
  });
});

describe("storeStateFace:disabled/active 诚实态", () => {
  it("active 不加提示;disabled 给门店暂停提示文案", () => {
    expect(storeStateFace("active")).toBe("");
    const face = storeStateFace("disabled");
    expect(face).toContain("暂停");
    expect(face).not.toContain("undefined");
  });

  it("未知状态按未知处理,不冒充 active", () => {
    expect(storeStateFace("weird")).toContain("未知");
  });
});

describe("activityCards:活动卡可选,无码不伪造深链", () => {
  it("有合法活动码 → 深链 /c/{code}?entry=web;无码 → 无链接", () => {
    const cards = activityCards(PUBLISHED_PAYLOAD.activity_cards);
    expect(cards).toHaveLength(2);
    expect(cards[0].link).toBe("/c/8HXK38MQR2VW?entry=web");
    expect(cards[1].link).toBeNull();
    expect(cards[1].stateFace).toContain("结束");
  });

  it("非法码形/注入字符不进链接", () => {
    const cards = activityCards([{ card_id: "ac_3", title: "x", campaign_code: "../c/admin" }]);
    expect(cards[0].link).toBeNull();
  });

  it("缺 card_id 的卡片丢弃", () => {
    const cards = activityCards([{ title: "x", campaign_code: "8HXK38MQR2VW" }]);
    expect(cards).toHaveLength(0);
  });
});

// ---- 4590 offers 契约(草案;隔离 fixture 只做本地契约验证,待真实提供者联验) --

const OFFERS_FIXTURE = {
  schema_version: "offers.v1",
  store_code: "st_8HXK38MQR2VW",
  items: [
    { offer_id: "of_1", platform: "meituan", title: "双人咖啡券", price_note: "¥68", state: "available", product_url: "https://win.meituan.com/p/xyz" },
    { offer_id: "of_2", platform: "dianping", title: "下午茶", price_note: "", state: "unverified" },
    { offer_id: "of_3", platform: "douyin", title: "套餐 A", price_note: "¥99", state: "sold_out" },
    { offer_id: "of_4", platform: "meituan", title: "过期套餐", state: "expired" },
  ],
};

describe("offersView(4590 草案契约):售罄/失效停购买入口", () => {
  it("解析条目并保留平台/标题/价格说明", () => {
    const offers = offersView(OFFERS_FIXTURE)!;
    expect(offers).toHaveLength(4);
    expect(offers[0]).toMatchObject({ offerId: "of_1", platform: "meituan", title: "双人咖啡券", priceNote: "¥68" });
  });

  it("非对象/缺 items → null(整区不渲染)", () => {
    expect(offersView(null)).toBeNull();
    expect(offersView({})).toBeNull();
    expect(offersView({ items: "x" })).toBeNull();
  });

  it("入口判定:available→平台购买;unverified→到平台查看;sold_out/expired→停入口给替代", () => {
    const offers = offersView(OFFERS_FIXTURE)!;
    expect(offerEntry(offers[0])).toEqual({ kind: "platform", text: "到平台购买", href: "https://win.meituan.com/p/xyz" });
    expect(offerEntry(offers[1])).toEqual({ kind: "view", text: "到平台查看", href: null });
    expect(offerEntry(offers[2]).kind).toBe("stopped");
    expect(offerEntry(offers[3]).kind).toBe("stopped");
    expect(offerEntry(offers[2]).text).not.toContain("购买");
  });

  it("非 https 商品地址不渲染为链接", () => {
    const offers = offersView({ items: [{ offer_id: "of_9", platform: "meituan", title: "x", state: "available", product_url: "javascript:alert(1)" }] })!;
    expect(offerEntry(offers[0]).href).toBeNull();
  });
});
