import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { parseServices, type ServicesPayload } from "../src/lib/storefront-view";

// HUI-1896 C3 服务面板宿主(/s/{storeCode}/service/{actionId}):源码级断言 +
// 4589 services 草案契约本地验证(fixture 只进本测试,待真实提供者联验)。
// 红线:点评=打开正确平台门店自主评价,零模板/零 AIGC/零截图核验/无领奖暗示;
// Wi-Fi 密码只展示+复制,不强制留资后才看;入口信息读不到给诚实待办态
// (不死按钮);地址化直开可用(无 referer 依赖);无生态导航。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const view = codeOf(src("app/s/[storeCode]/service/[actionId]/service-view.tsx"));

describe("C3 宿主:按 actionId 复用面板,可地址化", () => {
  it("从 storefronts 找动作实例;找不到给入口不存在态", () => {
    expect(view.includes("parseStorefront")).toBe(true);
    expect(view.includes("not_found")).toBe(true);
    expect(view.includes("不存在")).toBe(true);
  });

  it("kind 分派面板:点评/关注/Wi-Fi/微信/团购各有承接区", () => {
    expect(view.includes("review")).toBe(true);
    expect(view.includes("follow")).toBe(true);
    expect(view.includes("wifi")).toBe(true);
    expect(view.includes("contact")).toBe(true);
    expect(view.includes("groupbuy")).toBe(true);
  });

  it("点评纪律:自主评价,零模板/零 AIGC/零截图核验/无领奖暗示", () => {
    expect(view.includes("自主")).toBe(true);
    expect(view.includes("模板")).toBe(false);
    expect(view.includes("AIGC")).toBe(false);
    expect(view.includes("截图核验")).toBe(false);
    expect(view.includes("领奖")).toBe(false);
    expect(view.includes("好评")).toBe(false);
  });

  it("3535 路线诚实渲染(detectEnvironment/pickRoute)", () => {
    expect(view.includes("detectEnvironment")).toBe(true);
    expect(view.includes("pickRoute")).toBe(true);
  });

  it("services 读不到时给「暂未开通在线展示,请向店员索取」待办态(不死按钮)", () => {
    expect(view.includes("暂未开通在线展示")).toBe(true);
    expect(view.includes("parseServices")).toBe(true);
  });

  it("Wi-Fi 密码只展示+复制;不强制留资后才看", () => {
    expect(view.includes("复制")).toBe(true);
    expect(view.includes("留资")).toBe(false);
    expect(view.includes("手机号")).toBe(false);
  });

  it("无生态导航;返回门店入口在场;390/430/桌面列模式", () => {
    expect(view.includes("EcoTopNav")).toBe(false);
    expect(view.includes("返回门店")).toBe(true);
    expect(view.includes("data-columns")).toBe(true);
    expect(view.includes("data-viewport")).toBe(true);
  });
});

// ---- 4589 services 草案契约(隔离 fixture;待真实提供者联验) --------------------

const SERVICES_FIXTURE: ServicesPayload = {
  schema_version: "services.v1",
  store_code: "st_8HXK38MQR2VW",
  items: [
    { action_id: "sa_a17", kind: "wifi", title: "门店 Wi-Fi", fields: { ssid: "Painuo-Cafe", password: "coffee2026", encryption: "WPA2" }, note: "连接后即可使用店内网络" },
    { action_id: "sa_a18", kind: "contact", title: "加微信", fields: { wechat_id: "painuo-cafe" }, note: "" },
    { action_id: "sa_a29", kind: "join_group", title: "会员群", fields: { group_number: "123456" }, note: "入群申请审核中时如实显示" },
  ],
};

describe("parseServices(4589 草案契约)", () => {
  it("按 action_id 解析 Wi-Fi/微信/社群字段", () => {
    const services = parseServices(SERVICES_FIXTURE);
    expect(services).not.toBeNull();
    const wifi = services?.get("sa_a17");
    expect(wifi?.field("ssid")).toBe("Painuo-Cafe");
    expect(wifi?.field("password")).toBe("coffee2026");
    const wechat = services?.get("sa_a18");
    expect(wechat?.field("wechat_id")).toBe("painuo-cafe");
    const group = services?.get("sa_a29");
    expect(group?.field("group_number")).toBe("123456");
  });

  it("非对象/缺 items → null(宿主给待办态)", () => {
    expect(parseServices(null)).toBeNull();
    expect(parseServices({})).toBeNull();
    expect(parseServices({ items: "x" })).toBeNull();
  });

  it("缺 action_id 的条目丢弃", () => {
    const services = parseServices({ schema_version: "services.v1", items: [{ kind: "wifi", fields: { ssid: "x" } }] });
    expect(services?.size ?? 0).toBe(0);
  });
});
