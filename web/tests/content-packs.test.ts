import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const materialsRel = "app/work/materials/page.tsx";
const panelRel = "components/work/content-pack-panel.tsx";
const detailRel = "app/work/campaigns/[id]/page.tsx";
const panelProductionRel = "components/work/store-production-panel.tsx";

describe("素材页统一内容编辑器", () => {
  const materials = read(materialsRel);
  const editor = read("components/work/store-content-drawer.tsx");
  it("当前门店与可选活动进入同一编辑器", () => {
    expect(materials).toContain("<StoreContentDrawer");
    expect(materials).toContain("campaignId={context.campaignId || undefined}");
    expect(materials).not.toContain("PackForm");
  });
  it("上传、已有、制作单选且共享一份标题正文", () => {
    expect(editor).toContain('type="radio"');
    for (const label of ["上传文件", "选择已有", "帮我制作"]) expect(editor).toContain(label);
    expect((editor.match(/name="title"/g) || []).length).toBe(1);
    expect((editor.match(/name="body"/g) || []).length).toBe(1);
    expect(editor).not.toMatch(/name="asset_id"|name="version"/);
  });
  it("版权、广告与AI标识随候选保存，采用与上线分开", () => {
    expect(editor).toContain("素材版权及使用授权");
    expect(editor).toContain("marking: { ad_required: true, ai_generated: aiGenerated }");
    expect(editor).toContain("adopted_pending_publish");
    expect(editor).toContain("线上是否使用，以原用途的发布版本为准");
  });
  it("制作能力失败只影响制作来源，允许获取权威报价与明确确认", () => {
    const production=read(panelProductionRel);
    expect(editor).toContain('<StoreProductionPanel');
    expect(production).toContain("content-production-handoffs/capabilities");
    expect(production).toContain('data-action="request-quote"');
    expect(production).toContain("确认费用并提交制作");
  });
  it("采用后回到同一门店或活动，不引入固定像素宽", () => {
    expect(editor).toContain("onAdopted?.({ storeId, campaignId, packId, versionId, platform");
    expect(materials).toContain("returnHref={returnHref}");
    expect(editor).not.toMatch(/width:\s*[34]\d\dpx/);
  });
});

describe("活动内容面板：候选→预览/编辑→明确采用", () => {
  const panel = read(panelRel);

  it("读取活动的包/版本/采用指针", () => {
    expect(panel).toMatch(/session\.api\(\s*"GET",\s*`campaigns\/\$\{campaignId\}\/content-packs`\s*\)/);
    expect(panel).toContain("versions");
    expect(panel).toContain("adoptions");
  });

  it("采用必须显式：按钮明示采用并带平台；blocked 显示商家语言原因", () => {
    expect(panel).toMatch(/versions\/\$\{[^}]+\}\/adopt/);
    expect(panel).toMatch(/data-action="adopt"/);
    expect(panel).toContain("采用这一版");
    expect(panel).toMatch(/data-field="blocked-reason"/);
    expect(panel).not.toMatch(/blocked_reason.*直接展示机器码/);
  });

  it("采用后展示同一版本的快照标识，不随机换片", () => {
    expect(panel).toMatch(/pack_version_id|version_no/);
    expect(panel).toContain("当前采用");
  });

  it("弱网重登可恢复：失败显示重试，不把失败当成功", () => {
    expect(panel).toMatch(/data-action="retry"/);
    expect(panel).toMatch(/data-state="error"/);
  });

  it("390/430 与桌面可操作：无固定像素宽，块级堆叠", () => {
    expect(panel).toMatch(/className="tk-section|className="tk-list/);
    expect(panel).not.toMatch(/width:\s*[34]\d\dpx/);
  });
});

describe("活动内容复用统一编辑器", () => {
 it("活动宿主不再重复挂载独立内容包面板", () => {
  const detail=read("app/work/campaigns/[id]/page.tsx");
  expect(detail).not.toContain("<ContentPackPanel");
  expect(read("components/work/campaign-config-panel.tsx")).toContain("<ContentEditor");
  expect(detail).toContain('id="touch"');
 });
});
