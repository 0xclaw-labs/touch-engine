import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
  BILLING_UNCONFIRMED,
  NAV_INTENT_MANUAL_SWITCH,
  TOUCH_APP_ID,
  billingBadgeText,
  canLaunchManually,
  isRenderable,
  parseEcoNavDocument,
  partitionApps,
  payerText,
  planManualSwitch,
  selectTenant,
  switchableApps,
  toViewModel,
  type EcoNavDocument,
  type VisibleApp,
} from "@/lib/eco-nav/model";

const FIXTURES = [
  "ready-first-party.json",
  "selection-required.json",
  "degraded-delegation-billing-unavailable.json",
  "denied-unauthenticated.json",
] as const;

function readFixture(name: string): unknown {
  return JSON.parse(readFileSync(path.resolve(process.cwd(), "tests/eco-nav/fixtures", name), "utf8"));
}

function readyDocument(): EcoNavDocument {
  const parsed = parseEcoNavDocument(readFixture("ready-first-party.json"));
  if (!parsed.ok) throw new Error("ready fixture");
  return parsed.document;
}

function launchable(partial: Partial<VisibleApp> & Pick<VisibleApp, "app_id" | "display_name">): VisibleApp {
  return {
    icon_ref: null,
    state: "launchable",
    launch_mode: "sso_launch",
    launch_target_id: "ti-goboost-web",
    unavailable_reason: null,
    ...partial,
  } as VisibleApp;
}

describe("冻结夹具 eco-nav/v1 @ be6d7d3", () => {
  it("四份上游原件都能 parse", () => {
    for (const name of FIXTURES) {
      const parsed = parseEcoNavDocument(readFixture(name));
      expect(parsed.ok, name).toBe(true);
    }
  });

  it("ready 原件保留 brand_id、value_minor、entry 字符串和 role.label", () => {
    const parsed = parseEcoNavDocument(readFixture("ready-first-party.json"));
    if (!parsed.ok) throw new Error("ready");
    const doc = parsed.document;
    if (doc.status.state !== "ready") throw new Error("state");
    expect(doc.brand?.brand_id).toBe("brand-huigoo");
    expect(doc.app?.icon_ref).toBe("lucide:clapperboard");
    expect(doc.app?.home_target_id).toBe("ti-goboost-web");
    expect(doc.work_context?.state).toBe("resolved");
    expect(doc.work_context?.restored).toBe(true);
    expect(doc.role?.label).toBe("owner");
    expect(doc.role?.delegations).toEqual([]);
    expect(doc.billing?.amount).toEqual({ value_minor: 12800, unit: "cny_fen" });
    expect(doc.billing?.entry).toBe("billing_center");
    expect(doc.billing?.payer_source).toBe("personal");
    expect(isRenderable(doc)).toBe(true);
  });

  it("selection 的 role 与 billing 为空，denied 不渲染", () => {
    const selection = parseEcoNavDocument(readFixture("selection-required.json"));
    if (!selection.ok) throw new Error("selection");
    expect(selection.document.role).toBeNull();
    expect(selection.document.billing).toBeNull();
    expect(selection.document.return_context).toBeNull();
    expect(isRenderable(selection.document)).toBe(true);

    const denied = parseEcoNavDocument(readFixture("denied-unauthenticated.json"));
    if (!denied.ok) throw new Error("denied");
    expect(denied.document.brand).toBeNull();
    expect(denied.document.visible_apps).toEqual([]);
    expect(isRenderable(denied.document)).toBe(false);
    expect(toViewModel(denied.document, { provenance: "public_ai_context", statusSummary: "" }).renderable).toBe(false);
  });

  it("ready 里可启动的订单应用是 manual_switch，不创建 handoff，也不把 id 当成 URL", () => {
    const view = toViewModel(readyDocument(), { provenance: "public_ai_context", statusSummary: "" });
    const orders = view.apps.find((item) => item.app_id === "orders");
    if (!orders) throw new Error("orders");
    const plan = planManualSwitch(orders, view.capabilities.can_switch_app);
    expect(plan.nav_intent).toBe(NAV_INTENT_MANUAL_SWITCH);
    expect(plan.nav_intent).not.toBe("task_handoff");
    expect(plan.creates_handoff).toBe(false);
    expect(plan.launch_target_id).toBe("ti-orders-web");
    expect(plan.href).toBeNull();
    expect(plan.residual).toContain("不会伪造跳转地址");
  });

  it("正式 wallet 用 value_minor 显示分，预览态回到额度需确认", () => {
    const live = toViewModel(readyDocument(), { provenance: "public_ai_context", statusSummary: "" });
    expect(billingBadgeText(live)).toBe("钱包 CNY 128.00");
    expect(billingBadgeText(live)).not.toContain("订单");
    expect(payerText(live)).toBe("个人付款");

    const preview = toViewModel(readyDocument(), { provenance: "provisional_fixture", statusSummary: "预览" });
    expect(billingBadgeText(preview)).toBe(BILLING_UNCONFIRMED);

    const credits = toViewModel(readyDocument(), { provenance: "public_ai_context", statusSummary: "" });
    if (!credits.billing?.amount) throw new Error("amount");
    credits.billing = {
      ...credits.billing,
      kind: "customer_quota",
      amount: { value_minor: 12, unit: "quota_credit" },
    };
    expect(billingBadgeText(credits)).toBe("客户额度 12");
  });

  it("降级且额度未知时，即使标成正式身份也不展示金额", () => {
    const parsed = parseEcoNavDocument(readFixture("degraded-delegation-billing-unavailable.json"));
    if (!parsed.ok) throw new Error("degraded");
    const view = toViewModel(parsed.document, { provenance: "public_ai_context", statusSummary: "" });
    expect(view.billing?.known).toBe(false);
    expect(view.billing?.amount).toBeNull();
    expect(billingBadgeText(view)).toBe(BILLING_UNCONFIRMED);
    expect(payerText(view)).toBe("委托付款");
  });
});

