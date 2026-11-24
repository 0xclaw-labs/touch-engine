// 本地墙钟日期/时间值的纯函数收口：复合件（Popover+Calendar+时间槽）与
// 单测共用。不做 UTC 偏移换算；严格拒绝月份/日期进位回绕。
// 值口径与既有 toRFC3339/fromRFC3339 契约衔接：
//   - datetime 槽：`YYYY-MM-DDTHH:mm`
//   - date 槽：`YYYY-MM-DD`

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DATETIME_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;
const TIME_RE = /^(\d{2}):(\d{2})$/;

export function pad2(value: number): string {
  return String(value).padStart(2, "0");
}

/** Local Date → `YYYY-MM-DD`; NaN → "". */
export function formatDateValue(date: Date): string {
  if (Number.isNaN(date.getTime())) return "";
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

/** Parse `YYYY-MM-DD` as a local date; reject malformed / out-of-range / rollover. */
export function parseDateValue(value: string): Date | null {
  const match = DATE_RE.exec(value.trim());
  if (!match) return null;
  const [, yearText, monthText, dayText] = match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const date = new Date(year, month - 1, day);
  if (Number.isNaN(date.getTime())) return null;
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day) {
    return null;
  }
  return date;
}

/** Parse `YYYY-MM-DDTHH:mm`; reject malformed / out-of-range / rollover. */
export function parseDateTimeValue(value: string): { date: Date; time: string } | null {
  const match = DATETIME_RE.exec(value.trim());
  if (!match) return null;
  const [, yearText, monthText, dayText, hourText, minuteText] = match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const date = new Date(year, month - 1, day, hour, minute);
  if (Number.isNaN(date.getTime())) return null;
  if (
    date.getFullYear() !== year ||
    date.getMonth() !== month - 1 ||
    date.getDate() !== day ||
    date.getHours() !== hour ||
    date.getMinutes() !== minute
  ) {
    return null;
  }
  return { date, time: `${pad2(hour)}:${pad2(minute)}` };
}

/** Combine a local date and `HH:mm` into `YYYY-MM-DDTHH:mm`. */
export function combineDateTime(date: Date, time: string): string {
  if (Number.isNaN(date.getTime())) return "";
  const timeMatch = TIME_RE.exec(time.trim());
  const hour = timeMatch ? timeMatch[1] : "00";
  const minute = timeMatch ? timeMatch[2] : "00";
  return `${formatDateValue(date)}T${hour}:${minute}`;
}

/** Trigger label for a datetime value; empty → placeholder. */
export function formatDateTimeLabel(value: string): string {
  const parsed = parseDateTimeValue(value);
  if (!parsed) return "";
  return `${formatDateValue(parsed.date)} ${parsed.time}`;
}

/** Trigger label for a date value; empty → placeholder. */
export function formatDateLabel(value: string): string {
  const parsed = parseDateValue(value);
  return parsed ? formatDateValue(parsed) : "";
}
