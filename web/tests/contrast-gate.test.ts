import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
// @ts-expect-error - the deterministic gate is plain ESM, not a TS module
import { PAGE_CHECKS, RESOLVED_PATH, TOUCH_SURFACES, contrastRatio, evaluateContrast } from "../scripts/contrast-gate.mjs";

const resolved = JSON.parse(readFileSync(RESOLVED_PATH, "utf8")) as unknown;

describe("deterministic contrast gate", () => {
  it("computes WCAG ratios from actual hex values", () => {
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 5);
    expect(contrastRatio("#777777", "#ffffff")).toBeCloseTo(4.478, 2);
    expect(contrastRatio("#ffffff", "#ffffff")).toBeCloseTo(1, 5);
  });

  it("passes every pair the touched pages actually paint on all six touch surfaces", () => {
    const { rows, failures } = evaluateContrast(resolved);
    expect(rows.length).toBe(TOUCH_SURFACES.length * PAGE_CHECKS.reduce((n, c) => n + c.pairs.length, 0));
    expect(failures).toEqual([]);
  });

  it("would fail on a below-threshold pair (no fake PASS)", () => {
    const broken = structuredClone(resolved) as {
      tokens: Record<string, Record<string, Record<string, { type: string; value: string }>>>;
    };
    broken.tokens["work.light"].light["status.danger.fg"] = { type: "color", value: "#777777" };
    const { failures } = evaluateContrast(broken);
    expect(failures.some((row: { pair: string }) => row.pair === "status.danger.fg.on.bg.surface")).toBe(true);
  });
});

describe("touched CSS uses semantic roles, not ad-hoc colors", () => {
  it("has no bare hex colors in handwritten CSS", () => {
    for (const file of ["src/app/globals.css", "src/app/touch.css", "src/components/eco-nav/eco-top-nav.module.css"]) {
      const css = readFileSync(path.join(process.cwd(), file), "utf8");
      const hits = [...css.matchAll(/(?:^|[^A-Za-z0-9_])#(?:[0-9A-Fa-f]{6}|[0-9A-Fa-f]{3})(?![0-9A-Fa-f])/g)].map((m) => m[0]);
      expect(hits, file).toEqual([]);
    }
  });

  it("uses the semantic token variables in the customer and nav surfaces", () => {
    for (const file of ["src/app/touch.css", "src/components/eco-nav/eco-top-nav.module.css"]) {
      expect(readFileSync(path.join(process.cwd(), file), "utf8"), file).toContain("var(--pn-");
    }
  });
});
