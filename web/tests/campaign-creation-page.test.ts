import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = process.cwd();
const read = (rel: string) => readFileSync(path.join(root, rel), "utf8");

describe("HUI-2628 creation route", () => {
  it("is a real creation route with the three states declared", () => {
    const page = read("src/app/work/campaigns/new/page.tsx");
    expect(page).toContain("CampaignCreateWizard");
    for (const state of ["loading", "empty", "error"]) {
      expect(page).toContain(`data-state="${state}"`);
    }
  });

  it("list page points at the wizard and no longer hosts the form wall", () => {
    const list = read("src/app/work/campaigns/page.tsx");
    expect(list).toContain('href="/work/campaigns/new"');
    expect(list).toContain('data-primary-action="true"');
    expect(list).not.toContain("新建草稿");
    expect(list).not.toContain("RFC3339");
    expect(list).not.toContain("<select");
  });

  it("wizard keeps a local draft, uses local datetime inputs, and has one primary per step", () => {
    const wizard = read("src/components/work/campaign-create-wizard.tsx");
    expect(wizard).toContain("draftStorageKey");
    expect(wizard).toContain("serializeDraft");
    expect(wizard).toContain("parseDraft");
    expect(wizard).toContain('type="datetime-local"');
    expect(wizard).not.toMatch(/placeholder="[^"]*RFC3339/);
    expect(wizard.match(/data-primary-action="true"/g)?.length).toBe(3);
    expect(wizard).toContain("生成链接并发布");
    expect(wizard).toContain("hydrated.current");
    expect(wizard).toContain("async function copyShare()");
    expect(wizard).toContain("保存草稿并继续");
  });

  it("wizard validates before creating and never silently publishes", () => {
    const wizard = read("src/components/work/campaign-create-wizard.tsx");
    expect(wizard).toContain("draftErrors(draft)");
    expect(wizard).toContain('status: "active"');
    expect(wizard).toContain("这一步只保存草稿");
    expect(wizard).toContain("正在发布…");
  });

  it("detail page uses the share panel and a human status", () => {
    const detail = read("src/app/work/campaigns/[id]/page.tsx");
    expect(detail).toContain("<CampaignShare");
    expect(detail).toContain("草稿（未发布）");
    expect(detail).toContain("data-list=\"links\" data-state=\"error\"");
    expect(detail).toContain("data-primary-action=\"true\"");
    // draft publish and paused resume are mutually exclusive states; the share
    // panel owns the active-state copy action.
    expect(detail.match(/data-primary-action="true"/g)?.length).toBe(2);
    const share = read("src/components/work/campaign-share.tsx");
    expect(share).toContain('const primary = campaign.status === "active"');
  });
});
