"use client";

// 单日期复合件：Popover + Calendar(react-day-picker)。
// 值口径为本地墙钟 `YYYY-MM-DD`，不做 UTC 偏移；传入 `name` 时渲染 hidden input
// 承载提交值，对齐原生 `<input type="date">` 的 FormData 行为。

import * as React from "react";
import { CalendarIcon, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { formatDateLabel, formatDateValue, parseDateValue } from "@/lib/date-time-value";

export interface DateFieldProps {
  /** `YYYY-MM-DD` 或空串（未选）。 */
  value: string;
  onChange: (value: string) => void;
  /** 传入后渲染 `<input type="hidden" name>` 承载提交值。 */
  name?: string;
  id?: string;
  "aria-label"?: string;
  placeholder?: string;
  disabled?: boolean;
  /** `YYYY-MM-DD` 下界（含）。 */
  min?: string;
  /** `YYYY-MM-DD` 上界（含）。 */
  max?: string;
  invalid?: boolean;
  errorId?: string;
  className?: string;
}

function isDayDisabled(day: Date, min?: string, max?: string): boolean {
  const minDate = min ? parseDateValue(min) : null;
  const maxDate = max ? parseDateValue(max) : null;
  const dayStart = new Date(day.getFullYear(), day.getMonth(), day.getDate());
  if (minDate && dayStart < minDate) return true;
  if (maxDate && dayStart > maxDate) return true;
  return false;
}

export function DateField({
  value,
  onChange,
  name,
  id,
  placeholder = "选择日期",
  disabled,
  min,
  max,
  invalid,
  errorId,
  className,
}: DateFieldProps) {
  const [open, setOpen] = React.useState(false);
  const selected = parseDateValue(value);
  const label = selected ? formatDateLabel(value) : placeholder;

  const handleSelect = (day: Date | undefined) => {
    onChange(day ? formatDateValue(day) : "");
    setOpen(false);
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
