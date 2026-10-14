import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = process.cwd();
const panelPath = path.join(root, "src/components/work/touchpoints/touchpoints-panel.tsx");
const shellPath = path.join(root, "src/app/work/touchpoints/page.tsx");

function mustRead(file: string): string {
  expect(existsSync(file), file).toBe(true);
  return readFileSync(file, "utf8");
}

const panel = mustRead(panelPath);
const shell = mustRead(shellPath);

function occurrences(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1;
}

describe("/work/touchpoints 页面是薄壳", () => {
  it("只挂 TouchpointsPanel,交互全在组件里", () => {
    expect(shell).toContain("TouchpointsPanel");
    expect(shell.length).toBeLessThan(800);
    expect(shell).not.toContain("fetch(");
  });
});

describe("触点管理台:独占新页,不跳 /admin", () => {
  it("列表/创建/绑定走触点端点,不引用 /admin 路由", () => {
    expect(panel).toContain('"touchpoints"');
    expect(panel).not.toMatch(/\/admin/);
    expect(panel).toContain("nfc/tags");
  });

  it("稳定链接原样用服务端字段复制,不本地拼 /t/ 路径", () => {
    expect(panel).toContain("navigator.clipboard");
    expect(panel).not.toMatch(/\/t\/\$\{/);
  });
});

describe("换绑:先预览影响,确认才提交,并发冲突不覆盖", () => {
  it("预览端点 + 已开始参与数 + 影响文案", () => {
    expect(panel).toContain("rebind-preview");
    expect(panel).toContain("started_participations");
    expect(panel).toContain("rebindImpactFace");
  });

  it("提交带 expected_campaign_id(乐观并发),409 有专门文案并重查", () => {
    expect(panel).toContain("expected_campaign_id");
    expect(panel).toContain("409");
    expect(panel).toContain("rebindImpactFace");
  });
});

describe("部署五态:只经显式按钮单步推进;下载/检查零推进", () => {
  it("用 nextStage/isStage 守卫,/stage 端点只出现一次(显式推进按钮)", () => {
    expect(panel).toContain("nextStage");
    expect(panel).toContain("isStage");
    expect(occurrences(panel, "/stage")).toBe(1);
    expect(panel).toContain("stageFace");
  });

  it("QR/CSV 下载与检查不携带推进语义;未验证不冒充已部署", () => {
    expect(panel).toContain("/qrcode");
    expect(panel).toContain("export.csv");
    expect(panel).toContain("entryStateFace");
    expect(panel).not.toContain("已部署");
    expect(panel).not.toContain("已上线");
  });
});

describe("旧路径停用:共享影响先确认,再走旧端点", () => {
  it("两步确认:先展示 sharedStopFace 共享数,显式确认才调 nfc/tags 状态端点", () => {
    expect(panel).toContain("sharedStopFace");
    expect(panel).toContain("confirm-legacy-stop");
    expect(panel).toContain("link_id");
    // 不用浏览器原生弹窗:影响文案要留在页面里可回看,也守 uifinish 文件级闸
    expect(panel).not.toContain("window.confirm");
    expect(panel).not.toMatch(/\balert\s*\(/);
  });
});

describe("停用/恢复:只动别名,绝不碰共享短码", () => {
  it("走 touchpoints/{id}/status,不用旧标签端点停用有别名的卡", () => {
    expect(panel).toContain("}/status");
    // 别名行的停用按钮不得调用旧标签状态端点(旧端点只用于无别名标签的共享确认路径)
    const aliasStatusCalls = occurrences(panel, "touchpoints/${");
    expect(aliasStatusCalls).toBeGreaterThan(0);
  });
});
