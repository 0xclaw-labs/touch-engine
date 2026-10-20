// HUI-2628/2222: guest-facing campaign windows are human time, not ISO wire
// timestamps. Formatting is explicit about locale/time zone; invalid input is
// never rendered as a fake date.
export interface FormatWindowOptions {
  locale?: string;
  timeZone?: string;
}

function parse(value: string | undefined | null): Date | null {
  if (!value) return null;
  const time = new Date(value);
  return Number.isFinite(time.getTime()) ? time : null;
}

export function formatCampaignWindow(
  startsAt?: string | null,
  endsAt?: string | null,
  options: FormatWindowOptions = {},
): string {
  const start = parse(startsAt);
  const end = parse(endsAt);
  if (!start && !end) return "活动时间以门店现场说明为准";
  const formatter = new Intl.DateTimeFormat(options.locale ?? "zh-CN", {
    dateStyle: "medium",
    timeStyle: "short",
    ...(options.timeZone ? { timeZone: options.timeZone } : {}),
  });
  if (start && end) return `${formatter.format(start)} 至 ${formatter.format(end)}`;
  if (start) return `${formatter.format(start)} 起`;
  return `至 ${formatter.format(end as Date)}`;
}
