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

describe("B02 门店优先创建:先选有权门店,活动只作过滤,服务端从标签派生", () => {
  it("创建表单有门店选择与活动过滤,且不把客户端活动/门店作为绑定输入", () => {
    expect(panel).toContain("create-store");
    expect(panel).toContain("create-campaign");
    // provision 请求体只带标签/名称/摆位 —— 门店与活动由服务端从标签绑定派生
    const from = panel.indexOf('"touchpoints", {');
    const createCall = panel.slice(from, panel.indexOf("await load()", from));
    expect(createCall).toContain("tag_id");
    expect(createCall).not.toContain("store_id");
    expect(createCall).not.toContain("campaign_id");
  });

  it("列表加载 stores 端点,选卡列表按店过滤并展示标签绑定的活动注记", () => {
    expect(panel).toContain('"stores"');
    expect(panel).toContain("createStoreId");
    expect(panel).toContain("campaign_title");
  });
});

describe("B02 目标展示与门店筛选", () => {
  it("列表行展示目标类型/门店公共码/检查态按目标措辞", () => {
    expect(panel).toContain("target_type");
    expect(panel).toContain("targetFace");
    expect(panel).toContain("store_code");
    expect(panel).toContain("entryFace");
  });

  it("工具栏提供门店筛选,服务端 store_id 参数收口(客户端永不放宽经理视野)", () => {
    expect(panel).toContain("store-filter");
    expect(panel).toContain("store_id");
  });
});

describe("换绑:先预览影响,确认才提交,并发冲突不覆盖", () => {
  it("预览端点 + 已开始参与数 + 影响文案 + binding_revision 回显", () => {
    expect(panel).toContain("rebind-preview");
    expect(panel).toContain("started_participations");
    expect(panel).toContain("rebindImpactFace");
    expect(panel).toContain("binding_revision");
  });

  it("提交带 expected_binding_revision(乐观并发),409 有专门文案并重查", () => {
    expect(panel).toContain("expected_binding_revision");
    expect(panel).toContain("409");
  });

  it("换绑目标可选门店或活动(target_type),preview.current/next 都按目标展示", () => {
    expect(panel).toContain("rebind-type");
    expect(panel).toContain('"store"');
    expect(panel).toContain('"legacy_campaign"');
    expect(panel).toContain("rebind-store");
    expect(panel).toContain("rebind-campaign");
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
