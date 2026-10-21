import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const src = readFileSync(path.join(process.cwd(), "src/components/admin/TaskHandoffActions.tsx"), "utf8");

describe("HUI-2628 matrix handoff", () => {
  it("calls the real matrix-draft route instead of the broken campaign-image path", () => {
    expect(src).toContain("/matrix-draft");
    expect(src).not.toContain("/api/eco-nav/campaign-image");
    expect(src).toContain("activity_version");
    expect(src).toContain("没有外发");
  });

  it("keeps one executable primary and makes the other two explanatory", () => {
    expect(src).toContain('const primary = kind === "make_campaign_image";');
    expect(src.match(/className=\{primary \? "tk-button" : "tk-quiet"\}/g)?.length).toBe(1);
    expect(src).toContain("data-primary-action={primary ? \"true\" : undefined}");
  });
});
