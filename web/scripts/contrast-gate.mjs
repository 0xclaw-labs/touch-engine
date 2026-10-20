#!/usr/bin/env node
// HUI-2628 deterministic contrast gate.
//
// It reads the vendored resolved.json and computes WCAG ratios itself from the
// actual token values used by the touched pages. It never copies the upstream
// "verdict" field as its own result. Text pairs require >=4.5, non-text >=3.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
export const RESOLVED_PATH = join(here, "..", "src", "styles", "generated", "painuo", "v1", "resolved.json");

export const TOUCH_SURFACES = [
  ["work.light", "light"],
  ["work.light", "dark"],
  ["public.consumer", "light"],
  ["public.consumer", "dark"],
  ["admin.compact", "light"],
  ["admin.compact", "dark"],
];

// Pairs the touched pages actually paint. Each row names the page family so a
// failure is actionable rather than an anonymous ratio.
export const PAGE_CHECKS = [
  {
    page: "merchant/campaign-create",
    pairs: [
      ["text.primary", "bg.page", 4.5],
      ["text.secondary", "bg.page", 4.5],
      ["text.muted", "bg.surface", 4.5],
      ["brand.text", "bg.surface", 4.5],
      ["brand.ink", "brand.accent", 4.5],
      ["status.warning.fg", "status.warning.weak", 4.5],
      ["status.danger.fg", "bg.surface", 4.5],
      ["status.success.fg", "bg.surface", 4.5],
      ["border.focus", "bg.surface", 3],
    ],
  },
  {
    page: "customer/public-campaign",
    pairs: [
      ["text.primary", "bg.surface", 4.5],
      ["text.secondary", "bg.surface", 4.5],
      ["text.muted", "bg.surface", 4.5],
      ["brand.text", "bg.surface", 4.5],
      ["brand.ink", "brand.accent", 4.5],
      ["status.warning.fg", "status.warning.weak", 4.5],
      ["status.danger.fg", "bg.surface", 4.5],
      ["status.success.fg", "bg.surface", 4.5],
      ["border.focus", "bg.surface", 3],
    ],
  },
  {
    page: "eco-top-nav",
    pairs: [
      ["text.primary", "bg.surface", 4.5],
      ["text.secondary", "bg.surface", 4.5],
      ["text.muted", "bg.surface", 4.5],
      ["brand.ink", "brand.accent", 4.5],
      ["border.focus", "bg.surface", 3],
    ],
  },
];

function channel(value) {
  const s = value / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

export function luminance(hex) {
  const raw = String(hex).replace("#", "");
  if (!/^[0-9a-fA-F]{6}$/.test(raw)) throw new Error(`not a six-digit hex color: ${hex}`);
  const r = parseInt(raw.slice(0, 2), 16);
  const g = parseInt(raw.slice(2, 4), 16);
  const b = parseInt(raw.slice(4, 6), 16);
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function contrastRatio(fg, bg) {
  const a = luminance(fg);
  const b = luminance(bg);
  const [hi, lo] = a >= b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

function tokenValue(resolved, surface, theme, role) {
  const leaf = resolved?.tokens?.[surface]?.[theme]?.[role];
  if (!leaf || leaf.type !== "color" || typeof leaf.value !== "string") {
    throw new Error(`missing color token ${surface}/${theme}/${role}`);
  }
  return leaf.value;
}

export function evaluateContrast(resolved, checks = PAGE_CHECKS, surfaces = TOUCH_SURFACES) {
  const rows = [];
  const failures = [];
  for (const [surface, theme] of surfaces) {
    for (const check of checks) {
      for (const [fgRole, bgRole, minRatio] of check.pairs) {
        const fg = tokenValue(resolved, surface, theme, fgRole);
        const bg = tokenValue(resolved, surface, theme, bgRole);
        const ratio = contrastRatio(fg, bg);
        const rounded = Math.floor(ratio * 1000) / 1000;
        const pass = rounded >= minRatio;
        const row = {
          page: check.page,
          surface,
          theme,
          pair: `${fgRole}.on.${bgRole}`,
          fg,
          bg,
          ratio: rounded,
          minRatio,
          pass,
        };
        rows.push(row);
        if (!pass) failures.push(row);
      }
    }
  }
  return { rows, failures };
}

function main() {
  const resolved = JSON.parse(readFileSync(RESOLVED_PATH, "utf8"));
  const { rows, failures } = evaluateContrast(resolved);
  console.log(JSON.stringify({ checked: rows.length, failed: failures.length, rows, failures }, null, 2));
  if (failures.length > 0) process.exitCode = 1;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
