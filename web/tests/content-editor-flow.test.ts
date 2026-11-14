import { describe, expect, it } from "vitest";
import { ensureContentPack, quoteIsUsable, productionPayload, type ProductionQuote } from "@/lib/work/content-editor-flow";

describe("当前用途内容制作合同", () => {
  it("新店系统建空包并携带活动可选上下文，无需用户抄包引用", async () => {
    const calls: unknown[] = [];
    const api = async (...args: unknown[]) => { calls.push(args); return { ok: true, status: 201, data: { id: "pack-new" } }; };
    expect(await ensureContentPack(api, "store-a", [], "门店介绍")).toBe("pack-new");
    expect(calls).toEqual([["POST", "stores/store-a/content-packs", { title: "门店介绍" }]]);
    expect(await ensureContentPack(api, "store-a", [{ id: "old", title: "门店介绍" }], "门店介绍")).toBe("old");
    expect(calls).toHaveLength(1);
  });
  it("没有付款主体、交付内容、有效期或已过期时不能确认制作", () => {
    const q: ProductionQuote = { quote_ref: "q1", amount_minor: 8900, currency: "CNY", expires_at: "2026-11-14T13:00:00Z", payer: "付款组织", deliverables: "视频一条" };
    const now = Date.parse("2026-11-14T12:00:00Z");
    expect(quoteIsUsable(q, now)).toBe(true);
    expect(quoteIsUsable({ ...q, payer: "" }, now)).toBe(false);
    expect(quoteIsUsable({ ...q, expires_at: "2026-11-14T11:00:00Z" }, now)).toBe(false);
    expect(quoteIsUsable({ ...q, amount_minor: -1 }, now)).toBe(false);
    expect(quoteIsUsable({ ...q, amount_minor: 0 }, now)).toBe(true);
  });
  it("同名内容包只在同一活动或门店用途内自动复用", async () => {
    const calls: unknown[] = [];
    const api = async (...args: unknown[]) => { calls.push(args); return { ok: true, status: 201, data: { id: "new-purpose" } }; };
    const packs = [{ id: "other-campaign", title: "门店介绍", campaign_id: "campaign-a" }];
    expect(await ensureContentPack(api, "store-a", packs, "门店介绍", "campaign-b")).toBe("new-purpose");
    expect(calls).toEqual([["POST", "stores/store-a/content-packs", { title: "门店介绍（2）", campaign_id: "campaign-b" }]]);
    expect(await ensureContentPack(api, "store-a", packs, "门店介绍")).toBe("new-purpose");
    expect(await ensureContentPack(api, "store-a", packs, "门店介绍", "campaign-a")).toBe("other-campaign");
    expect(await ensureContentPack(api, "store-a", [...packs, { id: "created-b", title: "门店介绍（2）", campaign_id: "campaign-b" }], "门店介绍", "campaign-b")).toBe("created-b");
  });
  it("提交绑定用户确认的价格和过期时间，同一请求恢复复用完整负载", () => {
    const q: ProductionQuote = { quote_ref: "q1", amount_minor: 8900, currency: "CNY", expires_at: "2026-11-14T13:00:00Z", payer: "付款组织", deliverables: "视频一条" };
    const payload = productionPayload("pack-a", q, "request-same", "制作门店介绍");
    expect(JSON.parse(JSON.stringify(payload))).toEqual(payload);
    expect(payload).toMatchObject({ content_pack_ref: "pack:pack-a", quote_ref: "q1", accepted_amount_minor: 8900, accepted_currency: "CNY", accepted_expires_at: q.expires_at, idempotency_key: "request-same" });
  });
});
