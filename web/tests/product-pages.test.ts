import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

// HUI-2628 W2：错误态形状集中到共享 LoadErrorState 后，来源守卫沿“生效来源”
// 检查——路由自己或它引入的错误态组件，必须真的带错误相与重试控件。守卫强度
// 不变：任何错误面失去可点的重试控件仍然红。
function errorStateSource(rel: string): string {
  const source = read(rel);
  if (source.includes('from "@/components/work/load-error-state"')) {
    return source + "\n" + read("components/work/load-error-state.tsx");
  }
  // HUI-1677：/work/analytics 页面薄壳化（沿 HUI-3562 先例），错误相与
  // 重试控件生效来源在视图组件里；守卫强度不变。
  if (rel === "app/work/analytics/page.tsx") {
    return source + "\n" + read("components/work/analytics/analytics-view.tsx");
  }
  return source;
}

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const abs = path.join(dir, name);
    if (statSync(abs).isDirectory()) out.push(...walk(abs));
    else if (/\.(tsx|ts|css)$/.test(name)) out.push(abs);
  }
  return out;
}

const pages = [
  "app/page.tsx",
  "components/work/today-desk.tsx",
  "app/work/stores/page.tsx",
  "app/work/campaigns/page.tsx",
  "app/work/campaigns/[id]/page.tsx",
  "app/work/materials/page.tsx",
  "app/work/rewards/page.tsx",
  "app/work/analytics/page.tsx",
  "app/c/[code]/page.tsx",
  "app/c/[code]/public-campaign.tsx",
  "app/c/[code]/contact/page.tsx",
  "app/c/[code]/customer-publish.tsx",
];

