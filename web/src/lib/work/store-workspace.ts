import type { CatalogEntry } from "@/lib/store-channels";

export type WorkspacePanel = "page" | "channels" | "services" | "content" | "deals";
export function workspacePanel(value: string | null): WorkspacePanel {
  return value === "channels" || value === "services" || value === "content" || value === "deals" ? value : "page";
}
export function workspaceHref(storeId: string, panel: WorkspacePanel | "entrances" = "page"): string {
  const base = `/work/stores/${encodeURIComponent(storeId)}`;
  return panel === "entrances" ? `${base}?tab=entrances` : panel === "page" ? `${base}?tab=page` : `${base}?tab=page&panel=${panel}`;
}
export function catalogChoices(catalog: CatalogEntry[], query: string): CatalogEntry[] {
  const needle = query.trim().toLocaleLowerCase();
  return catalog.filter(entry => `${entry.code} ${entry.label} ${entry.platform} ${entry.kind}`.toLocaleLowerCase().includes(needle));
}
