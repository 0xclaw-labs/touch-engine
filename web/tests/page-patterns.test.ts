import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { ADOPTED_PATTERNS, ROUTE_PATTERNS, patternDefinition, patternRegions, routePattern } from "../src/lib/page-patterns";
import { pageLedger } from "../src/lib/page-map";

const root = process.cwd();
const catalogPath = path.join(root, "src/contracts/page-patterns/v1/catalog.json");

describe("page-pattern/v1 adoption", () => {
  it("vendors the canonical catalog byte-identically", () => {
    const raw = readFileSync(catalogPath);
    expect(createHash("sha256").update(raw).digest("hex")).toBe(
      "fb8a96d19c348aed3ea3e0bd5a7d69fabce5ad37787cb23bb715df158c89bca6",
    );
  });

  it("adopts only the four touched patterns, each with all required regions", () => {
    for (const id of ADOPTED_PATTERNS) {
      const regs = patternRegions(id);
      for (const required of ["header", "body", "primary_action", "status", "empty", "loading", "error"]) {
        expect(regs, id).toContain(required);
      }
      expect(patternDefinition(id).responsive.mobile_density).toBe("touch");
    }
    expect(patternDefinition("public_consumer").navigation_audiences).toEqual(["customer"]);
  });

  it("maps touched routes to patterns that exist in the frozen catalog", () => {
    for (const { route, pattern } of ROUTE_PATTERNS) {
      expect(ADOPTED_PATTERNS).toContain(pattern);
      if (route !== "/work/materials") {
        expect(pageLedger().some((page) => page.route === route), route).toBe(true);
      }
    }
    expect(routePattern("/c/[code]")).toBe("public_consumer");
    expect(routePattern("/work/campaigns/new")).toBe("creation");
    expect(routePattern("/admin")).toBeNull();
    // Touched page roots declare the adopted pattern; region markers live in
    // the components that render them (creation wizard/list/public page).
    expect(readFileSync(path.join(root, "src/app/work/campaigns/page.tsx"), "utf8")).toContain('data-pattern="list"');
    expect(readFileSync(path.join(root, "src/app/work/campaigns/new/page.tsx"), "utf8")).toContain("CampaignCreateWizard");
    expect(readFileSync(path.join(root, "src/components/work/campaign-create-wizard.tsx"), "utf8")).toContain('data-pattern="creation"');
    expect(readFileSync(path.join(root, "src/app/c/[code]/public-campaign.tsx"), "utf8")).toContain('data-pattern="public_consumer"');
    expect(readFileSync(path.join(root, "src/app/page.tsx"), "utf8")).toContain('data-pattern="workspace"');
  });
});


