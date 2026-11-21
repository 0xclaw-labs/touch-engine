"use client";

// 命令面板原语（shadcn Command 结构，零第三方运行时依赖）。
// 过滤逻辑由调用方完成（受控查询词）；本文件只提供输入、列表、分组、条目与空态结构。
// 不引入 cmdk / popover：依赖清单锁定，按需取 shadcn-admin 的结构母版。

import * as React from "react";
import { Search } from "lucide-react";

import { cn } from "@/lib/utils";

function Command({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="dialog"
      aria-label="命令面板"
      className={cn(
        "flex w-full flex-col overflow-hidden rounded-md border bg-background text-foreground shadow-md",
        className,
      )}
      {...props}
    />
  );
}

const CommandInput = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement>
>(function CommandInput({ className, ...props }, ref) {
  return (
    <div className="flex items-center gap-2 border-b px-3">
      <Search className="h-4 w-4 shrink-0 text-muted-foreground" />
      <input
        ref={ref}
        type="text"
        className={cn(
          "h-10 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground",
          className,
        )}
        {...props}
      />
    </div>
  );
});

function CommandList({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="listbox"
      className={cn("max-h-64 overflow-y-auto p-1", className)}
      {...props}
    />
  );
}

function CommandEmpty({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "px-3 py-6 text-center text-sm text-muted-foreground",
        className,
      )}
      {...props}
    />
  );
}

function CommandGroup({
  className,
  heading,
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & { heading?: string }) {
  return (
    <div className={cn("p-1", className)} {...props}>
      {heading ? (
        <p className="px-2 py-1.5 text-xs font-medium text-muted-foreground">
          {heading}
        </p>
      ) : null}
      {children}
    </div>
  );
}

function CommandItem({
  className,
  selected,
  onSelect,
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  selected?: boolean;
  onSelect?: () => void;
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={selected}
      onClick={onSelect}
      className={cn(
        "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-1 focus-visible:ring-ring",
        selected && "bg-accent",
        className,
      )}
      {...props}
    >
      {children}
    </button>
  );
}

export {
  Command,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
};
