import { describe, expect, it } from "vitest";
import { emittable, resolveRegisteredLaunch } from "@/lib/eco-nav/allowlist";
import { planManualSwitch, type VisibleApp } from "@/lib/eco-nav/model";
import { acceptCampaignImage, campaignHandoffDocument } from "@/lib/eco-nav/campaign-image";

const productImage: VisibleApp = {
  app_id: "product-image",
  display_name: "产品图",
  icon_ref: null,
  state: "launchable",
  launch_mode: "sso_launch",
  launch_target_id: "ti-product-image-web",
  unavailable_reason: null,
};

describe("已登记允许列表", () => {
  it("产品图的 .invalid 登记地址不输出", () => {
    expect(emittable("https://product-image.example.invalid/launch")).toBe(false);
    expect(resolveRegisteredLaunch("ti-product-image-web", {})).toBeNull();
  });

  // HUI-2227 残差②：登记真源=public-ai production-manifest.json（生产实况）。
  // 生产表没有的行诚实缺失（null），不再引用 PROVISIONAL .invalid 样例。
  it("登记真源是生产注册表：aicut/dhuman 真实地址可解析，样例行缺失", () => {
    expect(resolveRegisteredLaunch("ti-aicut-web", {})).toBe("https://aicut.modelxing.com/");
    expect(resolveRegisteredLaunch("ti-dhuman-web", {})).toBe("https://dhuman.modelxing.com/");
    expect(resolveRegisteredLaunch("ti-orders-web", {})).toBeNull();
    expect(resolveRegisteredLaunch("ti-touch-engine-web", {})).toBeNull();
    expect(resolveRegisteredLaunch("ti-leads-engine-web", {})).toBeNull();
    expect(resolveRegisteredLaunch("ti-agentbuddy-web", {})).toBe("https://agentbuddy.modelxing.com/");
    // PROVISIONAL 样例（.example.invalid）永不出现在允许列表
    expect(JSON.stringify(resolveRegisteredLaunch("ti-touch-engine-web", {})) ?? "").not.toContain("invalid");
  });

  it("GoBoost 只返回登记清单里的地址", () => {
    expect(resolveRegisteredLaunch("ti-goboost-web", {})).toBe("https://goboost.modelxing.com/projects");
  });

  it("测试品牌只能把产品图指到本机，不能指到公网", () => {
    const env = {
      TOUCH_ECO_NAV_TEST_BRAND: "1",
      TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL: "http://127.0.0.1:18220/healthz",
    };
    expect(resolveRegisteredLaunch("ti-product-image-web", env)).toBe("http://127.0.0.1:18220/healthz");
    expect(
      resolveRegisteredLaunch("ti-product-image-web", {
        TOUCH_ECO_NAV_TEST_BRAND: "1",
        TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL: "https://evil.example/launch",
      }),
    ).toBeNull();
    expect(
      resolveRegisteredLaunch("ti-goboost-web", {
        TOUCH_ECO_NAV_TEST_BRAND: "1",
        TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL: "http://127.0.0.1:18220/healthz",
      }),
    ).toBe("https://goboost.modelxing.com/projects");
  });

  it("手工切产品图使用允许地址，且不带 campaign_id", () => {
    const href = "http://127.0.0.1:18220/healthz";
    const plan = planManualSwitch(productImage, true, () => href);
    expect(plan.href).toBe(href);
    expect(plan.creates_handoff).toBe(false);
    expect(plan.href).not.toContain("campaign_id");
  });
});

describe("制作活动图交给产品图", () => {
  it("交接文档指向产品图，并且不带付费 scope", () => {
    const doc = campaignHandoffDocument("cmp_preview", new Date("2026-09-27T12:00:00Z"));
    expect(doc.target_app).toBe("product-image-engine");
    expect(doc.source_app).toBe("touch-engine");
    expect(doc).not.toHaveProperty("order_ref");
    expect((doc.scopes as string[]).join(",")).not.toMatch(/bill|pay|charge/);
    const profile = doc.source_profile as { campaign_ref: string; tenant_scope: string };
    expect(profile.campaign_ref).toBe("cmp_preview");
    expect(profile.tenant_scope).toBe("tnt_hui2222");
  });

  it("第一次创建工程，第二次恢复同一工程，且不调用计费", async () => {
    const calls: string[] = [];
    const result = await acceptCampaignImage(
      "cmp_preview",
      {
        TOUCH_ECO_NAV_PREVIEW: "1",
        TOUCH_ECO_NAV_TEST_BRAND: "1",
        TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL: "http://127.0.0.1:18220/healthz",
        PRODUCT_INTERNAL_TOKEN: "internal",
        TEST_IDENTITY_MINT_URL: "http://127.0.0.1:18222/test/mint",
      },
      async (url) => {
        calls.push(url);
        if (url.endsWith("/test/mint")) {
          return new Response(JSON.stringify({ access_token: "tok" }), { status: 200 });
        }
        return new Response(JSON.stringify({ resolution: "created", project: { id: "proj_1" } }), { status: 200 });
      },
    );
    expect(result).toEqual({ resolution: "created", project_id: "proj_1", charges_customer: false });
    expect(calls.some((url) => url.includes("billing"))).toBe(false);

    const again = await acceptCampaignImage(
      "cmp_preview",
      {
        TOUCH_ECO_NAV_PREVIEW: "1",
        TOUCH_ECO_NAV_TEST_BRAND: "1",
        TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL: "http://127.0.0.1:18220/healthz",
        PRODUCT_INTERNAL_TOKEN: "internal",
        TEST_IDENTITY_MINT_URL: "http://127.0.0.1:18222/test/mint",
      },
      async (url) => {
        if (url.endsWith("/test/mint")) {
          return new Response(JSON.stringify({ access_token: "tok" }), { status: 200 });
        }
        return new Response(JSON.stringify({ resolution: "restored", project: { id: "proj_1" } }), { status: 200 });
      },
    );
    expect(again.resolution).toBe("restored");
    expect(again.project_id).toBe("proj_1");
  });

  it("没有测试配置时拒绝，不假装已经创建工程", async () => {
    await expect(acceptCampaignImage("cmp_preview", {}, fetch)).rejects.toThrow(/跨应用交接暂未开放/);
  });
});
