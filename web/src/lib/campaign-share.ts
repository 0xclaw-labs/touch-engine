import { formatCampaignWindow } from "./public-time";

export interface ShareLink {
  code: string;
  enabled: boolean;
}

export interface ShareCampaign {
  title: string;
  store_name?: string;
  starts_at?: string | null;
  ends_at?: string | null;
}

export function campaignShareUrl(origin: string, code: string): string {
  return `${origin.replace(/\/+$/, "")}/c/${encodeURIComponent(code)}`;
}

export function campaignShareText(input: ShareCampaign & { url: string }): string {
  const lines = [`【${input.title || "门店活动"}】`];
  if (input.store_name) lines.push(input.store_name);
  if (input.starts_at || input.ends_at) lines.push(formatCampaignWindow(input.starts_at, input.ends_at));
  lines.push(`活动入口：${input.url}`);
  return lines.join("\n");
}

export function pickShareLink(links: ShareLink[]): ShareLink | null {
  return links.find((link) => link.enabled) ?? null;
}

export interface ShareAvailability {
  canShare: boolean;
  note: string;
}

export function shareAvailability(input: {
  status: string;
  ends_at?: string | null;
  link: ShareLink | null;
  now?: number;
}): ShareAvailability {
  if (!input.link) return { canShare: false, note: "还没有启用的短码。先生成或启用一条短码，才能分享。" };
  if (input.status === "ended") return { canShare: true, note: "活动已结束。打开链接会显示“活动已结束”，不会显示进行中。" };
  if (input.status === "paused") return { canShare: true, note: "活动已暂停。打开链接会显示“暂停中”，恢复后才可参与。" };
  const end = input.ends_at ? Date.parse(input.ends_at) : NaN;
  if (Number.isFinite(end) && end < (input.now ?? Date.now())) {
    return { canShare: true, note: "活动窗口已经过去。打开链接会显示“活动已结束”，不会显示进行中。" };
  }
  if (input.status === "draft") return { canShare: true, note: "活动还是草稿。顾客打开会看到“活动未发布”，发布后才可参与。" };
  return { canShare: true, note: "" };
}
