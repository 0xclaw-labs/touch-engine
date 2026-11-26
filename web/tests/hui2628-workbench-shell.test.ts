import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { buildTodaySummaryView, todayObligations } from "@/lib/work/today-obligations";
import {
  buildWorkContextHref,
  canViewDiagnostics,
  isCurrentTenantPayload,
  readWorkReturnContext,
  resolveStoreEntry,
  roleDisplayLabel,
  sanitizeWorkReturnPath,
  storeWorkspaceEntryHref,
  switchableTenants,
  visibleWorkNav,
} from "@/lib/work/workbench-shell";
import { planToday } from "@/lib/product-finish";
import { pageLedger } from "@/lib/page-map";

const srcRoot = path.resolve(process.cwd(), "src");

function readSrc(rel: string): string {
  return readFileSync(path.join(srcRoot, rel), "utf8");
}

describe("HUI-2628 任务导向共享导航与角色门控", () => {
  it("主导航围绕今天、门店、推广活动、内容中心、经营结果，高频内容中心始终直达", () => {
    const ownerNav = visibleWorkNav({ role: "org_owner", serviceDesk: true });
    expect(ownerNav.primary.map((i) => i.label)).toEqual([
      "今天",
      "门店",
      "推广活动",
      "内容中心",
      "经营结果",
      "确认与核销",
    ]);
    expect(ownerNav.primary.find((i) => i.href === "/work/materials")?.label).toBe("内容中心");

    const deskOff = visibleWorkNav({ role: "org_owner", serviceDesk: false });
    expect(deskOff.primary.some((i) => i.href === "/work/service")).toBe(false);
  });

  it("账单、团队、代理按角色精准呈现，已发券/权益履约入口始终保留", () => {
    const owner = visibleWorkNav({ role: "org_owner", serviceDesk: true }).secondary.map((i) => i.href);
    expect(owner).toContain("/work/billing");
    expect(owner).toContain("/work/settings");
    expect(owner).toContain("/work/rewards");
    expect(owner).toContain("/work/leads");
    expect(owner).toContain("/work/touchpoints");
    expect(owner).not.toContain("/agency");

    const manager = visibleWorkNav({ role: "store_manager", serviceDesk: true }).secondary.map((i) => i.href);
    expect(manager).toContain("/work/settings");
    expect(manager).toContain("/work/rewards");
    expect(manager).toContain("/work/touchpoints");
    expect(manager).not.toContain("/work/billing");
    expect(manager).not.toContain("/agency");

    const staff = visibleWorkNav({ role: "staff", serviceDesk: true }).secondary.map((i) => i.href);
    expect(staff).toContain("/work/rewards");
    expect(staff).toContain("/work/leads");
    expect(staff).not.toContain("/work/billing");
    expect(staff).not.toContain("/work/settings");
    expect(staff).not.toContain("/work/touchpoints");
    expect(staff).not.toContain("/agency");

    const agent = visibleWorkNav({ role: "agent", serviceDesk: false }).secondary.map((i) => i.href);
    expect(agent).toContain("/agency");
    expect(agent).toContain("/work/billing");
    expect(agent).toContain("/work/settings");
    expect(agent).toContain("/work/rewards");
  });

  it("保留全部 36 条路由覆盖账本，不因导航收敛删除已有业务能力", () => {
    const routes = pageLedger().map((p) => p.route);
    expect(routes).toHaveLength(36);
    for (const required of [
      "/",
      "/work/stores",
      "/work/stores/[id]",
      "/work/stores/[id]/channels",
      "/work/stores/[id]/services",
      "/work/stores/[id]/page",
      "/work/campaigns",
      "/work/campaigns/new",
      "/work/campaigns/[id]",
      "/work/materials",
      "/work/rewards",
      "/work/service",
      "/work/analytics",
      "/work/touchpoints",
      "/work/leads",
      "/work/billing",
      "/work/settings",
      "/agency",
    ]) {
      expect(routes).toContain(required);
    }
  });
});

