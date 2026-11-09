import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  admissionLine,
  contactSteps,
  eventNotice,
  panelKind,
  reviewNotices,
  safeQrURL,
  wifiFallbackSteps,
  type ServiceEntry,
} from "../src/lib/store-service-panels";

// HUI-4589 顾客承接面板（T12 Wi-Fi 异常回退 / T13 微信与关注）。
// 红线：点评零模板零AIGC零截图零奖励（UI 第二道拦）；一切面板零成功话术；
// Wi-Fi 无留资门 + 失败回手动；二维码只渲染公网 https。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const component = codeOf(src("components/customer/StoreServicePanels.tsx"));

describe("面板复用入口与渲染门", () => {
  it("未知 kind / 缺 service_id 不渲染（不出现死面板）", () => {
    expect(panelKind({ service_id: "sa_x", kind: "review" })).toBe("review");
    expect(panelKind({ service_id: "sv_x", kind: "unknown_thing" })).toBe("");
    expect(panelKind({ kind: "wifi" })).toBe("");
  });
});

describe("T12 Wi-Fi：展示+复制+失败回手动；无留资门", () => {
  it("密码仅展示：面板渲染密码字段（无任何留资/会话前置文案）", () => {
    expect(component.includes('data-field="wifi-password"')).toBe(true);
    expect(component.includes('data-field="wifi-ssid"')).toBe(true);
    expect(component.includes("data-action=\"copy-wifi-password\"")).toBe(true);
    expect(component.includes("留资")).toBe(false);
    expect(component.includes("手机号")).toBe(false);
  });

  it("连不上 → 手动指引回退（wifi-fallback 展开步骤）", () => {
    expect(component.includes("data-action=\"wifi-fallback\"")).toBe(true);
    expect(component.includes('data-testid="wifi-fallback-steps"')).toBe(true);
    const steps = wifiFallbackSteps();
    expect(steps.some((s) => s.includes("删除该网络"))).toBe(true);
    expect(steps.some((s) => s.includes("询问店员"))).toBe(true);
  });

  it("剪贴板失败不谎报已复制", () => {
    expect(component.includes("无法自动复制")).toBe(true);
  });
});

describe("T13 微信与关注", () => {
  it("微信按环境给识别步骤；企微链接原样打开", () => {
    expect(contactSteps("ios_wechat").some((s) => s.includes("长按"))).toBe(true);
    expect(contactSteps("desktop_browser").some((s) => s.includes("截图"))).toBe(true);
    expect(contactSteps("target_app").length).toBeGreaterThan(0);
    expect(component.includes('data-action="open-wecom-contact"')).toBe(true);
    expect(component.includes('data-action="copy-wechat-id"')).toBe(true);
    expect(component.includes('data-field="wechat-id"')).toBe(true);
  });

  it("关注面板：主页链接 + 点击≠已关注；无「已关注」状态显示", () => {
    expect(component.includes('data-action="open-follow-page"')).toBe(true);
    expect(component.includes("不记录也不显示「已关注」")).toBe(true);
    const lib = codeOf(src("lib/store-service-panels.ts"));
    expect(lib.includes("未安装该平台")).toBe(true);
  });

  it("社群面板：审批备注原样展示 + 点击≠已加群", () => {
    expect(component.includes('data-field="approval-note"')).toBe(true);
    expect(component.includes("点击不代表已加群")).toBe(true);
  });
});

describe("点评面板：零模板零AIGC零截图零奖励（UI 第二道拦）", () => {
  it("面板无输入框 textarea，无生成按钮，无截图上传", () => {
    expect(component.includes("<textarea")).toBe(false);
    expect(component.includes("生成")).toBe(false);
    expect(component.includes("截图上传")).toBe(false);
    expect(component.includes('type="file"')).toBe(false);
  });

  it("四不声明文案齐全（模板/生成/截图/奖励解耦）", () => {
    const notices = reviewNotices().join("\n");
    expect(notices).toContain("没有模板");
    expect(notices).toContain("自动生成");
    expect(notices).toContain("截图");
    expect(notices).toContain("与奖励完全无关");
  });

  it("点评面板无任何「已点评」状态字段", () => {
    expect(component.includes("已点评成功")).toBe(false);
    expect(component.includes("review-submitted")).toBe(false);
  });
});

describe("零成功话术与诚实回执", () => {
  it("事件回执永远带「不代表」否定", () => {
    expect(eventNotice("open")).toContain("不代表");
    expect(eventNotice("copy")).toContain("不代表已连接");
    expect(eventNotice("jump_attempt")).toContain("不代表");
  });

  it("admission 诚实翻译：pending/blocked/unknown 都不承诺", () => {
    expect(admissionLine({ admission: "promised" })).toContain("由你自己完成");
    expect(admissionLine({ admission: "pending_external" })).toContain("不能承诺");
    expect(admissionLine({ admission: "blocked" })).toContain("未开放");
    expect(admissionLine({ admission: "unknown" })).toContain("暂无判定依据");
  });
});

describe("二维码引用安全形状", () => {
  it("只渲染公网 https", () => {
    expect(safeQrURL("https://cdn.example.com/qr.png")).toBe("https://cdn.example.com/qr.png");
    expect(safeQrURL("http://cdn.example.com/qr.png")).toBe("");
    expect(safeQrURL("https://localhost/qr.png")).toBe("");
    expect(safeQrURL("javascript:alert(1)")).toBe("");
  });
  it("img 渲染前必经 safeQrURL", () => {
    expect(component.includes("safeQrURL(entry.qr_url")).toBe(true);
    expect(component.includes("safeQrURL(")).toBe(true);
  });
});

describe("事件上报（fire-and-forget，失败静默）", () => {
  it("上报经 /services/events 端点，catch 静默不阻断", () => {
    expect(component.includes("/services/events")).toBe(true);
    expect(component.includes(".catch(() => {")).toBe(true);
    expect(component.includes("data-action=\"open-platform-store\"")).toBe(true);
  });
});
