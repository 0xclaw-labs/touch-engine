"use client";

import { useRef, type KeyboardEvent, type ReactNode } from "react";

import styles from "./ui.module.css";

export type SegmentedItem = {
  key: string;
  label: ReactNode;
};

export type SegmentedProps = {
  items: SegmentedItem[];
  value: string;
  onChange: (key: string) => void;
  /** 屏幕阅读器需要知道这组标签在切换什么。 */
  ariaLabel: string;
  className?: string;
};

/**
 * 登录方式切换（goboost-fd Tabs 的 pill 单一变体裁剪）。
 * controlsPanel=false 语义：纯就地切换，无 TabPanel，因此不输出
 * aria-controls（指向不存在的 id 比不写更糟），也不再需要 tab id。
 */
export function Segmented({ items, value, onChange, ariaLabel, className }: SegmentedProps) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);

  const move = (from: number, step: number) => {
    const next = (from + step + items.length) % items.length;
    refs.current[next]?.focus();
    onChange(items[next].key);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    if (e.key === "ArrowRight") {
      e.preventDefault();
      move(index, 1);
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      move(index, -1);
    } else if (e.key === "Home") {
      e.preventDefault();
      move(-1, 1);
    } else if (e.key === "End") {
      e.preventDefault();
      move(0, -1);
    }
  };

  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      className={[styles.tabs, styles.pill, className].filter(Boolean).join(" ")}
    >
      {items.map((item, index) => {
        const active = item.key === value;
        return (
          <button
            key={item.key}
            ref={(el) => {
              refs.current[index] = el;
            }}
            type="button"
            role="tab"
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            className={[styles.tab, active && styles.active].filter(Boolean).join(" ")}
            onClick={() => onChange(item.key)}
            onKeyDown={(e) => onKeyDown(e, index)}
          >
            <span>{item.label}</span>
          </button>
        );
      })}
    </div>
  );
}