describe("HUI-2628 单店直达、多店受控切换与组织上下文隔离", () => {
  it("无门店给登记入口；单店自动直达本店工作区（含服务/入口/内容直链）；manage=1 保持列表管理", () => {
    expect(resolveStoreEntry([])).toEqual({
      mode: "empty",
      href: "/work/stores",
      label: "登记第一家门店",
    });

    const single = resolveStoreEntry([{ id: "sto_01", name: "静安旗舰店" }], { returnOrigin: "/" });
    expect(single.mode).toBe("auto_single");
    if (single.mode === "auto_single") {
      expect(single.href).toBe("/work/stores/sto_01?tab=page&return=%2F");
      expect(single.channelsHref).toBe("/work/stores/sto_01?tab=page&panel=channels&return=%2F");
      expect(single.entrancesHref).toBe("/work/stores/sto_01?tab=entrances&return=%2F");
      expect(single.contentHref).toBe("/work/materials?store=sto_01&return=%2F");
      expect(single.label).toContain("静安旗舰店");
    }

    const managed = resolveStoreEntry([{ id: "sto_01", name: "静安旗舰店" }], { manage: true });
    expect(managed.mode).toBe("multi_store");
  });

  it("多店走受控选择，不擅自跳入单店", () => {
    const multi = resolveStoreEntry([
      { id: "sto_01", name: "静安店" },
      { id: "sto_02", name: "徐汇店" },
    ]);
    expect(multi.mode).toBe("multi_store");
    if (multi.mode === "multi_store") {
      expect(multi.stores).toHaveLength(2);
      expect(multi.manageHref).toBe("/work/stores?manage=1");
    }
  });

  it("受控组织切换器只接受已启用 membership；迟到跨租户响应一律丢弃", () => {
    const scopes = [
      { tenant_id: "t_a", display_name: "甲餐厅", role: "org_owner", enabled: true, source: "membership" },
      { tenant_id: "t_b", display_name: "乙咖啡", role: "agent", enabled: true, source: "membership" },
      { tenant_id: "t_off", display_name: "停用户", role: "org_owner", enabled: false, source: "membership" },
      { tenant_id: "t_ext", display_name: "非成员", role: "staff", enabled: true, source: "delegation" },
    ];
    expect(switchableTenants(scopes, "t_a").map((s) => s.tenant_id)).toEqual(["t_a", "t_b"]);
    expect(switchableTenants([scopes[0]], "t_a")).toEqual([]);

    expect(isCurrentTenantPayload("t_a", "t_b")).toBe(false);
    expect(isCurrentTenantPayload("", "t_b")).toBe(false);
    expect(isCurrentTenantPayload("t_b", "t_b")).toBe(true);
  });

  it("TodayDesk、WorkLayout 与 StoresPage 均具备租户隔离键与迟到响应守卫", () => {
    const today = readSrc("components/work/today-desk.tsx");
    const layout = readSrc("app/work/layout.tsx");
    const stores = readSrc("app/work/stores/page.tsx");
    expect(today).toContain("<TodayBody key={session.tenantId} />");
    expect(today).toContain("isCurrentTenantPayload");
    expect(layout).toContain("<div key={session.tenantId}>{children}</div>");
    expect(stores).toContain("requestSeq");
    expect(stores).toContain("isCurrentTenantPayload");
  });
});