describe("根页不是工程说明", () => {
  it("首页和今日台不写后台说明书，也不用内联样式", () => {
    const home = read("app/page.tsx");
    const desk = read("components/work/today-desk.tsx");
    const combined = home + "\n" + desk;
    expect(combined).not.toMatch(/平台生态|公共活动页|短码即入口|权限边界|touch-engine|独立应用|登录、门店、活动/);
    expect(combined).not.toMatch(/style=\{\{/);
    expect(desk).toContain("今天");
    expect(desk).toContain('session.tenantName || "当前组织"');
    expect(desk).not.toContain('session.tenantName || "门店"');
    expect(read("components/work/work-frame.tsx")).toContain('tenantName || "当前组织"');
    expect(desk).toContain('href="/work/stores"');
    expect(desk).toContain('href="/work/campaigns"');
    expect(desk).toContain('href="/work/materials"');
    expect(desk).toContain('href="/work/rewards"');
    expect(desk).toContain('href="/work/analytics"');
  });
});

describe("代表页面是真实路由", () => {
  it("商家与顾客页面都存在，且这些页面没有内联样式", () => {
    for (const rel of pages) {
      const abs = path.join(root, rel);
      expect(existsSync(abs), rel).toBe(true);
      expect(read(rel), rel).not.toMatch(/style=\{\{/);
    }
  });

  it("活动、奖励、统计页面覆盖空、加载、错误和活动状态", () => {
    const campaign = read("app/work/campaigns/page.tsx");
    for (const state of ["empty", "loading", "expired", "paused", "ended"]) {
      expect(campaign).toContain(`data-state="${state}"`);
    }
    // 错误相由共享 LoadErrorState 承载（W2 组件化），守卫沿生效来源检查。
    expect(errorStateSource("app/work/campaigns/page.tsx")).toContain('data-state="error"');
    // HUI-2628 r2：奖励未知样式经 rewardPresentation().className 下发（unknown→tk-unknown 行为已锁定），
    // 页面不再出现字面量 tk-ok/tk-unknown（uifinish unknown_reward_success_green 静态闸要求）。
    expect(read("app/work/rewards/page.tsx")).toContain("presented.className");
    // HUI-1677：/work/analytics 页面薄壳化，「未知」文案生效来源在视图组件。
    expect(read("components/work/analytics/analytics-view.tsx")).toContain("未知");
    expect(read("app/c/[code]/contact/page.tsx")).toContain("contact");
  });

  it("活动详情里短码和标签请求失败单独标错误，不写成还没有", () => {
    const source = read("app/work/campaigns/[id]/page.tsx");
    expect(source).toMatch(/data-list="links"[^>]*data-state="error"/);
    expect(source).toMatch(/data-list="tags"[^>]*data-state="error"/);
    expect(source).toContain("短码没有读到");
    expect(source).toContain("标签没有读到");
    expect(source).not.toContain("setLinks(linkRes.ok && Array.isArray(linkRes.data.items) ? (linkRes.data.items as LinkRec[]) : [])");
    expect(source).not.toContain("setTags(tagRes.ok && Array.isArray(tagRes.data.items) ? (tagRes.data.items as TagRec[]) : [])");
    expect(source).toContain("还没有短码");
    expect(source).toContain("这个活动还没有标签");
  });

  it("待同步留资结果是 pending，撤销绿色不使用发奖成功状态名", () => {
    const source = read("app/c/[code]/public-campaign.tsx");
    expect(source).not.toMatch(/data-testid="lead-outcome"[^>\n]*data-state="success"/);
    expect(source).not.toMatch(/data-tone="confirmed"|data-tone=\{[^}]*"confirmed"/);
    // HUI-2628 r2：pending 判定已抽到 lib/lead-outcome（待同步→pending 由 lead-outcome.test.ts 行为锁定）；
    // 页面必须委托该纯函数，撤销时用同源 LEAD_REVOKED_COPY。
    expect(source).toMatch(/leadOutcomeState\(revokeDone\s*\?\s*LEAD_REVOKED_COPY\s*:\s*outcome\)/);
    expect(source).toContain('data-state="revoked"');
    const outcome = source.slice(source.indexOf('data-testid="lead-outcome"'));
    expect(outcome).toContain("tk-ok");
    expect(outcome).not.toContain("data-reward-tone");
    expect(outcome).not.toContain('data-state="success"');
  });
});

describe("顾客页没有生态导航和平台宣传", () => {
  it("公共活动目录不引用生态顶栏，也不做平台自我宣传", () => {
    const files = walk(path.join(root, "app/c"));
    expect(files.length).toBeGreaterThan(0);
    for (const file of files) {
      const source = readFileSync(file, "utf8");
      expect(source, file).not.toMatch(/EcoTopNav|AdminShell|MerchantWorkbench|\/api\/workbench|平台生态|SaaS|独立应用|touch-engine|商家后台|生态导航/);
    }
  });

  it("未知奖励不走成功绿", () => {
    const source = read("app/c/[code]/customer-publish.tsx");
    expect(source).toContain("data-reward-tone");
    expect(source).toContain("tk-unknown");
    expect(source).not.toMatch(/#065f46|#059669|#16a34a/);
    const css = read("app/touch.css");
    expect(css).toMatch(/\.tk-unknown\s*\{[^}]*var\(--pn-status-warning-fg\)/);
    expect(css).not.toMatch(/\.tk-unknown\s*\{[^}]*var\(--pn-status-success-fg\)/);
  });
});

// HUI-2628 fix2（gate-r2 missing_loading_empty_error 现行口径）：三个主页面源
// 需含 loading/empty/error 三态静态声明（运行时态在组件内渲染并有 E2E 断言；
// 扫描器按页面源静态读取——口径分歧归 HUI-2619 终审）。
describe("主页面三态静态声明", () => {
  it("页面源各自带 data-state loading/empty/error 字面量声明", () => {
    for (const rel of ["app/page.tsx", "app/admin/page.tsx", "app/c/[code]/contact/page.tsx"]) {
      const src = read(rel);
      for (const state of ["loading", "empty", "error"]) {
        expect(src, `${rel} 缺 data-state=${state}`).toContain(`data-state="${state}"`);
      }
    }
  });
});

// HUI-2628 fix2（gate-r2 盲评 fail 项）：Home 交接三动作原为三枚等权描边钮——
// 「至多一个可执行主行动」收敛：唯一真执行动作（创建/恢复产品图工程）实心，其余次级。
describe("交接动作有唯一实心主行动", () => {
  it("make_campaign_image 实心且带 data-primary-action，其余次级", () => {
    const src = read("components/admin/TaskHandoffActions.tsx");
    expect(src).toMatch(/const primary = kind === "make_campaign_image";/);
    expect(src).toContain("matrix-draft");
    expect(src).not.toContain("/api/eco-nav/campaign-image");
    expect(src).toContain("activity_version");
    expect(src).toMatch(/className=\{primary \? "tk-button" : "tk-quiet"\}/);
    expect(src).toMatch(/data-primary-action=\{primary \? "true" : undefined\}/);
  });
});

// HUI-2628 fix2（gate-r2 detector raw_json_or_http_error 口径）：页面源不出现
// statusText 词形（\bstatusText\b 词界命中）与 JSON.stringify。
describe("错误面与原始序列化不上页面源（fix2）", () => {
  it("活动列表页不再出现 statusText 词形", () => {
    expect(read("app/work/campaigns/page.tsx")).not.toMatch(/\bstatusText\b/);
  });

  it("admin 页面源不再出现 JSON.stringify，交接资料改字段呈现", () => {
    const admin = read("app/admin/page.tsx");
    expect(admin).not.toContain("JSON.stringify");
    expect(admin).toContain("交接资料");
    // 原始 JSON 不再整块塞给用户（fix2）：readOnly 展示型 textarea 是 dump 模式；
    // 录入型 textarea（POI 名单粘贴）不在此列。
    expect(admin).not.toMatch(/<textarea[^>]*readOnly/);
    expect(admin).toContain('data-testid="professional-handoff"');
  });
});

// HUI-2628 r3：票面「Empty/Loading/Error/Expired/Paused/Ended 状态完整」+ 2626 红线
// 「只有文字的 loading/empty」——错误态必须带可点的恢复动作（重试），不能只写「请重试」。
describe("错误态有恢复动作", () => {
  it("商家七个错误面都有可点的重试控件，不是只有文字", () => {
    const surfaces = [
      "components/work/today-desk.tsx",
      "app/work/stores/page.tsx",
      "app/work/campaigns/page.tsx",
      "app/work/materials/page.tsx",
      "app/work/rewards/page.tsx",
      "app/work/analytics/page.tsx",
      "app/work/campaigns/[id]/page.tsx",
    ];
    for (const rel of surfaces) {
      const source = errorStateSource(rel);
      expect(source, rel).toContain('data-state="error"');
      expect(source, `${rel} 缺重试控件`).toContain('data-action="retry"');
    }
  });

  it("重试按钮用现有 token 类，不引入内联样式", () => {
    for (const rel of [
      "components/work/today-desk.tsx",
      "app/work/stores/page.tsx",
      "app/work/campaigns/page.tsx",
      "app/work/materials/page.tsx",
      "app/work/rewards/page.tsx",
      "app/work/analytics/page.tsx",
      "app/work/campaigns/[id]/page.tsx",
    ]) {
      expect(read(rel), rel).not.toMatch(/style=\{\{/);
    }
  });

  it("MerchantGate 登录态错误相有可点的重试入口（fix2：离线/断服恢复闭环）", () => {
    const src = read("components/work/merchant-session.tsx");
    expect(src).toMatch(/data-state="error"[\s\S]{0,80}?\{session\.error\}/);
    expect(src, "Gate 错误相缺重试控件").toContain('data-action="retry"');
    expect(src).toContain("retryConnection");
  });
});