describe("parseEcoNavDocument", () => {
  it("拒绝旧形状、错误 schema_version 和未知枚举", () => {
    expect(parseEcoNavDocument(null).ok).toBe(false);
    expect(parseEcoNavDocument({ schema_version: "eco-nav/v2" }).ok).toBe(false);
    expect(
      parseEcoNavDocument({
        schema_version: "eco-nav/v1",
        current_app: { display_name: "碰一碰" },
        scopes: [],
        provenance: "public_ai_context",
        billing_or_quota: { kind: "wallet" },
      }).ok,
    ).toBe(false);

    const raw = readFixture("ready-first-party.json") as {
      status: { state: string };
      billing: { entry: unknown };
      role: unknown;
    };
    raw.status.state = "provisional";
    expect(parseEcoNavDocument(raw).ok).toBe(false);
    const entry = readFixture("ready-first-party.json") as { billing: { entry: unknown } };
    entry.billing.entry = { entry_id: "billing_center" };
    expect(parseEcoNavDocument(entry).ok).toBe(false);
    const role = readFixture("ready-first-party.json") as { role: unknown };
    role.role = { summary: "owner" };
    expect(parseEcoNavDocument(role).ok).toBe(false);
  });

  it("多出来的订单分让整份文档失败，而不是剥掉后继续用", () => {
    const raw = readFixture("ready-first-party.json") as { billing: Record<string, unknown> };
    raw.billing.order_balance_cents = 12345;
    expect(parseEcoNavDocument(raw).ok).toBe(false);
    const amount = readFixture("ready-first-party.json") as { billing: { amount: Record<string, unknown> } };
    amount.billing.amount.order_balance_cents = 99999;
    expect(parseEcoNavDocument(amount).ok).toBe(false);
  });

  it("缺必填能力位失败；视图把壳内当前 App 钉成 touch，文档 app_id 保留", () => {
    const missing = readFixture("ready-first-party.json") as { capabilities: Record<string, unknown> };
    delete missing.capabilities.can_manage_members;
    expect(parseEcoNavDocument(missing).ok).toBe(false);

    const parsed = parseEcoNavDocument(readFixture("ready-first-party.json"));
    if (!parsed.ok || parsed.document.app === null || parsed.document.status.state !== "ready") {
      throw new Error("ready");
    }
    expect(parsed.document.app.app_id).toBe("goboost");
    const view = toViewModel(parsed.document, { provenance: "provisional_fixture", statusSummary: "" });
    expect(view.current_app.app_id).toBe(TOUCH_APP_ID);
    expect(view.document_app_id).toBe("goboost");
    expect(view.current_app.display_name).toBe("GoBoost");
  });
});