describe("HUI-2628 选材/制作/权益返回保留 (store, campaign, draft, return_origin)", () => {
  it("上下文链接构建与回读完整保留门店、活动、草稿与返回来源", () => {
    const href = buildWorkContextHref("/work/materials", {
      store: "sto_1",
      campaign: "cmp_9",
      draft: "drf_2",
      returnOrigin: "/work/campaigns/cmp_9#publish-config",
    });
    expect(href).toBe(
      "/work/materials?store=sto_1&campaign=cmp_9&draft=drf_2&return=%2Fwork%2Fcampaigns%2Fcmp_9%23publish-config",
    );

    const parsed = readWorkReturnContext(href.slice(href.indexOf("?")), "/work/stores");
    expect(parsed).toEqual({
      storeId: "sto_1",
      campaignId: "cmp_9",
      draftId: "drf_2",
      returnHref: "/work/campaigns/cmp_9#publish-config",
      hasExplicitReturn: true,
    });

    const storeHref = storeWorkspaceEntryHref("sto_1", {
      panel: "channels",
      returnOrigin: "/work/campaigns/cmp_9",
    });
    expect(storeHref).toBe("/work/stores/sto_1?tab=page&panel=channels&return=%2Fwork%2Fcampaigns%2Fcmp_9");
  });

  it("回跳校验阻断外域、协议相对、脚本协议、/api 和路径穿越", () => {
    expect(sanitizeWorkReturnPath("/")).toBe("/");
    expect(sanitizeWorkReturnPath("/work/stores/s1?tab=page&panel=channels")).toBe(
      "/work/stores/s1?tab=page&panel=channels",
    );
    expect(sanitizeWorkReturnPath("/work/campaigns/c1#publish-config")).toBe("/work/campaigns/c1#publish-config");
    expect(sanitizeWorkReturnPath("/agency")).toBe("/agency");

    for (const bad of [
      "https://evil.example/work/stores",
      "//evil.example/work/stores",
      "javascript:alert(1)",
      "/api/v1/stores",
      "/work/../admin",
      "/work/%2e%2e/admin",
      "/work\\stores",
      "/work/stores\r\nSet-Cookie:a=1",
      "/c/public-code",
      "",
    ]) {
      expect(sanitizeWorkReturnPath(bad), bad).toBeNull();
    }
  });

  it("内容中心、优惠库存、门店工作区与活动详情接线返回上下文", () => {
    expect(readSrc("app/work/materials/page.tsx")).toContain("readWorkReturnContext");
    expect(readSrc("app/work/materials/page.tsx")).toContain('data-action="return-origin"');
    expect(readSrc("app/work/rewards/page.tsx")).toContain("readWorkReturnContext");
    expect(readSrc("app/work/rewards/page.tsx")).toContain('data-action="return-origin"');
    expect(readSrc("components/work/store-workspace.tsx")).toContain("sanitizeWorkReturnPath");
    expect(readSrc("app/work/campaigns/[id]/page.tsx")).toContain("buildWorkContextHref");
  });
});

