import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import path from "node:path";
import { ecoNavEndpoint, ecoNavIdentityEnabled } from "@/lib/eco-nav/env";
import { loadEcoNavModel } from "@/lib/eco-nav/load";
import { billingBadgeText, payerText, planManualSwitch, switchableApps } from "@/lib/eco-nav/model";

const envSnapshot = {
  url: process.env.PUBLIC_AI_ECO_NAV_URL,
  identity: process.env.PUBLIC_AI_ECO_NAV_IDENTITY,
};

afterEach(() => {
  if (envSnapshot.url === undefined) delete process.env.PUBLIC_AI_ECO_NAV_URL;
  else process.env.PUBLIC_AI_ECO_NAV_URL = envSnapshot.url;
  if (envSnapshot.identity === undefined) delete process.env.PUBLIC_AI_ECO_NAV_IDENTITY;
  else process.env.PUBLIC_AI_ECO_NAV_IDENTITY = envSnapshot.identity;
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function readFixture(name: string): unknown {
  return JSON.parse(readFileSync(path.resolve(process.cwd(), "tests/eco-nav/fixtures", name), "utf8"));
}

describe("eco nav env", () => {
  it("缺省没有目录地址，也不启用公共身份", () => {
    delete process.env.PUBLIC_AI_ECO_NAV_URL;
    delete process.env.PUBLIC_AI_ECO_NAV_IDENTITY;
    expect(ecoNavEndpoint()).toBeNull();
    expect(ecoNavIdentityEnabled()).toBe(false);
  });
});

describe("loadEcoNavModel", () => {
  it("没有 URL 时返回 blocked，不渲染也不伪造品牌/租户", async () => {
    const fetchImpl = vi.fn();
    const model = await loadEcoNavModel({ endpoint: null, identityEnabled: false, fetchImpl });
    expect(fetchImpl).not.toHaveBeenCalled();
    expect(model.renderable).toBe(false);
    expect(model.brand).toBeNull();
    expect(model.apps).toEqual([]);
    expect(model.status.state).toBe("unavailable");
    expect(model.status.reasons).toContain("authority_unavailable");
    expect(payerText(model)).toBe("付款主体需确认");
    expect(billingBadgeText(model)).toBe("额度需确认");
  });

  it("网络失败或契约不对时返回 blocked，不换成预览夹具", async () => {
    const failed = await loadEcoNavModel({
      endpoint: "https://registry.invalid/eco-nav",
      identityEnabled: true,
      fetchImpl: vi.fn(async () => {
        throw new Error("down");
      }),
    });
    expect(failed.renderable).toBe(false);
    expect(failed.brand).toBeNull();
    expect(failed.status.state).toBe("unavailable");

    const bad = await loadEcoNavModel({
      endpoint: "https://registry.invalid/eco-nav",
      identityEnabled: true,
      fetchImpl: vi.fn(async () => jsonResponse({ schema_version: "eco-nav/v1", scopes: [], current_app: {} })),
    });
    expect(bad.renderable).toBe(false);
    expect(bad.brand).toBeNull();
    expect(bad.status.state).toBe("unavailable");
  });

  it("读到冻结文档但身份开关关闭时，来源仍是预览，且不生成跳转 URL", async () => {
    const model = await loadEcoNavModel({
      endpoint: "https://registry.invalid/eco-nav",
      identityEnabled: false,
      fetchImpl: vi.fn(async () => jsonResponse(readFixture("ready-first-party.json"))),
    });
    expect(model.provenance).toBe("provisional_fixture");
    expect(model.status.summary).toBe("目录已读取，身份上下文仍为预览");
    const orders = model.apps.find((item) => item.app_id === "orders");
    expect(orders?.launch_target_id).toBe("ti-orders-web");
    expect(orders ? planManualSwitch(orders, true).href : "missing").toBeNull();
    expect(billingBadgeText(model)).toBe("额度需确认");
  });

  it("身份开关打开且文档符合冻结契约时才标正式来源，并显示钱包金额", async () => {
    const model = await loadEcoNavModel({
      endpoint: "https://registry.invalid/eco-nav",
      identityEnabled: true,
      fetchImpl: vi.fn(async () => jsonResponse(readFixture("ready-first-party.json"))),
    });
    expect(model.provenance).toBe("public_ai_context");
    expect(model.document_app_id).toBe("goboost");
    expect(model.current_app.app_id).toBe("touch");
    expect(billingBadgeText(model)).toBe("钱包 CNY 128.00");
    expect(billingBadgeText(model)).not.toContain("订单");
    const orders = switchableApps(model.apps, model.current_app.app_id).find((item) => item.app_id === "orders");
    expect(orders ? planManualSwitch(orders, true).href : "missing").toBeNull();
  });

  it("denied 原件解析成功后不渲染，也不换成预览夹具", async () => {
    const model = await loadEcoNavModel({
      endpoint: "https://registry.invalid/eco-nav",
      identityEnabled: true,
      fetchImpl: vi.fn(async () => jsonResponse(readFixture("denied-unauthenticated.json"))),
    });
    expect(model.renderable).toBe(false);
    expect(model.brand).toBeNull();
    expect(model.apps).toEqual([]);
    expect(model.provenance).toBe("public_ai_context");
    expect(model.status.summary).toBe("");
  });
});

describe("预览夹具", () => {
  it("实现不写 http(s) 地址；上游原件保留品牌 URL", () => {
    const source = readFileSync(path.resolve(process.cwd(), "src/lib/eco-nav/fixture.ts"), "utf8");
    expect(source).not.toMatch(/https?:\/\//);
    const ready = readFileSync(path.resolve(process.cwd(), "tests/eco-nav/fixtures/ready-first-party.json"), "utf8");
    expect(ready).toContain("https://assets.example.com/brand-huigoo/logo.svg");
    expect(ready).toContain("https://help.example.com/");
  });
});
