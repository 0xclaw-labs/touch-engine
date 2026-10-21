// HUI-2628: local adoption map for the frozen page-pattern/v1 catalog. The
// definition describes obligations; it does not authorize a route or prove the
// rendered DOM. Only the four patterns this slice touched are adopted here.
import catalog from "@/contracts/page-patterns/v1/catalog.json";

export const ADOPTED_PATTERNS = ["workspace", "list", "creation", "public_consumer"] as const;
export type AdoptedPattern = (typeof ADOPTED_PATTERNS)[number];

interface RegionDefinition {
  id: string;
  purpose: string;
  required: boolean;
}
interface PatternDefinition {
  id: string;
  regions: RegionDefinition[];
  responsive: { mobile_density: string; desktop_density: string; mobile_below: number };
  navigation_audiences: string[];
}

const definitions = (catalog as { definitions: PatternDefinition[] }).definitions;

export function patternDefinition(id: AdoptedPattern): PatternDefinition {
  const found = definitions.find((item) => item.id === id);
  if (!found) throw new Error(`page-pattern ${id} missing from vendored catalog`);
  return found;
}

export function patternRegions(id: AdoptedPattern): string[] {
  return patternDefinition(id).regions.map((region) => region.id);
}

export const ROUTE_PATTERNS: ReadonlyArray<{ route: string; pattern: AdoptedPattern }> = [
  { route: "/", pattern: "workspace" },
  { route: "/work/campaigns", pattern: "list" },
  { route: "/work/campaigns/new", pattern: "creation" },
  { route: "/work/materials", pattern: "list" },
  { route: "/c/[code]", pattern: "public_consumer" },
  { route: "/c/[code]/contact", pattern: "public_consumer" },
];

export function routePattern(route: string): AdoptedPattern | null {
  return ROUTE_PATTERNS.find((item) => item.route === route)?.pattern ?? null;
}