describe("HUI-2628 今天只列真实受权待办：正常终态与独立矩阵不冒充异常", () => {
  it("正常暂停、结束、过期活动以及未核销库存/停用动作/空内容引用绝不进入待办", () => {
    const todos = todayObligations(
      {
        pending_confirm: 0,
        rewards_issued: 12,
        rewards_redeemed: 5,
        todos: {
          billing_anomalies: 0,
          pending_redeem: 7,
          unavailable_actions: 4,
          missing_content: 6,
          action_auth_expired: 2,
          admission_expired: 1,
        },
      },
      {
        serviceDesk: true,
        owner: true,
        campaigns: [
          { id: "c_pause", title: "春季活动", status: "paused" },
          { id: "c_end", title: "周年庆", status: "ended" },
          { id: "c_exp", title: "暑期活动", status: "expired" },
        ],
      },
    );
    expect(todos).toEqual([]);

    const planned = planToday({
      stores: 1,
      active: [{ id: "c_exp", title: "暑期活动", ends_at: "2020-01-01T00:00:00Z" }],
      paused: [{ id: "c_pause", title: "春季活动" }],
      ended: [{ id: "c_end", title: "周年庆" }],
      drafts: [],
      materialGap: "has_material",
      pendingLeads: 0,
      rewardKnown: true,
      redemptionKnown: true,
    });
    expect(planned.every((item) => item.tone !== "now" && item.tone !== "paused" && item.tone !== "ended" && item.tone !== "expired")).toBe(true);
  });

  it("真实受权待办具备对象、当前状态、业务影响、主操作、目标深链与返回位置", () => {
    const todos = todayObligations(
      {
        pending_confirm: 3,
        todos: { billing_anomalies: 1 },
      },
      { serviceDesk: true, owner: true },
    );
    expect(todos).toHaveLength(2);
    for (const item of todos) {
      expect(item.object).toBeTruthy();
      expect(item.title).toBeTruthy();
      expect(item.status).toBeTruthy();
      expect(item.impact).toBeTruthy();
      expect(item.action).toBeTruthy();
      expect(item.href).toContain("return=/");
      expect(item.returnOrigin).toBe("/");
    }
    expect(todos.map((i) => i.id)).toEqual(["confirm", "billing"]);
  });

  it("独立商家矩阵（HUI-2390）绝不是顾客发布或门店上线前置，日常工作台零「记下矩阵草稿」噪音", () => {
    const ordinaryMerchantFiles = [
      "components/work/today-desk.tsx",
      "components/work/work-frame.tsx",
      "components/work/store-workspace.tsx",
      "app/work/stores/page.tsx",
      "app/work/materials/page.tsx",
      "app/work/campaigns/page.tsx",
      "app/work/campaigns/[id]/page.tsx",
    ];
    for (const rel of ordinaryMerchantFiles) {
      const src = readSrc(rel);
      expect(src, rel).not.toContain("记下矩阵草稿");
      expect(src, rel).not.toContain("MatrixDraftButton");
      expect(src, rel).not.toContain("TaskHandoffActions");
      expect(src, rel).not.toContain("matrix-draft");
    }
  });
});

describe("HUI-2628 经营摘要四态诚实呈现（loading / known_zero / partial_failure / not_connected）", () => {
  it("loading 态所有指标保持未知，绝不填 0", () => {
    const view = buildTodaySummaryView({ loading: true, ok: false, status: null, data: null });
    expect(view.state).toBe("loading");
    expect(view.canRetry).toBe(false);
    expect(view.metrics.every((m) => m.value === null && m.state === "unknown")).toBe(true);
  });

  it("not_connected（404 / feature_disabled）显式标识未接入，绝不冒充 0", () => {
    for (const input of [
      { loading: false, ok: false, status: 404, data: null },
      { loading: false, ok: false, status: 503, data: null, errorCode: "feature_disabled" },
    ]) {
      const view = buildTodaySummaryView(input);
      expect(view.state).toBe("not_connected");
      expect(view.canRetry).toBe(false);
      expect(view.metrics.every((m) => m.value === null && m.state === "unknown")).toBe(true);
      expect(view.detail).toContain("不显示 0");
    }
  });

  it("partial_failure（500 / 网络断连 / 部分字段缺失 / 门店读取失败）绝不把错误静默补 0", () => {
    const failed = buildTodaySummaryView({ loading: false, ok: false, status: 500, data: null });
    expect(failed.state).toBe("partial_failure");
    expect(failed.canRetry).toBe(true);
    expect(failed.metrics.every((m) => m.value === null && m.state === "unknown")).toBe(true);

    const partialFields = buildTodaySummaryView({
      loading: false,
      ok: true,
      status: 200,
      data: { pending_confirm: 2, rewards_issued: 5 },
    });
    expect(partialFields.state).toBe("partial_failure");
    expect(partialFields.canRetry).toBe(true);
    expect(partialFields.missingFields).toEqual(["rewards_redeemed"]);
    expect(partialFields.metrics.find((m) => m.key === "pending_confirm")).toMatchObject({ value: 2, state: "known" });
    expect(partialFields.metrics.find((m) => m.key === "rewards_issued")).toMatchObject({ value: 5, state: "known" });
    expect(partialFields.metrics.find((m) => m.key === "rewards_redeemed")).toMatchObject({ value: null, state: "unknown" });

    const storeFailed = buildTodaySummaryView({
      loading: false,
      ok: true,
      status: 200,
      data: { pending_confirm: 0, rewards_issued: 0, rewards_redeemed: 0 },
      storeLoadFailed: true,
    });
    expect(storeFailed.state).toBe("partial_failure");
    expect(storeFailed.missingFields).toEqual(["stores"]);
  });

  it("known_zero 与 ready 明确区分权威真实零值与正数事实", () => {
    const zero = buildTodaySummaryView({
      loading: false,
      ok: true,
      status: 200,
      data: { as_of: "2026-11-26T10:00:00Z", pending_confirm: 0, rewards_issued: 0, rewards_redeemed: 0 },
    });
    expect(zero.state).toBe("known_zero");
    expect(zero.canRetry).toBe(false);
    expect(zero.metrics.every((m) => m.value === 0 && m.state === "known")).toBe(true);

    const ready = buildTodaySummaryView({
      loading: false,
      ok: true,
      status: 200,
      data: { as_of: "2026-11-26T10:00:00Z", pending_confirm: 1, rewards_issued: 4, rewards_redeemed: 2 },
    });
    expect(ready.state).toBe("ready");
    expect(ready.metrics.find((m) => m.key === "rewards_issued")?.value).toBe(4);
    expect(ready.metrics.find((m) => m.key === "rewards_redeemed")?.value).toBe(2);
  });
});

