import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const pageRel = "app/work/campaigns/[id]/page.tsx";
const noteRel = "components/work/store-motion-note.tsx";

describe("商家活动页记下门店参数", () => {
  it("不启动浏览器，锁住按钮、价格和未成片声明", () => {
    const page = read(pageRel);
    const note = read(noteRel);

    expect(page).not.toContain('from "@/components/work/store-motion-note"');
    expect(page).not.toContain("<StoreMotionNote");
    expect(page).toContain("CampaignConfigPanel");
    expect(page).not.toContain("已生成");
    expect(page).not.toContain("已送出");
    expect(note).not.toContain("已生成");
    expect(note).not.toContain("已送出");
    expect(note).toContain("记下门店参数");
    expect(note).toContain("还没有记下门店参数。");
    expect(note).toContain("unchanged_store_note");
    expect(note).toContain("presentDeclaration(recorded.status)");
    expect(note).toContain('data-field="declaration-status"');
    expect(note).toContain("data-state={statusView.tone}");
    expect(note).not.toContain("{recorded.status}");
    expect(note).not.toContain("tk-ok");
    expect(note).toMatch(/session\.api\(\s*"GET",\s*`campaigns\/\$\{campaignId\}\/store-motion`\s*\)/);
    expect(note).toMatch(/session\.api\(\s*"PUT",\s*`campaigns\/\$\{campaignId\}\/store-motion`/);
    expect(note).toMatch(/price:\s*params\.price/);
    expect(note).not.toMatch(/price:\s*Number\(/);
    expect(note).not.toMatch(/amount_cents|price_cents|settlement/);

    const priceAt = note.indexOf("价格");
    expect(priceAt).toBeGreaterThan(-1);
    const priceBlock = note.slice(priceAt, priceAt + 500);
    expect(priceBlock).toContain('name="price"');
    expect(priceBlock).toContain('type="text"');
    expect(priceBlock).not.toContain('type="number"');
    expect(note).not.toContain('type="number"');

    const buttons = [...note.matchAll(/<button\b[\s\S]*?<\/button>/g)].map((match) => match[0]);
    expect(buttons).toEqual([expect.stringContaining("记下门店参数")]);
    expect(buttons.join("\n")).not.toMatch(/生成|送出|做视频/);

    const imports = note.match(/import[\s\S]*?from\s+["'][^"']+["']/g) ?? [];
    expect(imports.length).toBeGreaterThan(0);
    for (const line of imports) {
      expect(line, line).not.toMatch(/app\/c(?:\/|["'])/);
      expect(line, line).not.toMatch(/public-campaign|customer-publish/);
    }
  });
});