describe("selectTenant", () => {
  it("切换后不保留上一租户的付款主体和返回条，额度不再沿用原 scope", () => {
    const view = toViewModel(readyDocument(), { provenance: "public_ai_context", statusSummary: "" });
    const next = selectTenant(view, "tnt-studio-b");
    expect(next.active_tenant_id).toBe("tnt-studio-b");
    expect(payerText(next)).toBe("付款主体需确认");
    expect(next.role_label).toBe("");
    expect(next.return_context).toBeNull();
    expect(billingBadgeText(next)).toBe(BILLING_UNCONFIRMED);
    expect(next.apps.map((item) => item.app_id)).toEqual(view.apps.map((item) => item.app_id));
    expect(next.delegations).toEqual([]);
    expect(next.seat_source).toBeNull();

    const back = selectTenant(next, "tnt-studio-a");
    expect(payerText(back)).toBe("个人付款");
    expect(billingBadgeText(back)).toBe("钱包 CNY 128.00");
    expect(back.return_context).toBeNull();
    expect(back.seat_source).toBe("membership");
    expect(back.delegations).toEqual([]);
  });

  it("无权或未知租户时不改变当前 scope", () => {
    const view = toViewModel(readyDocument(), { provenance: "public_ai_context", statusSummary: "" });
    const locked = {
      ...view,
      capabilities: { ...view.capabilities, can_switch_tenant: false },
    };
    expect(selectTenant(locked, "tnt-studio-b").active_tenant_id).toBe("tnt-studio-a");
    expect(selectTenant(view, "tenant-missing").active_tenant_id).toBe("tnt-studio-a");
  });
});

describe("planManualSwitch", () => {
  it("允许列表解析后才有 href；id 本身即使像 URL 也不会被拿去跳转", () => {
    const target = launchable({ app_id: "goboost", display_name: "GoBoost", launch_target_id: "https://evil.example/go" });
    expect(planManualSwitch(target, true).href).toBeNull();
    const resolved = planManualSwitch(target, true, () => "https://127.0.0.1/go");
    expect(resolved.href).toBe("https://127.0.0.1/go");
    expect(resolved.href).not.toContain("evil.example");
    const allowed = planManualSwitch(launchable({ app_id: "goboost", display_name: "GoBoost" }), true, (id) =>
      id === "ti-goboost-web" ? "https://127.0.0.1/apps/goboost" : null,
    );
    expect(allowed.href).toBe("https://127.0.0.1/apps/goboost");
    expect(allowed.creates_handoff).toBe(false);
    expect(allowed.nav_intent).toBe(NAV_INTENT_MANUAL_SWITCH);
  });

  it("订单载荷、非可启动状态、none 和无权都没有 href", () => {
    const resolve = () => "https://127.0.0.1/apps/goboost?order_id=9";
    expect(planManualSwitch(launchable({ app_id: "goboost", display_name: "GoBoost" }), true, resolve).href).toBeNull();
    expect(
      planManualSwitch(
        launchable({
          app_id: "touch",
          display_name: "碰一碰",
          state: "entitlement_required",
          launch_mode: "none",
          launch_target_id: null,
          unavailable_reason: "entitlement_missing",
        }),
        true,
        () => "https://127.0.0.1/touch",
      ).href,
    ).toBeNull();
    expect(
      planManualSwitch(
        launchable({
          app_id: "goboost",
          display_name: "GoBoost",
          state: "current",
          launch_mode: "none",
          launch_target_id: null,
          unavailable_reason: null,
        }),
        true,
        () => "https://127.0.0.1/go",
      ).href,
    ).toBeNull();
    expect(
      planManualSwitch(launchable({ app_id: "goboost", display_name: "GoBoost" }), false, () => "https://127.0.0.1/go").href,
    ).toBeNull();
  });
});

describe("partitionApps", () => {
  const apps = ["aa", "bb", "cc", "dd", "ee"].map((id) => launchable({ app_id: id, display_name: id }));

  it("宽屏最多钉住 4 个，其余进溢出；窄屏全部进溢出", () => {
    expect(partitionApps(apps, "wide").pinned).toHaveLength(4);
    expect(partitionApps(apps, "wide").overflow.map((item) => item.app_id)).toEqual(["ee"]);
    expect(partitionApps(apps, "narrow").pinned).toHaveLength(0);
    expect(partitionApps(apps, "narrow").overflow).toHaveLength(5);
  });

  it("switchableApps 去掉当前 App", () => {
    const listed = switchableApps(
      [
        launchable({
          app_id: TOUCH_APP_ID,
          display_name: "碰一碰",
          state: "current",
          launch_mode: "none",
          launch_target_id: null,
          unavailable_reason: null,
        }),
        launchable({ app_id: "goboost", display_name: "GoBoost" }),
      ],
      TOUCH_APP_ID,
    );
    expect(listed.map((item) => item.app_id)).toEqual(["goboost"]);
  });
});

describe("HUI-2628 launchable resolution", () => {
  it("requires a registered target and never yields a URL", () => {
    const app = launchable({ app_id: "goboost", display_name: "GoBoost" });
    expect(canLaunchManually(app, true, () => false)).toBe(false);
    expect(canLaunchManually(app, true, () => true)).toBe(true);
    expect(canLaunchManually(app, false, () => true)).toBe(false);
    const ent = launchable({ app_id: "x", display_name: "X", state: "entitlement_required" } as never);
    expect(canLaunchManually(ent, true, () => true)).toBe(false);
  });
});