describe("HUI-2628 普通商家界面无内部术语，排障信息收口至受权诊断", () => {
  it("受权诊断只对组织负责人与代管代理开放，角色文案全部中文化", () => {
    expect(canViewDiagnostics("org_owner")).toBe(true);
    expect(canViewDiagnostics("agent")).toBe(true);
    expect(canViewDiagnostics("store_manager")).toBe(false);
    expect(canViewDiagnostics("staff")).toBe(false);
    expect(canViewDiagnostics("")).toBe(false);

    expect(roleDisplayLabel("org_owner")).toBe("组织负责人");
    expect(roleDisplayLabel("store_manager")).toBe("门店经理");
    expect(roleDisplayLabel("staff")).toBe("店员");
    expect(roleDisplayLabel("agent")).toBe("代管代理");
  });

  it("TodayDesk 具备受权诊断折叠区且日常流程不再引导去 /admin 或暴露原始技术术语", () => {
    const today = readSrc("components/work/today-desk.tsx");
    expect(today).toContain('data-testid="authorized-diagnostics"');
    expect(today).toContain("diagnosticsAllowed");
    expect(today).not.toContain('href="/admin"');
    expect(today).not.toContain("按 UTC 当天统计");
    expect(today).not.toContain("asset_id");
    expect(today).not.toContain("task_handoff");

    const campaignDetail = readSrc("app/work/campaigns/[id]/page.tsx");
    expect(campaignDetail).not.toContain('href="/admin"');
    expect(campaignDetail).toContain("管理桌卡与碰触入口");

    const merchantWb = readSrc("components/admin/MerchantWorkbench.tsx");
    expect(merchantWb).not.toContain("已选素材 {item.asset_id}");
    expect(merchantWb).not.toContain("交接 ${plan.kind}");
    expect(merchantWb).not.toContain("${plan.nav_intent}");
  });

  it("保留必要知情确认（活动暂停/结束影响确认、公开前预览与发布、权限保护）", () => {
    expect(readSrc("app/work/campaigns/page.tsx")).toContain('aria-label="确认活动影响"');
    expect(readSrc("app/work/campaigns/[id]/page.tsx")).toContain('aria-label="确认活动影响"');
    expect(readSrc("components/work/store-workspace.tsx")).toContain("预览与发布");
  });
});
