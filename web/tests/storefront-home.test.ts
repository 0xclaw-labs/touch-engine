import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 C1 门店首页:源码级断言(node 无 DOM)。
// 红线:无 EcoTopNav/生态导航;不强制注册/留资/下载;未配置动作不渲染;
// disabled 门店收起动作区;活动卡可选(无码不深链);offers 端点不在基线时
// 整区不渲染(不伪建素材);网络失败给重试;390/430/桌面列模式。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const home = codeOf(src("app/s/[storeCode]/storefront-home.tsx"));
const page = codeOf(src("app/s/[storeCode]/page.tsx"));
const layout = codeOf(src("app/s/[storeCode]/layout.tsx"));
const viewLib = codeOf(src("lib/storefront-view.ts"));

describe("C1 布局:公共面无生态导航", () => {
  it("layout 用 public.consumer 面,不引 EcoTopNav/eco-nav", () => {
    expect(layout.includes('data-pn-surface="public.consumer"')).toBe(true);
    expect(layout.includes("EcoTopNav")).toBe(false);
    expect(layout.includes("eco-nav")).toBe(false);
  });

  it("首页组件零生态导航/零平台工具推销/零强制下载文案", () => {
    expect(home.includes("EcoTopNav")).toBe(false);
    expect(home.includes("随身搭子")).toBe(false);
    expect(home.includes("下载")).toBe(false);
    expect(home.includes("注册")).toBe(false);
    expect(home.includes("商家账号")).toBe(false);
  });
});

describe("C1 数据面:契约消费与诚实态", () => {
  it("走 parseStorefront/groupActions/storeStateFace/activityCards 唯一出口", () => {
    expect(home.includes("parseStorefront")).toBe(true);
    expect(home.includes("groupActions(")).toBe(true);
    expect(home.includes("storeStateFace")).toBe(true);
    expect(home.includes("activityCards(")).toBe(true);
  });

  it("加载/404/网络三态 + 重试按钮", () => {
    expect(home.includes("loading")).toBe(true);
    expect(home.includes("not_found")).toBe(true);
    expect(home.includes("network_error")).toBe(true);
    expect(home.includes("重试")).toBe(true);
  });

  it("disabled 门店:动作区收起只给门店提示(不冒充可用)", () => {
    expect(home.includes('data-testid="store-closed"')).toBe(true);
    expect(home.includes('data-testid="storefront-actions"')).toBe(true);
  });

  it("活动卡可选:无 campaign_code 不渲染深链(不伪造活动入口)", () => {
    expect(home.includes("card.link")).toBe(true);
    // C4 兼容:深链指向既有 /c/{code}(构造在 lib,组件只消费 link 字段)。
    expect(viewLib.includes("/c/${code}?entry=web")).toBe(true);
    // 无码卡只给「到店咨询」文案,不给死链。
    expect(home.includes("暂不支持在线参与")).toBe(true);
  });

  it("offers 读不到时整区不渲染(production 诚实;契约 fixture 归 storefront-view 测试)", () => {
    expect(home.includes("offers")).toBe(true);
    expect(home.includes("offersView")).toBe(true);
  });

  it("底部链到 C5 权益与 C6 隐私(地址化入口)", () => {
    expect(home.includes("benefits")).toBe(true);
    expect(home.includes("privacy")).toBe(true);
    expect(home.includes('data-testid="benefits-link"')).toBe(true);
    expect(home.includes('data-testid="privacy-link"')).toBe(true);
  });

  it("390/430/桌面列模式(columns 视口自适应)", () => {
    expect(home.includes("data-columns")).toBe(true);
    expect(home.includes("data-viewport")).toBe(true);
  });

  it("访客说明:浏览不建参与不留资(与 /c 同纪律)", () => {
    expect(home.includes("publicVisitorCopy") || home.includes("不会自动留下联系方式")).toBe(true);
  });

  it("页面路由只渲染门店首页(入口页)", () => {
    expect(page.includes("StorefrontHome")).toBe(true);
  });
});
