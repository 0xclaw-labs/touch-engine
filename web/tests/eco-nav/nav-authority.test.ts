import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const read = (rel: string) => readFileSync(path.join(process.cwd(), rel), "utf8");

describe("HUI-2627/2628 nav authority discipline", () => {
  it("production nav loading never falls back to the preview fixture", () => {
    expect(read("src/app/api/eco-nav/route.ts")).not.toContain("provisionalEcoNav");
    expect(read("src/components/admin/AdminShell.tsx")).not.toContain("provisionalEcoNav");
    expect(read("src/lib/eco-nav/load.ts")).toContain("blockedUnavailableEcoNav");
  });

  it("the renderer never fetches, hardcodes a URL, or carries business ids", () => {
    const source = read("src/components/eco-nav/EcoTopNav.tsx");
    expect(source).not.toContain("fetch(");
    expect(source).not.toMatch(/https?:\/\//);
    expect(source).not.toContain("campaign_id");
    expect(source).not.toContain("task_handoff");
    expect(source).toContain("onSwitchApp");
  });

  it("the switch hook sends only the two contract ids", () => {
    const source = read("src/components/eco-nav/use-app-switch.ts");
    expect(source).toContain("app_id: appId");
    expect(source).toContain("launch_target_id: targetId");
    for (const forbidden of ["campaign_id", "asset_ref", "payer", "project_id"]) {
      expect(source).not.toContain(forbidden);
    }
  });

  it("availability route exposes target ids, not launch URLs", () => {
    const source = read("src/app/api/eco-nav/launches/route.ts");
    expect(source).toContain("targets");
    expect(source).not.toContain("https://");
    expect(source).toContain("Object.keys(registeredLaunchMap())");
  });
});
