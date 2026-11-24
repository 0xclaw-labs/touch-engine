"use client";

// 日期+时间复合件：Popover + Calendar(react-day-picker) + 复合件内显式时间槽。
// 值口径为本地墙钟 `YYYY-MM-DDTHH:mm`（与既有 toRFC3339/fromRFC3339 衔接），
// 不做 UTC 偏移；传入 `name` 时渲染 hidden input 承载提交值，对齐原生
// datetime-local 的 FormData 行为。时间槽保留原生 `type="time"` 作为复合件内
// 显式时间输入（D7 口径），label/aria/错误态齐备，不允许孤立裸 input。

import * as React from "react";
import { CalendarIcon, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  combineDateTime,
  formatDateTimeLabel,
  parseDateTimeValue,
} from "@/lib/date-time-value";

export interface DateTimeFieldProps {
  /** `YYYY-MM-DDTHH:mm` 或空串（未选）。 */
  value: string;
  onChange: (value: string) => void;
  /** 传入后渲染 `<input type="hidden" name>` 承载提交值。 */
  name?: string;
  id?: string;
  "aria-label"?: string;
  placeholder?: string;
  disabled?: boolean;
  /** `YYYY-MM-DDTHH:mm` 下界（含）。 */
  min?: string;
  /** `YYYY-MM-DDTHH:mm` 上界（含）。 */
  max?: string;
  /** 校验失败态：触发器与时间槽同步 aria-invalid。 */
  invalid?: boolean;
  /** 外部错误文案锚点 id，经 aria-describedby 关联。 */
  errorId?: string;
  className?: string;
}

function isDayDisabled(day: Date, min?: string, max?: string): boolean {
  const minDate = min ? parseDateTimeValue(min)?.date : null;
  const maxDate = max ? parseDateTimeValue(max)?.date : null;
  const dayStart = new Date(day.getFullYear(), day.getMonth(), day.getDate());
  if (minDate && dayStart < minDate) return true;
  if (maxDate && dayStart > maxDate) return true;
  return false;
}

export function DateTimeField({
  value,
  onChange,
  name,
  id,
  placeholder = "选择日期与时间",
  disabled,
  min,
  max,
  invalid,
  errorId,
  className,
}: DateTimeFieldProps) {
  const [open, setOpen] = React.useState(false);
  const parsed = parseDateTimeValue(value);
  const selected = parsed?.date ?? null;
  const timeText = parsed?.time ?? "00:00";
  const label = parsed ? formatDateTimeLabel(value) : placeholder;

  const handleSelect = (day: Date | undefined) => {
    if (!day) {
      onChange("");
      return;
    }
    onChange(combineDateTime(day, timeText));
  };

  const handleTimeChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const base = selected ?? new Date();
    onChange(combineDateTime(base, event.target.value));
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      {name ? <input type="hidden" name={name} value={value} /> : null}
      <PopoverTrigger asChild>
        <Button
          type="button"
          id={id}
          variant="outline"
          size="sm"
          disabled={disabled}
          aria-label={placeholder}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-invalid={invalid || undefined}
          aria-describedby={errorId}
          className="w-full justify-start gap-2 text-left font-normal"
        >
          <CalendarIcon className="h-4 w-4 shrink-0" />
          <span className="truncate">{label}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        sideOffset={4}
        collisionPadding={12}
        avoidCollisions
        className="w-auto p-0"
      >
        <div className="flex max-h-[calc(100dvh-1.5rem)] w-[min(280px,calc(100vw-24px))] flex-col gap-2 overflow-y-auto p-3">
          <Calendar
            mode="single"
            showOutsideDays
            defaultMonth={selected ?? new Date()}
            selected={selected ?? undefined}
            onSelect={handleSelect}
            disabled={(day) => isDayDisabled(day, min, max)}
            className="px-0"
          />
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            时间
            <input
              type="time"
              className="tk-input h-8 w-[120px]"
              value={timeText}
              aria-label="时间（北京时间 UTC+8）"
              aria-invalid={invalid || undefined}
              aria-describedby={errorId}
              onChange={handleTimeChange}
            />
          </label>
          <div className="flex justify-between gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={!value}
              onClick={() => onChange("")}
            >
              <X className="h-3.5 w-3.5" />
              清除
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)}>
              关闭
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
