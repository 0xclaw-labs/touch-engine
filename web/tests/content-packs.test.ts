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

describe("素材页三个入口，商家不填技术 ID", () => {
  const materials = read(materialsRel);

  it("提供上传已有内容/从素材库选择/帮我制作三个入口", () => {
    expect(materials).toContain("上传已有内容");
    expect(materials).toContain("从素材库选择");
    expect(materials).toContain("帮我制作");
    // 两个真实入口的表单 + 制作入口的 section,均有稳定标识
    expect(materials).toMatch(/entry="upload"/);
    expect(materials).toMatch(/entry="library"/);
    expect(materials).toMatch(/data-entry="produce"/);
  });

  it("不要求填写 asset_id/version 技术字段", () => {
    expect(materials).not.toContain('placeholder="平台 asset_id"');
    expect(materials).not.toMatch(/name="asset_id"/);
    expect(materials).not.toMatch(/name="version"/);
    // 版本选择走素材选择器(asset_id 由选择器内部携带,商家不输入)
    expect(materials).toMatch(/value={item\.id}/);
  });

  it("选择器展示来源/类型/授权声明与适用平台，预览含文案与门店事实", () => {
    expect(materials).toMatch(/item\.source/);
    expect(materials).toMatch(/item\.media_type/);
    expect(materials).toMatch(/item\.grant_ref/);
    expect(materials).toMatch(/适用平台/);
    expect(materials).toMatch(/name="title"/);
    expect(materials).toMatch(/name="body"/);
    expect(materials).toMatch(/name="store_name"/);
    expect(materials).toMatch(/name="price_note"/);
    expect(materials).toMatch(/name="valid_note"/);
    // 采用路径:候选提交后可明确采用
    expect(materials).toMatch(/versions\/\$\{[^}]+\}\/adopt/);
    expect(materials).toContain("采用");
  });

  it("帮我制作：未接通时显示原因、按钮不可用，不阻塞上传/选素材", () => {
    expect(materials).toMatch(/content-handoffs\/capabilities/);
    expect(materials).toMatch(/production_available/);
    // 禁用态按钮 + 原因文案区
    expect(materials).toMatch(/data-field="produce-reason"/);
    expect(materials).toMatch(/disabled=\{!/);
    // 两个真实入口的表单不因制作不可用而隐藏
    expect(materials).toMatch(/data-entry-panel=\{entry\}/);
    expect(materials).toMatch(/entry="upload"/);
    expect(materials).toMatch(/entry="library"/);
  });

  it("390/430 与桌面可操作：窄屏堆叠样式，无固定像素宽", () => {
    expect(materials).toMatch(/className="tk-section/);
    expect(materials).toMatch(/className="tk-form/);
    expect(materials).not.toMatch(/style=\{\{[^}]*width:\s*\d/);
    expect(materials).not.toMatch(/width:\s*[34]\d\dpx/);
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

describe("活动详情页只做最小窄补丁挂载内容面板", () => {
  const detail = read(detailRel);

  it("import 并挂载 ContentPackPanel，一处 section", () => {
    expect(detail).toContain('from "@/components/work/content-pack-panel"');
    const mounts = detail.match(/<ContentPackPanel/g) ?? [];
    expect(mounts.length).toBe(1);
    expect(detail).toMatch(/<ContentPackPanel campaignId=\{id\} \/?>/);
  });

  it("不动 HUI-2628 在改的既有区块（门店参数/碰一碰仍在）", () => {
    expect(detail).toContain('id="store-motion"');
    expect(detail).toContain('id="touch"');
  });
});
